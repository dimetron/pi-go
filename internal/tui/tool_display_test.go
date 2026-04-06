package tui

import (
	"strings"
	"testing"
)

func TestRenderCompactTool_RegularTool(t *testing.T) {
	td := ToolDisplayModel{Width: 80, CompactTools: true}
	msg := message{
		role:    "tool",
		tool:    "read",
		toolIn:  "main.go",
		content: `{"content":"package main\n","total_lines":1}`,
	}
	result := td.RenderToolMessage(msg)
	if !strings.Contains(result, "read") {
		t.Error("expected tool name in compact output")
	}
	if !strings.Contains(result, "✓") {
		t.Error("expected checkmark in compact output")
	}
	// Should be a single line (no multi-line content).
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line in compact output, got %d", len(lines))
	}
}

func TestRenderCompactTool_AgentTool(t *testing.T) {
	td := ToolDisplayModel{Width: 80, CompactTools: true}
	msg := message{
		role:      "tool",
		tool:      "agent",
		agentType: "explore",
		content:   "Found 3 files",
	}
	result := td.RenderToolMessage(msg)
	if !strings.Contains(result, "agent") {
		t.Error("expected tool name in compact agent output")
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line in compact agent output, got %d", len(lines))
	}
}

func TestRenderCompactTool_LongArgs(t *testing.T) {
	td := ToolDisplayModel{Width: 80, CompactTools: true}
	longArg := strings.Repeat("a", 100)
	msg := message{
		role:   "tool",
		tool:   "bash",
		toolIn: longArg,
	}
	result := td.RenderToolMessage(msg)
	// Args should be truncated.
	if strings.Contains(result, longArg) {
		t.Error("expected long args to be truncated")
	}
}

func TestRenderExpandedTool_Default(t *testing.T) {
	td := ToolDisplayModel{Width: 80, CompactTools: false}
	msg := message{
		role:    "tool",
		tool:    "read",
		toolIn:  "main.go",
		content: "     1\tpackage main\n     2\t\n     3\timport \"fmt\"",
	}
	result := td.RenderToolMessage(msg)
	// Expanded mode shows multi-line output with │ borders.
	if !strings.Contains(result, "│") {
		t.Error("expected pipe borders in expanded output")
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) < 2 {
		t.Error("expected multi-line expanded output")
	}
}

func TestCompactToggle_SwitchModes(t *testing.T) {
	td := ToolDisplayModel{Width: 80}
	if td.CompactTools {
		t.Error("expected compact mode off by default")
	}
	td.CompactTools = true
	msg := message{
		role:    "tool",
		tool:    "grep",
		toolIn:  "pattern",
		content: "file.go:1: match\nfile.go:2: another",
	}
	compact := td.RenderToolMessage(msg)
	td.CompactTools = false
	expanded := td.RenderToolMessage(msg)
	if compact == expanded {
		t.Error("compact and expanded output should differ")
	}
	compactLines := strings.Count(compact, "\n")
	expandedLines := strings.Count(expanded, "\n")
	if compactLines >= expandedLines {
		t.Errorf("compact (%d lines) should have fewer lines than expanded (%d lines)",
			compactLines, expandedLines)
	}
}

func TestRenderCompactTool_NoContent(t *testing.T) {
	td := ToolDisplayModel{Width: 80, CompactTools: true}
	msg := message{
		role:   "tool",
		tool:   "write",
		toolIn: "out.txt",
	}
	result := td.RenderToolMessage(msg)
	if !strings.Contains(result, "write") {
		t.Error("expected tool name")
	}
	// No checkmark when no content.
	if strings.Contains(result, "✓") {
		t.Error("expected no checkmark when content is empty")
	}
}

func TestContentWidth_DefaultWhenZero(t *testing.T) {
	td := ToolDisplayModel{Width: 0}
	cw := td.contentWidth()
	// Width 0 → default 80, 80*8/10 - 4 = 60
	if cw != 60 {
		t.Errorf("expected 60, got %d", cw)
	}
}

func TestContentWidth_NormalWidth(t *testing.T) {
	td := ToolDisplayModel{Width: 120}
	cw := td.contentWidth()
	// 120*8/10 - 4 = 92
	if cw != 92 {
		t.Errorf("expected 92, got %d", cw)
	}
}

func TestContentWidth_SmallWidth(t *testing.T) {
	td := ToolDisplayModel{Width: 30}
	cw := td.contentWidth()
	// Width 30 < 40 → default 80, 80*8/10 - 4 = 60
	if cw != 60 {
		t.Errorf("expected 60, got %d", cw)
	}
}

func TestSoftWrap_ShortLine(t *testing.T) {
	lines := softWrap("hello world", 80)
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Errorf("expected no wrap, got %v", lines)
	}
}

func TestSoftWrap_LongLine(t *testing.T) {
	long := strings.Repeat("abcdef ", 20) // 140 chars
	lines := softWrap(long, 40)
	if len(lines) < 2 {
		t.Error("expected wrapping for long line")
	}
	for _, l := range lines {
		if len(l) > 42 { // allow small overflow for word boundaries
			t.Errorf("wrapped line too long: %d chars", len(l))
		}
	}
}

func TestSoftWrap_ZeroWidth(t *testing.T) {
	lines := softWrap("hello", 0)
	if len(lines) != 1 || lines[0] != "hello" {
		t.Errorf("expected no wrap for zero width, got %v", lines)
	}
}

func TestRenderRegularTool_SoftWrapsLongContent(t *testing.T) {
	long := strings.Repeat("x", 200)
	td := ToolDisplayModel{Width: 100}
	msg := message{
		role:    "tool",
		tool:    "bash",
		content: long,
	}
	result := td.RenderToolMessage(msg)
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	// First line is the tool header, remaining lines are content.
	// Content should be wrapped into multiple lines.
	contentLines := 0
	for _, l := range lines {
		if strings.Contains(l, "│") {
			contentLines++
		}
	}
	if contentLines < 2 {
		t.Errorf("expected long content to wrap into multiple lines, got %d content lines", contentLines)
	}
}

func TestRenderAgentTool_SoftWrapsLongResult(t *testing.T) {
	long := strings.Repeat("y", 200)
	td := ToolDisplayModel{Width: 100}
	msg := message{
		role:      "tool",
		tool:      "agent",
		agentType: "task",
		agentID:   "sub-1",
		content:   long[:100], // capped at 100 by renderAgentTool
	}
	result := td.RenderToolMessage(msg)
	if !strings.Contains(result, "│") {
		t.Error("expected bordered result summary")
	}
}
