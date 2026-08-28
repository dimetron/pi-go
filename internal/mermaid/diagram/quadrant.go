package diagram

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

type quadrantPoint struct {
	label string
	x, y  float64
}

type quadrantData struct {
	title       string
	xAxisLeft   string
	xAxisRight  string
	yAxisBottom string
	yAxisTop    string
	quadrant1   string // top-right
	quadrant2   string // top-left
	quadrant3   string // bottom-left
	quadrant4   string // bottom-right
	points      []quadrantPoint
}

var (
	reQuadrantHeader = regexp.MustCompile(`(?i)^\s*quadrantChart\s*$`)
	reQuadrantTitle  = regexp.MustCompile(`(?i)^\s*title\s+(.+)$`)
	reQuadrantXAxis  = regexp.MustCompile(`(?i)^\s*x-axis\s+"?([^"]*?)"?\s*-->\s*"?([^"]*?)"?\s*$`)
	reQuadrantYAxis  = regexp.MustCompile(`(?i)^\s*y-axis\s+"?([^"]*?)"?\s*-->\s*"?([^"]*?)"?\s*$`)
	reQuadrantQ1     = regexp.MustCompile(`(?i)^\s*quadrant-1\s+(.+)$`)
	reQuadrantQ2     = regexp.MustCompile(`(?i)^\s*quadrant-2\s+(.+)$`)
	reQuadrantQ3     = regexp.MustCompile(`(?i)^\s*quadrant-3\s+(.+)$`)
	reQuadrantQ4     = regexp.MustCompile(`(?i)^\s*quadrant-4\s+(.+)$`)
	reQuadrantPoint  = regexp.MustCompile(`^\s*(.+?):\s*\[\s*([0-9.]+)\s*,\s*([0-9.]+)\s*\]\s*$`)
)

func parseQuadrant(source string) *quadrantData {
	qd := &quadrantData{}
	lines := strings.Split(source, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if reQuadrantHeader.MatchString(trimmed) {
			continue
		}
		if m := reQuadrantTitle.FindStringSubmatch(trimmed); m != nil {
			qd.title = strings.TrimSpace(m[1])
			continue
		}
		if m := reQuadrantXAxis.FindStringSubmatch(trimmed); m != nil {
			qd.xAxisLeft = strings.TrimSpace(m[1])
			qd.xAxisRight = strings.TrimSpace(m[2])
			continue
		}
		if m := reQuadrantYAxis.FindStringSubmatch(trimmed); m != nil {
			qd.yAxisBottom = strings.TrimSpace(m[1])
			qd.yAxisTop = strings.TrimSpace(m[2])
			continue
		}
		if m := reQuadrantQ1.FindStringSubmatch(trimmed); m != nil {
			qd.quadrant1 = strings.TrimSpace(m[1])
			continue
		}
		if m := reQuadrantQ2.FindStringSubmatch(trimmed); m != nil {
			qd.quadrant2 = strings.TrimSpace(m[1])
			continue
		}
		if m := reQuadrantQ3.FindStringSubmatch(trimmed); m != nil {
			qd.quadrant3 = strings.TrimSpace(m[1])
			continue
		}
		if m := reQuadrantQ4.FindStringSubmatch(trimmed); m != nil {
			qd.quadrant4 = strings.TrimSpace(m[1])
			continue
		}
		if m := reQuadrantPoint.FindStringSubmatch(trimmed); m != nil {
			x, _ := strconv.ParseFloat(m[2], 64)
			y, _ := strconv.ParseFloat(m[3], 64)
			qd.points = append(qd.points, quadrantPoint{
				label: strings.TrimSpace(m[1]),
				x:     x,
				y:     y,
			})
			continue
		}
	}

	return qd
}

