package diagram

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// pieSlice represents a single slice in a pie chart.
type pieSlice struct {
	label string
	value float64
}

// pieChart represents a parsed pie chart definition.
type pieChart struct {
	title    string
	showData bool
	slices   []pieSlice
}

// unicodeFills are fill characters for unicode mode.
var unicodeFills = []rune{'█', '▓', '░', '▒', '▞', '▚', '▖', '▗'}

// asciiFills are fill characters for ascii mode.
var asciiFills = []rune{'#', '*', '+', '~', ':', '.', 'o', '='}

// pieColors are vibrant, distinct colors for pie slices.
var pieColors = [][3]int{
	{65, 105, 225},  // royal blue
	{220, 70, 60},   // crimson
	{50, 180, 80},   // green
	{255, 165, 0},   // orange
	{148, 103, 189}, // purple
	{0, 190, 190},   // teal
	{255, 105, 140}, // pink
	{255, 215, 0},   // gold
}

const (
	pieBarWidth = 40
	pieMargin   = 2
)

// Regex patterns for pie chart parsing.
var (
	rePieHeader = regexp.MustCompile(`(?i)^\s*pie\s*(showData)?\s*$`)
	rePieTitle  = regexp.MustCompile(`(?i)^\s*title\s+(.+)$`)
	rePieSlice  = regexp.MustCompile(`^\s*"([^"]+)"\s*:\s*([0-9]*\.?[0-9]+)\s*$`)
)

// parsePieChart parses pie chart source text into a pieChart model.
func parsePieChart(source string) *pieChart {
	pc := &pieChart{}
	lines := strings.Split(source, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}

		// Header
		if m := rePieHeader.FindStringSubmatch(trimmed); m != nil {
			if strings.EqualFold(m[1], "showData") {
				pc.showData = true
			}
			continue
		}

		// Title
		if m := rePieTitle.FindStringSubmatch(trimmed); m != nil {
			pc.title = strings.TrimSpace(m[1])
			continue
		}

		// Slice
		if m := rePieSlice.FindStringSubmatch(trimmed); m != nil {
			val, err := strconv.ParseFloat(m[2], 64)
			if err != nil {
				continue
			}
			pc.slices = append(pc.slices, pieSlice{
				label: m[1],
				value: val,
			})
			continue
		}
	}

	return pc
}

// RenderPieChart parses and renders a Mermaid pie chart.
// Three modes: color circle (useColor), braille circle (plain), bar chart (ASCII).
func RenderPieChart(source string, useASCII bool, useColor bool, theme *renderer.Theme) *renderer.Canvas {
	pc := parsePieChart(source)
	if len(pc.slices) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[pie] no data", "default")
		return c
	}

	if useASCII {
		return renderPieBarChart(pc, true)
	}
	if useColor {
		// Use monochromatic shades if theme provides a base hue
		colors := pieColors
		if theme != nil && theme.HasPieBase() {
			colors = theme.PieColors(len(pc.slices))
		}
		return renderPieCircle(pc, colors)
	}
	return renderPieBraille(pc)
}

// pieLegendWidth computes the total legend box width for a pie chart.
func pieLegendWidth(pc *pieChart, total float64) int {
	maxLabelW := 0
	for _, s := range pc.slices {
		if len(s.label) > maxLabelW {
			maxLabelW = len(s.label)
		}
	}
	maxSuffixW := 0
	for _, s := range pc.slices {
		pct := s.value / total * 100
		suffix := fmt.Sprintf(" %.1f%%", pct)
		if pc.showData {
			suffix = fmt.Sprintf(" %.1f%% (%.0f)", pct, s.value)
		}
		if len(suffix) > maxSuffixW {
			maxSuffixW = len(suffix)
		}
	}
	legendInnerW := 1 + 2 + 1 + maxLabelW + maxSuffixW + 1
	return legendInnerW + 2 // +2 for borders
}

