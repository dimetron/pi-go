package pirpc

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/logger"
)

// emitCapture builds a Server that writes NDJSON into a buffer, which is all
// the part-emitting helpers need — no agent, no stdin.
func emitCapture(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return NewServer(Config{Out: &buf}), &buf
}

// decodeEmitted parses the buffer's NDJSON lines into maps.
func decodeEmitted(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("emitted line is not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func modelEvent(author string, parts ...*genai.Part) *session.Event {
	ev := &session.Event{}
	ev.Author = author
	ev.Content = &genai.Content{Role: string(genai.RoleModel), Parts: parts}
	return ev
}

// TestEmitPartTextRouting pins the switch that decides which delta kind a
// text part becomes. The thinking case is selected by role, so a "thinking"
// role with empty text must fall through to neither case.
func TestEmitPartTextRouting(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		text      string
		wantKind  string // "" means nothing emitted
		wantDelta string
	}{
		{"model text becomes a text delta", string(genai.RoleModel), "hello", "text_delta", "hello"},
		{"thinking role becomes a thinking delta", "thinking", "pondering", "thinking_delta", "pondering"},
		{"empty text emits nothing", string(genai.RoleModel), "", "", ""},
		{"empty text on a thinking event emits nothing", "thinking", "", "", ""},
		{"an unknown role still emits text", "tool", "from a tool", "text_delta", "from a tool"},
		{"whitespace is text, not emptiness", string(genai.RoleModel), " ", "text_delta", " "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, buf := emitCapture(t)
			ev := &session.Event{}
			ev.Author = "assistant"
			ev.Content = &genai.Content{Role: tt.role, Parts: []*genai.Part{{Text: tt.text}}}

			var dedup agent.StreamDedup
			dedup.BeginEvent(ev)
			s.emitPart(ev, ev.Content.Parts[0], &dedup)

			got := decodeEmitted(t, buf)
			if tt.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("emitted %v, want nothing", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("emitted %d events, want 1: %v", len(got), got)
			}
			ame, ok := got[0]["assistantMessageEvent"].(map[string]any)
			if !ok {
				t.Fatalf("event has no assistantMessageEvent: %v", got[0])
			}
			if ame["type"] != tt.wantKind {
				t.Errorf("delta kind = %v, want %v", ame["type"], tt.wantKind)
			}
			if ame["delta"] != tt.wantDelta {
				t.Errorf("delta = %q, want %q", ame["delta"], tt.wantDelta)
			}
		})
	}
}

// TestEmitPartDedupSkipDropsWholePart is the branch most at risk from the
// extraction: inside the loop the dedup skip was a `continue`, which also
// skipped the tool-call and tool-result blocks below it. As an extracted
// function that has to be a `return`, not a fallthrough.
func TestEmitPartDedupSkipDropsWholePart(t *testing.T) {
	s, buf := emitCapture(t)
	var dedup agent.StreamDedup

	// A partial event marks the stream as having streamed deltas.
	partial := modelEvent("assistant", &genai.Part{Text: "hel"})
	partial.Partial = true
	dedup.BeginEvent(partial)
	s.emitPart(partial, partial.Content.Parts[0], &dedup)

	// The aggregate re-send carries the same text *and* a function call on the
	// same part. The text is a duplicate; the call must be dropped with it.
	aggregate := modelEvent("assistant", &genai.Part{
		Text:         "hel",
		FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read"},
	})
	dedup.BeginEvent(aggregate)
	s.emitPart(aggregate, aggregate.Content.Parts[0], &dedup)

	got := decodeEmitted(t, buf)
	if len(got) != 1 {
		t.Fatalf("emitted %d events, want only the first delta: %v", len(got), got)
	}
	for _, ev := range got {
		if ev["type"] == "tool_execution_start" {
			t.Error("a skipped text part still emitted its tool call; the `continue` became a fallthrough")
		}
	}
}

