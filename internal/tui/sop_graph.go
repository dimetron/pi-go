package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dimetron/pi-go/internal/sop"
)

// stageStatus is what the sidebar draws for one node of the SOP graph. It
// mirrors the engine's node lifecycle rather than anything the TUI infers: the
// scheduler is the only thing that knows what is running.
type stageStatus uint8

const (
	stageInactive stageStatus = iota
	stageRunning
	stageCompleted
	stageFailed
	stageWaiting // parked on a human approval
)

// Layout of the diagram. Every stage after the first hangs off one vertical
// line, so the flow reads as a single sequence at a glance.
//
// An earlier version stepped each stage one column right of the last. It looked
// better on paper, but the panel is 20 columns wide, so the stagger had to stop
// after four stages — and once it did, the remaining stages shared a column and
// read as siblings of the stage above rather than as steps after it. A straight
// spine says the true thing in fewer rows.
const (
	graphSpineCol   = 2 // the vertical line, and the first stage's glyph
	graphStageCol   = 5 // every stage hanging off the line
	graphReviewCol  = 5 // the elbow of a review checkpoint
	graphEdgeIndent = 1 // an edge sits one column right of the node it leaves
)

// routeMark renders an edge's condition. The graph is 20 columns wide, so the
// condition has to be a glyph rather than the word.
func routeMark(route string) string {
	switch route {
	case "PASS":
		return "✓"
	case "FAIL":
		return "✗"
	case sop.RecheckSignal:
		return "↺"
	default:
		return "→"
	}
}

// sidebarGraphLines draws the compiled SOP as a single spine: the first stage
// heads it, every later stage hangs off it, and each stage's review checkpoint
// and conditional edges hang under the stage itself.
//
//	▶ clarify
//	│  └ ✔ review
//	├─ ○ research
//	├─ ○ plan
//	│  └ ○ review
//	│     ✗→ plan
//	│     ✓→ prompt
//	└─ ○ manifest
//
// The line closes with └ on the last stage, so the end of the flow is visible
// rather than implied by running out of rows.
//
// It returns nil when there is nothing to draw, so the caller can drop the
// whole section rather than emit an empty box.
func sidebarGraphLines(
	order []string, edges []sop.GraphEdge, status map[string]stageStatus,
	innerW int, st sidebarStyles,
) []string {
	if len(order) == 0 {
		return nil
	}

	// Conditional edges only. An unconditional or default edge is the forward
	// path, which the spine itself shows.
	branches := map[string][]sop.GraphEdge{}
	for _, e := range edges {
		if e.From == sop.StartNodeName || e.Route == "" {
			continue
		}
		branches[e.From] = append(branches[e.From], e)
	}

	var stages []string
	for _, id := range order {
		if !isReview(id) {
			stages = append(stages, id)
		}
	}

	hasReview := map[string]bool{}
	for _, id := range order {
		if stage, ok := strings.CutSuffix(id, ".review"); ok {
			hasReview[stage] = true
		}
	}

	var lines []string
	for i, id := range stages {
		// The line carries on below this stage only while another follows it.
		more := i < len(stages)-1

		lines = append(lines, stageLine(id, status[id], i == 0, more, innerW, st))

		owner, ownerCol := id, stageGlyphCol(i == 0)
		if hasReview[id] {
			lines = append(lines, edgeLines(branches[id], ownerCol+graphEdgeIndent, more, innerW, st)...)
			review := id + ".review"
			lines = append(lines, reviewLine(status[review], more, innerW, st))
			owner, ownerCol = review, graphReviewCol+2
		}
		lines = append(lines, edgeLines(branches[owner], ownerCol+graphEdgeIndent, more, innerW, st)...)
	}
	return lines
}

// stageGlyphCol is the column a stage's status glyph sits in: the first stage
// heads the line, the rest hang off it.
func stageGlyphCol(first bool) int {
	if first {
		return graphSpineCol
	}
	return graphStageCol
}

// spinePad returns width columns with the vertical line drawn at the spine when
// the flow continues below this row.
func spinePad(width int, more bool) string {
	out := []rune(strings.Repeat(" ", max(width, 0)))
	if more && graphSpineCol < len(out) {
		out[graphSpineCol] = '│'
	}
	return string(out)
}

// isReview reports whether id names a stage's review checkpoint, which the
// compiler keys as "<stage>.review".
func isReview(id string) bool {
	return strings.HasSuffix(id, ".review")
}

// stageLine draws one stage: the first heads the line, the rest hang off it
// with ├, and the last closes it with └.
func stageLine(id string, s stageStatus, first, more bool, innerW int, st sidebarStyles) string {
	col := stageGlyphCol(first)
	room := max(innerW-col-2, 6)
	body := statusStyle(s, st).Render(statusGlyph(s) + " " + truncateLabel(id, room))

	if first {
		return strings.Repeat(" ", graphSpineCol) + body
	}
	elbow := "└─ "
	if more {
		elbow = "├─ "
	}
	return st.overlay.Render(strings.Repeat(" ", graphSpineCol)+elbow) + body
}

// reviewLine draws a stage's review checkpoint under it.
func reviewLine(s stageStatus, more bool, innerW int, st sidebarStyles) string {
	room := max(innerW-graphReviewCol-4, 6)
	return st.overlay.Render(spinePad(graphReviewCol, more)+"└ ") +
		statusStyle(s, st).Render(statusGlyph(s)+" "+truncateLabel("review", room))
}

// edgeLines renders the conditional edges leaving one node, indented under it
// so they read as its outcomes rather than as steps of their own.
func edgeLines(edges []sop.GraphEdge, indent int, more bool, innerW int, st sidebarStyles) []string {
	lines := make([]string, 0, len(edges))
	for _, e := range edges {
		label := truncateLabel(e.To, max(innerW-indent-4, 6))
		lines = append(lines, st.overlay.Render(spinePad(indent, more)+routeMark(e.Route)+"→ "+label))
	}
	return lines
}

func statusGlyph(s stageStatus) string {
	switch s {
	case stageCompleted:
		return "✔"
	case stageRunning:
		return "▶"
	case stageFailed:
		return "✗"
	case stageWaiting:
		return "⏸"
	default:
		return "○"
	}
}

func statusStyle(s stageStatus, st sidebarStyles) lipgloss.Style {
	switch s {
	case stageCompleted:
		return st.green
	case stageRunning:
		return st.peach
	case stageFailed:
		return st.red
	case stageWaiting:
		return st.yellow
	default:
		return st.overlay
	}
}
