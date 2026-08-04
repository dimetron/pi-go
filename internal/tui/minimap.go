package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// railWidth is how many columns the minimap occupies on the right edge of the
// left panel: exactly one, the same as the separator it replaces. The minimap
// and the divider are the same rail, so they are the same width — anything else
// makes the rule appear to change thickness as you scroll.
//
// The minimap *is* the panel separator rather than a strip beside it. Drawing
// both put two vertical rules side by side — the scroll bar and the border —
// which read as one overloaded gutter. One rail carries both jobs: it divides
// the panels, and its color and weight say what is where in the conversation.
const railWidth = 1

// Rail glyphs. The track is a thin rule; the scroll position is a round thumb
// sitting on top of it, which reads as a scrollbar rather than as a heavy block
// cut into the divider.
//
// The thumb is ◉ (U+25C9) rather than ● (U+25CF): they read the same at terminal
// sizes, but ● is East Asian Ambiguous and renders two cells wide wherever the
// terminal resolves ambiguous width as wide, which pushes the rail out of its
// column for exactly the three thumb rows. See widthSafeGlyphs in
// render_integrity_test.go.
const (
	railGlyph = "│" // track
	railThumb = "◉" // the thumb, i.e. where you are
	railFoot  = "┴" // the joint where the rail meets the panel's closing rule
)

// thumbRows is the thumb's fixed height. A thumb sized to the visible fraction
// swells to fill the whole rail on a short conversation and shrinks to nothing
// on a long one; a fixed three dots stays legible at any length and reads
// unambiguously as a scroll position.
const thumbRows = 3

// thumbRange returns the rows the thumb covers: three rows centered on where the
// viewport sits in the conversation, clamped to the rail. A rail shorter than
// the thumb is filled entirely.
func thumbRange(start, end, total, rows int) (lo, hi int) {
	if rows <= thumbRows || total <= 0 {
		return 0, rows
	}

	// Center the thumb on the middle of the visible slice.
	mid := (start + end) / 2
	center := mid * rows / total

	lo = center - thumbRows/2
	lo = max(lo, 0)
	lo = min(lo, rows-thumbRows)
	return lo, lo + thumbRows
}

// separatorColor matches the sidebar's old border (Mocha surface1), so rows with
// no conversation behind them still read as a plain divider.
const separatorColor = "#45475a"

// railCell renders one rail cell: glyph repeated to fill railWidth, in color.
func railCell(glyph, color string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(color)).
		Render(strings.Repeat(glyph, railWidth))
}

// separatorCell is the rail outside the message viewport — above and below the
// chat, where there is nothing to map.
func separatorCell() string {
	return railCell(railGlyph, separatorColor)
}

// ruleColor is the color of the horizontal rules that close the panel and frame
// the input (Mocha surface2).
const ruleColor = "#585b70"

// railFootCell is the rail's last cell, the one that sits on the panel's closing
// rule. A vertical glyph there cuts the rule in half where the panel meets the
// sidebar; the joint carries the line through instead, so the panel's rule, this
// cell and the sidebar's rule read as one line across the terminal.
func railFootCell() string {
	return railCell(railFoot, ruleColor)
}

// blockKind classifies a rendered line by the kind of message it came from.
// It is what gives the minimap its colors.
type blockKind uint8

const (
	blockNone blockKind = iota // padding, or a blank line between blocks
	blockUser
	blockAssistant
	blockWarning
	blockError
	blockThinking
	blockRead    // read, ls, tree, grep, find — non-mutating inspection
	blockEdit    // edit, write — file mutations
	blockExecute // bash, shell
	blockAgent   // subagent dispatch
	blockTool    // any other tool, incl. MCP
)

