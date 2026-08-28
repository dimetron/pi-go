package diagram

import (
	"regexp"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// timelineEvent holds one or more items at a time point.
type timelineEvent struct {
	period string
	items  []string
}

// timelineData holds the parsed timeline.
type timelineData struct {
	title  string
	events []timelineEvent
}

var (
	reTimelineHeader = regexp.MustCompile(`(?i)^\s*timeline\s*$`)
	reTimelineTitle  = regexp.MustCompile(`(?i)^\s*title\s+(.+)$`)
)

func parseTimeline(source string) *timelineData {
	td := &timelineData{}
	lines := strings.Split(source, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if reTimelineHeader.MatchString(trimmed) {
			continue
		}
		if m := reTimelineTitle.FindStringSubmatch(trimmed); m != nil {
			td.title = strings.TrimSpace(m[1])
			continue
		}

		// "section X" lines become period labels for subsequent events
		if strings.HasPrefix(strings.ToLower(trimmed), "section ") {
			td.events = append(td.events, timelineEvent{
				period: strings.TrimSpace(trimmed[8:]),
			})
			continue
		}

		// Event line: "period : item1 : item2" or continuation ": item"
		parts := strings.Split(trimmed, ":")
		if len(parts) >= 2 {
			period := strings.TrimSpace(parts[0])
			var items []string
			for _, p := range parts[1:] {
				item := strings.TrimSpace(p)
				if item != "" {
					items = append(items, item)
				}
			}
			if period != "" {
				td.events = append(td.events, timelineEvent{period: period, items: items})
			} else if len(td.events) > 0 {
				// Continuation line — append items to last event
				td.events[len(td.events)-1].items = append(td.events[len(td.events)-1].items, items...)
			}
		}
	}

	return td
}

// timelineChars holds the box-drawing characters for timeline rendering.
type timelineChars struct {
	hLine, dot, tl, tr, bl, br, vLine rune
}

func getTimelineChars(useASCII bool) timelineChars {
	if useASCII {
		return timelineChars{'-', 'o', '+', '+', '+', '+', '|'}
	}
	return timelineChars{'─', '●', '╭', '╮', '╰', '╯', '│'}
}

// computeTimelineColWidth returns the column width needed for horizontal layout.
func computeTimelineColWidth(td *timelineData) int {
	colWidth := 0
	for _, e := range td.events {
		if len(e.period) > colWidth {
			colWidth = len(e.period)
		}
		for _, item := range e.items {
			if len(item)+4 > colWidth {
				colWidth = len(item) + 4
			}
		}
	}
	return colWidth + 2
}

// RenderTimeline parses and renders a Mermaid timeline diagram.
// Automatically switches to vertical layout when horizontal won't fit in the terminal.
func RenderTimeline(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	td := parseTimeline(source)
	if len(td.events) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[timeline] no events", "default")
		return c
	}

	colWidth := computeTimelineColWidth(td)
	horizontalWidth := len(td.events)*colWidth + 2
	termW := usableWidth()

	if horizontalWidth > termW {
		return renderTimelineVertical(td, useASCII, theme)
	}
	return renderTimelineHorizontal(td, useASCII, theme, colWidth)
}

