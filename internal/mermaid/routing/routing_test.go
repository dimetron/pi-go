package routing

import (
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/layout"
)

func TestFindPathStraightLine(t *testing.T) {
	isFree := func(col, row int) bool { return true }
	path := FindPath(0, 5, 9, 5, isFree, nil)
	if len(path) == 0 {
		t.Fatal("expected a path, got empty")
	}
	if path[0] != (Point{0, 5}) {
		t.Errorf("expected start at (0,5), got %v", path[0])
	}
	if path[len(path)-1] != (Point{9, 5}) {
		t.Errorf("expected end at (9,5), got %v", path[len(path)-1])
	}
}

func TestFindPathAroundObstacle(t *testing.T) {
	blocked := map[Point]bool{}
	for col := 3; col <= 7; col++ {
		blocked[Point{col, 5}] = true
	}
	isFree := func(col, row int) bool {
		if col < 0 || row < 0 || col >= 10 || row >= 10 {
			return false
		}
		return !blocked[Point{col, row}]
	}

	path := FindPath(0, 5, 9, 5, isFree, nil)
	if len(path) == 0 {
		t.Fatal("expected a path around obstacle, got empty")
	}
	if path[len(path)-1] != (Point{9, 5}) {
		t.Errorf("expected end at (9,5), got %v", path[len(path)-1])
	}
}

func TestSimplifyPath(t *testing.T) {
	path := []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}}
	simplified := SimplifyPath(path)
	if len(simplified) != 2 {
		t.Errorf("expected 2 points after simplification, got %d: %v", len(simplified), simplified)
	}
}

func TestSimplifyPathWithCorner(t *testing.T) {
	path := []Point{{0, 0}, {1, 0}, {2, 0}, {2, 1}, {2, 2}}
	simplified := SimplifyPath(path)
	if len(simplified) != 3 {
		t.Errorf("expected 3 points (start, corner, end), got %d: %v", len(simplified), simplified)
	}
}

// placement builds a NodePlacement at a grid cell with a drawing rectangle.
func placement(id string, col, row, drawX, drawY, w, h int) *layout.NodePlacement {
	return &layout.NodePlacement{
		NodeID:     id,
		Grid:       layout.GridCoord{Col: col, Row: row},
		DrawX:      drawX,
		DrawY:      drawY,
		DrawWidth:  w,
		DrawHeight: h,
	}
}

func newTestLayout(placements ...*layout.NodePlacement) *layout.GridLayout {
	l := layout.NewGridLayout()
	for _, p := range placements {
		l.Placements[p.NodeID] = p
	}
	return l
}

func TestAdjacentToRoute(t *testing.T) {
	soft := map[Point]bool{{5, 5}: true}

	for _, p := range []Point{{4, 5}, {6, 5}, {5, 4}, {5, 6}} {
		if !adjacentToRoute(soft, p.Col, p.Row) {
			t.Errorf("expected (%d,%d) to be adjacent to an occupied cell", p.Col, p.Row)
		}
	}
	for _, p := range []Point{{5, 5}, {7, 5}, {5, 7}, {0, 0}} {
		if adjacentToRoute(soft, p.Col, p.Row) {
			t.Errorf("expected (%d,%d) not to be adjacent", p.Col, p.Row)
		}
	}
	if adjacentToRoute(nil, 0, 0) {
		t.Error("expected no adjacency for an empty obstacle set")
	}
}

func TestGetAttachPoint(t *testing.T) {
	p := placement("A", 4, 6, 0, 0, 1, 1)
	tests := []struct {
		dir  AttachDir
		want Point
	}{
		{AttachTop, Point{4, 5}},
		{AttachBottom, Point{4, 7}},
		{AttachLeft, Point{3, 6}},
		{AttachRight, Point{5, 6}},
		// An out-of-range AttachDir falls back to the node's own cell rather
		// than returning the zero Point, which would route from the origin.
		{AttachDir(99), Point{4, 6}},
	}
	for _, tt := range tests {
		if got := getAttachPoint(p, tt.dir); got != tt.want {
			t.Errorf("getAttachPoint(dir=%d) = %v, want %v", tt.dir, got, tt.want)
		}
	}
}

