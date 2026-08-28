package parser

import (
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

func TestParseSimpleLR(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A --> B")
	if g.Direction != graph.DirLR {
		t.Errorf("expected LR, got %s", g.Direction)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(g.Edges))
	}
}

func TestParseDirections(t *testing.T) {
	tests := []struct {
		input string
		want  graph.Direction
	}{
		{"graph TB\n  A --> B", graph.DirTB},
		{"graph TD\n  A --> B", graph.DirTD},
		{"graph LR\n  A --> B", graph.DirLR},
		{"graph BT\n  A --> B", graph.DirBT},
		{"graph RL\n  A --> B", graph.DirRL},
		{"flowchart LR\n  A --> B", graph.DirLR},
	}
	for _, tt := range tests {
		g := ParseFlowchart(tt.input)
		if g.Direction != tt.want {
			t.Errorf("input %q: expected %s, got %s", tt.input, tt.want, g.Direction)
		}
	}
}

func TestParseNodeShapes(t *testing.T) {
	tests := []struct {
		input string
		shape graph.NodeShape
	}{
		{"graph LR\n  A[rect]", graph.ShapeRectangle},
		{"graph LR\n  A(round)", graph.ShapeRounded},
		{"graph LR\n  A{diamond}", graph.ShapeDiamond},
		{"graph LR\n  A((circle))", graph.ShapeCircle},
		{"graph LR\n  A([stadium])", graph.ShapeStadium},
		{"graph LR\n  A[[sub]]", graph.ShapeSubroutine},
		{"graph LR\n  A{{hex}}", graph.ShapeHexagon},
		{"graph LR\n  A[(cyl)]", graph.ShapeCylinder},
	}
	for _, tt := range tests {
		g := ParseFlowchart(tt.input)
		if n, ok := g.Nodes["A"]; !ok {
			t.Errorf("input %q: node A not found", tt.input)
		} else if n.Shape != tt.shape {
			t.Errorf("input %q: expected shape %d, got %d", tt.input, tt.shape, n.Shape)
		}
	}
}

func TestParseEdgeStyles(t *testing.T) {
	tests := []struct {
		input string
		style graph.EdgeStyle
	}{
		{"graph LR\n  A --> B", graph.EdgeSolid},
		{"graph LR\n  A -.-> B", graph.EdgeDotted},
		{"graph LR\n  A ==> B", graph.EdgeThick},
	}
	for _, tt := range tests {
		g := ParseFlowchart(tt.input)
		if len(g.Edges) != 1 {
			t.Errorf("input %q: expected 1 edge, got %d", tt.input, len(g.Edges))
			continue
		}
		if g.Edges[0].Style != tt.style {
			t.Errorf("input %q: expected style %d, got %d", tt.input, tt.style, g.Edges[0].Style)
		}
	}
}

func TestParseEdgeLabel(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A -->|yes| B")
	if len(g.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(g.Edges))
	}
	if g.Edges[0].Label != "yes" {
		t.Errorf("expected label 'yes', got %q", g.Edges[0].Label)
	}
}

func TestParseChainedArrows(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A --> B --> C")
	if len(g.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(g.Edges))
	}
}

