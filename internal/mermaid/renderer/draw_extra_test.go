package renderer

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/layout"
	"github.com/dimetron/pi-go/internal/mermaid/routing"
)

func TestLabelSegmentsToStyled(t *testing.T) {
	tests := []struct {
		name string
		seg  graph.LabelSegment
		want string
	}{
		{"plain", graph.LabelSegment{Text: "a"}, "label"},
		{"bold", graph.LabelSegment{Text: "a", Bold: true}, "label_bold"},
		{"italic", graph.LabelSegment{Text: "a", Italic: true}, "label_italic"},
		{"bold italic", graph.LabelSegment{Text: "a", Bold: true, Italic: true}, "label_bold_italic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := labelSegmentsToStyled([]graph.LabelSegment{tt.seg})
			if len(got) != 1 {
				t.Fatalf("expected 1 segment, got %d", len(got))
			}
			if got[0].Style != tt.want {
				t.Errorf("style = %q, want %q", got[0].Style, tt.want)
			}
			if got[0].Text != tt.seg.Text {
				t.Errorf("text = %q, want %q", got[0].Text, tt.seg.Text)
			}
		})
	}
	if got := labelSegmentsToStyled(nil); len(got) != 0 {
		t.Errorf("expected no segments for nil input, got %d", len(got))
	}
}

// pts builds a path from alternating col,row values, keeping the routing.Point
// literals keyed (vet forbids unkeyed fields on an imported struct).
func pts(coords ...int) []routing.Point {
	out := make([]routing.Point, 0, len(coords)/2)
	for i := 0; i+1 < len(coords); i += 2 {
		out = append(out, routing.Point{Col: coords[i], Row: coords[i+1]})
	}
	return out
}

func TestLabelOverlaps(t *testing.T) {
	placed := []placedLabel{{row: 2, colStart: 5, colEnd: 10}}

	cases := []struct {
		name                  string
		row, colStart, colEnd int
		want                  bool
	}{
		{"different row", 3, 5, 10, false},
		{"exactly on top", 2, 5, 10, true},
		{"overlapping start", 2, 8, 12, true},
		{"overlapping end", 2, 3, 7, true},
		{"fully inside", 2, 6, 8, true},
		{"fully covering", 2, 0, 20, true},
		{"adjacent after", 2, 10, 15, false},
		{"adjacent before", 2, 0, 5, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := labelOverlaps(tt.row, tt.colStart, tt.colEnd, placed); got != tt.want {
				t.Errorf("labelOverlaps = %v, want %v", got, tt.want)
			}
		})
	}
	if labelOverlaps(0, 0, 5, nil) {
		t.Error("expected no overlap against an empty list")
	}
}

func TestTryPlaceLabel(t *testing.T) {
	c := NewCanvas(20, 5)
	var placed []placedLabel

	if !tryPlaceLabel(c, 1, 2, "edge", &placed) {
		t.Fatal("expected the first label to be placed")
	}
	if !strings.Contains(c.ToString(), "edge") {
		t.Errorf("expected the label on the canvas:\n%s", c.ToString())
	}
	if len(placed) != 1 {
		t.Fatalf("expected 1 placed label, got %d", len(placed))
	}

	// A second label at the same spot collides and is refused.
	if tryPlaceLabel(c, 1, 2, "clash", &placed) {
		t.Error("expected an overlapping label to be refused")
	}
	// Negative coordinates are refused rather than written out of bounds.
	if tryPlaceLabel(c, -1, 0, "neg", &placed) {
		t.Error("expected a negative row to be refused")
	}
	if tryPlaceLabel(c, 0, -1, "neg", &placed) {
		t.Error("expected a negative column to be refused")
	}
}

