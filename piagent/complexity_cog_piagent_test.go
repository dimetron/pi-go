package piagent

import (
	"context"
	"errors"
	"iter"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// These tests drive observeTurn directly with a synthetic runner, so each
// branch of the turn accounting — a nil event, an event with no content, an
// error carried on the event rather than the iteration, an abandoned range
// loop — is exercised without needing a provider. The observable contract is
// the TurnInfo the after-turn hooks receive.

// cogPair is one (event, error) the fake runner yields.
type cogPair struct {
	ev  *session.Event
	err error
}

// cogRunner yields the given pairs and records how many times it was invoked
// and with what.
type cogRunner struct {
	pairs    []cogPair
	calls    int
	lastSess string
	lastMsg  string
}

func (r *cogRunner) fn() runner {
	return func(_ context.Context, sessionID, message string) iter.Seq2[*session.Event, error] {
		r.calls++
		r.lastSess, r.lastMsg = sessionID, message
		return func(yield func(*session.Event, error) bool) {
			for _, p := range r.pairs {
				if !yield(p.ev, p.err) {
					return
				}
			}
		}
	}
}

// cogTextEvent is an ordinary assistant-text event.
func cogTextEvent(text string) *session.Event {
	return &session.Event{LLMResponse: model.LLMResponse{
		Content: genai.NewContentFromText(text, genai.RoleModel),
	}}
}

// cogToolEvent is an event carrying n function calls in one content block.
func cogToolEvent(n int) *session.Event {
	parts := make([]*genai.Part, 0, n+1)
	parts = append(parts, &genai.Part{Text: "calling"})
	for i := 0; i < n; i++ {
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{Name: "ls"}})
	}
	return &session.Event{LLMResponse: model.LLMResponse{
		Content: &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}}
}

// cogErrEvent is an event that reports a provider failure in-band.
func cogErrEvent(code, msg string) *session.Event {
	return &session.Event{LLMResponse: model.LLMResponse{ErrorCode: code, ErrorMessage: msg}}
}

// cogEmptyEvent has no content at all, which must still count as an event.
func cogEmptyEvent() *session.Event {
	return &session.Event{}
}

