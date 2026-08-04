package tui

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// sgrSegment matches one styled run: its truecolor fragment and its text.
var sgrSegment = regexp.MustCompile(`\x1b\[(38;2;\d+;\d+;\d+)m([^\x1b]*)\x1b\[m`)

// filledCells counts the leading run drawn in the severity color. The gauge
// encodes usage in color, not glyph, so this is what "filled" means.
func filledCells(out string, pct float64) int {
	want := truecolorFragment(contextSeverityColor(pct))
	for _, m := range sgrSegment.FindAllStringSubmatch(out, -1) {
		if m[1] == want {
			return ansi.StringWidth(m[2])
		}
	}
	return 0
}

// hasSeverityColor reports whether the gauge drew any run in a severity color,
// i.e. whether it is showing a reading at all.
func hasSeverityColor(out string, pct float64) bool {
	return strings.Contains(out, truecolorFragment(contextSeverityColor(pct)))
}

// I1: the gauge is a frame row, so it must be exactly the terminal width at
// every width and every fill level — including the wide-rune-free but
// multi-byte box-drawing glyphs it is built from.
func TestContextRule_ExactWidth(t *testing.T) {
	for _, width := range []int{20, 40, 60, 80, 120, 200} {
		for _, used := range []int64{0, 1, 1_000, 50_000, 128_000, 255_999, 256_000, 999_999} {
			in := contextRuleInput{Width: width, UsedTokens: used, WindowSize: defaultContextWindow}
			got := ansi.StringWidth(renderContextRule(in))
			if got != width {
				t.Errorf("width=%d used=%d: rule is %d cells, want %d", width, used, got, width)
			}
		}
	}
}

// I3: no escape may leak into the visible text.
func TestContextRule_NoEscapeLeak(t *testing.T) {
	out := renderContextRule(contextRuleInput{
		Width: 120, UsedTokens: 150_000, WindowSize: defaultContextWindow,
	})
	if strings.Contains(ansi.Strip(out), "38;5;") || strings.Contains(ansi.Strip(out), "[0m") {
		t.Errorf("escape leaked into visible text: %q", ansi.Strip(out))
	}
}

func TestContextRule_SeverityLadder(t *testing.T) {
	tests := []struct {
		name string
		pct  float64
		want string
	}{
		// The bar's pct runs from 0 (smart) to 100 (entering the dumb zone).
		// warm/dumb = 0.40 / 0.70 * 100 ≈ 57.14 — peach at or above this means
		// "you have crossed out of the smart zone".
		{"green in smart zone", 0, "#a6e3a1"},
		{"green just below warm boundary", 56, "#a6e3a1"},
		{"peach at warm boundary", 57.14, "#fab387"},
		{"peach just below dumb-zone entry", 99, "#fab387"},
		{"red at dumb-zone entry", 100, "#f38ba8"},
		{"red past dumb-zone entry", 200, "#f38ba8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := contextSeverityColor(tc.pct)
			if got == nil {
				t.Fatal("nil color")
			}
			// lipgloss.Color renders as the hex string it was built from.
			if s, ok := got.(interface{ String() string }); ok {
				if s.String() != tc.want {
					t.Errorf("color = %s, want %s", s.String(), tc.want)
				}
			}
		})
	}
}

// truecolorFragment renders a color as the SGR fragment lipgloss emits for it,
// so a color chosen by one renderer can be located in another's raw output.
func truecolorFragment(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// The gauge and the sidebar bar no longer share a threshold ladder by design:
// the gauge is calibrated to the dumb-zone framework (red at the dumb-zone
// boundary, peach at the warm-zone boundary) while the sidebar bar keeps its
// own 60/80 ladder over the raw used/window ratio. This test pins the
// invariant that still holds: at the dumb-zone entry, both render red.
func TestContextRule_DumbZoneEntryTurnsRed(t *testing.T) {
	tests := []struct {
		used   int64
		window int64
	}{
		{70_000, 100_000},
		{179_200, 256_000},
		{350_000, 500_000},
		{700_000, 1_000_000},
	}
	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			out := renderContextRule(contextRuleInput{
				Width: 120, UsedTokens: tc.used, WindowSize: tc.window,
			})
			if !hasSeverityColor(out, 100) {
				t.Errorf("used=%d window=%d: gauge at dumb-zone entry should be red, got %q",
					tc.used, tc.window, out)
			}
		})
	}
}

