// Package cursor provides an ACP client for Cursor CLI via its
// `agent acp` subprocess (https://cursor.com/docs/cli/acp).
package cursor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
	client "github.com/dimetron/pi-go/internal/acp/client"
)

// rpcTimeout caps Initialize and NewSession against a hung subprocess. See
// claudecode for the rationale; 60s is enough for a normal startup but short
// enough to surface a missing API key or missing binary quickly.
const rpcTimeout = 60 * time.Second

// BinaryName is the preferred name of the Cursor CLI binary.
// Cursor's documentation calls the binary `agent`; we prefer the
// disambiguated `cursor-agent` name and fall back to `agent`.
const BinaryName = "cursor-agent"

// ACPSubcommand is the subcommand that puts Cursor CLI into ACP mode.
const ACPSubcommand = "acp"

// DefaultBinaryPaths lists common installation locations for the Cursor
// CLI binary. The Cursor installer places the binary at ~/.local/bin/agent;
// we also look for a disambiguated `cursor-agent` in the same locations.
var DefaultBinaryPaths = []string{
	"cursor-agent",
	"agent",
	".local/bin/cursor-agent",
	".local/bin/agent",
	"/usr/local/bin/cursor-agent",
	"/usr/local/bin/agent",
	"/usr/bin/cursor-agent",
	"/usr/bin/agent",
}

// Runner launches Cursor CLI in ACP mode and manages the client-side
// connection. Authentication is taken from the environment
// (CURSOR_API_KEY / CURSOR_AUTH_TOKEN) unless explicitly overridden.
type Runner struct {
	// ClientInfo identifies this client to the agent.
	ClientInfo acp.Implementation

	// Binary is the path to the cursor-agent (or `agent`) binary.
	// If empty, uses the first found in DefaultBinaryPaths.
	Binary string

	// Logger for connection debugging.
	Logger *slog.Logger

	// ExtraEnv is additional environment variables to pass to the subprocess.
	ExtraEnv []string
}

// RunRequest describes a Cursor CLI prompt turn.
type RunRequest struct {
	Prompt    string   // Prompt to send to Cursor CLI
	SessionID string   // Optional session ID to resume
	CWD       string   // Working directory
	Env       []string // Additional environment variables
	Command   []string // Optional command override for testing (defaults to cursor-agent acp)

	// Cursor-specific options
	APIKey    string // Optional; if set, passed as --api-key. Prefer CURSOR_API_KEY env var.
	AuthToken string // Optional; if set, passed as --auth-token. Prefer CURSOR_AUTH_TOKEN env var.
	Endpoint  string // Optional API endpoint override (e.g. https://api2.cursor.sh).
	Model     string // Optional model hint (informational; Cursor picks per-task).
	Debug     bool   // Enable verbose/debug output if supported by the CLI.
}

// RunningSession represents one in-flight Cursor CLI prompt turn.
type RunningSession struct {
	cmd    *exec.Cmd
	stdin  io.Closer
	stderr *stderrBuffer
	conn   *acp.ClientSideConnection

	events chan shared.Event
	done   chan struct{}
	// stderrDone closes once streamStderr returns so waitProcess can
	// synchronize with the drain goroutine before reading s.stderr. Without
	// it, slow-scheduled goroutines (observed on CI) may leave the buffer
	// empty even after the subprocess has written and exited.
	stderrDone chan struct{}

	closeStdin sync.Once

	mu         sync.Mutex
	result     shared.RunResult
	finished   bool
	toolFilter *shared.ToolCallTitleFilter
	curSession string
}

// Events returns the translated local ACP event stream.
func (s *RunningSession) Events() <-chan shared.Event { return s.events }

// Done is closed when the prompt turn finishes.
func (s *RunningSession) Done() <-chan struct{} { return s.done }

// Cancel terminates the running subprocess.
func (s *RunningSession) Cancel() error {
	s.mu.Lock()
	finished := s.finished
	cmd := s.cmd
	s.mu.Unlock()
	if finished || cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill %s subprocess: %w", BinaryName, err)
	}
	return nil
}