// cogDrain ranges the whole sequence and returns what it saw.
func cogDrain(seq iter.Seq2[*session.Event, error]) (events int, errs []error) {
	for ev, err := range seq {
		if ev != nil {
			events++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return events, errs
}

// cogAgent builds an Agent with only the hook fields observeTurn reads.
func cogAgent(before []BeforeTurnFunc, after []AfterTurnFunc) *Agent {
	return &Agent{beforeTurn: before, afterTurn: after}
}

// cogTurnCase is one runner script and the accounting it must produce.
type cogTurnCase struct {
	name          string
	pairs         []cogPair
	wantEvents    int
	wantToolCalls int
	wantErr       string
}

func checkTurnInfo(t *testing.T, info TurnInfo, tt cogTurnCase) {
	t.Helper()
	if info.SessionID != "sess-1" || info.Message != "hello" {
		t.Errorf("SessionID/Message = %q/%q, want %q/%q", info.SessionID, info.Message, "sess-1", "hello")
	}
	if info.Events != tt.wantEvents {
		t.Errorf("Events = %d, want %d", info.Events, tt.wantEvents)
	}
	if info.ToolCalls != tt.wantToolCalls {
		t.Errorf("ToolCalls = %d, want %d", info.ToolCalls, tt.wantToolCalls)
	}
	switch {
	case tt.wantErr == "" && info.Err != nil:
		t.Errorf("Err = %v, want nil", info.Err)
	case tt.wantErr != "" && (info.Err == nil || info.Err.Error() != tt.wantErr):
		t.Errorf("Err = %v, want %q", info.Err, tt.wantErr)
	}
	if info.Abandoned {
		t.Error("Abandoned = true for a fully consumed turn")
	}
	if info.Duration <= 0 {
		t.Error("Duration = 0, want the turn to be timed")
	}
}

func TestObserveTurn_AccountingAcrossEventShapes(t *testing.T) {
	tests := []cogTurnCase{
		{
			name:       "text only",
			pairs:      []cogPair{{ev: cogTextEvent("a")}, {ev: cogTextEvent("b")}},
			wantEvents: 2,
		},
		{
			name:          "function calls counted per part, not per event",
			pairs:         []cogPair{{ev: cogToolEvent(3)}, {ev: cogTextEvent("done")}},
			wantEvents:    2,
			wantToolCalls: 3,
		},
		{
			name:       "an event with no content still counts as an event",
			pairs:      []cogPair{{ev: cogEmptyEvent()}, {ev: cogTextEvent("x")}},
			wantEvents: 2,
		},
		{
			name:       "a nil event with no error counts as nothing",
			pairs:      []cogPair{{}, {ev: cogTextEvent("x")}},
			wantEvents: 1,
		},
		{
			name:       "an iteration error is reported",
			pairs:      []cogPair{{ev: cogTextEvent("x")}, {err: errors.New("iteration failed")}},
			wantEvents: 1,
			wantErr:    "iteration failed",
		},
		{
			name:       "an in-band provider failure is reported",
			pairs:      []cogPair{{ev: cogErrEvent("STREAM_ERROR", "400 Bad Request")}},
			wantEvents: 1,
			wantErr:    "400 Bad Request",
		},
		{
			name: "the first error wins over a later one",
			pairs: []cogPair{
				{err: errors.New("first")},
				{ev: cogErrEvent("API_ERROR", "second")},
			},
			wantEvents: 1,
			wantErr:    "first",
		},
		{
			name: "an iteration error on the same event does not mask the event error",
			pairs: []cogPair{
				{ev: cogErrEvent("API_ERROR", "in band"), err: errors.New("on iteration")},
			},
			wantEvents: 1,
			wantErr:    "on iteration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []TurnInfo
			ag := cogAgent(nil, []AfterTurnFunc{func(_ context.Context, info TurnInfo) {
				got = append(got, info)
			}})

			cogDrain(ag.observeTurn(t.Context(), "sess-1", "hello", (&cogRunner{pairs: tt.pairs}).fn()))

			if len(got) != 1 {
				t.Fatalf("after-turn hook ran %d times, want 1", len(got))
			}
			checkTurnInfo(t, got[0], tt)
		})
	}
}

func TestObserveTurn_AbandonedTurnReportsWhatWasConsumed(t *testing.T) {
	var got []TurnInfo
	ag := cogAgent(nil, []AfterTurnFunc{func(_ context.Context, info TurnInfo) {
		got = append(got, info)
	}})
	r := &cogRunner{pairs: []cogPair{
		{ev: cogToolEvent(1)},
		{ev: cogToolEvent(1)},
		{ev: cogToolEvent(1)},
	}}

	for range ag.observeTurn(t.Context(), "s", "m", r.fn()) {
		break
	}

	if len(got) != 1 {
		t.Fatalf("after-turn hook ran %d times, want 1", len(got))
	}
	info := got[0]
	if !info.Abandoned {
		t.Error("Abandoned = false after the caller broke out of the range loop")
	}
	if info.Events != 1 || info.ToolCalls != 1 {
		t.Errorf("Events/ToolCalls = %d/%d, want 1/1 — only the consumed event counts", info.Events, info.ToolCalls)
	}
}

func TestObserveTurn_BeforeHookFailureAbortsTheTurn(t *testing.T) {
	sentinel := errors.New("over budget")
	var ran []string
	var afterRan int

	ag := cogAgent(
		[]BeforeTurnFunc{
			func(_ context.Context, _, _ string) error { ran = append(ran, "first"); return nil },
			func(_ context.Context, _, _ string) error { ran = append(ran, "second"); return sentinel },
			func(_ context.Context, _, _ string) error { ran = append(ran, "third"); return nil },
		},
		[]AfterTurnFunc{func(context.Context, TurnInfo) { afterRan++ }},
	)
	r := &cogRunner{pairs: []cogPair{{ev: cogTextEvent("never")}}}

	events, errs := cogDrain(ag.observeTurn(t.Context(), "s", "m", r.fn()))

	if events != 0 {
		t.Errorf("yielded %d events, want 0 — the turn never reached the model", events)
	}
	if len(errs) != 1 {
		t.Fatalf("yielded %d errors, want exactly 1", len(errs))
	}
	if want := "piagent: before-turn hook: over budget"; errs[0].Error() != want {
		t.Errorf("error = %q, want %q", errs[0], want)
	}
	if !errors.Is(errs[0], sentinel) {
		t.Error("wrapped error does not unwrap to the hook's error")
	}
	if len(ran) != 2 || ran[0] != "first" || ran[1] != "second" {
		t.Errorf("hooks ran %v, want [first second] — order preserved, stop at the first error", ran)
	}
	if r.calls != 0 {
		t.Errorf("runner invoked %d times, want 0", r.calls)
	}
	if afterRan != 0 {
		t.Errorf("after-turn hook ran %d times, want 0 — the turn never started", afterRan)
	}
}

func TestObserveTurn_EveryAfterHookRunsInOrder(t *testing.T) {
	var order []string
	ag := cogAgent(
		[]BeforeTurnFunc{func(context.Context, string, string) error { return nil }},
		[]AfterTurnFunc{
			func(context.Context, TurnInfo) { order = append(order, "a") },
			func(context.Context, TurnInfo) { order = append(order, "b") },
			func(context.Context, TurnInfo) { order = append(order, "c") },
		},
	)
	r := &cogRunner{pairs: []cogPair{{ev: cogTextEvent("x")}}}

	cogDrain(ag.observeTurn(t.Context(), "s", "m", r.fn()))

	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("after-turn hooks ran %v, want [a b c]", order)
	}
}

func TestObserveTurn_HooksAreLazyAndTheRunnerSeesTheTurn(t *testing.T) {
	var before, after int
	ag := cogAgent(
		[]BeforeTurnFunc{func(context.Context, string, string) error { before++; return nil }},
		[]AfterTurnFunc{func(context.Context, TurnInfo) { after++ }},
	)
	r := &cogRunner{pairs: []cogPair{{ev: cogTextEvent("x")}}}

	seq := ag.observeTurn(t.Context(), "sess-9", "the message", r.fn())
	if before != 0 || after != 0 || r.calls != 0 {
		t.Fatalf("before/after/runner = %d/%d/%d before iteration, want 0/0/0", before, after, r.calls)
	}

	cogDrain(seq)

	if before != 1 || after != 1 || r.calls != 1 {
		t.Errorf("before/after/runner = %d/%d/%d after iteration, want 1/1/1", before, after, r.calls)
	}
	if r.lastSess != "sess-9" || r.lastMsg != "the message" {
		t.Errorf("runner saw %q/%q, want %q/%q", r.lastSess, r.lastMsg, "sess-9", "the message")
	}
}

func TestObserveTurn_NoHooksSkipsTheWrapper(t *testing.T) {
	ag := cogAgent(nil, nil)
	r := &cogRunner{pairs: []cogPair{{ev: cogTextEvent("x")}, {ev: cogTextEvent("y")}}}

	events, errs := cogDrain(ag.observeTurn(t.Context(), "s", "m", r.fn()))

	if events != 2 || len(errs) != 0 {
		t.Errorf("events/errors = %d/%d, want 2/0", events, len(errs))
	}
	if r.calls != 1 {
		t.Errorf("runner invoked %d times, want 1", r.calls)
	}
}
