// grid.go implements the grid-based layout algorithm for flowchart diagrams.
//
// Layout algorithm:
//  1. assignLayers - BFS from roots, assign layers based on longest path using tree edges only
//  2. orderLayers - Barycenter heuristic with crossing minimization (8 passes max)
//  3. placeNodes - Place on grid using Stride spacing, collision check
//  4. computeSizes - Column widths and row heights from node content, word wrapping
//  5. normalizeSizes - Per-layer normalization, capped at MaxNormalized*
//  6. expandGapsForSubgraphs - Extra space for subgraph borders/labels/nesting
//  7. computeDrawCoords - Convert grid to draw coordinates
//  8. computeSubgraphBounds - Recursive bounding boxes
//  9. adjustForNegativeBounds - Shift everything if subgraph bounds go negative
//  10. Compute canvas size from max extents
package layout

import (
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/textwidth"
)

const (
	// Stride is the grid distance between node centers.
	Stride = 4

	// MaxLabelWidth is the character count before word wrapping.
	MaxLabelWidth = 20
	// MaxNormalizedWidth caps per-layer column normalization.
	MaxNormalizedWidth = 25
	// MaxNormalizedHeight caps per-layer row normalization.
	MaxNormalizedHeight = 7

	// SGBorderPad is padding between content and subgraph border.
	SGBorderPad = 2
	// SGLabelHeight is space for subgraph label + border line.
	SGLabelHeight = 2
	// SGGapPerLevel is the gap per nesting level.
	SGGapPerLevel = SGBorderPad + SGLabelHeight + 1
)

// GridCoord represents a position on the logical grid.
type GridCoord struct {
	Col int
	Row int
}

// NodePlacement stores the grid and drawing coordinates of a placed node.
type NodePlacement struct {
	NodeID     string
	Grid       GridCoord
	DrawX      int
	DrawY      int
	DrawWidth  int
	DrawHeight int

	// ContentWidth and ContentHeight are what this node's own label needs,
	// before the grid rounds it up. A grid cell is sized to the largest node
	// sharing its row and column, so drawing every box at cell size makes a
	// one-word node as large as the three-line node beside it — the box reads
	// as mostly empty. Keeping the node's own measurements lets the box be
	// drawn at its true size and centered in the cell it was allotted, while
	// the cell keeps its size so edge routing is unaffected.
	ContentWidth  int
	ContentHeight int
}

// SubgraphBounds stores the drawing bounds of a subgraph.
type SubgraphBounds struct {
	Subgraph *graph.Subgraph
	X        int
	Y        int
	Width    int
	Height   int
}

// GridLayout is the result of the layout process.
type GridLayout struct {
	Placements     map[string]*NodePlacement
	ColWidths      map[int]int
	RowHeights     map[int]int
	GridOccupied   map[GridCoord]string
	CanvasWidth    int
	CanvasHeight   int
	SubgraphBounds []SubgraphBounds
	OffsetX        int
	OffsetY        int
}

// NewGridLayout returns a GridLayout initialized with empty maps.
func NewGridLayout() *GridLayout {
	return &GridLayout{
		Placements:   make(map[string]*NodePlacement),
		ColWidths:    make(map[int]int),
		RowHeights:   make(map[int]int),
		GridOccupied: make(map[GridCoord]string),
	}
}

// IsFree reports whether a grid cell is not occupied by any node's 3x3 block.
func (l *GridLayout) IsFree(col, row int, exclude map[string]bool) bool {
	if col < 0 || row < 0 {
		return false
	}
	key := GridCoord{col, row}
	occupant, ok := l.GridOccupied[key]
	if !ok {
		return true
	}
	if exclude != nil && exclude[occupant] {
		return true
	}
	return false
}

// GridToDraw converts grid coordinates to drawing (character) coordinates.
func (l *GridLayout) GridToDraw(col, row int) (int, int) {
	x := l.OffsetX
	for c := range col {
		if w, ok := l.ColWidths[c]; ok {
			x += w
		} else {
			x++
		}
	}
	y := l.OffsetY
	for r := range row {
		if h, ok := l.RowHeights[r]; ok {
			y += h
		} else {
			y++
		}
	}
	return x, y
}

// GridToDrawCenter converts grid coordinates to the center of the cell.
func (l *GridLayout) GridToDrawCenter(col, row int) (int, int) {
	x, y := l.GridToDraw(col, row)
	w := 1
	if cw, ok := l.ColWidths[col]; ok {
		w = cw
	}
	h := 1
	if rh, ok := l.RowHeights[row]; ok {
		h = rh
	}
	return x + w/2, y + h/2
}

