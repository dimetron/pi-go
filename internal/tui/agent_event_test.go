package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/extension"
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
	// toolCallSummary no longer truncates — the renderer clips to the
	// terminal width. The full command is preserved for wide terminals.
	if result != long {
		t.Errorf("expected full command preserved, got len=%d", len(result))
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

func TestAgentBracketLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"claude", "claude"},
		{"gemini", "gemini"},
		{"cursor", "cursor"},
		{"explore", "pi"},
		{"task", "pi"},
		{"plan", "pi"},
		{"code-reviewer", "pi"},
		{"claude+gemini", "claude+gemini"},
		{"claude+cursor+gemini", "claude+cursor+gemini"},
		{"cursor+task", "cursor+pi"},
		{"claude+explore", "claude+pi"},
		{"explore+task", "pi"},
		{"claude+gemini+task", "claude+gemini+pi"},
	}
	for _, tc := range tests {
		if got := agentBracketLabel(tc.in); got != tc.want {
			t.Errorf("agentBracketLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtractAgentType(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"type field", map[string]any{"type": "claude"}, "claude"},
		{"agent field fallback", map[string]any{"agent": "gemini"}, "gemini"},
		{"type wins over agent", map[string]any{"type": "claude", "agent": "gemini"}, "claude"},
		{"tasks parallel", map[string]any{
			"tasks": []any{
				map[string]any{"agent": "claude"},
				map[string]any{"agent": "gemini"},
			},
		}, "claude+gemini"},
		{"chain", map[string]any{
			"chain": []any{
				map[string]any{"agent": "explore"},
				map[string]any{"agent": "task"},
			},
		}, "explore+task"},
		{"tasks dedupe", map[string]any{
			"tasks": []any{
				map[string]any{"agent": "claude"},
				map[string]any{"agent": "claude"},
			},
		}, "claude"},
		{"empty", map[string]any{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractAgentType(tc.args); got != tc.want {
				t.Errorf("extractAgentType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitSubagentCards_Single(t *testing.T) {
	base := message{role: "tool", tool: "subagent"}
	cards := splitSubagentCards(base, map[string]any{
		"agent": "explore",
		"task":  "find all TODO comments",
	})
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	if cards[0].agentType != "explore" {
		t.Errorf("agentType = %q, want %q", cards[0].agentType, "explore")
	}
	if cards[0].agentTitle != "find all TODO comments" {
		t.Errorf("agentTitle = %q, want %q", cards[0].agentTitle, "find all TODO comments")
	}
}

func TestSplitSubagentCards_Parallel(t *testing.T) {
	base := message{role: "tool", tool: "subagent"}
	cards := splitSubagentCards(base, map[string]any{
		"tasks": []any{
			map[string]any{"agent": "claude", "task": "review A"},
			map[string]any{"agent": "gemini", "task": "review B"},
			map[string]any{"agent": "cursor", "task": "review C"},
			map[string]any{"agent": "task", "task": "review D"},
		},
	})
	if len(cards) != 4 {
		t.Fatalf("expected 4 cards, got %d", len(cards))
	}
	wantTypes := []string{"claude", "gemini", "cursor", "task"}
	wantTitles := []string{"review A", "review B", "review C", "review D"}
	for i, card := range cards {
		if card.agentType != wantTypes[i] {
			t.Errorf("card[%d].agentType = %q, want %q", i, card.agentType, wantTypes[i])
		}
		if card.agentTitle != wantTitles[i] {
			t.Errorf("card[%d].agentTitle = %q, want %q", i, card.agentTitle, wantTitles[i])
		}
	}
}

func TestSplitSubagentCards_Chain(t *testing.T) {
	base := message{role: "tool", tool: "subagent"}
	cards := splitSubagentCards(base, map[string]any{
		"chain": []any{
			map[string]any{"agent": "explore", "task": "step 1"},
			map[string]any{"agent": "task", "task": "step 2"},
		},
	})
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}
	if cards[0].agentType != "explore" || cards[1].agentType != "task" {
		t.Errorf("chain card types = %q/%q, want explore/task", cards[0].agentType, cards[1].agentType)
	}
}

func TestFindUnassignedAgentCard_PrefersTypeMatch(t *testing.T) {
	msgs := []message{
		{tool: "subagent", agentType: "claude"},
		{tool: "subagent", agentType: "gemini"},
		{tool: "subagent", agentType: "cursor"},
	}
	idx := findUnassignedAgentCard(msgs, "gemini-1700000000")
	if idx != 1 {
		t.Errorf("expected gemini card at index 1, got %d", idx)
	}
}

func TestFindUnassignedAgentCard_FallbackWhenNoPrefixMatch(t *testing.T) {
	msgs := []message{
		{tool: "subagent", agentType: "explore"},
		{tool: "subagent", agentType: "task"},
	}
	// agentID starts with "plan" which matches neither; fallback to first
	// unassigned card (walking newest-to-oldest, that's index 1).
	idx := findUnassignedAgentCard(msgs, "plan-123")
	if idx != 1 {
		t.Errorf("expected fallback to index 1, got %d", idx)
	}
}

func TestFindUnassignedAgentCard_SkipsAlreadyBound(t *testing.T) {
	msgs := []message{
		{tool: "subagent", agentType: "claude", agentID: "claude-1"},
		{tool: "subagent", agentType: "claude"},
	}
	idx := findUnassignedAgentCard(msgs, "claude-2")
	if idx != 1 {
		t.Errorf("expected second claude card (index 1), got %d", idx)
	}
}

func TestFindUnassignedAgentCard_NoneAvailable(t *testing.T) {
	msgs := []message{
		{tool: "subagent", agentType: "claude", agentID: "claude-1"},
	}
	if idx := findUnassignedAgentCard(msgs, "claude-2"); idx != -1 {
		t.Errorf("expected -1 when all cards bound, got %d", idx)
	}
}

func TestCollapseToSingleLine(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"hello", "hello"},
		{"line one\nline two", "line one line two"},
		{"a\r\nb\tc", "a b c"},
		{"  multi   spaces\n\n  here ", "multi spaces here"},
	}
	for _, tc := range tests {
		if got := collapseToSingleLine(tc.in); got != tc.want {
			t.Errorf("collapseToSingleLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
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

func TestFormatToolResult_BashShowsFirstTwoAndLastTwoLines(t *testing.T) {
	data := map[string]any{
		"exit_code": float64(0),
		"stdout":    "one\ntwo\nthree\nfour\nfive\nsix\n",
	}
	result := formatToolResult(data)
	want := "one\ntwo\nfive\nsix"
	if result != want {
		t.Errorf("expected %q, got %q", want, result)
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

	var mm = m
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

func TestAgentToolCallMsg_StoresTitleWithinCap(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	// 100 runes is within the storage cap, so it is kept whole — the visible
	// length is decided at render time against the terminal width.
	longPrompt := strings.Repeat("a", 100)
	newM, _ := m.Update(agentToolCallMsg{
		name: "agent",
		args: map[string]any{
			"type":   "explore",
			"prompt": longPrompt,
		},
	})
	mm := newM.(*model)

	if mm.chatModel.Messages[0].agentTitle != longPrompt {
		t.Errorf("expected title kept whole within the cap, got %q", mm.chatModel.Messages[0].agentTitle)
	}
}

func TestAgentToolCallMsg_TruncatesOverCapTitle(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
	}

	overCap := strings.Repeat("a", maxStoredAgentTitle+50)
	newM, _ := m.Update(agentToolCallMsg{
		name: "agent",
		args: map[string]any{
			"type":   "explore",
			"prompt": overCap,
		},
	})
	mm := newM.(*model)

	if len(mm.chatModel.Messages[0].agentTitle) > maxStoredAgentTitle {
		t.Errorf("expected title <= %d chars, got %d", maxStoredAgentTitle, len(mm.chatModel.Messages[0].agentTitle))
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
		t.Error("expected 'agent' label in rendered output")
	}
	// Regular pi-based subagents (task, explore, …) collapse to "agent[pi]".
	if !strings.Contains(output, "[pi]") {
		t.Error("expected '[pi]' bracketed label for pi-based subagent in rendered output")
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
				},
			},
		}},
	}
	m.chatModel.UpdateRenderer(m.width)

	output := m.chatModel.RenderMessages(m.running)
	if !strings.Contains(output, "read") {
		t.Error("expected 'read' tool call in event stream")
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
	m := &model{
		cfg: Config{
			AgentEventCh: eventCh,
		},
	}
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected non-nil cmd when AgentEventCh is set")
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

func TestIsUserInput_SingleChar(t *testing.T) {
	// Real keystrokes are single runes — must always pass.
	for _, ch := range []string{"a", "Z", "/", "@", "1", ";", "é", "中"} {
		if !isUserInput(ch) {
			t.Errorf("single char %q should be accepted", ch)
		}
	}
}

func TestIsUserInput_RejectsMultiChar(t *testing.T) {
	// Multi-character text in a KeyPressMsg is always terminal garbage.
	for _, s := range []string{
		"hello",
		";2$y",
		"gb:0a0a/0e0e/1414",
		"]11;rgb:ffff/ffff/ffff",
		"[38;4R",
		"rgb:0a0a/0e0e/1414[38;4R",
		"0a/0e0e/1414[38;4R",
	} {
		if isUserInput(s) {
			t.Errorf("multi-char %q should be rejected", s)
		}
	}
}

func TestIsUserInput_NonPrintable(t *testing.T) {
	if isUserInput("\x00") {
		t.Error("expected non-printable char to be rejected")
	}
}

func TestIsUserInput_Empty(t *testing.T) {
	if isUserInput("") {
		t.Error("expected empty string to be rejected")
	}
}

// --- isUserPaste ---

func TestIsUserPaste_Normal(t *testing.T) {
	if !isUserPaste("hello world") {
		t.Error("expected normal text to be accepted as paste")
	}
	if !isUserPaste("line1\nline2") {
		t.Error("expected multiline paste to be accepted")
	}
}

func TestIsUserPaste_RejectsTerminalResponses(t *testing.T) {
	for _, s := range []string{
		"]11;rgb:0a0a/0e0e/1414",
		"rgb:0a0a/0e0e/1414[38;4R",
		";2$ygb:0a0a/0e0e/1414",
		"0a0a/0e0e/1414",
	} {
		if isUserPaste(s) {
			t.Errorf("terminal response %q should be rejected as paste", s)
		}
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

func TestAgentTextMsg_AppendsAfterToolForCorrectOrder(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{
			{role: "assistant", content: ""},
			{role: "tool", tool: "read", content: "42 lines"},
		}},
		running: true,
		agentCh: make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentTextMsg{text: "Final answer"})
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(mm.chatModel.Messages))
	}
	last := mm.chatModel.Messages[len(mm.chatModel.Messages)-1]
	if last.role != "assistant" {
		t.Fatalf("expected last message to be assistant, got %q", last.role)
	}
	if last.content != "Final answer" {
		t.Fatalf("expected last assistant content %q, got %q", "Final answer", last.content)
	}
}

func TestAgentDoneMsg_ClearsRunning(t *testing.T) {
	m := &model{
		chatModel: ChatModel{
			Messages:  []message{{role: "assistant", content: "done"}},
			Streaming: "text",
			Thinking:  "thought",
		},
		running: true,
		statusModel: StatusModel{
			ActiveTool:  "read",
			ActiveTools: map[string]time.Time{"read": {}},
		},
		agentCh: make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentDoneMsg{})
	mm := newM.(*model)
	if mm.running {
		t.Error("expected running=false after done")
	}
	if mm.statusModel.ActiveTool != "" {
		t.Errorf("expected empty activeTool, got %q", mm.statusModel.ActiveTool)
	}
	if mm.statusModel.ActiveTools != nil {
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
	if mm.statusModel.ActiveTool != "read" {
		t.Errorf("expected activeTool 'read', got %q", mm.statusModel.ActiveTool)
	}
}

func TestAgentToolResultMsg_ClearsActiveTool(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "tool", tool: "read", content: ""}}},
		running:   true,
		statusModel: StatusModel{
			ActiveTool:  "read",
			ActiveTools: map[string]time.Time{"read": {}},
		},
		agentCh: make(chan agentMsg, 64),
	}

	newM, _ := m.Update(agentToolResultMsg{
		name:    "read",
		content: `{"content":"hello","total_lines":1}`,
	})
	mm := newM.(*model)
	if mm.statusModel.ActiveTool != "" {
		t.Errorf("expected empty activeTool, got %q", mm.statusModel.ActiveTool)
	}
	if mm.chatModel.Messages[0].content == "" {
		t.Error("expected message content to be updated")
	}
}

func TestAgentDoneMsg_FiresLifecycleHooks(t *testing.T) {
	// Lifecycle hooks run through "sh -c", which is Git Bash on Windows and
	// would eat the backslashes in a native path. Forward slashes work for both
	// the shell and Go's file APIs.
	dir := filepath.ToSlash(t.TempDir())
	turnOut := dir + "/turn.json"
	inputOut := dir + "/input.json"
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: []message{{role: "assistant"}}},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
		cfg: Config{
			LifecycleHooks: []extension.HookConfig{
				{Event: "turn_complete", Command: `cat > "` + turnOut + `"`, Timeout: 5},
				{Event: "user_input_required", Command: `cat > "` + inputOut + `"`, Timeout: 5},
			},
		},
	}

	if _, err := m.handleAgentDone(agentDoneMsg{}); err != nil {
		t.Fatalf("handleAgentDone returned a Cmd, want nil")
	}

	for name, path := range map[string]string{"turn_complete": turnOut, "user_input_required": inputOut} {
		// Hooks run off the Update goroutine, so the write is not visible
		// the instant handleAgentDone returns.
		data := waitForHookOutput(t, path)
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s hook output: %v", name, err)
		}
		if got["event"] != name {
			t.Errorf("%s event = %v, want %s", name, got["event"], name)
		}
	}
}

