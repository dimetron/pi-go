package renderer

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

// buildGraph assembles a two-node graph directly, without going through the
// parser, so these tests fail for renderer reasons only.
func buildGraph(t *testing.T) *graph.Graph {
	t.Helper()

	g := graph.NewGraph()
	g.Direction = graph.DirLR
	g.AddNode(&graph.Node{ID: "a", Label: "Alpha", Shape: graph.ShapeRectangle})
	g.AddNode(&graph.Node{ID: "b", Label: "Beta", Shape: graph.ShapeRounded})
	g.Edges = append(g.Edges, graph.Edge{Source: "a", Target: "b", HasArrowEnd: true})
	return g
}

// TestRenderGraph covers the string-returning entry point. Render() in the
// parent package goes through RenderGraphCanvas so it can apply a theme;
// RenderGraph is the shortcut for callers that just want text.
func TestRenderGraph(t *testing.T) {
	got := RenderGraph(buildGraph(t), false, 4, 2, true)
	if got == "" {
		t.Fatal("RenderGraph returned empty output")
	}
	for _, want := range []string{"Alpha", "Beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing node label %q:\n%s", want, got)
		}
	}
}

// TestRenderGraphASCII asserts the ASCII path of the same entry point emits no
// characters the Unicode-free terminals it exists for cannot draw.
func TestRenderGraphASCII(t *testing.T) {
	for _, r := range RenderGraph(buildGraph(t), true, 4, 2, true) {
		if r > 127 {
			t.Fatalf("ASCII mode emitted non-ASCII rune %q", r)
		}
	}
}

// TestRenderGraphEmpty asserts a graph with no nodes renders without panicking.
// An empty graph reaches here whenever the parser rejects its input, which the
// corpus shows happens for syntax this engine does not model yet.
func TestRenderGraphEmpty(t *testing.T) {
	if got := RenderGraph(graph.NewGraph(), false, 4, 2, true); strings.Contains(got, "panic") {
		t.Fatalf("empty graph produced %q", got)
	}
}

// TestCanvasGetOutOfBounds pins the bounds behavior of the accessors: reads
// outside the canvas return a blank rather than panicking. The drawing code
// indexes by computed coordinates, so this guard is load-bearing — an
// off-by-one in a routing pass must produce a gap, not a crash in the TUI.
//
// Get and GetFill return different blanks: a space for the character grid,
// since that is what an unwritten cell holds, and an empty string for the fill
// grid, since that means "no style".
func TestCanvasGetOutOfBounds(t *testing.T) {
	c := NewCanvas(4, 3)
	c.Put(1, 1, 'X', false, "node")

	if got := c.Get(1, 1); got != 'X' {
		t.Errorf("Get(1,1) = %q, want 'X'", got)
	}
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {3, 0}, {0, 4}} {
		if got := c.Get(p[0], p[1]); got != ' ' {
			t.Errorf("Get(%d,%d) = %q, want a space", p[0], p[1], got)
		}
		if got := c.GetFill(p[0], p[1]); got != "" {
			t.Errorf("GetFill(%d,%d) = %q, want empty", p[0], p[1], got)
		}
	}
}

// TestCanvasToStyledPairs covers the accessor a caller uses to re-style output
// itself — the shape a TUI needs when it wants to map diagram styles onto its
// own palette instead of taking the package's ANSI.
func TestCanvasToStyledPairs(t *testing.T) {
	c := NewCanvas(3, 2)
	c.Put(0, 0, 'A', false, "node")

	pairs := c.ToStyledPairs()
	if len(pairs) != 2 {
		t.Fatalf("got %d rows, want 2", len(pairs))
	}
	if len(pairs[0]) != 3 {
		t.Fatalf("got %d cols, want 3", len(pairs[0]))
	}
	if pairs[0][0].Char != 'A' {
		t.Errorf("Char = %q, want 'A'", pairs[0][0].Char)
	}
	if pairs[0][0].Style != "node" {
		t.Errorf("Style = %q, want \"node\"", pairs[0][0].Style)
	}
}

// TestGetThemeUnknownName asserts an unregistered name yields a usable theme
// rather than a zero value, since theme names arrive from user config.
func TestGetThemeUnknownName(t *testing.T) {
	known := GetTheme("default")
	unknown := GetTheme("definitely-not-a-theme")
	if unknown.Node == "" && known.Node != "" {
		t.Error("unknown theme name returned an unusable zero theme")
	}
}

// TestToColorStringAppliesTheme covers the colored-output path, which the
// golden corpus deliberately does not exercise.
func TestToColorStringAppliesTheme(t *testing.T) {
	c := NewCanvas(3, 1)
	c.Put(0, 0, 'A', false, "node")

	theme := GetTheme("default")
	got := c.ToColorString(theme)
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("ToColorString emitted no ANSI escapes: %q", got)
	}
	if !strings.Contains(got, "A") {
		t.Errorf("ToColorString dropped the content: %q", got)
	}
}
