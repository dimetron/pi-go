package webserver

import (
	"strings"
	"testing"
)

// This file pins the branch structure of the functions the complexity refactor
// reshaped:
//
//   - (*screenBuf).csi, whose one switch over the CSI final byte became the
//     csiOps table. A dispatch table is easy to get subtly wrong on parameter
//     defaults and on which finals it does NOT handle, so every final the old
//     switch named is exercised here, together with finals it never named.
//
// The voice halves of that refactor — the tool bodies lifted out of
// (*ServerV2).executeVoiceTool and the relay guard lifted out of
// handleGeminiVoiceWS — are pinned in complexity_voice_test.go, which is
// excluded from Windows along with the voice feature itself.

// ---------------------------------------------------------------------------
// CSI dispatch
// ---------------------------------------------------------------------------

// csiState drives one CSI sequence against a screen whose cursor has been
// parked at (row, col) on a known body of text, and reports where the cursor
// ended up and what the screen holds. Driving csi through feed is deliberate:
// it is the only way the dispatch is reached in production.
func csiState(t *testing.T, seq string) (*screenBuf, string) {
	t.Helper()
	s := newScreenBuf(100)
	feedString(s, seq)
	return s, s.snapshot(0)
}

// TestCSICursorMoves pins every cursor-moving final the old switch named,
// including the aliases ('e' for 'B', 'a' for 'C', '`' for 'G', 'f' for 'H')
// that a hand-written table is most likely to drop.
func TestCSICursorMoves(t *testing.T) {
	tests := []struct {
		name    string
		feed    string
		wantRow int
		wantCol int
	}{
		// CUU: up, clamped at the top of the buffer.
		{"CUU moves up by the parameter", "a\r\nb\r\nc\r\n\x1b[2A", 1, 0},
		{"CUU defaults to one", "a\r\nb\r\n\x1b[A", 1, 0},
		{"CUU clamps at row zero", "a\r\nb\r\n\x1b[99A", 0, 0},
		// An explicit zero is taken literally: csiNum only falls back to the
		// default for an absent, empty or unparseable parameter, so "\x1b[0A"
		// moves nothing. A real terminal would treat 0 as 1; this does not, and
		// that difference predates the dispatch table.
		{"CUU with an explicit zero moves nothing", "a\r\nb\r\nc\r\n\x1b[0A", 3, 0},
		{"CUD with an explicit zero moves nothing", "\x1b[0B", 0, 0},
		{"CUF with an explicit zero moves nothing", "abc\x1b[0C", 0, 3},

		// CUD and its alias 'e'.
		{"CUD moves down by the parameter", "\x1b[3B", 3, 0},
		{"CUD defaults to one", "\x1b[B", 1, 0},
		{"CUD alias e behaves identically", "\x1b[3e", 3, 0},
		{"CUD alias e defaults to one", "\x1b[e", 1, 0},

		// CUF and its alias 'a', clamped at maxScreenCols.
		{"CUF moves right by the parameter", "\x1b[5C", 0, 5},
		{"CUF defaults to one", "\x1b[C", 0, 1},
		{"CUF alias a behaves identically", "\x1b[5a", 0, 5},
		{"CUF clamps at maxScreenCols", "\x1b[999999C", 0, maxScreenCols},

		// CUB, clamped at column zero.
		{"CUB moves left by the parameter", "abcdef\x1b[2D", 0, 4},
		{"CUB defaults to one", "abcdef\x1b[D", 0, 5},
		{"CUB clamps at column zero", "abc\x1b[99D", 0, 0},

		// CHA and its alias '`': one-based absolute column.
		{"CHA is one-based", "abcdef\x1b[3G", 0, 2},
		{"CHA defaults to column one, meaning zero", "abcdef\x1b[G", 0, 0},
		{"CHA with parameter zero clamps to zero", "abcdef\x1b[0G", 0, 0},
		{"CHA alias backtick behaves identically", "abcdef\x1b[3`", 0, 2},
		{"CHA clamps at maxScreenCols", "\x1b[999999G", 0, maxScreenCols},

		// CNL: down and to column zero.
		{"CNL moves down and to column zero", "abc\x1b[2E", 2, 0},
		{"CNL defaults to one", "abc\x1b[E", 1, 0},

		// CPL: up and to column zero.
		{"CPL moves up and to column zero", "a\r\nb\r\nabc\x1b[2F", 0, 0},
		{"CPL defaults to one", "a\r\nb\r\nabc\x1b[F", 1, 0},
		{"CPL clamps at row zero", "a\r\nabc\x1b[99F", 0, 0},

		// CUP: the ROW half is deliberately ignored — an inline renderer has no
		// window to be absolute against — and the column half is honored.
		{"CUP honors the column and ignores the row", "a\r\nb\r\nc\x1b[1;4H", 2, 3},
		{"CUP alias f behaves identically", "a\r\nb\r\nc\x1b[1;4f", 2, 3},
		{"CUP with no parameters lands at column zero", "a\r\nb\r\nc\x1b[H", 2, 0},
		{"CUP with only a row lands at column zero", "a\r\nb\r\nc\x1b[9H", 2, 0},
		{"CUP with an empty second parameter takes the default", "a\r\nb\r\nc\x1b[9;H", 2, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := csiState(t, tt.feed)
			if s.row != tt.wantRow || s.col != tt.wantCol {
				t.Errorf("after %q cursor is (row %d, col %d), want (row %d, col %d)",
					tt.feed, s.row, s.col, tt.wantRow, tt.wantCol)
			}
		})
	}
}