// TestEmitPartTextAndToolCallTogether is the control for the test above: when
// the text is *not* a duplicate, both the delta and the tool call go out.
func TestEmitPartTextAndToolCallTogether(t *testing.T) {
	s, buf := emitCapture(t)
	var dedup agent.StreamDedup

	ev := modelEvent("assistant", &genai.Part{
		Text:         "calling a tool",
		FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"path": "a.go"}},
	})
	dedup.BeginEvent(ev)
	s.emitPart(ev, ev.Content.Parts[0], &dedup)

	got := decodeEmitted(t, buf)
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2 (delta + tool call): %v", len(got), got)
	}
	if got[0]["type"] != "message_update" {
		t.Errorf("first event = %v, want message_update", got[0]["type"])
	}
	if got[1]["type"] != "tool_execution_start" {
		t.Errorf("second event = %v, want tool_execution_start", got[1]["type"])
	}
}

// TestEmitPartCallAndResponseTogether covers a part carrying both halves,
// which the original emitted in order: start then end.
func TestEmitPartCallAndResponseTogether(t *testing.T) {
	s, buf := emitCapture(t)
	var dedup agent.StreamDedup

	ev := modelEvent("assistant", &genai.Part{
		FunctionCall:     &genai.FunctionCall{ID: "c1", Name: "read"},
		FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"ok": true}},
	})
	dedup.BeginEvent(ev)
	s.emitPart(ev, ev.Content.Parts[0], &dedup)

	got := decodeEmitted(t, buf)
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2: %v", len(got), got)
	}
	if got[0]["type"] != "tool_execution_start" || got[1]["type"] != "tool_execution_end" {
		t.Errorf("order = %v then %v, want start then end", got[0]["type"], got[1]["type"])
	}
}

// TestEmitToolCallShape pins the tool_execution_start payload, including that
// a provider-assigned ID is used verbatim and an absent one is synthesized.
func TestEmitToolCallShape(t *testing.T) {
	tests := []struct {
		name   string
		fc     *genai.FunctionCall
		wantID string
	}{
		{"provider id is used verbatim", &genai.FunctionCall{ID: "call_abc", Name: "grep"}, "call_abc"},
		{"missing id is synthesized from the name", &genai.FunctionCall{Name: "grep"}, "grep-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, buf := emitCapture(t)
			s.emitToolCall("assistant", tt.fc)

			got := decodeEmitted(t, buf)
			if len(got) != 1 {
				t.Fatalf("emitted %d events, want 1", len(got))
			}
			if got[0]["type"] != "tool_execution_start" {
				t.Errorf("type = %v, want tool_execution_start", got[0]["type"])
			}
			if got[0]["toolCallId"] != tt.wantID {
				t.Errorf("toolCallId = %v, want %v", got[0]["toolCallId"], tt.wantID)
			}
			if got[0]["toolName"] != tt.fc.Name {
				t.Errorf("toolName = %v, want %v", got[0]["toolName"], tt.fc.Name)
			}
		})
	}
}

// TestEmitToolResultShape pins the tool_execution_end payload. isError is
// hard-coded false; pi-acp uses it to pick the card style, so a change there
// would be user-visible.
func TestEmitToolResultShape(t *testing.T) {
	s, buf := emitCapture(t)
	s.emitToolResult("assistant", &genai.FunctionResponse{
		ID: "call_abc", Name: "grep", Response: map[string]any{"hits": float64(3)},
	})

	got := decodeEmitted(t, buf)
	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1", len(got))
	}
	if got[0]["type"] != "tool_execution_end" {
		t.Errorf("type = %v, want tool_execution_end", got[0]["type"])
	}
	if got[0]["toolCallId"] != "call_abc" {
		t.Errorf("toolCallId = %v, want call_abc", got[0]["toolCallId"])
	}
	if got[0]["isError"] != false {
		t.Errorf("isError = %v, want false", got[0]["isError"])
	}
	res, ok := got[0]["result"].(map[string]any)
	if !ok || res["hits"] != float64(3) {
		t.Errorf("result = %v, want the response passed through", got[0]["result"])
	}
}

