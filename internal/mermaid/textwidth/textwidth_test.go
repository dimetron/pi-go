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

// TestTextPresentationEmojiAreNarrow pins the fix for a label sitting a column
// left of the box around it.
//
// These codepoints are symbols first and emoji only when a variation selector
// follows: Unicode gives them Emoji_Presentation=No and a terminal draws them
// one column wide. Treating their whole block as wide made a label measure a
// column wider than it drew, so centering pushed it left while its neighbors
// in the same box sat straight.
func TestTextPresentationEmojiAreNarrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"hammer and wrench is narrow", "🛠", 1},
		{"building construction is narrow", "🏗", 1},
		{"eyes is wide", "👀", 2},
		{"high voltage is wide", "⚡", 2},
		{"bust in silhouette is wide", "👤", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.in); got != tc.want {
				t.Errorf("String(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestVariationSelectorRequestsEmojiPresentation covers the one thing a
// per-rune width cannot answer: U+FE0F carries no width itself, but it turns
// the symbol before it into a two-column emoji.
func TestVariationSelectorRequestsEmojiPresentation(t *testing.T) {
	if got := String("🛠"); got != 1 {
		t.Errorf("bare symbol = %d, want 1", got)
	}
	if got := String("🛠️"); got != 2 {
		t.Errorf("symbol with U+FE0F = %d, want 2", got)
	}
}

// TestSplitWidthsSumToString asserts the walk the canvas draws with and the
// measurement the layout sizes with cannot disagree — the two drifting apart
// is what puts a label off-center.
func TestSplitWidthsSumToString(t *testing.T) {
	for _, s := range []string{"plain", "🛠 task", "🛠️ task", "👀 eyes", "日本語", "é"} {
		sum := 0
		for _, c := range Split(s) {
			sum += c.Width
		}
		if sum != String(s) {
			t.Errorf("Split(%q) sums to %d but String reports %d", s, sum, String(s))
		}
	}
}
