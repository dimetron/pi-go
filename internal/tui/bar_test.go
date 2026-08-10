package tui

import (
	"strings"
	"testing"
)

// The clamps are the whole reason this helper exists: four call sites each
// carried their own, and a bar that overflows its width pushes every column to
// its right out of place — which in the status bar means the rail moves.
func TestBarFill(t *testing.T) {
	tests := []struct {
		name     string
		fraction float64
		width    int
		want     int
	}{
		{name: "empty", fraction: 0, width: 20, want: 0},
		{name: "half", fraction: 0.5, width: 20, want: 10},
		{name: "full", fraction: 1, width: 20, want: 20},
		{name: "truncates rather than rounds", fraction: 0.99, width: 10, want: 9},
		{name: "over one is clamped to width", fraction: 2.5, width: 20, want: 20},
		{name: "negative is clamped to zero", fraction: -1, width: 20, want: 0},
		{name: "zero width has no cells", fraction: 0.5, width: 0, want: 0},
		{name: "negative width has no cells", fraction: 0.5, width: -3, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := barFill(tt.fraction, tt.width); got != tt.want {
				t.Errorf("barFill(%v, %d) = %d, want %d", tt.fraction, tt.width, got, tt.want)
			}
		})
	}
}

func TestBarGlyphs(t *testing.T) {
	tests := []struct {
		name          string
		filled, width int
		want          string
	}{
		{name: "empty", filled: 0, width: 4, want: "░░░░"},
		{name: "partial", filled: 2, width: 4, want: "██░░"},
		{name: "full", filled: 4, width: 4, want: "████"},
		{name: "overfull is clamped", filled: 9, width: 4, want: "████"},
		{name: "negative is clamped", filled: -2, width: 4, want: "░░░░"},
		{name: "zero width", filled: 3, width: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := barGlyphs(tt.filled, tt.width)
			if got != tt.want {
				t.Errorf("barGlyphs(%d, %d) = %q, want %q", tt.filled, tt.width, got, tt.want)
			}
			// A bar is a fixed-width field. Whatever it is asked for, it draws
			// exactly width cells or the caller's layout shifts under it.
			if want := max(tt.width, 0); len([]rune(got)) != want {
				t.Errorf("barGlyphs(%d, %d) drew %d cells, want %d",
					tt.filled, tt.width, len([]rune(got)), want)
			}
		})
	}
}

// The status gauge builds its own two styled runs from barFill rather than
// using barGlyphs, so this pins that the two still describe the same bar.
func TestRenderContextBarWidthMatchesBarGlyphs(t *testing.T) {
	for _, pct := range []float64{-10, 0, 33.3, 60, 80, 100, 250} {
		bar := renderContextBar(pct, Palette{}.Transparent, paletteOrDark(Palette{}))
		filled := strings.Count(bar, barFilled)
		empty := strings.Count(bar, barEmpty)
		if filled+empty != contextBarWidth {
			t.Errorf("renderContextBar(%v) drew %d+%d cells, want %d",
				pct, filled, empty, contextBarWidth)
		}
		if want := barFill(min(max(pct, 0), 100)/100, contextBarWidth); filled != want {
			t.Errorf("renderContextBar(%v) filled %d cells, want %d", pct, filled, want)
		}
	}
}

// The existing TestEstimateContextTokenCount only asserts the estimate is
// positive. Pin the actual arithmetic here, since the status bar and /context
// now share it and a change to the ratio would move both at once.
func TestEstimateContextTokenCountIsExact(t *testing.T) {
	msgs := []message{
		{role: "user", content: strings.Repeat("a", 40)},
		{role: "tool", tool: strings.Repeat("b", 8), toolIn: strings.Repeat("c", 12)},
	}

	// 40 + 8 + 12 = 60 chars at ~4 chars per token.
	if got := estimateContextTokenCount(msgs); got != 15 {
		t.Errorf("estimateContextTokenCount() = %d, want 15", got)
	}
}

// messageChars decides what counts toward the estimate. It is one function
// because it used to be three, and a field added to message would then have
// been counted by some of them and not others.
func TestMessageCharsCountsEveryTextField(t *testing.T) {
	msg := message{content: "abcd", tool: "ef", toolIn: "ghij"}
	if got := messageChars(msg); got != 10 {
		t.Errorf("messageChars() = %d, want 10", got)
	}
}