// piScaledRadius computes the pie circle radius scaled to available width.
func piScaledRadius(pc *pieChart, total float64) int {
	legendW := pieLegendWidth(pc, total)
	legendOverhead := 3 + legendW + 1 // legendGap + legendW + margin
	availableForCircle := usableWidth() - legendOverhead
	radius := (availableForCircle - 1) / 2
	if radius < 8 {
		radius = 8
	}
	if radius > 30 {
		radius = 30
	}
	return radius
}

// pieSliceAngle defines the angular range for a pie slice.
type pieSliceAngle struct {
	start, end float64
	idx        int
}

// renderPieCircle renders a circular pie chart using half-block characters.
func renderPieCircle(pc *pieChart, colors [][3]int) *renderer.Canvas {
	// Compute slice angles
	total := 0.0
	for _, s := range pc.slices {
		total += s.value
	}
	if total == 0 {
		total = 1
	}

	angles := make([]pieSliceAngle, len(pc.slices))
	cur := -math.Pi / 2 // start from top (12 o'clock)
	for i, s := range pc.slices {
		span := s.value / total * 2 * math.Pi
		angles[i] = pieSliceAngle{cur, cur + span, i}
		cur += span
	}

	// Scale radius to available width
	radius := piScaledRadius(pc, total)
	diameter := radius*2 + 1

	legendGap := 3
	legendW := pieLegendWidth(pc, total)

	titleRow := 0
	startRow := 0
	if pc.title != "" {
		startRow = 2
	}

	// Canvas rows: each cell = 2 sub-pixels vertically
	circleRows := (diameter + 1) / 2
	circleCols := diameter

	canvasWidth := circleCols + legendGap + legendW + 1
	canvasHeight := startRow + max(circleRows, len(pc.slices)+2) + 1 // +2 for legend borders

	c := renderer.NewCanvas(canvasWidth, canvasHeight)

	// Draw title
	if pc.title != "" {
		titleCol := (canvasWidth - len(pc.title)) / 2
		if titleCol < 0 {
			titleCol = 0
		}
		// Use raw bold white for the title — theme label styles may include backgrounds
		c.PutText(titleRow, titleCol, pc.title, "_ansi:\033[1m\033[38;2;255;255;255m")
	}

	// Draw circle with anti-aliasing via supersampling.
	// Each half-pixel is sampled on a 4x4 sub-grid; colors are blended
	// by coverage to smooth circle edges and slice boundaries.
	cx, cy := float64(radius), float64(radius)
	const aaSamples = 4 // 4x4 grid per half-pixel
	for row := 0; row < circleRows; row++ {
		for col := 0; col < circleCols; col++ {
			topColor := pieBlendHalfPixel(float64(col), float64(row*2), cx, cy, float64(radius), angles, aaSamples, colors)
			botColor := pieBlendHalfPixel(float64(col), float64(row*2+1), cx, cy, float64(radius), angles, aaSamples, colors)

			topInside := topColor[3] > 0
			botInside := botColor[3] > 0

			if !topInside && !botInside {
				continue
			}

			var ansi string
			var ch rune

			if topInside && botInside {
				if topColor == botColor {
					ch = '█'
					ansi = fmt.Sprintf("\033[38;2;%d;%d;%dm", topColor[0], topColor[1], topColor[2])
				} else {
					ch = '▀'
					ansi = fmt.Sprintf("\033[38;2;%d;%d;%dm\033[48;2;%d;%d;%dm",
						topColor[0], topColor[1], topColor[2],
						botColor[0], botColor[1], botColor[2])
				}
			} else if topInside {
				ch = '▀'
				ansi = fmt.Sprintf("\033[38;2;%d;%d;%dm", topColor[0], topColor[1], topColor[2])
			} else {
				ch = '▄'
				ansi = fmt.Sprintf("\033[38;2;%d;%d;%dm", botColor[0], botColor[1], botColor[2])
			}

			c.Put(startRow+row, col, ch, false, "_ansi:"+ansi)
		}
	}

	// Draw legend box to the right of the circle
	legendCol := circleCols + legendGap
	// Vertically center legend relative to circle
	legendStartRow := startRow + (circleRows-len(pc.slices))/2 - 1 // -1 for top border
	if legendStartRow < startRow {
		legendStartRow = startRow
	}

	// Recompute maxLabelW for legend alignment
	maxLabelW := 0
	for _, s := range pc.slices {
		if len(s.label) > maxLabelW {
			maxLabelW = len(s.label)
		}
	}

	// Legend box dimensions
	legendBoxW := legendW
	legendBoxH := len(pc.slices) + 2 // +2 for top/bottom borders
	legendBoxTop := legendStartRow
	legendBoxLeft := legendCol

	// Draw legend border
	c.Put(legendBoxTop, legendBoxLeft, '┌', false, "node")
	c.DrawHorizontal(legendBoxTop, legendBoxLeft+1, legendBoxLeft+legendBoxW-2, '─', "node")
	c.Put(legendBoxTop, legendBoxLeft+legendBoxW-1, '┐', false, "node")

	for row := legendBoxTop + 1; row < legendBoxTop+legendBoxH-1; row++ {
		c.Put(row, legendBoxLeft, '│', false, "node")
		c.Put(row, legendBoxLeft+legendBoxW-1, '│', false, "node")
	}

	c.Put(legendBoxTop+legendBoxH-1, legendBoxLeft, '└', false, "node")
	c.DrawHorizontal(legendBoxTop+legendBoxH-1, legendBoxLeft+1, legendBoxLeft+legendBoxW-2, '─', "node")
	c.Put(legendBoxTop+legendBoxH-1, legendBoxLeft+legendBoxW-1, '┘', false, "node")

	// Fill legend interior — use fill layer only so all cells share the same bg
	for row := legendBoxTop; row < legendBoxTop+legendBoxH; row++ {
		for col := legendBoxLeft; col < legendBoxLeft+legendBoxW; col++ {
			c.SetFill(row, col, "subgraph_fill")
		}
	}

	// Draw legend entries
	for i, s := range pc.slices {
		row := legendBoxTop + 1 + i
		if row >= legendBoxTop+legendBoxH-1 {
			break
		}

		clr := colors[i%len(colors)]
		blockAnsi := fmt.Sprintf("\033[38;2;%d;%d;%dm", clr[0], clr[1], clr[2])

		// Color swatch
		swatchCol := legendBoxLeft + 2
		c.Put(row, swatchCol, '█', false, "_ansi:"+blockAnsi)
		c.Put(row, swatchCol+1, '█', false, "_ansi:"+blockAnsi)

		// Label (regular weight, white text — inherits fill bg from legend box)
		c.PutText(row, swatchCol+3, s.label, "_ansi:\033[38;2;255;255;255m")

		// Percentage (dimmer)
		pct := s.value / total * 100
		suffix := fmt.Sprintf(" %.1f%%", pct)
		if pc.showData {
			suffix = fmt.Sprintf(" %.1f%% (%.0f)", pct, s.value)
		}
		c.PutText(row, swatchCol+3+maxLabelW, suffix, "_ansi:\033[38;2;180;180;180m")
	}

	return c
}

