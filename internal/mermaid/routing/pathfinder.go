// Package routing provides A* pathfinding and edge routing for grid-based layouts.
package routing

import "container/heap"

// Point represents a grid coordinate (col, row).
type Point struct {
	Col, Row int
}

// dirs defines the 4-directional movement: up, down, left, right.
var dirs = [4]Point{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}

// heuristic computes Manhattan distance with +1 corner penalty when not
// axis-aligned.
func heuristic(c1, r1, c2, r2 int) float64 {
	dx := abs(c1 - c2)
	dy := abs(r1 - r2)
	if dx == 0 || dy == 0 {
		return float64(dx + dy)
	}
	return float64(dx + dy + 1) // corner penalty
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// aStarNode is a node in the A* open set.
type aStarNode struct {
	fCost  float64
	gCost  float64
	col    int
	row    int
	parent *aStarNode
	index  int // index in the heap
}

// adjacentToRoute reports whether (col,row) sits directly beside a cell an
// earlier edge already occupies.
func adjacentToRoute(soft map[Point]bool, col, row int) bool {
	return soft[Point{col - 1, row}] || soft[Point{col + 1, row}] ||
		soft[Point{col, row - 1}] || soft[Point{col, row + 1}]
}

// nodeHeap implements heap.Interface for A* nodes, ordered by fCost.
type nodeHeap []*aStarNode

func (h nodeHeap) Len() int           { return len(h) }
func (h nodeHeap) Less(i, j int) bool { return h[i].fCost < h[j].fCost }
func (h nodeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *nodeHeap) Push(x interface{}) {
	n, ok := x.(*aStarNode)
	if !ok {
		return
	}
	n.index = len(*h)
	*h = append(*h, n)
}
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = nil // avoid memory leak
	node.index = -1
	*h = old[:n-1]
	return node
}

const defaultMaxIterations = 5000

// FindPath finds a path from (startCol, startRow) to (endCol, endRow) using A*.
//
// isFree reports whether a grid cell is unoccupied by any node.
// softObstacles marks cells occupied by previously-routed edges (cost +2).
// Returns the path as a slice of Points, or nil if no path is found.
func FindPath(startCol, startRow, endCol, endRow int, isFree func(col, row int) bool, softObstacles map[Point]bool) []Point {
	if startCol == endCol && startRow == endRow {
		return []Point{{startCol, startRow}}
	}

	soft := softObstacles // may be nil

	startNode := &aStarNode{
		fCost: heuristic(startCol, startRow, endCol, endRow),
		gCost: 0,
		col:   startCol,
		row:   startRow,
	}

	openSet := &nodeHeap{startNode}
	heap.Init(openSet)

	closed := make(map[Point]bool)
	bestG := make(map[Point]float64)
	bestG[Point{startCol, startRow}] = 0

	iterations := 0
	for openSet.Len() > 0 && iterations < defaultMaxIterations {
		iterations++
		current, ok := heap.Pop(openSet).(*aStarNode)
		if !ok {
			break
		}

		if current.col == endCol && current.row == endRow {
			return reconstruct(current)
		}

		key := Point{current.col, current.row}
		if closed[key] {
			continue
		}
		closed[key] = true

		for _, d := range dirs {
			nc, nr := current.col+d.Col, current.row+d.Row
			nkey := Point{nc, nr}

			if closed[nkey] {
				continue
			}

			// Allow start and end even if "occupied"
			isEndpoint := nc == endCol && nr == endRow
			if !isEndpoint && !isFree(nc, nr) {
				continue
			}

			// Base cost + soft obstacle penalty
			stepCost := 1.0
			if soft != nil && soft[nkey] {
				stepCost += 2.0
			} else if soft != nil && adjacentToRoute(soft, nc, nr) {
				// Running immediately alongside another edge was free, so
				// parallel routes packed into neighboring columns and drew as
				// a solid block of lines with nothing between them. A cell
				// beside an existing route now costs a little more than open
				// space, which spreads the lines apart wherever there is room
				// and still lets them crowd together where there is not.
				//
				// The penalty is deliberately below the cost of stepping onto
				// a route (2.0) and of turning a corner plus a detour, so it
				// biases the choice without ever making a longer path win.
				stepCost += 0.6
			}

			// Corner penalty: if direction changes from parent's direction
			if current.parent != nil {
				prevDC := current.col - current.parent.col
				prevDR := current.row - current.parent.row
				if d.Col != prevDC || d.Row != prevDR {
					stepCost += 0.5 // slight corner penalty in g-cost too
				}
			}

			newG := current.gCost + stepCost

			if prev, ok := bestG[nkey]; ok && prev <= newG {
				continue
			}
			bestG[nkey] = newG

			h := heuristic(nc, nr, endCol, endRow)
			neighbor := &aStarNode{
				fCost:  newG + h,
				gCost:  newG,
				col:    nc,
				row:    nr,
				parent: current,
			}
			heap.Push(openSet, neighbor)
		}
	}

	return nil // no path found
}

// reconstruct walks parent pointers to build the path from start to end.
func reconstruct(node *aStarNode) []Point {
	var path []Point
	for n := node; n != nil; n = n.parent {
		path = append(path, Point{n.col, n.row})
	}
	// Reverse
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// SimplifyPath removes collinear intermediate points, keeping only corners.
func SimplifyPath(path []Point) []Point {
	if len(path) <= 2 {
		return path
	}

	result := []Point{path[0]}
	for i := 1; i < len(path)-1; i++ {
		prev := path[i-1]
		curr := path[i]
		nxt := path[i+1]
		// Direction from prev to curr
		d1c := curr.Col - prev.Col
		d1r := curr.Row - prev.Row
		// Direction from curr to next
		d2c := nxt.Col - curr.Col
		d2r := nxt.Row - curr.Row
		if d1c != d2c || d1r != d2r {
			result = append(result, curr)
		}
	}
	result = append(result, path[len(path)-1])
	return result
}