// ComputeLayout computes the grid layout for a graph.
// maxWidth, if > 0, hints the target canvas width — gap columns will be
// scaled proportionally to fill or compress to this width.
func ComputeLayout(g *graph.Graph, paddingX, paddingY, maxWidth int) *GridLayout {
	layout := NewGridLayout()
	direction := g.Direction.Normalized()

	if len(g.NodeOrder) == 0 {
		return layout
	}

	// Step 1: Assign layers via BFS from roots
	layers := assignLayers(g)

	// Step 2: Order nodes within layers (barycenter heuristic)
	layerOrder := orderLayers(g, layers)

	// Step 3: Place nodes on the grid
	placeNodes(g, layout, layerOrder, direction)

	// Step 4: Compute column widths and row heights (with word wrapping)
	computeSizes(g, layout, paddingX, paddingY)

	// Step 4b: Normalize sizes (per-layer, capped)
	// Step 4b: sizes are deliberately NOT normalized per layer — see
	// normalizeSizes' doc comment for why that was dropped.

	// Step 5: Expand gaps for subgraph borders and labels
	expandGapsForSubgraphs(g, layout, direction)

	// Step 6: Compute drawing coordinates
	computeDrawCoords(layout)

	// Step 6b: Scale node columns to fill target width (before subgraph bounds)
	if maxWidth > 0 {
		scaleNodeColumns(layout, maxWidth)
		// Recompute draw coords after scaling
		computeDrawCoords(layout)
	}

	// Step 7: Compute subgraph bounds
	computeSubgraphBounds(g, layout)

	// Step 8: Adjust for negative subgraph bounds
	adjustForNegativeBounds(layout)

	// Step 9: Compute canvas size
	computeCanvasSize(layout)

	return layout
}

// computeCanvasSize sets CanvasWidth/CanvasHeight from placements and subgraph bounds.
func computeCanvasSize(layout *GridLayout) {
	maxX := 0
	maxY := 0
	for _, p := range layout.Placements {
		if p.DrawX+p.DrawWidth > maxX {
			maxX = p.DrawX + p.DrawWidth
		}
		if p.DrawY+p.DrawHeight > maxY {
			maxY = p.DrawY + p.DrawHeight
		}
	}
	for _, sb := range layout.SubgraphBounds {
		if sb.X+sb.Width > maxX {
			maxX = sb.X + sb.Width
		}
		if sb.Y+sb.Height > maxY {
			maxY = sb.Y + sb.Height
		}
	}
	layout.CanvasWidth = maxX
	layout.CanvasHeight = maxY
}

// scaleNodeColumns proportionally scales node columns (center column of each
// node's 3×3 block) to fill targetWidth. Gap columns stay fixed so edges
// remain correctly attached at node boundaries.
func scaleNodeColumns(layout *GridLayout, targetWidth int) {
	// Compute current canvas width from column widths
	currentW := 0
	for _, w := range layout.ColWidths {
		currentW += w
	}

	slack := targetWidth - currentW
	if slack <= 0 {
		return // don't compress — content-driven width is the minimum
	}

	// Identify node columns
	nodeCols := map[int]bool{}
	for _, p := range layout.Placements {
		nodeCols[p.Grid.Col] = true
	}

	var scalable []int
	for c := range layout.ColWidths {
		if nodeCols[c] {
			scalable = append(scalable, c)
		}
	}
	if len(scalable) == 0 {
		return
	}

	// ColWidths is a map, so the column order above is Go's randomized map
	// order. The remainder loop below hands the leftover columns to the first
	// slack%n entries, which without this sort means a different column gets
	// widened on every render — the same diagram drawn twice comes out
	// different, and in a TUI that re-renders per frame it reads as flicker.
	sort.Ints(scalable)

	// Distribute slack evenly across node columns
	n := len(scalable)
	for i, c := range scalable {
		share := slack / n
		if i < slack%n {
			share++
		}
		layout.ColWidths[c] += share
	}
}

// edgeKey is a source-target pair used as a map key.
type edgeKey struct{ src, tgt string }