// TestEmitToolIDsPairAcrossCallAndResult is the invariant the synthesized-ID
// path exists for: start and end must agree, or pi-acp cannot pair them into
// one card. Two unidentified calls must not collide either.
func TestEmitToolIDsPairAcrossCallAndResult(t *testing.T) {
	s, buf := emitCapture(t)
	s.emitToolCall("assistant", &genai.FunctionCall{Name: "read"})
	s.emitToolCall("assistant", &genai.FunctionCall{Name: "read"})

	got := decodeEmitted(t, buf)
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2", len(got))
	}
	if got[0]["toolCallId"] == got[1]["toolCallId"] {
		t.Errorf("two unidentified calls share the id %v", got[0]["toolCallId"])
	}
	if got[0]["toolCallId"] != "read-1" || got[1]["toolCallId"] != "read-2" {
		t.Errorf("ids = %v, %v; want read-1, read-2", got[0]["toolCallId"], got[1]["toolCallId"])
	}
}

// TestEmitPartLogging checks the session-log side of each branch, which the
// extraction moved along with the emit calls. A nil log must stay harmless.
func TestEmitPartLogging(t *testing.T) {
	t.Run("nil log is tolerated on every branch", func(t *testing.T) {
		s, _ := emitCapture(t)
		if s.log != nil {
			t.Fatal("expected a nil log on a Config without one")
		}
		var dedup agent.StreamDedup
		for _, part := range []*genai.Part{
			{Text: "text"},
			{FunctionCall: &genai.FunctionCall{ID: "c", Name: "n"}},
			{FunctionResponse: &genai.FunctionResponse{ID: "c", Name: "n", Response: map[string]any{}}},
		} {
			ev := modelEvent("assistant", part)
			dedup.BeginEvent(ev)
			s.emitPart(ev, part, &dedup) // must not panic
		}
		thinking := &session.Event{}
		thinking.Author = "assistant"
		thinking.Content = &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: "hmm"}}}
		s.emitPart(thinking, thinking.Content.Parts[0], &dedup)
	})

	t.Run("every branch reaches the session log", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		log, err := logger.New()
		if err != nil {
			t.Fatalf("logger.New() error = %v", err)
		}
		s, _ := emitCapture(t)
		s.log = log

		var dedup agent.StreamDedup

		thinking := &session.Event{}
		thinking.Author = "assistant"
		thinking.Content = &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: "weighing options"}}}
		dedup.BeginEvent(thinking)
		s.emitPart(thinking, thinking.Content.Parts[0], &dedup)

		text := modelEvent("assistant", &genai.Part{Text: "the answer"})
		dedup.BeginEvent(text)
		s.emitPart(text, text.Content.Parts[0], &dedup)

		call := modelEvent("assistant", &genai.Part{
			FunctionCall: &genai.FunctionCall{ID: "c1", Name: "grepper"},
		})
		dedup.BeginEvent(call)
		s.emitPart(call, call.Content.Parts[0], &dedup)

		result := modelEvent("assistant", &genai.Part{
			FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "grepper", Response: map[string]any{"hits": 3}},
		})
		dedup.BeginEvent(result)
		s.emitPart(result, result.Content.Parts[0], &dedup)

		if err := log.Close(); err != nil {
			t.Fatalf("closing log: %v", err)
		}
		contents, err := os.ReadFile(log.Path())
		if err != nil {
			t.Fatalf("reading log: %v", err)
		}
		for _, want := range []string{"weighing options", "the answer", "grepper"} {
			if !bytes.Contains(contents, []byte(want)) {
				t.Errorf("session log is missing %q:\n%s", want, contents)
			}
		}
	})
}

// TestEmitToolResultUnmarshalableResponseStillEmits pins the inner guard on
// the log write: a response that cannot be marshaled skips the log line but
// must not stop the tool_execution_end event.
func TestEmitToolResultUnmarshalableResponseStillEmits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}
	s, buf := emitCapture(t)
	s.log = log

	// A channel value is not JSON-marshalable.
	s.emitToolResult("assistant", &genai.FunctionResponse{
		ID: "c1", Name: "weird", Response: map[string]any{"ch": make(chan int)},
	})

	// emit itself drops the unmarshalable event, so nothing lands on the wire
	// — but the call must return rather than panic, and the log must be clean.
	if err := log.Close(); err != nil {
		t.Fatalf("closing log: %v", err)
	}
	contents, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if bytes.Contains(contents, []byte("chan")) {
		t.Errorf("unmarshalable response reached the log:\n%s", contents)
	}
	_ = buf
}
