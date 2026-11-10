package tui

import (
	"encoding/json"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dimetron/pi-go/internal/tools"
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
	if pending.renderKey(80, false, false, false, 0, true, VerbositySmart) == pending.renderKey(80, false, false, false, 0, false, VerbositySmart) {
		t.Fatal("renderKey collides across blink phases -- cached render would freeze the bullet")
	}
	// Finished cards do not blink, so their key must not depend on the phase.
	done := message{role: "tool", tool: "bash", toolIn: "ls", content: "done"}
	if done.renderKey(80, false, false, false, 0, true, VerbositySmart) != done.renderKey(80, false, false, false, 0, false, VerbositySmart) {
		t.Fatal("finished card key depends on blink phase -- it would re-render needlessly")
	}
}

// TestToolResultSummary_DiffShapes guards the bug that made diff cards
// unreadable: git-file-diff and git-hunk have no entry in toolResultShapes, so
// both fell through to the JSON fallback and the card printed
// `{"diff":"@@ -1,3 +1,4 @@\n-old\n+new\n",...}` — the one tool whose payload is
// meant to be read as lines was the one rendering as one mangled line.
func TestToolResultSummary_DiffShapes(t *testing.T) {
	diff, _ := json.Marshal(map[string]any{
		"file": "a.go", "diff": "@@ -1,3 +1,4 @@\n ctx\n-old\n+new\n",
		"lines_added": 1, "lines_removed": 1,
	})
	got := toolResultSummary(string(diff))
	if strings.Contains(got, `"diff"`) || strings.Contains(got, `{\"`) {
		t.Errorf("git-file-diff summarized as JSON: %q", got)
	}
	for _, want := range []string{"(1 added, 1 removed)", "@@ -1,3 +1,4 @@", "-old", "+new"} {
		if !strings.Contains(got, want) {
			t.Errorf("git-file-diff summary missing %q:\n%s", want, got)
		}
	}

	hunks, _ := json.Marshal(map[string]any{
		"file": "a.go", "total_hunks": float64(1),
		"hunks": []any{map[string]any{
			"header": "@@ -1,2 +1,2 @@", "content": " ctx\n-a\n+b\n",
			"added": float64(1), "removed": float64(1),
		}},
	})
	got = toolResultSummary(string(hunks))
	if strings.Contains(got, `"hunks"`) {
		t.Errorf("git-hunk summarized as JSON: %q", got)
	}
	if !strings.Contains(got, "@@ -1,2 +1,2 @@") || !strings.Contains(got, "+b") {
		t.Errorf("git-hunk summary missing header or body:\n%s", got)
	}

	// A binary diff has no textual hunk to show, and must say so rather than
	// printing an empty card.
	bin, _ := json.Marshal(map[string]any{"file": "a.png", "diff": "", "binary": true})
	if got := toolResultSummary(string(bin)); !strings.Contains(got, "binary") {
		t.Errorf("binary diff summary = %q, want a binary notice", got)
	}

	// A hunk-less file is a real answer, not an unrecognized shape.
	empty, _ := json.Marshal(map[string]any{"file": "a.go", "hunks": []any{}, "total_hunks": float64(0)})
	if got := toolResultSummary(string(empty)); !strings.Contains(got, "no changes") {
		t.Errorf("empty hunk summary = %q, want a no-changes notice", got)
	}
}