// assignLayers assigns each node to a layer based on longest path from a root.
// Back-edges (edges that would create cycles) are excluded from layer computation.
func assignLayers(g *graph.Graph) map[string]int {
	layers := make(map[string]int)
	roots := g.GetRoots()

	// BFS to assign initial layers
	for _, root := range roots {
		if _, ok := layers[root]; !ok {
			layers[root] = 0
		}
	}

	// Detect tree edges via BFS (shortest-path discovery).
	// BFS ensures each node is discovered at the shallowest depth,
	// so edges like F->D (where D is also reachable from B at a
	// shallower level) are correctly treated as back/cross-edges.
	treeEdges := make(map[edgeKey]bool)
	visited := make(map[string]bool)

	queue := make([]string, 0)
	for _, root := range roots {
		if !visited[root] {
			visited[root] = true
			queue = append(queue, root)
		}
	}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, child := range g.GetChildren(node) {
			if !visited[child] {
				visited[child] = true
				treeEdges[edgeKey{node, child}] = true
				queue = append(queue, child)
			}
		}
	}

	// Also BFS from any unvisited nodes (disconnected components)
	for _, nid := range g.NodeOrder {
		if !visited[nid] {
			visited[nid] = true
			queue = append(queue, nid)
			for len(queue) > 0 {
				node := queue[0]
				queue = queue[1:]
				for _, child := range g.GetChildren(node) {
					if !visited[child] {
						visited[child] = true
						treeEdges[edgeKey{node, child}] = true
						queue = append(queue, child)
					}
				}
			}
		}
	}

	// Build edge min_length lookup
	edgeMinLengths := make(map[edgeKey]int)
	for _, e := range g.Edges {
		key := edgeKey{e.Source, e.Target}
		if cur, ok := edgeMinLengths[key]; ok {
			if e.MinLength > cur {
				edgeMinLengths[key] = e.MinLength
			}
		} else {
			edgeMinLengths[key] = e.MinLength
		}
	}

	// Assign layers using only tree edges (no back-edges)
	changed := true
	maxIter := len(g.NodeOrder) * 2
	iteration := 0
	for changed && iteration < maxIter {
		changed = false
		iteration++
		for ek := range treeEdges {
			if srcLayer, ok := layers[ek.src]; ok {
				ml := 1
				if v, ok2 := edgeMinLengths[ek]; ok2 {
					ml = v
				}
				newLayer := srcLayer + ml
				if tgtLayer, ok2 := layers[ek.tgt]; !ok2 || tgtLayer < newLayer {
					layers[ek.tgt] = newLayer
					changed = true
				}
			}
		}
	}

	// Assign unplaced nodes to layer 0
	for _, nid := range g.NodeOrder {
		if _, ok := layers[nid]; !ok {
			layers[nid] = 0
		}
	}

	// Collapse orthogonal subgraph nodes to the same layer
	orthoSets := getOrthogonalSGNodes(g)
	if len(orthoSets) > 0 {
		for _, sgNodes := range orthoSets {
			var present []string
			for nid := range sgNodes {
				if _, ok := layers[nid]; ok {
					present = append(present, nid)
				}
			}
			if len(present) == 0 {
				continue
			}
			minLayer := math.MaxInt
			for _, nid := range present {
				if layers[nid] < minLayer {
					minLayer = layers[nid]
				}
			}
			for _, nid := range present {
				layers[nid] = minLayer
			}
		}

		// Recompute layers for non-ortho nodes from scratch so downstream
		// nodes get pulled up to the correct layer after collapse.
		allOrtho := make(map[string]bool)
		for _, s := range orthoSets {
			for nid := range s {
				allOrtho[nid] = true
			}
		}

		// Remove non-ortho nodes and recompute from roots
		for _, nid := range g.NodeOrder {
			if !allOrtho[nid] {
				delete(layers, nid)
			}
		}
		for _, root := range g.GetRoots() {
			if _, ok := layers[root]; !ok {
				layers[root] = 0
			}
		}

		changed = true
		maxIter = len(g.NodeOrder) * 2
		iteration = 0
		for changed && iteration < maxIter {
			changed = false
			iteration++
			for ek := range treeEdges {
				if srcLayer, ok := layers[ek.src]; ok {
					ml := 1
					if v, ok2 := edgeMinLengths[ek]; ok2 {
						ml = v
					}
					newLayer := srcLayer + ml
					if allOrtho[ek.tgt] {
						continue
					}
					if tgtLayer, ok2 := layers[ek.tgt]; !ok2 || tgtLayer < newLayer {
						layers[ek.tgt] = newLayer
						changed = true
					}
				}
			}
		}

		for _, nid := range g.NodeOrder {
			if _, ok := layers[nid]; !ok {
				layers[nid] = 0
			}
		}
	}

	return layers
}

// countCrossings counts the total number of edge crossings between adjacent layers.
func countCrossings(g *graph.Graph, layerLists [][]string) int {
	total := 0
	for layerIdx := 1; layerIdx < len(layerLists); layerIdx++ {
		prevPos := make(map[string]int)
		for i, nid := range layerLists[layerIdx-1] {
			prevPos[nid] = i
		}
		curPos := make(map[string]int)
		for i, nid := range layerLists[layerIdx] {
			curPos[nid] = i
		}
		// Collect edges between these two layers
		type intPair struct{ u, v int }
		var edgesBetween []intPair
		for _, edge := range g.Edges {
			srcP, srcOk := prevPos[edge.Source]
			tgtP, tgtOk := curPos[edge.Target]
			if srcOk && tgtOk {
				edgesBetween = append(edgesBetween, intPair{srcP, tgtP})
			}
		}
		// Count crossings: two edges (u1,v1) and (u2,v2) cross iff
		// (u1 < u2 and v1 > v2) or (u1 > u2 and v1 < v2)
		for i := 0; i < len(edgesBetween); i++ {
			for j := i + 1; j < len(edgesBetween); j++ {
				u1, v1 := edgesBetween[i].u, edgesBetween[i].v
				u2, v2 := edgesBetween[j].u, edgesBetween[j].v
				if (u1-u2)*(v1-v2) < 0 {
					total++
				}
			}
		}
	}
	return total
}

