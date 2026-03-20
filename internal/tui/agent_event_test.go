package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- toolCallSummary for agent ---

func TestToolCallSummary_AgentTypeAndPrompt(t *testing.T) {
	args := map[string]any{
		"type":   "task",
		"prompt": "Fix the linter issues in config tests",
	}
	result := toolCallSummary("agent", args)
	if result != "task: Fix the linter issues in config tests" {
		t.Errorf("expected 'task: Fix the linter issues in config tests', got %q", result)
	}
}

func TestToolCallSummary_AgentTypeOnly(t *testing.T) {
	args := map[string]any{
		"type": "explore",
	}
	result := toolCallSummary("agent", args)
	if result != "explore" {
		t.Errorf("expected 'explore', got %q", result)
	}
}

func TestToolCallSummary_AgentPromptOnly(t *testing.T) {
	args := map[string]any{
		"prompt": "Search the codebase",
	}
	result := toolCallSummary("agent", args)
	if result != "Search the codebase" {
		t.Errorf("expected 'Search the codebase', got %q", result)
	}
}

func TestToolCallSummary_AgentLongPrompt(t *testing.T) {
	args := map[string]any{
		"type":   "task",
		"prompt": "This is a very long prompt that exceeds the sixty character limit and should be truncated",
	}
	result := toolCallSummary("agent", args)
	if len(result) > 70 { // type + ": " + 60 chars
		t.Errorf("expected truncated result, got len=%d: %q", len(result), result)
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected truncated prompt to end with '...', got %q", result)
	}
}

func TestToolCallSummary_AgentMultiLinePrompt(t *testing.T) {
	args := map[string]any{
		"type":   "task",
		"prompt": "First line of prompt\nSecond line\nThird line",
	}
	result := toolCallSummary("agent", args)
	if strings.Contains(result, "\n") {
		t.Errorf("expected single line output, got %q", result)
	}
	if !strings.Contains(result, "First line of prompt") {
		t.Errorf("expected first line preserved, got %q", result)
	}
}

func TestToolCallSummary_AgentEmptyArgs(t *testing.T) {
	args := map[string]any{}
	result := toolCallSummary("agent", args)
	if result != "" {
		t.Errorf("expected empty string for empty args, got %q", result)
	}
}

// --- toolCallSummary for other tools ---

func TestToolCallSummary_Read(t *testing.T) {
	args := map[string]any{"file_path": "/path/to/file.go"}
	result := toolCallSummary("read", args)
	if result != "/path/to/file.go" {
		t.Errorf("expected file path, got %q", result)
	}
}

func TestToolCallSummary_Bash(t *testing.T) {
	args := map[string]any{"command": "go build ./..."}
	result := toolCallSummary("bash", args)
	if result != "go build ./..." {
		t.Errorf("expected command, got %q", result)
	}
}