// minimapColors maps each block kind to its bar color, as an ANSI 256 index.
// Mutations and commands — the lines a reader most often scrolls back to find —
// get the warm, high-contrast end of the palette; passive content stays cool
// and recessive. Colors echo the ones each block already uses in the chat, so
// the strip reads as a scaled-down copy rather than a separate legend.
var minimapColors = map[blockKind]string{
	blockUser:      "39",  // blue, matches the "> " prompt label
	blockAssistant: "63",  // violet, matches the reply bullet
	blockWarning:   "226", // yellow, matches the warning bullet
	blockError:     "203", // red, matches the error bullet
	blockThinking:  "243", // gray, matches the thinking style
	blockRead:      "45",  // cyan
	blockEdit:      "208", // orange
	blockExecute:   "76",  // green
	blockAgent:     "141", // purple
	blockTool:      "102", // muted slate
	blockNone:      "237", // near-background track
}

// kindOf classifies a message for the minimap.
func kindOf(msg *message) blockKind {
	switch msg.role {
	case "user":
		return blockUser
	case "thinking":
		return blockThinking
	case "assistant":
		if msg.isError {
			return blockError
		}
		if msg.isWarning {
			return blockWarning
		}
		return blockAssistant
	case "tool":
		return toolBlockKind(msg.tool)
	}
	return blockNone
}

func toolBlockKind(tool string) blockKind {
	switch tool {
	case "read", "ls", "tree", "grep", "find", "glob":
		return blockRead
	case "edit", "write":
		return blockEdit
	case "bash", "shell":
		return blockExecute
	case "agent", "subagent":
		return blockAgent
	}
	return blockTool
}

// renderMinimap builds the minimap column, one styled cell per viewport row.
//
// The whole conversation is squeezed into rows cells, so the strip is a map of
// the entire scrollback rather than only what is on screen — that is the point
// of it. Rows covering the lines currently displayed are drawn as a full block;
// the rest are drawn as a thin bar, which is what makes the viewport's position
// in the conversation readable at a glance.
//
// kinds is the per-line classification of the full rendered chat; start and end
// bound the slice of it currently visible.
func renderMinimap(kinds []blockKind, start, end, rows int) []string {
	if rows <= 0 {
		return nil
	}

	cells := make([]string, rows)
	total := len(kinds)
	if total == 0 {
		// No content yet: draw the empty track, so the column never appears or
		// disappears from under the user.
		empty := railCell(railGlyph, minimapColors[blockNone])
		for i := range cells {
			cells[i] = empty
		}
		return cells
	}

	thumbLo, thumbHi := thumbRange(start, end, total, rows)

	for row := range rows {
		lo := row * total / rows
		hi := (row + 1) * total / rows
		if hi <= lo {
			hi = lo + 1
		}
		if hi > total {
			hi = total
		}

		kind := dominantKind(kinds[lo:hi])

		glyph, color := railGlyph, minimapColors[kind]
		if row >= thumbLo && row < thumbHi {
			glyph = railThumb
			if kind == blockNone {
				// A thumb over a run of blank lines still has to read as "you
				// are here", so brighten the track rather than leaving a hole
				// in it.
				color = "244"
			}
		}
		cells[row] = railCell(glyph, color)
	}
	return cells
}

// dominantKind returns the most common non-blank kind in a run of lines, so a
// row that mostly covers a tool block reads as that tool even when it also
// catches a blank separator line. Ties go to the first kind seen, which keeps
// the strip stable as content grows.
func dominantKind(kinds []blockKind) blockKind {
	var counts [blockTool + 1]int
	for _, k := range kinds {
		counts[k]++
	}

	best, bestCount := blockNone, 0
	for k := blockUser; k <= blockTool; k++ {
		if counts[k] > bestCount {
			best, bestCount = k, counts[k]
		}
	}
	return best
}

