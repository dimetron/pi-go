package diagram

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// blockDrawRoutedLine and blockDrawLink are the only code that draws a block
// diagram's edges. They read as 0% in this package on their own, so a wrong
// corner glyph or a link that exits the wrong side would only be caught by a
// human looking at a rendered diagram.
//
// The two functions also have to agree: blockDrawLink chooses a route and then
// hands its endpoints to blockDrawRoutedLine, so these tests assert the glyphs
// the route produces rather than the internal coordinates.
func TestBlockDrawRoutedLine(t *testing.T) {
	cs := renderer.UNICODE

	tests := []struct {
		name       string
		r1, c1     int
		r2, c2     int
		useASCII   bool
		wantGlyphs []rune
		wantAbsent []rune
	}{
		{
			name: "same column draws a straight vertical",
			r1:   1, c1: 3, r2: 5, c2: 3,
			wantGlyphs: []rune{cs.LineVertical},
			wantAbsent: []rune{'┌', '└'},
		},
		{
			name: "same row draws a straight horizontal",
			r1:   2, c1: 1, r2: 2, c2: 6,
			wantGlyphs: []rune{cs.LineHorizontal},
			wantAbsent: []rune{'┌', '└'},
		},
		{
			name: "different row and column draws a Z route with corners",
			r1:   1, c1: 1, r2: 6, c2: 7,
			wantGlyphs: []rune{cs.LineVertical, cs.LineHorizontal},
		},
		{
			// In ASCII mode the corners are replaced by a junction of the h/v
			// characters the caller passed. The flag's effect that holds here
			// is that none of the four box-drawing corner glyphs is drawn.
			name: "ASCII mode draws no box-drawing corners",
			r1:   1, c1: 1, r2: 6, c2: 7,
			useASCII:   true,
			wantGlyphs: []rune{cs.LineVertical, cs.LineHorizontal},
			wantAbsent: []rune{'┌', '└', '┐', '┘'},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := renderer.NewCanvas(12, 10)
			blockDrawRoutedLine(c, tc.r1, tc.c1, tc.r2, tc.c2, cs.LineHorizontal, cs.LineVertical, tc.useASCII, "edge")
			out := c.ToString()

			for _, g := range tc.wantGlyphs {
				if !strings.ContainsRune(out, g) {
					t.Errorf("output has no %q\n---\n%s\n---", g, out)
				}
			}
			for _, g := range tc.wantAbsent {
				if strings.ContainsRune(out, g) {
					t.Errorf("output unexpectedly has %q\n---\n%s\n---", g, out)
				}
			}
		})
	}
}

// The Z route leaves the source horizontally at the bend row and then turns
// down to the target, so the first corner depends on which side the target is
// on. Both horizontal directions are pinned because the L-route in
// blockDrawLink uses c2 < c1 to choose its corner and a swapped branch would
// still draw a plausible-looking-but-mirrored diagram.
func TestBlockDrawRoutedLineCornerDirection(t *testing.T) {
	cs := renderer.UNICODE

	tests := []struct {
		name         string
		r1, c1       int
		r2, c2       int
		wantCorner   rune
		wantOtherNot rune
	}{
		// Target to the right: the route comes down the source column and turns
		// right, so the bottom-left corner is used.
		{name: "target right", r1: 1, c1: 1, r2: 6, c2: 7, wantCorner: '└', wantOtherNot: '┘'},
		// Target to the left: the mirrored turn.
		{name: "target left", r1: 1, c1: 7, r2: 6, c2: 1, wantCorner: '┘', wantOtherNot: '└'},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := renderer.NewCanvas(12, 10)
			blockDrawRoutedLine(c, tc.r1, tc.c1, tc.r2, tc.c2, cs.LineHorizontal, cs.LineVertical, false, "edge")
			out := c.ToString()

			if !strings.ContainsRune(out, tc.wantCorner) {
				t.Errorf("output has no %q corner\n---\n%s\n---", tc.wantCorner, out)
			}
			if strings.ContainsRune(out, tc.wantOtherNot) {
				t.Errorf("output has the mirror corner %q\n---\n%s\n---", tc.wantOtherNot, out)
			}
		})
	}
}

func TestBlockDrawLink(t *testing.T) {
	cs := renderer.UNICODE

	// positions[n] is {column, row} — a is read as sx, sy := pos[0], pos[1], and
	// sy is then used as the row when the route is written. Getting this order
	// wrong silently swaps the axis and picks the other route branch, so the
	// layout is stated here explicitly.
	//
	//	a  b     a and b share a row and do not overlap in columns  -> horizontal
	//	c        c shares a's column and sits below it              -> vertical
	positions := map[string][2]int{"a": {1, 1}, "b": {12, 1}, "c": {1, 8}}
	sizes := map[string][2]int{"a": {3, 3}, "b": {3, 3}, "c": {3, 3}}

	// a and b are side by side at the same row, so the route is horizontal and
	// must exit the source on its right and enter the target on its left. The
	// two boxes overlap in columns, which is what the code uses to pick the
	// horizontal case.
	t.Run("side-by-side boxes route horizontally", func(t *testing.T) {
		c := renderer.NewCanvas(24, 14)
		blockDrawLink(c, blockLink{source: "a", target: "b"}, positions, sizes, cs, false)
		out := c.ToString()
		if !strings.ContainsRune(out, cs.ArrowRight) {
			t.Errorf("no right arrow for a side-by-side link\n---\n%s\n---", out)
		}
		if strings.ContainsRune(out, cs.ArrowDown) {
			t.Errorf("a side-by-side link drew a vertical arrow\n---\n%s\n---", out)
		}
	})

	// c is below a and does not overlap it in columns, so the route turns.
	t.Run("stacked boxes route vertically", func(t *testing.T) {
		c := renderer.NewCanvas(24, 14)
		blockDrawLink(c, blockLink{source: "a", target: "c"}, positions, sizes, cs, false)
		out := c.ToString()
		if !strings.ContainsRune(out, cs.ArrowDown) && !strings.ContainsRune(out, cs.ArrowRight) {
			t.Errorf("no arrow drawn between stacked boxes\n---\n%s\n---", out)
		}
	})

	t.Run("label is drawn on the link", func(t *testing.T) {
		c := renderer.NewCanvas(24, 14)
		blockDrawLink(c, blockLink{source: "a", target: "b", label: "hi"}, positions, sizes, cs, false)
		if !strings.Contains(c.ToString(), "hi") {
			t.Errorf("label missing\n---\n%s\n---", c.ToString())
		}
	})

	// A link naming a block that was never laid out must not draw: the
	// positions lookup fails and the zero value would otherwise be used as a
	// real coordinate at the canvas origin.
	t.Run("missing endpoint draws nothing", func(t *testing.T) {
		c := renderer.NewCanvas(24, 14)
		blockDrawLink(c, blockLink{source: "a", target: "nope"}, positions, sizes, cs, false)
		if out := c.ToString(); out != "" {
			t.Errorf("expected an empty canvas, got\n---\n%s\n---", out)
		}
	})
}
