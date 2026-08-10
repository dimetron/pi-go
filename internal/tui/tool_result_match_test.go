package tui

import (
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// newResultMatchModel builds a model wired for driving handleAgentToolCall and
// handleAgentToolResult back to back.
func newResultMatchModel(t *testing.T) *model {
	t.Helper()
	m := newHandlerModel()
	m.cfg.WorkDir = t.TempDir()
	m.statusModel.ActiveTools = map[string]time.Time{}
	return m
}

// TestToolResultMatching_ParallelSameToolNotTransposed pins the defect this
// file exists for: a model turn that calls the same tool twice (two `read`
// parts in one LLM response, which ADK runs concurrently and answers with one
// merged event carrying both responses in call order) must not have its two
// results swapped between the two cards.
//
// Before ID matching, the backscan for "newest tool card with this name and no
// content yet" handed result #1 to card #2 and result #2 to card #1.
func TestToolResultMatching_ParallelSameToolNotTransposed(t *testing.T) {
	m := newResultMatchModel(t)

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_1", name: "read", args: map[string]any{"file_path": "a.go"},
	})
	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_2", name: "read", args: map[string]any{"file_path": "b.go"},
	})

	m.handleAgentToolResult(agentToolResultMsg{id: "call_1", name: "read", content: "contents of a"})
	m.handleAgentToolResult(agentToolResultMsg{id: "call_2", name: "read", content: "contents of b"})

	if got := len(m.chatModel.Messages); got != 2 {
		t.Fatalf("expected 2 tool cards, got %d", got)
	}
	if got := m.chatModel.Messages[0].content; got != "contents of a" {
		t.Errorf("card for call_1 (a.go) got result %q, want %q", got, "contents of a")
	}
	if got := m.chatModel.Messages[1].content; got != "contents of b" {
		t.Errorf("card for call_2 (b.go) got result %q, want %q", got, "contents of b")
	}
}

// TestToolResultMatching_OutOfOrderResults covers the same-name case when the
// merged response event lists the slower call's result first. Nothing in ADK
// guarantees response order tracks call order across every provider, so the
// match must not depend on it.
func TestToolResultMatching_OutOfOrderResults(t *testing.T) {
	m := newResultMatchModel(t)

	m.handleAgentToolCall(agentToolCallMsg{id: "call_1", name: "bash", args: map[string]any{"command": "make build"}})
	m.handleAgentToolCall(agentToolCallMsg{id: "call_2", name: "bash", args: map[string]any{"command": "make lint"}})

	m.handleAgentToolResult(agentToolResultMsg{id: "call_2", name: "bash", content: "lint clean"})
	m.handleAgentToolResult(agentToolResultMsg{id: "call_1", name: "bash", content: "build ok"})

	if got := m.chatModel.Messages[0].content; got != "build ok" {
		t.Errorf("card for call_1 got %q, want %q", got, "build ok")
	}
	if got := m.chatModel.Messages[1].content; got != "lint clean" {
		t.Errorf("card for call_2 got %q, want %q", got, "lint clean")
	}
}

func TestMatchToolResultCard(t *testing.T) {
	tests := []struct {
		name string
		msgs []message
		id   string
		tool string
		want int
	}{
		{
			name: "id match wins over newest empty same-name card",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "a"},
				{role: "tool", tool: "read", toolID: "b"},
			},
			id: "a", tool: "read", want: 0,
		},
		{
			name: "duplicate result for an already-filled card is dropped",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "a", content: "done"},
				{role: "tool", tool: "read", toolID: "b"},
			},
			id: "a", tool: "read", want: -1,
		},
		{
			name: "unknown id falls back to newest unidentified same-name card",
			msgs: []message{
				{role: "tool", tool: "read"},
				{role: "tool", tool: "read", toolID: "b"},
			},
			id: "zz", tool: "read", want: 0,
		},
		{
			name: "no id falls back to newest empty same-name card",
			msgs: []message{
				{role: "tool", tool: "grounding", content: "old"},
				{role: "tool", tool: "grounding"},
			},
			tool: "grounding", want: 1,
		},
		{
			name: "id-less result still binds an identified card as a last resort",
			msgs: []message{{role: "tool", tool: "read", toolID: "a"}},
			tool: "read", want: 0,
		},
		{
			name: "name mismatch matches nothing",
			msgs: []message{{role: "tool", tool: "read"}},
			tool: "bash", want: -1,
		},
		{
			name: "non-tool roles are never matched",
			msgs: []message{{role: "assistant"}},
			tool: "read", want: -1,
		},
		{
			name: "all same-id cards filled and none share the name",
			msgs: nil,
			id:   "a", tool: "read", want: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchToolResultCard(tt.msgs, tt.id, tt.tool); got != tt.want {
				t.Errorf("matchToolResultCard() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestToolResultMatching_SubagentSplitCards guards the one call -> N cards
// path: splitSubagentCards stamps every card with the same call ID, and the
// single result must land on the newest of them, exactly as name matching did.
func TestToolResultMatching_SubagentSplitCards(t *testing.T) {
	m := newResultMatchModel(t)

	m.handleAgentToolCall(agentToolCallMsg{
		id:   "call_1",
		name: "agent",
		args: map[string]any{"tasks": []any{
			map[string]any{"agent": "pi", "task": "one"},
			map[string]any{"agent": "claude", "task": "two"},
		}},
	})
	if got := len(m.chatModel.Messages); got != 2 {
		t.Fatalf("expected 2 subagent cards, got %d", got)
	}
	for i, msg := range m.chatModel.Messages {
		if msg.toolID != "call_1" {
			t.Errorf("card %d toolID = %q, want %q", i, msg.toolID, "call_1")
		}
	}

	m.handleAgentToolResult(agentToolResultMsg{id: "call_1", name: "agent", content: "both done"})

	if m.chatModel.Messages[1].content == "" {
		t.Error("expected the newest subagent card to receive the result")
	}
}

// parallelReadEvents mirrors what a real session records for two concurrent
// `read` calls: one model event carrying both calls, then one merged user event
// carrying both responses, every part tagged with the call ID that pairs them.
// Taken from the shape in ~/.pi-go/sessions/*/events.jsonl.
func parallelReadEvents() []*session.Event {
	call := func(id, path string) *genai.Part {
		return &genai.Part{FunctionCall: &genai.FunctionCall{
			ID: id, Name: "read", Args: map[string]any{"file_path": path},
		}}
	}
	result := func(id, out string) *genai.Part {
		return &genai.Part{FunctionResponse: &genai.FunctionResponse{
			ID: id, Name: "read", Response: map[string]any{"content": out},
		}}
	}
	return []*session.Event{
		event("model", call("call_1", "a.go"), call("call_2", "b.go")),
		event("user", result("call_1", "alpha"), result("call_2", "beta")),
	}
}

// TestRestoreTranscript_ParallelSameToolNotTransposed covers the persisted
// path: replaying a session that used parallel same-name calls must rebuild the
// transcript with each result under its own call.
func TestRestoreTranscript_ParallelSameToolNotTransposed(t *testing.T) {
	events := parallelReadEvents()

	msgs := restoreTranscript(events)

	if got := len(msgs); got != 2 {
		t.Fatalf("expected 2 restored tool cards, got %d", got)
	}
	if got := msgs[0].content; got != "alpha" {
		t.Errorf("restored card for a.go got %q, want %q", got, "alpha")
	}
	if got := msgs[1].content; got != "beta" {
		t.Errorf("restored card for b.go got %q, want %q", got, "beta")
	}
}
