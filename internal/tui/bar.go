package tui

import "strings"

// Proportional bars are drawn in four places — the status bar's context gauge,
// two sections of /context, and the startup progress line — and until this file
// existed each one clamped its own fill and repeated its own glyphs. They had
// already drifted: three clamped the fill against the width, one clamped the
// percentage as well, and the block characters were spelled out four times.
//
// The split into fill-then-draw is what the call sites actually need. The
// status gauge colors the filled and empty runs differently, so it wants the
// count and builds its own two styled segments; everything else renders into
// markdown, where ANSI inside a code span would be printed literally, so it
// wants the plain string.

const (
	barFilled = "█"
	barEmpty  = "░"
)

// barFill returns how many of width cells represent fraction, clamped so a bar
// can never draw past its own width — the guard every call site used to carry.
func barFill(fraction float64, width int) int {
	if fraction <= 0 || width <= 0 {
		return 0
	}
	return min(int(fraction*float64(width)), width)
}

// barGlyphs draws an unstyled proportional bar, filled cells first.
func barGlyphs(filled, width int) string {
	if width <= 0 {
		return ""
	}
	filled = min(max(filled, 0), width)
	return strings.Repeat(barFilled, filled) + strings.Repeat(barEmpty, width-filled)
}
