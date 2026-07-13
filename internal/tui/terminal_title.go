package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// terminalTitleApp is the constant prefix used when constructing the terminal
// window/tab title for the TUI. pi-mono uses the same prefix so users get a
// consistent label across both implementations.
const terminalTitleApp = "pi-go"

// terminalTitleMax caps the OSC 0 payload so it fits on a single terminal
// line. The terminal itself will truncate anything longer, but doing it here
// keeps the title predictable.
const terminalTitleMax = 200

// deriveSessionTitle produces a short, single-line session title from a user
// prompt. It mirrors the truncation the agent card already uses
// (truncatePrompt in agent_loop.go) but is independent so the title length can
// be tuned separately. The title is intended to be:
//   - short enough to fit in a terminal tab/window title
//   - stable across a turn (derived once, not re-derived on every redraw)
//   - safe to embed inside an OSC 0 sequence
func deriveSessionTitle(prompt string) string {
	title := strings.TrimSpace(prompt)
	if title == "" {
		return ""
	}
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = title[:i]
	}
	if len(title) > terminalTitleMax {
		title = title[:terminalTitleMax-1] + "…"
	}
	return title
}

// formatTerminalTitle builds the OSC 0 payload "pi-go: <title>" (or just
// "pi-go" when title is empty). Any control characters in title have already
// been stripped by session.sanitizeSessionTitle — this is a defensive net for
// callers that don't go through the session service.
func formatTerminalTitle(title string) string {
	title = strings.TrimSpace(title)
	// Strip anything that could break the OSC 0 envelope: ESC, BEL, CR, LF,
	// TAB, and other C0 controls. Keep printable runes only.
	var b strings.Builder
	for _, r := range title {
		if r < 0x20 || r == 0x7F {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" {
		return terminalTitleApp
	}
	return terminalTitleApp + ": " + clean
}

// setTerminalTitle writes the OSC 0 sequence for the given title to w. The
// sequence is "ESC ] 0 ; <title> BEL" — the canonical "set window title" form
// supported by every major terminal. Pass io.Discard to skip the write
// entirely (used in tests).
func setTerminalTitle(w io.Writer, title string) {
	if w == nil {
		return
	}
	// OSC 0;title BEL — ESC ] 0 ; <title> \x07
	_, _ = fmt.Fprintf(w, "\x1b]0;%s\x07", formatTerminalTitle(title))
}

// resetTerminalTitle clears the terminal window/tab title. Some shells (tmux,
// screen) cache the title across invocations; explicitly resetting on quit
// prevents pi from leaving "pi-go: my task" stuck on the tab after exit.
func resetTerminalTitle(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprint(w, "\x1b]0;\x07")
}

// defaultTitleWriter is the destination for OSC 0 writes in production. It
// points at os.Stdout so the terminal title updates immediately. Bubble Tea
// also writes to os.Stdout, so the OSC 0 bytes can interleave with rendered
// frames in theory — but OSC 0 is non-displaying and the terminal processes it
// atomically, so the interleaving is harmless in practice. Tests override this
// via a buffer.
var defaultTitleWriter io.Writer = os.Stdout
