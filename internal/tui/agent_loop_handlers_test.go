package tui

import (
	"testing"
	"time"
)

// newHandlerModel builds a minimal model suitable for exercising the
// handleAgent* message handlers directly. width is set so mainWidth()/matrix
// don't degenerate, and agentCh is buffered so the returned waitForAgent Cmd
// (if invoked) never blocks.
func newHandlerModel() *model {
	m := &model{width: 100, height: 30}
	m.agentCh = make(chan agentMsg, 8)
	return m
}

// --- handleAgentText -------------------------------------------------------

func TestHandleAgentText_AppendsWhenLastIsTool(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{{role: "tool", tool: "bash", content: "done"}}

	m.handleAgentText(agentTextMsg{text: "hello"})

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.role != "assistant" || last.content != "hello" {
		t.Fatalf("expected new assistant message %q, got role=%q content=%q", "hello", last.role, last.content)
	}
	if got := len(m.chatModel.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if len(m.chatModel.TraceLog) != 1 || m.chatModel.TraceLog[0].kind != "llm" {
		t.Fatalf("expected one llm trace entry, got %+v", m.chatModel.TraceLog)
	}
}

func TestHandleAgentText_UpdatesTrailingAssistantAndTrace(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{{role: "assistant", content: ""}}

	m.handleAgentText(agentTextMsg{text: "foo"})
	m.handleAgentText(agentTextMsg{text: "bar"})

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.content != "foobar" {
		t.Fatalf("expected streaming concat %q, got %q", "foobar", last.content)
	}
	// Both deltas update the single trailing llm trace entry.
	if len(m.chatModel.TraceLog) != 1 {
		t.Fatalf("expected 1 trace entry, got %d", len(m.chatModel.TraceLog))
	}
	if m.chatModel.TraceLog[0].detail != "foobar" {
		t.Fatalf("expected trace detail %q, got %q", "foobar", m.chatModel.TraceLog[0].detail)
	}
}

func TestHandleAgentText_ClearsThinkingMessage(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Thinking = "pondering..."
	m.chatModel.Messages = []message{{role: "thinking", content: "pondering..."}}

	m.handleAgentText(agentTextMsg{text: "answer"})

	if m.chatModel.Thinking != "" {
		t.Fatalf("expected Thinking cleared, got %q", m.chatModel.Thinking)
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.role != "assistant" || last.content != "answer" {
		t.Fatalf("expected thinking message converted to assistant %q, got role=%q content=%q", "answer", last.role, last.content)
	}
}

// --- handleAgentToolResult -------------------------------------------------

func TestHandleAgentToolResult_FillsMatchingToolMessage(t *testing.T) {
	m := newHandlerModel()
	m.statusModel.ActiveTools = map[string]time.Time{}
	m.cfg.WorkDir = t.TempDir()
	m.chatModel.Messages = []message{{role: "tool", tool: "bash", content: ""}}

	m.handleAgentToolResult(agentToolResultMsg{name: "bash", content: `{"stdout":"hi"}`})

	if m.chatModel.Messages[0].content == "" {
		t.Fatal("expected matching tool message to be filled with the result summary")
	}
	if len(m.chatModel.TraceLog) != 1 || m.chatModel.TraceLog[0].kind != "tool_result" {
		t.Fatalf("expected one tool_result trace entry, got %+v", m.chatModel.TraceLog)
	}
}

func TestHandleAgentToolResult_PromotesRemainingActiveTool(t *testing.T) {
	m := newHandlerModel()
	m.cfg.WorkDir = t.TempDir()
	m.statusModel.ActiveTool = "bash"
	m.statusModel.ActiveTools = map[string]time.Time{"bash": {}, "read": {}}
	m.chatModel.Messages = []message{{role: "tool", tool: "read", content: ""}}

	m.handleAgentToolResult(agentToolResultMsg{name: "bash", content: "ok"})

	if _, still := m.statusModel.ActiveTools["bash"]; still {
		t.Fatal("expected finished tool to be removed from ActiveTools")
	}
	if m.statusModel.ActiveTool != "read" {
		t.Fatalf("expected ActiveTool promoted to remaining %q, got %q", "read", m.statusModel.ActiveTool)
	}
}

func TestHandleAgentToolResult_NoMatchingMessage(t *testing.T) {
	m := newHandlerModel()
	m.cfg.WorkDir = t.TempDir()
	m.statusModel.ActiveTools = map[string]time.Time{}
	m.chatModel.Messages = []message{{role: "assistant", content: "text"}}

	// Must not panic and must leave the assistant message untouched.
	m.handleAgentToolResult(agentToolResultMsg{name: "bash", content: "ok"})

	if m.chatModel.Messages[0].content != "text" {
		t.Fatalf("unrelated message changed: %q", m.chatModel.Messages[0].content)
	}
}

// --- handleAgentSubEvent ---------------------------------------------------

func TestHandleAgentSubEvent_SpawnBindsCard(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{{role: "tool", tool: "agent", agentType: "claude"}}

	m.handleAgentSubEvent(agentSubEventMsg{
		agentID: "claude-123", kind: "spawn",
		pipelineID: "p1", pipelineMode: "single", pipelineStep: 1, pipelineTotal: 1,
	})

	if m.chatModel.Messages[0].agentID != "claude-123" {
		t.Fatalf("expected card bound to agentID, got %q", m.chatModel.Messages[0].agentID)
	}
	if m.chatModel.Messages[0].pipelineMode != "single" {
		t.Fatalf("expected pipeline metadata copied, got %q", m.chatModel.Messages[0].pipelineMode)
	}
}

func TestHandleAgentSubEvent_SpawnNoCard(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{{role: "assistant", content: "x"}}

	// No agent card to bind → must be a no-op without panic.
	m.handleAgentSubEvent(agentSubEventMsg{agentID: "claude-1", kind: "spawn"})

	if m.chatModel.Messages[0].agentID != "" {
		t.Fatal("non-agent message should not be bound")
	}
}

func TestHandleAgentSubEvent_TextDeltaMergesIntoCard(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{{role: "tool", tool: "agent", agentID: "claude-9"}}

	m.handleAgentSubEvent(agentSubEventMsg{agentID: "claude-9", kind: "text_delta", content: "Hel"})
	m.handleAgentSubEvent(agentSubEventMsg{agentID: "claude-9", kind: "text_delta", content: "lo"})

	evs := m.chatModel.Messages[0].agentEvents
	if len(evs) != 1 {
		t.Fatalf("expected consecutive text deltas merged into 1 event, got %d", len(evs))
	}
	if evs[0].kind != "text" || evs[0].content != "Hello" {
		t.Fatalf("expected merged text %q, got kind=%q content=%q", "Hello", evs[0].kind, evs[0].content)
	}
}

func TestHandleAgentSubEvent_NonTextEventAppends(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{{role: "tool", tool: "subagent", agentID: "g-1"}}

	m.handleAgentSubEvent(agentSubEventMsg{agentID: "g-1", kind: "text_delta", content: "hi"})
	m.handleAgentSubEvent(agentSubEventMsg{agentID: "g-1", kind: "tool_call", content: "bash"})

	evs := m.chatModel.Messages[0].agentEvents
	if len(evs) != 2 {
		t.Fatalf("expected text + tool_call events, got %d", len(evs))
	}
	if evs[1].kind != "tool_call" {
		t.Fatalf("expected second event tool_call, got %q", evs[1].kind)
	}
}

// --- extractAgentType ------------------------------------------------------

func TestExtractAgentType_ChainWithDuplicates(t *testing.T) {
	args := map[string]any{
		"chain": []any{
			map[string]any{"agent": "claude"},
			map[string]any{"agent": "gemini"},
			map[string]any{"agent": "claude"},  // duplicate is dropped
			map[string]any{"task": "no agent"}, // skipped
		},
	}
	if got := extractAgentType(args); got != "claude+gemini" {
		t.Fatalf("extractAgentType(chain) = %q, want %q", got, "claude+gemini")
	}
}

func TestExtractAgentType_EmptyReturnsBlank(t *testing.T) {
	if got := extractAgentType(map[string]any{"tasks": []any{}}); got != "" {
		t.Fatalf("expected empty label for empty tasks, got %q", got)
	}
	if got := extractAgentType(map[string]any{"unrelated": 1}); got != "" {
		t.Fatalf("expected empty label for unrelated args, got %q", got)
	}
}
