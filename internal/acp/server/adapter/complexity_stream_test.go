package adapter

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// partialTextEvent builds the delta form of a model event: ADK marks streamed
// chunks Partial and then re-yields the turn once more as a non-partial
// aggregate, which the dedup drops.
func partialTextEvent(role string, parts ...*genai.Part) *adksession.Event {
	return &adksession.Event{
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Role: role, Parts: parts},
			Partial: true,
		},
	}
}

// chunkForPart is the part-classification chain OnEvent used to inline. The
// table pins one row per branch the original loop had.
func TestChunkForPart(t *testing.T) {
	callPart := &genai.Part{FunctionCall: &genai.FunctionCall{Name: "read"}}
	respPart := &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "read"}}

	tests := []struct {
		name          string
		part          *genai.Part
		wantOK        bool
		wantThought   bool
		wantText      string
		wantFinalText string
	}{
		{name: "nil part is dropped", part: nil},
		{name: "function call is dropped", part: callPart},
		{name: "function response is dropped", part: respPart},
		{name: "empty thought is dropped", part: thoughtPart("")},
		{name: "thought is emitted but not accumulated", part: thoughtPart("thinking"), wantOK: true, wantThought: true, wantText: "thinking"},
		{name: "empty text is dropped", part: textPart("")},
		{name: "text is emitted and accumulated", part: textPart("hi"), wantOK: true, wantText: "hi", wantFinalText: "hi"},
		{
			name:          "a part carrying both a call and text is dropped as plumbing",
			part:          &genai.Part{Text: "ignored", FunctionCall: &genai.FunctionCall{Name: "read"}},
			wantOK:        false,
			wantFinalText: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(nil)
			ev := textEvent("model", tt.part)

			chunk, ok := s.chunkForPart(ev, tt.part)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if chunk.thought != tt.wantThought || chunk.text != tt.wantText {
				t.Fatalf("chunk = %+v, want {thought:%v text:%q}", chunk, tt.wantThought, tt.wantText)
			}
			if got := s.Final(); got != tt.wantFinalText {
				t.Fatalf("Final() = %q, want %q", got, tt.wantFinalText)
			}
		})
	}
}

// The aggregate re-send arrives as a non-partial event repeating text already
// delivered as deltas; chunkForPart must drop it so the reply is not doubled.
func TestChunkForPart_DropsAggregateAfterDeltas(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	delta := partialTextEvent("model", textPart("hel"))
	if err := s.OnEvent(context.Background(), delta); err != nil {
		t.Fatalf("OnEvent(delta): %v", err)
	}
	aggregate := textEvent("model", textPart("hello"))
	if err := s.OnEvent(context.Background(), aggregate); err != nil {
		t.Fatalf("OnEvent(aggregate): %v", err)
	}

	if got, want := len(up.updates), 1; got != want {
		t.Fatalf("updates = %d, want %d (aggregate must be dropped)", got, want)
	}
	if got, want := s.Final(), "hel"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

// A user event between turns resets the dedup, so the next model aggregate
// passes through even though the previous turn streamed deltas.
func TestChunkForPart_UserEventResetsDedup(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	if err := s.OnEvent(context.Background(), partialTextEvent("model", textPart("first"))); err != nil {
		t.Fatalf("OnEvent(delta): %v", err)
	}
	// OnEvent returns early on user events, so feed the reset straight to the
	// collector; the event carries no parts, only the role that resets dedup.
	s.collectChunks(textEvent("user"))
	if err := s.OnEvent(context.Background(), textEvent("model", textPart("second"))); err != nil {
		t.Fatalf("OnEvent(aggregate): %v", err)
	}

	if got, want := len(up.updates), 2; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	if got, want := s.Final(), "firstsecond"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

// collectChunks returns the batch in part order and the updater read under the
// same lock that guarded the accumulation.
func TestCollectChunks_OrdersBatchAndReturnsUpdater(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	ev := textEvent("model",
		nil,
		thoughtPart("why"),
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "read"}},
		textPart("because"),
		textPart(""),
	)
	batch, updater := s.collectChunks(ev)

	if updater != SessionUpdater(up) {
		t.Fatalf("updater = %v, want the stream's updater", updater)
	}
	want := []pendingChunk{{thought: true, text: "why"}, {text: "because"}}
	if len(batch) != len(want) {
		t.Fatalf("batch = %+v, want %+v", batch, want)
	}
	for i := range want {
		if batch[i] != want[i] {
			t.Fatalf("batch[%d] = %+v, want %+v", i, batch[i], want[i])
		}
	}
	if got, want := s.Final(), "because"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestCollectChunks_NilUpdaterIsReturnedAsNil(t *testing.T) {
	s := New(nil)
	batch, updater := s.collectChunks(textEvent("model", textPart("x")))
	if updater != nil {
		t.Fatalf("updater = %v, want nil", updater)
	}
	if len(batch) != 1 {
		t.Fatalf("batch = %+v, want one chunk", batch)
	}
}

func TestEmitChunks_SendsBothChunkKindsInOrder(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	batch := []pendingChunk{{thought: true, text: "why"}, {text: "because"}}
	if err := s.emitChunks(context.Background(), up, batch); err != nil {
		t.Fatalf("emitChunks: %v", err)
	}

	if got, want := len(up.updates), 2; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	if th := up.updates[0].AgentThoughtChunk; th == nil || th.Content.Text == nil || th.Content.Text.Text != "why" {
		t.Fatalf("updates[0] = %+v, want thought chunk %q", up.updates[0], "why")
	}
	if msg := up.updates[1].AgentMessageChunk; msg == nil || msg.Content.Text == nil || msg.Content.Text.Text != "because" {
		t.Fatalf("updates[1] = %+v, want message chunk %q", up.updates[1], "because")
	}
}

func TestEmitChunks_EmptyBatchIsNoOp(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)
	if err := s.emitChunks(context.Background(), up, nil); err != nil {
		t.Fatalf("emitChunks(nil): %v", err)
	}
	if len(up.updates) != 0 {
		t.Fatalf("unexpected updates: %+v", up.updates)
	}
}

// A nil updater discards message chunks rather than panicking, matching what
// New(nil) promises.
func TestEmitChunks_NilUpdaterDiscardsMessageChunks(t *testing.T) {
	s := New(nil)
	if err := s.emitChunks(context.Background(), nil, []pendingChunk{{text: "dropped"}}); err != nil {
		t.Fatalf("emitChunks: %v", err)
	}
}

// The first failing update aborts the batch: the chunk after it must not be
// sent, whichever kind failed.
func TestEmitChunks_StopsAtFirstError(t *testing.T) {
	sentinel := errors.New("boom")

	for _, tt := range []struct {
		name  string
		batch []pendingChunk
	}{
		{"thought fails first", []pendingChunk{{thought: true, text: "why"}, {text: "because"}}},
		{"message fails first", []pendingChunk{{text: "because"}, {thought: true, text: "why"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			up := &failOnceUpdater{err: sentinel}
			s := New(up)
			err := s.emitChunks(context.Background(), up, tt.batch)
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want to wrap %v", err, sentinel)
			}
			if got, want := up.calls, 1; got != want {
				t.Fatalf("Update calls = %d, want %d", got, want)
			}
		})
	}
}

// failOnceUpdater fails on its first call and counts how many it received, so
// a test can prove the batch stopped rather than merely reported an error.
type failOnceUpdater struct {
	calls int
	err   error
}

func (f *failOnceUpdater) Update(_ context.Context, _ acp.SessionUpdate) error {
	f.calls++
	if f.calls == 1 {
		return f.err
	}
	return nil
}