func TestTryPlaceLabelGrowsCanvas(t *testing.T) {
	c := NewCanvas(4, 2)
	var placed []placedLabel
	// The label runs past both edges; the canvas must grow to fit it.
	if !tryPlaceLabel(c, 1, 2, "longlabel", &placed) {
		t.Fatal("expected the label to be placed")
	}
	if c.Width <= 4 {
		t.Errorf("expected the canvas to widen, got width %d", c.Width)
	}
	if !strings.Contains(c.ToString(), "longlabel") {
		t.Errorf("expected the full label after the resize:\n%s", c.ToString())
	}
}

func TestFindLastTurn(t *testing.T) {
	// Straight paths have no turn.
	straight := pts(0, 0, 1, 0, 2, 0, 3, 0)
	if got := findLastTurn(straight); got != -1 {
		t.Errorf("expected no turn on a straight path, got %d", got)
	}
	// Too short to contain a turn.
	for _, p := range [][]routing.Point{nil, pts(0, 0), pts(0, 0, 1, 0)} {
		if got := findLastTurn(p); got != -1 {
			t.Errorf("expected -1 for a %d-point path, got %d", len(p), got)
		}
	}
	// One turn: the index of the corner point, where the direction changes.
	bend := pts(0, 0, 1, 0, 2, 0, 2, 1, 2, 2)
	if got := findLastTurn(bend); got != 2 {
		t.Errorf("expected the corner at index 2, got %d", got)
	}
	// Two turns: the last one wins.
	twice := pts(0, 0, 0, 1, 3, 1, 3, 4)
	if got := findLastTurn(twice); got != 2 {
		t.Errorf("expected the last turn at index 2, got %d", got)
	}
}

func TestTryPlaceOnSegment(t *testing.T) {
	t.Run("vertical", func(t *testing.T) {
		c := NewCanvas(20, 12)
		var placed []placedLabel
		if !tryPlaceOnSegment(c, 5, 0, 5, 10, "v", &placed, nil, false, false) {
			t.Fatal("expected the label to be placed beside a vertical segment")
		}
		if !strings.Contains(c.ToString(), "v") {
			t.Errorf("expected the label on the canvas:\n%s", c.ToString())
		}
	})
	t.Run("vertical prefer left misses", func(t *testing.T) {
		// Column 0 has no room to the left, so the fallback offset must be used.
		c := NewCanvas(20, 12)
		var placed []placedLabel
		if !tryPlaceOnSegment(c, 0, 0, 0, 6, "x", &placed, nil, true, false) {
			t.Error("expected the label to fall back to the right side")
		}
	})
	t.Run("vertical biased to target", func(t *testing.T) {
		c := NewCanvas(20, 12)
		var placed []placedLabel
		if !tryPlaceOnSegment(c, 5, 0, 5, 10, "b", &placed, nil, false, true) {
			t.Fatal("expected a biased label to be placed")
		}
	})
	t.Run("horizontal", func(t *testing.T) {
		c := NewCanvas(20, 12)
		var placed []placedLabel
		if !tryPlaceOnSegment(c, 0, 5, 12, 5, "h", &placed, nil, false, false) {
			t.Fatal("expected the label to be placed above a horizontal segment")
		}
	})
	t.Run("neither axis", func(t *testing.T) {
		// A diagonal segment has no defined placement strategy.
		c := NewCanvas(20, 12)
		var placed []placedLabel
		if tryPlaceOnSegment(c, 0, 0, 5, 5, "d", &placed, nil, false, false) {
			t.Error("expected a diagonal segment to place no label")
		}
	})
}

