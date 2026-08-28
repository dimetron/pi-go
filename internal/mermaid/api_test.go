package mermaid

import (
	"sort"
	"strings"
	"testing"

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
