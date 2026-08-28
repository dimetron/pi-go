package textwidth

import "testing"

func TestString(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"ascii", "You", 3},
		{"empty", "", 0},
		{"emoji is two columns", "👤", 2},
		{"emoji with text", "👤 You", 6},
		{"several emoji", "🔑 Security · 🐛 Debugging", 26},
		{"cjk is two columns", "日本語", 6},
		{"combining mark adds nothing", "é", 1},
		{"variation selector adds nothing", "⚡️", 2},
		{"zero width joiner", "a\u200db", 2},
		{"box drawing is one column", "┌──┐", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.in); got != tc.want {
				t.Errorf("String(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestStringMatchesByteLengthForASCII pins the case the old code got right, so
// a future change to the width table cannot silently shift plain labels.
func TestStringMatchesByteLengthForASCII(t *testing.T) {
	for _, s := range []string{"", "a", "hello world", "cmd/pi/main.go", "[rect]"} {
		if got, want := String(s), len(s); got != want {
			t.Errorf("String(%q) = %d, want %d for pure ASCII", s, got, want)
		}
	}
}

func TestMax(t *testing.T) {
	if got := Max([]string{"a", "👤👤", "abc"}); got != 4 {
		t.Errorf("Max = %d, want 4", got)
	}
	if got := Max(nil); got != 0 {
		t.Errorf("Max(nil) = %d, want 0", got)
	}
}
