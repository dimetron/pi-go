package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/codex"
)

// fakeCodexSession stands in for a real codex.Session so dispatchCodex and
// pumpCodexSession can be exercised without the codex binary.
type fakeCodexSession struct {
	events   chan codex.Event
	done     chan struct{}
	canceled chan struct{}
	result   codex.RunResult
}

func newFakeCodexSession() *fakeCodexSession {
	return &fakeCodexSession{
		events:   make(chan codex.Event, 8),
		done:     make(chan struct{}),
		canceled: make(chan struct{}, 1),
	}
}

func (s *fakeCodexSession) Events() <-chan codex.Event { return s.events }
func (s *fakeCodexSession) Done() <-chan struct{}      { return s.done }

func (s *fakeCodexSession) Cancel() error {
	select {
	case s.canceled <- struct{}{}:
	default:
	}
	return nil
}

func (s *fakeCodexSession) Wait() codex.RunResult {
	<-s.done
	return s.result
}

// finish closes the stream and unblocks Wait.
func (s *fakeCodexSession) finish(result codex.RunResult) {
	close(s.events)
	s.result = result
	close(s.done)
}

func withFakeCodexRunner(t *testing.T, sess codexSession) {
	t.Helper()
	prev := startCodexSessionFn
	startCodexSessionFn = func(_ context.Context, _, _ string, _ SpawnOpts) (codexSession, error) {
		return sess, nil
	}
	t.Cleanup(func() { startCodexSessionFn = prev })
}

