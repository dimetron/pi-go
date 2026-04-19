package subagent

import (
	"context"
	"testing"
	"time"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
)

// fakeACPSession is a lightweight stand-in for a real ACP RunningSession used
// to exercise dispatchACP/pumpACPSession without launching a real subprocess.
type fakeACPSession struct {
	events chan sharedacp.Event
	done   chan struct{}
	result sharedacp.RunResult
}

func newFakeACPSession() *fakeACPSession {
	return &fakeACPSession{
		events: make(chan sharedacp.Event, 8),
		done:   make(chan struct{}),
	}
}

func (s *fakeACPSession) Events() <-chan sharedacp.Event { return s.events }
func (s *fakeACPSession) Done() <-chan struct{}          { return s.done }
func (s *fakeACPSession) Cancel() error                  { return nil }
func (s *fakeACPSession) Wait() sharedacp.RunResult {
	<-s.done
	return s.result
}

// finish closes the stream and unblocks Wait.
func (s *fakeACPSession) finish(result sharedacp.RunResult) {
	close(s.events)
	s.result = result
	close(s.done)
}

func withFakeACPRunner(t *testing.T, sess acpSession) {
	t.Helper()
	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, _, _ string, _ SpawnOpts) (acpSession, error) {
		return sess, nil
	}
	t.Cleanup(func() { startACPSessionFn = prev })
}

func TestACPPromptPreamble(t *testing.T) {
	got := acpPromptPreamble("claude", "echo name")
	want := "You are subagent[claude], echo name when done reply <Task Completed>!"
	if got != want {
		t.Errorf("acpPromptPreamble() = %q, want %q", got, want)
	}
}

// cancelableACPSession tracks whether Cancel was invoked; used to verify
// pumpACPSession tears down the session when the sentinel is emitted.
type cancelableACPSession struct {
	*fakeACPSession
	canceled chan struct{}
}

func newCancelableACPSession() *cancelableACPSession {
	return &cancelableACPSession{
		fakeACPSession: newFakeACPSession(),
		canceled:       make(chan struct{}, 1),
	}
}

func (s *cancelableACPSession) Cancel() error {
	select {
	case s.canceled <- struct{}{}:
	default:
	}
	return nil
}

func TestDispatchACP_CompletionSentinelClosesSession(t *testing.T) {
	sess := newCancelableACPSession()
	withFakeACPRunner(t, sess)

	proc, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "hi"}, "claude")
	if err != nil {
		t.Fatalf("dispatchACP: %v", err)
	}

	go func() {
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "working... ", SessionID: "s"}
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "done <Task Completed>!", SessionID: "s"}
		// Simulate the runner surfacing a kill error after Cancel — pump must
		// coerce that into a success because the sentinel was observed.
		sess.finish(sharedacp.RunResult{
			Status:    sharedacp.StatusError,
			Error:     "killed by signal",
			Result:    "working... done <Task Completed>!",
			SessionID: "s",
		})
	}()

	// Drain events so pumpACPSession can progress through the loop.
	for range proc.Events() {
	}

	select {
	case <-sess.canceled:
	case <-time.After(time.Second):
		t.Fatal("sentinel did not trigger Cancel()")
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait err = %v, expected success on graceful completion", err)
	}
	if result != "working... done" {
		t.Errorf("result = %q, want sentinel stripped", result)
	}
}

// TestDispatchACP_CompletionSentinelSplitAcrossChunks verifies the sentinel
// is detected even when delivered across multiple text_delta chunks.
func TestDispatchACP_CompletionSentinelSplitAcrossChunks(t *testing.T) {
	sess := newCancelableACPSession()
	withFakeACPRunner(t, sess)

	proc, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "hi"}, "cursor")
	if err != nil {
		t.Fatalf("dispatchACP: %v", err)
	}

	go func() {
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "ok <Task ", SessionID: "s"}
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "Completed>!", SessionID: "s"}
		sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "ok <Task Completed>!", SessionID: "s"})
	}()

	for range proc.Events() {
	}

	select {
	case <-sess.canceled:
	case <-time.After(time.Second):
		t.Fatal("split sentinel not detected")
	}
}

