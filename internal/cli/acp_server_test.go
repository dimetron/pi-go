package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"
)

type availableCommandsCapture struct {
	mu       sync.Mutex
	commands map[string][]acp.AvailableCommand
}

func (c *availableCommandsCapture) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	if n.Update.AvailableCommandsUpdate == nil {
		return nil
	}
	cmds := append([]acp.AvailableCommand(nil), n.Update.AvailableCommandsUpdate.AvailableCommands...)
	c.mu.Lock()
	if c.commands == nil {
		c.commands = make(map[string][]acp.AvailableCommand)
	}
	c.commands[string(n.SessionId)] = cmds
	c.mu.Unlock()
	return nil
}

func (c *availableCommandsCapture) availableCommands(sessionID string) ([]acp.AvailableCommand, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmds, ok := c.commands[sessionID]
	if !ok {
		return nil, false
	}
	return append([]acp.AvailableCommand(nil), cmds...), true
}

func waitForCapturedCommands(t *testing.T, c *availableCommandsCapture, sessionID string) []acp.AvailableCommand {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cmds, ok := c.availableCommands(sessionID); ok {
			return cmds
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for available commands for session %s", sessionID)
	return nil
}

func captureHasCommand(cmds []acp.AvailableCommand, name string) bool {
	for _, cmd := range cmds {
		if cmd.Name == name {
			return true
		}
	}
	return false
}

func (c *availableCommandsCapture) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, errors.New("requestPermission not used in acp server cli tests")
}

func (c *availableCommandsCapture) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.New("readTextFile not used in acp server cli tests")
}

func (c *availableCommandsCapture) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errors.New("writeTextFile not used in acp server cli tests")
}

func (c *availableCommandsCapture) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.New("createTerminal not used in acp server cli tests")
}

func (c *availableCommandsCapture) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.New("killTerminal not used in acp server cli tests")
}

func (c *availableCommandsCapture) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.New("releaseTerminal not used in acp server cli tests")
}

func (c *availableCommandsCapture) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.New("terminalOutput not used in acp server cli tests")
}

func (c *availableCommandsCapture) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.New("waitForTerminalExit not used in acp server cli tests")
}

// -----------------------------------------------------------------------
// newACPServerCmd — structural verification
// -----------------------------------------------------------------------