func TestToolCallSummary_BashLongCommand(t *testing.T) {
	long := strings.Repeat("x", 100)
	args := map[string]any{"command": long}
	result := toolCallSummary("bash", args)
	if len(result) > 80 {
		t.Errorf("expected truncated command, got len=%d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("expected '...' suffix for truncated command")
	}
}

func TestToolCallSummary_Grep(t *testing.T) {
	args := map[string]any{"pattern": "func main"}
	result := toolCallSummary("grep", args)
	if result != "func main" {
		t.Errorf("expected pattern, got %q", result)
	}
}

func TestToolCallSummary_Tree(t *testing.T) {
	args := map[string]any{"path": "src", "depth": float64(3)}
	result := toolCallSummary("tree", args)
	if result != "src (depth 3)" {
		t.Errorf("expected 'src (depth 3)', got %q", result)
	}
}

func TestToolCallSummary_TreeDefaultPath(t *testing.T) {
	args := map[string]any{}
	result := toolCallSummary("tree", args)
	if result != "." {
		t.Errorf("expected '.', got %q", result)
	}
}

func TestToolCallSummary_Unknown(t *testing.T) {
	args := map[string]any{"foo": "bar"}
	result := toolCallSummary("unknown_tool", args)
	if result != "" {
		t.Errorf("expected empty string for unknown tool, got %q", result)
	}
}

// --- formatToolResult for read ---

func TestFormatToolResult_ReadContent(t *testing.T) {
	data := map[string]any{
		"content":     "     1\tpackage main\n     2\t\n     3\tfunc main() {}\n",
		"total_lines": float64(3),
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "package main") {
		t.Errorf("expected content preserved, got %q", result)
	}
	if !strings.Contains(result, "1") {
		t.Errorf("expected line number in content, got %q", result)
	}
}

func TestFormatToolResult_ReadTruncated(t *testing.T) {
	data := map[string]any{
		"content":     "     1\tpackage main\n",
		"total_lines": float64(1000),
		"truncated":   true,
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "1000 total lines, truncated") {
		t.Errorf("expected truncation note, got %q", result)
	}
}

func TestFormatToolResult_ReadNoContent(t *testing.T) {
	data := map[string]any{
		"total_lines": float64(42),
	}
	result := formatToolResult(data)
	if result != "42 lines" {
		t.Errorf("expected '42 lines', got %q", result)
	}
}

func TestFormatToolResult_Bash(t *testing.T) {
	data := map[string]any{
		"exit_code": float64(0),
		"stdout":    "ok",
	}
	result := formatToolResult(data)
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestFormatToolResult_BashError(t *testing.T) {
	data := map[string]any{
		"exit_code": float64(1),
		"stdout":    "build failed",
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "exit 1") {
		t.Errorf("expected 'exit 1' in output, got %q", result)
	}
}

func TestFormatToolResult_BashNoOutput(t *testing.T) {
	data := map[string]any{
		"exit_code": float64(0),
		"stdout":    "",
	}
	result := formatToolResult(data)
	if result != "(No output)" {
		t.Errorf("expected '(No output)', got %q", result)
	}
}

func TestFormatToolResult_Edit(t *testing.T) {
	data := map[string]any{
		"replacements": float64(3),
	}
	result := formatToolResult(data)
	if result != "3 replacements" {
		t.Errorf("expected '3 replacements', got %q", result)
	}
}

func TestFormatToolResult_Write(t *testing.T) {
	data := map[string]any{
		"bytes_written": float64(1024),
		"path":          "/tmp/file.go",
	}
	result := formatToolResult(data)
	if result != "/tmp/file.go (1024 bytes)" {
		t.Errorf("expected path and bytes, got %q", result)
	}
}

func TestFormatToolResult_GrepWithMatches(t *testing.T) {
	data := map[string]any{
		"matches": []any{
			map[string]any{"file": "main.go", "line": float64(10), "content": "func main() {}"},
			map[string]any{"file": "util.go", "line": float64(5), "content": "var x = 1"},
		},
		"total_matches": float64(2),
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "main.go:10:") {
		t.Errorf("expected 'main.go:10:' in output, got %q", result)
	}
	if !strings.Contains(result, "util.go:5:") {
		t.Errorf("expected 'util.go:5:' in output, got %q", result)
	}
	if !strings.Contains(result, "func main()") {
		t.Errorf("expected content in output, got %q", result)
	}
}

func TestFormatToolResult_GrepTruncated(t *testing.T) {
	data := map[string]any{
		"matches": []any{
			map[string]any{"file": "a.go", "line": float64(1), "content": "x"},
		},
		"total_matches": float64(200),
		"truncated":     true,
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "200 total matches, truncated") {
		t.Errorf("expected truncation note, got %q", result)
	}
}

func TestFormatToolResult_GrepFallback(t *testing.T) {
	// No matches array, only count — fallback to "N matches".
	data := map[string]any{
		"total_matches": float64(7),
	}
	result := formatToolResult(data)
	if result != "7 matches" {
		t.Errorf("expected '7 matches', got %q", result)
	}
}

func TestFormatToolResult_FindWithFiles(t *testing.T) {
	data := map[string]any{
		"files":       []any{"internal/tools/read.go", "internal/tools/write.go", "cmd/pi/main.go"},
		"total_files": float64(3),
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "internal/tools/read.go") {
		t.Errorf("expected file path in output, got %q", result)
	}
	if !strings.Contains(result, "cmd/pi/main.go") {
		t.Errorf("expected file path in output, got %q", result)
	}
}

func TestFormatToolResult_FindTruncated(t *testing.T) {
	data := map[string]any{
		"files":       []any{"a.go"},
		"total_files": float64(500),
		"truncated":   true,
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "500 total files, truncated") {
		t.Errorf("expected truncation note, got %q", result)
	}
}

func TestFormatToolResult_FindFallback(t *testing.T) {
	// No files array, only count — fallback to "N files".
	data := map[string]any{
		"total_files": float64(15),
	}
	result := formatToolResult(data)
	if result != "15 files" {
		t.Errorf("expected '15 files', got %q", result)
	}
}

func TestFormatToolResult_Ls(t *testing.T) {
	data := map[string]any{
		"entries": []any{
			map[string]any{"name": "main.go", "is_dir": false},
			map[string]any{"name": "pkg", "is_dir": true},
		},
	}
	result := formatToolResult(data)
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected 'main.go' in ls output, got %q", result)
	}
	if !strings.Contains(result, "pkg/") {
		t.Errorf("expected 'pkg/' in ls output, got %q", result)
	}
}

func TestFormatToolResult_Fallback(t *testing.T) {
	data := map[string]any{
		"custom": "value",
	}
	result := formatToolResult(data)
	if result == "" {
		t.Error("expected non-empty fallback JSON")
	}
}

// --- toolResultSummary ---

func TestToolResultSummary_JSON(t *testing.T) {
	data := map[string]any{"exit_code": float64(0), "stdout": "ok"}
	jsonBytes, _ := json.Marshal(data)
	result := toolResultSummary(string(jsonBytes))
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestToolResultSummary_PlainText(t *testing.T) {
	result := toolResultSummary("just some plain text")
	if result != "just some plain text" {
		t.Errorf("expected plain text preserved, got %q", result)
	}
}

func TestToolResultSummary_LongText(t *testing.T) {
	long := strings.Repeat("x", 200)
	result := toolResultSummary(long)
	if len(result) > 120 {
		t.Errorf("expected truncated to 120, got len=%d", len(result))
	}
}

func TestToolResultSummary_MultiLine(t *testing.T) {
	result := toolResultSummary("line1\nline2\nline3")
	if strings.Contains(result, "\n") {
		t.Error("expected newlines collapsed")
	}
}

// --- agentSubEventMsg handling ---

func TestAgentSubEvent_SpawnAssignsID(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentType: "task", agentTitle: "fix bug"},
		}},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-123",
		kind:    "spawn",
		content: "task",
	})
	mm := newM.(*model)
	if mm.chatModel.Messages[0].agentID != "sub-123" {
		t.Errorf("expected agentID 'sub-123', got %q", mm.chatModel.Messages[0].agentID)
	}
}

