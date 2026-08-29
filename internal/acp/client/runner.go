package client

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// Runner launches a local ACP-capable subprocess and runs a single prompt turn.
type Runner struct {
	ClientInfo acp.Implementation
	Logger     *slog.Logger
	ExtraEnv   []string
}

func (r Runner) startCommand(ctx context.Context, cmd *exec.Cmd, req shared.RunRequest) (*RunningSession, error) {
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
		return nil, fmt.Errorf("start acp subprocess: %w", err)
	}

	session := newRunningSession(cmd, stdin, stderr)
	client := &callbackClient{session: session}
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	// Always install a logger. With none set the SDK falls back to
	// slog.Default(), which writes to stderr — the same terminal the TUI is
	// drawing on. Its end-of-run line
	//
	//	INFO connection closed cause="peer connection closed"
	//
	// lands mid-frame and carries its own newline, which scrolls the terminal
	// by a row. The TUI renders inline rather than on the alternate screen, so
	// it repaints by moving the cursor up a counted number of rows: after that
	// scroll every frame lands one row off, stranding the previous frame's
	// status bar in the input line for the rest of the session.
	//
	// No caller sets Logger today, so defaulting here rather than at each call
	// site is what makes the guarantee hold for every agent — and for the next
	// one added.
	conn.SetLogger(r.logger())
	session.conn = conn

	go session.run(req, r.clientInfo())

	return session, nil
}

// StartCommand launches an already-resolved ACP subprocess command with the
// shared client-side connection/session wiring.
func (r Runner) StartCommand(ctx context.Context, cmd *exec.Cmd, req shared.RunRequest) (*RunningSession, error) {
	return r.startCommand(ctx, cmd, req)
}

// Start launches the subprocess and begins the ACP initialize/session/prompt flow.
func (r Runner) Start(ctx context.Context, req shared.RunRequest) (*RunningSession, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	return r.startCommand(ctx, cmd, req)
}

// logger returns the logger for SDK connection diagnostics. A nil Logger means
// "discard", never "fall back to slog.Default()" — see startCommand.
func (r Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.New(slog.DiscardHandler)
}

func (r Runner) clientInfo() acp.Implementation {
	if strings.TrimSpace(r.ClientInfo.Name) != "" {
		return r.ClientInfo
	}
	return acp.Implementation{Name: "pi-go", Version: "dev"}
}

type callbackClient struct {
	session *RunningSession
}

func (c *callbackClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, fmt.Errorf("readTextFile not implemented")
}

func (c *callbackClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, fmt.Errorf("writeTextFile not implemented")
}

func (c *callbackClient) RequestPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: shared.AutoApproveOutcome(req)}, nil
}

func (c *callbackClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.session.handleUpdate(params)
	return nil
}

func (c *callbackClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (c *callbackClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (c *callbackClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (c *callbackClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (c *callbackClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
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