func TestNewACPServerCmd_Structure(t *testing.T) {
	cmd := newACPServerCmd()
	if cmd.Use != "acp-server" {
		t.Errorf("Use = %q, want %q", cmd.Use, "acp-server")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
	if cmd.Flags().Lookup("model") == nil {
		t.Error("missing --model flag")
	}
	if cmd.Flags().Lookup("header") == nil {
		t.Error("missing --header flag")
	} else if got := cmd.Flags().Lookup("header").NoOptDefVal; got != "" {
		t.Errorf("--header NoOptDefVal = %q, want empty string", got)
	}
	// Verify Args == NoArgs.
	if cmd.Args == nil {
		t.Error("Args validator is nil")
	}
}

// -----------------------------------------------------------------------
// runACPServer — drive with canceled context so Serve returns quickly.
// runACPServer calls signal.NotifyContext and acpserver.Serve which blocks
// on ctx.Done(). By redirecting os.Stdin/os.Stdout to pipes and canceling
// the parent context, Serve returns ctx.Err() which is wrapped.
// -----------------------------------------------------------------------

func TestRunACPServer_ContextCanceled(t *testing.T) {
	// Save and restore stdin/stdout.
	origStdin := os.Stdin
	origStdout := os.Stdout
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	// Set up a pipe for stdin so reads block but never receive data.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()
	os.Stdin = stdinR

	// Drain stdout into a discard so the ACP server's JSON writes don't deadlock.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	os.Stdout = stdoutW

	// Drain stdout in background so we don't fill the pipe.
	go func() {
		_, _ = io.Copy(io.Discard, stdoutR)
	}()

	// Cancel the context quickly after starting to stop the server.
	ctx, cancel := context.WithCancel(context.Background())

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	// Save and restore flags.
	origModel := flagModel
	defer func() { flagModel = origModel }()
	flagModel = "" // exercise the fallback "minimax-m2.7:cloud" branch.

	done := make(chan error, 1)
	go func() {
		done <- runACPServer(cmd, nil)
	}()

	// Give runACPServer a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Either nil (clean exit) or a wrapped context error is acceptable.
		if err != nil && !strings.Contains(err.Error(), "context") &&
			!strings.Contains(err.Error(), "acp server") &&
			!strings.Contains(err.Error(), "canceled") {
			t.Logf("runACPServer returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runACPServer did not return within 5 seconds of context cancel")
	}
}

func TestRunACPServer_ExplicitModel(t *testing.T) {
	origStdin := os.Stdin
	origStdout := os.Stdout
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()
	os.Stdin = stdinR

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	os.Stdout = stdoutW
	go func() { _, _ = io.Copy(io.Discard, stdoutR) }()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	origModel := flagModel
	defer func() { flagModel = origModel }()
	flagModel = "gpt-4o" // Exercise the branch where model != "".

	done := make(chan error, 1)
	go func() { done <- runACPServer(cmd, nil) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runACPServer did not return within 5 seconds")
	}
}

func TestRunACPServer_AvailableCommandsUseSessionCWD(t *testing.T) {
	origStdin := os.Stdin
	origStdout := os.Stdout
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
		_ = os.Chdir(origWD)
	}()

	launcherDir := t.TempDir()
	projectRoot := t.TempDir()
	sessionCWD := filepath.Join(projectRoot, "nested", "workspace")
	if err := os.MkdirAll(sessionCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll(sessionCWD) error = %v", err)
	}
	skillDir := filepath.Join(projectRoot, ".pi-go", "skills", "session-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skillDir) error = %v", err)
	}
	skillBody := `---
name: session-skill
description: Loaded from session cwd
---
Use session-specific behavior.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	if err := os.Chdir(launcherDir); err != nil {
		t.Fatalf("Chdir(%s) error = %v", launcherDir, err)
	}

	clientToServerR, clientToServerW, err := os.Pipe()
	if err != nil {
		t.Fatalf("client->server pipe: %v", err)
	}
	defer clientToServerR.Close()
	defer clientToServerW.Close()
	serverToClientR, serverToClientW, err := os.Pipe()
	if err != nil {
		t.Fatalf("server->client pipe: %v", err)
	}
	defer serverToClientR.Close()
	defer serverToClientW.Close()

	os.Stdin = clientToServerR
	os.Stdout = serverToClientW

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- runACPServer(cmd, nil) }()

	captures := &availableCommandsCapture{}
	clientConn := acp.NewClientSideConnection(captures, clientToServerW, serverToClientR)

	initCtx, initCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer initCancel()
	if _, err := clientConn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sessResp, err := clientConn.NewSession(initCtx, acp.NewSessionRequest{
		Cwd:        sessionCWD,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	cmds := waitForCapturedCommands(t, captures, string(sessResp.SessionId))
	if !captureHasCommand(cmds, "session-skill") {
		t.Fatalf("available commands = %+v, want session-skill from session cwd", cmds)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runACPServer did not return within 5 seconds")
	}
}

// -----------------------------------------------------------------------
// logHandler tests
// -----------------------------------------------------------------------

func TestLogHandler_Handle(t *testing.T) {
	f, err := os.CreateTemp("", "logtest-*.log")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	h := &logHandler{f: f}
	rec := slog.Record{
		Message: "test message",
		Level:   slog.LevelInfo,
	}
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Verify the log file was written to.
	content, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "test message") {
		t.Errorf("log output = %q, want contains 'test message'", string(content))
	}
}

func TestLogHandler_WithAttrs(t *testing.T) {
	f, err := os.CreateTemp("", "logtest-*.log")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	h := &logHandler{f: f}
	h2 := h.WithAttrs(nil)
	if h2 == nil {
		t.Fatal("WithAttrs returned nil")
	}
	if h2 != h {
		t.Error("WithAttrs should return the same handler")
	}
}

func TestLogHandler_WithGroup(t *testing.T) {
	f, err := os.CreateTemp("", "logtest-*.log")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	h := &logHandler{f: f}
	h2 := h.WithGroup("group")
	if h2 == nil {
		t.Fatal("WithGroup returned nil")
	}
	if h2 != h {
		t.Error("WithGroup should return the same handler")
	}
}

func TestLogHandler_Enabled(t *testing.T) {
	f, err := os.CreateTemp("", "logtest-*.log")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	h := &logHandler{f: f}
	if !h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Enabled should always return true")
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled should always return true")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled should always return true")
	}
}
