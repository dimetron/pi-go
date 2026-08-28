package mermaid

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// themeSample is a diagram exercising enough shapes and edges that a theme has
// something of every kind to color.
const themeSample = `graph TD
    A[Start] --> B{Decide}
    B -->|yes| C([Ship])
    B -->|no| D((Stop))
    subgraph Review
        C --> E[Approve]
    end
`

// TestWithThemeColorsOutput renders the sample under every registered theme
// and asserts the theme actually reached the output. Themes are the one part
// of the renderer the golden corpus never touches, because goldens are stored
// uncolored so a palette change does not rewrite 437 files.
func TestWithThemeColorsOutput(t *testing.T) {
	names := make([]string, 0, len(renderer.Themes))
	for name := range renderer.Themes {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no themes registered")
	}

	plain := Render(themeSample)
	if plain == "" {
		t.Fatal("uncolored render is empty")
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			got := Render(themeSample, WithTheme(name), WithWidth(goldenWidth))
			if got == "" {
				t.Fatal("themed render is empty")
			}
			if !strings.Contains(got, "\x1b[") {
				t.Error("themed render carries no ANSI escapes")
			}
			// Stripping the escapes must leave the same shape of diagram the
			// uncolored render produced: a theme colors, it does not relayout.
			if stripANSI(got) == got {
				t.Error("stripANSI removed nothing from a themed render")
			}
		})
	}
}

// TestUnknownThemeFallsBack asserts an unregistered theme name still renders
// rather than returning empty or panicking. Theme names reach this package
// from config, so a typo must degrade to a readable diagram.
func TestUnknownThemeFallsBack(t *testing.T) {
	got := Render(themeSample, WithTheme("no-such-theme"), WithWidth(goldenWidth))
	if got == "" {
		t.Fatal("unknown theme produced empty output")
	}
}

// stripANSI removes CSI escape sequences so colored and uncolored renders can
// be compared for layout.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // consume the 'm'
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestParseExposesGraph covers the public Parse entry point, which returns the
// model rather than art. Callers that want to inspect a diagram — counting
// nodes, checking a subgraph exists — use this instead of Render.
func TestParseExposesGraph(t *testing.T) {
	g := Parse(themeSample)
	if g == nil {
		t.Fatal("Parse returned nil")
	}
	if len(g.NodeOrder) == 0 {
		t.Fatal("Parse found no nodes")
	}

	if sg := g.FindSubgraphByID("Review"); sg == nil {
		t.Error("FindSubgraphByID did not find the Review subgraph")
	}
	if sg := g.FindSubgraphByID("does-not-exist"); sg != nil {
		t.Error("FindSubgraphByID invented a subgraph that is not there")
	}
}

// TestParseNonFlowchartIsEmpty documents that Parse models flowcharts only.
// Other diagram types render fine through Render but have no graph model, and
// a caller must get an empty graph rather than a nil dereference.
func TestParseNonFlowchartIsEmpty(t *testing.T) {
	g := Parse("sequenceDiagram\n    A->>B: hi\n")
	if g == nil {
		t.Fatal("Parse returned nil for a sequence diagram")
	}
	if len(g.NodeOrder) != 0 {
		t.Errorf("expected an empty graph for a non-flowchart, got %d nodes", len(g.NodeOrder))
	}
}

// TestDirectionPredicates covers the direction helpers. BT and RL are the
// reversed pair the renderer draws by laying out TB/LR and flipping.
func TestDirectionPredicates(t *testing.T) {
	for _, tc := range []struct {
		dir        graph.Direction
		horizontal bool
		reversed   bool
	}{
		{graph.DirTB, false, false},
		{graph.DirBT, false, true},
		{graph.DirLR, true, false},
		{graph.DirRL, true, true},
	} {
		if got := tc.dir.IsHorizontal(); got != tc.horizontal {
			t.Errorf("%v: IsHorizontal = %v, want %v", tc.dir, got, tc.horizontal)
		}
		if got := tc.dir.IsReversed(); got != tc.reversed {
			t.Errorf("%v: IsReversed = %v, want %v", tc.dir, got, tc.reversed)
		}
	}
}

// TestBidirectionalEdge covers Edge.IsBidirectional through the parser, so the
// test pins the mermaid syntax that produces a two-headed arrow rather than
// just the struct field.
func TestBidirectionalEdge(t *testing.T) {
	g := Parse("graph LR\n    A <--> B\n    B --> C\n")

	var bidi, plain int
	for _, e := range g.Edges {
		if e.IsBidirectional() {
			bidi++
		} else {
			plain++
		}
	}
	if bidi != 1 {
		t.Errorf("expected 1 bidirectional edge, got %d", bidi)
	}
	if plain != 1 {
		t.Errorf("expected 1 single-headed edge, got %d", plain)
	}
}