// renderTimelineHorizontal renders the timeline with a horizontal axis.
func renderTimelineHorizontal(td *timelineData, useASCII bool, theme *renderer.Theme, colWidth int) *renderer.Canvas {
	ch := getTimelineChars(useASCII)

	maxItems := 0
	for _, e := range td.events {
		if len(e.items) > maxItems {
			maxItems = len(e.items)
		}
	}

	titleRows := 0
	if td.title != "" {
		titleRows = 2
	}
	itemHeight := maxItems * 3
	axisRow := titleRows + itemHeight
	periodRow := axisRow + 1

	canvasWidth := len(td.events)*colWidth + 2
	canvasHeight := periodRow + 2

	c := renderer.NewCanvas(canvasWidth, canvasHeight)
	useRegion := theme != nil && theme.HasDepthColors()

	if useRegion {
		for r := 0; r < canvasHeight; r++ {
			for col := 0; col < canvasWidth; col++ {
				c.SetFill(r, col, "subgraph_fill")
			}
		}
	}

	if td.title != "" {
		titleCol := (canvasWidth - len(td.title)) / 2
		if titleCol < 0 {
			titleCol = 0
		}
		c.PutText(0, titleCol, td.title, "bold_label")
	}

	// Draw axis line
	for x := 0; x < canvasWidth; x++ {
		c.Put(axisRow, x, ch.hLine, false, "edge")
	}

	for i, e := range td.events {
		centerX := i*colWidth + colWidth/2

		c.Put(axisRow, centerX, ch.dot, false, "arrow")

		periodStyle := "label"
		if useRegion {
			periodStyle = "_ansi:" + theme.RegionTextStyle(i, 0)
		}
		labelX := centerX - len(e.period)/2
		if labelX < 0 {
			labelX = 0
		}
		c.PutText(periodRow, labelX, e.period, periodStyle)

		for j, item := range e.items {
			boxW := len(item) + 2
			boxX := centerX - boxW/2
			if boxX < 0 {
				boxX = 0
			}
			boxBottom := axisRow - 1 - j*3
			boxTop := boxBottom - 2

			borderStyle := "node"
			labelStyle := "label"
			if useRegion {
				borderStyle = "_ansi:" + theme.RegionBorderStyle(i, 1)
				labelStyle = "_ansi:" + theme.RegionLabelStyle(i, 1)
			}

			c.Put(boxTop, boxX, ch.tl, false, borderStyle)
			c.DrawHorizontal(boxTop, boxX+1, boxX+boxW-1, ch.hLine, borderStyle)
			c.Put(boxTop, boxX+boxW, ch.tr, false, borderStyle)

			c.Put(boxTop+1, boxX, ch.vLine, false, borderStyle)
			c.PutText(boxTop+1, boxX+1, " "+item+" ", labelStyle)
			c.Put(boxTop+1, boxX+boxW, ch.vLine, false, borderStyle)

			c.Put(boxTop+2, boxX, ch.bl, false, borderStyle)
			c.DrawHorizontal(boxTop+2, boxX+1, boxX+boxW-1, ch.hLine, borderStyle)
			c.Put(boxTop+2, boxX+boxW, ch.br, false, borderStyle)

			if useRegion {
				fillStyle := "_ansi:" + theme.RegionStyle(i, 1)
				for row := boxTop; row <= boxTop+2; row++ {
					for col := boxX; col <= boxX+boxW; col++ {
						c.SetFill(row, col, fillStyle)
					}
				}
			}
			for col := boxX + 1; col < boxX+boxW; col++ {
				if c.Get(boxTop+1, col) == ' ' {
					c.SetStyle(boxTop+1, col, borderStyle)
				}
			}

			if boxBottom < axisRow-1 {
				c.Put(axisRow-1, centerX, ch.vLine, false, "edge")
			}
		}
	}

	return c
}