// TestCSIEditsTheScreen pins the finals that change what a line holds. These
// used to be inline switch arms; each is now a table entry calling into
// eraseLine, eraseDisplay, deleteChars or blank.
func TestCSIEditsTheScreen(t *testing.T) {
	tests := []struct {
		name string
		feed string
		want string
	}{
		// EL — note the default is mode 0, not mode 1 as with the cursor moves.
		{"EL default erases from the cursor to the end", "abcdef\x1b[3G\x1b[K", "ab"},
		{"EL mode 0 erases from the cursor to the end", "abcdef\x1b[3G\x1b[0K", "ab"},
		{"EL mode 1 erases up to and including the cursor", "abcdef\x1b[3G\x1b[1K", "   def"},
		{"EL mode 2 erases the whole line", "abcdef\x1b[3G\x1b[2K", ""},
		{"EL past the end of a short line is a no-op", "ab\x1b[9G\x1b[K", "ab"},

		// ED — default mode 0 as well.
		{"ED mode 2 clears everything", "a\r\nb\r\nc\x1b[2J", ""},
		{"ED mode 3 clears everything too", "a\r\nb\r\nc\x1b[3J", ""},
		// ED mode 1 clears the lines above the cursor AND blanks the cursor line
		// up to and including the cursor, so only the tail of it survives.
		{"ED mode 1 clears above the cursor and the line's head", "a\r\nb\r\nxyz\x1b[2G\x1b[1J", "  z"},
		{"ED mode 1 at the line start still blanks one cell", "a\r\nb\r\nxyz\x1b[G\x1b[1J", " yz"},
		{"ED default clears from the cursor down", "aa\r\nbb\r\ncc\x1b[2A\x1b[G\x1b[J", ""},
		{"ED mode 0 clears from the cursor down", "aa\r\nbb\r\ncc\x1b[2A\x1b[2G\x1b[0J", "a"},

		// DCH — shift the rest of the line left.
		{"DCH deletes one character by default", "abcdef\x1b[3G\x1b[P", "abdef"},
		{"DCH deletes the parameter count", "abcdef\x1b[3G\x1b[3P", "abf"},
		{"DCH past the end truncates the line", "abcdef\x1b[3G\x1b[99P", "ab"},

		// ECH — blank in place, so the line keeps its length.
		{"ECH blanks one character by default", "abcdef\x1b[3G\x1b[X", "ab def"},
		{"ECH blanks the parameter count", "abcdef\x1b[3G\x1b[3X", "ab   f"},
		{"ECH past the end blanks to the end", "abcdef\x1b[3G\x1b[99X", "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := csiState(t, tt.feed)
			if got != tt.want {
				t.Errorf("after %q screen = %q, want %q", tt.feed, got, tt.want)
			}
		})
	}
}

// TestCSIIgnoresUnhandledFinals is the other half of the dispatch contract: a
// final byte with no table entry must be consumed and dropped, leaving both the
// cursor and the screen exactly as they were, and never printing its own bytes.
//
// The old switch got this from having no default arm. A table gets it from the
// lookup miss, which is the branch most likely to regress.
func TestCSIIgnoresUnhandledFinals(t *testing.T) {
	// Every unhandled final the terminal actually sees, plus neighbors of the
	// handled ones ('I' next to 'H'/'J', 'b' between 'a' and 'e', 'd', 'Z').
	unhandled := []string{
		"\x1b[m",         // SGR reset
		"\x1b[0m",        // SGR reset, explicit
		"\x1b[1;31;47m",  // SGR with several parameters
		"\x1b[?25l",      // cursor hide
		"\x1b[?25h",      // cursor show
		"\x1b[?2004h",    // bracketed paste on
		"\x1b[?2004l",    // bracketed paste off
		"\x1b[?1049h",    // alternate screen
		"\x1b[2 q",       // DECSCUSR, with an intermediate byte
		"\x1b[6n",        // DSR, a query
		"\x1b[3I",        // CHT
		"\x1b[3Z",        // CBT
		"\x1b[2L",        // IL
		"\x1b[2M",        // DL
		"\x1b[2S",        // SU
		"\x1b[2T",        // SD
		"\x1b[2b",        // REP
		"\x1b[2d",        // VPA
		"\x1b[2@",        // ICH
		"\x1b[1;3r",      // DECSTBM
		"\x1b[c",         // DA
		"\x1b[!p",        // DECSTR, with an intermediate byte
		"\x1b[999999999", // no final byte at all, on this chunk
	}

	for _, seq := range unhandled {
		t.Run(strings.ReplaceAll(seq, "\x1b", "ESC"), func(t *testing.T) {
			s := newScreenBuf(100)
			feedString(s, "abcdef")
			// Park the cursor somewhere non-trivial so a stray move shows.
			feedString(s, "\x1b[3G")
			row, col := s.row, s.col

			feedString(s, seq)

			if s.row != row || s.col != col {
				t.Errorf("%q moved the cursor from (%d,%d) to (%d,%d)", seq, row, col, s.row, s.col)
			}
			if got := s.snapshot(0); got != "abcdef" {
				t.Errorf("%q changed the screen to %q, want %q", seq, got, "abcdef")
			}
		})
	}
}

