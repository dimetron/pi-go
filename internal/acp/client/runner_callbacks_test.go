package client

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// newTestCallbackClient returns a callbackClient wrapping a minimal
// RunningSession built from a no-op exec.Cmd with dummy stdin/stderr. The
// returned session is safe to use for exercising SessionUpdate — no background
// subprocess is started against it.
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
		// closing pw causes streamStderr to return so stderrDone closes and
		// no goroutines are left behind.
		_ = pw.Close()
		select {
		case <-session.stderrDone:
		case <-time.After(time.Second):
		}
	})

	return &callbackClient{session: session}
}

func TestCallbackClientReadTextFileNotImplemented(t *testing.T) {
	c := newTestCallbackClient(t)
	_, err := c.ReadTextFile(context.Background(), acp.ReadTextFileRequest{})
	if err == nil {
		t.Fatal("ReadTextFile err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("ReadTextFile err = %q, want to contain 'not implemented'", err.Error())
	}
}

func TestCallbackClientWriteTextFileNotImplemented(t *testing.T) {
	c := newTestCallbackClient(t)
	_, err := c.WriteTextFile(context.Background(), acp.WriteTextFileRequest{})
	if err == nil {
		t.Fatal("WriteTextFile err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("WriteTextFile err = %q, want to contain 'not implemented'", err.Error())
	}
}

func TestCallbackClientRequestPermissionAutoApproves(t *testing.T) {
	c := newTestCallbackClient(t)
	resp, err := c.RequestPermission(context.Background(), acp.RequestPermissionRequest{})
	if err != nil {
		t.Fatalf("RequestPermission err = %v, want nil", err)
	}
	// AutoApproveOutcome returns an outcome even for empty request (canceled)
	// so Outcome must be non-zero-value.
	if resp.Outcome == (acp.RequestPermissionOutcome{}) {
		t.Errorf("RequestPermission outcome is zero-value; AutoApproveOutcome should populate it")
	}
}

func TestCallbackClientTerminalMethodsReturnMethodNotFound(t *testing.T) {
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

func TestCallbackClientSessionUpdateAcceptsEmptyNotification(t *testing.T) {
	c := newTestCallbackClient(t)
	if err := c.SessionUpdate(context.Background(), acp.SessionNotification{}); err != nil {
		t.Fatalf("SessionUpdate err = %v, want nil", err)
	}
}

// TestStartCommandInvalidBinary verifies that StartCommand surfaces a start
// error when the subprocess cannot be exec'd. The returned error from
// StartCommand itself may be non-nil (Start fails) — both paths are valid;
// this test documents that Start errors are propagated somewhere observable
// (either the Start return or the session's eventual StatusError result).
func TestStartCommandInvalidBinary(t *testing.T) {
	runner := Runner{}
	cmd := exec.Command("/nonexistent/binary/does/not/exist")
	session, err := runner.StartCommand(context.Background(), cmd, shared.RunRequest{
		Prompt: "hi",
	})
	if err != nil {
		// Start itself failed — acceptable; nothing more to verify.
		return
	}
	// Start succeeded (unexpected for a missing binary, but possible if pipes
	// were wired before Start errored). The session should converge to an
	// error status through waitProcess / finish.
	result := session.Wait()
	if result.Status != shared.StatusError {
		t.Errorf("status = %q, want %q", result.Status, shared.StatusError)
	}
}

func TestAbsDirTable(t *testing.T) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("filepath.Abs(.) err = %v", err)
	}
	fooAbs, err := filepath.Abs("foo")
	if err != nil {
		t.Fatalf("filepath.Abs(foo) err = %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "empty returns cwd", path: "", want: cwd},
		{name: "whitespace returns cwd", path: "   ", want: cwd},
		{name: "relative foo becomes absolute", path: "foo", want: fooAbs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := absDir(tt.path)
			if got != tt.want {
				t.Errorf("absDir(%q) = %q, want %q", tt.path, got, tt.want)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("absDir(%q) = %q, want absolute path", tt.path, got)
			}
		})
	}
}

// TestAbsDirDoesNotExpandTilde documents that absDir does not perform `~`
// home expansion — `~` is treated as a literal directory name relative to
// the current working directory.
func TestAbsDirDoesNotExpandTilde(t *testing.T) {
	got := absDir("~")
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("filepath.Abs(.) err = %v", err)
	}
	want := filepath.Join(cwd, "~")
	if got != want {
		t.Errorf("absDir(~) = %q, want %q (no tilde expansion)", got, want)
	}
}
