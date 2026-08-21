package webserver

import (
	"strings"
	"testing"
)

func feedString(s *screenBuf, chunks ...string) {
	for _, c := range chunks {
		s.feed([]byte(c))
	}
}

func TestScreenPlainText(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "hello\r\nworld\r\n")
	if got := s.snapshot(0); got != "hello\nworld" {
		t.Errorf("snapshot() = %q", got)
	}
}

// The reason this file exists: an inline TUI redraws by moving the cursor up
// over its last frame. Keeping the bytes would show every draft of the same
// paragraph; replaying the movement shows the one screen a human sees.
func TestScreenCollapsesInlineRedraws(t *testing.T) {
	s := newScreenBuf(100)
	// Frame 1: two lines. Frame 2: up two, rewrite both.
	feedString(s,
		"\x1b[2Kthinking...\r\n\x1b[2Kplease wait\r\n",
		"\x1b[2A\x1b[2Kdone: 3 tests passed\r\n\x1b[2Kall good\r\n",
	)
	got := s.snapshot(0)
	if strings.Contains(got, "thinking") || strings.Contains(got, "please wait") {
		t.Errorf("the overwritten frame survived: %q", got)
	}
	if got != "done: 3 tests passed\nall good" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenCarriageReturnOverwrite(t *testing.T) {
	s := newScreenBuf(100)
	// A spinner redrawing one line in place.
	feedString(s, "loading 10%\rloading 60%\rloading 100%\r\n")
	if got := s.snapshot(0); got != "loading 100%" {
		t.Errorf("snapshot() = %q", got)
	}
}

// A carriage return moves the cursor; it does not clear. Text written after one
// overwrites a prefix and leaves the rest standing, which is what a terminal
// shows and what a partial repaint depends on.
func TestScreenCarriageReturnOverwritesPrefixOnly(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "abcdef\rXY\r\n")
	if got := s.snapshot(0); got != "XYcdef" {
		t.Errorf("snapshot() = %q, want the tail to survive", got)
	}
}

// The defect the live PTY check caught: Bubble Tea repaints differentially,
// seeking to the column where a line first changed and writing from there. A
// model without a column appends instead of overwriting, which produced lines
// like "skills ok tools okagent... git ok".
func TestScreenPartialRepaintLandsAtTheColumn(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "status: idle        \r\n")
	// Up one, absolute column 9, rewrite from there.
	feedString(s, "\x1b[1A\x1b[9Gbusy\x1b[K\r\n")
	if got := s.snapshot(0); got != "status: busy" {
		t.Errorf("snapshot() = %q, want the repaint to land at column 9", got)
	}
}

// Cursor-forward is the other way a repaint skips an unchanged prefix.
func TestScreenCursorForwardSkipsPrefix(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "one two three\r")
	feedString(s, "\x1b[8CTHREE\r\n")
	if got := s.snapshot(0); got != "one two THREE" {
		t.Errorf("snapshot() = %q", got)
	}
}

// Erase-to-end must cut at the cursor, not wipe the line: a repaint uses it to
// drop what the previous, longer frame left behind.
func TestScreenEraseToEndOfLine(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "a very long status line\r")
	feedString(s, "short\x1b[K\r\n")
	if got := s.snapshot(0); got != "short" {
		t.Errorf("snapshot() = %q", got)
	}
}

// Writing past the end of a line pads rather than losing the position.
func TestScreenWritePastEndPads(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "ab\x1b[10Gx\r\n")
	if got := s.snapshot(0); got != "ab       x" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenTabStops(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "ab\tc\r\n")
	if got := s.snapshot(0); got != "ab      c" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenStripsStyling(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "\x1b[1;32mgreen bold\x1b[0m plain\r\n")
	if got := s.snapshot(0); got != "green bold plain" {
		t.Errorf("snapshot() = %q", got)
	}
}

// A window title is not screen content, and its payload contains text that
// would otherwise be indistinguishable from output.
func TestScreenDropsOSC(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "\x1b]0;pi — some/project\x07real output\r\n")
	if got := s.snapshot(0); got != "real output" {
		t.Errorf("snapshot() = %q", got)
	}

	s2 := newScreenBuf(100)
	feedString(s2, "\x1b]2;title\x1b\\after\r\n")
	if got := s2.snapshot(0); got != "after" {
		t.Errorf("snapshot() with ST terminator = %q", got)
	}
}

