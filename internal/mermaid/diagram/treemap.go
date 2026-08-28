package diagram

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// ── model ────────────────────────────────────────────────────────

type treemapNode struct {
	label    string
	value    float64
	children []treemapNode
}

func (n *treemapNode) totalValue() float64 {
	if len(n.children) > 0 {
		total := 0.0
		for i := range n.children {
			total += n.children[i].totalValue()
		}
		return total
	}
	return n.value
}

type treemap struct {
	roots []treemapNode
}

func (t *treemap) totalValue() float64 {
	total := 0.0
	for i := range t.roots {
		total += t.roots[i].totalValue()
	}
	return total
}

// ── layout constants ─────────────────────────────────────────────

const (
	tmMinBoxW  = 4
	tmMinBoxH  = 3
	tmGap      = 1
	tmLabelPad = 0
)

// ── parser ───────────────────────────────────────────────────────

var reTreemapNode = regexp.MustCompile(`^(\s*)"([^"]+)"(?:\s*:\s*([0-9]+(?:\.[0-9]*)?))?`)

// RenderTreemap parses and renders a Mermaid treemap diagram.
func RenderTreemap(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	tm := parseTreemap(source)
	return renderTreemap(tm, useASCII, theme)
}

func parseTreemap(source string) *treemap {
	lines := strings.Split(strings.TrimSpace(source), "\n")
	tm := &treemap{}

	if len(lines) == 0 {
		return tm
	}

	// Skip header line (treemap-beta)
	type bodyLine struct {
		indent int
		label  string
		value  float64
	}
	var bodyLines []bodyLine

	for _, line := range lines[1:] {
		// Strip comments
		if idx := strings.Index(line, "%%"); idx >= 0 {
			line = line[:idx]
		}

		m := reTreemapNode.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		indent := len(m[1])
		label := m[2]
		value := 0.0
		if m[3] != "" {
			value, _ = strconv.ParseFloat(m[3], 64)
		}
		bodyLines = append(bodyLines, bodyLine{indent, label, value})
	}

	if len(bodyLines) == 0 {
		return tm
	}

	// Build tree from indentation using a stack
	type stackEntry struct {
		indent int
		node   *treemapNode
	}
	var stack []stackEntry

	for _, bl := range bodyLines {
		node := treemapNode{label: bl.label, value: bl.value}

		// Pop stack until we find a parent with less indentation
		for len(stack) > 0 && stack[len(stack)-1].indent >= bl.indent {
			stack = stack[:len(stack)-1]
		}

		if len(stack) > 0 {
			stack[len(stack)-1].node.children = append(stack[len(stack)-1].node.children, node)
			// Get a pointer to the just-appended child
			parent := stack[len(stack)-1].node
			childPtr := &parent.children[len(parent.children)-1]
			stack = append(stack, stackEntry{bl.indent, childPtr})
		} else {
			tm.roots = append(tm.roots, node)
			stack = append(stack, stackEntry{bl.indent, &tm.roots[len(tm.roots)-1]})
		}
	}

	return tm
}

// ── renderer ─────────────────────────────────────────────────────

func renderTreemap(tm *treemap, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	cs := renderer.UNICODE
	if useASCII {
		cs = renderer.ASCII
	}

	if len(tm.roots) == 0 {
		return renderer.NewCanvas(1, 1)
	}

	total := tm.totalValue()
	if total <= 0 {
		return renderer.NewCanvas(1, 1)
	}

	canvasH := tmComputeHeight(tm.roots)
	minW := tmComputeMinWidth(tm.roots)

	// Scale width: proportional for small diagrams, tight for large ones
	// Use terminal width to decide layout, but never truncate labels
	termW := usableWidth()
	canvasW := max(minW, min(termW-2, max(60, int(float64(minW)*1.6))))

	c := renderer.NewCanvas(canvasW, canvasH)

	// Render each root with its own section index for per-hue coloring
	tmLayoutRoots(c, cs, tm.roots, 0, 0, canvasW, canvasH, theme)
	return c
}

