package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateOutput_NeverSplitsARune is the regression guard for the raw
// s[:maxOutputBytes] this replaced: a byte slice lands mid-rune whenever the
// cap falls inside a multi-byte character, and the broken byte travels all the
// way to the provider.
func TestTruncateOutput_NeverSplitsARune(t *testing.T) {
	// "→" is 3 bytes, so a cap at any offset has a 2-in-3 chance of splitting
	// one. Sweep the boundary rather than trusting a single alignment.
	for pad := range 8 {
		s := strings.Repeat("a", pad) + strings.Repeat("→", maxOutputBytes)
		got := truncateOutput(s)
		if !utf8.ValidString(got) {
			t.Fatalf("pad=%d produced invalid UTF-8", pad)
		}
		if len(got) <= maxOutputBytes-4 {
			t.Errorf("pad=%d gave back too much budget: %d bytes", pad, len(got))
		}
	}
}

func TestTruncateOutput_ShortInputUntouched(t *testing.T) {
	s := "hello → world"
	if got := truncateOutput(s); got != s {
		t.Errorf("truncateOutput rewrote a short string: %q", got)
	}
}

func TestTruncateLine_NeverSplitsARune(t *testing.T) {
	s := strings.Repeat("→", maxLineLength)
	got := truncateLine(s)
	if !utf8.ValidString(got) {
		t.Error("truncateLine produced invalid UTF-8")
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncateLine dropped its marker: %q", got[max(0, len(got)-10):])
	}
}

func TestUTF8SafeCut_Boundaries(t *testing.T) {
	const s = "a→b" // 1 + 3 + 1 bytes

	tests := []struct {
		n    int
		want string
	}{
		{0, ""},
		{1, "a"},
		{2, "a"}, // inside the arrow — must give the whole rune back
		{3, "a"},
		{4, "a→"},
		{5, "a→b"},
		{99, "a→b"},
	}
	for _, tt := range tests {
		if got := utf8SafeCut(s, tt.n); got != tt.want {
			t.Errorf("utf8SafeCut(%q, %d) = %q, want %q", s, tt.n, got, tt.want)
		}
	}
}

func TestUTF8SafeCutFrom_Boundaries(t *testing.T) {
	const s = "a→b"

	tests := []struct {
		n    int
		want string
	}{
		{0, "a→b"},
		{1, "→b"},
		{2, "b"}, // inside the arrow — must skip past it, not emit half
		{3, "b"},
		{4, "b"},
		{5, ""},
	}
	for _, tt := range tests {
		if got := utf8SafeCutFrom(s, tt.n); got != tt.want {
			t.Errorf("utf8SafeCutFrom(%q, %d) = %q, want %q", s, tt.n, got, tt.want)
		}
	}
}