func TestDetermineDirections(t *testing.T) {
	tests := []struct {
		name              string
		src, tgt          [2]int // col, row
		dir               graph.Direction
		wantPref, wantAlt dirPair
	}{
		// Horizontal flow: target to the right, primary is right->left.
		// Rows are equal, so the alt branch falls back to preferred.
		{"LR forward", [2]int{0, 0}, [2]int{3, 0}, graph.DirLR,
			dirPair{AttachRight, AttachLeft}, dirPair{AttachRight, AttachLeft}},
		// Horizontal flow backwards: the back-edge shape, and no alt search.
		{"LR back-edge", [2]int{3, 0}, [2]int{0, 0}, graph.DirLR,
			dirPair{AttachBottom, AttachBottom}, dirPair{AttachBottom, AttachTop}},
		// Same column, target below: vertical pair even in horizontal flow.
		{"LR same column", [2]int{1, 0}, [2]int{1, 2}, graph.DirLR,
			dirPair{AttachBottom, AttachTop}, dirPair{AttachBottom, AttachTop}},
		// Same column, target above.
		{"LR same column upward", [2]int{1, 2}, [2]int{1, 0}, graph.DirLR,
			dirPair{AttachTop, AttachBottom}, dirPair{AttachTop, AttachBottom}},
		// Same cell: alt must fall back to preferred, not a zero pair.
		{"LR identical cells", [2]int{1, 1}, [2]int{1, 1}, graph.DirLR,
			dirPair{AttachTop, AttachBottom}, dirPair{AttachTop, AttachBottom}},

		// Vertical flow: target below.
		// Columns are equal, so the alt branch falls back to preferred.
		{"TB forward", [2]int{0, 0}, [2]int{0, 3}, graph.DirTB,
			dirPair{AttachBottom, AttachTop}, dirPair{AttachBottom, AttachTop}},
		// Vertical flow upwards: the back-edge shape.
		{"TB back-edge", [2]int{0, 3}, [2]int{0, 0}, graph.DirTB,
			dirPair{AttachRight, AttachRight}, dirPair{AttachRight, AttachLeft}},
		// Same row, target to the right.
		{"TB same row", [2]int{0, 1}, [2]int{2, 1}, graph.DirTB,
			dirPair{AttachRight, AttachLeft}, dirPair{AttachRight, AttachLeft}},
		// Same row, target to the left.
		{"TB same row leftward", [2]int{2, 1}, [2]int{0, 1}, graph.DirTB,
			dirPair{AttachLeft, AttachRight}, dirPair{AttachLeft, AttachRight}},
		{"TB identical cells", [2]int{1, 1}, [2]int{1, 1}, graph.DirTB,
			dirPair{AttachLeft, AttachRight}, dirPair{AttachLeft, AttachRight}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := placement("S", tt.src[0], tt.src[1], 0, 0, 1, 1)
			tgt := placement("T", tt.tgt[0], tt.tgt[1], 0, 0, 1, 1)
			pref, alt := determineDirections(src, tgt, tt.dir)
			if pref != tt.wantPref {
				t.Errorf("preferred = %+v, want %+v", pref, tt.wantPref)
			}
			if alt != tt.wantAlt {
				t.Errorf("alt = %+v, want %+v", alt, tt.wantAlt)
			}
		})
	}
}

func TestResolvePlacement(t *testing.T) {
	l := newTestLayout(placement("A", 0, 0, 0, 0, 5, 3))

	// A node resolves to its own placement.
	if got := resolvePlacement("A", false, l, nil); got == nil || got.NodeID != "A" {
		t.Fatalf("expected placement A, got %+v", got)
	}
	// An unknown node is nil, which makes RouteEdges skip the edge.
	if got := resolvePlacement("missing", false, l, nil); got != nil {
		t.Errorf("expected nil for an unknown node, got %+v", got)
	}

	// Subgraph lookup misses are nil too.
	if got := resolvePlacement("sg", true, l, nil); got != nil {
		t.Errorf("expected nil for a missing subgraph, got %+v", got)
	}
}

// TestResolvePlacementSubgraphUsesCenter pins the two properties that make a
// subgraph anchor stable: the anchor is the member nearest the subgraph center,
// and ties resolve the same way on every run despite Placements being a map.
func TestResolvePlacementSubgraphUsesCenter(t *testing.T) {
	sg := &graph.Subgraph{ID: "S", Label: "S", NodeIDs: []string{"A", "B"}}

	// A subgraph spanning (0,0)-(10,10). Two members sit equidistant from its
	// center, which is exactly the tie that used to depend on map order.
	bounds := &layout.SubgraphBounds{Subgraph: sg, X: 0, Y: 0, Width: 10, Height: 10}
	sgBounds := map[string]*layout.SubgraphBounds{"S": bounds}

	for _, ids := range [][2]string{{"B", "A"}, {"A", "B"}} {
		l := layout.NewGridLayout()
		for _, id := range ids {
			// Both centerd on the subgraph center, so both are distance 0.
			l.Placements[id] = placement(id, 2, 2, 0, 0, 10, 10)
		}
		got := resolvePlacement("S", true, l, sgBounds)
		if got == nil {
			t.Fatal("expected a synthesized placement")
		}
		// sorted ids means A is scanned first and keeps the tie.
		if got.Grid.Col != 2 || got.Grid.Row != 2 {
			t.Errorf("expected tie to resolve to A's cell (2,2), got %+v", got.Grid)
		}
		if got.DrawX != 0 || got.DrawY != 0 || got.DrawWidth != 10 || got.DrawHeight != 10 {
			t.Errorf("expected the subgraph rectangle to be used, got %+v", got)
		}
	}
}

