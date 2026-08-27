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

// sidebarGraphLines draws the compiled SOP as a vertical diagram: one line per
// stage down the spine, with conditional edges hanging off it.
//
//	✔ validate_spec
//	│
//	▶ slices
//	├─✗→ repair
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

	// Conditional edges only. An unconditional or default edge is the spine
	// itself, drawn as the connector between consecutive stages.
	branches := map[string][]sop.GraphEdge{}
	for _, e := range edges {
		if e.From == sop.StartNodeName || e.Route == "" {
			continue
		}
		branches[e.From] = append(branches[e.From], e)
	}

	lines := make([]string, 0, len(order)*2)
	for i, id := range order {
		lines = append(lines, stageLine(id, status[id], innerW, st))

		outgoing := branches[id]
		for j, e := range outgoing {
			connector := "├─"
			if j == len(outgoing)-1 {
				connector = "└─"
			}
			label := truncateLabel(e.To, max(innerW-8, 6))
			lines = append(lines, st.overlay.Render(
				"  "+connector+routeMark(e.Route)+"→ "+label))
		}

		// No connector between a stage and its own review: the review hangs
		// off the stage, it is not the next step down the spine.
		if i < len(order)-1 && order[i+1] != id+".review" {
			lines = append(lines, st.overlay.Render("  │"))
		}
	}
	return lines
}

// stageLine renders one node. A review checkpoint is drawn as a child of the
// stage it belongs to — that is how the compiler keys it ("<id>.review"), and
// showing it inline would read as a stage of its own.
func stageLine(id string, s stageStatus, innerW int, st sidebarStyles) string {
	if _, ok := strings.CutSuffix(id, ".review"); ok {
		label := truncateLabel("review", max(innerW-6, 6))
		return statusStyle(s, st).Render("  └ " + statusGlyph(s) + " " + label)
	}
	label := truncateLabel(id, max(innerW-4, 8))
	return statusStyle(s, st).Render("  " + statusGlyph(s) + " " + label)
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
