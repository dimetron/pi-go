package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		msg  message
		want blockKind
	}{
		{"user", message{role: "user"}, blockUser},
		{"assistant", message{role: "assistant"}, blockAssistant},
		{"warning", message{role: "assistant", isWarning: true}, blockWarning},
		{"thinking", message{role: "thinking"}, blockThinking},
		{"read tool", message{role: "tool", tool: "read"}, blockRead},
		{"grep tool", message{role: "tool", tool: "grep"}, blockRead},
		{"edit tool", message{role: "tool", tool: "edit"}, blockEdit},
		{"write tool", message{role: "tool", tool: "write"}, blockEdit},
		{"bash tool", message{role: "tool", tool: "bash"}, blockExecute},
		{"subagent", message{role: "tool", tool: "subagent"}, blockAgent},
		{"mcp tool", message{role: "tool", tool: "some_mcp_thing"}, blockTool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kindOf(&tt.msg); got != tt.want {
				t.Errorf("kindOf() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDominantKind(t *testing.T) {
	// Blanks never win: a row that mostly covers a tool block reads as that tool
	// even when it also catches the separator lines around it.
	got := dominantKind([]blockKind{blockNone, blockEdit, blockEdit, blockNone})
	if got != blockEdit {
		t.Errorf("dominantKind() = %d, want blockEdit", got)
	}
	if got := dominantKind([]blockKind{blockNone, blockNone}); got != blockNone {
		t.Errorf("all-blank run = %d, want blockNone", got)
	}
}

// The minimap maps the whole conversation into the rows it has, not just the
// visible slice — that is what makes it a map rather than a scrollbar.
func TestRenderMinimapCompressesWholeConversation(t *testing.T) {
	kinds := make([]blockKind, 100)
	for i := range kinds {
		kinds[i] = blockEdit
	}

	cells := renderMinimap(kinds, 0, 10, 10)

	if len(cells) != 10 {
		t.Fatalf("got %d cells, want one per row (10)", len(cells))
	}
	// Every cell is exactly railWidth wide — a bar that varies in width reads as
	// a jumping rail.
	for i, c := range cells {
		if got := ansi.StringWidth(c); got != railWidth {
			t.Fatalf("cell %d has width %d, want %d", i, got, railWidth)
		}
	}
}

// The minimap and the separator are the same rail, so they must be the same
// width — otherwise the rule appears to change thickness as you scroll.
func TestMinimapWidthMatchesSeparator(t *testing.T) {
	sep := ansi.StringWidth(separatorCell())

	for _, cell := range renderMinimap([]blockKind{blockUser, blockEdit}, 0, 1, 2) {
		if got := ansi.StringWidth(cell); got != sep {
			t.Errorf("minimap cell width = %d, separator width = %d; they must match", got, sep)
		}
	}
	if sep != railWidth {
		t.Errorf("separator width = %d, want railWidth %d", sep, railWidth)
	}
}

// The thumb is a fixed three dots that ride the track, not a bar sized to the
// visible fraction — that would swell to fill the rail on a short conversation
// and vanish on a long one.
func TestScrollThumbIsThreeDots(t *testing.T) {
	kinds := make([]blockKind, 200)
	for i := range kinds {
		kinds[i] = blockAssistant
	}

	countThumb := func(cells []string) (n, first int) {
		first = -1
		for i, c := range cells {
			if strings.Contains(c, railThumb) {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		return n, first
	}

	// Whatever the viewport size, the thumb stays three rows.
	for _, view := range [][2]int{{0, 10}, {0, 100}, {95, 105}, {190, 200}} {
		cells := renderMinimap(kinds, view[0], view[1], 20)
		if n, _ := countThumb(cells); n != thumbRows {
			t.Errorf("viewport %v: thumb is %d rows, want %d", view, n, thumbRows)
		}
	}

	// And it rides the track: top of the conversation puts it at the top, the
	// bottom puts it at the bottom.
	_, atTop := countThumb(renderMinimap(kinds, 0, 10, 20))
	_, atBottom := countThumb(renderMinimap(kinds, 190, 200, 20))
	if atTop != 0 {
		t.Errorf("scrolled to the top, thumb starts at row %d, want 0", atTop)
	}
	if want := 20 - thumbRows; atBottom != want {
		t.Errorf("scrolled to the bottom, thumb starts at row %d, want %d", atBottom, want)
	}
	if atTop >= atBottom {
		t.Error("the thumb does not move with the scroll position")
	}
}

// A rail shorter than the thumb is filled, rather than showing a thumb longer
// than its track.
func TestScrollThumbOnShortRail(t *testing.T) {
	cells := renderMinimap([]blockKind{blockUser, blockEdit}, 0, 2, 2)
	for i, c := range cells {
		if !strings.Contains(c, railThumb) {
			t.Errorf("row %d of a 2-row rail has no thumb", i)
		}
	}
}

// Always visible: an empty chat still gets a track, so the column never appears
// or disappears under the user.
func TestRenderMinimapAlwaysDrawsATrack(t *testing.T) {
	cells := renderMinimap(nil, 0, 0, 5)
	if len(cells) != 5 {
		t.Fatalf("got %d cells for an empty chat, want 5", len(cells))
	}
	for i, c := range cells {
		if got := ansi.StringWidth(c); got != railWidth {
			t.Errorf("empty-chat cell %d width = %d, want %d", i, got, railWidth)
		}
	}
}

// The body is pinned to a fixed width so the rail column beside it lands in the
// same place no matter how ragged the text is.
func TestPanelBodyIsFixedWidth(t *testing.T) {
	panel := "header\nshort\na much longer line of text here\n\nx\nstatus"

	out := padLinesTo(panel, 40)

	for i, line := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(line); got != 40 {
			t.Errorf("line %d width = %d, want 40", i, got)
		}
	}
}

// A line longer than the body width is truncated rather than allowed to push the
// rail (and the sidebar behind it) out of column.
func TestPanelBodyTruncatesOverlongLines(t *testing.T) {
	long := strings.Repeat("x", 200)

	if got := ansi.StringWidth(padLinesTo(long, 20)); got != 20 {
		t.Fatalf("width = %d, want 20", got)
	}
}

// The rail is one continuous column, one row per panel row: minimap alongside
// the messages, a plain divider above and below them. Drawing the minimap AND a
// sidebar border put two vertical rules side by side, which is the overloaded
// gutter this replaces.
func TestRailIsContinuousAndSingleColumn(t *testing.T) {
	// Two header rows, three message rows, two footer rows.
	cells := renderMinimap([]blockKind{blockUser, blockEdit, blockAssistant}, 0, 3, 3)

	out := railColumn(7, 2, cells)
	lines := strings.Split(out, "\n")

	if len(lines) != 7 {
		t.Fatalf("got %d rail rows, want one per panel row (7)", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != railWidth {
			t.Errorf("rail row %d width = %d, want %d", i, got, railWidth)
		}
		plain := ansi.Strip(line)
		if plain != railGlyph && plain != railThumb {
			t.Errorf("rail row %d is %q, want a rail glyph", i, plain)
		}
	}

	// Message rows carry the viewport marker; header/footer rows are plain.
	for _, row := range []int{2, 3, 4} {
		if !strings.Contains(lines[row], railThumb) {
			t.Errorf("message row %d has no minimap bar", row)
		}
	}
	for _, row := range []int{0, 1, 5, 6} {
		if strings.Contains(lines[row], railThumb) {
			t.Errorf("row %d is outside the messages but carries a minimap bar", row)
		}
	}
}

// railColumns reports, for each rendered row, the column the rail glyph sits in.
func railColumns(t *testing.T, frame string) []int {
	t.Helper()
	var cols []int
	for _, line := range strings.Split(frame, "\n") {
		plain := []rune(ansi.Strip(line))
		col := -1
		for i, r := range plain {
			if string(r) == railGlyph || string(r) == railThumb {
				col = i // last rail-ish glyph on the row
			}
		}
		cols = append(cols, col)
	}
	return cols
}

// The rail must not jump: one column, the same column, on every row. Read-tool
// output is the case that broke it — it is syntax-highlighted, and it carries
// the "... (N more lines)" marker that used to be double-styled into garbage.
func TestRailDoesNotJump(t *testing.T) {
	m := historyModel(t, "first")
	m.width = 120
	m.height = 30
	m.applyResize()

	var code strings.Builder
	for i := range 40 {
		fmt.Fprintf(&code, "%d\tfunc handler%d(w http.ResponseWriter) error {\n", i+1, i)
	}
	for range 6 {
		m.chatModel.Messages = append(m.chatModel.Messages,
			message{role: "user", content: "read the server"},
			message{role: "tool", tool: "read", toolIn: `{"file_path":"server.go"}`, content: code.String()},
			message{role: "assistant", content: "Here is what it does."},
		)
	}

	for _, scroll := range []int{0, 7, 25, 60} {
		m.chatModel.Scroll = scroll
		frame := m.View().Content

		// Every row is exactly the terminal width.
		for i, line := range strings.Split(frame, "\n") {
			if got := ansi.StringWidth(line); got != m.width {
				t.Fatalf("scroll %d row %d width = %d, want %d", scroll, i, got, m.width)
			}
		}

		// And the rail is in the same column on every row.
		cols := railColumns(t, frame)
		want := -1
		for _, c := range cols {
			if c < 0 {
				continue
			}
			if want < 0 {
				want = c
			} else if c != want {
				t.Fatalf("scroll %d: rail jumps between columns %d and %d", scroll, want, c)
			}
		}
		if want < 0 {
			t.Fatalf("scroll %d: no rail rendered at all", scroll)
		}
	}
}

// Chroma must never be handed a string that already contains ANSI escapes: it
// re-tokenizes them and prints the escape as literal text.
func TestReadOutputDoesNotLeakEscapeCodes(t *testing.T) {
	var code strings.Builder
	for i := range 40 {
		fmt.Fprintf(&code, "%d\tline %d\n", i+1, i)
	}
	msg := message{role: "tool", tool: "read", toolIn: `{"file_path":"x.go"}`, content: code.String()}

	plain := ansi.Strip((&ToolDisplayModel{Width: 100}).RenderToolMessage(msg))

	if strings.Contains(plain, "38;5;") || strings.Contains(plain, "[0m") {
		t.Fatalf("escape codes leaked into the visible text:\n%s", plain)
	}
	if !strings.Contains(plain, "more lines") {
		t.Error("the truncation marker vanished")
	}
}

// isRule reports whether a row is one of the panel's horizontal rules.
func isRule(plain string) bool {
	trimmed := strings.TrimRight(plain, " "+railGlyph+railThumb)
	return trimmed != "" && strings.Trim(trimmed, "─") == ""
}

// isBlankRow reports whether a row carries nothing but the rail.
func isBlankRow(plain string) bool {
	return strings.TrimSpace(strings.Trim(plain, railGlyph+railThumb)) == ""
}

// The message block must sit inset by the same number of rows from the header
// above it and the rule below it. The gap used to exist only below — and only
// when the matrix bar was off — so the block hugged the top and floated off the
// bottom.
func TestMessageBlockGapsAreSymmetric(t *testing.T) {
	for _, matrix := range []bool{false, true} {
		m := historyModel(t, "first")
		m.width = 80
		m.height = 20
		m.applyResize()
		// applyResize feeds the matrix, which activates it — so the flag has to
		// be set afterwards to actually test the bar being off.
		m.matrix.active = matrix
		m.chatModel.Messages = append(m.chatModel.Messages,
			message{role: "user", content: "hi"},
			message{role: "assistant", content: "hello"})

		var rows []string
		for _, l := range strings.Split(m.View().Content, "\n") {
			rows = append(rows, ansi.Strip(l))
		}

		// The chat area runs from just below the header to the rule above the
		// status bar. With the matrix bar on, the header is rule/bar/rule; with
		// it off, the chat starts at the top of the panel.
		top := -1
		if matrix {
			for i, r := range rows {
				if isRule(r) {
					top = i // the second rule closes the matrix header
				}
				if i >= 2 {
					break
				}
			}
			if top != 2 {
				t.Fatalf("matrix header is not rule/bar/rule; second rule at row %d", top)
			}
		}
		bottom := -1
		for i := top + 1; i < len(rows); i++ {
			if isRule(rows[i]) {
				bottom = i
				break
			}
		}
		if bottom < 0 {
			t.Fatalf("matrix=%v: no rule below the chat", matrix)
		}

		// One blank row on each side of the messages, and the same on each side.
		if !isBlankRow(rows[top+1]) {
			t.Errorf("matrix=%v: no gap below the header: %q", matrix, rows[top+1])
		}
		if !isBlankRow(rows[bottom-1]) {
			t.Errorf("matrix=%v: no gap above the rule: %q", matrix, rows[bottom-1])
		}

		// A rule must never carry a scroll thumb — that would mean the minimap
		// spilled out of the message area.
		for i, r := range rows {
			if isRule(r) && strings.Contains(r, railThumb) {
				t.Errorf("matrix=%v: rule at row %d carries a scroll thumb", matrix, i)
			}
		}
	}
}

// The sidebar must not draw its own border any more, or the rail is doubled.
func TestSidebarDrawsNoBorder(t *testing.T) {
	out := RenderSidebar(SidebarRenderInput{Width: 30, Height: 20, Mode: "chat"})

	for i, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ansi.Strip(line)), "│") {
			t.Fatalf("sidebar line %d still starts with a border rule: %q",
				i, ansi.Strip(line))
		}
	}
}

func TestPadLinesToFixesPanelWidth(t *testing.T) {
	panel := "a\nbbbbbbbbbb\n\nccc"

	out := padLinesTo(panel, 12)

	for i, line := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(line); got != 12 {
			t.Errorf("line %d width = %d, want 12", i, got)
		}
	}
}

// The sidebar jitter bug: the left panel must be the same width no matter what
// content is scrolled into view, or JoinHorizontal moves the sidebar.
func TestPanelWidthIndependentOfContent(t *testing.T) {
	narrow := padLinesTo("hi", 40)
	wide := padLinesTo(strings.Repeat("y", 120), 40)

	if ansi.StringWidth(narrow) != ansi.StringWidth(wide) {
		t.Fatalf("panel width varies with content: %d vs %d",
			ansi.StringWidth(narrow), ansi.StringWidth(wide))
	}
}

// The reported bug, at the level it was seen: scrolling changed the sidebar's
// width. Every rendered frame must be the same width, whatever is on screen.
func TestViewWidthStableWhileScrolling(t *testing.T) {
	m := historyModel(t, "first")
	m.width = 120 // wide enough that the sidebar shows
	m.height = 30
	m.applyResize()

	// Mix short lines with lines far longer than the panel, so the widest
	// visible line changes as the viewport moves.
	for i := range 120 {
		content := "short"
		if i%5 == 0 {
			content = strings.Repeat("wide ", 40)
		}
		m.chatModel.Messages = append(m.chatModel.Messages,
			message{role: "assistant", content: content})
	}

	widthAt := func(scroll int) int {
		m.chatModel.Scroll = scroll
		widest := 0
		for _, line := range strings.Split(m.View().Content, "\n") {
			if w := ansi.StringWidth(line); w > widest {
				widest = w
			}
		}
		return widest
	}

	want := widthAt(0)
	for _, scroll := range []int{5, 20, 60, 100} {
		if got := widthAt(scroll); got != want {
			t.Fatalf("frame width = %d at scroll %d, want %d at every scroll position "+
				"(a varying width is what slides the sidebar around)", got, scroll, want)
		}
	}
	if want != m.width {
		t.Errorf("frame width = %d, want the full terminal width %d", want, m.width)
	}
}

// The kinds slice must stay index-aligned with the lines it describes, including
// across the blank-line collapsing that RenderMessages does last.
func TestRenderMessagesWithKindsStaysAligned(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 60
	c.ToolDisplay.Width = 60
	c.Messages = []message{
		{role: "user", content: "do the thing"},
		{role: "tool", tool: "bash", toolIn: `{"command":"ls"}`, content: "a\nb"},
		{role: "assistant", content: "Done."},
	}

	text, kinds := c.renderMessages(false)

	lines := strings.Split(text, "\n")
	if len(kinds) != len(lines) {
		t.Fatalf("kinds has %d entries for %d lines; the minimap would be offset",
			len(kinds), len(lines))
	}

	// The conversation's kinds must actually show up — not a strip of blanks.
	seen := map[blockKind]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	for _, want := range []blockKind{blockUser, blockExecute, blockAssistant} {
		if !seen[want] {
			t.Errorf("kind %d never appears in the classified lines", want)
		}
	}
}