// TestHighlightDiffOutput pins the diff palette to the renderer: the four
// ThemeColors diff tokens existed and were drawn in the /theme preview strip,
// but no renderer read them, so a diff could not look like a diff.
func TestHighlightDiffOutput(t *testing.T) {
	p := darkPalette
	lines := []string{"@@ -1,3 +1,4 @@", " context", "-removed", "+added", "diff --git a/a.go b/a.go"}

	got := highlightDiffOutput(lines, p)
	if len(got) != len(lines) {
		t.Fatalf("highlightDiffOutput returned %d lines for %d inputs", len(got), len(lines))
	}
	// Added rows carry the added foreground; removed rows the removed one.
	if !strings.Contains(got[3], truecolorFragment(p.DiffAddedText)) {
		t.Errorf("added line not styled with DiffAddedText: %q", got[3])
	}
	if !strings.Contains(got[2], truecolorFragment(p.DiffRemovedText)) {
		t.Errorf("removed line not styled with DiffRemovedText: %q", got[2])
	}
	// The +/- gutter is not the only cue: a background tint distinguishes the
	// rows even where the glyphs are not color-differentiated.
	if !strings.Contains(got[3], "48;2;") && !strings.Contains(got[3], "48;5;") {
		t.Errorf("added line has no background tint: %q", got[3])
	}
	// A hunk header is chrome, not content: it must not take a diff row color.
	if strings.Contains(got[0], truecolorFragment(p.DiffAddedText)) {
		t.Errorf("hunk header styled as an added line: %q", got[0])
	}
}

// TestTruncateRunes_SmallBound is a regression guard, not a style preference:
// the old body was `string(r[:n-3]) + "..."` with no floor, so n < 3 sliced with
// a negative bound and panicked. argWidth() floors at 60 today, which is the only
// reason this never fired in production — one new call site away from a crash.
func TestTruncateRunes_SmallBound(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 2, 3, 4} {
		got := truncateRunes("abcdef", n)
		if len([]rune(got)) > max(n, 0) {
			t.Errorf("truncateRunes(%q, %d) = %q, exceeds the bound", "abcdef", n, got)
		}
	}
	if got := truncateRunes("abcdef", 0); got != "" {
		t.Errorf("truncateRunes with n=0 = %q, want empty", got)
	}
	// A string that already fits is returned untouched at every bound.
	if got := truncateRunes("ab", 2); got != "ab" {
		t.Errorf("truncateRunes(%q, 2) = %q, want it unchanged", "ab", got)
	}
}

