// Package mermaid renders Mermaid diagram syntax as Unicode (or ASCII)
// terminal art.
//
// The rendering engine is adapted from github.com/aaronsb/mmaid-go (MIT); see
// ATTRIBUTION.md for the upstream commit and the local changes made since.
//
// Nothing in this package writes to stdout or stderr, so it is safe to call
// from inside the TUI render path. Render recovers from panics in the parser
// and renderer and returns an error string rather than unwinding into the
// caller's View.
package mermaid

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/diagram"
	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/parser"
	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// config holds rendering options.
type config struct {
	useASCII     bool
	paddingX     int
	paddingY     int
	roundedEdges bool
	theme        string // "" = no color, "default", "terra", etc.
	width        int    // 0 = detect from the terminal
}

func defaultConfig() config {
	return config{
		paddingX:     4,
		paddingY:     2,
		roundedEdges: true,
	}
}

// Option configures rendering.
type Option func(*config)

// WithASCII forces ASCII-only output instead of Unicode box-drawing characters.
func WithASCII() Option {
	return func(c *config) { c.useASCII = true }
}

// WithPadding sets horizontal and vertical padding inside node boxes.
func WithPadding(x, y int) Option {
	return func(c *config) {
		c.paddingX = x
		c.paddingY = y
	}
}

// WithSharpEdges disables rounded corners on edge turns.
func WithSharpEdges() Option {
	return func(c *config) { c.roundedEdges = false }
}

// WithWidth sets the width, in columns, that the layout engine targets.
//
// Upstream only exposed this through the CLI, which left a library caller
// with the terminal's own width — wrong for anything drawing into a pane
// narrower than the terminal, which is every TUI with a sidebar. Zero keeps
// the upstream behavior of detecting the terminal.
//
// Note that the engine treats this as a fill target, not a hard cap: a graph
// wide enough to need more columns still gets them. Callers rendering into a
// fixed pane must measure the result and decide what to do when it overflows.
func WithWidth(cols int) Option {
	return func(c *config) {
		if cols > 0 {
			c.width = cols
		}
	}
}

// WithTheme enables colored output with the given theme name.
// Available themes: default, terra, neon, mono, amber, phosphor.
func WithTheme(name string) Option {
	return func(c *config) { c.theme = name }
}

// frontmatterRe matches YAML frontmatter at the start of a document.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\s*\n.*?\n---\s*\n`)

// stripFrontmatter removes YAML frontmatter from the beginning of source.
func stripFrontmatter(source string) string {
	return frontmatterRe.ReplaceAllString(source, "")
}

// detectDiagramType returns the diagram type keyword from the first non-empty line.
func detectDiagramType(source string) string {
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "sequencediagram"):
			return "sequence"
		case strings.HasPrefix(lower, "classdiagram"):
			return "class"
		case strings.HasPrefix(lower, "erdiagram"):
			return "er"
		case strings.HasPrefix(lower, "block"):
			return "block"
		case strings.HasPrefix(lower, "gitgraph"):
			return "gitgraph"
		case strings.HasPrefix(lower, "%%{init") && strings.Contains(lower, "gitgraph"):
			return "gitgraph"
		case strings.HasPrefix(lower, "pie"):
			return "pie"
		case strings.HasPrefix(lower, "treemap"):
			return "treemap"
		case strings.HasPrefix(lower, "statediagram"):
			return "state"
		case strings.HasPrefix(lower, "gantt"):
			return "gantt"
		case strings.HasPrefix(lower, "timeline"):
			return "timeline"
		case strings.HasPrefix(lower, "mindmap"):
			return "mindmap"
		case strings.HasPrefix(lower, "quadrantchart"):
			return "quadrant"
		case strings.HasPrefix(lower, "xychart"):
			return "xychart"
		case strings.HasPrefix(lower, "kanban"):
			return "kanban"
		case strings.HasPrefix(lower, "journey"):
			return "journey"
		case strings.HasPrefix(lower, "packet"):
			return "packet"
		default:
			return "flowchart"
		}
	}
	return "flowchart"
}

// Render renders mermaid syntax as Unicode (or ASCII) art.
//
// It detects the diagram type from the source and dispatches to the appropriate
// parser and renderer. Currently only flowcharts are supported; other diagram
// types return a placeholder message.
func Render(source string, opts ...Option) (result string) {
	// Recover from panics in parser/renderer and return an error message.
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprintf("[mmaid] internal error: %v", r)
		}
	}()

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	source = normalizeNewlines(stripFrontmatter(source))
	dtype := detectDiagramType(source)

	// Get a canvas for any diagram type. The width scope pins the package
	// width the diagram renderers read, so WithWidth reaches all of them and
	// concurrent callers cannot see each other's value.
	var canvas *renderer.Canvas
	diagram.WithWidthScope(cfg.width, func() {
		canvas = renderCanvas(dtype, source, cfg)
	})

	if canvas == nil {
		return ""
	}

	// Apply theme if set, otherwise plain text
	if cfg.theme != "" {
		theme := renderer.GetTheme(cfg.theme)
		return canvas.ToColorString(theme)
	}
	return canvas.ToString()
}