// orderLayers orders nodes within each layer using the barycenter heuristic.
func orderLayers(g *graph.Graph, layers map[string]int) [][]string {
	// Group nodes by layer
	maxLayer := 0
	for _, l := range layers {
		if l > maxLayer {
			maxLayer = l
		}
	}

	layerLists := make([][]string, maxLayer+1)
	for i := range layerLists {
		layerLists[i] = []string{}
	}
	for _, nid := range g.NodeOrder {
		l := layers[nid]
		layerLists[l] = append(layerLists[l], nid)
	}

	// Barycenter ordering with improvement tracking
	bestCrossings := countCrossings(g, layerLists)
	bestOrdering := copyLayers(layerLists)
	noImprovement := 0

	for pass := 0; pass < 8; pass++ {
		_ = pass
		for layerIdx := 1; layerIdx < len(layerLists); layerIdx++ {
			prevPositions := make(map[string]int)
			for i, nid := range layerLists[layerIdx-1] {
				prevPositions[nid] = i
			}
			barycenters := make(map[string]float64)
			for _, nid := range layerLists[layerIdx] {
				var predPositions []int
				for _, edge := range g.Edges {
					if edge.Target == nid {
						if pos, ok := prevPositions[edge.Source]; ok {
							predPositions = append(predPositions, pos)
						}
					}
				}
				if len(predPositions) > 0 {
					sum := 0
					for _, p := range predPositions {
						sum += p
					}
					barycenters[nid] = float64(sum) / float64(len(predPositions))
				} else {
					barycenters[nid] = float64(slices.Index(layerLists[layerIdx], nid))
				}
			}

			sort.SliceStable(layerLists[layerIdx], func(i, j int) bool {
				return barycenters[layerLists[layerIdx][i]] < barycenters[layerLists[layerIdx][j]]
			})
		}

		crossings := countCrossings(g, layerLists)
		if crossings < bestCrossings {
			bestCrossings = crossings
			bestOrdering = copyLayers(layerLists)
			noImprovement = 0
		} else {
			noImprovement++
		}

		if noImprovement >= 4 || bestCrossings == 0 {
			break
		}
	}

	layerLists = bestOrdering

	// Enforce topological order for orthogonal subgraph nodes in the same layer
	orthoSets := getOrthogonalSGNodes(g)
	if len(orthoSets) > 0 {
		for li := range layerLists {
			layer := layerLists[li]
			for _, sgNodes := range orthoSets {
				var inLayer []string
				for _, n := range layer {
					if sgNodes[n] {
						inLayer = append(inLayer, n)
					}
				}
				if len(inLayer) <= 1 {
					continue
				}
				// Build topological order from internal edges
				internal := make(map[string]bool)
				for _, n := range inLayer {
					internal[n] = true
				}
				successors := make(map[string][]string)
				inDegree := make(map[string]int)
				for _, n := range inLayer {
					successors[n] = nil
					inDegree[n] = 0
				}
				for _, edge := range g.Edges {
					if internal[edge.Source] && internal[edge.Target] {
						successors[edge.Source] = append(successors[edge.Source], edge.Target)
						inDegree[edge.Target]++
					}
				}
				// Kahn's algorithm
				var topoQueue []string
				for _, n := range inLayer {
					if inDegree[n] == 0 {
						topoQueue = append(topoQueue, n)
					}
				}
				var topo []string
				for len(topoQueue) > 0 {
					node := topoQueue[0]
					topoQueue = topoQueue[1:]
					topo = append(topo, node)
					for _, succ := range successors[node] {
						inDegree[succ]--
						if inDegree[succ] == 0 {
							topoQueue = append(topoQueue, succ)
						}
					}
				}
				// Replace in-layer positions: find positions of sg nodes, fill with topo order
				var positions []int
				for i, n := range layer {
					if internal[n] {
						positions = append(positions, i)
					}
				}
				for idx, pos := range positions {
					if idx < len(topo) {
						layer[pos] = topo[idx]
					}
				}
			}
		}
	}

	return layerLists
}

// placeNodes places nodes on the grid based on layer assignments.
func placeNodes(
	g *graph.Graph,
	layout *GridLayout,
	layerOrder [][]string,
	direction graph.Direction,
) {
	for layerIdx, nodes := range layerOrder {
		for posIdx, nid := range nodes {
			var col, row int
			if direction.IsHorizontal() {
				col = layerIdx*Stride + 1 // +1 for border cell
				row = posIdx*Stride + 1
			} else {
				col = posIdx*Stride + 1
				row = layerIdx*Stride + 1
			}

			gc := GridCoord{Col: col, Row: row}

			// Collision check: shift perpendicular if occupied
			for !canPlace(layout, gc) {
				if direction.IsHorizontal() {
					gc = GridCoord{Col: gc.Col, Row: gc.Row + Stride}
				} else {
					gc = GridCoord{Col: gc.Col + Stride, Row: gc.Row}
				}
			}

			placement := &NodePlacement{NodeID: nid, Grid: gc}
			layout.Placements[nid] = placement

			// Reserve 3x3 block
			for dc := -1; dc <= 1; dc++ {
				for dr := -1; dr <= 1; dr++ {
					layout.GridOccupied[GridCoord{gc.Col + dc, gc.Row + dr}] = nid
				}
			}
		}
	}
}

