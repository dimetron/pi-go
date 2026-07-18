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

// terminalTitleCmdMax caps the command/prompt portion of the context-aware
// title. The CWD prefix already eats into the tab's visible width, so the
// command is stripped to a tighter length than terminalTitleMax. 60 runes
// fits a typical tab with the prefix + separator + CWD basename (~12 chars)
// while still being readable.
const terminalTitleCmdMax = 60

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
	clean := stripControlChars(title)
	if clean == "" {
		return terminalTitleApp
	}
	return terminalTitleApp + " " + clean
}

// formatTerminalTitleWithCWD builds a context-aware window title:
// "π - <cwd> | <command>". The CWD basename gives the user a fixed anchor
// across turns (so different sessions in different repos are visually
// distinguishable in the tab bar), and the slash command / prompt is shown
// after a separator. The command is stripped to terminalTitleCmdMax runes so
// the assembled title still fits a typical tab width.
//
// Pass an empty cwd to fall back to the no-context shape ("π - <command>"
// or just "π -") — this matches what callers and tests have always produced
// for an unconfigured WorkDir. The command portion is run through the same
// control-character scrub as formatTerminalTitle so the OSC 0 envelope stays
// well-formed.
func formatTerminalTitleWithCWD(title, cwd string) string {
	// Scrub the CWD basename the same way the prompt is scrubbed. On Unix a
	// directory name may legally contain ESC, BEL, or other C0 controls, and
	// the OSC 0 envelope treats those as terminators — an unsanitized folder
	// would let the WorkDir smuggle arbitrary escape sequences into the
	// terminal title. After scrubbing, an all-control folder resolves to
	// "" and falls through to the no-context path below.
	folder := stripControlChars(sidebarFolderName(strings.TrimSpace(cwd)))
	clean := stripControlChars(title)
	if folder == "" {
		// No CWD context — fall back to the bare prefix shape so callers that
		// never set WorkDir (notably the unit tests) keep the old behavior.
		if clean == "" {
			return terminalTitleApp
		}
		return terminalTitleApp + " " + clean
	}
	if clean == "" {
		return terminalTitleApp + " " + folder
	}
	// Strip the command portion to terminalTitleCmdMax runes (counted in
	// runes, not bytes, to match terminalTitleMax's rune-safe behavior).
	if r := []rune(clean); len(r) > terminalTitleCmdMax {
		clean = string(r[:terminalTitleCmdMax-1]) + "…"
	}
	return terminalTitleApp + " " + folder + " | " + clean
}

// stripControlChars is the shared prefix-cleaner: any C0 control (ESC, BEL,
// CR, LF, TAB, …) is replaced with a space and the result is trimmed. This
// is what keeps an untrusted user prompt from breaking the OSC 0 envelope.
func stripControlChars(title string) string {
	title = strings.TrimSpace(title)
	var b strings.Builder
	for _, r := range title {
		if r < 0x20 || r == 0x7F {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
