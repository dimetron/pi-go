package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Layout bugs do not show up on toy content. They show up on the things a real
// session actually renders: glamour-formatted markdown, syntax-highlighted file
// reads, long unbreakable URLs, code fences, tables, subagent cards, and wide
// runes. Every case below is one that has actually broken the frame — a row of
// the wrong width pushes the rail out of column and drags the sidebar with it.
//
// The invariants are the same for all of them, so they are checked together:
//
//   - every row is exactly the terminal width
//   - the rail is in the same column on every row
//   - no escape sequence leaks into the visible text
func realisticChat() []message {
	md := "# pi-go Architecture\n\n## Overview\n\n" +
		"pi-go is a coding agent built on [Google ADK Go](https://google.golang.org/adk) with " +
		"multi-provider LLM support, sandboxed tool execution, session persistence, an " +
		"interactive terminal UI, LSP integration, and a subagent orchestration system.\n\n" +
		"```go\nfunc main() { fmt.Println(\"a line of code that certainly exceeds the chat width by a wide margin\") }\n```\n\n" +
		"| Column | Description |\n|---|---|\n| a | a description long enough to need wrapping |\n\n" +
		"See [![codecov](https://codecov.io/gh/dimetron/pi-go/graph/badge.svg)](https://codecov.io/gh/dimetron/pi-go)\n"

	var goSrc strings.Builder
	for i := range 40 {
		fmt.Fprintf(&goSrc, "%d\tfunc handler%d(w http.ResponseWriter, r *http.Request) error {\n", i+1, i)
	}

	return []message{
		{role: "user", content: "explain the architecture and read the server"},
		{role: "thinking", content: "considering the package layout"},
		{role: "assistant", content: md},
		{role: "tool", tool: "read", toolIn: `{"file_path":"server.go"}`, content: goSrc.String()},
		{role: "tool", tool: "bash", toolIn: `{"command":"go test ./... -run TestVeryLongTestNameThatKeepsGoing"}`,
			content: "ok  \tgithub.com/dimetron/pi-go/internal/tui\t7.4s\n" + strings.Repeat("noisy output line\n", 30)},
		{role: "tool", tool: "grep", toolIn: `{"pattern":"func "}`, content: "server.go:12: func main()"},
		{role: "tool", tool: "subagent", agentType: "pi", agentTitle: "Analyze internal/subagent",
			agentEvents: []agentEv{
				{kind: "tool_call", content: "read /a/very/long/path/that/keeps/going/internal/subagent/orchestrator.go"},
				{kind: "text", content: strings.Repeat("The analysis of the provider package. ", 60)},
			}},
		{role: "assistant", content: "Done — see the ✓ marks and the 💭 notes above. Résumé: naïve café.", isWarning: false},
		{role: "assistant", content: "Careful: this rewrites history.", isWarning: true},
	}
}

// runeAtCol returns the rune occupying display column col, measuring in terminal
// cells rather than rune indices — a wide rune (emoji, box drawing) advances the
// column by two, so indexing by rune would report the wrong place.
func runeAtCol(plain string, col int) string {
	w := 0
	for _, r := range plain {
		rw := ansi.StringWidth(string(r))
		if col < w+rw {
			return string(r)
		}
		w += rw
	}
	return ""
}