func TestIsCodexAgent(t *testing.T) {
	for name, want := range map[string]bool{
		"codex":        true,
		"codex-review": true,
		"claude":       false,
		"copilot":      false,
		"task":         false,
		"":             false,
	} {
		if got := isCodexAgent(name); got != want {
			t.Errorf("isCodexAgent(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestCodexAgentsAreNotACPAgents guards the routing invariant: adding codex to
// acpAgentNames would send it through dispatchACP and the ACP SDK, which cannot
// speak the app-server protocol.
func TestCodexAgentsAreNotACPAgents(t *testing.T) {
	for name := range codexAgentNames {
		if isACPAgent(name) {
			t.Errorf("%q is registered as an ACP agent; it must dispatch through dispatchCodex", name)
		}
	}
}

func TestCodexPromptPreamble(t *testing.T) {
	got := codexPromptPreamble("codex", "fix the build")
	if !strings.Contains(got, "You are subagent[codex]. fix the build") {
		t.Errorf("preamble missing role header: %q", got)
	}
	if !strings.Contains(got, "ANTI-HALLUCINATION RULES") {
		t.Errorf("preamble missing anti-hallucination rules: %q", got)
	}
	// Codex signals completion with turn/completed, so the ACP sentinel must
	// not leak into its prompt.
	if strings.Contains(got, acpCompletionMatcher) {
		t.Errorf("codex preamble must not ask for the ACP completion sentinel: %q", got)
	}
}

func TestDispatchCodex_TranslatesEventsAndResult(t *testing.T) {
	sess := newFakeCodexSession()
	withFakeCodexRunner(t, sess)

	proc, err := dispatchCodex(context.Background(), SpawnOpts{Prompt: "hi"}, "codex")
	if err != nil {
		t.Fatalf("dispatchCodex: %v", err)
	}

	go func() {
		sess.events <- codex.Event{Type: codex.EventTypeMessage, Content: "hello", SessionID: "thr_1"}
		sess.events <- codex.Event{Type: codex.EventTypeTool, Content: "Running: go build", SessionID: "thr_1"}
		sess.events <- codex.Event{Type: codex.EventTypeProgress, Content: "thinking", SessionID: "thr_1"}
		sess.events <- codex.Event{Type: codex.EventTypeStderr, Content: "warn", SessionID: "thr_1"}
		sess.finish(codex.RunResult{
			Status:     codex.StatusSuccess,
			Result:     "hello",
			SessionID:  "thr_1",
			StopReason: codex.TurnCompleted,
		})
	}()

	var got []Event
	for ev := range proc.Events() {
		got = append(got, ev)
	}

	wantTypes := []string{"message_start", "text_delta", "tool_call", "tool_call", "stderr", "message_end"}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d (events: %+v)", len(got), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, got[i].Type, want)
		}
	}
	if got[0].SessionID != "thr_1" {
		t.Errorf("message_start session id = %q, want thr_1", got[0].SessionID)
	}
	if got[1].Content != "hello" {
		t.Errorf("text_delta content = %q, want hello", got[1].Content)
	}
	last := got[len(got)-1]
	if last.StopReason != codex.TurnCompleted {
		t.Errorf("message_end stopReason = %q, want %q", last.StopReason, codex.TurnCompleted)
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait err: %v", err)
	}
	if result != "hello" {
		t.Errorf("Wait result = %q, want hello", result)
	}
}

// TestDispatchCodex_NoSentinelStripping verifies codex output is passed through
// verbatim: unlike the ACP path, there is no completion sentinel to detect or
// strip, and the session must not be canceled on the agent's say-so.
func TestDispatchCodex_NoSentinelStripping(t *testing.T) {
	sess := newFakeCodexSession()
	withFakeCodexRunner(t, sess)

	proc, err := dispatchCodex(context.Background(), SpawnOpts{Prompt: "hi"}, "codex")
	if err != nil {
		t.Fatalf("dispatchCodex: %v", err)
	}

	text := "all done <Task Completed>!"
	go func() {
		sess.events <- codex.Event{Type: codex.EventTypeMessage, Content: text, SessionID: "thr_1"}
		sess.finish(codex.RunResult{Status: codex.StatusSuccess, Result: text, SessionID: "thr_1"})
	}()

	for range proc.Events() {
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait err: %v", err)
	}
	if result != text {
		t.Errorf("result = %q, want it left verbatim (no sentinel stripping)", result)
	}
	select {
	case <-sess.canceled:
		t.Error("codex session was canceled on a sentinel; only turn/completed ends a codex turn")
	default:
	}
}

func TestDispatchCodex_ErrorResultEmitsError(t *testing.T) {
	sess := newFakeCodexSession()
	withFakeCodexRunner(t, sess)

	proc, err := dispatchCodex(context.Background(), SpawnOpts{Prompt: "go"}, "codex-review")
	if err != nil {
		t.Fatalf("dispatchCodex: %v", err)
	}

	go func() {
		sess.finish(codex.RunResult{
			Status:    codex.StatusError,
			Error:     "codex app-server exited",
			Stderr:    "fatal: no auth",
			SessionID: "thr_1",
		})
	}()

	var sawStart, sawError, sawEnd bool
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-proc.Events():
			if !ok {
				break loop
			}
			switch ev.Type {
			case "message_start":
				sawStart = true
			case "error":
				sawError = true
			case "message_end":
				sawEnd = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for events")
		}
	}
	if !sawStart || !sawError || !sawEnd {
		t.Errorf("events: start=%v error=%v end=%v; want all three", sawStart, sawError, sawEnd)
	}

	_, err = proc.Wait()
	if err == nil {
		t.Fatal("expected a non-nil error from Wait on an error result")
	}
	if !strings.Contains(err.Error(), "fatal: no auth") {
		t.Errorf("err = %q, want the app-server stderr appended for diagnosis", err)
	}
}

func TestDispatchCodex_CancelCancelsSession(t *testing.T) {
	sess := newFakeCodexSession()
	withFakeCodexRunner(t, sess)

	proc, err := dispatchCodex(context.Background(), SpawnOpts{Prompt: "hi"}, "codex")
	if err != nil {
		t.Fatalf("dispatchCodex: %v", err)
	}

	proc.Cancel()

	select {
	case <-sess.canceled:
	case <-time.After(time.Second):
		t.Fatal("Process.Cancel did not cancel the codex session")
	}

	sess.finish(codex.RunResult{Status: codex.StatusError, Error: "canceled", SessionID: "thr_1"})
	for range proc.Events() {
	}
}

func TestDispatchCodex_WrapsPromptWithPreamble(t *testing.T) {
	sess := newFakeCodexSession()
	var gotPrompt, gotAgent string
	prev := startCodexSessionFn
	startCodexSessionFn = func(_ context.Context, agent, prompt string, _ SpawnOpts) (codexSession, error) {
		gotAgent, gotPrompt = agent, prompt
		return sess, nil
	}
	t.Cleanup(func() { startCodexSessionFn = prev })

	proc, err := dispatchCodex(context.Background(), SpawnOpts{
		Prompt:      "review changes",
		Instruction: "be terse",
	}, "codex-review")
	if err != nil {
		t.Fatalf("dispatchCodex: %v", err)
	}
	t.Cleanup(func() {
		sess.finish(codex.RunResult{Status: codex.StatusSuccess})
		<-proc.done
	})

	if gotAgent != "codex-review" {
		t.Errorf("agent = %q, want codex-review", gotAgent)
	}
	if !strings.HasPrefix(gotPrompt, "be terse\n\n") {
		t.Errorf("prompt = %q, want the agent instruction prepended", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "You are subagent[codex-review]. review changes") {
		t.Errorf("prompt = %q, want the codex preamble", gotPrompt)
	}
}

func TestDispatchCodex_RequiresPrompt(t *testing.T) {
	if _, err := dispatchCodex(context.Background(), SpawnOpts{}, "codex"); err == nil {
		t.Fatal("expected an error for an empty prompt")
	}
}

func TestStartCodexSession_UnknownAgent(t *testing.T) {
	if _, err := startCodexSession(context.Background(), "bogus", "p", SpawnOpts{}); err == nil {
		t.Fatal("expected an error for an unknown codex agent")
	}
}
