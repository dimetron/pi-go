package layout

import (
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

func TestComputeLayoutSimpleChain(t *testing.T) {
	g := graph.NewGraph()
	g.Direction = graph.DirLR
	g.AddNode(&graph.Node{ID: "A", Label: "Start"})
	g.AddNode(&graph.Node{ID: "B", Label: "End"})
	g.AddEdge(graph.NewEdge("A", "B"))

	l := ComputeLayout(g, 4, 2, 0)
	if l == nil {
		t.Fatal("ComputeLayout returned nil")
	}
	if len(l.Placements) != 2 {
		t.Fatalf("expected 2 placements, got %d", len(l.Placements))
	}

	pa := l.Placements["A"]
	pb := l.Placements["B"]
	// In LR, A should be to the left of B
	if pa.DrawX >= pb.DrawX {
		t.Errorf("expected A left of B: A.x=%d, B.x=%d", pa.DrawX, pb.DrawX)
	}
}

func TestComputeLayoutTD(t *testing.T) {
	g := graph.NewGraph()
	g.Direction = graph.DirTD
	g.AddNode(&graph.Node{ID: "A", Label: "Top"})
	g.AddNode(&graph.Node{ID: "B", Label: "Bottom"})
	g.AddEdge(graph.NewEdge("A", "B"))

	l := ComputeLayout(g, 4, 2, 0)
	pa := l.Placements["A"]
	pb := l.Placements["B"]
	// In TD, A should be above B
	if pa.DrawY >= pb.DrawY {
		t.Errorf("expected A above B: A.y=%d, B.y=%d", pa.DrawY, pb.DrawY)
	}
}

func TestComputeLayoutCanvasSize(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: "A", Label: "Hello World"})

	l := ComputeLayout(g, 4, 2, 0)
	if l.CanvasWidth <= 0 || l.CanvasHeight <= 0 {
		t.Errorf("expected positive canvas dimensions, got %dx%d", l.CanvasWidth, l.CanvasHeight)
	}
}

func TestComputeLayoutBranching(t *testing.T) {
	g := graph.NewGraph()
	g.Direction = graph.DirTD
	g.AddNode(&graph.Node{ID: "A", Label: "A"})
	g.AddNode(&graph.Node{ID: "B", Label: "B"})
	g.AddNode(&graph.Node{ID: "C", Label: "C"})
	g.AddEdge(graph.NewEdge("A", "B"))
	g.AddEdge(graph.NewEdge("A", "C"))

	l := ComputeLayout(g, 4, 2, 0)
	if len(l.Placements) != 3 {
		t.Fatalf("expected 3 placements, got %d", len(l.Placements))
	}
	// B and C should be on the same layer (below A)
	pb := l.Placements["B"]
	pc := l.Placements["C"]
	if pb.DrawY != pc.DrawY {
		t.Errorf("expected B and C on same row: B.y=%d, C.y=%d", pb.DrawY, pc.DrawY)
	}
}
