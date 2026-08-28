package diagram

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

type xyChartData struct {
	title    string
	xLabels  []string
	yLabel   string
	yMin     float64
	yMax     float64
	autoY    bool
	barData  []float64
	lineData []float64
}

var (
	reXYHeader = regexp.MustCompile(`(?i)^\s*xychart-beta\s*$`)
	reXYTitle  = regexp.MustCompile(`(?i)^\s*title\s+"?([^"]*)"?\s*$`)
	reXYXAxis  = regexp.MustCompile(`(?i)^\s*x-axis\s+\[(.+)\]\s*$`)
	reXYYAxis  = regexp.MustCompile(`(?i)^\s*y-axis\s+"?([^"]*?)"?\s*([0-9.]+)\s*-->\s*([0-9.]+)\s*$`)
	reXYYLabel = regexp.MustCompile(`(?i)^\s*y-axis\s+"([^"]+)"\s*$`)
	reXYBar    = regexp.MustCompile(`(?i)^\s*bar\s+\[(.+)\]\s*$`)
	reXYLine   = regexp.MustCompile(`(?i)^\s*line\s+\[(.+)\]\s*$`)
)

func parseXYChart(source string) *xyChartData {
	xd := &xyChartData{autoY: true}
	lines := strings.Split(source, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if reXYHeader.MatchString(trimmed) {
			continue
		}
		if m := reXYTitle.FindStringSubmatch(trimmed); m != nil {
			xd.title = strings.TrimSpace(m[1])
			continue
		}
		if m := reXYXAxis.FindStringSubmatch(trimmed); m != nil {
			for _, label := range strings.Split(m[1], ",") {
				l := strings.TrimSpace(label)
				l = strings.Trim(l, "\"")
				if l != "" {
					xd.xLabels = append(xd.xLabels, l)
				}
			}
			continue
		}
		if m := reXYYAxis.FindStringSubmatch(trimmed); m != nil {
			xd.yLabel = strings.TrimSpace(m[1])
			xd.yMin, _ = strconv.ParseFloat(m[2], 64)
			xd.yMax, _ = strconv.ParseFloat(m[3], 64)
			xd.autoY = false
			continue
		}
		if m := reXYYLabel.FindStringSubmatch(trimmed); m != nil {
			xd.yLabel = strings.TrimSpace(m[1])
			continue
		}
		if m := reXYBar.FindStringSubmatch(trimmed); m != nil {
			xd.barData = parseFloatList(m[1])
			continue
		}
		if m := reXYLine.FindStringSubmatch(trimmed); m != nil {
			xd.lineData = parseFloatList(m[1])
			continue
		}
	}

	// Auto-determine y range
	if xd.autoY {
		xd.yMin = math.Inf(1)
		xd.yMax = math.Inf(-1)
		for _, v := range xd.barData {
			if v < xd.yMin {
				xd.yMin = v
			}
			if v > xd.yMax {
				xd.yMax = v
			}
		}
		for _, v := range xd.lineData {
			if v < xd.yMin {
				xd.yMin = v
			}
			if v > xd.yMax {
				xd.yMax = v
			}
		}
		if math.IsInf(xd.yMin, 1) {
			xd.yMin = 0
		}
		if math.IsInf(xd.yMax, -1) {
			xd.yMax = 100
		}
		// Round to nice bounds
		if xd.yMin > 0 {
			xd.yMin = 0
		}
		xd.yMax = math.Ceil(xd.yMax*1.1/100) * 100
	}

	return xd
}

func parseFloatList(s string) []float64 {
	var vals []float64
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err == nil {
			vals = append(vals, v)
		}
	}
	return vals
}