// renderCanvas dispatches to the renderer for dtype. It must be called inside
// a diagram.WithWidthScope so the width-sensitive renderers see cfg.width.
func renderCanvas(dtype, source string, cfg config) *renderer.Canvas {
	var canvas *renderer.Canvas
	switch dtype {
	case "sequence":
		canvas = diagram.RenderSequence(source, cfg.useASCII)
	case "class":
		canvas = diagram.RenderClassDiagram(source, cfg.useASCII)
	case "er":
		canvas = diagram.RenderERDiagram(source, cfg.useASCII)
	case "pie":
		canvas = diagram.RenderPieChart(source, cfg.useASCII, cfg.theme != "", getThemePtr(cfg.theme))
	case "state":
		g := diagram.ParseStateDiagram(source)
		canvas = renderer.RenderGraphCanvas(g, cfg.useASCII, cfg.paddingX, cfg.paddingY, cfg.roundedEdges, diagram.UsableWidth())
	case "block":
		canvas = diagram.RenderBlockDiagram(source, cfg.useASCII)
	case "gitgraph":
		canvas = diagram.RenderGitGraph(source, cfg.useASCII)
	case "treemap":
		canvas = diagram.RenderTreemap(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "gantt":
		canvas = diagram.RenderGantt(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "timeline":
		canvas = diagram.RenderTimeline(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "mindmap":
		canvas = diagram.RenderMindmap(source, cfg.useASCII)
	case "quadrant":
		canvas = diagram.RenderQuadrantChart(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "xychart":
		canvas = diagram.RenderXYChart(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "kanban":
		canvas = diagram.RenderKanban(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "journey":
		canvas = diagram.RenderJourney(source, cfg.useASCII, getThemePtr(cfg.theme))
	case "packet":
		canvas = diagram.RenderPacket(source, cfg.useASCII, getThemePtr(cfg.theme))
	default:
		g := parser.ParseFlowchart(source)
		canvas = renderer.RenderGraphCanvas(g, cfg.useASCII, cfg.paddingX, cfg.paddingY, cfg.roundedEdges, diagram.UsableWidth())
	}

	return canvas
}

func getThemePtr(name string) *renderer.Theme {
	if name == "" {
		return nil
	}
	t := renderer.GetTheme(name)
	return &t
}

// Parse parses mermaid syntax and returns a Graph model.
//
// It detects the diagram type and dispatches to the appropriate parser.
// Currently only flowcharts are supported; other types return an empty graph.
func Parse(source string) *graph.Graph {
	source = normalizeNewlines(stripFrontmatter(source))
	dtype := detectDiagramType(source)

	switch dtype {
	case "flowchart":
		return parser.ParseFlowchart(source)
	default:
		return graph.NewGraph()
	}
}

// Cell is one character of a rendered diagram together with the semantic style
// key the renderer chose for it — "node", "edge", "arrow", "label", and so on.
type Cell struct {
	Char  rune
	Style string

	// Fill is the background style key, empty for most cells. Subgraph
	// interiors and chart bands carry one so a caller can tint the region
	// behind the glyphs rather than only coloring the glyphs themselves.
	Fill string
}

// RenderCells renders source and returns the diagram as rows of styled cells,
// leaving the coloring to the caller.
//
// Render bakes in this package's own ANSI themes, which is right for a CLI and
// wrong for a TUI that has a palette of its own: a diagram drawn in mmaid's
// "default" theme sits in a pane where every other element follows the user's
// chosen theme, and it reads as a foreign object. Cells let the caller map
// each style key onto its own colors instead.
//
// WithTheme is ignored here; the caller is the theme. A source the engine
// cannot draw returns nil.
func RenderCells(source string, opts ...Option) (rows [][]Cell) {
	// Render recovers; this did not — and the TUI calls only this function,
	// from inside the View path, where a panic anywhere in the adapted parsing
	// code would unwind into the render loop and take the session down. The
	// package doc promised panic safety for the package, so the guard was on
	// the wrong function and the claim was wider than the cover.
	defer func() {
		if r := recover(); r != nil {
			rows = nil
		}
	}()

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	source = normalizeNewlines(stripFrontmatter(source))
	dtype := detectDiagramType(source)

	var canvas *renderer.Canvas
	diagram.WithWidthScope(cfg.width, func() {
		canvas = renderCanvas(dtype, source, cfg)
	})
	if canvas == nil {
		return nil
	}

	pairs := canvas.ToStyledPairs()
	rows = make([][]Cell, len(pairs))
	for y, row := range pairs {
		cells := make([]Cell, len(row))
		for x, p := range row {
			cells[x] = Cell{Char: safeCell(p.Char), Style: p.Style, Fill: p.Fill}
		}
		rows[y] = cells
	}
	return rows
}

// safeCell neutralizes any control character before it reaches a caller.
//
// Diagram source is attacker-influenceable: it arrives in a model reply, and a
// model can be steered by a fetched page or a file it was asked to summarize.
// A label carrying OSC 52 writes the viewer's clipboard, OSC 0 rewrites the
// window title, and stray CSI corrupts the alternate screen — none of which a
// diagram should be able to do.
//
// The parser has stripControlChars, but it reaches only the flowchart
// node-label path: edge labels leak, and so do the other sixteen diagram
// types, which have no equivalent. Filtering per parser has already failed
// once inside the flowchart parser itself, so the guard belongs here, at the
// single boundary every renderer's output crosses on its way to a caller.
//
// A blank is the right substitute rather than dropping the rune, because the
// canvas is a fixed grid: removing a cell would shift the rest of the row.
func safeCell(r rune) rune {
	if r == 0 {
		return r // an untouched cell; callers render it as a blank
	}
	if r < 0x20 || r == 0x7f {
		return ' '
	}
	return r
}

// normalizeNewlines folds CRLF and lone CR line endings to LF.
//
// Diagram source does not always arrive with Unix endings: a file authored on
// Windows carries CRLF, and so does anything that took a round trip through a
// tool that rewrote them. Only some of the seventeen parsers tolerate the
// stray CR — the flowchart one does, because it trims each line — while
// others carry it into a label and draw it as a spurious cell. Folding once at
// the entry points means every parser downstream sees the endings it was
// written against.
func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
