package diagram

import (
	"regexp"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

type kanbanCard struct {
	label string
}

type kanbanColumn struct {
	title string
	cards []kanbanCard
}

type kanbanBoard struct {
	columns []kanbanColumn
}

var (
	reKanbanHeader = regexp.MustCompile(`(?i)^\s*kanban\s*$`)
	reKanbanCol    = regexp.MustCompile(`^\s*(\w+)\[([^\]]+)\]\s*$`)
	reKanbanCard   = regexp.MustCompile(`^\s+(\w+)\[([^\]]+)\]\s*$`)
)

func parseKanban(source string) *kanbanBoard {
	kb := &kanbanBoard{}
	lines := strings.Split(source, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if reKanbanHeader.MatchString(trimmed) {
			continue
		}

		// Detect indentation: cards are indented more than columns
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		if m := reKanbanCol.FindStringSubmatch(trimmed); m != nil && indent < 4 {
			kb.columns = append(kb.columns, kanbanColumn{title: m[2]})
			continue
		}

		if m := reKanbanCard.FindStringSubmatch(line); m != nil {
			if len(kb.columns) > 0 {
				col := &kb.columns[len(kb.columns)-1]
				col.cards = append(col.cards, kanbanCard{label: m[2]})
			}
			continue
		}
	}

	return kb
}

// RenderKanban parses and renders a Mermaid kanban board.
// When a theme with depth colors is provided, each column gets a distinct hue
// and cards get a lighter shade (per-section hue + per-depth shade).
func RenderKanban(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	kb := parseKanban(source)
	if len(kb.columns) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[kanban] no columns", "default")
		return c
	}

	useRegion := theme != nil && theme.HasDepthColors()

	// Compute column widths: max of title and card labels + padding
	colWidths := make([]int, len(kb.columns))
	maxCards := 0
	for i, col := range kb.columns {
		w := len(col.title) + 4
		for _, card := range col.cards {
			if cw := len(card.label) + 6; cw > w {
				w = cw
			}
		}
		colWidths[i] = w
		if len(col.cards) > maxCards {
			maxCards = len(col.cards)
		}
	}

	baseGap := 2
	if useRegion {
		baseGap = 1
	}
	// Compute natural width, then scale gap
	naturalW := 0
	for _, w := range colWidths {
		naturalW += w + baseGap
	}
	naturalW -= baseGap
	nGaps := len(colWidths) - 1
	gap := baseGap
	if nGaps > 0 {
		gap = scaleGap(baseGap, nGaps, naturalW, 0, 4)
	}
	totalWidth := 0
	for _, w := range colWidths {
		totalWidth += w + gap
	}
	if nGaps > 0 {
		totalWidth -= gap // no trailing gap
	}

	// Height: header(3) + cards(3 each) + bottom border(1)
	colHeight := 3 + maxCards*3 + 1
	canvasHeight := colHeight + 1

	c := renderer.NewCanvas(totalWidth+1, canvasHeight)

	hLine := '─'
	vLine := '│'
	tl := '┌'
	tr := '┐'
	bl := '└'
	br := '┘'
	cardTL := '╭'
	cardTR := '╮'
	cardBL := '╰'
	cardBR := '╯'
	if useASCII {
		hLine = '-'
		vLine = '|'
		tl = '+'
		tr = '+'
		bl = '+'
		br = '+'
		cardTL = '+'
		cardTR = '+'
		cardBL = '+'
		cardBR = '+'
	}

	x := 0
	for i, col := range kb.columns {
		w := colWidths[i]

		// Style keys for this column
		colBorderStyle := "subgraph"
		colTitleStyle := "subgraph_label"
		cardBorderStyle := "node"
		cardLabelStyle := "label"
		colFillStyle := ""

		if useRegion {
			colBorderStyle = "_ansi:" + theme.RegionBorderStyle(i, 0)
			colTitleStyle = "_ansi:" + theme.RegionLabelStyle(i, 0)
			cardBorderStyle = "_ansi:" + theme.RegionBorderStyle(i, 1)
			cardLabelStyle = "_ansi:" + theme.RegionLabelStyle(i, 1)
			colFillStyle = "_ansi:" + theme.RegionStyle(i, 0)
		}

		// Column border
		c.Put(0, x, tl, false, colBorderStyle)
		c.DrawHorizontal(0, x+1, x+w-2, hLine, colBorderStyle)
		c.Put(0, x+w-1, tr, false, colBorderStyle)

		for row := 1; row < colHeight-1; row++ {
			c.Put(row, x, vLine, false, colBorderStyle)
			c.Put(row, x+w-1, vLine, false, colBorderStyle)
		}

		c.Put(colHeight-1, x, bl, false, colBorderStyle)
		c.DrawHorizontal(colHeight-1, x+1, x+w-2, hLine, colBorderStyle)
		c.Put(colHeight-1, x+w-1, br, false, colBorderStyle)

		// Fill column interior
		if colFillStyle != "" {
			for row := 0; row < colHeight; row++ {
				for col := x; col < x+w; col++ {
					c.SetFill(row, col, colFillStyle)
				}
			}
		}

		// Column title (centered, bold)
		titleX := x + (w-len(col.title))/2
		c.PutText(1, titleX, col.title, colTitleStyle)

		// Separator under title
		c.DrawHorizontal(2, x+1, x+w-2, hLine, colBorderStyle)

		// Cards
		for j, card := range col.cards {
			cardRow := 3 + j*3
			cardW := len(card.label) + 4
			cardX := x + (w-cardW)/2

			c.Put(cardRow, cardX, cardTL, false, cardBorderStyle)
			c.DrawHorizontal(cardRow, cardX+1, cardX+cardW-2, hLine, cardBorderStyle)
			c.Put(cardRow, cardX+cardW-1, cardTR, false, cardBorderStyle)

			c.Put(cardRow+1, cardX, vLine, false, cardBorderStyle)
			labelX := cardX + (cardW-len(card.label))/2
			c.PutText(cardRow+1, labelX, card.label, cardLabelStyle)
			c.Put(cardRow+1, cardX+cardW-1, vLine, false, cardBorderStyle)

			c.Put(cardRow+2, cardX, cardBL, false, cardBorderStyle)
			c.DrawHorizontal(cardRow+2, cardX+1, cardX+cardW-2, hLine, cardBorderStyle)
			c.Put(cardRow+2, cardX+cardW-1, cardBR, false, cardBorderStyle)

			// Fill card interior
			if useRegion {
				cardFill := "_ansi:" + theme.RegionStyle(i, 1)
				for row := cardRow; row <= cardRow+2; row++ {
					for col := cardX; col < cardX+cardW; col++ {
						c.SetFill(row, col, cardFill)
					}
				}
			}
		}

		x += w + gap
	}

	return c
}