// TestDispatchACP_CompletionSentinelWithoutExclamation verifies the loose
// matcher fires when agents drop the trailing "!" (observed in Claude and
// Gemini outputs). The bang is instructional, not load-bearing.
func TestDispatchACP_CompletionSentinelWithoutExclamation(t *testing.T) {
	for _, tc := range []struct {
		name, agent, text, wantResult string
	}{
		{"gemini no space no bang", "gemini", "Gemini CLI<Task Completed>", "Gemini CLI"},
		{"claude trailing no bang", "claude", "claude <Task Completed>", "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := newCancelableACPSession()
			withFakeACPRunner(t, sess)

			proc, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "hi"}, tc.agent)
			if err != nil {
				t.Fatalf("dispatchACP: %v", err)
			}

			go func() {
				sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: tc.text, SessionID: "s"}
				sess.finish(sharedacp.RunResult{
					Status:    sharedacp.StatusError,
					Error:     "killed",
					Result:    tc.text,
					SessionID: "s",
				})
			}()

			for range proc.Events() {
			}

			select {
			case <-sess.canceled:
			case <-time.After(time.Second):
				t.Fatal("loose sentinel did not trigger Cancel()")
			}

			result, err := proc.Wait()
			if err != nil {
				t.Fatalf("Wait err = %v, expected success", err)
			}
			if result != tc.wantResult {
				t.Errorf("result = %q, want %q", result, tc.wantResult)
			}
		})
	}
}

func TestDispatchACP_WrapsPromptWithPreamble(t *testing.T) {
	sess := newFakeACPSession()
	var capturedPrompt, capturedAgent string
	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, agent, prompt string, _ SpawnOpts) (acpSession, error) {
		capturedPrompt = prompt
		capturedAgent = agent
		return sess, nil
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	proc, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "review changes"}, "cursor")
	if err != nil {
		t.Fatalf("dispatchACP: %v", err)
	}
	t.Cleanup(func() {
		sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess})
		<-proc.done
	})

	wantPrompt := "You are subagent[cursor], review changes when done reply <Task Completed>!"
	if capturedPrompt != wantPrompt {
		t.Errorf("prompt = %q, want %q", capturedPrompt, wantPrompt)
	}
	if capturedAgent != "cursor" {
		t.Errorf("agent name = %q, want cursor", capturedAgent)
	}
}

func TestDispatchACP_TranslatesEventsAndResult(t *testing.T) {
	sess := newFakeACPSession()
	withFakeACPRunner(t, sess)

	proc, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "hi"}, "claude")
	if err != nil {
		t.Fatalf("dispatchACP: %v", err)
	}

	// Push a small event sequence through the fake session.
	go func() {
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "hello", SessionID: "abc"}
		sess.events <- sharedacp.Event{Type: sharedacp.EventTypeTool, Content: "read"}
		sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "hello", SessionID: "abc"})
	}()

	var got []Event
	for ev := range proc.Events() {
		got = append(got, ev)
	}

	// Expected: message_start (sessionID), text_delta, tool_call, message_end.
	wantTypes := []string{"message_start", "text_delta", "tool_call", "message_end"}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d (events: %+v)", len(got), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, got[i].Type, want)
		}
	}
	if got[0].SessionID != "abc" {
		t.Errorf("message_start session id = %q, want abc", got[0].SessionID)
	}
	if got[1].Content != "hello" {
		t.Errorf("text_delta content = %q, want hello", got[1].Content)
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait err: %v", err)
	}
	if result != "hello" {
		t.Errorf("Wait result = %q, want hello", result)
	}
}

func TestDispatchACP_ErrorResultEmitsError(t *testing.T) {
	sess := newFakeACPSession()
	withFakeACPRunner(t, sess)

	proc, err := dispatchACP(context.Background(), SpawnOpts{Prompt: "go"}, "gemini")
	if err != nil {
		t.Fatalf("dispatchACP: %v", err)
	}

	go func() {
		sess.finish(sharedacp.RunResult{Status: sharedacp.StatusError, Error: "boom", SessionID: "x"})
	}()

	deadline := time.After(2 * time.Second)
	var (
		sawError    bool
		sawMsgEnd   bool
		sawMsgStart bool
	)
loop:
	for {
		select {
		case ev, ok := <-proc.Events():
			if !ok {
				break loop
			}
			switch ev.Type {
			case "message_start":
				sawMsgStart = true
			case "error":
				sawError = true
			case "message_end":
				sawMsgEnd = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for events")
		}
	}

	if !sawMsgStart {
		t.Error("expected message_start event from final session id")
	}
	if !sawError {
		t.Error("expected error event")
	}
	if !sawMsgEnd {
		t.Error("expected message_end event")
	}

	if _, err := proc.Wait(); err == nil {
		t.Error("expected non-nil error from Wait on error result")
	}
}

func TestDispatchACP_RequiresPrompt(t *testing.T) {
	if _, err := dispatchACP(context.Background(), SpawnOpts{}, "claude"); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestStartACPSession_UnknownAgent(t *testing.T) {
	if _, err := startACPSession(context.Background(), "bogus", "p", SpawnOpts{}); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}
