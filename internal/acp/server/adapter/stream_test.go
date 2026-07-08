package adapter

import (
	"context"
	"errors"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// fakeUpdater captures every update sent through it for assertion.
// It is safe for concurrent use by multiple goroutines.
type fakeUpdater struct {
	mu      sync.Mutex
	updates []acp.SessionUpdate
	err     error
}

func (f *fakeUpdater) Update(_ context.Context, u acp.SessionUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, u)
	return f.err
}

func textEvent(role string, parts ...*genai.Part) *adksession.Event {
	return &adksession.Event{
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Role: role, Parts: parts},
		},
	}
}

func textPart(s string) *genai.Part { return &genai.Part{Text: s} }

func thoughtPart(s string) *genai.Part { return &genai.Part{Text: s, Thought: true} }

func TestStream_SingleTextChunk(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	if err := s.OnEvent(context.Background(), textEvent("model", textPart("hello"))); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	if got, want := len(up.updates), 1; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	chunk := up.updates[0].AgentMessageChunk
	if chunk == nil {
		t.Fatalf("update is not an AgentMessageChunk: %+v", up.updates[0])
	}
	if chunk.Content.Text == nil || chunk.Content.Text.Text != "hello" {
		t.Fatalf("chunk text = %+v, want %q", chunk.Content.Text, "hello")
	}
	if got, want := s.Final(), "hello"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestStream_MultipleChunksPreserveOrder(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	chunks := []string{"one ", "two ", "three"}
	for _, c := range chunks {
		if err := s.OnEvent(context.Background(), textEvent("model", textPart(c))); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}

	if got, want := len(up.updates), len(chunks); got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	for i, want := range chunks {
		got := up.updates[i].AgentMessageChunk
		if got == nil || got.Content.Text == nil || got.Content.Text.Text != want {
			t.Fatalf("updates[%d] = %+v, want text %q", i, got, want)
		}
	}
	if got, want := s.Final(), "one two three"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestStream_MultiPartEventEmitsEachAsChunk(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	ev := textEvent("model", textPart("alpha"), textPart("-beta"))
	if err := s.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	if got, want := len(up.updates), 2; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	if got, want := s.Final(), "alpha-beta"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestStream_IgnoresUserRoleAndEmptyAndNil(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	cases := []struct {
		name string
		ev   *adksession.Event
	}{
		{"nil event", nil},
		{"nil content", &adksession.Event{}},
		{"user role", textEvent("user", textPart("prompt"))},
		{"empty text", textEvent("model", textPart(""))},
		{"nil part", textEvent("model", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.OnEvent(context.Background(), tc.ev); err != nil {
				t.Fatalf("OnEvent: %v", err)
			}
		})
	}

	if len(up.updates) != 0 {
		t.Fatalf("unexpected updates emitted: %+v", up.updates)
	}
	if got := s.Final(); got != "" {
		t.Fatalf("Final() = %q, want empty", got)
	}
}

func TestStream_ThoughtPartsEmitThoughtChunkAndStayOutOfFinal(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	ev := textEvent("model", thoughtPart("reasoning"), textPart("answer"))
	if err := s.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	if got, want := len(up.updates), 2; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	thought := up.updates[0].AgentThoughtChunk
	if thought == nil || thought.Content.Text == nil || thought.Content.Text.Text != "reasoning" {
		t.Fatalf("expected thought chunk 'reasoning', got %+v", up.updates[0])
	}
	msg := up.updates[1].AgentMessageChunk
	if msg == nil || msg.Content.Text == nil || msg.Content.Text.Text != "answer" {
		t.Fatalf("expected message chunk 'answer', got %+v", up.updates[1])
	}
	if got, want := s.Final(), "answer"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestStream_EmptyThoughtPartEmitsNothing(t *testing.T) {
	// ADK occasionally emits zero-length thought parts between reasoning
	// segments. They should not produce empty panel entries in Zed.
	up := &fakeUpdater{}
	s := New(up)

	if err := s.OnEvent(context.Background(), textEvent("model", thoughtPart(""))); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(up.updates) != 0 {
		t.Fatalf("unexpected updates: %+v", up.updates)
	}
	if got := s.Final(); got != "" {
		t.Fatalf("Final() = %q, want empty", got)
	}
}

func TestStream_ThoughtUpdaterErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	up := &fakeUpdater{err: sentinel}
	s := New(up)

	err := s.OnEvent(context.Background(), textEvent("model", thoughtPart("x")))
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want to wrap %v", err, sentinel)
	}
}

func TestStream_ThoughtWithNilUpdaterIsNoOp(t *testing.T) {
	s := New(nil)
	if err := s.OnEvent(context.Background(), textEvent("model", thoughtPart("silent"))); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if got := s.Final(); got != "" {
		t.Fatalf("Final() = %q, want empty", got)
	}
}

func TestStream_SkipsFunctionCallAndResponseParts(t *testing.T) {
	// Zed-04 will surface tool calls via Before/After tool callbacks; those
	// parts must not be forwarded as assistant text here.
	up := &fakeUpdater{}
	s := New(up)

	call := &genai.Part{FunctionCall: &genai.FunctionCall{Name: "read", Args: map[string]any{"path": "a.txt"}}}
	resp := &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "read", Response: map[string]any{"ok": true}}}
	ev := textEvent("model", call, resp, textPart("summary"))
	if err := s.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	if got, want := len(up.updates), 1; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	if got, want := s.Final(), "summary"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestEmitThought_EmptyTextIsNoOp(t *testing.T) {
	// emitThought guards against empty text as a safety check even though
	// OnEvent pre-filters them; test it directly to keep the guard covered.
	up := &fakeUpdater{}
	s := New(up)
	if err := s.emitThought(context.Background(), ""); err != nil {
		t.Fatalf("emitThought(\"\") = %v, want nil", err)
	}
	if len(up.updates) != 0 {
		t.Fatalf("unexpected updates for empty thought: %+v", up.updates)
	}
}

func TestStream_NilUpdaterStillAccumulates(t *testing.T) {
	s := New(nil)
	if err := s.OnEvent(context.Background(), textEvent("model", textPart("hi"))); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if got, want := s.Final(), "hi"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}

func TestStream_UpdaterErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	up := &fakeUpdater{err: sentinel}
	s := New(up)

	err := s.OnEvent(context.Background(), textEvent("model", textPart("x")))
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want to wrap %v", err, sentinel)
	}
}

func TestStream_FinalTrimsSurroundingWhitespace(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	if err := s.OnEvent(context.Background(), textEvent("model", textPart("  padded  "))); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if got, want := s.Final(), "padded"; got != want {
		t.Fatalf("Final() = %q, want %q", got, want)
	}
}
