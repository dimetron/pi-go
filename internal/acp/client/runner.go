package client

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

// Runner launches a local ACP-capable subprocess and runs a single prompt turn.
type Runner struct {
	ClientInfo acp.Implementation
	Logger     *slog.Logger
}

// Start launches the subprocess and begins the ACP initialize/session/prompt flow.
func (r Runner) Start(ctx context.Context, req shared.RunRequest) (*RunningSession, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	cmd.Env = os.Environ()
	if req.CWD != "" {
		cmd.Dir = req.CWD
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

type callbackClient struct {
	session *RunningSession
}

func (c *callbackClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, fmt.Errorf("readTextFile not implemented")
}

func (c *callbackClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, fmt.Errorf("writeTextFile not implemented")
}

func (c *callbackClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, fmt.Errorf("requestPermission not implemented")
}

func (c *callbackClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.session.handleUpdate(params)
	return nil
}

func (c *callbackClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("createTerminal not implemented")
}

func (c *callbackClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("killTerminal not implemented")
}

func (c *callbackClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("terminalOutput not implemented")
}

func (c *callbackClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("releaseTerminal not implemented")
}

func (c *callbackClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("waitForTerminalExit not implemented")
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
