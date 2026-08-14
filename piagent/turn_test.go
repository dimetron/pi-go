package piagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"google.golang.org/genai"
)

// turnRecorder collects what the after-turn hook saw, safely enough to be read
// after the turn.
type turnRecorder struct {
	mu    sync.Mutex
	turns []TurnInfo
}

func (r *turnRecorder) hook() AfterTurnFunc {
	return func(_ context.Context, info TurnInfo) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.turns = append(r.turns, info)
	}
}

func (r *turnRecorder) all() []TurnInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TurnInfo(nil), r.turns...)
}

func (r *turnRecorder) one(t *testing.T) TurnInfo {
	t.Helper()
	got := r.all()
	if len(got) != 1 {
		t.Fatalf("after-turn hook ran %d times, want exactly 1", len(got))
	}
	return got[0]
}

func TestAfterTurnReportsACompletedTurn(t *testing.T) {
	isolate(t)
	var rec turnRecorder

	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "done"}, WithAfterTurn(rec.hook()))
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "hello"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	got := rec.one(t)
	if got.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, sessionID)
	}
	if got.Message != "hello" {
		t.Errorf("Message = %q, want %q", got.Message, "hello")
	}
	if got.Err != nil {
		t.Errorf("Err = %v, want nil", got.Err)
	}
	if got.Abandoned {
		t.Error("Abandoned = true for a turn that ran to completion")
	}
	if got.Events == 0 {
		t.Error("Events = 0, want the turn's events to be counted")
	}
	if got.Duration <= 0 {
		t.Error("Duration = 0, want the turn to be timed")
	}
}

func TestAfterTurnCountsToolCalls(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	var rec turnRecorder

	ag := newTestAgent(t, &fakeLLM{
		name:     "fake",
		reply:    "one file",
		toolCall: &genai.FunctionCall{Name: "ls", Args: map[string]any{"path": workDir}},
	}, WithWorkingDir(workDir), WithAfterTurn(rec.hook()))

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "what is here?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if got := rec.one(t); got.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", got.ToolCalls)
	}
}

func TestAfterTurnReportsFailure(t *testing.T) {
	isolate(t)
	var rec turnRecorder

	ag := newTestAgent(t, &fakeLLM{name: "fake", err: errors.New("provider exploded")},
		WithAfterTurn(rec.hook()))
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "hi"); err == nil {
		t.Fatal("Ask succeeded against a failing provider")
	}

	got := rec.one(t)
	if got.Err == nil {
		t.Fatal("Err = nil, want the provider failure recorded")
	}
	if !strings.Contains(got.Err.Error(), "provider exploded") {
		t.Errorf("Err = %v, want it to carry the provider failure", got.Err)
	}
}

// TestAfterTurnFiresOnEarlyBreak is the case an explicit call at the end of the
// loop would miss: a caller that stops consuming still took a turn, and a
// metrics or audit hook that never fires for it is worse than useless.
func TestAfterTurnFiresOnEarlyBreak(t *testing.T) {
	isolate(t)
	var rec turnRecorder

	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "long reply"}, WithAfterTurn(rec.hook()))
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	for range ag.Run(t.Context(), sessionID, "hi") {
		break
	}

	got := rec.one(t)
	if !got.Abandoned {
		t.Error("Abandoned = false, want true when the caller broke out early")
	}
	if got.Duration <= 0 {
		t.Error("Duration = 0, want an abandoned turn still timed")
	}
}

func TestBeforeTurnAbortsTheTurn(t *testing.T) {
	isolate(t)
	var rec turnRecorder
	llm := &fakeLLM{name: "fake", reply: "should never be produced"}
	denied := errors.New("over budget")

	ag := newTestAgent(t, llm,
		WithBeforeTurn(func(context.Context, string, string) error { return denied }),
		WithAfterTurn(rec.hook()),
	)
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = ag.Ask(t.Context(), sessionID, "hi")
	if err == nil {
		t.Fatal("Ask succeeded although the before-turn hook denied it")
	}
	if !errors.Is(err, denied) {
		t.Errorf("err = %v, want it to wrap the hook's error", err)
	}
	if llm.callCount() != 0 {
		t.Errorf("the model was called %d times; an aborted turn must not reach it", llm.callCount())
	}
	// The turn never started, so there is nothing to report afterwards.
	if got := rec.all(); len(got) != 0 {
		t.Errorf("after-turn hook ran %d times for an aborted turn, want 0", len(got))
	}
}