func TestAgentSubEvent_SpawnAssignsToLatestUnmatched(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-old"},   // already assigned
			{role: "tool", tool: "agent", agentType: "explore"}, // unassigned
		}},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-new",
		kind:    "spawn",
		content: "explore",
	})
	mm := newM.(*model)
	if mm.chatModel.Messages[0].agentID != "sub-old" {
		t.Error("first agent should keep its original ID")
	}
	if mm.chatModel.Messages[1].agentID != "sub-new" {
		t.Errorf("second agent should get new ID, got %q", mm.chatModel.Messages[1].agentID)
	}
}

func TestAgentSubEvent_ToolCallAppended(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-1"},
		}},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-1",
		kind:    "tool_call",
		content: "read",
	})
	mm := newM.(*model)
	if len(mm.chatModel.Messages[0].agentEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(mm.chatModel.Messages[0].agentEvents))
	}
	ev := mm.chatModel.Messages[0].agentEvents[0]
	if ev.kind != "tool_call" {
		t.Errorf("expected kind 'tool_call', got %q", ev.kind)
	}
	if ev.content != "read" {
		t.Errorf("expected content 'read', got %q", ev.content)
	}
}

func TestAgentSubEvent_ToolResultAppended(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-1"},
		}},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-1",
		kind:    "tool_result",
		content: "file contents here",
	})
	mm := newM.(*model)
	if len(mm.chatModel.Messages[0].agentEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(mm.chatModel.Messages[0].agentEvents))
	}
	if mm.chatModel.Messages[0].agentEvents[0].kind != "tool_result" {
		t.Error("expected tool_result kind")
	}
}