// RenderQuadrantChart parses and renders a Mermaid quadrant chart.
func RenderQuadrantChart(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	qd := parseQuadrant(source)

	plotH := 20
	yLabelW := 0
	if qd.yAxisBottom != "" || qd.yAxisTop != "" {
		yLabelW = max(len(qd.yAxisBottom), len(qd.yAxisTop)) + 2
	}
	// Minimum y-label width for axis label
	if yLabelW < 2 {
		yLabelW = 2
	}

	plotW := scaleWidth(yLabelW+4, 30, maxScaledWidth)

	titleRows := 0
	if qd.title != "" {
		titleRows = 2
	}

	canvasWidth := yLabelW + plotW + 2
	canvasHeight := titleRows + plotH + 4 // +4 for x-axis labels and axis line

	c := renderer.NewCanvas(canvasWidth, canvasHeight)

	// Wallpaper: base background behind entire diagram
	if theme != nil && theme.HasDepthColors() {
		for r := 0; r < canvasHeight; r++ {
			for col := 0; col < canvasWidth; col++ {
				c.SetFill(r, col, "subgraph_fill")
			}
		}
	}

	// Title
	if qd.title != "" {
		titleCol := (canvasWidth - len(qd.title)) / 2
		c.PutText(0, titleCol, qd.title, "bold_label")
	}

	plotStartX := yLabelW
	plotStartY := titleRows
	plotEndY := plotStartY + plotH - 1

	vLine := '│'
	hLine := '─'
	cross := '┼'
	dot := '●'
	if useASCII {
		vLine = '|'
		hLine = '-'
		cross = '+'
		dot = '*'
	}

	// Y axis
	for r := plotStartY; r <= plotEndY; r++ {
		c.Put(r, plotStartX-1, vLine, false, "edge")
	}

	// X axis (at middle)
	midY := plotStartY + plotH/2
	for col := plotStartX; col < plotStartX+plotW; col++ {
		c.Put(midY, col, hLine, false, "edge")
	}

	// Center cross
	midX := plotStartX + plotW/2
	c.Put(midY, midX, cross, false, "edge")

	// Center lines are dashed. Take the glyphs from the charset rather than
	// hardcoding the Unicode ones, so --ascii output stays 7-bit.
	dashV, dashH := '┆', '┄'
	if useASCII {
		dashV, dashH = ':', '.'
	}

	// Vertical center line
	for r := plotStartY; r <= plotEndY; r++ {
		if r != midY {
			c.Put(r, midX, dashV, false, "edge")
		}
	}

	// Horizontal center line (dashed)
	for col := plotStartX; col < plotStartX+plotW; col++ {
		if col != midX && c.Get(midY, col) == hLine {
			c.Put(midY, col, dashH, false, "edge")
		}
	}

	// Bottom x-axis
	axisRow := plotEndY + 1
	for col := plotStartX; col < plotStartX+plotW; col++ {
		c.Put(axisRow, col, hLine, false, "edge")
	}

	// Quadrant labels and fills
	useRegion := theme != nil && theme.HasDepthColors()
	qw := plotW / 2
	qh := plotH / 2

	// Quadrant regions: Q2=top-left(0), Q1=top-right(1), Q3=bottom-left(2), Q4=bottom-right(3)
	type quadRegion struct {
		label      string
		startR     int
		endR       int
		startC     int
		endC       int
		sectionIdx int
	}
	regions := []quadRegion{
		{qd.quadrant2, plotStartY, plotStartY + qh - 1, plotStartX, plotStartX + qw - 1, 0},
		{qd.quadrant1, plotStartY, plotStartY + qh - 1, plotStartX + qw, plotStartX + plotW - 1, 1},
		{qd.quadrant3, plotStartY + qh, plotEndY, plotStartX, plotStartX + qw - 1, 2},
		{qd.quadrant4, plotStartY + qh, plotEndY, plotStartX + qw, plotStartX + plotW - 1, 3},
	}

	for _, reg := range regions {
		if useRegion {
			fillStyle := "_ansi:" + theme.RegionStyle(reg.sectionIdx, 0)
			for r := reg.startR; r <= reg.endR; r++ {
				for col := reg.startC; col <= reg.endC; col++ {
					c.SetFill(r, col, fillStyle)
				}
			}
		}

		labelStyle := "subgraph_label"
		if useRegion {
			labelStyle = "_ansi:" + theme.RegionLabelStyle(reg.sectionIdx, 0)
		}
		if reg.label != "" {
			labelR := reg.startR + (reg.endR-reg.startR)/2
			labelC := reg.startC + (reg.endC-reg.startC-len(reg.label))/2
			c.PutText(labelR, labelC, reg.label, labelStyle)
		}
	}

	// Axis labels
	if qd.xAxisLeft != "" {
		c.PutText(axisRow+1, plotStartX, qd.xAxisLeft, "label")
	}
	if qd.xAxisRight != "" {
		c.PutText(axisRow+1, plotStartX+plotW-len(qd.xAxisRight), qd.xAxisRight, "label")
	}
	if qd.yAxisTop != "" {
		row := plotStartY
		col := plotStartX - len(qd.yAxisTop) - 1
		if col < 0 {
			col = 0
		}
		c.PutText(row, col, qd.yAxisTop, "label")
	}
	if qd.yAxisBottom != "" {
		row := plotEndY
		col := plotStartX - len(qd.yAxisBottom) - 1
		if col < 0 {
			col = 0
		}
		c.PutText(row, col, qd.yAxisBottom, "label")
	}

	// Plot points — draw dots first, then labels (so labels don't get overwritten)
	type plotted struct {
		px, py int
		label  string
	}
	var points []plotted
	for _, p := range qd.points {
		px := plotStartX + int(p.x*float64(plotW-1))
		py := plotEndY - int(p.y*float64(plotH-1))
		if px >= plotStartX && px < plotStartX+plotW && py >= plotStartY && py <= plotEndY {
			c.Put(py, px, dot, false, "arrow")
			points = append(points, plotted{px, py, p.label})
		}
	}
	for _, p := range points {
		// Label with solid connector: ●─Label (right) or Label─● (left)
		// Clear cells first so spaces in labels overwrite underlying text
		rightSpace := canvasWidth - p.px - 2
		if rightSpace >= len(p.label)+1 {
			for i := 0; i < len(p.label)+1; i++ {
				c.ClearCell(p.py, p.px+1+i)
			}
			c.Put(p.py, p.px+1, hLine, false, "arrow")
			c.PutText(p.py, p.px+2, p.label, "label")
		} else {
			labelStart := p.px - len(p.label) - 2
			if labelStart < plotStartX {
				labelStart = plotStartX
			}
			for i := 0; i < len(p.label)+1; i++ {
				c.ClearCell(p.py, labelStart+i)
			}
			c.PutText(p.py, labelStart, p.label, "label")
			c.Put(p.py, p.px-1, hLine, false, "arrow")
		}
	}

	return c
}
