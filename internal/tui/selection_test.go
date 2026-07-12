package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// drag runs a full press → move → release over the model and returns it.
//
// The row arguments are *frame* rows; the mouse reports absolute terminal rows.
// Converting here is the point: the UI is on the normal screen, so the frame
// sits below whatever the terminal already had on it, and a raw mouse Y indexes
// the frame too low.
func drag(t *testing.T, m *model, x1, y1, x2, y2 int) *model {
	t.Helper()
	m.View() // populate lastFrame/frameRows; the selection is in screen coordinates
	top := m.frameTop()

	next, _ := m.Update(tea.MouseClickMsg(tea.Mouse{X: x1, Y: y1 + top, Button: tea.MouseLeft}))
	m = next.(*model)
	next, _ = m.Update(tea.MouseMotionMsg(tea.Mouse{X: x2, Y: y2 + top, Button: tea.MouseLeft}))
	m = next.(*model)
	next, _ = m.Update(tea.MouseReleaseMsg(tea.Mouse{X: x2, Y: y2 + top, Button: tea.MouseLeft}))
	return next.(*model)
}

func selectionModel(t *testing.T) *model {
	t.Helper()
	m := historyModel(t, "first")
	m.width, m.height = 100, 24
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "SELECT-ME-ONE"},
		message{role: "assistant", content: "SELECT-ME-TWO"})
	return m
}

// findRow returns the frame row containing needle.
func findRow(t *testing.T, m *model, needle string) (row int, col int) {
	t.Helper()
	for i, line := range strings.Split(m.lastFrame, "\n") {
		plain := ansi.Strip(line)
		if idx := strings.Index(plain, needle); idx >= 0 {
			return i, ansi.StringWidth(plain[:idx])
		}
	}
	t.Fatalf("%q not found in the frame", needle)
	return 0, 0
}

// The whole point: dragging selects, and releasing copies — no modifier key,
// because reporting the wheel took click-drag away from the terminal.
func TestDragSelectsAndCopiesOnRelease(t *testing.T) {
	m := selectionModel(t)
	m.View()

	row, col := findRow(t, m, "SELECT-ME-ONE")
	m = drag(t, m, col, row, col+len("SELECT-ME-ONE")-1, row)

	if !m.sel.present {
		t.Fatal("no selection after the drag")
	}
	if m.sel.dragging {
		t.Error("still dragging after release")
	}

	got := selectedText(m.lastFrame, m.sel, m.chatWidth())
	if got != "SELECT-ME-ONE" {
		t.Errorf("copied %q, want %q", got, "SELECT-ME-ONE")
	}
}

// A drag across rows takes the middle rows whole, as a terminal does.
func TestDragAcrossRowsSelectsWholeLines(t *testing.T) {
	m := selectionModel(t)
	m.View()

	r1, c1 := findRow(t, m, "SELECT-ME-ONE")
	r2, c2 := findRow(t, m, "SELECT-ME-TWO")
	if r2 <= r1 {
		t.Skip("messages not laid out as expected")
	}
	m = drag(t, m, c1, r1, c2+len("SELECT-ME-TWO")-1, r2)

	got := selectedText(m.lastFrame, m.sel, m.chatWidth())
	if !strings.Contains(got, "SELECT-ME-ONE") || !strings.Contains(got, "SELECT-ME-TWO") {
		t.Fatalf("multi-row selection = %q, want both messages", got)
	}
	if len(strings.Split(got, "\n")) != r2-r1+1 {
		t.Errorf("selection spans %d lines, want %d", len(strings.Split(got, "\n")), r2-r1+1)
	}
}

// Dragging backwards (up, or right-to-left) selects the same span.
func TestDragBackwardsSelectsSameSpan(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, col := findRow(t, m, "SELECT-ME-ONE")
	end := col + len("SELECT-ME-ONE") - 1

	forward := drag(t, selectionModel(t), col, row, end, row)
	backward := drag(t, m, end, row, col, row)

	f := selectedText(forward.lastFrame, forward.sel, forward.chatWidth())
	b := selectedText(backward.lastFrame, backward.sel, backward.chatWidth())
	if f != b {
		t.Errorf("backward drag copied %q, forward copied %q — they must match", b, f)
	}
}

