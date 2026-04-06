package atif

import (
	"testing"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestConvertEvent_NilEvent(t *testing.T) {
	if steps := ConvertEvent(nil, 1); steps != nil {
		t.Fatalf("expected nil, got %v", steps)
	}
}

func TestConvertEvent_NilContent(t *testing.T) {
	ev := &session.Event{}
	if steps := ConvertEvent(ev, 1); steps != nil {
		t.Fatalf("expected nil, got %v", steps)
	}
}

func TestConvertEvent_EmptyParts(t *testing.T) {
	ev := &session.Event{}
	ev.Content = &genai.Content{Parts: []*genai.Part{}}
	if steps := ConvertEvent(ev, 1); steps != nil {
		t.Fatalf("expected nil, got %v", steps)
	}
}

func TestConvertEvent_UserTextMessage(t *testing.T) {
	ev := makeTextEvent("user", "Hello, world!")
	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	s := steps[0]
	if s.StepID != 1 {
		t.Errorf("step_id = %d, want 1", s.StepID)
	}
	if s.Source != "user" {
		t.Errorf("source = %q, want %q", s.Source, "user")
	}
	if msg, ok := s.Message.(string); !ok || msg != "Hello, world!" {
		t.Errorf("message = %v, want %q", s.Message, "Hello, world!")
	}
	if len(s.ToolCalls) != 0 {
		t.Errorf("expected no tool calls")
	}
	if s.Observation != nil {
		t.Errorf("expected no observation")
	}
}

func TestConvertEvent_ModelTextResponse(t *testing.T) {
	ev := makeTextEvent("model", "I can help with that.")
	steps := ConvertEvent(ev, 5)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	s := steps[0]
	if s.StepID != 5 {
		t.Errorf("step_id = %d, want 5", s.StepID)
	}
	if s.Source != "agent" {
		t.Errorf("source = %q, want %q", s.Source, "agent")
	}
	if msg, ok := s.Message.(string); !ok || msg != "I can help with that." {
		t.Errorf("message = %v, want %q", s.Message, "I can help with that.")
	}
}

func TestConvertEvent_SystemSource(t *testing.T) {
	ev := makeTextEvent("system", "System prompt")
	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Source != "system" {
		t.Errorf("source = %q, want %q", steps[0].Source, "system")
	}
}

func TestConvertEvent_UnknownAuthorMapsToSystem(t *testing.T) {
	ev := makeTextEvent("tool", "Some output")
	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Source != "system" {
		t.Errorf("source = %q, want %q", steps[0].Source, "system")
	}
}

func TestConvertEvent_SingleToolCall(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: "Let me read that."},
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_001",
				Name: "read",
				Args: map[string]any{"path": "main.go"},
			}},
		},
	}

	steps := ConvertEvent(ev, 2)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	s := steps[0]
	if s.Source != "agent" {
		t.Errorf("source = %q, want %q", s.Source, "agent")
	}
	if msg, ok := s.Message.(string); !ok || msg != "Let me read that." {
		t.Errorf("message = %v, want %q", s.Message, "Let me read that.")
	}
	if len(s.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(s.ToolCalls))
	}
	tc := s.ToolCalls[0]
	if tc.ToolCallID != "call_001" {
		t.Errorf("tool_call_id = %q, want %q", tc.ToolCallID, "call_001")
	}
	if tc.FunctionName != "read" {
		t.Errorf("function_name = %q, want %q", tc.FunctionName, "read")
	}
	if tc.Arguments["path"] != "main.go" {
		t.Errorf("arguments[path] = %v, want %q", tc.Arguments["path"], "main.go")
	}
}

func TestConvertEvent_MultipleToolCalls(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_a",
				Name: "read",
				Args: map[string]any{"path": "a.go"},
			}},
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_b",
				Name: "read",
				Args: map[string]any{"path": "b.go"},
			}},
		},
	}

	steps := ConvertEvent(ev, 3)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if len(steps[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(steps[0].ToolCalls))
	}
	if steps[0].ToolCalls[0].ToolCallID != "call_a" {
		t.Errorf("first tool_call_id = %q, want %q", steps[0].ToolCalls[0].ToolCallID, "call_a")
	}
	if steps[0].ToolCalls[1].ToolCallID != "call_b" {
		t.Errorf("second tool_call_id = %q, want %q", steps[0].ToolCalls[1].ToolCallID, "call_b")
	}
}

