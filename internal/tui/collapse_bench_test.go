package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// This file quantifies TODO.md ## PPROF item 25 and de-risks its proposed fix.
//
// collapseBlankLines calls ansi.Strip on every line of the entire rendered
// history on every frame, purely to answer "is this line blank?". ansi.Strip
// runs a full ANSI state-machine parse and allocates a new string. The question
// it is being asked can be answered by a single non-allocating pass.
//
// blankFast is the candidate: walk the bytes, skip escape sequences, and bail
// on the first non-whitespace rune. It answers the same question with no
// allocation and no parser.

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
			if i+1 >= len(line) || line[i+1] != '[' {
				return slowBlank(line) // not CSI: defer to the parser
			}
			// Fast-path ONLY the exact CSI grammar lipgloss/glamour emit:
			//   ESC [ <param bytes 0x30-0x3f> <final byte 0x40-0x7e>
			// Intermediate bytes (0x20-0x2f) are deliberately excluded: they are
			// legal CSI but order-sensitive, and ansi.Strip aborts on bad order
			// (FuzzBlankFast: "\x1b[ 0A"). C0 controls abort too ("\x1b[\x1c").
			// Every deviation defers to the parser instead of guessing.
			j := i + 2
			for j < len(line) && line[j] >= 0x30 && line[j] <= 0x3f {
				j++
			}
			if j >= len(line) || line[j] < 0x40 || line[j] > 0x7e {
				return slowBlank(line)
			}
			i = j + 1 // consume final byte
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

// slowBlank is the reference implementation blankFast must agree with.
func slowBlank(line string) bool {
	return strings.TrimSpace(ansi.Strip(line)) == ""
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

// collapseBlankLinesFast is collapseBlankLines with only the blank test swapped.
// Everything else is identical, so the benchmark isolates the ansi.Strip cost.
func collapseBlankLinesFast(s string, kinds []blockKind) (string, []blockKind) {
	if s == "" {
		return s, nil
	}
	lines := strings.Split(s, "\n")
	for len(kinds) < len(lines) {
		kinds = append(kinds, blockNone)
	}
	out := make([]string, 0, len(lines))
	outKinds := make([]blockKind, 0, len(lines))
	prevBlank := false
	for i, line := range lines {
		if blankFast(line) {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			outKinds = append(outKinds, blockNone)
			continue
		}
		prevBlank = false
		out = append(out, line)
		outKinds = append(outKinds, kinds[i])
	}
	return strings.Join(out, "\n"), outKinds
}

// history synthesizes a rendered chat buffer shaped like a real one: styled
// content lines, gutter-prefixed lines, and the runs of blank lines that
// collapseBlankLines exists to squash.
func history(msgs int) string {
	var b strings.Builder
	for i := range msgs {
		fmt.Fprintf(&b, "\x1b[38;5;245m▌\x1b[0m \x1b[1mmessage %d\x1b[0m\n", i)
		for j := range 8 {
			fmt.Fprintf(&b, "\x1b[38;5;252m  some rendered output line %d with \x1b[36mcolor\x1b[0m spans\x1b[0m\n", j)
		}
		b.WriteString("\x1b[38;5;245m\x1b[0m\n") // styled-but-blank: the case that defeats a naive == "" check
		b.WriteString("\n")
		b.WriteString("   \n")
		b.WriteString("\n")
	}
	return b.String()
}

// TestBlankFastMatchesStrip is the correctness gate: the fast path must agree
// with the ansi.Strip implementation on every line, or the optimization is a bug.
func TestBlankFastMatchesStrip(t *testing.T) {
	cases := []string{
		"", " ", "\t", "   \t  ",
		"x", " x ", "\x1b[0m", "\x1b[38;5;245m\x1b[0m",
		"\x1b[38;5;245m   \x1b[0m", "\x1b[1mbold\x1b[0m", "\x1b[38;5;245m▌\x1b[0m content",
		"\x1b]0;title\x07", "\x1b]0;title\x1b\\", "\x1b[", "\x1b", " ", "　",
		"\x1b[38;5;252m  \x1b[36m \x1b[0m\x1b[0m",
	}
	for _, in := range cases {
		want := strings.TrimSpace(ansi.Strip(in)) == ""
		if got := blankFast(in); got != want {
			t.Errorf("blankFast(%q) = %v, want %v (ansi.Strip -> %q)", in, got, want, ansi.Strip(in))
		}
	}
}

// FuzzBlankFast proves equivalence on arbitrary input, including malformed escapes.
func FuzzBlankFast(f *testing.F) {
	for _, s := range []string{"", " ", "x", "\x1b[0m", "\x1b[38;5;245m \x1b[0m", "\x1b]8;;u\x07"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if strings.Contains(s, "\n") {
			t.Skip() // collapseBlankLines only ever passes single lines
		}
		want := strings.TrimSpace(ansi.Strip(s)) == ""
		if got := blankFast(s); got != want {
			t.Errorf("blankFast(%q) = %v, want %v", s, got, want)
		}
	})
}

func BenchmarkCollapseBlankLines(b *testing.B) {
	for _, n := range []int{10, 100, 500} {
		h := history(n)
		b.Run(fmt.Sprintf("current/msgs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(h)))
			for b.Loop() {
				collapseBlankLines(h, nil)
			}
		})
		b.Run(fmt.Sprintf("fast/msgs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(h)))
			for b.Loop() {
				collapseBlankLinesFast(h, nil)
			}
		})
	}
}
