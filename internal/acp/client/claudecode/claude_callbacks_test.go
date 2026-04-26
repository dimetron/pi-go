package claudecode

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// newTestCallbackClient returns a callbackClient wrapping a minimal
// RunningSession built from a no-op exec.Cmd with dummy stdin/stderr.
func newTestCallbackClient(t *testing.T) *callbackClient {
	t.Helper()

	cmd := exec.Command("true")
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
	})

	session := newRunningSession(cmd, pw, pr)
	t.Cleanup(func() {
		_ = pw.Close()
		select {
		case <-session.stderrDone:
		case <-time.After(time.Second):
		}
	})

	return &callbackClient{session: session}
}

func TestClaudeCallbackClientReadTextFileNotImplemented(t *testing.T) {
	c := newTestCallbackClient(t)
	_, err := c.ReadTextFile(context.Background(), acp.ReadTextFileRequest{})
	if err == nil {
		t.Fatal("ReadTextFile err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("ReadTextFile err = %q, want to contain 'not implemented'", err.Error())
	}
}

func TestClaudeCallbackClientWriteTextFileNotImplemented(t *testing.T) {
	c := newTestCallbackClient(t)
	_, err := c.WriteTextFile(context.Background(), acp.WriteTextFileRequest{})
	if err == nil {
		t.Fatal("WriteTextFile err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("WriteTextFile err = %q, want to contain 'not implemented'", err.Error())
	}
}

func TestClaudeCallbackClientRequestPermissionAutoApproves(t *testing.T) {
	c := newTestCallbackClient(t)
	resp, err := c.RequestPermission(context.Background(), acp.RequestPermissionRequest{})
	if err != nil {
		t.Fatalf("RequestPermission err = %v, want nil", err)
	}
	if resp.Outcome == (acp.RequestPermissionOutcome{}) {
		t.Errorf("RequestPermission outcome is zero-value; AutoApproveOutcome should populate it")
	}
}

func TestClaudeCallbackClientSessionUpdateAcceptsEmptyNotification(t *testing.T) {
	c := newTestCallbackClient(t)
	if err := c.SessionUpdate(context.Background(), acp.SessionNotification{}); err != nil {
		t.Fatalf("SessionUpdate err = %v, want nil", err)
	}
}

func TestClaudeCallbackClientTerminalMethodsReturnMethodNotFound(t *testing.T) {
	c := newTestCallbackClient(t)
	ctx := context.Background()

	if _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{}); err == nil {
		t.Error("CreateTerminal err = nil, want non-nil")
	}
	if _, err := c.KillTerminal(ctx, acp.KillTerminalRequest{}); err == nil {
		t.Error("KillTerminal err = nil, want non-nil")
	}
	if _, err := c.TerminalOutput(ctx, acp.TerminalOutputRequest{}); err == nil {
		t.Error("TerminalOutput err = nil, want non-nil")
	}
	if _, err := c.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{}); err == nil {
		t.Error("ReleaseTerminal err = nil, want non-nil")
	}
	if _, err := c.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{}); err == nil {
		t.Error("WaitForTerminalExit err = nil, want non-nil")
	}
}
