// Package claudecode provides an ACP client for Claude Code via the
// @zed-industries/claude-code-acp subprocess adapter.
package claudecode

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// BinaryName is the expected name of the Claude Code ACP binary.
const BinaryName = "claude-code-acp"

// DefaultBinaryPaths lists common installation locations for claude-code-acp.
var DefaultBinaryPaths = []string{
	"claude-code-acp",
	".claude/bin/claude-code-acp",
	"/usr/local/bin/claude-code-acp",
	"/usr/bin/claude-code-acp",
}

// Runner launches Claude Code via the claude-code-acp subprocess adapter
// and manages the ACP client-side connection.
type Runner struct {
	// ClientInfo identifies this client to the agent.
	ClientInfo acp.Implementation

	// Binary is the path to the claude-code-acp binary.
	// If empty, uses the first found in DefaultBinaryPaths.
	Binary string

	// Logger for connection debugging.
	Logger *slog.Logger

	// ExtraEnv is additional environment variables to pass to the subprocess.
	ExtraEnv []string
}

// RunRequest describes a Claude Code prompt turn.
type RunRequest struct {
	Prompt    string   // Prompt to send to Claude Code
	SessionID string   // Optional session ID to resume
	CWD       string   // Working directory
	Env       []string // Additional environment variables
	Command   []string // Optional command override for testing (defaults to claude-code-acp)
}

// RunningSession represents one in-flight Claude Code prompt turn.
type RunningSession struct {
	cmd    *exec.Cmd
	stdin  io.Closer
	stderr *stderrBuffer
	conn   *acp.ClientSideConnection

	events chan shared.Event
	done   chan struct{}

	closeStdin sync.Once

	mu       sync.Mutex
	result   shared.RunResult
	finished bool
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
		return fmt.Errorf("kill claude-code subprocess: %w", err)
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

// Start launches the Claude Code subprocess and begins the ACP flow.
func (r Runner) Start(ctx context.Context, req RunRequest) (*RunningSession, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	binary := r.Binary
	if binary == "" {
		// Use Command if provided (for testing), otherwise find the binary.
		if len(req.Command) > 0 {
			binary = req.Command[0]
		} else {
			var err error
			binary, err = findBinary(DefaultBinaryPaths)
			if err != nil {
				return nil, fmt.Errorf("finding %s: %w", BinaryName, err)
			}
		}
	}

	cmdArgs := []string{}
	if len(req.Command) > 1 {
		cmdArgs = req.Command[1:]
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

func (r Runner) clientInfo() acp.Implementation {
	if strings.TrimSpace(r.ClientInfo.Name) != "" {
		return r.ClientInfo
	}
	return acp.Implementation{Name: "pi-go", Version: "dev"}
}

func newRunningSession(cmd *exec.Cmd, stdin io.Closer, stderr io.Reader) *RunningSession {
	rs := &RunningSession{
		cmd:    cmd,
		stdin:  stdin,
		stderr: &stderrBuffer{},
		events: make(chan shared.Event, 32),
		done:   make(chan struct{}),
	}
	go rs.stderr.readFrom(stderr)
	return rs
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
	defer s.closeStdinOnce()

	ctx := context.Background()

	initResp, err := s.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		ClientInfo:      &clientInfo,
	})
	if err != nil {
		s.closeStdinOnce()
		s.finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("initialize: %v", err)})
		_ = s.waitProcess()
		return
	}

	newSessionResp, err := s.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        absDir(req.CWD),
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		s.closeStdinOnce()
		s.finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("new session: %v", err)})
		_ = s.waitProcess()
		return
	}

	sessionID := string(newSessionResp.SessionId)
	if sessionID == "" {
		sessionID = req.SessionID
	}

	_, _ = initResp, req.SessionID

	promptResp, err := s.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: newSessionResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(req.Prompt)},
	})
	if err != nil {
		s.closeStdinOnce()
		s.finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("prompt: %v", err), SessionID: sessionID})
		_ = s.waitProcess()
		return
	}

	// Signal end-of-stream to the agent so it can exit cleanly.
	s.closeStdinOnce()

	if err := s.waitProcess(); err != nil {
		s.finish(shared.RunResult{Status: shared.StatusError, Error: err.Error(), SessionID: sessionID})
		return
	}

	s.mu.Lock()
	result := s.result
	s.mu.Unlock()
	if strings.TrimSpace(result.Result) == "" {
		result.Result = stopReasonText(promptResp.StopReason)
	}
	result.Status = shared.StatusSuccess
	result.SessionID = sessionID
	s.finish(result)
}

func (s *RunningSession) handleUpdate(notification acp.SessionNotification) {
	sessionID := string(notification.SessionId)
	update := notification.Update

	s.mu.Lock()
	if s.result.SessionID == "" {
		s.result.SessionID = sessionID
	}
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
		if title := strings.TrimSpace(toolCall.Title); title != "" {
			s.emit(shared.Event{Type: shared.EventTypeTool, Content: title, SessionID: sessionID})
		}
	}
	if toolUpdate := update.ToolCallUpdate; toolUpdate != nil {
		if title := toolUpdate.Title; title != nil && strings.TrimSpace(*title) != "" {
			s.emit(shared.Event{Type: shared.EventTypeTool, Content: *title, SessionID: sessionID})
		}
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
	if strings.TrimSpace(result.Error) == "" {
		result.Error = strings.TrimSpace(s.stderr.String())
	}
	s.result = result
	s.finished = true
}

func (s *RunningSession) waitProcess() error {
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

// callbackClient implements the acp.Agent interface for Claude Code callbacks.
type callbackClient struct {
	session *RunningSession
}

func (c *callbackClient) ReadTextFile(ctx context.Context, req acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	// Claude Code can read files directly; delegate to the subprocess.
	// For now, return a redirect response.
	return acp.ReadTextFileResponse{}, fmt.Errorf("readTextFile not implemented: use terminal for file operations")
}

func (c *callbackClient) WriteTextFile(ctx context.Context, req acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, fmt.Errorf("writeTextFile not implemented: use terminal for file operations")
}

func (c *callbackClient) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// By default, deny permissions for safety.
	// Claude Code should handle permissions via its own flow.
	return acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeCancelled(),
	}, nil
}

func (c *callbackClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	c.session.handleUpdate(params)
	return nil
}

func (c *callbackClient) CreateTerminal(ctx context.Context, req acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	// Terminal management is handled by Claude Code directly.
	return acp.CreateTerminalResponse{}, fmt.Errorf("createTerminal not implemented: Claude Code manages terminals")
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

// findBinary searches for the claude-code-acp binary in the given paths.
func findBinary(paths []string) (string, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		// Check if it's a full path that exists
		if filepath.IsAbs(path) || strings.HasPrefix(path, ".") {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
			continue
		}
		// Try using LookPath for commands in PATH
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

func (b *stderrBuffer) readFrom(r io.Reader) {
	_, _ = io.Copy(b, r)
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
