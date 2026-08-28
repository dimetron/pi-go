package diagram

import (
	"sort"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// journeyTask is a single step in a user journey, with a 1-5 satisfaction
// score and an optional list of actors involved.
type journeyTask struct {
	title  string
	score  int
	actors []string
}

// journeySection groups consecutive tasks under a heading.
type journeySection struct {
	title string
	tasks []journeyTask
}

// journeyData is the parsed `journey` diagram.
type journeyData struct {
	title    string
	sections []journeySection
}

// parseJourney parses a Mermaid user-journey definition.
//
//	journey
//	    title My working day
//	    section Go to work
//	        Make tea: 5: Me
//	        Do work: 1: Me, Cat
func parseJourney(source string) *journeyData {
	jd := &journeyData{}
	lines := strings.Split(source, "\n")
	if len(lines) == 0 {
		return jd
	}

	var current *journeySection // points into jd.sections

	for _, line := range lines[1:] { // skip the "journey" header line
		if i := strings.Index(line, "%%"); i >= 0 {
			line = line[:i]
		}
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		lower := strings.ToLower(stripped)

		if strings.HasPrefix(lower, "title ") {
			jd.title = strings.TrimSpace(stripped[6:])
			continue
		}
		if strings.HasPrefix(lower, "section ") {
			jd.sections = append(jd.sections, journeySection{title: strings.TrimSpace(stripped[8:])})
			current = &jd.sections[len(jd.sections)-1]
			continue
		}

		// Task: "Task name: score: actor1, actor2"
		if !strings.Contains(stripped, ":") {
			continue
		}
		parts := strings.Split(stripped, ":")
		task := journeyTask{title: strings.TrimSpace(parts[0]), score: 3}
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				task.score = min(5, max(1, n))
			}
		}
		if len(parts) >= 3 {
			for _, a := range strings.Split(parts[2], ",") {
				if a = strings.TrimSpace(a); a != "" {
					task.actors = append(task.actors, a)
				}
			}
		}
		if current == nil {
			jd.sections = append(jd.sections, journeySection{})
			current = &jd.sections[len(jd.sections)-1]
		}
		current.tasks = append(current.tasks, task)
	}

	return jd
}

// Satisfaction faces. Upstream termaid uses emoji (😞..😄), but this port's
// canvas is a strict one-rune-per-cell grid with no wide-character handling,
// so width-2 emoji would shear the layout. Fixed-width ASCII faces keep the
// grid aligned in every terminal — an intentional divergence from upstream.
var journeyFaces = map[int]string{
	1: ":((",
	2: ":( ",
	3: ":-|",
	4: ":) ",
	5: ":D ",
}

func journeyActorSymbols(useASCII bool) []rune {
	if useASCII {
		return []rune{'*', '+', '#', '^', '@', 'o', 'x', '>'}
	}
	return []rune{'●', '◆', '■', '▲', '★', '◉', '◈', '▶'}
}

// RenderJourney parses and renders a Mermaid user-journey diagram.
//
// Default orientation is horizontal (a left-to-right timeline). A
// `direction TB` directive in the diagram, or --orientation tb on the CLI,
// renders it vertically instead — useful when many tasks would otherwise
// sprawl past the terminal width.
func RenderJourney(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	jd := parseJourney(source)
	if len(jd.sections) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[journey] no sections", "default")
		return c
	}
	if resolveVertical(source, false) {
		return renderJourneyVertical(jd, useASCII, theme)
	}
	return renderJourneyHorizontal(jd, useASCII, theme)
}