func TestAgentSubEvent_TextDeltaConvertedToText(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-1"},
		}},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-1",
		kind:    "text_delta",
		content: "some text",
	})
	mm := newM.(*model)
	if len(mm.chatModel.Messages[0].agentEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(mm.chatModel.Messages[0].agentEvents))
	}
	if mm.chatModel.Messages[0].agentEvents[0].kind != "text" {
		t.Errorf("expected text_delta converted to 'text', got %q", mm.chatModel.Messages[0].agentEvents[0].kind)
	}
}

func TestAgentSubEvent_MultipleEventsAccumulate(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-1"},
		}},
	}

	events := []agentSubEventMsg{
		{agentID: "sub-1", kind: "tool_call", content: "read"},
		{agentID: "sub-1", kind: "tool_result", content: "ok"},
		{agentID: "sub-1", kind: "tool_call", content: "edit"},
		{agentID: "sub-1", kind: "tool_result", content: "1 replacement"},
	}

	var mm *model = m
	for _, ev := range events {
		newM, _ := mm.Update(ev)
		mm = newM.(*model)
	}

	if len(mm.chatModel.Messages[0].agentEvents) != 4 {
		t.Fatalf("expected 4 events, got %d", len(mm.chatModel.Messages[0].agentEvents))
	}
}

func TestAgentSubEvent_RoutedByAgentID(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-1"},
			{role: "tool", tool: "agent", agentID: "sub-2"},
		}},
	}

	// Event for sub-2.
	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-2",
		kind:    "tool_call",
		content: "bash",
	})
	mm := newM.(*model)

	if len(mm.chatModel.Messages[0].agentEvents) != 0 {
		t.Error("sub-1 should have no events")
	}
	if len(mm.chatModel.Messages[1].agentEvents) != 1 {
		t.Fatal("sub-2 should have 1 event")
	}
	if mm.chatModel.Messages[1].agentEvents[0].content != "bash" {
		t.Errorf("expected 'bash', got %q", mm.chatModel.Messages[1].agentEvents[0].content)
	}
}

func TestAgentSubEvent_UnknownAgentIDIgnored(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{Messages: []message{
			{role: "tool", tool: "agent", agentID: "sub-1"},
		}},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-unknown",
		kind:    "tool_call",
		content: "read",
	})
	mm := newM.(*model)
	if len(mm.chatModel.Messages[0].agentEvents) != 0 {
		t.Error("event for unknown agentID should not be appended")
	}
}

func TestAgentSubEvent_ResetsScroll(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
		chatModel: ChatModel{
			Scroll: 5,
			Messages: []message{
				{role: "tool", tool: "agent", agentID: "sub-1"},
			},
		},
	}

	newM, _ := m.Update(agentSubEventMsg{
		agentID: "sub-1",
		kind:    "tool_call",
		content: "read",
	})
	mm := newM.(*model)
	if mm.chatModel.Scroll != 0 {
		t.Errorf("expected scroll reset to 0, got %d", mm.chatModel.Scroll)
	}
}

// --- agentToolCallMsg stores agent fields ---

