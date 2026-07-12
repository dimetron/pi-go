package tui

import (
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// selection is a mouse text selection over the rendered frame.
//
// pi has to do this itself. Reporting mouse events is what lets the wheel scroll
// the chat, but it also takes click-drag away from the terminal, so the terminal
// can no longer select text for us. Rather than make the user hold a bypass
// modifier, we capture the drag, highlight it, and copy on release.
type selection struct {
	// dragging is true between press and release.
	dragging bool
	// anchor is where the drag started; cursor is where it is now. Either may
	// be before the other — the drag can go up or left.
	anchorX, anchorY int
	cursorX, cursorY int
	// present is true once there is something to show. It outlives the drag so
	// the highlight stays up after release, showing what was copied.
	present bool
}

// ordered returns the selection's bounds with start before end, reading order.
func (s selection) ordered() (startX, startY, endX, endY int) {
	if s.anchorY < s.cursorY || (s.anchorY == s.cursorY && s.anchorX <= s.cursorX) {
		return s.anchorX, s.anchorY, s.cursorX, s.cursorY
	}
	return s.cursorX, s.cursorY, s.anchorX, s.anchorY
}

// rowRange returns the half-open column range [lo, hi) selected on row, or
// ok=false if the row is outside the selection. Rows in the middle of a
// multi-row selection are taken whole, as a terminal does.
//
// selWidth bounds the selectable area — the chat panel, not the frame. A
// full-row span must stop at the panel's edge, or dragging down a few lines
// would sweep the rail and the whole sidebar into the clipboard.
func (s selection) rowRange(row, selWidth int) (lo, hi int, ok bool) {
	if !s.present {
		return 0, 0, false
	}
	startX, startY, endX, endY := s.ordered()
	if row < startY || row > endY {
		return 0, 0, false
	}

	lo, hi = 0, selWidth
	if row == startY {
		lo = startX
	}
	if row == endY {
		hi = endX + 1 // the cell under the cursor is included
	}
	lo = max(lo, 0)
	hi = min(hi, selWidth)
	if hi <= lo {
		return 0, 0, false
	}
	return lo, hi, true
}

// empty reports whether the selection covers nothing.
func (s selection) empty() bool {
	return !s.present || (s.anchorX == s.cursorX && s.anchorY == s.cursorY)
}

// contains reports whether the cell at (x, y) is inside the selection, using the
// same selWidth bound as the highlight — so what the user sees highlighted is
// exactly what they can click to copy.
func (s selection) contains(x, y, selWidth int) bool {
	lo, hi, ok := s.rowRange(y, selWidth)
	return ok && x >= lo && x < hi
}

// selectionStyle is how a selected region is drawn: reverse video, the
// convention every terminal already uses, so it needs no explanation.
var selectionStyle = lipgloss.NewStyle().Reverse(true)

// highlight draws the selection over the rendered frame.
//
// The selected span is re-rendered as plain reversed text: the region's own
// colors are dropped on purpose. Layering reverse video over arbitrary
// foreground colors produces unreadable combinations, and a selection needs to
// be legible more than it needs to be colorful.
//
// selWidth bounds what may be selected (the chat panel); frameWidth is the width
// of the row being rewritten. They differ, and conflating them would either
// truncate the rail and sidebar off every highlighted row or let the selection
// swallow them.
func highlight(frame string, sel selection, selWidth, frameWidth int) string {
	if sel.empty() {
		return frame
	}

	lines := strings.Split(frame, "\n")
	for row, line := range lines {
		lo, hi, ok := sel.rowRange(row, selWidth)
		if !ok {
			continue
		}
		left := ansi.Cut(line, 0, lo)
		mid := ansi.Strip(ansi.Cut(line, lo, hi))
		right := ansi.Cut(line, hi, frameWidth) // keeps the rail and sidebar intact
		lines[row] = left + selectionStyle.Render(mid) + right
	}
	return strings.Join(lines, "\n")
}

// selectedText extracts the selection from the frame as plain text.
// Trailing padding is trimmed per line — the frame pads every row out to the
// full width, and nobody wants to paste a block of trailing spaces.
func selectedText(frame string, sel selection, selWidth int) string {
	if sel.empty() {
		return ""
	}

	var out []string
	for row, line := range strings.Split(frame, "\n") {
		lo, hi, ok := sel.rowRange(row, selWidth)
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(line, lo, hi)), " "))
	}
	return strings.Join(out, "\n")
}

// copySelection puts text on the system clipboard.
//
// Two paths, because neither is reliable alone: OSC 52 travels over SSH and tmux
// but plenty of terminals disable it for security, while the platform clipboard
// command always works locally but knows nothing about a remote session. Doing
// both costs nothing and covers both cases.
func copySelection(text string) tea.Cmd {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// Run the platform clipboard command asynchronously so a hung xclip/pbcopy
	// cannot block the Bubble Tea command goroutine.
	go writeSystemClipboard(text)
	return tea.SetClipboard(text)
}

// clipboardCommand returns the platform's clipboard writer, or nil if there is
// no obvious one — in which case OSC 52 is the only path and that is fine.
func clipboardCommand() *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("pbcopy")
	case "windows":
		return exec.Command("clip")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return exec.Command("wl-copy")
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return exec.Command("xclip", "-selection", "clipboard")
		}
	}
	return nil
}

// writeSystemClipboard is best-effort: a failure here still leaves OSC 52.
// A timeout is applied to cmd.Wait() so a hung clipboard command (e.g. xclip
// waiting for X11) cannot block indefinitely.
func writeSystemClipboard(text string) {
	cmd := clipboardCommand()
	if cmd == nil {
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	_, _ = stdin.Write([]byte(text))
	_ = stdin.Close()

	// Wait with a timeout so a hung clipboard command does not block forever.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}