// pieSliceAt returns the slice index for a point at (px, py), or -1 if outside the circle.
func pieSliceAt(px, py, cx, cy, radius float64, angles []pieSliceAngle) int {
	dx := px - cx
	dy := py - cy
	if dx*dx+dy*dy > radius*radius {
		return -1
	}
	angle := math.Atan2(dy, dx)
	for _, a := range angles {
		normAngle := angle
		if normAngle < a.start {
			normAngle += 2 * math.Pi
		}
		if normAngle >= a.start && normAngle < a.end {
			return a.idx
		}
	}
	return angles[len(angles)-1].idx
}

// pieBlendHalfPixel supersamples a half-pixel and returns [R,G,B,A].
// Uses dominant-slice coloring: the majority slice wins each half-pixel,
// so small wedges keep their identity. Brightness is scaled by coverage
// to anti-alias circle edges (blending toward black).
func pieBlendHalfPixel(px, py, cx, cy, radius float64, angles []pieSliceAngle, samples int, colors [][3]int) [4]int {
	totalSamples := samples * samples
	counts := make(map[int]int)
	insideCount := 0

	for sy := 0; sy < samples; sy++ {
		for sx := 0; sx < samples; sx++ {
			spx := px + (float64(sx)+0.5)/float64(samples) - 0.5
			spy := py + (float64(sy)+0.5)/float64(samples) - 0.5

			idx := pieSliceAt(spx, spy, cx, cy, radius, angles)
			if idx >= 0 {
				counts[idx]++
				insideCount++
			}
		}
	}

	// Minimum coverage threshold: skip very sparse edge pixels (eliminates sparks)
	if insideCount < 3 {
		return [4]int{0, 0, 0, 0}
	}

	// Find dominant slice (most samples)
	bestIdx, bestCount := -1, 0
	for idx, count := range counts {
		if count > bestCount {
			bestIdx = idx
			bestCount = count
		}
	}

	// Use dominant slice color, scale brightness by coverage for edge AA
	clr := colors[bestIdx%len(colors)]
	coverage := float64(insideCount) / float64(totalSamples)
	r := int(float64(clr[0]) * coverage)
	g := int(float64(clr[1]) * coverage)
	b := int(float64(clr[2]) * coverage)
	return [4]int{r, g, b, 255}
}

