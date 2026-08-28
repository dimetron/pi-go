package renderer

import (
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

// drawLabel centers multi-line text inside a shape region.
// Lines are split on "\n" or the literal two-character sequence "\\n".
func drawLabel(c *Canvas, x, y, width, height int, label string, style string) {
	labelStyle := ""
	if style != "" {
		labelStyle = "label"
	}

	var lines []string
	if strings.Contains(label, "\n") {
		lines = strings.Split(label, "\n")
	} else if strings.Contains(label, `\n`) {
		lines = strings.Split(label, `\n`)
	} else {
		lines = []string{label}
	}

	startRow := y + (height-len(lines))/2
	for i, line := range lines {
		row := startRow + i
		col := x + (width-len(line))/2
		if row >= 0 && row < c.Height {
			c.PutText(row, col, line, labelStyle)
		}
	}
}

// fillInterior sets the style on all interior cells of a box (for background-color themes).
func fillInterior(c *Canvas, x, y, width, height int, style string) {
	for row := y + 1; row < y+height-1; row++ {
		for col := x + 1; col < x+width-1; col++ {
			c.SetStyle(row, col, style)
		}
	}
}

// isUnicode returns true if the charset is using Unicode box-drawing characters.
func isUnicode(cs CharSet) bool {
	return cs.Horizontal == '─'
}

// drawBox draws a standard rectangular border with the given corner runes.
func drawBox(c *Canvas, x, y, width, height int, tl, tr, bl, br rune, cs CharSet, style string) {
	// Top border
	c.Put(y, x, tl, true, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, tr, true, style)

	// Bottom border
	c.Put(y+height-1, x, bl, true, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, br, true, style)

	// Side borders and interior fill (for background-color themes)
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
		// Fill interior spaces with the style so background colors render
		for col := x + 1; col < x+width-1; col++ {
			c.SetStyle(row, col, style)
		}
	}
}

// shapeIndicator places a small shape-type symbol inside the upper-left corner.
func shapeIndicator(c *Canvas, x, y int, indicator rune, style string) {
	c.Put(y+1, x+1, indicator, false, style)
}

// DrawRectangle draws a standard box with corners, horizontal/vertical borders,
// and a centered label.
func DrawRectangle(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	drawBox(c, x, y, width, height, cs.TopLeft, cs.TopRight, cs.BottomLeft, cs.BottomRight, cs, style)
	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawRounded draws a box with rounded corners and a centered label.
func DrawRounded(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	drawBox(c, x, y, width, height, cs.RoundTopLeft, cs.RoundTopRight, cs.RoundBottomLeft, cs.RoundBottomRight, cs, style)
	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x, y, cs.IndRounded, style) // rounded
}

// DrawStadium draws a stadium shape: rounded top/bottom with parentheses on sides.
func DrawStadium(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top border
	c.Put(y, x, cs.RoundTopLeft, true, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, cs.RoundTopRight, true, style)

	// Bottom border
	c.Put(y+height-1, x, cs.RoundBottomLeft, true, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, cs.RoundBottomRight, true, style)

	// Side borders with parentheses
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, '(', false, style)
		c.Put(row, x+width-1, ')', false, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x, y, cs.IndStadium, style) // stadium
}

// DrawSubroutine draws a rectangle with inner vertical lines at x+1 and x+width-2.
func DrawSubroutine(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	drawBox(c, x, y, width, height, cs.TopLeft, cs.TopRight, cs.BottomLeft, cs.BottomRight, cs, style)

	// Inner vertical lines
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x+1, cs.Vertical, true, style)
		c.Put(row, x+width-2, cs.Vertical, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x, y+1, cs.IndSubroutine, style) // subroutine (offset since inner borders at x+1)
}

// DrawDiamond draws a diamond shape with chamfered /\ corners.
func DrawDiamond(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	chamferTL := '⟋'
	chamferTR := '⟍'
	chamferBL := '⟍'
	chamferBR := '⟋'
	if !isUnicode(cs) {
		chamferTL = '/'
		chamferTR = '\\'
		chamferBL = '\\'
		chamferBR = '/'
	}

	// Top row: /──────\
	c.Put(y, x, chamferTL, false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, chamferTR, false, style)

	// Side borders
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	// Bottom row: \──────/
	c.Put(y+height-1, x, chamferBL, false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, chamferBR, false, style)

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x, y, cs.IndDiamond, style) // diamond
}

// DrawHexagon draws a hexagon shape with / \ top corners and \ / bottom corners.
func DrawHexagon(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top border
	c.Put(y, x, '/', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, '\\', false, style)

	// Bottom border
	c.Put(y+height-1, x, '\\', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, '/', false, style)

	// Side borders
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x, y, cs.IndHexagon, style) // hexagon
}

// DrawCircle draws a rounded box with circle markers at the top and bottom center.
func DrawCircle(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	cx := x + width/2
	var marker rune
	if isUnicode(cs) {
		marker = '◯'
	} else {
		marker = 'O'
	}

	// Draw rounded box
	drawBox(c, x, y, width, height, cs.RoundTopLeft, cs.RoundTopRight, cs.RoundBottomLeft, cs.RoundBottomRight, cs, style)

	// Place markers at top/bottom center
	c.Put(y, cx, marker, false, style)
	c.Put(y+height-1, cx, marker, false, style)

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x, y, cs.IndCircle, style) // circle
}

