package graph

import "testing"

func TestNewGraph(t *testing.T) {
	g := NewGraph()
	if g == nil {
		t.Fatal("NewGraph returned nil")
	}
	if g.Direction != DirTB {
		t.Errorf("expected default direction TB, got %s", g.Direction)
	}
	if len(g.Nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(g.Nodes))
	}
}

func TestAddNode(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: "A", Label: "Hello", Shape: ShapeRectangle})
	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(g.Nodes))
	}
	n := g.Nodes["A"]
	if n.Label != "Hello" {
		t.Errorf("expected label Hello, got %s", n.Label)
	}
}

func TestAddNodePreservesFirst(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: "A", Label: "First", Shape: ShapeRectangle})
	g.AddNode(&Node{ID: "A", Label: "Second", Shape: ShapeDiamond})
	if g.Nodes["A"].Label != "First" {
		t.Errorf("expected first label preserved, got %s", g.Nodes["A"].Label)
	}
}

func TestAddEdge(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: "A", Label: "A"})
	g.AddNode(&Node{ID: "B", Label: "B"})
	g.AddEdge(NewEdge("A", "B"))
	if len(g.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(g.Edges))
	}
	if g.Edges[0].Source != "A" || g.Edges[0].Target != "B" {
		t.Errorf("expected A->B, got %s->%s", g.Edges[0].Source, g.Edges[0].Target)
	}
}

func TestGetRoots(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: "A", Label: "A"})
	g.AddNode(&Node{ID: "B", Label: "B"})
	g.AddNode(&Node{ID: "C", Label: "C"})
	g.AddEdge(NewEdge("A", "B"))
	g.AddEdge(NewEdge("A", "C"))

	roots := g.GetRoots()
	if len(roots) != 1 || roots[0] != "A" {
		t.Errorf("expected root [A], got %v", roots)
	}
}

func TestGetChildren(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: "A", Label: "A"})
	g.AddNode(&Node{ID: "B", Label: "B"})
	g.AddNode(&Node{ID: "C", Label: "C"})
	g.AddEdge(NewEdge("A", "B"))
	g.AddEdge(NewEdge("A", "C"))

	children := g.GetChildren("A")
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

func TestNewEdgeDefaults(t *testing.T) {
	e := NewEdge("X", "Y")
	if !e.HasArrowEnd {
		t.Error("expected HasArrowEnd true by default")
	}
	if e.MinLength != 1 {
		t.Errorf("expected MinLength 1, got %d", e.MinLength)
	}
}

func TestDirectionClassifiers(t *testing.T) {
	tests := []struct {
		dir        Direction
		vertical   bool
		horizontal bool
		reversed   bool
		normalized Direction
	}{
		{DirTB, true, false, false, DirTB},
		{DirTD, true, false, false, DirTB},
		{DirBT, true, false, true, DirTB},
		{DirLR, false, true, false, DirLR},
		{DirRL, false, true, true, DirLR},
		// An empty/unknown direction must not claim any axis, and Normalized
		// returns it unchanged rather than silently picking TB.
		{"", false, false, false, ""},
		{Direction("XX"), false, false, false, Direction("XX")},
	}

	for _, tt := range tests {
		t.Run(string(tt.dir), func(t *testing.T) {
			if got := tt.dir.IsVertical(); got != tt.vertical {
				t.Errorf("IsVertical() = %v, want %v", got, tt.vertical)
			}
			if got := tt.dir.IsHorizontal(); got != tt.horizontal {
				t.Errorf("IsHorizontal() = %v, want %v", got, tt.horizontal)
			}
			if got := tt.dir.IsReversed(); got != tt.reversed {
				t.Errorf("IsReversed() = %v, want %v", got, tt.reversed)
			}
			if got := tt.dir.Normalized(); got != tt.normalized {
				t.Errorf("Normalized() = %q, want %q", got, tt.normalized)
			}
		})
	}
}

func TestEdgeIsBidirectional(t *testing.T) {
	e := NewEdge("A", "B")
	if e.IsBidirectional() {
		t.Error("expected a default edge not to be bidirectional")
	}
	e.HasArrowStart = true
	if !e.IsBidirectional() {
		t.Error("expected arrows on both ends to be bidirectional")
	}
}

func TestEdgeIsSelfReference(t *testing.T) {
	self := NewEdge("A", "A")
	if !self.IsSelfReference() {
		t.Error("expected A->A to be a self reference")
	}
	other := NewEdge("A", "B")
	if other.IsSelfReference() {
		t.Error("expected A->B not to be a self reference")
	}
}