func TestScreenEraseDisplay(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "old stuff\r\nmore old\r\n", "\x1b[2Jfresh\r\n")
	if got := s.snapshot(0); got != "fresh" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenBackspace(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "abcx\b\bcd\r\n")
	if got := s.snapshot(0); got != "abcd" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenUnicodeAcrossChunks(t *testing.T) {
	s := newScreenBuf(100)
	// A rune split across two PTY reads must not become a replacement glyph.
	full := []byte("box │ и привіт")
	s.feed(full[:8])
	s.feed(full[8:])
	s.feed([]byte("\r\n"))
	if got := s.snapshot(0); got != "box │ и привіт" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenTrimsScrollback(t *testing.T) {
	s := newScreenBuf(10)
	for i := 0; i < 100; i++ {
		feedString(s, "line\r\n")
	}
	if got := len(s.lines); got > 10 {
		t.Errorf("retained %d lines, want at most 10", got)
	}
	// The cursor must survive the trim: writing after it must not panic or
	// land in a line the trim already dropped.
	feedString(s, "tail\r\n")
	if !strings.HasSuffix(s.snapshot(0), "tail") {
		t.Errorf("snapshot() = %q, want it to end with the newest line", s.snapshot(0))
	}
}

func TestScreenSnapshotLimit(t *testing.T) {
	s := newScreenBuf(100)
	for i := 0; i < 20; i++ {
		feedString(s, "line\r\n")
	}
	got := s.snapshot(5)
	if n := strings.Count(got, "\n") + 1; n != 5 {
		t.Errorf("snapshot(5) returned %d lines: %q", n, got)
	}
}

// Vertical padding is what a TUI uses to place its input box. To a model asked
// what the agent did, it is noise that pushes real output out of the window.
func TestScreenCollapsesBlankRuns(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s, "a\r\n\r\n\r\n\r\nb\r\n\r\n\r\n")
	if got := s.snapshot(0); got != "a\n\nb" {
		t.Errorf("snapshot() = %q", got)
	}
}

func TestScreenEmpty(t *testing.T) {
	s := newScreenBuf(100)
	if got := s.snapshot(0); got != "" {
		t.Errorf("snapshot() = %q, want empty", got)
	}
}

// Feeding a byte at a time must reach the same screen as feeding it all at
// once: PTY read boundaries are arbitrary and land mid-sequence constantly.
func TestScreenChunkingIsIrrelevant(t *testing.T) {
	input := "\x1b[2Kfirst\r\n\x1b[1;31msecond\x1b[0m\r\n\x1b[1A\x1b[2Krewritten\r\n"

	whole := newScreenBuf(100)
	whole.feed([]byte(input))

	byByte := newScreenBuf(100)
	for i := 0; i < len(input); i++ {
		byByte.feed([]byte{input[i]})
	}

	if whole.snapshot(0) != byByte.snapshot(0) {
		t.Errorf("chunking changed the screen:\n whole:  %q\n byByte: %q", whole.snapshot(0), byByte.snapshot(0))
	}
}

// A parser that stalls on malformed input would freeze the capture for the rest
// of the session, so every path must consume at least one byte.
func TestScreenSurvivesGarbage(t *testing.T) {
	s := newScreenBuf(100)
	feedString(s,
		"\x1b", "\x1b[", "\x1b[999999999999m", "\x1b[;;;H", "\x1b(", "\x1b]0;unterminated",
		"\x00\x01\x02", "\xff\xfe", "ok\r\n",
	)
	if got := s.snapshot(0); !strings.Contains(got, "ok") {
		t.Errorf("snapshot() = %q, want the real text to survive", got)
	}
}

func TestCSINum(t *testing.T) {
	tests := []struct {
		params string
		idx    int
		def    int
		want   int
	}{
		{"", 0, 1, 1},
		{"5", 0, 1, 5},
		{"12;7", 0, 1, 12},
		{"12;7", 1, 1, 7}, // CUP's column half
		{"12", 1, 1, 1},   // an absent second parameter takes the default
		{"?25", 0, 0, 25},
		{"nope", 0, 3, 3},
		{"38:2:1", 0, 1, 38},     // SGR sub-parameters
		{"99999999999", 0, 1, 1}, // absurd counts fall back rather than overflow
	}
	for _, tt := range tests {
		if got := csiNum(tt.params, tt.idx, tt.def); got != tt.want {
			t.Errorf("csiNum(%q, %d, %d) = %d, want %d", tt.params, tt.idx, tt.def, got, tt.want)
		}
	}
}