// RenderXYChart parses and renders a Mermaid xychart-beta diagram.
func RenderXYChart(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	xd := parseXYChart(source)

	plotH := 15

	yLabelW := len(formatNum(xd.yMax)) + 1
	if yLabelW < len(formatNum(xd.yMin))+1 {
		yLabelW = len(formatNum(xd.yMin)) + 1
	}
	if xd.yLabel != "" && len(xd.yLabel)+2 > yLabelW {
		yLabelW = len(xd.yLabel) + 2
	}

	// Scale plot width to terminal, with minimum from label count
	minPlotW := 30
	if len(xd.xLabels) > 0 {
		if lw := len(xd.xLabels) * 6; lw > minPlotW {
			minPlotW = lw
		}
	}
	plotW := scaleWidth(yLabelW+4, minPlotW, maxScaledWidth)

	titleRows := 0
	if xd.title != "" {
		titleRows = 2
	}

	canvasWidth := yLabelW + plotW + 2
	canvasHeight := titleRows + plotH + 4

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
	if xd.title != "" {
		titleCol := (canvasWidth - len(xd.title)) / 2
		c.PutText(0, titleCol, xd.title, "bold_label")
	}

	plotX := yLabelW
	plotY := titleRows
	plotBottom := plotY + plotH

	vLine := '│'
	hLine := '─'
	barCh := '▓'
	lineDot := '●'
	if useASCII {
		vLine = '|'
		hLine = '-'
		barCh = '#'
		lineDot = '*'
	}

	// Y axis
	for r := plotY; r <= plotBottom; r++ {
		c.Put(r, plotX-1, vLine, false, "edge")
	}

	// X axis
	for col := plotX; col < plotX+plotW; col++ {
		c.Put(plotBottom, col, hLine, false, "edge")
	}

	// Fill plot area background
	useRegion := theme != nil && theme.HasDepthColors()
	if useRegion {
		plotFill := "_ansi:" + theme.RegionStyle(0, 0)
		for r := plotY; r < plotBottom; r++ {
			for col := plotX; col < plotX+plotW; col++ {
				c.SetFill(r, col, plotFill)
			}
		}
	}

	// Y axis labels (top, middle, bottom)
	topLabel := formatNum(xd.yMax)
	midLabel := formatNum((xd.yMin + xd.yMax) / 2)
	botLabel := formatNum(xd.yMin)
	c.PutText(plotY, plotX-1-len(topLabel), topLabel, "default")
	c.PutText(plotY+plotH/2, plotX-1-len(midLabel), midLabel, "default")
	c.PutText(plotBottom, plotX-1-len(botLabel), botLabel, "default")

	// Y axis label (vertical, abbreviated)
	if xd.yLabel != "" {
		c.PutText(plotY, 0, xd.yLabel, "label")
	}

	// Determine number of data points
	nPoints := len(xd.barData)
	if len(xd.lineData) > nPoints {
		nPoints = len(xd.lineData)
	}
	if len(xd.xLabels) > nPoints {
		nPoints = len(xd.xLabels)
	}
	if nPoints == 0 {
		return c
	}

	colW := plotW / nPoints
	if colW < 3 {
		colW = 3
	}
	barW := colW - 2
	if barW < 1 {
		barW = 1
	}

	yRange := xd.yMax - xd.yMin
	if yRange <= 0 {
		yRange = 1
	}

	// Draw bars
	for i, v := range xd.barData {
		barH := int((v - xd.yMin) / yRange * float64(plotH-1))
		if barH < 1 && v > xd.yMin {
			barH = 1
		}
		barX := plotX + i*colW + (colW-barW)/2
		barStyle := "node"
		if useRegion {
			barStyle = "_ansi:" + theme.RegionBarStyle(i+1, 1)
		}
		for row := plotBottom - barH; row < plotBottom; row++ {
			for dx := 0; dx < barW; dx++ {
				c.Put(row, barX+dx, barCh, false, barStyle)
			}
		}
	}

	// Draw line (after bars so dots render on top)
	if len(xd.lineData) > 0 {
		// Use bright white for line elements so they stand out on colored bars
		lineStyle := "arrow"
		connStyle := "bold_label"
		if useRegion {
			lineStyle = "_ansi:\033[1m\033[38;2;255;215;0m"   // bold gold
			connStyle = "_ansi:\033[1m\033[38;2;255;255;255m" // bold white
		}

		prevX, prevY := -1, -1
		for i, v := range xd.lineData {
			lx := plotX + i*colW + colW/2
			ly := plotBottom - 1 - int((v-xd.yMin)/yRange*float64(plotH-2))
			if ly < plotY {
				ly = plotY
			}

			c.Put(ly, lx, lineDot, false, lineStyle)

			// Connect to previous point
			if prevX >= 0 {
				if prevY == ly {
					c.DrawHorizontal(ly, prevX+1, lx-1, hLine, connStyle)
				} else {
					midCol := (prevX + lx) / 2
					c.DrawHorizontal(prevY, prevX+1, midCol, hLine, connStyle)
					c.DrawVertical(midCol, prevY, ly, vLine, connStyle)
					c.DrawHorizontal(ly, midCol, lx-1, hLine, connStyle)
				}
			}

			prevX, prevY = lx, ly
		}
	}

	// X axis labels — use section-colored text when themed
	for i, label := range xd.xLabels {
		if i >= nPoints {
			break
		}
		lx := plotX + i*colW + colW/2 - len(label)/2
		if lx < 0 {
			lx = 0
		}
		xLabelStyle := "label"
		if useRegion {
			xLabelStyle = "_ansi:" + theme.RegionTextStyle(i+1, 0)
		}
		c.PutText(plotBottom+1, lx, label, xLabelStyle)
	}

	return c
}

func formatNum(v float64) string {
	if v == math.Trunc(v) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
