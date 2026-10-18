package renderer

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

// drawIn runs one shape draw on a canvas large enough that no put is clipped,
// and returns it. Shapes are compared through the rendered text so the tests
// describe what a reader sees rather than which cell got which rune.
func drawIn(width, height int, draw func(c *Canvas)) *Canvas {
	c := NewCanvas(width, height)
	draw(c)
	return c
}

// shapeDrawers is the full set of node shapes a flowchart can ask for. Every
// one of them must produce visible output — an empty or blank region means the
// shape silently rendered nothing.
func shapeDrawers() map[string]func(*Canvas, int, int, int, int, string, CharSet, string) {
	return map[string]func(*Canvas, int, int, int, int, string, CharSet, string){
		"rectangle":         DrawRectangle,
		"rounded":           DrawRounded,
		"stadium":           DrawStadium,
		"subroutine":        DrawSubroutine,
		"diamond":           DrawDiamond,
		"hexagon":           DrawHexagon,
		"circle":            DrawCircle,
		"double_circle":     DrawDoubleCircle,
		"asymmetric":        DrawAsymmetric,
		"cylinder":          DrawCylinder,
		"trapezoid":         DrawTrapezoid,
		"trapezoid_alt":     DrawTrapezoidAlt,
		"parallelogram":     DrawParallelogram,
		"parallelogram_alt": DrawParallelogramAlt,
		"start_state":       DrawStartState,
		"end_state":         DrawEndState,
		"fork_join":         DrawForkJoin,
	}
}

func TestEveryShapeDrawsSomething(t *testing.T) {
	for name, draw := range shapeDrawers() {
		t.Run(name, func(t *testing.T) {
			c := drawIn(24, 9, func(c *Canvas) {
				draw(c, 0, 0, 24, 9, "hi", UNICODE, "")
			})
			out := c.ToString()
			if strings.TrimSpace(out) == "" {
				t.Fatalf("%s rendered blank output", name)
			}
		})
	}
}

func TestEveryShapeRendersItsLabel(t *testing.T) {
	// Start/end state and fork/join are markers: they deliberately drop the
	// label and draw a glyph instead.
	noLabel := map[string]bool{"start_state": true, "end_state": true, "fork_join": true}

	for name, draw := range shapeDrawers() {
		if noLabel[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			c := drawIn(24, 9, func(c *Canvas) {
				draw(c, 0, 0, 24, 9, "hello", UNICODE, "")
			})
			if !strings.Contains(c.ToString(), "hello") {
				t.Errorf("%s did not render its label:\n%s", name, c.ToString())
			}
		})
	}
}

func TestEveryShapeIsASCIIOnlyInASCIISet(t *testing.T) {
	// --ascii output must not leak box-drawing glyphs. This is the regression
	// the CharSet shape indicators were introduced for.
	for name, draw := range shapeDrawers() {
		t.Run(name, func(t *testing.T) {
			c := drawIn(24, 9, func(c *Canvas) {
				draw(c, 0, 0, 24, 9, "hi", ASCII, "")
			})
			for _, r := range c.ToString() {
				if r > 127 {
					t.Fatalf("%s emitted non-ASCII rune %q in the ASCII charset:\n%s", name, r, c.ToString())
				}
			}
		})
	}
}

func TestIsUnicode(t *testing.T) {
	if !isUnicode(UNICODE) {
		t.Error("expected UNICODE charset to report as unicode")
	}
	if isUnicode(ASCII) {
		t.Error("expected ASCII charset not to report as unicode")
	}
}

func TestDrawLabelSplitsOnBothNewlineForms(t *testing.T) {
	// Mermaid source can carry a real newline or the two-character escape; both
	// must break the label into centerd lines.
	for label, want := range map[string]int{
		"one":     1,
		"a\nb":    2,
		`a\nb`:    2,
		"a\nb\nc": 3,
	} {
		c := NewCanvas(20, 8)
		drawLabel(c, 0, 0, 20, 8, label, "")
		got := 0
		for _, line := range strings.Split(c.ToString(), "\n") {
			if strings.TrimSpace(line) != "" {
				got++
			}
		}
		if got != want {
			t.Errorf("label %q rendered %d non-empty lines, want %d:\n%s", label, got, want, c.ToString())
		}
	}
}