// Braille dot layout within a cell (2 cols × 4 rows):
//
//	bit0  bit3
//	bit1  bit4
//	bit2  bit5
//	bit6  bit7
//
// Unicode braille: U+2800 + bitmask
var brailleBitMap = [4][2]uint{
	{0x01, 0x08}, // row 0
	{0x02, 0x10}, // row 1
	{0x04, 0x20}, // row 2
	{0x40, 0x80}, // row 3
}

// pieBraillePattern returns whether a dot should be "on" for a given slice.
// Uses global sub-pixel coordinates so patterns tile seamlessly across cells.
// Each slice gets a visually distinct texture.
func pieBraillePattern(sliceIdx, globalRow, globalCol int) bool {
	switch sliceIdx % 8 {
	case 0:
		return true // solid
	case 1:
		return (globalRow+globalCol)%2 == 0 // checkerboard A
	case 2:
		return (globalRow+globalCol)%2 == 1 // checkerboard B
	case 3:
		return globalRow%2 == 0 // horizontal stripes
	case 4:
		return globalCol%2 == 0 // vertical stripes
	case 5:
		return globalRow%3 == 0 // sparse horizontal
	case 6:
		return (globalRow%3 == 0 && globalCol%2 == 0) || (globalRow%3 == 1 && globalCol%2 == 1) // diagonal
	case 7:
		return globalRow%4 == 0 && globalCol%2 == 0 // very sparse
	}
	return true
}