func TestParseAmpersand(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A & B --> C")
	if len(g.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 2 {
		t.Errorf("expected 2 edges (A->C, B->C), got %d", len(g.Edges))
	}
}

func TestParseSubgraph(t *testing.T) {
	g := ParseFlowchart(`graph TB
    subgraph sg1 [My Group]
        A --> B
    end`)
	if len(g.Subgraphs) != 1 {
		t.Fatalf("expected 1 subgraph, got %d", len(g.Subgraphs))
	}
	if g.Subgraphs[0].Label != "My Group" {
		t.Errorf("expected label 'My Group', got %q", g.Subgraphs[0].Label)
	}
	if len(g.Subgraphs[0].NodeIDs) != 2 {
		t.Errorf("expected 2 nodes in subgraph, got %d", len(g.Subgraphs[0].NodeIDs))
	}
}

func TestParseClassDef(t *testing.T) {
	g := ParseFlowchart("graph LR\n  classDef red fill:#f00,color:#fff\n  A:::red --> B")
	if _, ok := g.ClassDefs["red"]; !ok {
		t.Error("classDef 'red' not found")
	}
	if n, ok := g.Nodes["A"]; !ok {
		t.Error("node A not found")
	} else if n.StyleClass != "red" {
		t.Errorf("expected style class 'red', got %q", n.StyleClass)
	}
}

func TestParseSemicolons(t *testing.T) {
	g := ParseFlowchart("graph LR; A --> B; B --> C; C --> D")
	if len(g.Nodes) != 4 {
		t.Errorf("expected 4 nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 3 {
		t.Errorf("expected 3 edges, got %d", len(g.Edges))
	}
}

func TestParseComments(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A --> B %% this is a comment\n  B --> C")
	if len(g.Edges) != 2 {
		t.Errorf("expected 2 edges (comment stripped), got %d", len(g.Edges))
	}
}

func TestParseBidirectional(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A <--> B")
	if len(g.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(g.Edges))
	}
	e := g.Edges[0]
	if !e.HasArrowStart || !e.HasArrowEnd {
		t.Error("expected bidirectional edge")
	}
}

func TestParseNodeLabel(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A[Hello World]")
	if n, ok := g.Nodes["A"]; !ok {
		t.Error("node A not found")
	} else if n.Label != "Hello World" {
		t.Errorf("expected label 'Hello World', got %q", n.Label)
	}
}

func TestParseLinkStyle(t *testing.T) {
	g := ParseFlowchart("graph LR\n  A --> B\n  linkStyle 0 stroke:#ff0")
	if _, ok := g.LinkStyles[0]; !ok {
		t.Error("linkStyle 0 not found")
	}
}

func TestSanitizeLabel_StripsControlChars(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal text", "normal text"},
		{"has\ttab", "has tab"},
		{"has\x1b[31mANSI\x1b[0m", "hasANSI"},
		{"null\x00byte", "nullbyte"},
		{"line\nbreak", "linebreak"},    // real newline stripped by stripControlChars
		{`line\nbreak`, "line break"},   // literal \n replaced by sanitizeLabel
		{"html<br>break", "html break"}, // <br> → space
	}
	for _, tt := range tests {
		got := sanitizeLabel(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripControlChars(t *testing.T) {
	// Full ANSI escape sequences should be stripped entirely.
	input := "before\x1b[31mred\x1b[0mafter"
	got := stripControlChars(input)
	want := "beforeredafter"
	if got != want {
		t.Errorf("stripControlChars(%q) = %q, want %q", input, got, want)
	}
}

// TestParseNodeFoldsHTMLBreaks pins the fix for shape detection running before
// HTML line breaks were folded.
//
// `<br/>` carries a `/` and a `>`, both of which the shape patterns match on.
// In `b["with<br/>break"]` the asymmetric `>`…`]` pattern matched the `>`
// inside the tag, so the node ID came out as `b["with<br/`, the label as
// `break"`, and the box was drawn as a parallelogram. `<br>` in a label is
// ordinary GitHub-flavored Mermaid, so this hit real diagrams.
func TestParseNodeFoldsHTMLBreaks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		label string
	}{
		{"br slash", `graph TD` + "\n" + `b["with<br/>break"]`, "with break"},
		{"br plain", `graph TD` + "\n" + `b["with<br>break"]`, "with break"},
		{"br spaced", `graph TD` + "\n" + `b["with<br />break"]`, "with break"},
		{"br upper", `graph TD` + "\n" + `b["with<BR/>break"]`, "with break"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := ParseFlowchart(tc.src)

			if len(g.NodeOrder) != 1 {
				t.Fatalf("got %d nodes, want 1: %v", len(g.NodeOrder), g.NodeOrder)
			}
			id := g.NodeOrder[0]
			if id != "b" {
				t.Errorf("node ID = %q, want \"b\": the break tag corrupted it", id)
			}
			node := g.Nodes[id]
			if node.Label != tc.label {
				t.Errorf("label = %q, want %q", node.Label, tc.label)
			}
			if node.Shape != graph.ShapeRectangle {
				t.Errorf("shape = %v, want ShapeRectangle: the break tag changed it", node.Shape)
			}
		})
	}
}

// TestParseSubgraphBracketLabel pins the fix for subgraph titles rendering as
// raw source. Mermaid accepts both `subgraph SUB [Title]` and the far more
// common `subgraph SUB[Title]`; the pattern required the space, so the second
// form fell through and captioned the group `SUB["Title"]`.
func TestParseSubgraphBracketLabel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		id    string
		label string
	}{
		{"no space quoted", "graph TD\nsubgraph SUB[\"Group Title\"]\nA-->B\nend", "SUB", "Group Title"},
		{"no space bare", "graph TD\nsubgraph SUB[Group Title]\nA-->B\nend", "SUB", "Group Title"},
		{"with space", "graph TD\nsubgraph SUB [\"Group Title\"]\nA-->B\nend", "SUB", "Group Title"},
		{"id only", "graph TD\nsubgraph SUB\nA-->B\nend", "SUB", "SUB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := ParseFlowchart(tc.src)
			if len(g.Subgraphs) != 1 {
				t.Fatalf("got %d subgraphs, want 1", len(g.Subgraphs))
			}
			sg := g.Subgraphs[0]
			if sg.ID != tc.id {
				t.Errorf("ID = %q, want %q", sg.ID, tc.id)
			}
			if sg.Label != tc.label {
				t.Errorf("label = %q, want %q — raw source leaked into the caption", sg.Label, tc.label)
			}
		})
	}
}
