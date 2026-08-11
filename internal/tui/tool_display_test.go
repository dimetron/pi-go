package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// TestRenderSkillTool verifies the skill-activation confirmation card renders
// as a single line: "◉ skill(disk-check) Successfully loaded skill". It must
// not open a content gutter like a regular tool card.
func TestRenderSkillTool(t *testing.T) {
	td := ToolDisplayModel{Width: 80}
	msg := message{
		role:    "tool",
		tool:    "skill",
		toolIn:  "disk-check",
		content: "Successfully loaded skill",
	}
	result := td.RenderToolMessage(msg)
	plain := stripANSI(result)
	if !strings.Contains(plain, "skill(disk-check)") {
		t.Errorf("expected skill name in parens, got %q", plain)
	}
	if !strings.Contains(plain, "Successfully loaded skill") {
		t.Errorf("expected success message, got %q", plain)
	}
	// Single line, no content gutter.
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d: %q", len(lines), result)
	}
	if strings.Contains(result, "│") {
		t.Errorf("skill card must not open a content gutter, got %q", result)
	}
}

// TestRenderSkillTool_NoContent renders a skill card with no result text; the
// header must still show the skill name.
func TestRenderSkillTool_NoContent(t *testing.T) {
	td := ToolDisplayModel{Width: 80}
	msg := message{
		role:   "tool",
		tool:   "skill",
		toolIn: "ponytail",
	}
	result := td.RenderToolMessage(msg)
	plain := stripANSI(result)
	if !strings.Contains(plain, "skill(ponytail)") {
		t.Errorf("expected skill name in parens, got %q", plain)
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

func TestArgWidth_ScalesWithTerminalWidth(t *testing.T) {
	// Narrow terminal floors at 60.
	if got := (ToolDisplayModel{Width: 40}).argWidth(); got != 60 {
		t.Errorf("narrow width: expected 60, got %d", got)
	}
	// Wide terminal gives more room for the command.
	if got := (ToolDisplayModel{Width: 200}).argWidth(); got != 180 {
		t.Errorf("wide width: expected 180, got %d", got)
	}
	// Default width (0 → 80) floors at 60.
	if got := (ToolDisplayModel{Width: 0}).argWidth(); got != 60 {
		t.Errorf("default width: expected 60, got %d", got)
	}
}

func TestRenderRegularTool_WideTerminalShowsLongerCommand(t *testing.T) {
	longCmd := strings.Repeat("x", 150)
	msg := message{role: "tool", tool: "bash", toolIn: longCmd}

	// Narrow terminal truncates the command.
	narrow := ToolDisplayModel{Width: 80}
	if out := narrow.RenderToolMessage(msg); strings.Contains(out, longCmd) {
		t.Error("narrow terminal should truncate the long command")
	}

	// Wide terminal shows the full command.
	wide := ToolDisplayModel{Width: 200}
	if out := wide.RenderToolMessage(msg); !strings.Contains(out, longCmd) {
		t.Error("wide terminal should show the full command")
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

// TestRenderRegularTool_Bash_OutputNotDim exercises the bash highlight branch.
// Bash output used to fall through to the dim (240) default. With the new
// highlightBashOutput path, output should get *some* color — either chroma's
// tokens or the non-gray fallback foreground (252) — and not stay plain dim.
func TestRenderRegularTool_Bash_OutputNotDim(t *testing.T) {
	td := ToolDisplayModel{Width: 100}
	msg := message{
		role:    "tool",
		tool:    "bash",
		toolIn:  "echo hello",
		content: "hello world\nplain text line",
	}
	result := td.RenderToolMessage(msg)
	// Must not be the dim (240) gray that the default branch would produce.
	// The gutter pipe is still 240, so look at the content line, not the pipe.
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + content lines, got %d: %q", len(lines), result)
	}
	// Body lines should not be plain dim 240. We pick the second body line
	// because chroma may or may not tokenize the first one; the second
	// ("plain text line") is too short to be confidently identified, so the
	// fallback foreground (252) should win.
	var bodyLines []string
	for _, l := range lines {
		if strings.Contains(l, "│") && !strings.HasPrefix(l, "●") {
			bodyLines = append(bodyLines, l)
		}
	}
	if len(bodyLines) < 2 {
		t.Fatalf("expected at least 2 body lines, got %d: %v", len(bodyLines), bodyLines)
	}
	// The gutter pipe is still 240; isolate the content portion (everything
	// after the first "│ ") and check that *that* is not dim. Chroma's
	// fallback lexer gives foreground 231 (bright white) for unrecognized
	// content, which is what "plain text line" should produce.
	stripped := strings.SplitN(bodyLines[1], "│ ", 2)
	if len(stripped) < 2 {
		t.Fatalf("body line missing gutter: %q", bodyLines[1])
	}
	if strings.Contains(stripped[1], "[38;5;240m") {
		t.Errorf("bash content fell back to dim gray (240), want non-dim color: %q", stripped[1])
	}
}

// TestRenderRegularTool_Bash_NoContent exercises the no-content path: the
// bash branch should not crash and should still emit a header.
func TestRenderRegularTool_Bash_NoContent(t *testing.T) {
	td := ToolDisplayModel{Width: 80}
	msg := message{
		role:   "tool",
		tool:   "bash",
		toolIn: "ls",
	}
	result := td.RenderToolMessage(msg)
	if !strings.Contains(result, "bash") {
		t.Error("expected tool name in header")
	}
}

// TestRenderRegularTool_Read_ToolIn_ProducesFilePathInHeader verifies the
// pre-existing happy path: a read message with toolIn renders the path in
// the header. The pre-fix subagent path set tool: but not toolIn, so this
// regression test pins the contract that renderRegularTool relies on.
func TestRenderRegularTool_Read_ToolIn_ProducesFilePathInHeader(t *testing.T) {
	td := ToolDisplayModel{Width: 100}
	msg := message{
		role:    "tool",
		tool:    "read",
		toolIn:  "internal/tui/tool_display.go",
		content: "     1\tpackage tui\n     2\t",
	}
	result := td.RenderToolMessage(msg)
	if !strings.Contains(result, "internal/tui/tool_display.go") {
		t.Errorf("expected file path in header, got: %s", result)
	}
	// Content is chroma-tokenized, so the literal "package tui" gets split by
	// ANSI escape codes. Strip escapes and confirm the body text is present.
	plain := stripANSI(result)
	if !strings.Contains(plain, "package tui") {
		t.Errorf("expected file content in body, got: %s", plain)
	}
}

// stripANSI removes ANSI escape sequences from s. Used by tests that need to
// match chroma-highlighted output without writing exact escape sequences.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestToolBullet_BlinkOnPending covers the pending-tool bullet blink: a card
// with no result yet renders ◉ when the blink phase is on and a same-width
// blank when it is off, so the header pulses without shifting columns. A card
// that already has its result renders the bullet solid in both phases.
func TestToolBullet_BlinkOnPending(t *testing.T) {
	style := lipgloss.NewStyle().Foreground(darkPalette.Tool).Bold(true)

	on := (&ToolDisplayModel{BlinkOn: true}).toolBullet(style, true)
	off := (&ToolDisplayModel{BlinkOn: false}).toolBullet(style, true)
	if !strings.Contains(on, "◉") {
		t.Errorf("pending card with BlinkOn=true should show ◉, got %q", on)
	}
	if strings.Contains(off, "◉") {
		t.Errorf("pending card with BlinkOn=false should hide ◉, got %q", off)
	}
	// The off phase must occupy the same two columns so the header does not
	// shift left and right every half second.
	if lipgloss.Width(off) != lipgloss.Width(on) {
		t.Errorf("blink off width %d != on width %d", lipgloss.Width(off), lipgloss.Width(on))
	}

	// Finished cards never blink.
	doneOn := (&ToolDisplayModel{BlinkOn: true}).toolBullet(style, false)
	doneOff := (&ToolDisplayModel{BlinkOn: false}).toolBullet(style, false)
	if !strings.Contains(doneOn, "◉") || !strings.Contains(doneOff, "◉") {
		t.Errorf("finished card should always show ◉, got on=%q off=%q", doneOn, doneOff)
	}
}

// TestRenderRegularTool_PendingBlink verifies the blink reaches the rendered
// header: a pending bash card alternates between a ◉ header and a blank-bullet
// header as BlinkOn flips, and the tool name stays put in both phases.
func TestRenderRegularTool_PendingBlink(t *testing.T) {
	msg := message{role: "tool", tool: "bash", toolIn: "sleep 10"}

	on := stripANSI((&ToolDisplayModel{Width: 100, BlinkOn: true}).RenderToolMessage(msg))
	off := stripANSI((&ToolDisplayModel{Width: 100, BlinkOn: false}).RenderToolMessage(msg))

	if !strings.Contains(on, "◉ bash") {
		t.Errorf("BlinkOn=true header should read ◉ bash, got %q", on)
	}
	if strings.Contains(off, "◉") {
		t.Errorf("BlinkOn=false header should hide the bullet, got %q", off)
	}
	if !strings.Contains(off, "bash(sleep 10)") {
		t.Errorf("BlinkOn=false header lost the tool name, got %q", off)
	}
}

// TestRenderKey_BlinkPhaseDistinct guards the render cache: the blink phase is
// part of the key for pending tool cards, so a cached render cannot freeze the
// bullet at one phase.
func TestRenderKey_BlinkPhaseDistinct(t *testing.T) {
	pending := message{role: "tool", tool: "bash", toolIn: "ls"}
	if pending.renderKey(80, false, false, false, 0, true) == pending.renderKey(80, false, false, false, 0, false) {
		t.Fatal("renderKey collides across blink phases -- cached render would freeze the bullet")
	}
	// Finished cards do not blink, so their key must not depend on the phase.
	done := message{role: "tool", tool: "bash", toolIn: "ls", content: "done"}
	if done.renderKey(80, false, false, false, 0, true) != done.renderKey(80, false, false, false, 0, false) {
		t.Fatal("finished card key depends on blink phase -- it would re-render needlessly")
	}
}