// A plain click away from the highlight clears it. (A click *inside* the
// highlight copies instead — see TestClickInsideSelectionCopiesAgain.)
func TestClickOutsideClearsSelection(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, col := findRow(t, m, "SELECT-ME-ONE")
	m = drag(t, m, col, row, col+5, row)
	if !m.sel.present {
		t.Fatal("expected a selection to clear")
	}

	// Press and release in place, well clear of the highlighted span.
	m = drag(t, m, col+40, row, col+40, row)

	if m.sel.present {
		t.Error("a plain click away from the highlight left a selection behind")
	}
}

// The highlight must not change the frame's geometry — a selection that
// re-flowed the layout would be worse than no selection.
func TestHighlightPreservesFrameWidth(t *testing.T) {
	m := selectionModel(t)
	frame := m.View().Content
	row, col := findRow(t, m, "SELECT-ME-ONE")

	sel := selection{present: true, anchorX: col, anchorY: row, cursorX: col + 8, cursorY: row + 2}
	out := highlight(frame, sel, m.chatWidth(), m.width)

	for i, line := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(line); got != m.width {
			t.Fatalf("row %d is %d wide after highlighting, want %d", i, got, m.width)
		}
	}
	if !strings.Contains(out, "\x1b[7m") {
		t.Error("nothing was drawn in reverse video; the selection is invisible")
	}
}

// Trailing padding must not end up on the clipboard: every row is padded to the
// full width, and nobody wants to paste a block of spaces.
func TestSelectedTextTrimsPadding(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, _ := findRow(t, m, "SELECT-ME-ONE")

	// Select the entire row, padding included.
	sel := selection{present: true, anchorX: 0, anchorY: row, cursorX: m.chatWidth() - 1, cursorY: row}
	got := selectedText(m.lastFrame, sel, m.chatWidth())

	if got != strings.TrimRight(got, " ") {
		t.Errorf("copied text keeps trailing padding: %q", got)
	}
	if !strings.Contains(got, "SELECT-ME-ONE") {
		t.Errorf("copied %q, want it to contain the message", got)
	}
}

func TestCopySelectionIgnoresBlankText(t *testing.T) {
	if cmd := copySelection("   \n  "); cmd != nil {
		t.Error("blank selection still tried to write the clipboard")
	}
}

// Selection belongs to the chat panel alone. Dragging off its right edge — or
// straight down, which used to sweep whole frame rows — must never pull the
// rail or the sidebar into the clipboard.
func TestSelectionStaysInsideTheChatPanel(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, col := findRow(t, m, "SELECT-ME-ONE")

	// Drag past the right edge of the world and several rows down.
	m = drag(t, m, col, row, m.width+50, row+4)

	if got := m.sel.cursorX; got >= m.chatWidth() {
		t.Errorf("selection cursor at column %d, past the chat panel (%d)", got, m.chatWidth())
	}

	got := selectedText(m.lastFrame, m.sel, m.chatWidth())
	for _, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > m.chatWidth() {
			t.Fatalf("copied a %d-wide line, wider than the chat panel (%d): %q",
				w, m.chatWidth(), line)
		}
	}
	// The rail sits in the column just past the panel and the sidebar beyond it,
	// so the width bound above is what actually keeps them out. Matching on the
	// rail's glyph would not work: "●" is the thumb *and* the assistant bullet,
	// so it legitimately appears in copied chat text.
	if strings.Contains(got, "Context") || strings.Contains(got, "Model") {
		t.Errorf("sidebar content was copied along with the text: %q", got)
	}
}

