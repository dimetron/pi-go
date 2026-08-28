package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dimetron/pi-go/internal/mermaid"
)

// mermaidStyleFor maps a diagram's semantic style key onto the active palette.
//
// The goal is that the three structural roles read apart at a glance, because
// a diagram where boxes, connectors and groups share one grey is just a texture:
//
//   - a box is chrome, so it takes the neutral Overlay grey and stays back;
//   - its text is the content, so it takes the full-strength Text;
//   - a connector is flow, so it takes Primary — a hue, not a grey, which is
//     what lets the eye trace an edge across a crowded diagram;
//   - a group is an enclosure, so its border and its title share Accent, which
//     ties a caption to the box it names;
//   - an edge label is an annotation on a connector, so it takes Peach: warm,
//     and distinct from both the blue it sits on and the grey behind it.
//
// The renderer emits no "arrow" key — arrowheads are drawn as "edge" — so
// arrowheads inherit the connector color, which is what we want anyway.
func mermaidStyleFor(key string, p Palette) lipgloss.Style {
	base := lipgloss.NewStyle()
	switch key {
	case "node":
		return base.Foreground(p.Overlay)
	case "label":
		return base.Foreground(p.Text)
	case "bold_label":
		return base.Foreground(p.Text).Bold(true)
	case "italic_label":
		return base.Foreground(p.Text).Italic(true)
	case "edge":
		return base.Foreground(p.Primary)
	case "edge_label":
		return base.Foreground(p.Peach)
	case "subgraph":
		return base.Foreground(p.Accent)
	case "subgraph_label":
		return base.Foreground(p.Accent).Bold(true)
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

	if art, ok := renderMermaidAt(source, width, p); ok {
		return art
	}

	// Too wide as written. A flowchart's direction is the single biggest lever
	// on its width — a fan-out of fifteen children puts them all in one row
	// under TD and in one column under LR — so try the perpendicular
	// orientation before giving up. It trades columns for rows, which is the
	// right trade in a pane that scrolls vertically and not horizontally.
	if swapped, ok := swapFlowchartDirection(source); ok {
		if art, ok := renderMermaidAt(swapped, width, p); ok {
			return art
		}
	}

	return ""
}

// renderMermaidAt renders source at width and reports whether the result fits.
func renderMermaidAt(source string, width int, p Palette) (string, bool) {
	// Tight padding. The engine's CLI defaults (4, 2) suit a diagram that owns
	// the whole terminal; in a chat pane the same diagram reads as mostly empty
	// boxes, and every blank row is a row of transcript the reader has to
	// scroll past. (1, 0) keeps one column of breathing room either side of a
	// label and drops the blank rows above and below it, which is about a fifth
	// off the area for no loss of legibility.
	//
	// It is padding rather than the layout constants because those do not pay:
	// MaxNormalizedHeight changes nothing measurable, and the one lever that
	// would give a large win — cutting Stride from 4 to 3 — leaves too little
	// room between node centers for subgraph borders and edge routing, so
	// borders start cutting through boxes and labels.
	rows := mermaid.RenderCells(source,
		mermaid.WithWidth(width),
		mermaid.WithPadding(1, 0),
	)
	if len(rows) == 0 {
		return "", false
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

	if widest == 0 || widest > width {
		return "", false
	}
	return strings.TrimRight(b.String(), "\n"), true
}

// flowchartHeaderRe matches a flowchart header and captures its direction, so
// the direction alone can be rewritten without disturbing the rest of the line.
var flowchartHeaderRe = regexp.MustCompile(`(?i)^(\s*(?:graph|flowchart)\s+)(TB|TD|BT|LR|RL)(\s*.*)$`)

// perpendicular maps each flowchart direction to the one at right angles to it,
// preserving the axis polarity so a reversed diagram stays reversed.
var perpendicular = map[string]string{
	"TB": "LR", "TD": "LR", "BT": "RL",
	"LR": "TB", "RL": "BT",
}

// swapFlowchartDirection rewrites a flowchart's declared direction to the
// perpendicular one, reporting whether it found a direction to swap.
//
// Only the header is touched. A `direction` statement inside a subgraph sets
// that subgraph's internal flow, not the diagram's, and rewriting those would
// change the drawing rather than merely reorient it.
func swapFlowchartDirection(source string) (string, bool) {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}

		m := flowchartHeaderRe.FindStringSubmatch(line)
		if m == nil {
			return "", false // first real line is not a flowchart header
		}
		swapped, ok := perpendicular[strings.ToUpper(m[2])]
		if !ok {
			return "", false
		}
		lines[i] = m[1] + swapped + m[3]
		return strings.Join(lines, "\n"), true
	}
	return "", false
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

// stableFenceLang rewrites a mermaid fence's info string to "text".
//
// Chroma ships no mermaid lexer, so glamour falls back to guessing the
// language from the content — and that guess is not stable. Several lexers
// score equally on a large diagram body and the winner is decided by map
// iteration order, so the block is re-colored slightly differently on every
// repaint. Measured on a real 171-line diagram: the ```mermaid fence differed
// between renders 48 times out of 60, an untagged fence 10 times out of 60,
// and a fence tagged with a language Chroma knows, zero.
//
// Since the pane re-renders on every blink tick, that shows up as a diagram
// that quietly shimmers. Naming a lexer Chroma has stops the guessing. Nothing
// is lost visually: glamour does not display the language name.
func stableFenceLang(raw string) string {
	nl := strings.IndexByte(raw, '\n')
	if nl < 0 {
		return raw
	}
	open := raw[:nl]
	indent := open[:len(open)-len(strings.TrimLeft(open, " \t"))]
	return indent + "```text" + raw[nl:]
}

// mermaidSegment is one piece of a message: either markdown to hand to
// glamour, or the body of a closed ```mermaid fence to draw as a diagram.
type mermaidSegment struct {
	raw     string // the original text, fence markers included
	diagram string // the fence body; empty when this segment is not a diagram
}

// splitMermaidFences splits text into markdown and mermaid-fence segments.
//
// Only *closed* fences become diagrams. While a reply is streaming, the last
// fence has no closing marker yet, and treating it as a diagram would mean
// re-parsing a growing fragment on every token and flashing half-drawn art at
// the reader. An unterminated fence stays markdown until its closing line
// arrives, at which point the message re-renders once as a diagram.
func splitMermaidFences(text string) []mermaidSegment {
	lines := strings.Split(text, "\n")

	var (
		segments []mermaidSegment
		markdown []string
		fence    []string
		inFence  bool
	)

	flushMarkdown := func() {
		if len(markdown) > 0 {
			segments = append(segments, mermaidSegment{raw: strings.Join(markdown, "\n")})
			markdown = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inFence {
			if isMermaidFenceOpen(trimmed) {
				inFence = true
				fence = []string{line}
				continue
			}
			markdown = append(markdown, line)
			continue
		}

		fence = append(fence, line)
		if trimmed == "```" {
			flushMarkdown()
			// The body excludes the opening and closing marker lines.
			body := strings.Join(fence[1:len(fence)-1], "\n")
			segments = append(segments, mermaidSegment{
				raw:     strings.Join(fence, "\n"),
				diagram: body,
			})
			inFence = false
			fence = nil
		}
	}

	// An unclosed fence is still streaming: hand it back as markdown.
	if inFence {
		markdown = append(markdown, fence...)
	}
	flushMarkdown()

	return segments
}

// isMermaidFenceOpen reports whether a trimmed line opens a mermaid fence.
// The info string may carry attributes after the language, as some authoring
// tools emit (```mermaid {theme=dark}), so only the first word is compared.
func isMermaidFenceOpen(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "```") {
		return false
	}
	info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
	if info == "" {
		return false
	}
	lang, _, _ := strings.Cut(info, " ")
	return strings.EqualFold(lang, "mermaid")
}