func TestDrawEdgeLabel(t *testing.T) {
	tests := []struct {
		name  string
		label string
		path  []routing.Point
		want  bool
	}{
		{"no label", "", pts(0, 0, 5, 0), false},
		{"no path", "x", nil, false},
		{"single point", "x", pts(0, 0), false},
		{"horizontal", "x", pts(1, 1, 9, 1), true},
		{"with a turn", "x", pts(1, 1, 1, 6, 9, 6), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCanvas(20, 20)
			var placed []placedLabel
			re := routing.RoutedEdge{Label: tt.label, DrawPath: tt.path}
			drawEdgeLabel(c, re, &placed)
			if got := len(placed) > 0; got != tt.want {
				t.Errorf("placed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDrawSubgraphBorders(t *testing.T) {
	sg := &graph.Subgraph{ID: "S", Label: "S"}
	l := layout.NewGridLayout()
	l.SubgraphBounds = []layout.SubgraphBounds{
		{Subgraph: sg, X: 1, Y: 1, Width: 8, Height: 5},
		// Degenerate bounds are skipped rather than drawn as a negative box.
		{Subgraph: &graph.Subgraph{ID: "zero"}, X: 0, Y: 0, Width: 0, Height: 0},
	}

	c := NewCanvas(20, 12)
	drawSubgraphBorders(c, l, UNICODE)

	out := c.ToString()
	if !strings.Contains(out, string(UNICODE.SGTopLeft)) {
		t.Errorf("expected a subgraph corner in the output:\n%s", out)
	}
	// The interior carries the fill layer so a themed render colors the region.
	if got := c.GetFill(3, 3); got != "subgraph_fill" {
		t.Errorf("expected interior fill subgraph_fill, got %q", got)
	}
}

func TestDrawSubgraphBordersClipsNegativeOrigin(t *testing.T) {
	// A subgraph shifted off the top-left must be clamped, not written at a
	// negative index.
	l := layout.NewGridLayout()
	l.SubgraphBounds = []layout.SubgraphBounds{
		{Subgraph: &graph.Subgraph{ID: "S"}, X: -5, Y: -5, Width: 10, Height: 8},
	}
	c := NewCanvas(20, 12)
	drawSubgraphBorders(c, l, UNICODE)
	if c.Get(0, 0) == ' ' {
		t.Error("expected the clamped border to start at (0,0)")
	}
}

func TestDrawSubgraphLabelsFallBackToID(t *testing.T) {
	l := layout.NewGridLayout()
	l.SubgraphBounds = []layout.SubgraphBounds{
		{Subgraph: &graph.Subgraph{ID: "groupid"}, X: 0, Y: 0, Width: 16, Height: 5},
	}
	c := NewCanvas(24, 10)
	drawSubgraphLabels(c, l, UNICODE)
	// An unlabelled subgraph shows its ID so the group is still identifiable.
	if !strings.Contains(c.ToString(), "groupid") {
		t.Errorf("expected the ID used as the label:\n%s", c.ToString())
	}
}

func TestDrawSubgraphLabelsSkipsDegenerateBounds(t *testing.T) {
	l := layout.NewGridLayout()
	l.SubgraphBounds = []layout.SubgraphBounds{
		{Subgraph: &graph.Subgraph{ID: "zero", Label: "zero"}, X: 0, Y: 0, Width: 0, Height: 0},
	}
	c := NewCanvas(10, 5)
	drawSubgraphLabels(c, l, UNICODE)
	if strings.Contains(c.ToString(), "zero") {
		t.Error("expected a zero-size subgraph to draw no label")
	}
}

func TestGetCornerChar(t *testing.T) {
	// A right-then-down bend and a right-then-up bend must not share a glyph.
	down := getCornerChar(0, 0, 1, 0, 1, 1, UNICODE, false)
	up := getCornerChar(0, 0, 1, 0, 1, -1, UNICODE, false)
	if down == up {
		t.Errorf("expected distinct corners for opposite bends, both %q", down)
	}
	if down != UNICODE.CornerTopRight {
		t.Errorf("expected the top-right corner, got %q", down)
	}
	// Rounded bends use the rounded glyph set.
	rounded := getCornerChar(0, 0, 1, 0, 1, 1, UNICODE, true)
	if rounded != UNICODE.RoundTopRight {
		t.Errorf("expected the rounded top-right corner, got %q", rounded)
	}
}
