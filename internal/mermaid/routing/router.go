package routing

import (
	"math"
	"sort"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/layout"
)

// AttachDir represents which side of a node an edge attaches to.
type AttachDir int

const (
	AttachTop AttachDir = iota
	AttachBottom
	AttachLeft
	AttachRight
)

// RoutedEdge is an edge with its computed path in grid and drawing coordinates.
type RoutedEdge struct {
	Edge          graph.Edge
	GridPath      []Point
	DrawPath      []Point
	StartDir      AttachDir
	EndDir        AttachDir
	Label         string
	Index         int
	OccupiedCells map[Point]bool
}

// RouteEdges routes all edges in the graph using the computed layout.
func RouteEdges(g *graph.Graph, l *layout.GridLayout) []RoutedEdge {
	direction := g.Direction.Normalized()
	var routed []RoutedEdge
	softObstacles := make(map[Point]bool)

	// Build subgraph bounds lookup
	sgBounds := make(map[string]*layout.SubgraphBounds, len(l.SubgraphBounds))
	for i := range l.SubgraphBounds {
		sb := &l.SubgraphBounds[i]
		sgBounds[sb.Subgraph.ID] = sb
	}

	for i, edge := range g.Edges {
		src := resolvePlacement(edge.Source, edge.SourceIsSubgraph, l, sgBounds)
		tgt := resolvePlacement(edge.Target, edge.TargetIsSubgraph, l, sgBounds)

		if src == nil || tgt == nil {
			continue
		}

		if edge.IsSelfReference() && !edge.SourceIsSubgraph {
			re := routeSelfEdge(edge, src, l, direction)
			re.Index = i
			routed = append(routed, re)
			continue
		}

		re := routeEdge(edge, src, tgt, l, direction, softObstacles)
		re.Index = i
		for p := range re.OccupiedCells {
			softObstacles[p] = true
		}
		routed = append(routed, re)
	}

	return routed
}

// resolvePlacement resolves a node or subgraph ID to a NodePlacement.
// For subgraphs, it synthesizes a virtual placement at the subgraph center.
func resolvePlacement(
	nodeID string,
	isSubgraph bool,
	l *layout.GridLayout,
	sgBounds map[string]*layout.SubgraphBounds,
) *layout.NodePlacement {
	if !isSubgraph {
		p, ok := l.Placements[nodeID]
		if !ok {
			return nil
		}
		return p
	}

	sb, ok := sgBounds[nodeID]
	if !ok {
		return nil
	}

	// Synthesize a virtual placement at the subgraph center
	cx := sb.X + sb.Width/2
	cy := sb.Y + sb.Height/2

	// Find the closest grid cell to the subgraph center
	bestCol := 0
	bestRow := 0
	bestDist := math.MaxFloat64

	// Placements is a map, so this scan must run in a fixed order. Two members
	// of a subgraph are routinely equidistant from its center — `subgraph A;
	// A1; A2; end` is the common case — and whichever the scan reaches first
	// wins the tie. Iterating the map directly hands that decision to Go's
	// randomized map order, so the arrow leaving the subgraph anchors to A1 on
	// one render and A2 on the next.
	ids := make([]string, 0, len(l.Placements))
	for id := range l.Placements {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		p := l.Placements[id]
		dx := p.DrawX + p.DrawWidth/2 - cx
		dy := p.DrawY + p.DrawHeight/2 - cy
		dist := float64(abs(dx) + abs(dy))
		if dist < bestDist {
			bestDist = dist
			bestCol = p.Grid.Col
			bestRow = p.Grid.Row
		}
	}

	return &layout.NodePlacement{
		NodeID:     nodeID,
		Grid:       layout.GridCoord{Col: bestCol, Row: bestRow},
		DrawX:      sb.X,
		DrawY:      sb.Y,
		DrawWidth:  sb.Width,
		DrawHeight: sb.Height,
	}
}

// getAttachPoint returns the grid coordinate of an attachment point on a node.
func getAttachPoint(placement *layout.NodePlacement, dir AttachDir) Point {
	gc := placement.Grid
	switch dir {
	case AttachTop:
		return Point{gc.Col, gc.Row - 1}
	case AttachBottom:
		return Point{gc.Col, gc.Row + 1}
	case AttachLeft:
		return Point{gc.Col - 1, gc.Row}
	case AttachRight:
		return Point{gc.Col + 1, gc.Row}
	}
	return Point{gc.Col, gc.Row}
}

// dirPair is a pair of attachment directions (start, end).
type dirPair struct {
	start AttachDir
	end   AttachDir
}