// TestToolResultSummary_InspectorShapes pins shapes for the tools that render
// repository and image inspection results. Both were missing, so a git-overview
// card printed the whole repo state as one JSON line and a read_image card
// printed its dimensions as JSON — output a reader cannot parse at a glance.
//
// Payloads are marshaled from the tools package's real output structs rather
// than hand-written maps: a fixture I invented would encode the field names I
// assumed, which is the thing under test.
func TestToolResultSummary_InspectorShapes(t *testing.T) {
	t.Run("git-overview", func(t *testing.T) {
		raw, err := json.Marshal(tools.GitOverviewOutput{
			Branch: "main", Upstream: "origin/main", Ahead: 2, Behind: 1,
			RecentCommits:  []string{"abc1234 fix(tui): x"},
			StagedFiles:    []string{"a.go"},
			UnstagedFiles:  []string{"b.go"},
			UntrackedFiles: []string{"c.go", "d.go"},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := toolResultSummary(string(raw))
		if strings.Contains(got, `"branch"`) {
			t.Fatalf("git-overview summarized as JSON: %q", got)
		}
		// The upstream position and every dirty-state list must survive: these are
		// the fields a reader acts on, so a cap or a dropped section is a
		// regression even though the summary would look tidier.
		for _, want := range []string{"main", "origin/main", "+2/-1", "staged", "a.go", "unstaged", "b.go", "untracked", "c.go", "d.go"} {
			if !strings.Contains(got, want) {
				t.Errorf("git-overview summary missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("git-overview without upstream", func(t *testing.T) {
		raw, _ := json.Marshal(tools.GitOverviewOutput{Branch: "main", RecentCommits: []string{"abc1234 y"}})
		got := toolResultSummary(string(raw))
		if strings.Contains(got, "→") {
			t.Errorf("a repo with no upstream printed an arrow: %q", got)
		}
		if !strings.HasPrefix(got, "main") {
			t.Errorf("summary should start with the branch: %q", got)
		}
	})

	t.Run("read_image", func(t *testing.T) {
		raw, _ := json.Marshal(tools.ReadImageOutput{
			Path: "shot.png", MIMEType: "image/png", Width: 800, Height: 600, Size: 12345,
		})
		got := toolResultSummary(string(raw))
		if strings.Contains(got, `"path"`) {
			t.Fatalf("read_image summarized as JSON: %q", got)
		}
		for _, want := range []string{"shot.png", "800x600", "image/png", "12345 bytes"} {
			if !strings.Contains(got, want) {
				t.Errorf("read_image summary missing %q: %q", want, got)
			}
		}
	})

	t.Run("read_image undetectable dimensions", func(t *testing.T) {
		// Width/height are 0 when the decoder cannot measure the image; the summary
		// must not print "0x0".
		raw, _ := json.Marshal(tools.ReadImageOutput{Path: "shot.png", MIMEType: "image/png", Size: 10})
		got := toolResultSummary(string(raw))
		if strings.Contains(got, "0x0") {
			t.Errorf("undetectable dimensions rendered as 0x0: %q", got)
		}
	})
}

// TestHighlightGitOverviewOutput_ReachesTheRenderer is the guard the plan
// credited to TestToolResultSummary_InspectorShapes and did not get.
//
// InspectorShapes calls toolResultSummary, which builds the summary text but
// never renders a card. highlightGitOverviewOutput therefore had zero coverage:
// it could have been deleted and the summary test would still have passed. This
// renders a real card so the styling path is actually executed.
func TestHighlightGitOverviewOutput_ReachesTheRenderer(t *testing.T) {
	raw, err := json.Marshal(tools.GitOverviewOutput{
		Branch:         "main",
		Upstream:       "origin/main",
		Ahead:          2,
		Behind:         1,
		RecentCommits:  []string{"abc1234 fix(tui): x"},
		StagedFiles:    []string{"a.go"},
		UnstagedFiles:  []string{"b.go"},
		UntrackedFiles: []string{"c.go"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mirror production: attachToolResult stores toolResultSummary(raw) as the
	// card content (agent_loop.go), so the renderer receives the summary lines,
	// not the JSON. Feeding the card raw JSON would test a shape no producer
	// emits, and highlightToolOutput's git-overview branch would still be dark.
	content := toolResultSummary(string(raw))
	if strings.Contains(content, `"branch"`) {
		t.Fatalf("summary is raw JSON, so the card would render JSON: %q", content)
	}

	td := &ToolDisplayModel{Width: 100}
	card := td.RenderToolMessage(message{role: "tool", tool: "git-overview", content: content})
	if strings.TrimSpace(card) == "" {
		t.Fatal("git-overview card rendered nothing")
	}
	plain := ansi.Strip(card)

	// Every section the summary carries must survive into the card: a renderer
	// that drops the dirty-state lists is a regression whatever the styling.
	for _, want := range []string{"main", "origin/main", "a.go", "b.go", "c.go"} {
		if !strings.Contains(plain, want) {
			t.Errorf("git-overview card is missing %q:\n%s", want, plain)
		}
	}
	// The distinguishing property is not "is it styled" but "is it styled by row
	// kind": the branch row takes Primary, section headers take Tool, commit
	// subjects Faint and file paths Text. Without the dispatch every content row
	// falls to the default branch and is dimmed identically.
	//
	// Four earlier versions of this assertion were hollow, and each passed with
	// the dispatch removed: "the card contains an escape" (chrome carries ANSI),
	// calling the helper directly (exercises it regardless of dispatch),
	// comparing against an unstyled render (default branch still dims, so the two
	// differed for the wrong reason), and parsing style prefixes out of the card
	// (the gutter's own escapes confound it). Asserting the row-to-role mapping
	// tests what the helper promises instead of how it encodes it.
	p := darkPalette
	roleColor := func(row string, role color.Color) bool {
		want := lipgloss.NewStyle().Foreground(role).Render(ansi.Strip(row))
		return row == want
	}
	plainRows := strings.Split(content, "\n")
	styled := highlightGitOverviewOutput(plainRows, p)
	if len(styled) != len(plainRows) {
		t.Fatalf("got %d styled rows for %d inputs", len(styled), len(plainRows))
	}
	for i, row := range styled {
		if ansi.Strip(row) != plainRows[i] {
			t.Errorf("row %d content changed: %q -> %q", i, plainRows[i], ansi.Strip(row))
		}
	}
	// Select by content, not position: the summary interleaves branch, commit,
	// a section header and its file rows, so an index would silently start
	// asserting on the wrong row the moment the summary's order changed.
	find := func(substr string) string {
		for _, row := range styled {
			if strings.Contains(ansi.Strip(row), substr) {
				return row
			}
		}
		t.Fatalf("no row contains %q", substr)
		return ""
	}
	branch := find("main → origin/main")
	commit := find("abc1234")
	file := find("a.go")
	if !roleColor(branch, p.Primary) {
		t.Errorf("branch row is not Primary: %q", branch)
	}
	if !roleColor(commit, p.Faint) {
		t.Errorf("commit row is not Faint: %q", commit)
	}
	if !roleColor(file, p.Text) {
		t.Errorf("file row is not Text: %q", file)
	}
	if branch == file {
		t.Error("branch and file rows rendered identically")
	}
	// The dispatch itself is not asserted here, and deliberately so: every attempt
	// to detect it through the card failed for a reason that was not the dispatch.
	// An unknown tool is dimmed by the default branch and git-overview is styled
	// per row, but the two renders differ under several removals, so comparing
	// them proves nothing about the case. The row-to-role mapping above is the
	// real contract; TestHighlightGitOverviewOutput_StylesEachRowKind covers the
	// branch/header/file discrimination, and the frame test covers the card.
}

// TestHighlightGitOverviewOutput_StylesEachRowKind exercises the four branches of
// the helper directly: branch line, a section header, a commit row, and a file row.
//
// Driving it directly rather than through a card is deliberate — the branch/header
// distinction is a string-shape rule, and a card fixture cannot vary it one case at
// a time.
func TestHighlightGitOverviewOutput_StylesEachRowKind(t *testing.T) {
	p := darkPalette
	lines := []string{
		"main → origin/main (+2/-1)", // 0: branch
		"staged [1]:",                // unindented, colon suffix: header
		"  a.go",                     // indented, not a hash: file
		"  abc1234 fix(tui): x",      // indented, hash prefix: commit
		"trailing loose line",        // default branch
	}
	out := highlightGitOverviewOutput(lines, p)

	if len(out) != len(lines) {
		t.Fatalf("got %d styled lines for %d inputs", len(out), len(lines))
	}
	for i, line := range out {
		if ansi.Strip(line) != lines[i] {
			t.Errorf("row %d content changed: %q -> %q", i, lines[i], ansi.Strip(line))
		}
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("row %d was not styled: %q", i, line)
		}
	}
	// The branch row and a file row must not share a style; that difference is
	// what makes the panel readable at a glance.
	if out[0] == out[2] {
		t.Error("branch row and file row rendered identically")
	}
}

func TestLooksLikeCommitRow(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"  abc1234 fix(tui): x", true},
		{"  abc1234", false},         // no subject
		{"  abcdef", false},          // hash too short
		{"  a.go", false},            // a path, not a hash
		{"  README.md notes", false}, // not hex
		{"  ABC1234 upper", false},   // hex is lowercase only
		{"  " + strings.Repeat("a", 40) + " long", true},
		{"  " + strings.Repeat("a", 41) + " too long", false},
	}
	for _, tc := range cases {
		if got := looksLikeCommitRow(tc.in); got != tc.want {
			t.Errorf("looksLikeCommitRow(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