// renderTimelineVertical renders the timeline with a vertical axis.
// Used when horizontal layout would exceed terminal width.
func renderTimelineVertical(td *timelineData, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	ch := getTimelineChars(useASCII)
	useRegion := theme != nil && theme.HasDepthColors()

	// Compute layout dimensions
	maxPeriodW := 0
	maxItemW := 0
	for _, e := range td.events {
		if len(e.period) > maxPeriodW {
			maxPeriodW = len(e.period)
		}
		for _, item := range e.items {
			if len(item) > maxItemW {
				maxItemW = len(item)
			}
		}
	}

	// Layout columns:
	// [period label right-aligned] [gap] [axis dot] [gap] [item boxes]
	periodCol := maxPeriodW // right edge of period labels
	axisCol := periodCol + 2
	boxStartCol := axisCol + 2
	boxInnerW := maxItemW + 2                  // +2 for padding spaces inside box
	canvasWidth := boxStartCol + boxInnerW + 2 // +2 for box borders

	// Height: title + each event takes 3 rows (box height) * max(1, items) + 1 gap
	titleRows := 0
	if td.title != "" {
		titleRows = 2
	}

	totalRows := titleRows
	for _, e := range td.events {
		items := len(e.items)
		if items == 0 {
			items = 1
		}
		totalRows += items*3 + 1 // each item box is 3 rows, +1 gap between events
	}
	totalRows++ // trailing space

	c := renderer.NewCanvas(canvasWidth, totalRows)

	if useRegion {
		for r := 0; r < totalRows; r++ {
			for col := 0; col < canvasWidth; col++ {
				c.SetFill(r, col, "subgraph_fill")
			}
		}
	}

	if td.title != "" {
		titleX := (canvasWidth - len(td.title)) / 2
		if titleX < 0 {
			titleX = 0
		}
		c.PutText(0, titleX, td.title, "bold_label")
	}

	// Draw vertical axis line
	for r := titleRows; r < totalRows-1; r++ {
		c.Put(r, axisCol, ch.vLine, false, "edge")
	}

	// Draw each event
	row := titleRows
	for i, e := range td.events {
		items := e.items
		if len(items) == 0 {
			items = []string{}
		}
		eventHeight := len(items) * 3
		if eventHeight == 0 {
			eventHeight = 1
		}

		// Dot on axis at the vertical center of this event's boxes
		dotRow := row + eventHeight/2
		c.Put(dotRow, axisCol, ch.dot, false, "arrow")

		// Period label to the left of the axis, right-aligned
		periodStyle := "label"
		if useRegion {
			periodStyle = "_ansi:" + theme.RegionTextStyle(i, 0)
		}
		labelX := periodCol - len(e.period)
		if labelX < 0 {
			labelX = 0
		}
		c.PutText(dotRow, labelX, e.period, periodStyle)

		// Item boxes to the right of the axis
		for j, item := range e.items {
			boxTop := row + j*3
			boxX := boxStartCol
			boxW := len(item) + 2

			borderStyle := "node"
			labelStyle := "label"
			if useRegion {
				borderStyle = "_ansi:" + theme.RegionBorderStyle(i, 1)
				labelStyle = "_ansi:" + theme.RegionLabelStyle(i, 1)
			}

			c.Put(boxTop, boxX, ch.tl, false, borderStyle)
			c.DrawHorizontal(boxTop, boxX+1, boxX+boxW-1, ch.hLine, borderStyle)
			c.Put(boxTop, boxX+boxW, ch.tr, false, borderStyle)

			c.Put(boxTop+1, boxX, ch.vLine, false, borderStyle)
			c.PutText(boxTop+1, boxX+1, " "+item+" ", labelStyle)
			c.Put(boxTop+1, boxX+boxW, ch.vLine, false, borderStyle)

			c.Put(boxTop+2, boxX, ch.bl, false, borderStyle)
			c.DrawHorizontal(boxTop+2, boxX+1, boxX+boxW-1, ch.hLine, borderStyle)
			c.Put(boxTop+2, boxX+boxW, ch.br, false, borderStyle)

			if useRegion {
				fillStyle := "_ansi:" + theme.RegionStyle(i, 1)
				for fr := boxTop; fr <= boxTop+2; fr++ {
					for fc := boxX; fc <= boxX+boxW; fc++ {
						c.SetFill(fr, fc, fillStyle)
					}
				}
			}
			for col := boxX + 1; col < boxX+boxW; col++ {
				if c.Get(boxTop+1, col) == ' ' {
					c.SetStyle(boxTop+1, col, borderStyle)
				}
			}

			// Horizontal connector from axis to box
			for cx := axisCol + 1; cx < boxX; cx++ {
				c.Put(boxTop+1, cx, ch.hLine, false, "edge")
			}
		}

		row += eventHeight + 1
	}

	return c
}