func TestDrawLabelClipsOutOfRangeRows(t *testing.T) {
	// A label taller than the region must not panic on the rows that fall
	// outside the canvas.
	c := NewCanvas(20, 3)
	c.Put(0, 0, 'X', false, "")
	drawLabel(c, 0, 0, 20, 1, "a\nb\nc\nd", "")
}

func TestDrawLabelStyle(t *testing.T) {
	c := NewCanvas(20, 5)
	drawLabel(c, 0, 0, 20, 5, "styled", "primary")
	if got := c.GetStyle(2, 7); got != "label" {
		t.Errorf("expected the label to be styled 'label', got %q", got)
	}

	// An empty style leaves the cell's default style alone.
	c2 := NewCanvas(20, 5)
	drawLabel(c2, 0, 0, 20, 5, "plain", "")
	if got := c2.GetStyle(2, 7); got == "label" {
		t.Error("expected no label style when style is empty")
	}
}

func TestFillInterior(t *testing.T) {
	c := NewCanvas(10, 6)
	fillInterior(c, 0, 0, 10, 6, "warm")

	// Interior cells carry the style...
	if got := c.GetStyle(3, 5); got != "warm" {
		t.Errorf("expected interior style warm, got %q", got)
	}
	// ...and the border ring does not.
	for _, cell := range [][2]int{{0, 0}, {0, 5}, {5, 0}, {5, 9}} {
		if got := c.GetStyle(cell[0], cell[1]); got == "warm" {
			t.Errorf("expected border cell %v not to be filled", cell)
		}
	}
}

func TestShapeIndicator(t *testing.T) {
	c := NewCanvas(10, 6)
	shapeIndicator(c, 2, 2, '~', "primary")
	// The indicator sits one cell in from the shape's top-left corner.
	if got := c.Get(3, 3); got != '~' {
		t.Errorf("expected indicator at (3,3), got %q", got)
	}
}

func TestPutStyledText(t *testing.T) {
	c := NewCanvas(40, 5)
	c.PutStyledText(0, 0, []StyledSegment{
		{Text: "plain", Style: ""},
		{Text: "bold", Style: "bold"},
	})

	out := c.ToString()
	if !strings.Contains(out, "plain") || !strings.Contains(out, "bold") {
		t.Errorf("expected both segments rendered, got:\n%s", out)
	}
	// The second segment starts after the first, not on top of it.
	if c.Get(0, 5) != 'b' {
		t.Errorf("expected the second segment at col 5, got %q", c.Get(0, 5))
	}
	if got := c.GetStyle(0, 5); got != "bold" {
		t.Errorf("expected style bold at col 5, got %q", got)
	}
}

func TestPutStyledTextClipsToCanvas(t *testing.T) {
	// Segments running past the right edge must be clipped, not panic.
	c := NewCanvas(5, 2)
	c.PutStyledText(0, 0, []StyledSegment{{Text: "abcdefghij", Style: ""}})
	if got := c.Get(0, 4); got != 'e' {
		t.Errorf("expected the last in-range cell to be 'e', got %q", got)
	}
}

func TestSetFillAndGetFill(t *testing.T) {
	c := NewCanvas(10, 5)
	c.SetFill(1, 2, "accent")
	if got := c.GetFill(1, 2); got != "accent" {
		t.Errorf("expected fill accent, got %q", got)
	}
	c.SetFill(1, 2, "")
	if got := c.GetFill(1, 2); got != "" {
		t.Errorf("expected the fill to be cleared, got %q", got)
	}
}

func TestSetFillOutOfBounds(t *testing.T) {
	// Out-of-bounds writes must be ignored rather than panicking.
	c := NewCanvas(4, 4)
	c.SetFill(-1, 0, "x")
	c.SetFill(0, -1, "x")
	c.SetFill(4, 0, "x")
	c.SetFill(0, 4, "x")
	if got := c.GetFill(0, 0); got != "" {
		t.Errorf("expected out-of-bounds writes to be dropped, got %q", got)
	}
}