// renderPieBraille renders a circular pie chart using braille characters.
// Each slice uses a distinct dot pattern so slices are distinguishable without color.
func renderPieBraille(pc *pieChart) *renderer.Canvas {
	// Compute slice angles
	total := 0.0
	for _, s := range pc.slices {
		total += s.value
	}
	if total == 0 {
		total = 1
	}

	angles := make([]pieSliceAngle, len(pc.slices))
	cur := -math.Pi / 2
	for i, s := range pc.slices {
		span := s.value / total * 2 * math.Pi
		angles[i] = pieSliceAngle{cur, cur + span, i}
		cur += span
	}

	// Scale radius to available width (compute legend overhead first)
	radius := piScaledRadius(pc, total)

	// Braille: 2 dots wide × 4 dots tall per cell.
	// Terminal chars are ~2:1 aspect, so each dot is roughly square.
	// Sub-pixel grid dimensions:
	spDiameter := radius*2 + 1
	spW := spDiameter * 2 // 2 sub-pixels per cell column
	spH := spDiameter * 4 // 4 sub-pixels per cell row

	cellCols := (spW + 1) / 2
	cellRows := (spH + 3) / 4

	cx := float64(spW-1) / 2.0
	cy := float64(spH-1) / 2.0
	spRadius := float64(min(spW, spH)-1) / 2.0

	// Legend dimensions
	legendGap := 3
	maxLabelW := 0
	for _, s := range pc.slices {
		if len(s.label) > maxLabelW {
			maxLabelW = len(s.label)
		}
	}
	maxSuffixW := 0
	for _, s := range pc.slices {
		pct := s.value / total * 100
		suffix := fmt.Sprintf(" %.1f%%", pct)
		if pc.showData {
			suffix = fmt.Sprintf(" %.1f%% (%.0f)", pct, s.value)
		}
		if len(suffix) > maxSuffixW {
			maxSuffixW = len(suffix)
		}
	}

	// Legend box: border + " PP label  xx.x% " + border
	legendInnerW := 1 + 2 + 1 + maxLabelW + maxSuffixW + 1
	legendBoxW := legendInnerW + 2

	titleRow := 0
	startRow := 0
	if pc.title != "" {
		startRow = 2
	}

	canvasWidth := cellCols + legendGap + legendBoxW + 1
	canvasHeight := startRow + max(cellRows, len(pc.slices)+2) + 1

	c := renderer.NewCanvas(canvasWidth, canvasHeight)

	// Draw title
	if pc.title != "" {
		titleCol := (canvasWidth - len(pc.title)) / 2
		if titleCol < 0 {
			titleCol = 0
		}
		c.PutText(titleRow, titleCol, pc.title, "bold_label")
	}

	// Draw circle with braille
	for row := 0; row < cellRows; row++ {
		for col := 0; col < cellCols; col++ {
			var bits uint
			for dr := 0; dr < 4; dr++ {
				for dc := 0; dc < 2; dc++ {
					gx := col*2 + dc
					gy := row*4 + dr

					idx := pieSliceAt(float64(gx), float64(gy), cx, cy, spRadius, angles)
					if idx < 0 {
						continue
					}

					if pieBraillePattern(idx, gy, gx) {
						bits |= brailleBitMap[dr][dc]
					}
				}
			}

			if bits == 0 {
				continue
			}

			ch := rune(0x2800 + bits)
			c.Put(startRow+row, col, ch, false, "default")
		}
	}

	// Draw legend box
	legendCol := cellCols + legendGap
	legendStartRow := startRow + (cellRows-len(pc.slices))/2 - 1
	if legendStartRow < startRow {
		legendStartRow = startRow
	}
	legendBoxH := len(pc.slices) + 2
	legendBoxLeft := legendCol

	// Border
	c.Put(legendStartRow, legendBoxLeft, '┌', false, "node")
	c.DrawHorizontal(legendStartRow, legendBoxLeft+1, legendBoxLeft+legendBoxW-2, '─', "node")
	c.Put(legendStartRow, legendBoxLeft+legendBoxW-1, '┐', false, "node")
	for row := legendStartRow + 1; row < legendStartRow+legendBoxH-1; row++ {
		c.Put(row, legendBoxLeft, '│', false, "node")
		c.Put(row, legendBoxLeft+legendBoxW-1, '│', false, "node")
	}
	c.Put(legendStartRow+legendBoxH-1, legendBoxLeft, '└', false, "node")
	c.DrawHorizontal(legendStartRow+legendBoxH-1, legendBoxLeft+1, legendBoxLeft+legendBoxW-2, '─', "node")
	c.Put(legendStartRow+legendBoxH-1, legendBoxLeft+legendBoxW-1, '┘', false, "node")

	// Entries
	for i, s := range pc.slices {
		row := legendStartRow + 1 + i
		if row >= legendStartRow+legendBoxH-1 {
			break
		}

		swatchCol := legendBoxLeft + 2

		// Swatch: braille pattern matching the slice
		var swatchBits uint
		for dr := 0; dr < 4; dr++ {
			for dc := 0; dc < 2; dc++ {
				if pieBraillePattern(i, dr, dc) {
					swatchBits |= brailleBitMap[dr][dc]
				}
			}
		}
		swatch := rune(0x2800 + swatchBits)
		c.Put(row, swatchCol, swatch, false, "default")
		c.Put(row, swatchCol+1, swatch, false, "default")

		// Label
		c.PutText(row, swatchCol+3, s.label, "default")

		// Percentage
		pct := s.value / total * 100
		suffix := fmt.Sprintf(" %.1f%%", pct)
		if pc.showData {
			suffix = fmt.Sprintf(" %.1f%% (%.0f)", pct, s.value)
		}
		c.PutText(row, swatchCol+3+maxLabelW, suffix, "default")
	}

	return c
}