// renderJourneyHorizontal renders the journey as a left-to-right timeline of
// task boxes grouped by section, with a satisfaction face under each task.
func renderJourneyHorizontal(jd *journeyData, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	var hz, tl, tr, bl, br, vt, arrow rune
	if useASCII {
		hz, tl, tr, bl, br, vt, arrow = '-', '+', '+', '+', '+', '|', '>'
	} else {
		hz, tl, tr, bl, br, vt, arrow = '─', '╭', '╮', '╰', '╯', '│', '►'
	}
	symbols := journeyActorSymbols(useASCII)
	useRegion := theme != nil && theme.HasDepthColors()

	const paddingX = 2
	const taskGap = 1
	const sectionGap = 3
	taskWidth := paddingX * 4 // minimum task box width (8)

	type placedTask struct {
		x, w       int
		task       journeyTask
		sectionIdx int
	}
	type sectionSpan struct {
		start, end int
		title      string
	}

	var tasks []placedTask
	var spans []sectionSpan
	actorSet := map[string]bool{}

	x := 2
	for si, sec := range jd.sections {
		if si > 0 {
			x += sectionGap
		}
		secStart := x
		for _, t := range sec.tasks {
			w := taskWidth
			if cand := runeLen(t.title) + paddingX*2; cand > w {
				w = cand
			}
			tasks = append(tasks, placedTask{x: x, w: w, task: t, sectionIdx: si})
			for _, a := range t.actors {
				actorSet[a] = true
			}
			x += w + taskGap
		}
		spans = append(spans, sectionSpan{start: secStart, end: x - taskGap, title: sec.title})
	}

	actors := make([]string, 0, len(actorSet))
	for a := range actorSet {
		actors = append(actors, a)
	}
	sort.Strings(actors)
	actorIdx := make(map[string]int, len(actors))
	for i, a := range actors {
		actorIdx[a] = i
	}

	totalW := x + 4

	titleRow := 0
	actorRow := 0
	if jd.title != "" {
		actorRow = 2
	}
	sectionRow := actorRow + len(actors) + 1
	timelineRow := sectionRow + 2
	taskRow := timelineRow
	faceRow := taskRow + 3
	totalH := faceRow + 2

	c := renderer.NewCanvas(totalW+1, totalH+1)

	if jd.title != "" {
		c.PutText(titleRow, 2, jd.title, "bold_label")
	}

	// Actor legend.
	for ai, actor := range actors {
		row := actorRow + ai
		style := "label"
		if useRegion {
			style = "_ansi:" + theme.RegionTextStyle(ai, 0)
		}
		c.Put(row, 2, symbols[ai%len(symbols)], false, style)
		c.PutText(row, 4, actor, style)
	}

	// Section bars above the task boxes.
	for si, sp := range spans {
		borderStyle := "node"
		labelStyle := "bold_label"
		if useRegion {
			borderStyle = "_ansi:" + theme.RegionBorderStyle(si, 0)
			labelStyle = "_ansi:" + theme.RegionLabelStyle(si, 0)
		}
		c.Put(sectionRow, sp.start, tl, false, borderStyle)
		c.DrawHorizontal(sectionRow, sp.start+1, sp.end-1, hz, borderStyle)
		c.Put(sectionRow, sp.end, tr, false, borderStyle)

		titleX := max(sp.start+(sp.end-sp.start-runeLen(sp.title))/2, sp.start+1)
		for col := titleX - 1; col < titleX+runeLen(sp.title)+1; col++ {
			if col > sp.start && col < sp.end {
				c.ClearCell(sectionRow, col)
			}
		}
		c.PutText(sectionRow, titleX, sp.title, labelStyle)
	}

	// Timeline arrow (drawn first; task boxes straddle it).
	c.DrawHorizontal(timelineRow+1, 1, totalW-2, hz, "edge")
	c.Put(timelineRow+1, totalW-1, arrow, false, "edge")

	// Task boxes.
	for _, pt := range tasks {
		x, w, si := pt.x, pt.w, pt.sectionIdx
		borderStyle := "node"
		labelStyle := "label"
		if useRegion {
			borderStyle = "_ansi:" + theme.RegionBorderStyle(si, 1)
			labelStyle = "_ansi:" + theme.RegionLabelStyle(si, 1)
		}

		c.Put(taskRow, x, tl, false, borderStyle)
		c.DrawHorizontal(taskRow, x+1, x+w-2, hz, borderStyle)
		c.Put(taskRow, x+w-1, tr, false, borderStyle)

		c.Put(taskRow+1, x, vt, false, borderStyle)
		c.Put(taskRow+1, x+w-1, vt, false, borderStyle)

		c.Put(taskRow+2, x, bl, false, borderStyle)
		c.DrawHorizontal(taskRow+2, x+1, x+w-2, hz, borderStyle)
		c.Put(taskRow+2, x+w-1, br, false, borderStyle)

		// Clear the box interior (removes the timeline running through it),
		// then center the task title. ClearCell is required here: Canvas.Put
		// ignores spaces, so it cannot erase the timeline underneath.
		for col := x + 1; col < x+w-1; col++ {
			c.ClearCell(taskRow+1, col)
		}
		if useRegion {
			fill := "_ansi:" + theme.RegionStyle(si, 1)
			for row := taskRow; row <= taskRow+2; row++ {
				for col := x; col <= x+w-1; col++ {
					c.SetFill(row, col, fill)
				}
			}
		}
		title := pt.task.title
		tx := max(x+(w-runeLen(title))/2, x+1)
		c.PutText(taskRow+1, tx, title, labelStyle)

		// Actor markers in the box's top-left.
		ax := x + 1
		for _, actor := range pt.task.actors {
			ai := actorIdx[actor]
			style := "label"
			if useRegion {
				style = "_ansi:" + theme.RegionTextStyle(ai, 0)
			}
			c.Put(taskRow, ax, symbols[ai%len(symbols)], false, style)
			ax++
		}

		// Satisfaction face below the box.
		face := journeyFaces[pt.task.score]
		if face == "" {
			face = journeyFaces[3]
		}
		fx := max(x+(w-runeLen(face))/2, x)
		c.PutText(faceRow, fx, face, "label")
	}

	return c
}