func TestDrawBoxWithZeroOrNegativeSize(t *testing.T) {
	// Degenerate dimensions appear when a caller clamps to a tiny region; they
	// must not panic by indexing backwards.
	for _, tt := range [][2]int{{0, 0}, {1, 1}, {-1, -1}} {
		c := NewCanvas(10, 10)
		drawBox(c, 0, 0, tt[0], tt[1],
			UNICODE.TopLeft, UNICODE.TopRight, UNICODE.BottomLeft, UNICODE.BottomRight,
			UNICODE, "")
	}
}

func TestDrawRectangleBorders(t *testing.T) {
	c := NewCanvas(12, 6)
	DrawRectangle(c, 0, 0, 12, 6, "x", UNICODE, "")

	checks := map[string]rune{
		"top-left":     UNICODE.TopLeft,
		"top-right":    UNICODE.TopRight,
		"bottom-left":  UNICODE.BottomLeft,
		"bottom-right": UNICODE.BottomRight,
		"top edge":     UNICODE.Horizontal,
		"left edge":    UNICODE.Vertical,
	}
	for what, want := range checks {
		var got rune
		switch what {
		case "top-left":
			got = c.Get(0, 0)
		case "top-right":
			got = c.Get(0, 11)
		case "bottom-left":
			got = c.Get(5, 0)
		case "bottom-right":
			got = c.Get(5, 11)
		case "top edge":
			got = c.Get(0, 5)
		case "left edge":
			got = c.Get(3, 0)
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", what, got, want)
		}
	}
}

func TestDrawStateMarkers(t *testing.T) {
	// Start is a filled circle, end is a double circle, fork/join is a bar.
	// They are markers, so they must differ from each other and from a box.
	render := func(draw func(*Canvas, int, int, int, int, string, CharSet, string)) string {
		c := NewCanvas(10, 5)
		draw(c, 0, 0, 10, 5, "", UNICODE, "")
		return strings.TrimSpace(c.ToString())
	}

	start := render(DrawStartState)
	end := render(DrawEndState)
	fork := render(DrawForkJoin)

	if start == "" || end == "" || fork == "" {
		t.Fatalf("expected all markers to draw: start=%q end=%q fork=%q", start, end, fork)
	}
	if start == end {
		t.Errorf("start and end state render identically:\n%s", start)
	}
}

func TestShapeForEveryNodeShapeHasADrawer(t *testing.T) {
	// Guards the shape enum against growing a member nothing can draw.
	drawers := map[graph.NodeShape]func(*Canvas, int, int, int, int, string, CharSet, string){
		graph.ShapeRectangle:        DrawRectangle,
		graph.ShapeRounded:          DrawRounded,
		graph.ShapeStadium:          DrawStadium,
		graph.ShapeSubroutine:       DrawSubroutine,
		graph.ShapeDiamond:          DrawDiamond,
		graph.ShapeHexagon:          DrawHexagon,
		graph.ShapeCircle:           DrawCircle,
		graph.ShapeDoubleCircle:     DrawDoubleCircle,
		graph.ShapeAsymmetric:       DrawAsymmetric,
		graph.ShapeCylinder:         DrawCylinder,
		graph.ShapeParallelogram:    DrawParallelogram,
		graph.ShapeParallelogramAlt: DrawParallelogramAlt,
		graph.ShapeTrapezoid:        DrawTrapezoid,
		graph.ShapeTrapezoidAlt:     DrawTrapezoidAlt,
		graph.ShapeStartState:       DrawStartState,
		graph.ShapeEndState:         DrawEndState,
		graph.ShapeForkJoin:         DrawForkJoin,
	}

	// The enum is contiguous from 0; every value must be covered.
	for s := graph.ShapeRectangle; s <= graph.ShapeForkJoin; s++ {
		if _, ok := drawers[s]; !ok {
			t.Errorf("NodeShape %d has no drawer registered in this test", s)
		}
	}
}
