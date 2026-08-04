package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The rule below the prompt input doubles as the session's context gauge. It
// was already a full-width horizontal line spending a whole row on decoration;
// filling the leading run in proportion to context used turns that row into a
// reading without costing any height.
//
// The gauge shares its thresholds with renderContextBar (green < 60, peach
// 60-80, red >= 80), which also line up with the two-stage auto-compactor:
// peach is where shedding starts, red is where a summarizing rebuild is near.

// The gauge auto-ranges when the model's real window is unknown, the way a
// multimeter does: start on the 256k scale, and step up once usage runs past
// what that scale can show usefully. Without this a million-token session pegs
// at 100% and stops saying anything; with a fixed 1M scale instead, every
// ordinary session crawls along the first fifth of the rule.
//
// Stepping up rescales, so the filled run visibly shortens as it crosses a
// boundary (200k reads 78% on the 256k scale, 40% on the 500k scale). That
// jump is the cost of keeping resolution at both ends, and the readout always
// prints the scale in use, so the number never lies about what it is measuring.
//
// This is a pure function of current usage, not a ratchet: after compaction
// drops usage back down, the gauge returns to the finer scale rather than
// staying zoomed out on a window that is no longer relevant.
const (
	defaultContextWindow = 256_000
	midContextWindow     = 500_000
	maxContextWindow     = 1_000_000

	// Step-up points, in tokens used.
	midScaleThreshold = 200_000
	maxScaleThreshold = 400_000
)

// autoRangeWindow picks the gauge's full-scale value for the given usage.
func autoRangeWindow(used int64) int64 {
	switch {
	case used >= maxScaleThreshold:
		return maxContextWindow
	case used >= midScaleThreshold:
		return midContextWindow
	default:
		return defaultContextWindow
	}
}

// The gauge is drawn entirely in the rule glyph the frame already uses, and
// encodes usage in color alone.
//
// The obvious alternative — a heavier '━' for the filled run — is a trap here:
// U+2501 is EastAsianAmbiguous, so a CJK-configured terminal renders it two
// cells wide and every column to its right shifts. TestFrameChromeHasNoUndeclared
// AmbiguousRunes catches exactly that. Color carries the signal for free and
// keeps the row a rule rather than turning it into a progress bar.
const (
	gaugeFilledGlyph = '─'
	gaugeEmptyGlyph  = '─'
)

// contextRuleInput is everything the gauge reads. Passing a struct keeps the
// renderer a pure function of its inputs, so it is testable without a model.
type contextRuleInput struct {
	Width       int
	UsedTokens  int64
	WindowSize  int64
	FallbackEst int64 // char-derived estimate, used before the first response

	// Breakdown attributes the used portion to its origins. When set, the
	// filled run is drawn as proportional segments instead of one flat color;
	// the severity color still drives the readout, so the headline reading is
	// unchanged either way.
	Breakdown *ContextBreakdown
}

// renderContextRule draws the full-width rule below the input, with its leading
// run filled in proportion to context used and a right-aligned readout.
//
// The result is always exactly Width display cells, so it satisfies the frame's
// width invariant on its own and needs no padding by the caller.
func renderContextRule(in contextRuleInput) string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70")) // Mocha surface2

	if in.Width <= 0 {
		return ""
	}

	used := in.UsedTokens
	if used <= 0 {
		used = in.FallbackEst
	}
	if used < 0 {
		used = 0
	}

	// A window the provider actually reported always wins; auto-ranging is only
	// for the case where nothing knows the real size.
	window := in.WindowSize
	if window <= 0 {
		window = autoRangeWindow(used)
	}

	// Nothing measured yet — a plain rule, so the gauge never implies a reading
	// it does not have.
	if used == 0 {
		return dim.Render(strings.Repeat(string(gaugeEmptyGlyph), in.Width))
	}

	pct := float64(used) / float64(window) * 100
	if pct > 100 {
		pct = 100
	}

	// Floor rather than round: a gauge that reads 100% at 99.6% overstates how
	// full the window is, which is the direction that misleads.
	label := fmt.Sprintf(" %s/%s %d%% ",
		formatTokenCount(used), formatTokenCount(window), int(pct))
	labelWidth := ansi.StringWidth(label)

	// Too narrow to carry a readout: fall back to a bare filled rule rather than
	// truncating the label into something misleading.
	gaugeWidth := in.Width - labelWidth
	if gaugeWidth < 8 {
		return dim.Render(strings.Repeat(string(gaugeEmptyGlyph), in.Width))
	}

	filled := int(pct / 100 * float64(gaugeWidth))
	if filled > gaugeWidth {
		filled = gaugeWidth
	}
	// Any measured usage shows at least one cell, so a nearly-empty context is
	// still visibly a gauge and not a blank rule.
	if filled < 1 {
		filled = 1
	}

	fg := contextSeverityColor(pct)
	labelStyle := lipgloss.NewStyle().Foreground(fg)

	// The filled run is either one severity-colored block or, when a breakdown
	// is available, the same run split into per-origin segments. Both paths
	// produce exactly `filled` cells, so width is invariant to which is used.
	var filledRun string
	if in.Breakdown != nil && in.Breakdown.Total() > 0 {
		filledRun = renderSegmentedGauge(in.Breakdown.withConversationFrom(used), filled)
	} else {
		filledRun = lipgloss.NewStyle().Foreground(fg).
			Render(strings.Repeat(string(gaugeFilledGlyph), filled))
	}

	// Build raw runs, then style each once — styling last keeps escapes out of
	// anything that measures or slices the text.
	return filledRun +
		dim.Render(strings.Repeat(string(gaugeEmptyGlyph), gaugeWidth-filled)) +
		labelStyle.Render(label)
}

// contextSeverityColor maps usage to the same ladder renderContextBar uses, so
// the rule and the sidebar bar never disagree about how alarming a number is.
func contextSeverityColor(pct float64) color.Color {
	switch {
	case pct >= 80:
		return lipgloss.Color("#f38ba8") // Mocha red
	case pct >= 60:
		return lipgloss.Color("#fab387") // Mocha peach
	default:
		return lipgloss.Color("#a6e3a1") // Mocha green
	}
}

// contextRuleFor builds the gauge input from the model's current state.
func (m *model) contextRuleFor(width int) contextRuleInput {
	in := contextRuleInput{Width: width}
	if tt := m.cfg.TokenTracker; tt != nil {
		in.UsedTokens = tt.LastPromptTokens()
		in.WindowSize = tt.ContextWindowSize()
	}
	if in.UsedTokens == 0 {
		in.FallbackEst = estimateContextTokenCount(m.chatModel.Messages)
	}
	in.Breakdown = m.cfg.ContextBreakdown
	return in
}

// estimateContextTokenCount approximates context size from message text at the
// usual ~4 characters per token, for the window before the provider reports a
// real count.
func estimateContextTokenCount(msgs []message) int64 {
	chars := 0
	for _, msg := range msgs {
		chars += len(msg.content) + len(msg.tool) + len(msg.toolIn)
	}
	return int64(chars / 4)
}