// A sequence split across chunk boundaries must dispatch exactly once, on the
// chunk that completes it — the carry in `pending` is what the table lookup
// depends on seeing a whole sequence.
func TestCSIDispatchesOnceAcrossChunkBoundaries(t *testing.T) {
	whole := newScreenBuf(100)
	feedString(whole, "abcdef\x1b[3G\x1b[3P")

	// The same bytes, cut at every interior position.
	seq := "abcdef\x1b[3G\x1b[3P"
	for cut := 1; cut < len(seq); cut++ {
		split := newScreenBuf(100)
		feedString(split, seq[:cut], seq[cut:])
		if got, want := split.snapshot(0), whole.snapshot(0); got != want {
			t.Errorf("split at %d gave %q, want %q", cut, got, want)
		}
		if split.row != whole.row || split.col != whole.col {
			t.Errorf("split at %d left the cursor at (%d,%d), want (%d,%d)",
				cut, split.row, split.col, whole.row, whole.col)
		}
	}
}

// csiOps is the table csi dispatches through. Its key set IS the set of finals
// the old switch named, so a dropped alias is a dropped behavior.
func TestCSIOpsCoversExactlyTheHandledFinals(t *testing.T) {
	want := []byte{'A', 'B', 'e', 'C', 'a', 'D', 'G', '`', 'E', 'F', 'K', 'J', 'H', 'f', 'P', 'X'}
	if len(csiOps) != len(want) {
		t.Errorf("csiOps has %d entries, want %d", len(csiOps), len(want))
	}
	for _, f := range want {
		if _, ok := csiOps[f]; !ok {
			t.Errorf("csiOps is missing the %q final", f)
		}
	}
	// The aliases must be the same operation, not two that drifted apart.
	for _, pair := range [][2]byte{{'B', 'e'}, {'C', 'a'}, {'G', '`'}, {'H', 'f'}} {
		for _, params := range []string{"", "0", "1", "4", "2;5", "999999"} {
			a := newScreenBuf(100)
			b := newScreenBuf(100)
			feedString(a, "abcdef\r\nghijkl\x1b[4G")
			feedString(b, "abcdef\r\nghijkl\x1b[4G")

			csiOps[pair[0]](a, params)
			csiOps[pair[1]](b, params)

			if a.row != b.row || a.col != b.col || a.snapshot(0) != b.snapshot(0) {
				t.Errorf("%q and %q disagree on params %q: (%d,%d)%q vs (%d,%d)%q",
					pair[0], pair[1], params, a.row, a.col, a.snapshot(0), b.row, b.col, b.snapshot(0))
			}
		}
	}
}

// csi's return value is the other half of its contract: how many bytes it
// consumed, and whether the sequence was complete. A truncated sequence must
// report incomplete so feed carries it rather than printing it.
func TestCSIConsumesTheWholeSequence(t *testing.T) {
	tests := []struct {
		in       string
		wantN    int
		wantDone bool
	}{
		{"\x1b[3P", 4, true},
		{"\x1b[X", 3, true},
		{"\x1b[1;31;47m", 10, true},  // an unhandled final is still consumed
		{"\x1b[2 q", 5, true},        // parameter bytes, then an intermediate
		{"\x1b[?2004h", 8, true},     // '?' is a parameter byte
		{"\x1b[3PTRAILING", 4, true}, // only the sequence, not what follows
		{"\x1b[3", 0, false},         // no final byte yet
		{"\x1b[", 0, false},          // nothing at all yet
		{"\x1b[999999999", 0, false}, // still parameters
		{"\x1b[2 ", 0, false},        // an intermediate with no final
	}
	for _, tt := range tests {
		s := newScreenBuf(100)
		n, done := s.csi([]byte(tt.in))
		if n != tt.wantN || done != tt.wantDone {
			t.Errorf("csi(%q) = (%d, %t), want (%d, %t)", tt.in, n, done, tt.wantN, tt.wantDone)
		}
	}
}
