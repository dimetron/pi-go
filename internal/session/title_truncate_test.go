package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Titles are stored in meta.json and written to the terminal inside an OSC 0
// sequence. A byte-sliced cut splits multi-byte runes, and the fragment shows
// up as U+FFFD — half of recent session titles carried one.
func TestTruncateTitle_NeverSplitsARune(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"multi-byte at the boundary", strings.Repeat("a", MaxSessionTitle-1) + "é" + "tail"},
		{"three-byte rune spanning the cut", strings.Repeat("a", MaxSessionTitle-2) + "→more"},
		{"four-byte emoji spanning the cut", strings.Repeat("a", MaxSessionTitle-2) + "🎉more"},
		{"all multi-byte", strings.Repeat("→", MaxSessionTitle)},
		{"emoji only", strings.Repeat("🎉", MaxSessionTitle)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateTitle(tc.in)

			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Errorf("result contains U+FFFD: %q", got)
			}
			if len(got) > MaxSessionTitle {
				t.Errorf("result is %d bytes, want at most %d", len(got), MaxSessionTitle)
			}
		})
	}
}

// Short titles pass through untouched.
func TestTruncateTitle_ShortTitlesUnchanged(t *testing.T) {
	for _, in := range []string{"", "hello", "héllo → 🎉", strings.Repeat("a", MaxSessionTitle)} {
		if got := truncateTitle(in); got != in {
			t.Errorf("truncateTitle(%q) = %q, want it unchanged", in, got)
		}
	}
}

// The cut must keep as much as fits, not round down to nothing.
func TestTruncateTitle_KeepsWhatFits(t *testing.T) {
	in := strings.Repeat("→", MaxSessionTitle) // 3 bytes per rune
	got := truncateTitle(in)

	wantRunes := MaxSessionTitle / 3
	if n := utf8.RuneCountInString(got); n != wantRunes {
		t.Errorf("kept %d runes, want %d", n, wantRunes)
	}
}

// End to end through the sanitizer that callers actually use.
func TestSanitizeSessionTitle_ProducesNoReplacementChar(t *testing.T) {
	long := "Implement Slice 2 of the agent — inspect the server routing " +
		strings.Repeat("and the handler wiring ", 20) + "🎉"

	got := sanitizeSessionTitle(long)

	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("sanitized title contains U+FFFD: …%q", got[max(0, len(got)-40):])
	}
	if !utf8.ValidString(got) {
		t.Errorf("sanitized title is not valid UTF-8")
	}
	if len(got) > MaxSessionTitle {
		t.Errorf("sanitized title is %d bytes, want at most %d", len(got), MaxSessionTitle)
	}
}