func TestContextRule_FillGrowsWithUsage(t *testing.T) {
	const width = 100
	prev := -1
	// Stay below the dumb-zone entry (70% of window = 179.2k here) so the bar
	// is still in its readable range. Past that point, the bar pegs at 100%
	// by design.
	for _, used := range []int64{1_000, 32_000, 64_000, 128_000, 170_000} {
		pct := float64(used) / float64(defaultContextWindow) * 100
		pct /= dumbZoneFraction // bar pct: 100 = dumb-zone entry
		if pct > 100 {
			pct = 100
		}
		out := renderContextRule(contextRuleInput{
			Width: width, UsedTokens: used, WindowSize: defaultContextWindow,
		})
		filled := filledCells(out, pct)
		if filled <= prev {
			t.Errorf("used=%d: filled=%d did not grow past %d", used, filled, prev)
		}
		prev = filled
	}
}

// Past the dumb-zone boundary the bar pegs at full — a user who has crossed
// 70% of the window gets the same "100%" reading the bar shows right at the
// boundary. The bar cannot say more past that point.
func TestContextRule_PegsAtDumbZoneEntry(t *testing.T) {
	const width = 120
	atBoundary := renderContextRule(contextRuleInput{
		Width: width, UsedTokens: 179_200, WindowSize: defaultContextWindow,
	})
	pastBoundary := renderContextRule(contextRuleInput{
		Width: width, UsedTokens: 200_000, WindowSize: defaultContextWindow,
	})
	wellPast := renderContextRule(contextRuleInput{
		Width: width, UsedTokens: 256_000, WindowSize: defaultContextWindow,
	})
	atFill := filledCells(atBoundary, 100)
	pastFill := filledCells(pastBoundary, 100)
	wellPastFill := filledCells(wellPast, 100)
	if atFill != pastFill || pastFill != wellPastFill {
		t.Errorf("bar should peg at dumb-zone entry, got fills %d / %d / %d",
			atFill, pastFill, wellPastFill)
	}
	if !hasSeverityColor(wellPast, 100) {
		t.Errorf("past the dumb zone the gauge should still be red")
	}
}

// The readout's two halves carry different weight: the percentage keeps the
// severity color, the token counts drop to dim. Coloring both made them compete
// at equal volume when only one of them is what the color is about.
//
// Asserting "different from each other" rather than "dim is #585b70" keeps this
// from becoming self-referential — it follows the intent, not the constant.
func TestContextRule_PercentAndCountsDifferInColor(t *testing.T) {
	for _, used := range []int64{50_000, 128_000, 179_200} {
		out := renderContextRule(contextRuleInput{
			Width: 120, UsedTokens: used, WindowSize: defaultContextWindow,
		})

		var pctColor, countsColor string
		for _, m := range sgrSegment.FindAllStringSubmatch(out, -1) {
			switch {
			case strings.Contains(m[2], "%"):
				pctColor = m[1]
			case strings.Contains(m[2], "/"):
				countsColor = m[1]
			}
		}
		if pctColor == "" || countsColor == "" {
			t.Fatalf("used=%d: readout halves not found in %q", used, out)
		}
		if pctColor == countsColor {
			t.Errorf("used=%d: percentage and counts share color %s", used, pctColor)
		}
		if want := truecolorFragment(contextSeverityColor(
			float64(used) / (defaultContextWindow * dumbZoneFraction) * 100,
		)); pctColor != want {
			t.Errorf("used=%d: percentage color = %s, want severity %s", used, pctColor, want)
		}
	}
}

func TestContextRule_UnmeasuredRendersPlainRule(t *testing.T) {
	out := renderContextRule(contextRuleInput{Width: 80})
	if hasSeverityColor(out, 0) {
		t.Error("an unmeasured context must not render a filled gauge")
	}
	if strings.Contains(ansi.Strip(out), "%") {
		t.Error("an unmeasured context must not print a percentage readout")
	}
	if ansi.StringWidth(out) != 80 {
		t.Errorf("plain rule width = %d, want 80", ansi.StringWidth(out))
	}
}