// canPlace checks if a 3x3 block centered at gc is free.
func canPlace(layout *GridLayout, gc GridCoord) bool {
	for dc := -1; dc <= 1; dc++ {
		for dr := -1; dr <= 1; dr++ {
			if !layout.IsFree(gc.Col+dc, gc.Row+dr, nil) {
				return false
			}
		}
	}
	return true
}

// WordWrap splits text at word boundaries, keeping lines under maxWidth.
func WordWrap(text string, maxWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	var lines []string
	currentLine := words[0]

	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) <= maxWidth {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	lines = append(lines, currentLine)
	return lines
}

// computeSizes computes column widths and row heights based on node content.
func computeSizes(
	g *graph.Graph,
	layout *GridLayout,
	paddingX, paddingY int,
) {
	for nid, placement := range layout.Placements {
		node := g.Nodes[nid]
		label := node.Label

		var lines []string
		if strings.Contains(label, "\\n") {
			lines = strings.Split(label, "\\n")
		} else {
			lines = []string{label}
		}

		// Word-wrap lines that exceed max width
		var wrappedLines []string
		for _, line := range lines {
			if len(line) <= MaxLabelWidth {
				wrappedLines = append(wrappedLines, line)
			} else {
				wrappedLines = append(wrappedLines, WordWrap(line, MaxLabelWidth)...)
			}
		}

		// Update the node's label with wrapped text
		if len(wrappedLines) > 1 && !slices.Equal(wrappedLines, lines) {
			node.Label = strings.Join(wrappedLines, "\\n")
		}

		textWidth := 0
		for _, l := range wrappedLines {
			if n := textwidth.String(l); n > textWidth {
				textWidth = n
			}
		}
		textHeight := len(wrappedLines)

		contentWidth := textWidth + paddingX   // padding on each side
		contentHeight := textHeight + paddingY // padding top/bottom

		// Start/end state markers are single-character — use minimal sizing
		// so they sit tight against the connecting edges.
		isMarker := node.Shape == graph.ShapeStartState || node.Shape == graph.ShapeEndState
		if isMarker {
			contentWidth = 1
			contentHeight = 1
		}

		// Ensure minimum sizes (but not for point markers)
		if !isMarker {
			if contentWidth < 3 {
				contentWidth = 3
			}
			if contentHeight < 3 {
				contentHeight = 3
			}
		}

		placement.ContentWidth = contentWidth
		placement.ContentHeight = contentHeight

		col := placement.Grid.Col
		row := placement.Grid.Row

		// Center column gets the content width
		if cur, ok := layout.ColWidths[col]; ok {
			if contentWidth > cur {
				layout.ColWidths[col] = contentWidth
			}
		} else {
			layout.ColWidths[col] = contentWidth
		}

		// Center row gets the content height
		if cur, ok := layout.RowHeights[row]; ok {
			if contentHeight > cur {
				layout.RowHeights[row] = contentHeight
			}
		} else {
			layout.RowHeights[row] = contentHeight
		}
	}

	// Border cells (around nodes) get width 1
	allCols := make(map[int]bool)
	allRows := make(map[int]bool)
	for _, placement := range layout.Placements {
		c, r := placement.Grid.Col, placement.Grid.Row
		for dc := -1; dc <= 1; dc++ {
			allCols[c+dc] = true
		}
		for dr := -1; dr <= 1; dr++ {
			allRows[r+dr] = true
		}
	}

	for c := range allCols {
		if _, ok := layout.ColWidths[c]; !ok {
			layout.ColWidths[c] = 1
		}
	}
	for r := range allRows {
		if _, ok := layout.RowHeights[r]; !ok {
			layout.RowHeights[r] = 1
		}
	}

	// Gap cells between nodes
	maxCol := 0
	maxRow := 0
	for c := range allCols {
		if c > maxCol {
			maxCol = c
		}
	}
	for r := range allRows {
		if r > maxRow {
			maxRow = r
		}
	}
	for c := 0; c < maxCol+2; c++ {
		if _, ok := layout.ColWidths[c]; !ok {
			layout.ColWidths[c] = 4 // gap columns
		}
	}
	for r := 0; r < maxRow+2; r++ {
		if _, ok := layout.RowHeights[r]; !ok {
			layout.RowHeights[r] = 3 // gap rows
		}
	}

	// Expand gaps to fit edge labels
	expandGapsForEdgeLabels(g, layout)
}

