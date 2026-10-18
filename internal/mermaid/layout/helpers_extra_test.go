package layout

import (
	"reflect"
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

func TestWordWrap(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxWidth int
		want     []string
	}{
		{"fits on one line", "hello world", 20, []string{"hello world"}},
		{"breaks at the limit", "hello world", 5, []string{"hello", "world"}},
		{"exactly fits", "abc def", 7, []string{"abc def"}},
		{"one over", "abc def", 6, []string{"abc", "def"}},
		{"three lines", "aa bb cc", 2, []string{"aa", "bb", "cc"}},
		// A single word longer than the limit cannot be broken by words
		// alone, so it stays on its own line rather than being sliced.
		{"word longer than the limit", "overlongword x", 4, []string{"overlongword", "x"}},
		{"extra whitespace is collapsed", "  a   b  ", 10, []string{"a b"}},
		{"empty text", "", 10, []string{""}},
		{"only whitespace", "   ", 10, []string{"   "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WordWrap(tt.text, tt.maxWidth); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("WordWrap(%q, %d) = %q, want %q", tt.text, tt.maxWidth, got, tt.want)
			}
		})
	}
}

func TestSortedKeys(t *testing.T) {
	if got := sortedKeys(map[int]int{3: 0, 1: 0, 2: 0}); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("expected sorted keys [1 2 3], got %v", got)
	}
	if got := sortedKeys(map[int]string{}); len(got) != 0 {
		t.Errorf("expected no keys for an empty map, got %v", got)
	}
}

func TestMapsEqual(t *testing.T) {
	a := map[string]bool{"x": true, "y": true}

	if !mapsEqual(a, map[string]bool{"x": true, "y": true}) {
		t.Error("expected equal sets to compare equal")
	}
	if mapsEqual(a, map[string]bool{"x": true}) {
		t.Error("expected different sizes to compare unequal")
	}
	if mapsEqual(a, map[string]bool{"x": true, "z": true}) {
		t.Error("expected different members to compare unequal")
	}
	if !mapsEqual(map[string]bool{}, map[string]bool{}) {
		t.Error("expected two empty sets to compare equal")
	}
}

func TestGatherAllNodes(t *testing.T) {
	inner := &graph.Subgraph{ID: "inner", NodeIDs: []string{"C", "D"}}
	outer := &graph.Subgraph{ID: "outer", NodeIDs: []string{"A", "B"}, Children: []*graph.Subgraph{inner}}

	got := map[string]bool{}
	gatherAllNodes(outer, got)

	want := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	if !mapsEqual(got, want) {
		t.Errorf("gatherAllNodes = %v, want %v", got, want)
	}
}

func TestGetOrthogonalSGNodes(t *testing.T) {
	t.Run("no subgraphs", func(t *testing.T) {
		g := graph.NewGraph()
		if got := getOrthogonalSGNodes(g); len(got) != 0 {
			t.Errorf("expected no sets, got %v", got)
		}
	})

	t.Run("subgraph inherits direction", func(t *testing.T) {
		g := graph.NewGraph()
		g.Direction = graph.DirTB
		g.Subgraphs = []*graph.Subgraph{{ID: "s", NodeIDs: []string{"A"}}} // Direction nil
		if got := getOrthogonalSGNodes(g); len(got) != 0 {
			t.Errorf("expected an inheriting subgraph to be skipped, got %v", got)
		}
	})

	t.Run("explicit same-axis direction is not orthogonal", func(t *testing.T) {
		g := graph.NewGraph()
		g.Direction = graph.DirTB
		same := graph.DirTD // still vertical once normalized
		g.Subgraphs = []*graph.Subgraph{{ID: "s", NodeIDs: []string{"A"}, Direction: &same}}
		if got := getOrthogonalSGNodes(g); len(got) != 0 {
			t.Errorf("expected a same-axis subgraph to be skipped, got %v", got)
		}
	})

	t.Run("orthogonal subgraph is collected with its nodes", func(t *testing.T) {
		g := graph.NewGraph()
		g.Direction = graph.DirTB
		lr := graph.DirLR
		g.Subgraphs = []*graph.Subgraph{{ID: "s", NodeIDs: []string{"A", "B"}, Direction: &lr}}

		got := getOrthogonalSGNodes(g)
		if len(got) != 1 {
			t.Fatalf("expected 1 orthogonal set, got %d", len(got))
		}
		if !mapsEqual(got[0], map[string]bool{"A": true, "B": true}) {
			t.Errorf("expected nodes A and B, got %v", got[0])
		}
	})

	t.Run("nested orthogonal subgraph is found", func(t *testing.T) {
		g := graph.NewGraph()
		g.Direction = graph.DirTB
		rl := graph.DirRL
		child := &graph.Subgraph{ID: "child", NodeIDs: []string{"C"}, Direction: &rl}
		g.Subgraphs = []*graph.Subgraph{{ID: "parent", NodeIDs: []string{"A"}, Children: []*graph.Subgraph{child}}}

		got := getOrthogonalSGNodes(g)
		if len(got) != 1 {
			t.Fatalf("expected the nested set to be found, got %d", len(got))
		}
		if !mapsEqual(got[0], map[string]bool{"C": true}) {
			t.Errorf("expected node C, got %v", got[0])
		}
	})
}

