package tui

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// pi renders on the normal screen rather than the alternate one, so that
// scrollback and the terminal's own copy still work. The cost is that it
// inherits whatever state the previous command left behind, and the first frame
// is drawn straight on top of it. Empirically a partial reset is not enough:
// in JCEF-based embedded terminals (IntelliJ, some IDE plugins) a previous
// program that left the font-renderer or any DEC private mode in a non-default
// state corrupts the first frame's Unicode cells, and only a full RIS brings
// the renderer back to a known-good configuration.
//
// And separately, unread bytes in stdin — a terminal's answer to a query the
// previous program never collected — are parsed by Bubble Tea as keystrokes,
// which is how stray characters end up in the input box before the user has
// typed anything.
//
// prepareTerminal handles both. It is called before tea.NewProgram, which is
// the only safe moment to write escapes directly: once the renderer is running,
// a direct write to stdout races the frame writes and lands mid-frame. (Note
// the matching comment in View, which routes the window title through the View
// for exactly this reason.)
//
// RIS does clear the screen and scrollback. We accept that — the alternative is
// a first frame that is unreadable in a non-trivial fraction of the terminals
// pi-go is actually run in, and the trade is not worth the visible breakage.
func prepareTerminal() {
	// Discard unread input first, so a stale query response cannot be read as a
	// keystroke by the first frames.
	drainTerminalResponses()

	// Only speak escapes to a real terminal. Under a pipe or a test harness
	// these bytes would be captured as output.
	if !term.IsTerminal(os.Stdout.Fd()) {
		return
	}

	_, _ = os.Stdout.WriteString(terminalResetSequence)
}

// terminalResetSequence is what prepareTerminal writes to a real terminal:
//
//	ESC c   RIS — full terminal reset. Resets G0–G3 charsets, SGR, every DEC
//	       private mode, the text cursor enable bits, DEC saved cursor, and the
//	       font-renderer state. It also clears the screen and scrollback; that
//	       is the cost of a first frame that is correct in every terminal.
//
// Declared as a constant so the tests assert against the sequence actually
// emitted rather than their own copy of it — a duplicated literal would keep
// passing after the real one changed.
const terminalResetSequence = "\x1bc"
