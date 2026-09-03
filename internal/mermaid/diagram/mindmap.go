package diagram

import (
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

type mindmapNode struct {
	label    string
	shape    string // "", "()", "((", "[", "{{", "))"
	children []*mindmapNode
}

func parseMindmap(source string) *mindmapNode {
	lines := strings.Split(source, "\n")
	var root *mindmapNode

	// Stack of (node, indent) for tracking tree structure
	type stackEntry struct {
		node   *mindmapNode
		indent int
	}
	var stack []stackEntry

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "mindmap") {
			continue
		}

		indent := 0
		for _, ch := range line {
			switch ch {
			case ' ':
				indent++
				continue
			case '\t':
				indent += 4
				continue
			}
			break
		}

		// Parse node shape and label
		label, shape := parseMindmapLabel(trimmed)

		node := &mindmapNode{label: label, shape: shape}

		if root == nil {
			root = node
			stack = []stackEntry{{node, indent}}
			continue
		}

		// Pop stack to find parent
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		parent := stack[len(stack)-1].node
		parent.children = append(parent.children, node)
		stack = append(stack, stackEntry{node, indent})
	}

	return root
}

func parseMindmapLabel(s string) (string, string) {
	// Strip leading ID: "root((label))" -> "((label))"
	// Find the first shape delimiter
	for i, ch := range s {
		if ch == '(' || ch == '[' || ch == '{' || ch == ')' {
			if i > 0 {
				s = s[i:]
			}
			break
		}
	}

	// ((label)) — cloud/circle
	if strings.HasPrefix(s, "((") && strings.HasSuffix(s, "))") {
		return s[2 : len(s)-2], "(("
	}
	// (label) — rounded
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		return s[1 : len(s)-1], "()"
	}
	// [label] — square
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s[1 : len(s)-1], "[]"
	}
	// {{label}} — hexagon
	if strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		return s[2 : len(s)-2], "{{}}"
	}
	// ))label(( — bang
	if strings.HasPrefix(s, "))") && strings.HasSuffix(s, "((") {
		return s[2 : len(s)-2], "))(("
	}
	return s, ""
}

// RenderMindmap parses and renders a Mermaid mindmap.
func RenderMindmap(source string, useASCII bool) *renderer.Canvas {
	root := parseMindmap(source)
	if root == nil {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[mindmap] empty", "default")
		return c
	}

	// Measure the tree to determine canvas size
	type laidOut struct {
		node *mindmapNode
		col  int
		row  int
	}

	// Simple layout: root on left, children branching right.
	// Each subtree occupies a vertical span.
	var nodes []laidOut
	colWidth := 0

	// First pass: compute max label width per depth level
	depthWidths := map[int]int{}
	var measureDepth func(n *mindmapNode, depth int)
	measureDepth = func(n *mindmapNode, depth int) {
		w := len(n.label) + 4 // box padding
		if w > depthWidths[depth] {
			depthWidths[depth] = w
		}
		for _, ch := range n.children {
			measureDepth(ch, depth+1)
		}
	}
	measureDepth(root, 0)

	// Column positions (cumulative)
	depthX := map[int]int{}
	maxDepth := 0
	for d := range depthWidths {
		if d > maxDepth {
			maxDepth = d
		}
	}

	// Compute natural width at base gap, then scale gap to fit terminal
	baseGap := 4
	naturalW := 0
	for d := 0; d <= maxDepth; d++ {
		naturalW += depthWidths[d] + baseGap
	}
	gap := baseGap
	if maxDepth > 0 {
		gap = scaleGap(baseGap, maxDepth, naturalW, 2, 8)
	}

	x := 0
	for d := 0; d <= maxDepth; d++ {
		depthX[d] = x
		x += depthWidths[d] + gap
		if depthWidths[d] > colWidth {
			colWidth = depthWidths[d]
		}
	}
	totalWidth := x

	// Second pass: assign row positions (each leaf gets one row, parents center on children)
	nextRow := 0
	var layout func(n *mindmapNode, depth int) (startRow, endRow int)
	layout = func(n *mindmapNode, depth int) (int, int) {
		if len(n.children) == 0 {
			row := nextRow
			nextRow += 3 // 3 rows per node (box top, content, box bottom)
			nodes = append(nodes, laidOut{n, depthX[depth], row})
			return row, row + 2
		}

		childStart := nextRow
		childEnd := childStart
		for _, ch := range n.children {
			_, end := layout(ch, depth+1)
			childEnd = end
		}

		// Center parent on children
		row := (childStart + childEnd) / 2
		// Ensure row is on a content line (odd offset from box top)
		if (row-childStart)%3 != 1 {
			row = childStart + 1
		}
		nodes = append(nodes, laidOut{n, depthX[depth], row - 1})
		return childStart, childEnd
	}

	startR, endR := layout(root, 0)
	totalHeight := endR - startR + 2

	c := renderer.NewCanvas(totalWidth+1, totalHeight+1)

	hLine := '─'
	vLine := '│'
	tl := '╭'
	tr := '╮'
	bl := '╰'
	br := '╯'
	if useASCII {
		hLine = '-'
		vLine = '|'
		tl = '+'
		tr = '+'
		bl = '+'
		br = '+'
	}

	// Draw nodes
	for _, ln := range nodes {
		n := ln.node
		row := ln.row
		col := ln.col
		boxW := len(n.label) + 4

		c.Put(row, col, tl, false, "node")
		c.DrawHorizontal(row, col+1, col+boxW-2, hLine, "node")
		c.Put(row, col+boxW-1, tr, false, "node")

		c.Put(row+1, col, vLine, false, "node")
		c.PutText(row+1, col+2, n.label, "label")
		c.Put(row+1, col+boxW-1, vLine, false, "node")

		c.Put(row+2, col, bl, false, "node")
		c.DrawHorizontal(row+2, col+1, col+boxW-2, hLine, "node")
		c.Put(row+2, col+boxW-1, br, false, "node")

		// Fill interior spaces so background themes render solid
		for cx := col + 1; cx < col+boxW-1; cx++ {
			if c.Get(row+1, cx) == ' ' {
				c.SetStyle(row+1, cx, "node")
			}
		}
	}

	// Draw edges: horizontal line from parent box right edge to child box left edge
	var drawEdges func(n *mindmapNode, depth int)
	drawEdges = func(n *mindmapNode, depth int) {
		if len(n.children) == 0 {
			return
		}

		// Find parent's row
		parentRow := -1
		parentBoxW := len(n.label) + 4
		parentX := depthX[depth]
		for _, ln := range nodes {
			if ln.node == n {
				parentRow = ln.row + 1 // content row
				break
			}
		}

		for _, ch := range n.children {
			childRow := -1
			childX := depthX[depth+1]
			for _, ln := range nodes {
				if ln.node == ch {
					childRow = ln.row + 1
					break
				}
			}

			if parentRow >= 0 && childRow >= 0 {
				// Horizontal from parent to midpoint
				midX := parentX + parentBoxW + (childX-parentX-parentBoxW)/2
				c.DrawHorizontal(parentRow, parentX+parentBoxW, midX, hLine, "edge")

				// Vertical from parentRow to childRow
				if parentRow != childRow {
					c.DrawVertical(midX, parentRow, childRow, vLine, "edge")
				}

				// Horizontal from midpoint to child
				c.DrawHorizontal(childRow, midX, childX-1, hLine, "edge")
			}

			drawEdges(ch, depth+1)
		}
	}
	drawEdges(root, 0)

	return c
}