func TestConvertEvent_ToolCallNilArgs(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_x",
				Name: "ping",
			}},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	tc := steps[0].ToolCalls[0]
	if tc.Arguments == nil {
		t.Fatal("arguments should not be nil")
	}
	if len(tc.Arguments) != 0 {
		t.Errorf("arguments should be empty, got %v", tc.Arguments)
	}
}

func TestConvertEvent_FunctionResponse(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "system"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{
				ID:       "call_001",
				Name:     "read",
				Response: map[string]any{"output": "file contents here"},
			}},
		},
	}

	steps := ConvertEvent(ev, 3)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	s := steps[0]
	if s.Source != "system" {
		t.Errorf("source = %q, want %q", s.Source, "system")
	}
	if s.Observation == nil {
		t.Fatal("expected observation")
	}
	if len(s.Observation.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(s.Observation.Results))
	}
	r := s.Observation.Results[0]
	if r.SourceCallID != "call_001" {
		t.Errorf("source_call_id = %q, want %q", r.SourceCallID, "call_001")
	}
	resp, ok := r.Content.(map[string]any)
	if !ok {
		t.Fatalf("content type = %T, want map[string]any", r.Content)
	}
	if resp["output"] != "file contents here" {
		t.Errorf("content.output = %v, want %q", resp["output"], "file contents here")
	}
}

func TestConvertEvent_FunctionResponseFallbackToName(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "system"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{
				Name:     "read",
				Response: map[string]any{"output": "ok"},
			}},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Observation.Results[0].SourceCallID != "read" {
		t.Errorf("source_call_id = %q, want %q", steps[0].Observation.Results[0].SourceCallID, "read")
	}
}

func TestConvertEvent_MultipleFunctionResponses(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "system"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{
				ID:       "call_a",
				Name:     "read",
				Response: map[string]any{"output": "a"},
			}},
			{FunctionResponse: &genai.FunctionResponse{
				ID:       "call_b",
				Name:     "write",
				Response: map[string]any{"output": "b"},
			}},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if len(steps[0].Observation.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(steps[0].Observation.Results))
	}
}

func TestConvertEvent_MultipleTextParts(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: "Part one."},
			{Text: "Part two."},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	parts, ok := steps[0].Message.([]ContentPart)
	if !ok {
		t.Fatalf("message type = %T, want []ContentPart", steps[0].Message)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "Part one." {
		t.Errorf("part[0] = %+v, want text='Part one.'", parts[0])
	}
	if parts[1].Type != "text" || parts[1].Text != "Part two." {
		t.Errorf("part[1] = %+v, want text='Part two.'", parts[1])
	}
}

func TestConvertEvent_ThoughtPartsSkipped(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: "thinking...", Thought: true},
			{Text: "Visible response."},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if msg, ok := steps[0].Message.(string); !ok || msg != "Visible response." {
		t.Errorf("message = %v, want %q", steps[0].Message, "Visible response.")
	}
}

func TestConvertEvent_TimestampFormat(t *testing.T) {
	ev := makeTextEvent("user", "hi")
	ev.Timestamp = time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)
	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	want := "2026-04-05T10:30:00Z"
	if steps[0].Timestamp != want {
		t.Errorf("timestamp = %q, want %q", steps[0].Timestamp, want)
	}
}

func TestConvertEvent_MixedTextAndToolCalls(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: "I'll check that."},
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_1",
				Name: "search",
				Args: map[string]any{"q": "test"},
			}},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	s := steps[0]
	if msg, ok := s.Message.(string); !ok || msg != "I'll check that." {
		t.Errorf("message = %v, want text", s.Message)
	}
	if len(s.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(s.ToolCalls))
	}
}

func TestConvertEvent_PureToolCallEmptyMessage(t *testing.T) {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_1",
				Name: "read",
				Args: map[string]any{"path": "x"},
			}},
		},
	}

	steps := ConvertEvent(ev, 1)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if msg, ok := steps[0].Message.(string); !ok || msg != "" {
		t.Errorf("message = %v, want empty string", steps[0].Message)
	}
}

// -- helpers --

var refTime = time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)

func makeTextEvent(author, text string) *session.Event {
	ev := &session.Event{}
	ev.Author = author
	ev.Timestamp = refTime
	ev.Content = &genai.Content{
		Parts: []*genai.Part{{Text: text}},
	}
	return ev
}