// expandGapsForEdgeLabels expands gap cells between nodes to fit edge labels.
func expandGapsForEdgeLabels(g *graph.Graph, layout *GridLayout) {
	direction := g.Direction.Normalized()
	isHorizontal := direction.IsHorizontal()

	for _, edge := range g.Edges {
		if edge.Label == "" {
			continue
		}
		labelLen := len(edge.Label)

		srcP := layout.Placements[edge.Source]
		tgtP := layout.Placements[edge.Target]
		if srcP == nil || tgtP == nil {
			continue
		}

		if isHorizontal {
			// Edges run horizontally -- label needs gap column width
			c1 := min(srcP.Grid.Col, tgtP.Grid.Col)
			c2 := max(srcP.Grid.Col, tgtP.Grid.Col)
			gapStart := c1 + 2
			gapEnd := c2 - 2
			if gapStart > gapEnd {
				continue
			}
			// Need: gap_width + 1 >= label_len + 2 -> gap_width >= label_len + 1
			needed := labelLen + 1
			cur := 4
			if v, ok := layout.ColWidths[gapStart]; ok {
				cur = v
			}
			if needed > cur {
				layout.ColWidths[gapStart] = needed
			}
		} else {
			// Edges run vertically -- label placed beside the line (x+1)
			r1 := min(srcP.Grid.Row, tgtP.Grid.Row)
			r2 := max(srcP.Grid.Row, tgtP.Grid.Row)
			gapStart := r1 + 2
			gapEnd := r2 - 2
			if gapStart > gapEnd {
				continue
			}
			// Need enough vertical space: at least 2 rows for the label
			cur := 3
			if v, ok := layout.RowHeights[gapStart]; ok {
				cur = v
			}
			if cur < 3 {
				layout.RowHeights[gapStart] = 3
			}

			// Also ensure the gap column beside the edge is wide enough
			// for the label text.
			srcCol := srcP.Grid.Col
			tgtCol := tgtP.Grid.Col
			gapCols := make(map[int]bool)
			if tgtCol >= srcCol {
				gapCols[srcCol+2] = true // gap to the right of source
			}
			if tgtCol <= srcCol {
				gapCols[srcCol-2] = true // gap to the left of source
			}
			// For edges crossing multiple columns, also expand intermediate gaps
			cMin := min(srcCol, tgtCol)
			cMax := max(srcCol, tgtCol)
			for c := cMin + 2; c < cMax; c += Stride {
				gapCols[c] = true
			}
			for gapCol := range gapCols {
				if gapCol >= 0 {
					if cur, ok := layout.ColWidths[gapCol]; ok {
						if labelLen+1 > cur {
							layout.ColWidths[gapCol] = labelLen + 1
						}
					}
				}
			}
		}
	}

	// For vertical flow: when multiple labeled edges leave the same source,
	// ensure the gap row is tall enough for all labels with spacing.
	if !isHorizontal {
		labeledPerSrc := make(map[string]int)
		for _, edge := range g.Edges {
			if edge.Label != "" {
				labeledPerSrc[edge.Source]++
			}
		}
		for srcID, count := range labeledPerSrc {
			if count < 2 {
				continue
			}
			srcP := layout.Placements[srcID]
			if srcP == nil {
				continue
			}
			gapRow := srcP.Grid.Row + 2
			needed := count*2 + 1 // 2 rows per label + spacing
			cur := 3
			if v, ok := layout.RowHeights[gapRow]; ok {
				cur = v
			}
			if needed > cur {
				layout.RowHeights[gapRow] = needed
			}
		}
	}
}

// normalizeSizes normalizes node dimensions within the same layer, capped at a maximum.
// Nodes at the same flow level (same layer) are normalized to the same
// Sizes are not normalized per layer.
//
// This package used to equalize every box within a layer, so a row of nodes
// shared one width and one height. It reads as tidy on a diagram whose labels
// are all about the same length, and as mostly-empty boxes on a real one: a
// one-word node was drawn as large as the three-line node beside it, because
// the layer's largest label set the size for all of them.
//
// Sizing each cell to its own content is the cheaper half of the fix. The box
// still fills its grid cell, so every edge attachment stays exactly where the
// router expects it — an earlier attempt that shrank the box inside its cell
// instead left arrows starting in mid-air, disconnected from the box they came
// from.
//
// What this does not fix: two nodes in different layers can still share a grid
// row or column, and that cell is sized to the larger of them. Removing that
// needs per-node geometry and a router that follows it.