// Wait blocks until the turn completes and returns the final result.
func (s *RunningSession) Wait() shared.RunResult {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// envACPCursorCmd is the environment variable that overrides the Cursor ACP command.
// Format: "binary arg1 arg2 ..." or just "binary" (args default to "acp").
const envACPCursorCmd = "PI_ACP_CURSOR_CMD"

// Start launches the Cursor CLI subprocess and begins the ACP flow.
func (r Runner) Start(ctx context.Context, req RunRequest) (*RunningSession, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	binary := r.Binary
	var cmdArgs []string

	// Check for env var override before switch
	envCmd := os.Getenv(envACPCursorCmd)
	switch {
	case binary != "":
		found, err := findBinary([]string{binary})
		if err != nil {
			return nil, fmt.Errorf("finding %s: %w", BinaryName, err)
		}
		binary = found
		cmdArgs = buildArgs(req, true)
	case len(req.Command) > 0:
		binary = req.Command[0]
		// Caller supplied a full argv; honor it verbatim so tests can point
		// at a helper process without having the `acp` subcommand injected.
		cmdArgs = append([]string(nil), req.Command[1:]...)
	case envCmd != "":
		// PI_ACP_CURSOR_CMD overrides the default binary.
		// Parse "binary arg1 arg2 ..." from the env var.
		parts := strings.Fields(envCmd)
		if len(parts) > 0 {
			binary = parts[0]
			// If only one word (just the binary), use default "acp" subcommand.
			// Otherwise use the remaining parts as args.
			if len(parts) > 1 {
				cmdArgs = parts[1:]
			} else {
				cmdArgs = buildArgs(req, true)
			}
		}
	default:
		found, err := findBinary(DefaultBinaryPaths)
		if err != nil {
			return nil, fmt.Errorf("finding %s: %w", BinaryName, err)
		}
		binary = found
		cmdArgs = buildArgs(req, true)
	}

	cmd := exec.CommandContext(ctx, binary, cmdArgs...)
	cmd.Env = os.Environ()
	if req.CWD != "" {
		cmd.Dir = req.CWD
	}
	if len(r.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Env, r.ExtraEnv...)
	}
	if len(req.Env) > 0 {
		cmd.Env = append(cmd.Env, req.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s subprocess: %w", BinaryName, err)
	}

	session := newRunningSession(cmd, stdin, stderr)
	client := &callbackClient{session: session}
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	if r.Logger != nil {
		conn.SetLogger(r.Logger)
	}
	session.conn = conn

	go session.run(req, r.clientInfo())

	return session, nil
}

// buildArgs constructs the CLI argument list for launching Cursor in ACP mode.
// When includeSubcommand is false the caller has already provided a command
// that should be invoked verbatim (e.g. a test helper process).
func buildArgs(req RunRequest, includeSubcommand bool) []string {
	var args []string
	if req.Endpoint != "" {
		args = append(args, "-e", req.Endpoint)
	}
	if req.APIKey != "" {
		args = append(args, "--api-key", req.APIKey)
	}
	if req.AuthToken != "" {
		args = append(args, "--auth-token", req.AuthToken)
	}
	if includeSubcommand {
		args = append(args, ACPSubcommand)
	}
	return args
}

func (r Runner) clientInfo() acp.Implementation {
	if strings.TrimSpace(r.ClientInfo.Name) != "" {
		return r.ClientInfo
	}
	return acp.Implementation{Name: "pi-go", Version: "dev"}
}

func newRunningSession(cmd *exec.Cmd, stdin io.Closer, stderr io.Reader) *RunningSession {
	rs := &RunningSession{
		cmd:        cmd,
		stdin:      stdin,
		stderr:     &stderrBuffer{},
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
	rs.toolFilter = shared.NewToolCallTitleFilter(func(title string) {
		rs.mu.Lock()
		sid := rs.curSession
		rs.mu.Unlock()
		rs.emit(shared.Event{Type: shared.EventTypeTool, Content: title, SessionID: sid})
	})
	go rs.streamStderr(stderr)
	return rs
}

// streamStderr scans stderr line-by-line, buffering for error reports and
// surfacing each line as a progress event so the parent UI sees diagnostics
// (auth required, network errors, etc.) in real time.
func (s *RunningSession) streamStderr(r io.Reader) {
	defer func() {
		if s.stderrDone != nil {
			close(s.stderrDone)
		}
	}()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = s.stderr.Write([]byte(line + "\n"))
		if strings.TrimSpace(line) == "" {
			continue
		}
		s.mu.Lock()
		sid := s.curSession
		s.mu.Unlock()
		s.emit(shared.Event{Type: shared.EventTypeStderr, Content: line, SessionID: sid})
	}
}

func (s *RunningSession) closeStdinOnce() {
	s.closeStdin.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
	})
}

func (s *RunningSession) run(req RunRequest, clientInfo acp.Implementation) {
	defer close(s.done)
	defer close(s.events)
	defer s.toolFilter.Flush()
	defer s.closeStdinOnce()

	client.RunACPFlow(
		context.Background(),
		s.conn,
		shared.RunRequest{
			Command:    req.Command,
			Prompt:     req.Prompt,
			SessionID:  req.SessionID,
			CWD:        req.CWD,
			RPCTimeout: rpcTimeout,
		},
		clientInfo,
		func(sessionID string) {
			s.mu.Lock()
			s.curSession = sessionID
			s.mu.Unlock()
		},
		s.handleUpdate,
		s.finish,
		s.waitProcess,
		s.closeStdinOnce,
		func() shared.RunResult {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.result
		},
	)
}