// waitForHookOutput polls for a lifecycle hook's output file, which is written
// by the hook worker goroutine rather than by the caller of runLifecycleHooks.
func waitForHookOutput(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for hook output %s (last err: %v)", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLifecycleHooks_RunInOrder pins the serialization the hook worker
// provides. The events describe a state machine — turn_complete then
// user_input_required — so a consumer that maps them onto an external status
// would settle on the wrong final state if the two ran concurrently.
func TestLifecycleHooks_RunInOrder(t *testing.T) {
	out := filepath.ToSlash(t.TempDir()) + "/order.txt"
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: []message{{role: "assistant"}}},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
		cfg: Config{
			LifecycleHooks: []extension.HookConfig{
				// The first hook sleeps, so an unserialized implementation
				// would let the second one finish first.
				{Event: "turn_complete", Command: `sleep 0.3; echo turn >> "` + out + `"`, Timeout: 5},
				{Event: "user_input_required", Command: `echo input >> "` + out + `"`, Timeout: 5},
			},
		},
	}

	if _, err := m.handleAgentDone(agentDoneMsg{}); err != nil {
		t.Fatalf("handleAgentDone returned a Cmd, want nil")
	}

	var got string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(out)
		if err == nil {
			got = strings.TrimSpace(string(data))
			if got == "turn\ninput" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hook order = %q, want %q", got, "turn\ninput")
}