func TestResolvePlacementSubgraphPicksNearestMember(t *testing.T) {
	sg := &graph.Subgraph{ID: "S", Label: "S", NodeIDs: []string{"near", "far"}}
	l := layout.NewGridLayout()
	// Subgraph center is (5,5); "near" is drawn on it, "far" well away.
	l.Placements["near"] = placement("near", 1, 1, 4, 4, 2, 2)
	l.Placements["far"] = placement("far", 9, 9, 40, 40, 2, 2)
	sgBounds := map[string]*layout.SubgraphBounds{
		"S": {Subgraph: sg, X: 0, Y: 0, Width: 10, Height: 10},
	}

	got := resolvePlacement("S", true, l, sgBounds)
	if got == nil {
		t.Fatal("expected a synthesized placement")
	}
	if got.Grid.Col != 1 || got.Grid.Row != 1 {
		t.Errorf("expected the nearest member's cell (1,1), got %+v", got.Grid)
	}
}

// routedTestGraph is a two-node graph laid out side by side, enough to exercise
// the ordinary route path.
func routedTestGraph(dir graph.Direction) (*graph.Graph, *layout.GridLayout) {
	g := graph.NewGraph()
	g.Direction = dir
	g.AddNode(&graph.Node{ID: "A", Label: "A"})
	g.AddNode(&graph.Node{ID: "B", Label: "B"})
	g.AddEdge(graph.NewEdge("A", "B"))

	l := newTestLayout(
		placement("A", 0, 0, 0, 0, 1, 1),
		placement("B", 0, 2, 0, 4, 1, 1),
	)
	return g, l
}

func TestRouteEdgesForwardEdge(t *testing.T) {
	g, l := routedTestGraph(graph.DirTB)

	routed := RouteEdges(g, l)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed edge, got %d", len(routed))
	}
	re := routed[0]
	if re.Index != 0 {
		t.Errorf("expected Index 0, got %d", re.Index)
	}
	if len(re.GridPath) == 0 {
		t.Fatal("expected a grid path")
	}
	// Grid and draw paths are parallel: every grid point has a draw point.
	if len(re.DrawPath) != len(re.GridPath) {
		t.Errorf("expected %d draw points, got %d", len(re.GridPath), len(re.DrawPath))
	}
	if len(re.OccupiedCells) == 0 {
		t.Error("expected occupied cells to be recorded")
	}
	// Flowing downwards from A to B must leave the bottom and arrive at the top.
	if re.StartDir != AttachBottom || re.EndDir != AttachTop {
		t.Errorf("expected bottom->top attach, got %d->%d", re.StartDir, re.EndDir)
	}
}

func TestRouteEdgesSelfReference(t *testing.T) {
	g := graph.NewGraph()
	g.Direction = graph.DirTB
	g.AddNode(&graph.Node{ID: "A", Label: "A"})
	g.AddEdge(graph.NewEdge("A", "A"))
	l := newTestLayout(placement("A", 3, 3, 6, 6, 1, 1))

	routed := RouteEdges(g, l)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed edge, got %d", len(routed))
	}
	re := routed[0]
	if re.StartDir != AttachTop || re.EndDir != AttachRight {
		t.Errorf("expected the self-loop to run top->right, got %d->%d", re.StartDir, re.EndDir)
	}
	// The loop leaves the node, goes above it, and comes back in from the right.
	if len(re.GridPath) != 5 {
		t.Errorf("expected a 5-point loop, got %d: %v", len(re.GridPath), re.GridPath)
	}
}

func TestRouteEdgesSkipsUnplaceableEdges(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: "A", Label: "A"})
	g.AddEdge(graph.NewEdge("A", "ghost")) // target never placed
	l := newTestLayout(placement("A", 0, 0, 0, 0, 1, 1))

	if routed := RouteEdges(g, l); len(routed) != 0 {
		t.Errorf("expected unplaceable edges to be skipped, got %d", len(routed))
	}
}

func TestRouteEdgesSubgraphTarget(t *testing.T) {
	sg := &graph.Subgraph{ID: "S", Label: "S", NodeIDs: []string{"B"}}
	g := graph.NewGraph()
	g.Direction = graph.DirTB
	g.AddNode(&graph.Node{ID: "A", Label: "A"})
	g.AddNode(&graph.Node{ID: "B", Label: "B"})
	edge := graph.NewEdge("A", "S")
	edge.TargetIsSubgraph = true
	g.AddEdge(edge)

	l := newTestLayout(placement("A", 0, 0, 0, 0, 1, 1), placement("B", 0, 4, 0, 8, 1, 1))
	l.SubgraphBounds = []layout.SubgraphBounds{
		{Subgraph: sg, X: 0, Y: 8, Width: 1, Height: 1},
	}

	routed := RouteEdges(g, l)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed edge, got %d", len(routed))
	}
	if len(routed[0].GridPath) == 0 {
		t.Error("expected a path to the subgraph anchor")
	}
}