func TestAgentToolCallMsg_SetsAgentFields(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentToolCallMsg{
		name: "agent",
		args: map[string]any{
			"type":   "task",
			"prompt": "Fix the bug in main.go",
		},
	})
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	msg := mm.chatModel.Messages[0]
	if msg.tool != "agent" {
		t.Errorf("expected tool 'agent', got %q", msg.tool)
	}
	if msg.agentType != "task" {
		t.Errorf("expected agentType 'task', got %q", msg.agentType)
	}
	if msg.agentTitle != "Fix the bug in main.go" {
		t.Errorf("expected agentTitle, got %q", msg.agentTitle)
	}
}

func TestAgentToolCallMsg_TruncatesLongTitle(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	longPrompt := strings.Repeat("a", 100)
	newM, _ := m.Update(agentToolCallMsg{
		name: "agent",
		args: map[string]any{
			"type":   "explore",
			"prompt": longPrompt,
		},
	})
	mm := newM.(*model)

	if len(mm.chatModel.Messages[0].agentTitle) > 60 {
		t.Errorf("expected title <= 60 chars, got %d", len(mm.chatModel.Messages[0].agentTitle))
	}
	if !strings.HasSuffix(mm.chatModel.Messages[0].agentTitle, "...") {
		t.Error("expected '...' suffix for truncated title")
	}
}

func TestAgentToolCallMsg_MultiLinePromptTrimmed(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentToolCallMsg{
		name: "agent",
		args: map[string]any{
			"type":   "task",
			"prompt": "First line\nSecond line\nThird",
		},
	})
	mm := newM.(*model)

	if strings.Contains(mm.chatModel.Messages[0].agentTitle, "\n") {
		t.Error("expected single-line title")
	}
	if mm.chatModel.Messages[0].agentTitle != "First line" {
		t.Errorf("expected 'First line', got %q", mm.chatModel.Messages[0].agentTitle)
	}
}

func TestAgentToolCallMsg_NonAgentToolNoAgentFields(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentToolCallMsg{
		name: "read",
		args: map[string]any{"file_path": "/tmp/test.go"},
	})
	mm := newM.(*model)

	if mm.chatModel.Messages[0].agentType != "" {
		t.Error("non-agent tool should not set agentType")
	}
	if mm.chatModel.Messages[0].agentTitle != "" {
		t.Error("non-agent tool should not set agentTitle")
	}
}

// --- waitForSubEvent ---

func TestWaitForSubEvent_NilChannel(t *testing.T) {
	cmd := waitForSubEvent(nil)
	if cmd != nil {
		t.Error("expected nil cmd for nil channel")
	}
}

func TestWaitForSubEvent_ReceivesEvent(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	ch <- AgentSubEvent{AgentID: "sub-1", Kind: "tool_call", Content: "read"}

	cmd := waitForSubEvent(ch)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	msg := cmd()
	subMsg, ok := msg.(agentSubEventMsg)
	if !ok {
		t.Fatalf("expected agentSubEventMsg, got %T", msg)
	}
	if subMsg.agentID != "sub-1" {
		t.Errorf("expected agentID 'sub-1', got %q", subMsg.agentID)
	}
	if subMsg.kind != "tool_call" {
		t.Errorf("expected kind 'tool_call', got %q", subMsg.kind)
	}
	if subMsg.content != "read" {
		t.Errorf("expected content 'read', got %q", subMsg.content)
	}
}

// --- renderMessages for agent tool ---

func TestRenderMessages_AgentWithTitle(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:       "tool",
				tool:       "agent",
				agentType:  "task",
				agentTitle: "Fix linter issues",
				agentID:    "sub-1",
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "agent") {
		t.Error("expected 'agent' in rendered output")
	}
	if !strings.Contains(output, "task") {
		t.Error("expected agent type 'task' in rendered output")
	}
	if !strings.Contains(output, "Fix linter issues") {
		t.Error("expected agent title in rendered output")
	}
}