// determineDirections returns preferred and alternative start/end attachment
// directions based on relative position and flow direction.
func determineDirections(
	src, tgt *layout.NodePlacement,
	direction graph.Direction,
) (preferred, alt dirPair) {
	sc, sr := src.Grid.Col, src.Grid.Row
	tc, tr := tgt.Grid.Col, tgt.Grid.Row

	if direction.IsHorizontal() {
		// Primary flow is left-to-right
		if tc > sc {
			preferred = dirPair{AttachRight, AttachLeft}
		} else if tc < sc {
			// Back-edge: exit BOTTOM to separate from other back-edges entering TOP
			preferred = dirPair{AttachBottom, AttachBottom}
			return preferred, dirPair{AttachBottom, AttachTop}
		} else {
			if tr > sr {
				preferred = dirPair{AttachBottom, AttachTop}
			} else {
				preferred = dirPair{AttachTop, AttachBottom}
			}
		}

		// Alternative uses vertical
		if tr > sr {
			alt = dirPair{AttachBottom, AttachTop}
		} else if tr < sr {
			alt = dirPair{AttachTop, AttachBottom}
		} else {
			alt = preferred
		}
	} else {
		// Primary flow is top-to-bottom
		if tr > sr {
			preferred = dirPair{AttachBottom, AttachTop}
		} else if tr < sr {
			// Back-edge: exit RIGHT to separate from other back-edges entering LEFT
			preferred = dirPair{AttachRight, AttachRight}
			return preferred, dirPair{AttachRight, AttachLeft}
		} else {
			if tc > sc {
				preferred = dirPair{AttachRight, AttachLeft}
			} else {
				preferred = dirPair{AttachLeft, AttachRight}
			}
		}

		// Alternative uses horizontal
		if tc > sc {
			alt = dirPair{AttachRight, AttachLeft}
		} else if tc < sc {
			alt = dirPair{AttachLeft, AttachRight}
		} else {
			alt = preferred
		}
	}

	return preferred, alt
}

// routeEdge routes a single edge between two nodes.
func routeEdge(
	edge graph.Edge,
	src, tgt *layout.NodePlacement,
	l *layout.GridLayout,
	direction graph.Direction,
	softObstacles map[Point]bool,
) RoutedEdge {
	preferred, alt := determineDirections(src, tgt, direction)

	// Try preferred path
	startPref := getAttachPoint(src, preferred.start)
	endPref := getAttachPoint(tgt, preferred.end)

	isFree := func(c, r int) bool {
		return l.IsFree(c, r, nil)
	}

	pathPref := FindPath(startPref.Col, startPref.Row, endPref.Col, endPref.Row, isFree, softObstacles)

	// Try alternative path
	startAlt := getAttachPoint(src, alt.start)
	endAlt := getAttachPoint(tgt, alt.end)

	pathAlt := FindPath(startAlt.Col, startAlt.Row, endAlt.Col, endAlt.Row, isFree, softObstacles)

	// Pick shorter path
	var path []Point
	var startDir, endDir AttachDir

	if pathPref != nil && pathAlt != nil {
		if len(pathPref) <= len(pathAlt) {
			path, startDir, endDir = pathPref, preferred.start, preferred.end
		} else {
			path, startDir, endDir = pathAlt, alt.start, alt.end
		}
	} else if pathPref != nil {
		path, startDir, endDir = pathPref, preferred.start, preferred.end
	} else if pathAlt != nil {
		path, startDir, endDir = pathAlt, alt.start, alt.end
	} else {
		// Fallback: direct line
		path = []Point{startPref, endPref}
		startDir, endDir = preferred.start, preferred.end
	}

	simplified := SimplifyPath(path)

	// Convert to drawing coordinates (center of each cell)
	drawPath := make([]Point, len(simplified))
	for i, p := range simplified {
		dx, dy := l.GridToDrawCenter(p.Col, p.Row)
		drawPath[i] = Point{dx, dy}
	}

	// Track occupied cells
	occupied := make(map[Point]bool, len(path))
	for _, p := range path {
		occupied[p] = true
	}

	return RoutedEdge{
		Edge:          edge,
		GridPath:      simplified,
		DrawPath:      drawPath,
		StartDir:      startDir,
		EndDir:        endDir,
		Label:         edge.Label,
		OccupiedCells: occupied,
	}
}

// routeSelfEdge routes a self-referencing edge (A --> A).
// The loop goes out from the top, right, and back.
func routeSelfEdge(
	edge graph.Edge,
	src *layout.NodePlacement,
	l *layout.GridLayout,
	direction graph.Direction,
) RoutedEdge {
	gc := src.Grid

	// Loop: top -> above-right -> right -> back to right border
	path := []Point{
		{gc.Col, gc.Row - 1},     // top border of node
		{gc.Col, gc.Row - 2},     // one cell above
		{gc.Col + 2, gc.Row - 2}, // above and to the right
		{gc.Col + 2, gc.Row},     // right and level with center
		{gc.Col + 1, gc.Row},     // right border of node
	}

	drawPath := make([]Point, len(path))
	for i, p := range path {
		dx, dy := l.GridToDrawCenter(p.Col, p.Row)
		drawPath[i] = Point{dx, dy}
	}

	occupied := make(map[Point]bool, len(path))
	for _, p := range path {
		occupied[p] = true
	}

	return RoutedEdge{
		Edge:          edge,
		GridPath:      path,
		DrawPath:      drawPath,
		StartDir:      AttachTop,
		EndDir:        AttachRight,
		Label:         edge.Label,
		OccupiedCells: occupied,
	}
}