// testSubgraphTree builds a two-level tree:
//
//	outer          contains: A, B
//	  inner        contains: C
func testSubgraphTree() *Graph {
	g := NewGraph()
	inner := &Subgraph{ID: "inner", Label: "Inner", NodeIDs: []string{"C"}}
	outer := &Subgraph{ID: "outer", Label: "Outer", NodeIDs: []string{"A", "B"}, Children: []*Subgraph{inner}}
	inner.Parent = outer
	g.Subgraphs = []*Subgraph{outer}
	return g
}

func TestFindSubgraphByID(t *testing.T) {
	g := testSubgraphTree()

	if sg := g.FindSubgraphByID("outer"); sg == nil || sg.Label != "Outer" {
		t.Errorf("expected top-level subgraph outer, got %+v", sg)
	}
	// Nested lookup is the point of the recursive search.
	if sg := g.FindSubgraphByID("inner"); sg == nil || sg.Label != "Inner" {
		t.Errorf("expected nested subgraph inner, got %+v", sg)
	}
	if sg := g.FindSubgraphByID("missing"); sg != nil {
		t.Errorf("expected nil for a missing subgraph, got %+v", sg)
	}
	if sg := NewGraph().FindSubgraphByID("outer"); sg != nil {
		t.Errorf("expected nil when there are no subgraphs, got %+v", sg)
	}
}

func TestFindSubgraphForNode(t *testing.T) {
	g := testSubgraphTree()

	// A and B only exist in the outer subgraph.
	for _, id := range []string{"A", "B"} {
		if sg := g.FindSubgraphForNode(id); sg == nil || sg.ID != "outer" {
			t.Errorf("FindSubgraphForNode(%q) = %+v, want outer", id, sg)
		}
	}
	// C is in both the outer tree and the inner subgraph; the innermost wins.
	if sg := g.FindSubgraphForNode("C"); sg == nil || sg.ID != "inner" {
		t.Errorf("expected innermost subgraph inner for C, got %+v", sg)
	}
	if sg := g.FindSubgraphForNode("nowhere"); sg != nil {
		t.Errorf("expected nil for an unknown node, got %+v", sg)
	}
}

func TestGetRootsFallbackToFirstNode(t *testing.T) {
	// A cycle leaves every node with an incoming edge, so there is no root and
	// GetRoots falls back to the first node in definition order.
	g := NewGraph()
	for _, id := range []string{"A", "B"} {
		g.AddNode(&Node{ID: id, Label: id})
	}
	g.AddEdge(NewEdge("A", "B"))
	g.AddEdge(NewEdge("B", "A"))

	roots := g.GetRoots()
	if len(roots) != 1 || roots[0] != "A" {
		t.Errorf("expected fallback root [A], got %v", roots)
	}

	// An empty graph has nothing to fall back to.
	if roots := NewGraph().GetRoots(); roots != nil {
		t.Errorf("expected no roots for an empty graph, got %v", roots)
	}
}

func TestGetChildrenDeduplicatesAndSkipsSelfEdges(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"A", "B"} {
		g.AddNode(&Node{ID: id, Label: id})
	}
	g.AddEdge(NewEdge("A", "B"))
	g.AddEdge(NewEdge("A", "B")) // duplicate edge
	g.AddEdge(NewEdge("A", "A")) // self reference is not a child

	children := g.GetChildren("A")
	if len(children) != 1 || children[0] != "B" {
		t.Errorf("expected children [B], got %v", children)
	}
	if children := g.GetChildren("B"); children != nil {
		t.Errorf("expected no children for B, got %v", children)
	}
}

func TestAddNodeAdoptsDetailsOntoDefault(t *testing.T) {
	// The first declaration is a bare reference; the second carries a label,
	// shape and class. Those fill in the defaults rather than being dropped.
	g := NewGraph()
	g.AddNode(&Node{ID: "A", Label: "A", Shape: ShapeRectangle})
	g.AddNode(&Node{ID: "A", Label: "Alpha", Shape: ShapeDiamond, StyleClass: "hot"})

	n := g.Nodes["A"]
	if n.Label != "Alpha" {
		t.Errorf("expected label Alpha, got %s", n.Label)
	}
	if n.Shape != ShapeDiamond {
		t.Errorf("expected shape Diamond, got %v", n.Shape)
	}
	if n.StyleClass != "hot" {
		t.Errorf("expected class hot, got %q", n.StyleClass)
	}
	// Merging must not duplicate the node in insertion order.
	if len(g.NodeOrder) != 1 {
		t.Errorf("expected NodeOrder to hold one entry, got %v", g.NodeOrder)
	}
}