// expandGapsForSubgraphs expands gap cells to accommodate subgraph borders, labels, and nesting.
func expandGapsForSubgraphs(
	g *graph.Graph, layout *GridLayout, direction graph.Direction,
) {
	if len(g.Subgraphs) == 0 {
		return
	}

	// Compute nesting depth for each node
	getDepth := func(nid string) int {
		depth := 0
		sg := g.FindSubgraphForNode(nid)
		for sg != nil {
			depth++
			sg = sg.Parent
		}
		return depth
	}

	nodeDepths := make(map[string]int)
	for _, nid := range g.NodeOrder {
		nodeDepths[nid] = getDepth(nid)
	}

	isVertical := direction == graph.DirTB || direction == graph.DirTD

	// Group nodes by flow-axis (layer) and cross-axis grid positions
	flowGroups := make(map[int][]string)
	crossGroups := make(map[int][]string)
	for nid, p := range layout.Placements {
		var flowPos, crossPos int
		if isVertical {
			flowPos = p.Grid.Row
			crossPos = p.Grid.Col
		} else {
			flowPos = p.Grid.Col
			crossPos = p.Grid.Row
		}
		flowGroups[flowPos] = append(flowGroups[flowPos], nid)
		crossGroups[crossPos] = append(crossGroups[crossPos], nid)
	}

	sortedFlow := sortedKeys(flowGroups)
	sortedCross := sortedKeys(crossGroups)

	// --- Expand flow-direction gaps (between layers) ---
	for i := 0; i < len(sortedFlow)-1; i++ {
		pos1 := sortedFlow[i]
		pos2 := sortedFlow[i+1]

		minDepth1 := math.MaxInt
		maxDepth1 := 0
		for _, nid := range flowGroups[pos1] {
			d := nodeDepths[nid]
			if d < minDepth1 {
				minDepth1 = d
			}
			if d > maxDepth1 {
				maxDepth1 = d
			}
		}

		minDepth2 := math.MaxInt
		maxDepth2 := 0
		for _, nid := range flowGroups[pos2] {
			d := nodeDepths[nid]
			if d < minDepth2 {
				minDepth2 = d
			}
			if d > maxDepth2 {
				maxDepth2 = d
			}
		}

		entering := maxDepth2 - minDepth1
		if entering < 0 {
			entering = 0
		}
		exiting := maxDepth1 - minDepth2
		if exiting < 0 {
			exiting = 0
		}

		depthChange := entering
		if exiting > depthChange {
			depthChange = exiting
		}

		// Detect sibling subgraph transitions: when adjacent layers are in
		// different subgraphs at the same depth, we need to exit one and
		// enter the other (2 boundary crossings, not 0).
		if depthChange == 0 {
			sgIDs1 := make(map[string]bool)
			for _, nid := range flowGroups[pos1] {
				if sg := g.FindSubgraphForNode(nid); sg != nil {
					sgIDs1[sg.ID] = true
				}
			}
			sgIDs2 := make(map[string]bool)
			for _, nid := range flowGroups[pos2] {
				if sg := g.FindSubgraphForNode(nid); sg != nil {
					sgIDs2[sg.ID] = true
				}
			}
			if len(sgIDs1) > 0 && len(sgIDs2) > 0 && !mapsEqual(sgIDs1, sgIDs2) {
				// Exiting one subgraph and entering another at same depth
				depthChange = 2
			}
		}

		if depthChange > 0 {
			extra := depthChange * SGGapPerLevel
			// Gap cells between the two node rows/columns
			gapStart := pos1 + 2
			gapEnd := pos2 - 2
			for gap := gapStart; gap <= gapEnd; gap++ {
				if isVertical {
					cur := 1
					if v, ok := layout.RowHeights[gap]; ok {
						cur = v
					}
					if extra > cur {
						layout.RowHeights[gap] = extra
					}
				} else {
					cur := 2
					if v, ok := layout.ColWidths[gap]; ok {
						cur = v
					}
					if extra > cur {
						layout.ColWidths[gap] = extra
					}
				}
			}
		}
	}

	// --- Expand cross-direction gaps (sibling subgraphs) ---
	for i := 0; i < len(sortedCross)-1; i++ {
		pos1 := sortedCross[i]
		pos2 := sortedCross[i+1]

		inner1 := make(map[string]bool)
		for _, nid := range crossGroups[pos1] {
			if sg := g.FindSubgraphForNode(nid); sg != nil {
				inner1[sg.ID] = true
			}
		}

		inner2 := make(map[string]bool)
		for _, nid := range crossGroups[pos2] {
			if sg := g.FindSubgraphForNode(nid); sg != nil {
				inner2[sg.ID] = true
			}
		}

		if (len(inner1) > 0 || len(inner2) > 0) && !mapsEqual(inner1, inner2) {
			extra := 8 // Space for two subgraph borders + gap
			gapStart := pos1 + 2
			gapEnd := pos2 - 2
			for gap := gapStart; gap <= gapEnd; gap++ {
				if isVertical {
					cur := 2
					if v, ok := layout.ColWidths[gap]; ok {
						cur = v
					}
					if extra > cur {
						layout.ColWidths[gap] = extra
					}
				} else {
					cur := 1
					if v, ok := layout.RowHeights[gap]; ok {
						cur = v
					}
					if extra > cur {
						layout.RowHeights[gap] = extra
					}
				}
			}
		}
	}
}

// computeDrawCoords converts grid positions to drawing coordinates.
func computeDrawCoords(layout *GridLayout) {
	for _, placement := range layout.Placements {
		gc := placement.Grid
		// Top-left of the 3x3 block
		x, y := layout.GridToDraw(gc.Col-1, gc.Row-1)
		w := 0
		for dc := -1; dc <= 1; dc++ {
			if cw, ok := layout.ColWidths[gc.Col+dc]; ok {
				w += cw
			} else {
				w++
			}
		}
		h := 0
		for dr := -1; dr <= 1; dr++ {
			if rh, ok := layout.RowHeights[gc.Row+dr]; ok {
				h += rh
			} else {
				h++
			}
		}
		placement.DrawX = x
		placement.DrawY = y
		placement.DrawWidth = w
		placement.DrawHeight = h
	}
}