// railColumn renders the rail as its own column block, one row per panel row:
// the minimap alongside the message viewport, a plain separator everywhere else
// (the header, status bar, and prompt), so the divider runs unbroken top to
// bottom while still carrying the map.
//
// It is a block rather than a cell glued onto each panel line so the layout
// composes with lipgloss.JoinHorizontal like every other column, instead of
// depending on the panel's own line lengths being right.
//
// The panel's last row is its closing rule, so the rail's last cell is the joint
// that carries that rule across into the sidebar's.
//
// rows is the panel's height; msgStart is the row the message viewport begins
// on; cells are the minimap cells for it, one per row.
func railColumn(rows, msgStart int, cells []string) string {
	sep := separatorCell()

	out := make([]string, rows)
	for i := range out {
		out[i] = sep
		if row := i - msgStart; row >= 0 && row < len(cells) {
			out[i] = cells[row]
		}
	}
	if rows > 0 {
		out[rows-1] = railFootCell()
	}
	return strings.Join(out, "\n")
}

// tabStop is the column interval a terminal advances a tab to.
const tabStop = 8

// sanitizeCells rewrites a line so the columns it is measured at are the columns
// the terminal actually draws it at.
//
// Two characters lie about their width. A tab measures zero cells but moves the
// cursor to the next multiple-of-8 column — a single `go test` FAIL line carries
// two of them and lands up to 14 cells right of where the width math put it. A
// carriage return or backspace measures zero as well and moves the cursor
// backwards. Either one makes the row the terminal paints wider than the row we
// padded, and the overflow shoves the sidebar off the right edge — which is what
// made it look like the chat had eaten the sidebar's columns.
//
// ESC is left alone: it opens the SGR sequences that carry the colors, and those
// are already measured as zero width because they really are.
func sanitizeCells(line string) string {
	if !strings.ContainsFunc(line, isMiscountedControl) {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	col := 0
	for _, r := range line {
		switch {
		case r == '\t':
			pad := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", pad))
			col += pad
		case isMiscountedControl(r):
			// Drop it: there is no column it can be drawn in honestly.
		default:
			b.WriteRune(r)
			col += ansi.StringWidth(string(r))
		}
	}
	return b.String()
}

// isMiscountedControl reports whether r is a control character that measures
// zero cells but still moves the cursor. ESC (0x1b) is excluded — the SGR
// sequences it opens genuinely occupy no columns.
func isMiscountedControl(r rune) bool {
	return r == '\t' || (r < 0x20 && r != 0x1b) || r == 0x7f
}

// padLine makes a single line exactly width cells wide, measuring in terminal
// cells rather than bytes so ANSI styling and wide runes do not skew it.
func padLine(line string, width int) string {
	line = sanitizeCells(line)
	if width <= 0 {
		return line
	}
	w := ansi.StringWidth(line)
	switch {
	case w > width:
		return ansi.Truncate(line, width, "")
	case w < width:
		return line + strings.Repeat(" ", width-w)
	}
	return line
}

// padLinesTo fixes a panel to exactly width columns.
//
// Without this the panel is only as wide as its widest visible line, so
// lipgloss.JoinHorizontal placed the sidebar at a column that moved every time
// scrolling brought a longer or shorter line into view. Pinning the width keeps
// the sidebar still.
func padLinesTo(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = padLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// joinPanelSidebar lays the sidebar beside the panel, one row at a time, with
// each side clamped to the columns it owns.
//
// lipgloss.JoinHorizontal sizes the left block to its widest row and starts the
// sidebar after it, so a single row that renders wider than the panel — one
// stray control character is enough — moves the sidebar right on *every* row and
// pushes its tail off the screen. The sidebar then reads as narrower than it is:
// the columns are still there, they are just past the right edge. Clamping each
// row to leftWidth here means the sidebar always begins at the same column and
// always keeps all sidebarWidth of them, whatever the chat put on the row.
func joinPanelSidebar(left, sidebar string, leftWidth, sidebarWidth int) string {
	leftRows := strings.Split(left, "\n")
	sideRows := strings.Split(sidebar, "\n")
	rows := max(len(leftRows), len(sideRows))

	out := make([]string, rows)
	for i := range out {
		var l, s string
		if i < len(leftRows) {
			l = leftRows[i]
		}
		if i < len(sideRows) {
			s = sideRows[i]
		}
		out[i] = padLine(l, leftWidth) + padLine(s, sidebarWidth)
	}
	return strings.Join(out, "\n")
}