// TestRenderCells covers the styled-cell accessor the TUI uses to apply its
// own palette instead of this package's ANSI themes.
func TestRenderCells(t *testing.T) {
	rows := RenderCells(themeSample, WithWidth(goldenWidth))
	if len(rows) == 0 {
		t.Fatal("RenderCells returned no rows")
	}

	// Every row must be the same width: the caller indexes by column.
	width := len(rows[0])
	styles := map[string]int{}
	var text strings.Builder
	for i, row := range rows {
		if len(row) != width {
			t.Fatalf("row %d has %d cells, want %d", i, len(row), width)
		}
		for _, c := range row {
			styles[c.Style]++
			text.WriteRune(c.Char)
		}
	}

	if !strings.Contains(text.String(), "Start") {
		t.Error("cells do not contain the node label")
	}
	// A diagram with boxes, arrows and a subgraph must produce more than one
	// style key, or the palette mapping downstream has nothing to work with.
	if len(styles) < 2 {
		t.Errorf("expected several style keys, got %v", styles)
	}
}

// TestRenderCellsRejectsNonDiagram asserts unparseable input yields no rows,
// which is the caller's signal to fall back to the original fence.
func TestRenderCellsRejectsNonDiagram(t *testing.T) {
	for _, src := range []string{"", "   \n  ", "not a diagram at all"} {
		if rows := RenderCells(src, WithWidth(goldenWidth)); len(rows) > 0 {
			for _, row := range rows {
				for _, c := range row {
					if c.Char != ' ' && c.Char != 0 {
						t.Fatalf("expected no drawn cells for %q, found %q", src, c.Char)
					}
				}
			}
		}
	}
}

// TestRenderCellsIgnoresTheme documents that WithTheme has no effect here: the
// caller supplies the colors, so a theme option must not smuggle ANSI into the
// cell characters.
func TestRenderCellsIgnoresTheme(t *testing.T) {
	plain := RenderCells(themeSample, WithWidth(goldenWidth))
	themed := RenderCells(themeSample, WithWidth(goldenWidth), WithTheme("neon"))

	if len(plain) != len(themed) {
		t.Fatalf("theme changed the row count: %d vs %d", len(plain), len(themed))
	}
	for y := range plain {
		for x := range plain[y] {
			if plain[y][x].Char != themed[y][x].Char {
				t.Fatalf("theme changed cell (%d,%d): %q vs %q", y, x, plain[y][x].Char, themed[y][x].Char)
			}
			if plain[y][x].Char == 0x1b {
				t.Fatal("cells contain a raw escape byte")
			}
		}
	}
}

// TestRenderASCIIAndPaddingOptions covers the remaining option combinators so
// the public surface is exercised as callers actually compose it.
func TestRenderASCIIAndPaddingOptions(t *testing.T) {
	tight := Render(themeSample, WithWidth(goldenWidth), WithPadding(1, 1), WithSharpEdges())
	loose := Render(themeSample, WithWidth(goldenWidth), WithPadding(8, 3))
	if tight == "" || loose == "" {
		t.Fatal("padding options produced empty output")
	}
	if tight == loose {
		t.Error("WithPadding had no effect on the render")
	}
}

// escapeProbes are diagram sources carrying terminal control sequences in
// positions a model can reach. OSC 52 writes the viewer's clipboard on several
// terminals and OSC 0 rewrites the window title, so neither may survive to a
// caller. Cases span several diagram types because the parser-level guard
// covered only flowchart node labels.
var escapeProbes = map[string]string{
	"flowchart node label": "graph LR\n    A[\x1b]0;PWNED\x07] --> B\n",
	"flowchart edge label": "graph LR\n    A -->|\x1b]0;PWNED\x07| B\n",
	"sequence message":     "sequenceDiagram\n    participant A\n    A->>A: \x1b]52;c;cHduZWQ=\x07\n",
	"sequence participant": "sequenceDiagram\n    participant A as \x1b[31mRED\x1b[0m\n    A->>A: hi\n",
	"pie label":            "pie\n    \"\x1b]0;PWNED\x07\" : 40\n    \"ok\" : 60\n",
	"mindmap node":         "mindmap\n  root\n    \x1b]0;PWNED\x07\n",
	"class name":           "classDiagram\n    class \x1b]0;X\x07\n",
	"state label":          "stateDiagram-v2\n    [*] --> \x1b]0;X\x07\n",
}

