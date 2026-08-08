package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// This file guards the blank-line fast path in collapse.go and quantifies what
// it bought. blankFast replaced a per-line ansi.Strip call inside
// collapseBlankLines, which runs over the whole rendered history on every
// frame — and bubbletea calls View() once per message, so during streaming that
// is once per token. A live CPU profile attributed 24% of all CPU to the Strip
// call that blankFast removes.

// collapseBlankLinesStrip is the original implementation: collapseBlankLines
// with the ansi.Strip blank test it used before blankFast. Kept as the
// benchmark baseline and as an independent reference for the correctness gate.
func collapseBlankLinesStrip(s string, kinds []blockKind) (string, []blockKind) {
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
		if strings.TrimSpace(ansi.Strip(line)) == "" {
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
		b.Run(fmt.Sprintf("strip/msgs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(h)))
			for b.Loop() {
				collapseBlankLinesStrip(h, nil)
			}
		})
		b.Run(fmt.Sprintf("fast/msgs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(h)))
			for b.Loop() {
				collapseBlankLines(h, nil)
			}
		})
	}
}
