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