// tmLayoutRoots distributes top-level nodes and assigns each a unique section index.
func tmLayoutRoots(c *renderer.Canvas, cs renderer.CharSet, nodes []treemapNode, x, y, w, h int, theme *renderer.Theme) {
	if len(nodes) == 0 || w < tmMinBoxW || h < tmMinBoxH {
		return
	}

	total := 0.0
	for i := range nodes {
		total += nodes[i].totalValue()
	}
	if total <= 0 {
		return
	}

	// Compute widths (same proportional logic as tmSliceLayout)
	nGaps := len(nodes) - 1
	totalGapW := tmGap * nGaps
	usableW := w - totalGapW

	minWidths := make([]int, len(nodes))
	for i := range nodes {
		if len(nodes[i].children) > 0 {
			minWidths[i] = tmComputeMinWidth(nodes[i].children) + 2
		} else {
			labelW := len(nodes[i].label) + 2
			minWidths[i] = max(tmMinBoxW, labelW)
		}
	}

	sizes := make([]float64, len(nodes))
	for i := range nodes {
		sizes[i] = nodes[i].totalValue() / total * float64(usableW)
	}
	for iter := 0; iter < 3; iter++ {
		deficit := 0.0
		surplusTotal := 0.0
		for i := range sizes {
			if sizes[i] < float64(minWidths[i]) {
				deficit += float64(minWidths[i]) - sizes[i]
				sizes[i] = float64(minWidths[i])
			} else {
				surplusTotal += sizes[i] - float64(minWidths[i])
			}
		}
		if deficit > 0 && surplusTotal > 0 {
			scale := math.Max(0, 1-deficit/surplusTotal)
			for i := range sizes {
				if sizes[i] > float64(minWidths[i]) {
					excess := sizes[i] - float64(minWidths[i])
					sizes[i] = float64(minWidths[i]) + excess*scale
				}
			}
		}
	}

	intSizes := make([]int, len(sizes))
	for i := range sizes {
		intSizes[i] = max(minWidths[i], int(math.Round(sizes[i])))
	}
	currentTotal := 0
	for _, s := range intSizes {
		currentTotal += s
	}
	if currentTotal != usableW {
		diff := usableW - currentTotal
		largestIdx := 0
		for i := range intSizes {
			if intSizes[i] > intSizes[largestIdx] {
				largestIdx = i
			}
		}
		intSizes[largestIdx] = max(minWidths[largestIdx], intSizes[largestIdx]+diff)
	}

	posX := x
	for i := range nodes {
		bw := intSizes[i]
		if bw > x+w-posX {
			bw = x + w - posX
		}
		if bw < tmMinBoxW {
			break
		}
		tmDrawNode(c, cs, &nodes[i], posX, y, bw, h, 0, i, theme)
		posX += bw
		if i < nGaps {
			posX += tmGap
		}
	}
}

func tmComputeHeight(nodes []treemapNode) int {
	maxH := 0
	for i := range nodes {
		var h int
		if len(nodes[i].children) > 0 {
			childH := tmComputeHeight(nodes[i].children)
			h = childH + 4 // top border + label + child area + bottom border
		} else {
			h = 4 // top border + label + value + bottom border
		}
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

func tmComputeMinWidth(nodes []treemapNode) int {
	total := 0
	for i := range nodes {
		var nodeW int
		if len(nodes[i].children) > 0 {
			childW := tmComputeMinWidth(nodes[i].children)
			nodeW = childW + 2
		} else {
			labelW := len(nodes[i].label) + tmLabelPad
			nodeW = max(tmMinBoxW, labelW+2)
		}
		total += nodeW
	}
	// Add gaps between siblings
	total += tmGap * max(0, len(nodes)-1)
	return total
}

func tmLayoutNodes(c *renderer.Canvas, cs renderer.CharSet, nodes []treemapNode, x, y, w, h, depth, sectionIdx int, theme *renderer.Theme) {
	if len(nodes) == 0 || w < tmMinBoxW || h < tmMinBoxH {
		return
	}

	total := 0.0
	for i := range nodes {
		total += nodes[i].totalValue()
	}
	if total <= 0 {
		return
	}

	// Sort by value descending for better layout
	sorted := make([]int, len(nodes))
	for i := range sorted {
		sorted[i] = i
	}
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			if nodes[sorted[j]].totalValue() > nodes[sorted[j-1]].totalValue() {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			} else {
				break
			}
		}
	}

	sortedNodes := make([]treemapNode, len(sorted))
	for i, idx := range sorted {
		sortedNodes[i] = nodes[idx]
	}

	tmSliceLayout(c, cs, sortedNodes, x, y, w, h, total, depth, sectionIdx, theme)
}