// renderJourneyVertical renders the journey top-to-bottom: a vertical spine
// with section headers, task boxes hanging off it, and the satisfaction face
// plus actor markers to the right of each box. Used when `direction TB` or
// --orientation tb is in effect; stays narrow regardless of task count.
func renderJourneyVertical(jd *journeyData, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	var hz, vt, tl, tr, bl, br, tee rune
	if useASCII {
		hz, vt, tl, tr, bl, br, tee = '-', '|', '+', '+', '+', '+', '+'
	} else {
		hz, vt, tl, tr, bl, br, tee = '─', '│', '╭', '╮', '╰', '╯', '├'
	}
	symbols := journeyActorSymbols(useASCII)
	useRegion := theme != nil && theme.HasDepthColors()

	maxTitle := 1
	maxActors := 0
	actorSet := map[string]bool{}
	for _, sec := range jd.sections {
		for _, t := range sec.tasks {
			maxTitle = max(maxTitle, runeLen(t.title))
			maxActors = max(maxActors, len(t.actors))
			for _, a := range t.actors {
				actorSet[a] = true
			}
		}
	}
	actors := make([]string, 0, len(actorSet))
	for a := range actorSet {
		actors = append(actors, a)
	}
	sort.Strings(actors)
	actorIdx := make(map[string]int, len(actors))
	for i, a := range actors {
		actorIdx[a] = i
	}

	const spineCol = 1
	boxStartCol := spineCol + 3 // room for "├──"
	boxInnerW := maxTitle + 2   // one space of padding each side
	boxW := boxInnerW + 2       // + borders
	faceCol := boxStartCol + boxW + 1
	actorCol := faceCol + 4 // face is up to 3 cols + 1 gap
	canvasW := actorCol + max(1, maxActors) + 2

	titleRow := -1
	row := 0
	if jd.title != "" {
		titleRow = 0
		row = 2
	}
	if len(actors) > 0 {
		row += len(actors) + 1 // legend lines + blank
	}

	// Pre-compute row assignments so the spine can be drawn continuously.
	type boxPlace struct {
		top, si int
		task    journeyTask
	}
	var headerRows []struct {
		row, si int
		title   string
	}
	var boxes []boxPlace
	for si, sec := range jd.sections {
		headerRows = append(headerRows, struct {
			row, si int
			title   string
		}{row, si, sec.title})
		row++ // section header
		for _, t := range sec.tasks {
			boxes = append(boxes, boxPlace{top: row, si: si, task: t})
			row += 3
		}
		row++ // gap between sections
	}
	totalRows := row + 1

	c := renderer.NewCanvas(canvasW+1, totalRows)
	if useRegion {
		for r := range totalRows {
			for col := range canvasW + 1 {
				c.SetFill(r, col, "subgraph_fill")
			}
		}
	}

	if titleRow >= 0 {
		c.PutText(titleRow, spineCol, jd.title, "bold_label")
	}

	// Actor legend.
	legendRow := 0
	if titleRow >= 0 {
		legendRow = 2
	}
	for ai, actor := range actors {
		style := "label"
		if useRegion {
			style = "_ansi:" + theme.RegionTextStyle(ai, 0)
		}
		c.Put(legendRow+ai, spineCol, symbols[ai%len(symbols)], false, style)
		c.PutText(legendRow+ai, spineCol+2, actor, style)
	}

	// Continuous spine across the section/task region.
	if len(boxes) > 0 {
		spineTop := headerRows[0].row
		spineBot := boxes[len(boxes)-1].top + 2
		for r := spineTop; r <= spineBot; r++ {
			c.Put(r, spineCol, vt, false, "edge")
		}
	}

	// Section headers (a labeled break on the spine).
	for _, h := range headerRows {
		style := "bold_label"
		if useRegion {
			style = "_ansi:" + theme.RegionLabelStyle(h.si, 0)
		}
		c.ClearCell(h.row, spineCol)
		c.Put(h.row, spineCol, tee, false, style)
		c.PutText(h.row, spineCol+2, h.title, style)
	}

	// Task boxes.
	for _, b := range boxes {
		top := b.top
		mid := top + 1
		bot := top + 2
		borderStyle := "node"
		labelStyle := "label"
		if useRegion {
			borderStyle = "_ansi:" + theme.RegionBorderStyle(b.si, 1)
			labelStyle = "_ansi:" + theme.RegionLabelStyle(b.si, 1)
		}

		// Connector from spine to box.
		c.Put(mid, spineCol, tee, false, "edge")
		for col := spineCol + 1; col < boxStartCol; col++ {
			c.Put(mid, col, hz, false, "edge")
		}

		c.Put(top, boxStartCol, tl, false, borderStyle)
		c.DrawHorizontal(top, boxStartCol+1, boxStartCol+boxW-2, hz, borderStyle)
		c.Put(top, boxStartCol+boxW-1, tr, false, borderStyle)

		c.Put(mid, boxStartCol, vt, false, borderStyle)
		c.Put(mid, boxStartCol+boxW-1, vt, false, borderStyle)

		c.Put(bot, boxStartCol, bl, false, borderStyle)
		c.DrawHorizontal(bot, boxStartCol+1, boxStartCol+boxW-2, hz, borderStyle)
		c.Put(bot, boxStartCol+boxW-1, br, false, borderStyle)

		if useRegion {
			fill := "_ansi:" + theme.RegionStyle(b.si, 1)
			for r := top; r <= bot; r++ {
				for col := boxStartCol; col < boxStartCol+boxW; col++ {
					c.SetFill(r, col, fill)
				}
			}
		}

		title := b.task.title
		tx := max(boxStartCol+(boxW-runeLen(title))/2, boxStartCol+1)
		c.PutText(mid, tx, title, labelStyle)

		// Satisfaction face, then actor markers, to the right of the box.
		face := journeyFaces[b.task.score]
		if face == "" {
			face = journeyFaces[3]
		}
		c.PutText(mid, faceCol, face, "label")
		ax := actorCol
		for _, actor := range b.task.actors {
			ai := actorIdx[actor]
			style := "label"
			if useRegion {
				style = "_ansi:" + theme.RegionTextStyle(ai, 0)
			}
			c.Put(mid, ax, symbols[ai%len(symbols)], false, style)
			ax++
		}
	}

	return c
}

// runeLen returns the display width of s as a count of runes. The canvas is a
// strict one-rune-per-cell grid, so rune count equals column width here.
func runeLen(s string) int {
	return len([]rune(s))
}