// renderPieBarChart renders a horizontal bar chart (fallback for ASCII mode).
func renderPieBarChart(pc *pieChart, useASCII bool) *renderer.Canvas {
	fills := unicodeFills
	if useASCII {
		fills = asciiFills
	}

	// Compute total
	total := 0.0
	for _, s := range pc.slices {
		total += s.value
	}
	if total == 0 {
		total = 1
	}

	// Compute max label width for right-alignment
	maxLabelWidth := 0
	for _, s := range pc.slices {
		if len(s.label) > maxLabelWidth {
			maxLabelWidth = len(s.label)
		}
	}

	// Compute suffix widths for sizing
	maxSuffixWidth := 0
	for _, s := range pc.slices {
		pct := s.value / total * 100
		suffix := fmt.Sprintf(" %.1f%%", pct)
		if pc.showData {
			suffix = fmt.Sprintf(" %.1f%% (%.0f)", pct, s.value)
		}
		if len(suffix) > maxSuffixWidth {
			maxSuffixWidth = len(suffix)
		}
	}

	// Canvas dimensions
	separatorWidth := 3 // " ┃ " or " | "
	canvasWidth := pieMargin + maxLabelWidth + separatorWidth + pieBarWidth + maxSuffixWidth + pieMargin
	titleRow := 0
	barStartRow := 0
	if pc.title != "" {
		barStartRow = 2
	}
	canvasHeight := barStartRow + len(pc.slices) + 1

	c := renderer.NewCanvas(canvasWidth, canvasHeight)

	// Draw title
	if pc.title != "" {
		titleCol := (canvasWidth - len(pc.title)) / 2
		if titleCol < 0 {
			titleCol = 0
		}
		c.PutText(titleRow, titleCol, pc.title, "default")
	}

	// Separator character
	sepChar := "┃"
	if useASCII {
		sepChar = "|"
	}

	// Draw each bar
	for i, s := range pc.slices {
		row := barStartRow + i
		fillChar := fills[i%len(fills)]

		// Right-aligned label
		padding := maxLabelWidth - len(s.label)
		labelCol := pieMargin + padding
		c.PutText(row, labelCol, s.label, "default")

		// Separator
		sepCol := pieMargin + maxLabelWidth + 1
		c.PutText(row, sepCol, sepChar, "default")

		// Bar
		barCol := pieMargin + maxLabelWidth + separatorWidth
		pct := s.value / total
		barLen := int(pct * float64(pieBarWidth))
		if barLen < 1 && s.value > 0 {
			barLen = 1
		}
		for j := 0; j < barLen; j++ {
			c.Put(row, barCol+j, fillChar, false, "default")
		}

		// Suffix with percentage (and optionally value)
		suffixCol := barCol + pieBarWidth
		pctVal := s.value / total * 100
		suffix := fmt.Sprintf(" %.1f%%", pctVal)
		if pc.showData {
			suffix = fmt.Sprintf(" %.1f%% (%.0f)", pctVal, s.value)
		}
		c.PutText(row, suffixCol, suffix, "default")
	}

	return c
}