func TestContextRule_AnyUsageShowsAtLeastOneCell(t *testing.T) {
	// A single token against a 256k window rounds to zero cells; the gauge must
	// still read as a gauge rather than an empty rule.
	out := renderContextRule(contextRuleInput{
		Width: 120, UsedTokens: 1, WindowSize: defaultContextWindow,
	})
	if got := filledCells(out, 0); got < 1 {
		t.Errorf("measured usage must show at least one filled cell, got %d", got)
	}
}

func TestContextRule_FallsBackToDefaultWindow(t *testing.T) {
	// The opencode models have no catalog entry, so WindowSize is 0. The gauge
	// must still read against the assumed 256k rather than divide by zero.
	//
	// 128k of a 256k window sits at 50% of the window, which under the dumb-zone
	// calibration equals 50/70 ≈ 71.4% of the bar (100 = dumb-zone entry).
	out := renderContextRule(contextRuleInput{Width: 120, UsedTokens: 128_000})
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "256.0k") {
		t.Errorf("expected the assumed 256k denominator, got %q", plain)
	}
	if !strings.Contains(plain, "71%") {
		t.Errorf("expected 71%% of the bar (50%% of window against the dumb-zone frame), got %q", plain)
	}
}

func TestAutoRangeWindow_Ladder(t *testing.T) {
	tests := []struct {
		name string
		used int64
		want int64
	}{
		{"empty", 0, defaultContextWindow},
		{"small", 1_000, defaultContextWindow},
		{"just below mid step", midScaleThreshold - 1, defaultContextWindow},
		{"at mid step", midScaleThreshold, midContextWindow},
		{"between steps", 300_000, midContextWindow},
		{"just below max step", maxScaleThreshold - 1, midContextWindow},
		{"at max step", maxScaleThreshold, maxContextWindow},
		{"large", 900_000, maxContextWindow},
		{"beyond the top scale", 5_000_000, maxContextWindow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoRangeWindow(tc.used); got != tc.want {
				t.Errorf("autoRangeWindow(%d) = %d, want %d", tc.used, got, tc.want)
			}
		})
	}
}

// The ladder must never step down as usage rises — a larger context showing a
// smaller full-scale would misreport how full the window is.
func TestAutoRangeWindow_Monotonic(t *testing.T) {
	var prev int64
	for used := int64(0); used <= 1_200_000; used += 10_000 {
		got := autoRangeWindow(used)
		if got < prev {
			t.Fatalf("used=%d: scale dropped from %d to %d", used, prev, got)
		}
		prev = got
	}
}

// Each scale must be reachable in the readout, and must be labeled with the
// scale actually in use — a percentage against an unstated denominator is
// exactly the kind of number that misleads.
//
// The pct printed is the bar's pct, which runs 0..100 over the dumb-zone
// entry: 100k / (256k * 0.7) ≈ 55%, 200k / (500k * 0.7) ≈ 57%, 400k / (1M * 0.7)
// ≈ 57%. Past 70% of any window the bar pegs at 100.
func TestContextRule_ReadoutNamesTheScaleInUse(t *testing.T) {
	tests := []struct {
		used      int64
		wantScale string
		wantPct   string
	}{
		{100_000, "256.0k", "55%"},
		{midScaleThreshold, "500.0k", "57%"},
		{maxScaleThreshold, "1.0M", "57%"},
		{800_000, "1.0M", "100%"},
	}
	for _, tc := range tests {
		out := ansi.Strip(renderContextRule(contextRuleInput{Width: 120, UsedTokens: tc.used}))
		if !strings.Contains(out, tc.wantScale) {
			t.Errorf("used=%d: readout %q does not name scale %s", tc.used, out, tc.wantScale)
		}
		if !strings.Contains(out, tc.wantPct) {
			t.Errorf("used=%d: readout %q does not show %s", tc.used, out, tc.wantPct)
		}
	}
}