// computeSubgraphBounds computes bounding boxes for subgraphs.
func computeSubgraphBounds(g *graph.Graph, layout *GridLayout) {
	var compute func(sg *graph.Subgraph) *SubgraphBounds
	compute = func(sg *graph.Subgraph) *SubgraphBounds {
		// Recursively compute children first
		var childBounds []SubgraphBounds
		for _, child := range sg.Children {
			cb := compute(child)
			if cb != nil {
				childBounds = append(childBounds, *cb)
				layout.SubgraphBounds = append(layout.SubgraphBounds, *cb)
			}
		}

		// Gather all node placements in this subgraph
		allNodeIDs := make(map[string]bool)
		for _, nid := range sg.NodeIDs {
			allNodeIDs[nid] = true
		}
		for _, child := range sg.Children {
			for _, nid := range child.NodeIDs {
				allNodeIDs[nid] = true
			}
			gatherAllNodes(child, allNodeIDs)
		}

		if len(allNodeIDs) == 0 && len(childBounds) == 0 {
			return nil
		}

		minX := math.MaxInt
		minY := math.MaxInt
		maxX := 0
		maxY := 0

		for nid := range allNodeIDs {
			if p, ok := layout.Placements[nid]; ok {
				if p.DrawX < minX {
					minX = p.DrawX
				}
				if p.DrawY < minY {
					minY = p.DrawY
				}
				if p.DrawX+p.DrawWidth > maxX {
					maxX = p.DrawX + p.DrawWidth
				}
				if p.DrawY+p.DrawHeight > maxY {
					maxY = p.DrawY + p.DrawHeight
				}
			}
		}

		for _, cb := range childBounds {
			if cb.X < minX {
				minX = cb.X
			}
			if cb.Y < minY {
				minY = cb.Y
			}
			if cb.X+cb.Width > maxX {
				maxX = cb.X + cb.Width
			}
			if cb.Y+cb.Height > maxY {
				maxY = cb.Y + cb.Height
			}
		}

		if minX == math.MaxInt {
			return nil
		}

		contentWidth := (maxX - minX) + SGBorderPad*2
		labelWidth := len(sg.Label) + 4
		finalWidth := contentWidth
		if labelWidth > finalWidth {
			finalWidth = labelWidth
		}

		bounds := &SubgraphBounds{
			Subgraph: sg,
			X:        minX - SGBorderPad,
			Y:        minY - SGBorderPad - SGLabelHeight,
			Width:    finalWidth,
			Height:   (maxY - minY) + SGBorderPad*2 + SGLabelHeight,
		}
		return bounds
	}

	for _, sg := range g.Subgraphs {
		bounds := compute(sg)
		if bounds != nil {
			layout.SubgraphBounds = append(layout.SubgraphBounds, *bounds)
		}
	}
}

// adjustForNegativeBounds shifts all coordinates if subgraph bounds extend into negative space.
func adjustForNegativeBounds(layout *GridLayout) {
	if len(layout.SubgraphBounds) == 0 {
		return
	}

	minX := 0
	minY := 0
	for _, sb := range layout.SubgraphBounds {
		if sb.X < minX {
			minX = sb.X
		}
		if sb.Y < minY {
			minY = sb.Y
		}
	}

	if minX >= 0 && minY >= 0 {
		return
	}

	dx := 0
	if minX < 0 {
		dx = -minX + 1
	}
	dy := 0
	if minY < 0 {
		dy = -minY + 1
	}

	for _, p := range layout.Placements {
		p.DrawX += dx
		p.DrawY += dy
	}

	for i := range layout.SubgraphBounds {
		layout.SubgraphBounds[i].X += dx
		layout.SubgraphBounds[i].Y += dy
	}

	layout.OffsetX += dx
	layout.OffsetY += dy
}

// gatherAllNodes recursively gathers all node IDs from a subgraph and its children.
func gatherAllNodes(sg *graph.Subgraph, result map[string]bool) {
	for _, nid := range sg.NodeIDs {
		result[nid] = true
	}
	for _, child := range sg.Children {
		gatherAllNodes(child, result)
	}
}

// getOrthogonalSGNodes finds sets of node IDs in subgraphs whose direction
// is orthogonal to the graph's direction.
func getOrthogonalSGNodes(g *graph.Graph) []map[string]bool {
	graphVertical := g.Direction.Normalized().IsVertical()
	var result []map[string]bool

	var walk func(subs []*graph.Subgraph)
	walk = func(subs []*graph.Subgraph) {
		for _, sg := range subs {
			if sg.Direction != nil {
				sgVertical := sg.Direction.Normalized().IsVertical()
				if sgVertical != graphVertical {
					nodeSet := make(map[string]bool)
					for _, nid := range sg.NodeIDs {
						nodeSet[nid] = true
					}
					result = append(result, nodeSet)
				}
			}
			walk(sg.Children)
		}
	}

	walk(g.Subgraphs)
	return result
}

// --- Helper functions ---

// copyLayers creates a deep copy of layer lists.
func copyLayers(layers [][]string) [][]string {
	result := make([][]string, len(layers))
	for i, layer := range layers {
		result[i] = make([]string, len(layer))
		copy(result[i], layer)
	}
	return result
}

// sortedKeys returns the sorted keys of a map.
func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// mapsEqual checks if two map[string]bool sets are equal.
func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
