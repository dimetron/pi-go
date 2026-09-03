package diagram

import "sync"

// ── Terminal Width Scaling Constants ─────────────────────────────────────────
//
// Diagrams scale to fill available terminal width. These constants control
// the bounds and behavior of that scaling.
//
// The rendering pipeline:
//   1. usableWidth() returns terminal width (auto-detected or --width override)
//   2. Chart renderers (gantt, xy, pie, etc.) use scaleWidth/scaleGap to size
//      their scalable elements within [min, max] bounds
//   3. Graph renderers (flowchart, state, class, ER) scale node columns via
//      the layout engine at GraphFillRatio of the usable width
//
// Why not 100%? Graph-based diagrams have a 3×3 grid model where nodes absorb
// adjacent gap columns into their draw width. Subgraph borders, canvas margins
// (+4), and nested padding add further overhead. Filling 100% causes overflow.
// Chart renderers control their own canvas size so they can safely fill more.

const (
	// maxScaledWidth caps scalable chart elements (bars, plots, gaps) to prevent
	// unnaturally stretched diagrams on very wide terminals.
	maxScaledWidth = 140

	// Graph-based layouts (flowchart, state, class, ER) fill only part of the
	// usable width; the remainder absorbs canvas margins, subgraph borders and
	// nested padding. That fraction lives in renderer/draw.go as
	// graphFillPercent — this package cannot import it without a cycle, and
	// the copy that used to sit here was never read.
)

// ── Width Override ───────────────────────────────────────────────────────────

// diagramWidth is the effective width for diagram rendering.
// When 0 (default), auto-detects from the terminal.
// Set via CLI --width/-w flag.
var diagramWidth int

// SetWidthOverride forces a fixed rendering width, bypassing terminal detection.
func SetWidthOverride(w int) {
	widthMu.Lock()
	defer widthMu.Unlock()
	diagramWidth = w
}

// widthMu guards diagramWidth. The width is package state rather than a
// parameter because fourteen of the diagram renderers reach for it directly;
// threading it through each of them is a larger refactor than this package
// wants today. The lock at least makes concurrent renders safe.
var widthMu sync.Mutex

// WithWidthScope runs fn with the diagram width pinned to cols, restoring the
// previous value afterwards. Renders are serialized for the duration, which is
// acceptable while a render is sub-millisecond and the TUI drives them from a
// single goroutine.
//
// cols <= 0 runs fn against the detected terminal width.
func WithWidthScope(cols int, fn func()) {
	widthMu.Lock()
	defer widthMu.Unlock()

	prev := diagramWidth
	diagramWidth = cols
	defer func() { diagramWidth = prev }()

	fn()
}

// UsableWidth returns the diagram rendering width.
// Returns the override if set, otherwise the detected terminal width.
// Exported so the root package can pass it to the graph layout engine.
func UsableWidth() int {
	return usableWidth()
}

func usableWidth() int {
	if diagramWidth > 0 {
		return diagramWidth
	}
	return getTerminalWidth()
}

// ── Scaling Helpers ─────────────────────────────────────────────────────────

// scaleWidth adjusts a fixed-width element to use available diagram space.
// fixedOverhead is the non-scalable portion (labels, borders, margins).
// Returns the width clamped between minW and maxW.
func scaleWidth(fixedOverhead, minW, maxW int) int {
	available := usableWidth() - fixedOverhead
	if available < minW {
		return minW
	}
	if available > maxW {
		return maxW
	}
	return available
}

// scaleGap distributes available diagram width across nGaps gaps.
// naturalWidth is the diagram's natural width at current gap spacing.
// Returns adjusted gap clamped between minGap and maxGap.
func scaleGap(baseGap, nGaps, naturalWidth, minGap, maxGap int) int {
	if nGaps <= 0 {
		return baseGap
	}
	slack := usableWidth() - naturalWidth
	perGap := slack / nGaps
	newGap := baseGap + perGap
	if newGap < minGap {
		return minGap
	}
	if newGap > maxGap {
		return maxGap
	}
	return newGap
}