func (s *RunningSession) handleUpdate(notification acp.SessionNotification) {
	sessionID := string(notification.SessionId)
	update := notification.Update

	s.mu.Lock()
	if s.result.SessionID == "" {
		s.result.SessionID = sessionID
	}
	s.curSession = sessionID
	s.mu.Unlock()

	if chunk := update.AgentMessageChunk; chunk != nil {
		text := contentBlockText(chunk.Content)
		if strings.TrimSpace(text) != "" {
			s.emit(shared.Event{Type: shared.EventTypeMessage, Content: text, SessionID: sessionID})
			s.appendResult(text)
		}
	}
	if thought := update.AgentThoughtChunk; thought != nil {
		text := contentBlockText(thought.Content)
		if strings.TrimSpace(text) != "" {
			s.emit(shared.Event{Type: shared.EventTypeProgress, Content: text, SessionID: sessionID})
		}
	}
	if toolCall := update.ToolCall; toolCall != nil {
		s.toolFilter.OnToolCall(
			string(toolCall.ToolCallId),
			shared.EnrichToolCallTitle(toolCall.Title, toolCall.RawInput),
		)
	}
	if toolUpdate := update.ToolCallUpdate; toolUpdate != nil {
		title := ""
		if toolUpdate.Title != nil {
			title = *toolUpdate.Title
		}
		s.toolFilter.OnToolCallUpdate(
			string(toolUpdate.ToolCallId),
			shared.EnrichToolCallTitle(title, toolUpdate.RawInput),
		)
	}
}

func (s *RunningSession) appendResult(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result.Result += text
}

func (s *RunningSession) emit(event shared.Event) {
	select {
	case s.events <- event:
	default:
	}
}

func (s *RunningSession) finish(result shared.RunResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		if result.Status == shared.StatusSuccess && strings.TrimSpace(s.result.Result) == "" {
			s.result.Result = result.Result
		}
		if s.result.SessionID == "" {
			s.result.SessionID = result.SessionID
		}
		return
	}
	if strings.TrimSpace(result.Error) == "" && s.stderr != nil {
		result.Error = strings.TrimSpace(s.stderr.String())
	}
	s.result = result
	s.finished = true
}

func (s *RunningSession) waitProcess() error {
	// See claudecode.waitProcess — draining stderr before cmd.Wait avoids
	// the race where Wait closes our pipe read-end before the scanner has
	// read the subprocess's last lines.
	if s.stderrDone != nil {
		<-s.stderrDone
	}
	err := s.cmd.Wait()
	if err == nil {
		return nil
	}
	stderr := strings.TrimSpace(s.stderr.String())
	if stderr == "" {
		return fmt.Errorf("%s subprocess: %w", BinaryName, err)
	}
	return fmt.Errorf("%s subprocess: %w: %s", BinaryName, err, stderr)
}

// callbackClient implements the acp.Agent interface for Cursor CLI callbacks.
type callbackClient struct {
	session *RunningSession
}

func (c *callbackClient) ReadTextFile(ctx context.Context, req acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, fmt.Errorf("readTextFile not implemented: Cursor CLI reads files directly")
}

func (c *callbackClient) WriteTextFile(ctx context.Context, req acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, fmt.Errorf("writeTextFile not implemented: Cursor CLI writes files directly")
}

func (c *callbackClient) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// pi-go runs ACP subagents under an already-authorized user session, so
	// auto-approve every tool call. See shared.AutoApproveOutcome.
	return acp.RequestPermissionResponse{Outcome: shared.AutoApproveOutcome(req)}, nil
}

func (c *callbackClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	c.session.handleUpdate(params)
	return nil
}

func (c *callbackClient) CreateTerminal(ctx context.Context, req acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("createTerminal not implemented")
}

func (c *callbackClient) KillTerminal(ctx context.Context, req acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("killTerminal not implemented")
}

func (c *callbackClient) TerminalOutput(ctx context.Context, req acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("terminalOutput not implemented")
}

func (c *callbackClient) ReleaseTerminal(ctx context.Context, req acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("releaseTerminal not implemented")
}

func (c *callbackClient) WaitForTerminalExit(ctx context.Context, req acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("waitForTerminalExit not implemented")
}

// findBinary searches for the Cursor CLI binary in the given paths.
func findBinary(paths []string) (string, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) || strings.HasPrefix(path, ".") {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
			continue
		}
		if fullPath, err := exec.LookPath(path); err == nil {
			return fullPath, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH or default locations", BinaryName)
}

func absDir(path string) string {
	if strings.TrimSpace(path) == "" {
		cwd, err := filepath.Abs(".")
		if err != nil {
			return "."
		}
		return cwd
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func contentBlockText(block acp.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	if block.ResourceLink != nil {
		return block.ResourceLink.Uri
	}
	return ""
}

func stopReasonText(reason acp.StopReason) string {
	if strings.TrimSpace(string(reason)) == "" {
		return ""
	}
	return string(reason)
}

type stderrBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *stderrBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *stderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