func TestAdjustForNegativeBounds(t *testing.T) {
	t.Run("no subgraph bounds is a no-op", func(t *testing.T) {
		l := NewGridLayout()
		l.Placements["A"] = &NodePlacement{NodeID: "A", DrawX: -5, DrawY: -5}
		adjustForNegativeBounds(l)
		if got := l.Placements["A"].DrawX; got != -5 {
			t.Errorf("expected no offset without subgraph bounds, got %d", got)
		}
	})

	t.Run("non-negative bounds are a no-op", func(t *testing.T) {
		l := NewGridLayout()
		l.Placements["A"] = &NodePlacement{NodeID: "A", DrawX: 1, DrawY: 1}
		l.SubgraphBounds = []SubgraphBounds{{X: 0, Y: 0, Width: 5, Height: 5}}
		adjustForNegativeBounds(l)
		if l.OffsetX != 0 || l.OffsetY != 0 {
			t.Errorf("expected no offset, got (%d,%d)", l.OffsetX, l.OffsetY)
		}
	})

	t.Run("negative bounds shift placements and bounds", func(t *testing.T) {
		l := NewGridLayout()
		l.Placements["A"] = &NodePlacement{NodeID: "A", DrawX: 0, DrawY: 0}
		l.SubgraphBounds = []SubgraphBounds{{X: -3, Y: -2, Width: 5, Height: 5}}

		adjustForNegativeBounds(l)

		// -minX + 1 keeps a one-cell margin from the edge.
		if l.OffsetX != 4 {
			t.Errorf("expected OffsetX 4, got %d", l.OffsetX)
		}
		if l.OffsetY != 3 {
			t.Errorf("expected OffsetY 3, got %d", l.OffsetY)
		}
		if got := l.Placements["A"].DrawX; got != 4 {
			t.Errorf("expected the placement shifted to 4, got %d", got)
		}
		if got := l.SubgraphBounds[0].X; got != 1 {
			t.Errorf("expected the bounds shifted to 1, got %d", got)
		}
		if got := l.SubgraphBounds[0].Y; got != 1 {
			t.Errorf("expected the bounds to sit on the margin, got %d", got)
		}
	})
}

func TestGridToDrawCenterUsesCellSize(t *testing.T) {
	l := NewGridLayout()
	// A cell wider/taller than one column/row centers the point in the cell.
	l.ColWidths[1] = 6
	l.RowHeights[1] = 4

	x, y := l.GridToDrawCenter(1, 1)
	gx, gy := newLayoutWithDrawOrigin(1, 1)
	if x <= gx || y <= gy {
		t.Errorf("expected the center past the cell origin, got (%d,%d) from (%d,%d)", x, y, gx, gy)
	}
}

// newLayoutWithDrawOrigin returns the top-left draw coordinate of a cell, so a
// center can be compared against it.
func newLayoutWithDrawOrigin(col, row int) (int, int) {
	l := NewGridLayout()
	l.ColWidths[col] = 6
	l.RowHeights[row] = 4
	return l.GridToDraw(col, row)
}

func TestScaleNodeColumnsDoesNotCompress(t *testing.T) {
	// The content-driven width is the minimum: a target narrower than the
	// current width must leave the layout untouched.
	l := NewGridLayout()
	l.ColWidths[0] = 10
	l.ColWidths[1] = 10
	l.Placements["A"] = &NodePlacement{NodeID: "A", Grid: GridCoord{Col: 0, Row: 0}}

	before := l.ColWidths[0]
	scaleNodeColumns(l, 5)
	if l.ColWidths[0] != before {
		t.Errorf("expected no compression, got width %d", l.ColWidths[0])
	}
}

func TestScaleNodeColumnsWithSlack(t *testing.T) {
	// With surplus width the node column absorbs it, so the canvas reaches the
	// requested width instead of leaving dead space on the right.
	l := NewGridLayout()
	l.ColWidths[0] = 10
	l.Placements["A"] = &NodePlacement{NodeID: "A", Grid: GridCoord{Col: 0, Row: 0}}

	scaleNodeColumns(l, 30)

	total := 0
	for _, w := range l.ColWidths {
		total += w
	}
	if total != 30 {
		t.Errorf("expected the columns to total 30, got %d (%v)", total, l.ColWidths)
	}
}
