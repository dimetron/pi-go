package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// collapseBlankLines runs over the entire rendered history on every frame, and
// bubbletea calls View() once per message with no frame coalescing — so during
// streaming this executes once per token. The blank test is therefore the
// hottest line in the render path: profiling a live session attributed 24% of
// all CPU to the ansi.Strip call this replaces.
//
// blankFast reports whether line contains no non-whitespace content once ANSI
// escape sequences are ignored — the allocation-free equivalent of
// strings.TrimSpace(ansi.Strip(line)) == "".
//
// It fast-paths CSI only. CSI (SGR colors, cursor moves) is the entire
// vocabulary lipgloss, glamour, and our own renderers emit, so the fast path
// covers 100% of real frames. Anything else — OSC, DCS, SOS, PM, APC, or a
// malformed escape — falls back to ansi.Strip for that one line.
//
// The fallback is not defensive padding; it is load-bearing. Two rounds of
// FuzzBlankFast killed hand-rolled attempts to reproduce ansi.Strip's behavior
// on exotic sequences: "\x1bX0" (SOS, which Strip swallows) and "\x1bX\xd0"
// (unterminated SOS with invalid UTF-8, which Strip emits as content). Matching
// a parser's edge-case quirks by hand is a losing game. Deferring to it for the
// ~0% of lines that need it costs nothing and is correct by construction.
func blankFast(line string) bool {
	for i := 0; i < len(line); {
		c := line[i]
		if c == 0x1b { // ESC
			next := skipFastCSI(line, i)
			if next < 0 {
				return slowBlank(line) // not the fast-path grammar: defer to the parser
			}
			i = next
			continue
		}
		if c < utf8.RuneSelf { // ASCII fast path — the overwhelmingly common case
			if !isASCIISpace(c) {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size <= 1 {
			// Invalid UTF-8. ansi.Strip has its own opinion about these bytes
			// (FuzzBlankFast: "\xa0" is blank to Strip but not to a naive
			// decoder), so defer rather than guess. Valid multi-byte runes —
			// the ▌ gutter, box drawing, emoji — take the fast path above.
			return slowBlank(line)
		}
		if !unicode.IsSpace(r) {
			return false
		}
		i += size
	}
	return true
}

// skipFastCSI reports the index just past the CSI sequence that starts at the
// ESC byte line[i], or -1 when line[i:] is not the exact grammar the fast path
// handles and the caller must defer to the parser.
//
// It fast-paths ONLY the exact CSI grammar lipgloss/glamour emit:
//
//	ESC [ <param bytes 0x30-0x3f> <final byte 0x40-0x7e>
//
// Intermediate bytes (0x20-0x2f) are deliberately excluded: they are legal CSI
// but order-sensitive, and ansi.Strip aborts on bad order (FuzzBlankFast:
// "\x1b[ 0A"). C0 controls abort too ("\x1b[\x1c"). Every deviation defers to
// the parser instead of guessing.
func skipFastCSI(line string, i int) int {
	if i+1 >= len(line) || line[i+1] != '[' {
		return -1
	}
	j := i + 2
	for j < len(line) && line[j] >= 0x30 && line[j] <= 0x3f {
		j++
	}
	if j >= len(line) || line[j] < 0x40 || line[j] > 0x7e {
		return -1
	}
	return j + 1 // consume final byte
}

// slowBlank is the reference implementation blankFast must agree with, and the
// fallback blankFast defers to for escape sequences outside the CSI fast path.
func slowBlank(line string) bool {
	return strings.TrimSpace(ansi.Strip(line)) == ""
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}
