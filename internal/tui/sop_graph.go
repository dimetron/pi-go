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

// Layout of one stage group. The diagram is a waterfall: each stage starts one
// column right of the one before it, so the order of the flow is legible from
// the shape alone, without a drawn spine.
const (
	graphBaseIndent  = 2 // first stage
	graphStagePitch  = 1 // added per stage down the waterfall
	graphChildIndent = 2 // a review under its stage, an edge under its owner
	// graphMaxIndent stops the waterfall walking off a 23-column sidebar. A SOP
	// long enough to reach it keeps its remaining stages in one column, which
	// still reads in order.
	graphMaxIndent = 8
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

// sidebarGraphLines draws the compiled SOP as a waterfall of stage groups: the
// stage, the review checkpoint that guards it, and the conditional edges that
// leave either, with a blank row between groups.
//
//	✔ clarify
//	  └ ✔ review
//
//	 ▶ research
//
//	  ○ design
//	    └ ○ review
//
// Sequence is carried by the stagger and the spacing, not by a drawn spine. An
// earlier version put a │ between every pair of stages, which doubled the
// height of a diagram sharing 23 columns with everything else and read as noise
// once the groups were spaced apart.
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
	// path, which the waterfall already shows.
	branches := map[string][]sop.GraphEdge{}
	for _, e := range edges {
		if e.From == sop.StartNodeName || e.Route == "" {
			continue
		}
		branches[e.From] = append(branches[e.From], e)
	}

	var lines []string
	depth := 0
	for i, id := range order {
		if isReview(id) {
			continue // drawn as part of its stage's group
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}

		indent := stageIndent(depth)
		depth++

		lines = append(lines, nodeLine(id, status[id], indent, innerW, st))

		// A review checkpoint always immediately follows its stage in Order.
		owner, ownerIndent := id, indent
		if review := id + ".review"; i+1 < len(order) && order[i+1] == review {
			lines = append(lines, branchLines(branches[id], indent+graphChildIndent, innerW, st)...)
			ownerIndent = indent + graphChildIndent
			lines = append(lines, nodeLine(review, status[review], ownerIndent, innerW, st))
			owner = review
		}
		lines = append(lines, branchLines(branches[owner], ownerIndent+graphChildIndent, innerW, st)...)
	}
	return lines
}

// stageIndent returns the left offset of the nth stage in the waterfall.
func stageIndent(n int) int {
	return graphBaseIndent + min(n*graphStagePitch, graphMaxIndent)
}

// isReview reports whether id names a stage's review checkpoint, which the
// compiler keys as "<stage>.review".
func isReview(id string) bool {
	return strings.HasSuffix(id, ".review")
}

// nodeLine renders one node of the graph. A review checkpoint is drawn as a
// child of the stage it guards — that is how the compiler keys it, and giving
// it a step of its own in the waterfall would overstate it.
func nodeLine(id string, s stageStatus, indent, innerW int, st sidebarStyles) string {
	label := id
	prefix := ""
	if isReview(id) {
		label, prefix = "review", "└ "
	}
	room := max(innerW-indent-len(prefix)-2, 6)
	return statusStyle(s, st).Render(
		strings.Repeat(" ", indent) + prefix + statusGlyph(s) + " " + truncateLabel(label, room))
}

// branchLines renders the conditional edges leaving one node, indented under
// the node they belong to so they read as its outcomes rather than as steps of
// their own.
func branchLines(edges []sop.GraphEdge, indent, innerW int, st sidebarStyles) []string {
	lines := make([]string, 0, len(edges))
	pad := strings.Repeat(" ", indent)
	for _, e := range edges {
		label := truncateLabel(e.To, max(innerW-indent-4, 6))
		lines = append(lines, st.overlay.Render(pad+routeMark(e.Route)+"→ "+label))
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
