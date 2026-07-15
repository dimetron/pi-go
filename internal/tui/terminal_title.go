package tui

import (
	"strings"
)

// terminalTitleApp is the constant prefix used when constructing the terminal
// window/tab title for the TUI. The π matches the mascot on the sidebar (see
// internal/tui/face.go); keeping the prefix short leaves room for the
// prompt-derived title in a typical tab.
const terminalTitleApp = "π -"

// terminalTitleMax caps the title so it fits on a single terminal line. The
// terminal itself will truncate anything longer, but doing it here keeps the
// title predictable.
const terminalTitleMax = 200

// deriveSessionTitle produces a short, single-line session title from a user
// prompt. It mirrors the truncation the agent card already uses
// (truncatePrompt in agent_loop.go) but is independent so the title length can
// be tuned separately. The title is intended to be:
//   - short enough to fit in a terminal tab/window title
//   - stable across a turn (derived once, not re-derived on every redraw)
//   - safe to embed in a terminal escape sequence
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

// formatTerminalTitle builds the window title "π - <title>" (or just "π -"
// when title is empty). Any control characters in title have already been
// stripped by session.sanitizeSessionTitle — this is a defensive net for
// callers that don't go through the session service.
//
// The result is handed to Bubble Tea as View.WindowTitle; the renderer wraps it
// in the escape sequence and writes it in order with the frame. Scrubbing
// controls here is what keeps that envelope well-formed.
func formatTerminalTitle(title string) string {
	title = strings.TrimSpace(title)
	// Strip anything that could break the escape-sequence envelope: ESC, BEL,
	// CR, LF, TAB, and other C0 controls. Keep printable runes only.
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
	return terminalTitleApp + " " + clean
}