func tmSliceLayout(c *renderer.Canvas, cs renderer.CharSet, nodes []treemapNode, x, y, w, h int, total float64, depth, sectionIdx int, theme *renderer.Theme) {
	nGaps := len(nodes) - 1
	totalGapW := tmGap * nGaps
	usableW := w - totalGapW

	if usableW < tmMinBoxW*len(nodes) {
		// Gaps do not fit, so drop them: usableW takes the full width and the
		// gap count goes to zero. totalGapW is not reset because nothing reads
		// it past this point.
		usableW = w
		nGaps = 0
	}

	// Compute minimum widths for each node (must fit labels without truncation)
	minWidths := make([]int, len(nodes))
	for i := range nodes {
		if len(nodes[i].children) > 0 {
			minWidths[i] = tmComputeMinWidth(nodes[i].children) + 2
		} else {
			labelW := len(nodes[i].label) + 2 // borders
			minWidths[i] = max(tmMinBoxW, labelW)
		}
	}

	// Distribute width proportionally, respecting minimums
	rawSizes := make([]float64, len(nodes))
	for i := range nodes {
		rawSizes[i] = nodes[i].totalValue() / total * float64(usableW)
	}

	// Adjust: ensure minimums are met, redistribute excess
	sizes := make([]float64, len(rawSizes))
	copy(sizes, rawSizes)
	for iter := 0; iter < 3; iter++ {
		deficit := 0.0
		surplusTotal := 0.0
		for i := range sizes {
			if sizes[i] < float64(minWidths[i]) {
				deficit += float64(minWidths[i]) - sizes[i]
				sizes[i] = float64(minWidths[i])
			} else {
				surplusTotal += sizes[i] - float64(minWidths[i])
			}
		}
		if deficit > 0 && surplusTotal > 0 {
			scale := math.Max(0, 1-deficit/surplusTotal)
			for i := range sizes {
				if sizes[i] > float64(minWidths[i]) {
					excess := sizes[i] - float64(minWidths[i])
					sizes[i] = float64(minWidths[i]) + excess*scale
				}
			}
		}
	}

	// Round to integers
	intSizes := make([]int, len(sizes))
	for i := range sizes {
		intSizes[i] = max(minWidths[i], int(math.Round(sizes[i])))
	}

	// Fix total to match usableW
	currentTotal := 0
	for _, v := range intSizes {
		currentTotal += v
	}
	if currentTotal != usableW {
		diff := usableW - currentTotal
		largestIdx := 0
		for i := range intSizes {
			if intSizes[i] > intSizes[largestIdx] {
				largestIdx = i
			}
		}
		intSizes[largestIdx] = max(minWidths[largestIdx], intSizes[largestIdx]+diff)
	}

	// Draw each node
	posX := x
	for i := range nodes {
		bw := intSizes[i]
		// Clamp to available space
		if bw > x+w-posX {
			bw = x + w - posX
		}
		if bw < tmMinBoxW {
			break
		}
		tmDrawNode(c, cs, &nodes[i], posX, y, bw, h, depth, sectionIdx, theme)
		posX += bw
		if i < nGaps {
			posX += tmGap
		}
	}
}