// DrawDoubleCircle draws a rounded box with an inner border.
func DrawDoubleCircle(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Outer rounded box
	drawBox(c, x, y, width, height, cs.RoundTopLeft, cs.RoundTopRight, cs.RoundBottomLeft, cs.RoundBottomRight, cs, style)

	// Inner border (inset by 1)
	if width > 2 && height > 2 {
		drawBox(c, x+1, y+1, width-2, height-2, cs.RoundTopLeft, cs.RoundTopRight, cs.RoundBottomLeft, cs.RoundBottomRight, cs, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
	shapeIndicator(c, x+1, y+1, cs.IndDoubleCircle, style) // double circle (offset for inner border)
}

// DrawAsymmetric draws a flag shape: > on the left side, straight right side.
func DrawAsymmetric(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	cy := y + height/2

	// Left side: \ above center, > at center, / below center
	for row := y; row < y+height; row++ {
		if row < cy {
			c.Put(row, x, '\\', false, style)
		} else if row == cy {
			c.Put(row, x, '>', false, style)
		} else {
			c.Put(row, x, '/', false, style)
		}
	}

	// Right side is straight
	c.Put(y, x+width-1, cs.TopRight, true, style)
	c.Put(y+height-1, x+width-1, cs.BottomRight, true, style)
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	// Top and bottom borders
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawCylinder draws a cylinder shape with top ellipse (two horizontal lines),
// body, and bottom ellipse.
func DrawCylinder(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top ellipse: two horizontal lines
	// First line of top
	c.Put(y, x, cs.RoundTopLeft, true, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, cs.RoundTopRight, true, style)

	// Second line of top ellipse
	if height > 2 {
		c.Put(y+1, x, cs.RoundBottomLeft, true, style)
		for col := x + 1; col < x+width-1; col++ {
			c.Put(y+1, col, cs.Horizontal, true, style)
		}
		c.Put(y+1, x+width-1, cs.RoundBottomRight, true, style)
	}

	// Body (side borders)
	for row := y + 2; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	// Bottom ellipse
	c.Put(y+height-1, x, cs.RoundBottomLeft, true, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, cs.RoundBottomRight, true, style)

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawTrapezoid draws a trapezoid: / top-left, \ top-right, \ bottom-left, / bottom-right.
func DrawTrapezoid(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top border
	c.Put(y, x, '/', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, '\\', false, style)

	// Bottom border
	c.Put(y+height-1, x, '\\', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, '/', false, style)

	// Side borders
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawTrapezoidAlt draws an inverted trapezoid: \ top-left, / top-right,
// / bottom-left, \ bottom-right.
func DrawTrapezoidAlt(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top border
	c.Put(y, x, '\\', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, '/', false, style)

	// Bottom border
	c.Put(y+height-1, x, '/', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, '\\', false, style)

	// Side borders
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawParallelogram draws a parallelogram with / on all four corners.
func DrawParallelogram(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top border
	c.Put(y, x, '/', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, '/', false, style)

	// Bottom border
	c.Put(y+height-1, x, '/', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, '/', false, style)

	// Side borders
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawParallelogramAlt draws a parallelogram with \ on all four corners.
func DrawParallelogramAlt(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	// Top border
	c.Put(y, x, '\\', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y, col, cs.Horizontal, true, style)
	}
	c.Put(y, x+width-1, '\\', false, style)

	// Bottom border
	c.Put(y+height-1, x, '\\', false, style)
	for col := x + 1; col < x+width-1; col++ {
		c.Put(y+height-1, col, cs.Horizontal, true, style)
	}
	c.Put(y+height-1, x+width-1, '\\', false, style)

	// Side borders
	for row := y + 1; row < y+height-1; row++ {
		c.Put(row, x, cs.Vertical, true, style)
		c.Put(row, x+width-1, cs.Vertical, true, style)
	}

	fillInterior(c, x, y, width, height, style)
	drawLabel(c, x, y, width, height, label, style)
}

// DrawStartState draws a filled circle marker at the center of the region.
func DrawStartState(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	cx := x + width/2
	cy := y + height/2
	var marker rune
	if isUnicode(cs) {
		marker = '●'
	} else {
		marker = '*'
	}
	c.Put(cy, cx, marker, false, style)
}

// DrawEndState draws a bullseye marker at the center of the region.
func DrawEndState(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	cx := x + width/2
	cy := y + height/2
	var marker rune
	if isUnicode(cs) {
		marker = '◉'
	} else {
		marker = '@'
	}
	c.Put(cy, cx, marker, false, style)
}

// DrawForkJoin fills the entire area with thick horizontal lines.
func DrawForkJoin(c *Canvas, x, y, width, height int, label string, cs CharSet, style string) {
	var ch rune
	if isUnicode(cs) {
		ch = '━'
	} else {
		ch = '='
	}
	for row := y; row < y+height; row++ {
		for col := x; col < x+width; col++ {
			c.Put(row, col, ch, false, style)
		}
	}
}

// ShapeRenderers maps each NodeShape constant to its renderer function.
var ShapeRenderers = map[graph.NodeShape]func(*Canvas, int, int, int, int, string, CharSet, string){
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