// The reported bug: the selection landed several rows below the mouse.
//
// The UI is on the normal screen, so the frame is drawn under whatever the
// terminal already had on it (a shell prompt, a "pprof listening" line), and
// Bubble Tea reports the mouse in absolute terminal rows. Indexing the frame
// with a raw mouse Y therefore lands exactly that many rows too low.
func TestSelectionFollowsTheMouseWhenFrameIsOffset(t *testing.T) {
	m := selectionModel(t)
	m.View()

	top := m.frameTop()
	if top == 0 {
		t.Skip("frame is flush with the top of the screen; nothing to translate")
	}

	row, col := findRow(t, m, "SELECT-ME-ONE")

	// Press where the text actually is on screen: frame row + the frame's origin.
	next, _ := m.Update(tea.MouseClickMsg(tea.Mouse{X: col, Y: row + top, Button: tea.MouseLeft}))
	m = next.(*model)

	if m.sel.anchorY != row {
		t.Fatalf("clicking terminal row %d selected frame row %d, want %d — the selection is %d rows off",
			row+top, m.sel.anchorY, row, m.sel.anchorY-row)
	}

	// And the text under the mouse is what gets copied.
	next, _ = m.Update(tea.MouseMotionMsg(tea.Mouse{X: col + len("SELECT-ME-ONE") - 1, Y: row + top, Button: tea.MouseLeft}))
	m = next.(*model)
	next, _ = m.Update(tea.MouseReleaseMsg(tea.Mouse{X: col + len("SELECT-ME-ONE") - 1, Y: row + top, Button: tea.MouseLeft}))
	m = next.(*model)

	if got := selectedText(m.lastFrame, m.sel, m.chatWidth()); got != "SELECT-ME-ONE" {
		t.Errorf("copied %q, want the text under the mouse", got)
	}
}

// Only the message viewport is selectable. Everything else in the panel is
// chrome — the matrix rain, the rules, the status bar, the prompt — and nobody
// wants a screenful of braille rain on their clipboard.
func TestSelectionExcludesChromeRows(t *testing.T) {
	m := selectionModel(t)
	m.View()

	// Drag from far above the messages (through the matrix bar) to far below
	// them (through the status bar and prompt).
	m = drag(t, m, 0, -20, m.chatWidth()-1, m.frameRows+20)

	if m.sel.anchorY < m.msgTop || m.sel.anchorY >= m.msgBottom {
		t.Errorf("selection anchored on row %d, outside the message viewport [%d,%d)",
			m.sel.anchorY, m.msgTop, m.msgBottom)
	}
	if m.sel.cursorY < m.msgTop || m.sel.cursorY >= m.msgBottom {
		t.Errorf("selection ended on row %d, outside the message viewport [%d,%d)",
			m.sel.cursorY, m.msgTop, m.msgBottom)
	}

	// The matrix rain uses braille; the status bar carries the model name.
	got := selectedText(m.lastFrame, m.sel, m.chatWidth())
	for _, r := range got {
		if r >= '⠀' && r <= '⣿' {
			t.Fatalf("matrix rain was copied: %q", got)
		}
	}
	if strings.Contains(got, "glm-5.2:cloud") || strings.Contains(got, "tkn:") {
		t.Errorf("the status bar was copied: %q", got)
	}
}

// --- copy feedback ---------------------------------------------------------

// Copying says so. Without feedback the user cannot tell a successful copy from
// a missed drag — the clipboard is invisible.
func TestCopyRaisesFlashAndItExpires(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, col := findRow(t, m, "SELECT-ME-ONE")

	m = drag(t, m, col, row, col+len("SELECT-ME-ONE")-1, row)

	if m.flash != "Copied!" {
		t.Fatalf("flash = %q after copying, want %q", m.flash, "Copied!")
	}
	if got := m.statusRenderInput().Flash; got != "Copied!" {
		t.Errorf("the status bar was not told about the flash: %q", got)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Copied!") {
		t.Error("the flash is not visible in the rendered frame")
	}

	// Its timer clears it.
	next, _ := m.Update(flashExpiredMsg{seq: m.flashSeq})
	if got := next.(*model).flash; got != "" {
		t.Errorf("flash = %q after its timer fired, want it cleared", got)
	}
}