func tmDrawNode(c *renderer.Canvas, cs renderer.CharSet, node *treemapNode, x, y, w, h, depth, sectionIdx int, theme *renderer.Theme) {
	if w < tmMinBoxW || h < tmMinBoxH {
		return
	}

	isSection := len(node.children) > 0

	var hz, vt rune
	if isSection {
		hz = cs.LineDottedH
		vt = cs.LineDottedV
	} else {
		hz = cs.Horizontal
		vt = cs.Vertical
	}

	tl := cs.TopLeft
	tr := cs.TopRight
	bl := cs.BottomLeft
	br := cs.BottomRight

	// Determine styles — use region colors if theme supports it
	var borderStyle, fillStyle, labelStyle, valueStyle string
	useDirectANSI := theme != nil && theme.HasDepthColors()
	if useDirectANSI {
		// Wrap raw ANSI in _ansi: prefix so ToColorString uses them directly
		fillStyle = "_ansi:" + theme.RegionStyle(sectionIdx, depth)
		borderStyle = "_ansi:" + theme.RegionBorderStyle(sectionIdx, depth)
		labelStyle = "_ansi:" + theme.RegionLabelStyle(sectionIdx, depth)
		valueStyle = "_ansi:" + theme.RegionStyle(sectionIdx, depth)
	} else {
		if isSection {
			borderStyle = "subgraph"
		} else {
			borderStyle = "node"
		}
		fillStyle = borderStyle
		labelStyle = "label"
		valueStyle = "edge_label"
	}

	// Fill entire box area (borders + interior) with the fill style
	if useDirectANSI {
		for row := y; row < y+h; row++ {
			for col := x; col < x+w; col++ {
				c.SetStyle(row, col, fillStyle)
			}
		}
	}

	// Top border
	c.Put(y, x, tl, false, borderStyle)
	for col := x + 1; col < x+w-1; col++ {
		c.Put(y, col, hz, false, borderStyle)
	}
	c.Put(y, x+w-1, tr, false, borderStyle)

	// Bottom border
	c.Put(y+h-1, x, bl, false, borderStyle)
	for col := x + 1; col < x+w-1; col++ {
		c.Put(y+h-1, col, hz, false, borderStyle)
	}
	c.Put(y+h-1, x+w-1, br, false, borderStyle)

	// Side borders + interior fill
	for row := y + 1; row < y+h-1; row++ {
		c.Put(row, x, vt, false, borderStyle)
		c.Put(row, x+w-1, vt, false, borderStyle)
		if !useDirectANSI {
			for col := x + 1; col < x+w-1; col++ {
				c.SetStyle(row, col, fillStyle)
			}
		}
	}

	// Label -- centered on the first inner row
	label := node.label
	innerW := w - 2
	if len(label) > innerW {
		if innerW > 1 {
			label = label[:innerW-1] + "\u2026"
		} else {
			label = label[:innerW]
		}
	}
	labelCol := x + 1 + max(0, (innerW-len(label))/2)
	c.PutText(y+1, labelCol, label, labelStyle)

	// Value (for leaves only)
	if !isSection && node.value > 0 && h >= 4 {
		valStr := fmt.Sprintf("%g", node.value)
		if len(valStr) > innerW {
			valStr = valStr[:innerW]
		}
		valCol := x + 1 + max(0, (innerW-len(valStr))/2)
		c.PutText(y+2, valCol, valStr, valueStyle)
	}

	// Recurse into children
	if isSection {
		innerX := x + 1
		innerY := y + 2
		innerWVal := w - 2
		innerH := h - 3

		if innerWVal >= tmMinBoxW && innerH >= tmMinBoxH {
			tmLayoutNodes(c, cs, node.children, innerX, innerY, innerWVal, innerH, depth+1, sectionIdx, theme)
		}
	}
}
