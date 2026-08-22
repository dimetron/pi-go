package atif

import (
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// These tests pin the exact JSON shape ConvertEvent emits. ATIF steps are
// written to disk as trajectory files and read back by external tooling, so a
// change in emitted shape silently invalidates previously recorded
// trajectories. Every golden below was captured by running the same cases
// against the pre-refactor ConvertEvent, so a passing run proves the
// flattening is a no-op rather than proving the new code agrees with itself.

var cogRefTime = time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)

// cogEvent builds a session event with full control over the fields mapSource
// consults: author, turn completion, finish reason and content role.
func cogEvent(author string, turnComplete bool, finish genai.FinishReason, role string, parts ...*genai.Part) *session.Event {
	ev := &session.Event{}
	ev.Author = author
	ev.Timestamp = cogRefTime
	ev.TurnComplete = turnComplete
	ev.FinishReason = finish
	ev.Content = &genai.Content{Role: role, Parts: parts}
	return ev
}

func TestConvertEventGoldenShape(t *testing.T) {
	tests := []struct {
		name  string
		event *session.Event
		want  string
	}{
		{
			name:  "user text becomes a plain string message",
			event: cogEvent("user", false, "", "user", &genai.Part{Text: "Hello, world!"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"user","message":"Hello, world!"}]`,
		},
		{
			name:  "model text maps to the agent source",
			event: cogEvent("model", false, "", "model", &genai.Part{Text: "hi"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"hi"}]`,
		},
		{
			name:  "a thought-only event yields no step",
			event: cogEvent("model", false, "", "model", &genai.Part{Text: "think", Thought: true}),
			want:  `null`,
		},
		{
			name:  "an entirely empty part yields no step",
			event: cogEvent("model", false, "", "model", &genai.Part{}),
			want:  `null`,
		},
		{
			name:  "two text parts become a content-part array",
			event: cogEvent("model", false, "", "model", &genai.Part{Text: "a"}, &genai.Part{Text: "b"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]`,
		},
		{
			name:  "a function call carries its arguments through",
			event: cogEvent("model", false, "", "model", &genai.Part{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"path": "/x"}}}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"","tool_calls":[{"tool_call_id":"c1","function_name":"read","arguments":{"path":"/x"}}]}]`,
		},
		{
			name:  "nil call arguments become an empty object, never null",
			event: cogEvent("model", false, "", "model", &genai.Part{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "ls"}}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"","tool_calls":[{"tool_call_id":"c2","function_name":"ls","arguments":{}}]}]`,
		},
		{
			name:  "a function response becomes an observation result",
			event: cogEvent("user", false, "", "user", &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"out": "ok"}}}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"user","message":"","observation":{"results":[{"source_call_id":"c1","content":{"out":"ok"}}]}}]`,
		},
		{
			name:  "an id-less function response falls back to the function name",
			event: cogEvent("user", false, "", "user", &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "read", Response: map[string]any{"out": "ok"}}}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"user","message":"","observation":{"results":[{"source_call_id":"read","content":{"out":"ok"}}]}}]`,
		},
		{
			name: "two responses share one observation, in part order",
			event: cogEvent("user", false, "", "user",
				&genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "c1", Response: map[string]any{"a": float64(1)}}},
				&genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "c2", Response: map[string]any{"b": float64(2)}}}),
			want: `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"user","message":"","observation":{"results":[{"source_call_id":"c1","content":{"a":1}},{"source_call_id":"c2","content":{"b":2}}]}}]`,
		},
		{
			name: "text, thought, call and response in one event",
			event: cogEvent("model", false, "", "model",
				&genai.Part{Text: "doing it"},
				&genai.Part{Text: "skip", Thought: true},
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: "c9", Name: "bash", Args: map[string]any{"cmd": "ls"}}},
				&genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "c9", Response: map[string]any{"out": "f"}}}),
			want: `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"doing it","tool_calls":[{"tool_call_id":"c9","function_name":"bash","arguments":{"cmd":"ls"}}],"observation":{"results":[{"source_call_id":"c9","content":{"out":"f"}}]}}]`,
		},
		{
			name:  "an unknown author with a model content role is an agent",
			event: cogEvent("pi-custom", false, "", "model", &genai.Part{Text: "x"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"x"}]`,
		},
		{
			name:  "an unknown author completing on STOP with text is an agent",
			event: cogEvent("weird", true, genai.FinishReason("STOP"), "", &genai.Part{Text: "final"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"final"}]`,
		},
		{
			name:  "STOP with only thought text does not reach the agent fallback",
			event: cogEvent("weird", true, genai.FinishReason("STOP"), "", &genai.Part{Text: "t", Thought: true}),
			want:  `null`,
		},
		{
			name:  "a non-STOP finish reason stays on the system source",
			event: cogEvent("weird", true, genai.FinishReason("MAX_TOKENS"), "", &genai.Part{Text: "final"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"system","message":"final"}]`,
		},
		{
			name:  "the assistant author maps to agent",
			event: cogEvent("assistant", false, "", "", &genai.Part{Text: "a"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"a"}]`,
		},
		{
			name:  "the agent author maps to agent",
			event: cogEvent("agent", false, "", "", &genai.Part{Text: "a"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"a"}]`,
		},
		{
			name:  "the pi author maps to agent",
			event: cogEvent("pi", false, "", "", &genai.Part{Text: "a"}),
			want:  `[{"step_id":7,"timestamp":"2026-04-05T10:30:00Z","source":"agent","message":"a"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(ConvertEvent(tt.event, 7))
			if err != nil {
				t.Fatalf("marshal steps: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("ConvertEvent JSON mismatch\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// TestConvertEventNilInputs pins the three shapes that produce no step at all
// before any part is inspected.
func TestConvertEventNilInputs(t *testing.T) {
	empty := &session.Event{}
	emptyParts := &session.Event{}
	emptyParts.Content = &genai.Content{Parts: []*genai.Part{}}

	tests := []struct {
		name  string
		event *session.Event
	}{
		{"nil event", nil},
		{"nil content", empty},
		{"zero parts", emptyParts},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if steps := ConvertEvent(tt.event, 1); steps != nil {
				t.Errorf("ConvertEvent = %v, want nil", steps)
			}
		})
	}
}

// TestConvertEventStepIDIsTheStartingID pins that the returned step carries the
// caller's starting step ID unchanged.
func TestConvertEventStepIDIsTheStartingID(t *testing.T) {
	for _, id := range []int{0, 1, 42} {
		steps := ConvertEvent(cogEvent("user", false, "", "user", &genai.Part{Text: "x"}), id)
		if len(steps) != 1 {
			t.Fatalf("id %d: got %d steps, want 1", id, len(steps))
		}
		if steps[0].StepID != id {
			t.Errorf("StepID = %d, want %d", steps[0].StepID, id)
		}
	}
}