func TestBeforeTurnHooksRunInOrderAndStopAtTheFirstError(t *testing.T) {
	isolate(t)
	var ran []string
	stop := errors.New("denied")

	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"},
		WithBeforeTurn(func(context.Context, string, string) error {
			ran = append(ran, "first")
			return nil
		}),
		WithBeforeTurn(func(context.Context, string, string) error {
			ran = append(ran, "blocker")
			return stop
		}),
		WithBeforeTurn(func(context.Context, string, string) error {
			ran = append(ran, "never")
			return nil
		}),
	)
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "hi"); !errors.Is(err, stop) {
		t.Fatalf("Ask err = %v, want the blocker's error", err)
	}
	if strings.Join(ran, ",") != "first,blocker" {
		t.Errorf("hooks ran %v, want the chain to stop at the failing one", ran)
	}
}

// TestBeforeTurnSeesTheMessage pins the reason these hooks are not built on
// internal/agent's PreTurnHook, whose signature carries only the session ID:
// admission control that cannot read the outgoing message cannot moderate it.
func TestBeforeTurnSeesTheMessage(t *testing.T) {
	isolate(t)
	var gotSession, gotMessage string

	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"},
		WithBeforeTurn(func(_ context.Context, sessionID, message string) error {
			gotSession, gotMessage = sessionID, message
			return nil
		}))
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "the message"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if gotSession != sessionID || gotMessage != "the message" {
		t.Errorf("hook saw (%q, %q), want (%q, %q)", gotSession, gotMessage, sessionID, "the message")
	}
}

// TestTurnHooksDoNotFireUntilIterated pins the laziness: Run returns a
// sequence, and a sequence nobody ranges over is not a turn. A hook that fired
// at call time would record turns that never happened.
func TestTurnHooksDoNotFireUntilIterated(t *testing.T) {
	isolate(t)
	var rec turnRecorder
	before := 0

	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"},
		WithBeforeTurn(func(context.Context, string, string) error { before++; return nil }),
		WithAfterTurn(rec.hook()))
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	seq := ag.Run(t.Context(), sessionID, "hi")
	if before != 0 || len(rec.all()) != 0 {
		t.Fatalf("hooks fired before iteration: before=%d after=%d", before, len(rec.all()))
	}

	//nolint:revive // draining the sequence is the point
	for range seq {
	}
	if before != 1 {
		t.Errorf("before-turn ran %d times, want 1", before)
	}
	if len(rec.all()) != 1 {
		t.Errorf("after-turn ran %d times, want 1", len(rec.all()))
	}
}

func TestRunStreamingFiresTurnHooksToo(t *testing.T) {
	isolate(t)
	var rec turnRecorder

	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "streamed"}, WithAfterTurn(rec.hook()))
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	//nolint:revive // draining the sequence is the point
	for range ag.RunStreaming(t.Context(), sessionID, "hi") {
	}

	if got := rec.one(t); got.Events == 0 {
		t.Error("Events = 0 for a streaming turn")
	}
}

// TestNoHooksLeavesTheSequenceUnwrapped pins that the common case pays nothing:
// with no hooks registered the agent hands back ADK's own sequence.
func TestNoHooksLeavesTheSequenceUnwrapped(t *testing.T) {
	isolate(t)
	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"})
	if len(ag.beforeTurn) != 0 || len(ag.afterTurn) != 0 {
		t.Fatal("the test agent registered turn hooks; this test needs none")
	}

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got, err := ag.Ask(t.Context(), sessionID, "hi")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got != "ok" {
		t.Errorf("Ask = %q, want %q", got, "ok")
	}
}