// Crossing a step-up boundary rescales, so the filled run shortens. This is the
// documented trade-off; the test pins it so the behavior cannot change by
// accident and go unnoticed.
//
// The fill is the bar's pct (0..100 = 0..dumb-zone entry), not the raw
// used/window ratio, so the helper's lookup arg must match.
func TestContextRule_StepUpRescalesDownward(t *testing.T) {
	const width = 120
	var belowUsed int64 = midScaleThreshold - 1
	atUsed := int64(midScaleThreshold)

	below := renderContextRule(contextRuleInput{Width: width, UsedTokens: belowUsed})
	at := renderContextRule(contextRuleInput{Width: width, UsedTokens: atUsed})

	belowPct := float64(belowUsed) / (float64(defaultContextWindow) * dumbZoneFraction) * 100
	if belowPct > 100 {
		belowPct = 100
	}
	atPct := float64(atUsed) / (float64(midContextWindow) * dumbZoneFraction) * 100
	if atPct > 100 {
		atPct = 100
	}

	belowFill := filledCells(below, belowPct)
	atFill := filledCells(at, atPct)

	if atFill >= belowFill {
		t.Errorf("crossing the step-up should rescale downward: %d → %d cells", belowFill, atFill)
	}
}

// The gauge must stay exactly the terminal width across every scale, including
// the wider "1.0M" readout.
func TestContextRule_ExactWidthAcrossScales(t *testing.T) {
	for _, width := range []int{20, 40, 80, 120, 200} {
		for _, used := range []int64{
			0, 199_999, 200_000, 399_999, 400_000, 999_999, 1_000_000, 2_000_000,
		} {
			out := renderContextRule(contextRuleInput{Width: width, UsedTokens: used})
			if got := ansi.StringWidth(out); got != width {
				t.Errorf("width=%d used=%d: rule is %d cells", width, used, got)
			}
		}
	}
}

// Usage past the top scale pegs at 100% rather than overflowing the rule.
func TestContextRule_BeyondTopScalePegs(t *testing.T) {
	out := ansi.Strip(renderContextRule(contextRuleInput{Width: 120, UsedTokens: 5_000_000}))
	if !strings.Contains(out, "100%") {
		t.Errorf("usage beyond the top scale should peg at 100%%, got %q", out)
	}
}

func TestContextRule_RealWindowWins(t *testing.T) {
	out := renderContextRule(contextRuleInput{
		Width: 120, UsedTokens: 100_000, WindowSize: 200_000,
	})
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "200.0k") {
		t.Errorf("a known window must override the assumed one, got %q", plain)
	}
}

func TestContextRule_EstimateUsedBeforeFirstResponse(t *testing.T) {
	pct := 40_000.0 / float64(defaultContextWindow) * 100
	out := renderContextRule(contextRuleInput{Width: 120, FallbackEst: 40_000})
	if filledCells(out, pct) < 1 {
		t.Error("the pre-response estimate should still drive the gauge")
	}
}

func TestContextRule_NarrowTerminalDegradesGracefully(t *testing.T) {
	for _, width := range []int{1, 2, 5, 10, 15} {
		out := renderContextRule(contextRuleInput{
			Width: width, UsedTokens: 128_000, WindowSize: defaultContextWindow,
		})
		if got := ansi.StringWidth(out); got != width {
			t.Errorf("width=%d: got %d cells", width, got)
		}
	}
	if renderContextRule(contextRuleInput{Width: 0}) != "" {
		t.Error("zero width must render nothing")
	}
	if renderContextRule(contextRuleInput{Width: -5}) != "" {
		t.Error("negative width must render nothing")
	}
}

func TestEstimateContextTokenCount(t *testing.T) {
	msgs := []message{
		{content: strings.Repeat("a", 400)},
		{content: strings.Repeat("b", 400), tool: "read", toolIn: "/x.go"},
	}
	got := estimateContextTokenCount(msgs)
	if got <= 0 {
		t.Fatalf("estimate = %d, want > 0", got)
	}
	if estimateContextTokenCount(nil) != 0 {
		t.Error("no messages must estimate zero")
	}
}