func TestRenderMessages_AgentWithEvents(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:      "tool",
				tool:      "agent",
				agentType: "task",
				agentID:   "sub-1",
				agentEvents: []agentEv{
					{kind: "tool_call", content: "read"},
					{kind: "tool_result", content: "42 lines"},
					{kind: "tool_call", content: "edit"},
					{kind: "tool_result", content: "1 replacement"},
				},
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "read") {
		t.Error("expected 'read' tool call in event stream")
	}
	if !strings.Contains(output, "edit") {
		t.Error("expected 'edit' tool call in event stream")
	}
}

func TestRenderMessages_AgentEventStreamTruncated(t *testing.T) {
	// Create more than 8 events to test truncation.
	events := make([]agentEv, 12)
	for i := range events {
		events[i] = agentEv{kind: "tool_call", content: "tool"}
	}

	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:        "tool",
				tool:        "agent",
				agentType:   "task",
				agentID:     "sub-1",
				agentEvents: events,
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "earlier events") {
		t.Error("expected 'earlier events' note for truncated stream")
	}
}

func TestRenderMessages_AgentWithResult(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:      "tool",
				tool:      "agent",
				agentType: "task",
				agentID:   "sub-1",
				content:   "Changes applied successfully",
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "Changes applied") {
		t.Error("expected result summary in rendered output")
	}
}

func TestRenderMessages_RegularToolUnchanged(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:    "tool",
				tool:    "read",
				toolIn:  "/path/to/file.go",
				content: "42 lines",
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "read") {
		t.Error("expected 'read' tool name")
	}
	if !strings.Contains(output, "/path/to/file.go") {
		t.Error("expected file path in tool args")
	}
}

func TestRenderMessages_GrepHighlighted(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:    "tool",
				tool:    "grep",
				toolIn:  "func main",
				content: "main.go:5: func main() {}\nutil.go:10: func helper() {}",
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "\033[") {
		t.Error("expected ANSI codes for highlighted grep output")
	}
	if !strings.Contains(output, "main.go") {
		t.Error("expected file path in grep output")
	}
}

func TestRenderMessages_FindHighlighted(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:    "tool",
				tool:    "find",
				toolIn:  "*.go",
				content: "internal/tools/read.go\ninternal/tools/write.go",
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "\033[") {
		t.Error("expected ANSI codes for highlighted find output")
	}
	if !strings.Contains(output, "read.go") {
		t.Error("expected file path in find output")
	}
}

// --- Init batching ---

func TestInit_WithAgentEventCh(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	m := &model{
		cfg: Config{AgentEventCh: ch},
	}
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected non-nil cmd when AgentEventCh is set")
	}
}

func TestInit_WithBothChannels(t *testing.T) {
	eventCh := make(chan AgentSubEvent, 1)
	restartCh := make(chan struct{}, 1)
	m := &model{
		cfg: Config{
			AgentEventCh: eventCh,
			RestartCh:    restartCh,
		},
	}
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected non-nil cmd when both channels are set")
	}
}

func TestInit_NoChannels(t *testing.T) {
	m := &model{
		cfg: Config{},
	}
	cmd := m.Init()
	// With no channels, Init returns tea.Batch() with empty cmds which returns nil.
	_ = cmd
}

// --- renderMessages with read tool highlighting ---

func TestRenderMessages_ReadToolHighlighted(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{
				role:    "tool",
				tool:    "read",
				toolIn:  "main.go",
				content: "     1\tpackage main\n     2\t\n     3\tfunc main() {}",
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	// Should contain ANSI codes from syntax highlighting.
	if !strings.Contains(output, "\033[") {
		t.Error("expected ANSI escape codes for highlighted Go code")
	}
	if !strings.Contains(output, "1") {
		t.Error("expected line number in output")
	}
}

// --- renderMessages with various message types ---

func TestRenderMessages_UserMessage(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{role: "user", content: "hello world"},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "hello world") {
		t.Error("expected user message content")
	}
}

