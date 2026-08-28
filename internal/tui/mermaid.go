package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dimetron/pi-go/internal/mermaid"
)

// mermaidStyleFor maps a diagram's semantic style key onto the active palette.
//
// The mapping is chosen so a diagram reads like the rest of the pane rather
// than like a picture pasted into it: node borders take the same Surface the
// separators use, labels take Text, and edges take Dim so the arrows recede
// behind the boxes they connect. Accent marks the subgraph title only — it is
// the reply bullet's color, and spending it on every arrow would make a
// diagram shout over the message it illustrates.
func mermaidStyleFor(key string, p Palette) lipgloss.Style {
	base := lipgloss.NewStyle()
	switch key {
	case "node":
		return base.Foreground(p.Surface)
	case "label":
		return base.Foreground(p.Text)
	case "bold_label":
		return base.Foreground(p.Text).Bold(true)
	case "italic_label":
		return base.Foreground(p.Text).Italic(true)
	case "edge":
		return base.Foreground(p.Dim)
	case "arrow":
		return base.Foreground(p.Primary)
	case "edge_label":
		return base.Foreground(p.Subtext)
	case "subgraph":
		return base.Foreground(p.Overlay)
	case "subgraph_label":
		return base.Foreground(p.Accent)
	case "note":
		return base.Foreground(p.Yellow)
	default:
		return base.Foreground(p.Subtext)
	}
}

// RenderMermaid renders mermaid source as terminal art styled with the pane's
// palette, wrapped to width. It returns "" when the source is not a diagram
// this renderer models, which is the caller's signal to fall back to showing
// the original fence.
//
// Width is a fill target, not a hard cap — the layout engine will exceed it
// for a graph that genuinely needs more columns — so the result is measured
// and rejected if it overflows. A diagram wider than the pane is worse than no
// diagram: the terminal cannot scroll sideways, so it renders as ragged,
// truncated lines with no way to see the rest.
func RenderMermaid(source string, width int, p Palette) string {
	if width <= 0 {
		return ""
	}

	rows := mermaid.RenderCells(source, mermaid.WithWidth(width))
	if len(rows) == 0 {
		return ""
	}

	pal := paletteOrDark(p)
	var b strings.Builder
	widest := 0

	for i, row := range rows {
		line := styleRow(row, pal)
		if n := lipgloss.Width(line); n > widest {
			widest = n
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}

	if widest > width {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

// styleRow renders one row of cells, coalescing runs that share a style key
// into a single lipgloss call. Styling per rune would emit a set-and-reset
// escape pair around every character — correct, but it multiplies the size of
// a diagram several times over and every one of those bytes is re-scanned by
// the wrapping and highlighting passes downstream.
func styleRow(row []mermaid.Cell, p Palette) string {
	// Trailing blanks carry no style and only widen the line.
	end := len(row)
	for end > 0 && (row[end-1].Char == ' ' || row[end-1].Char == 0) {
		end--
	}
	if end == 0 {
		return ""
	}

	var b strings.Builder
	var run strings.Builder
	runKey := row[0].Style

	flush := func() {
		if run.Len() == 0 {
			return
		}
		b.WriteString(mermaidStyleFor(runKey, p).Render(run.String()))
		run.Reset()
	}

	for _, cell := range row[:end] {
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		if cell.Style != runKey {
			flush()
			runKey = cell.Style
		}
		run.WriteRune(ch)
	}
	flush()

	return b.String()
}