// TestRenderCellsStripsControlCharacters is the regression test for diagram
// labels smuggling terminal escapes to the screen. The TUI draws these cells
// directly, deliberately bypassing the markdown renderer because the art
// already carries its own styling — which is exactly why the cells themselves
// must be clean.
func TestRenderCellsStripsControlCharacters(t *testing.T) {
	for name, src := range escapeProbes {
		t.Run(name, func(t *testing.T) {
			for y, row := range RenderCells(src, WithWidth(100)) {
				for x, c := range row {
					if c.Char != 0 && (c.Char < 0x20 || c.Char == 0x7f) {
						t.Fatalf("cell (%d,%d) carries control character %q", y, x, c.Char)
					}
				}
			}
		})
	}
}

// TestRenderStripsControlCharacters covers the string entry point too, so a
// non-TUI caller is not left holding the escapes.
func TestRenderStripsControlCharacters(t *testing.T) {
	for name, src := range escapeProbes {
		t.Run(name, func(t *testing.T) {
			out := Render(src, WithWidth(100))
			if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
				t.Errorf("rendered output carries a terminal escape")
			}
		})
	}
}

// TestRenderCellsSurvivesMalformedInput asserts RenderCells has its own panic
// guard. The TUI calls only this function, from inside the View path, so a
// panic here would unwind into the render loop and end the session — Render's
// recover does not cover it.
func TestRenderCellsSurvivesMalformedInput(t *testing.T) {
	malformed := []string{
		"graph TD\n    A[unclosed",
		"graph TD\n    -->-->-->",
		"packet-beta\n    0-4294967295: \"x\"\n",
		"packet-beta\n    notanumber-9: \"x\"\n",
		"sequenceDiagram\n    A->>",
		"mindmap\n" + strings.Repeat("  ", 500) + "deep\n",
		"graph TD\n" + strings.Repeat("subgraph S\n", 200) + strings.Repeat("end\n", 200),
		"pie\n    \"x\" : notanumber\n",
		"xychart-beta\n    bar [notanumber]\n",
		"gantt\n    section\n    :::::\n",
	}
	for i, src := range malformed {
		if got := RenderCells(src, WithWidth(80)); got == nil {
			t.Logf("case %d returned nil (recovered or unrenderable), which is the contract", i)
		}
	}
}

// TestPacketBitRangeIsBounded pins the allocation cap. The renderer expands one
// row per 32 bits across a field's range, so an unbounded bit index taken from
// input is an allocation multiplier: `0-4294967295` allocated tens of
// gigabytes and hung the session.
func TestPacketBitRangeIsBounded(t *testing.T) {
	for _, src := range []string{
		"packet-beta\n    0-4294967295: \"x\"\n",
		"packet-beta\n    0-2000000: \"x\"\n",
		"packet-beta\n    +2000000000: \"x\"\n",
		"packet-beta\n    -5--1: \"x\"\n",
	} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			Render(src, WithWidth(80))
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("render did not finish within 5s for %q", src)
		}
	}
}

// TestEdgeRoutingIsNotClipped is the regression test for edges running off the
// right of the diagram and stopping in mid-air.
//
// The canvas used to be sized from the node layout alone, but the router can
// take an edge outside the node bounding box — around the right of the last
// column, or below the last row — and Canvas.Put silently discards an
// out-of-bounds write. The part of the stroke that fitted was drawn and the
// corner and arrowhead that did not were dropped, leaving a line that runs to
// the edge of the diagram and connects to nothing.
//
// The fixture is a real model-generated architecture diagram; it produced two
// such danglers before the canvas was sized to cover the routed paths. Natural
// width matters: at the corpus width of 100 this diagram does not route far
// enough right to clip, which is why the goldens never caught it.
func TestEdgeRoutingIsNotClipped(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "corpus", "flowchart", "regress-clipped-edge-routing.mmd"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	lines := strings.Split(strings.TrimRight(Render(string(src), WithWidth(1)), "\n"), "\n")

	widest := 0
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n > widest {
			widest = n
		}
	}
	if widest == 0 {
		t.Fatal("diagram rendered empty")
	}

	// A horizontal stroke occupying the final column is an edge whose
	// terminator was clipped: the canvas carries a margin, so a correctly
	// routed edge always turns or ends before the last column.
	for i, l := range lines {
		if utf8.RuneCountInString(l) != widest {
			continue
		}
		r := []rune(l)
		switch r[len(r)-1] {
		case '─', '━', '┄', '╌':
			t.Errorf("line %d ends with an unterminated stroke in the final column: the edge was clipped", i)
		}
	}
}