func TestRenderMessages_AssistantMessage(t *testing.T) {
	m := &model{
		width: 120,
		chatModel: ChatModel{Messages: []message{
			{role: "assistant", content: "I can help with that"},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "help") {
		t.Error("expected assistant message content")
	}
}

func TestRenderMessages_Empty(t *testing.T) {
	m := &model{
		width:     120,
		chatModel: ChatModel{Messages: []message{}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "Welcome") {
		t.Error("expected welcome message for empty conversation")
	}
}

// --- isUserInput ---

func TestIsUserInput_Normal(t *testing.T) {
	if !isUserInput("hello") {
		t.Error("expected 'hello' to be user input")
	}
}

func TestIsUserInput_NonPrintable(t *testing.T) {
	if isUserInput("\x00invalid") {
		t.Error("expected non-printable chars to be rejected")
	}
}

func TestIsUserInput_TerminalEscape(t *testing.T) {
	if isUserInput("]11;rgb:ffff/ffff/ffff") {
		t.Error("expected terminal escape sequence to be rejected")
	}
}

// --- Screen ---

func TestScreen_UpdateAndRead(t *testing.T) {
	s := &Screen{}
	s.update("test content")
	if s.ScreenContent() != "test content" {
		t.Errorf("expected 'test content', got %q", s.ScreenContent())
	}
}

func TestScreen_Empty(t *testing.T) {
	s := &Screen{}
	if s.ScreenContent() != "" {
		t.Errorf("expected empty string, got %q", s.ScreenContent())
	}
}

// --- additional agent message tests from existing patterns ---

func TestAgentTextMsg_AccumulatesStreaming(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "assistant", content: ""}}},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentTextMsg{text: "Hello "})
	mm := newM.(*model)
	if mm.chatModel.Streaming != "Hello " {
		t.Errorf("expected streaming 'Hello ', got %q", mm.chatModel.Streaming)
	}

	newM2, _ := mm.Update(agentTextMsg{text: "world"})
	mm2 := newM2.(*model)
	if mm2.chatModel.Streaming != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", mm2.chatModel.Streaming)
	}
}

func TestAgentDoneMsg_ClearsRunning(t *testing.T) {
	m := &model{
		chatModel: ChatModel{
			Messages:  []message{{role: "assistant", content: "done"}},
			Streaming: "text",
			Thinking:  "thought",
		},
		running:     true,
		activeTool:  "read",
		activeTools: map[string]time.Time{"read": {}},
		agentCh:     make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentDoneMsg{})
	mm := newM.(*model)
	if mm.running {
		t.Error("expected running=false after done")
	}
	if mm.activeTool != "" {
		t.Errorf("expected empty activeTool, got %q", mm.activeTool)
	}
	if mm.activeTools != nil {
		t.Error("expected nil activeTools")
	}
	if mm.chatModel.Streaming != "" {
		t.Error("expected empty streaming")
	}
}

func TestAgentDoneMsg_WithError(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "assistant"}}},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentDoneMsg{err: fmt.Errorf("connection lost")})
	mm := newM.(*model)
	found := false
	for _, msg := range mm.chatModel.Messages {
		if strings.Contains(msg.content, "connection lost") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected error message in messages")
	}
}

func TestAgentToolCallMsg_SetsActiveTool(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentToolCallMsg{
		name: "read",
		args: map[string]any{"file_path": "/tmp/file.go"},
	})
	mm := newM.(*model)
	if mm.activeTool != "read" {
		t.Errorf("expected activeTool 'read', got %q", mm.activeTool)
	}
}

func TestAgentToolResultMsg_ClearsActiveTool(t *testing.T) {
	m := &model{
		chatModel:   ChatModel{Messages: []message{{role: "tool", tool: "read", content: ""}}},
		running:     true,
		activeTool:  "read",
		activeTools: map[string]time.Time{"read": {}},
		agentCh:     make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentToolResultMsg{
		name:    "read",
		content: `{"content":"hello","total_lines":1}`,
	})
	mm := newM.(*model)
	if mm.activeTool != "" {
		t.Errorf("expected empty activeTool, got %q", mm.activeTool)
	}
	if mm.chatModel.Messages[0].content == "" {
		t.Error("expected message content to be updated")
	}
}
