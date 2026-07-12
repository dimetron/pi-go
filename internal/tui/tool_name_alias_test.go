package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The grep tool registers as "ripgrep" whenever rg is installed
// (internal/tools/grep.go newGrepTool), so the display layer must recognize
// both names. Matching only "grep" meant that on any machine with ripgrep —
// the common case — grep output silently lost its highlighting and its
// pattern in the header.
func TestGrepAliasIsRecognized(t *testing.T) {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	td := &ToolDisplayModel{Width: 100}

	content := "internal/tools/grep.go:137:func grepHandler(sb *Sandbox) {"

	for _, name := range []string{"grep", "ripgrep"} {
		t.Run(name, func(t *testing.T) {
			// toolCallSummary must surface the pattern under either name.
			got := toolCallSummary(name, map[string]any{"pattern": "grepHandler"})
			if got != "grepHandler" {
				t.Errorf("toolCallSummary(%q) = %q, want the pattern", name, got)
			}

			// And the rendered block must be colored, not the dim default.
			out := td.renderRegularTool(message{
				role: "tool", tool: name, toolIn: "grepHandler", content: content,
			}, dim)

			// highlightGrepOutput colors the filename blue (39) and the line
			// number gray (240); the dim default branch emits neither.
			if !strings.Contains(out, "38;5;39") {
				t.Errorf("%q output not highlighted (no grep file color); got %q", name, out)
			}
		})
	}
}