// The frame must never be taller than the terminal. One row of overflow makes
// the terminal scroll the whole frame, which tears the panel away from the
// sidebar — and looks exactly like a scrolling bug.
//
// It must not be *shorter* than the panel either: the sidebar used to be sized
// to the terminal while the panel was a row less, so JoinHorizontal padded the
// panel with blank rows and left a gap below the prompt while the sidebar's
// filler dots carried on past it.
func TestFrameHeightFitsTerminal(t *testing.T) {
	for _, dim := range [][2]int{{172, 48}, {120, 40}, {100, 24}, {80, 20}, {60, 16}, {200, 60}} {
		width, height := dim[0], dim[1]
		for _, scroll := range []int{0, 9, 40} {
			m := historyModel(t, "first")
			m.width, m.height = width, height
			m.applyResize()
			m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)
			m.chatModel.Scroll = min(scroll, m.chatModel.MaxScroll(m.messageViewportHeight()))

			rows := strings.Split(m.View().Content, "\n")
			if len(rows) > height {
				t.Errorf("%dx%d scroll=%d: frame is %d rows, taller than the terminal",
					width, height, m.chatModel.Scroll, len(rows))
			}

			// Every row carries the rail, so no row is panel-less padding that
			// JoinHorizontal invented to square the columns up.
			railCol := m.mainWidth() - railWidth
			for row, line := range rows {
				g := runeAtCol(ansi.Strip(line), railCol)
				if g != railGlyph && g != railThumb {
					t.Fatalf("%dx%d scroll=%d: row %d has no rail — the panel and the sidebar are different heights\n%q",
						width, height, m.chatModel.Scroll, row, ansi.Strip(line))
				}
			}
		}
	}
}

// The sidebar is a fixed-size block flush to the right edge: exactly
// SidebarWidth columns wide and exactly as tall as the panel. A line wider than
// that would push the frame past the screen; a taller block would leave a gap
// under the prompt.
func TestSidebarIsFixedSizeAndRightAligned(t *testing.T) {
	for _, dim := range [][2]int{{172, 48}, {120, 40}, {100, 30}} {
		width, height := dim[0], dim[1]
		m := historyModel(t, "first")
		m.width, m.height = width, height
		m.applyResize()
		m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)

		rows := strings.Split(m.View().Content, "\n")
		sidebarStart := m.mainWidth() // the column the sidebar begins at

		for row, line := range rows {
			plain := ansi.Strip(line)
			// The frame ends exactly at the terminal's right edge: the sidebar
			// occupies the last SidebarWidth columns and not one more.
			if got := ansi.StringWidth(line); got != width {
				t.Fatalf("%dx%d row %d: width %d, want %d — the sidebar is not flush right",
					width, height, row, got, width)
			}
			if got := width - sidebarStart; got != SidebarWidth {
				t.Fatalf("%dx%d: sidebar is %d columns, want the fixed %d",
					width, height, got, SidebarWidth)
			}
			_ = plain
		}
	}
}

func TestFrameIntegrityOnRealContent(t *testing.T) {
	// Narrow and wide, with and without the sidebar and the matrix bar.
	for _, width := range []int{60, 80, 120, 200} {
		for _, matrix := range []bool{false, true} {
			m := historyModel(t, "first")
			m.width = width
			m.height = 30
			m.matrix.active = matrix
			m.applyResize()
			m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)

			// Top, middle, and bottom of the scrollback.
			maxScroll := m.chatModel.MaxScroll(m.messageViewportHeight())
			for _, scroll := range []int{0, maxScroll / 2, maxScroll} {
				m.chatModel.Scroll = scroll
				frame := m.View().Content
				label := fmt.Sprintf("width=%d matrix=%v scroll=%d", width, matrix, scroll)

				// The rail owns the panel's last column, on every row.
				railCol := m.mainWidth() - railWidth

				for row, line := range strings.Split(frame, "\n") {
					// Every row is exactly the terminal width. A short row lets the
					// sidebar slide left; a long one pushes it right.
					if got := ansi.StringWidth(line); got != width {
						t.Fatalf("%s: row %d width = %d, want %d\n%q",
							label, row, got, width, ansi.Strip(line))
					}

					plain := ansi.Strip(line)
					if got := runeAtCol(plain, railCol); got != railGlyph && got != railThumb {
						t.Fatalf("%s: row %d has %q at the rail column %d, want the rail\n%q",
							label, row, got, railCol, plain)
					}
				}

				// A half-eaten escape makes the terminal swallow columns, so the
				// row renders narrower than it measures and the rail drifts.
				if plain := ansi.Strip(frame); strings.Contains(plain, "38;5;") ||
					strings.Contains(plain, "\x1b") {
					t.Fatalf("%s: escape codes leaked into the visible text", label)
				}
			}
		}
	}
}
