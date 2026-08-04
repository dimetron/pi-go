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
		{"green below 60", 0, "#a6e3a1"},
		{"green just below 60", 59.9, "#a6e3a1"},
		{"peach at 60", 60, "#fab387"},
		{"peach just below 80", 79.9, "#fab387"},
		{"red at 80", 80, "#f38ba8"},
		{"red at 100", 100, "#f38ba8"},
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

// The gauge and the sidebar bar must never disagree about how alarming a number
// is. This locates the rule's chosen color inside the bar's actual escape
// output, rather than asking contextSeverityColor whether it agrees with itself.
func TestContextRule_MatchesSidebarBarThresholds(t *testing.T) {
	for _, pct := range []float64{0, 30, 59, 60, 70, 79, 80, 95, 100} {
		want := truecolorFragment(contextSeverityColor(pct))
		bar := renderContextBar(pct, nil)
		if !strings.Contains(bar, want) {
			t.Errorf("pct=%.0f: bar %q does not use the rule's severity color %s",
				pct, bar, want)
		}
	}
}

func TestContextRule_FillGrowsWithUsage(t *testing.T) {
	const width = 100
	prev := -1
	for _, used := range []int64{1_000, 64_000, 128_000, 192_000, 256_000} {
		pct := float64(used) / float64(defaultContextWindow) * 100
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
	out := renderContextRule(contextRuleInput{Width: 120, UsedTokens: 128_000})
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "256.0k") {
		t.Errorf("expected the assumed 256k denominator, got %q", plain)
	}
	if !strings.Contains(plain, "50%") {
		t.Errorf("expected 50%% of the assumed window, got %q", plain)
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
func TestContextRule_ReadoutNamesTheScaleInUse(t *testing.T) {
	tests := []struct {
		used      int64
		wantScale string
		wantPct   string
	}{
		{100_000, "256.0k", "39%"},
		{midScaleThreshold, "500.0k", "40%"},
		{maxScaleThreshold, "1.0M", "40%"},
		{800_000, "1.0M", "80%"},
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
func TestContextRule_StepUpRescalesDownward(t *testing.T) {
	const width = 120
	below := renderContextRule(contextRuleInput{Width: width, UsedTokens: midScaleThreshold - 1})
	at := renderContextRule(contextRuleInput{Width: width, UsedTokens: midScaleThreshold})

	belowFill := filledCells(below, float64(midScaleThreshold-1)/defaultContextWindow*100)
	atFill := filledCells(at, float64(midScaleThreshold)/midContextWindow*100)

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