// The flash lands in the existing bracketed slot — the one that normally shows
// [chat] — rather than adding a segment. It must not shift the bar's geometry:
// the slot is fixed width, so the fields after it stay put.
func TestFlashTakesOverTheModeSlot(t *testing.T) {
	s := &StatusModel{Width: 100}
	in := StatusRenderInput{Mode: "chat", ProviderName: "ollama", ModelName: "glm-5.2:cloud"}

	before := ansi.Strip(s.Render(in))
	in.Flash = "Copied!"
	during := ansi.Strip(s.Render(in))

	if !strings.Contains(during, "[Copied!") {
		t.Fatalf("flash is not in the bracketed slot: %q", during)
	}
	if strings.Contains(during, "[chat") {
		t.Errorf("the mode is still shown alongside the flash: %q", during)
	}
	if ansi.StringWidth(before) != ansi.StringWidth(during) {
		t.Errorf("the bar changed width with the flash: %d -> %d",
			ansi.StringWidth(before), ansi.StringWidth(during))
	}
	// The rest of the bar is untouched — only the slot is borrowed.
	// The model name now lives in the sidebar, not in the status bar or any
	// info line above it, so the flash in the status bar cannot affect it.

	// And the mode comes straight back.
	in.Flash = ""
	if after := ansi.Strip(s.Render(in)); !strings.Contains(after, "[chat") {
		t.Errorf("the mode did not return after the flash: %q", after)
	}
}

// A stale timer must not wipe a flash raised after it — otherwise a second copy
// inside the three-second window gets cut short by the first one's expiry.
func TestStaleFlashTimerDoesNotClearNewerFlash(t *testing.T) {
	m := selectionModel(t)
	m.View()

	m.setFlash("First") // seq 1
	firstSeq := m.flashSeq
	m.setFlash("Copied!") // seq 2 supersedes it

	// The first flash's timer, arriving late.
	next, _ := m.Update(flashExpiredMsg{seq: firstSeq})
	m = next.(*model)

	if m.flash != "Copied!" {
		t.Errorf("flash = %q; a stale timer cleared the newer message", m.flash)
	}
}

// Clicking inside a highlight copies it again rather than discarding it: the
// highlight is the affordance, so what is lit up is what a click takes.
func TestClickInsideSelectionCopiesAgain(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, col := findRow(t, m, "SELECT-ME-ONE")
	m = drag(t, m, col, row, col+len("SELECT-ME-ONE")-1, row)

	// Clear the flash so the re-copy is what raises it.
	next, _ := m.Update(flashExpiredMsg{seq: m.flashSeq})
	m = next.(*model)
	if m.flash != "" {
		t.Fatal("failed to clear the flash before the click")
	}

	// Click in the middle of the highlight.
	next, _ = m.Update(tea.MouseClickMsg(tea.Mouse{
		X: col + 3, Y: row + m.frameTop(), Button: tea.MouseLeft,
	}))
	m = next.(*model)

	if !m.sel.present {
		t.Error("clicking the highlight threw the selection away")
	}
	if m.sel.dragging {
		t.Error("clicking the highlight started a new drag")
	}
	if m.flash != "Copied!" {
		t.Errorf("flash = %q after clicking the highlight, want a re-copy", m.flash)
	}
}

// A click *outside* the highlight still starts a fresh selection.
func TestClickOutsideSelectionStartsNewDrag(t *testing.T) {
	m := selectionModel(t)
	m.View()
	row, col := findRow(t, m, "SELECT-ME-ONE")
	m = drag(t, m, col, row, col+4, row)

	next, _ := m.Update(tea.MouseClickMsg(tea.Mouse{
		X: col + 40, Y: row + m.frameTop(), Button: tea.MouseLeft,
	}))
	m = next.(*model)

	if !m.sel.dragging {
		t.Error("a click away from the highlight did not start a new selection")
	}
}

// A press in the sidebar starts no selection at all.
func TestPressInSidebarDoesNotSelect(t *testing.T) {
	m := selectionModel(t)
	m.View()

	m = drag(t, m, m.chatWidth()+5, 4, m.chatWidth()+20, 8)

	if m.sel.present || m.sel.dragging {
		t.Error("dragging inside the sidebar started a selection")
	}
	if got := selectedText(m.lastFrame, m.sel, m.chatWidth()); got != "" {
		t.Errorf("copied %q from a sidebar drag, want nothing", got)
	}
}