// TestLifecycleHooks_DoNotWriteToStderr guards the TUI's terminal: the Bubble
// Tea renderer owns the alternate screen, and anything written to stderr is
// painted over it without the renderer knowing those cells were dirtied.
func TestLifecycleHooks_DoNotWriteToStderr(t *testing.T) {
	done := filepath.ToSlash(t.TempDir()) + "/done"
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr, origLogOut := os.Stderr, stdlog.Writer()
	os.Stderr = w
	stdlog.SetOutput(w)
	t.Cleanup(func() {
		os.Stderr = origStderr
		stdlog.SetOutput(origLogOut)
		_ = r.Close()
	})

	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: []message{{role: "assistant"}}},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
		cfg: Config{
			LifecycleHooks: []extension.HookConfig{
				// Fails, and is noisy on both streams while doing so.
				{Event: "turn_complete", Command: "echo noise; echo boom >&2; exit 1", Timeout: 5},
				{Event: "user_input_required", Command: `echo done > "` + done + `"`, Timeout: 5},
			},
		},
	}

	if _, err := m.handleAgentDone(agentDoneMsg{}); err != nil {
		t.Fatalf("handleAgentDone returned a Cmd, want nil")
	}
	// The second hook is queued behind the first, so its marker appearing
	// means the failing hook has already been handled and logged.
	waitForHookOutput(t, done)

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe: %v", err)
	}
	leaked, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	if len(leaked) != 0 {
		t.Errorf("lifecycle hooks wrote %q to stderr, want nothing", leaked)
	}
}

func TestAgentDoneMsg_ErrorDoesNotFireUserInputHook(t *testing.T) {
	// Lifecycle hooks run through "sh -c", which is Git Bash on Windows and
	// would eat the backslashes in a native path. Forward slashes work for both
	// the shell and Go's file APIs.
	dir := filepath.ToSlash(t.TempDir())
	turnOut := dir + "/turn.json"
	inputOut := dir + "/input.json"
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: []message{{role: "assistant"}}},
		running:   true,
		agentCh:   make(chan agentMsg, 64),
		cfg: Config{LifecycleHooks: []extension.HookConfig{
			{Event: "turn_complete", Command: `cat > "` + turnOut + `"`, Timeout: 5},
			{Event: "user_input_required", Command: `cat > "` + inputOut + `"`, Timeout: 5},
		}},
	}

	m.handleAgentDone(agentDoneMsg{err: errors.New("request canceled")})
	data := waitForHookOutput(t, turnOut)
	var payload struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal turn_complete output: %v", err)
	}
	if payload.Data["error"] != true {
		t.Errorf("turn_complete error = %v, want true", payload.Data["error"])
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(inputOut); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("user_input_required hook ran after an error, stat err = %v", err)
	}
}
