package gemini

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// newCallbackClient returns a callbackClient wrapping a minimal RunningSession
// suitable for exercising the 10 acp.Client callback methods without a live
// subprocess.
func newCallbackClient() *callbackClient {
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(string) {}),
	}
	return &callbackClient{session: session}
}

func TestGeminiCallbackReadTextFileNotImplemented(t *testing.T) {
	c := newCallbackClient()
	_, err := c.ReadTextFile(context.Background(), acp.ReadTextFileRequest{})
	if err == nil {
		t.Fatal("ReadTextFile err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("ReadTextFile err = %q, want to contain 'not implemented'", err.Error())
	}
}

func TestGeminiCallbackWriteTextFileNotImplemented(t *testing.T) {
	c := newCallbackClient()
	_, err := c.WriteTextFile(context.Background(), acp.WriteTextFileRequest{})
	if err == nil {
		t.Fatal("WriteTextFile err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("WriteTextFile err = %q, want to contain 'not implemented'", err.Error())
	}
}

func TestGeminiCallbackRequestPermissionAutoApproves(t *testing.T) {
	c := newCallbackClient()
	resp, err := c.RequestPermission(context.Background(), acp.RequestPermissionRequest{})
	if err != nil {
		t.Fatalf("RequestPermission err = %v, want nil", err)
	}
	if resp.Outcome == (acp.RequestPermissionOutcome{}) {
		t.Errorf("RequestPermission outcome is zero-value; AutoApproveOutcome should populate it")
	}
}

func TestGeminiCallbackSessionUpdateAcceptsEmpty(t *testing.T) {
	c := newCallbackClient()
	if err := c.SessionUpdate(context.Background(), acp.SessionNotification{}); err != nil {
		t.Fatalf("SessionUpdate err = %v, want nil", err)
	}
}

func TestGeminiCallbackTerminalMethodsReturnError(t *testing.T) {
	c := newCallbackClient()
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

// TestGeminiDoneChannelClosesAfterFinish verifies that the channel returned by
// Done() is the session's done channel and observes closure once finish() is
// called and done is manually closed (mirroring session.run's defer close(s.done)).
func TestGeminiDoneChannelClosesAfterFinish(t *testing.T) {
	session := &RunningSession{
		events:     make(chan shared.Event, 1),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(string) {}),
	}

	ch := session.Done()
	if ch == nil {
		t.Fatal("Done() returned nil channel")
	}

	// Simulate the lifecycle: finish populates result, then run's deferred
	// close(s.done) signals completion.
	session.finish(shared.RunResult{Status: shared.StatusSuccess, Result: "ok"})
	close(session.done)

	select {
	case <-ch:
		// expected
	default:
		t.Fatal("Done() channel was not closed after finish+close")
	}
}
