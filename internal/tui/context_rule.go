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
// The gauge is calibrated to the dumb-zone framework (Dex Horthy, HumanLayer;
// see specs/research/007-codex-context-compaction/research/dumb-zone.md):
// 100% on the bar means "you have just entered the dumb zone" — the point past
// which reasoning quality collapses (≥70% of the model's nominal context
// window). The fill therefore scales against `dumbZoneFraction * window`,
// not against the full window, so the bar visibly pegs the moment a session
// becomes unsafe rather than continuing to crawl along the last 30% of the
// ruler on its way to 100%.
//
// Color thresholds track the same zones:
//   - green  = smart   (used/window <  warmZoneFraction)
//   - peach  = warm    (warmZoneFraction ≤ used/window < dumbZoneFraction)
//   - red    = dumb    (used/window ≥ dumbZoneFraction)
//
// renderContextBar keeps its own 60/80 ladder over the raw used/window ratio
// — the sidebar compactness line is read alongside the daily-token number,
// not as a danger signal, so it does not adopt the dumb-zone frames.

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

// Zone fractions from the dumb-zone framework. These are fractions of the
// model's nominal context window:
//
//	0 .. warmZoneFraction       smart  (peak reasoning)
//	warmZoneFraction .. dumbZoneFraction  warm   (degrading)
//	dumbZoneFraction .. 1       dumb   (broken — compact now)
//
// The gauge fills the smart and warm zones — i.e. up to dumbZoneFraction of
// the window — and pegs at 100% once the dumb zone is entered. Past 100%,
// the gauge cannot say more than "you are already here".
const (
	warmZoneFraction = 0.40 // smart → warm boundary
	dumbZoneFraction = 0.70 // warm → dumb boundary
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

	// The denominator is the dumb-zone boundary, not the full window: at 70% of
	// the window the bar already reads 100% and turns red. Anything past it is
	// out of the gauge's vocabulary, so cap rather than overflow.
	//
	// Dumb-zone framework: dumbZoneFraction of the window is where reasoning
	// breaks down, so the bar must be saturated by then. (`pct == 100` here
	// does not mean "window full"; it means "you have entered the dumb zone.")
	pct := float64(used) / (float64(window) * dumbZoneFraction) * 100
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

// contextSeverityColor maps gauge fill (0–100, where 100 = entering the dumb
// zone) to a color, in lockstep with the dumb-zone framework:
//
//	green   pct <  warm/dumb — smart zone
//	peach   pct >= warm/dumb — warm zone
//	red     pct == 100        — dumb zone (entered)
//
// Input is the bar's own pct (full-scale = dumb zone entry), not the raw
// used/window ratio. That keeps the color picker testable by the same number
// the bar prints, so a user who sees "100%" sees red — the bar reaches 100
// only at the dumb-zone boundary.
func contextSeverityColor(pct float64) color.Color {
	switch {
	case pct >= 100:
		return lipgloss.Color("#f38ba8") // Mocha red — dumb zone
	case pct >= warmZoneFraction/dumbZoneFraction*100:
		return lipgloss.Color("#fab387") // Mocha peach — warm zone
	default:
		return lipgloss.Color("#a6e3a1") // Mocha green — smart zone
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
