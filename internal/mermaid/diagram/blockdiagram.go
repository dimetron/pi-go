package diagram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// ── model ────────────────────────────────────────────────────────

type blockNode struct {
	id       string
	label    string
	shape    string // "rectangle", "rounded", "diamond", "circle", etc.
	colSpan  int
	isSpace  bool
	children []blockNode
	columns  int // for nested groups (0 = inherit)
}

type blockLink struct {
	source string
	target string
	label  string
}

type blockDiagram struct {
	blocks  []blockNode
	links   []blockLink
	columns int
}

// ── layout constants ─────────────────────────────────────────────

const (
	blockPad    = 2
	minBlockW   = 12
	minBlockH   = 5
	blockRowGap = 2
	blockMargin = 2
	groupPad    = 2
)

// blockColumnGap returns the column gap scaled to available width.
func blockColumnGap(colCount int, contentW int) int {
	if colCount <= 1 {
		return 4
	}
	naturalW := blockMargin*2 + contentW + 4*(colCount-1)
	return scaleGap(4, colCount-1, naturalW, 2, 8)
}

// ── shape patterns ───────────────────────────────────────────────

// shapePattern maps delimiter pairs to shape names.
type shapePattern struct {
	open  string
	close string
	shape string
}

var shapePatterns = []shapePattern{
	{"(((", ")))", "double_circle"},
	{"((", "))", "circle"},
	{"([", "])", "stadium"},
	{"[(", ")]", "cylinder"},
	{"[[", "]]", "subroutine"},
	{"[/", "\\]", "trapezoid"},
	{"[\\", "/]", "trapezoid_alt"},
	{"[/", "/]", "parallelogram"},
	{"[\\", "\\]", "parallelogram_alt"},
	{"{{", "}}", "hexagon"},
	{"{", "}", "diamond"},
	{"(", ")", "rounded"},
	{">", "]", "asymmetric"},
	{"[", "]", "rectangle"},
}

// shapeToNodeShape converts a shape string to a graph.NodeShape.
var shapeToNodeShape = map[string]graph.NodeShape{
	"rectangle":         graph.ShapeRectangle,
	"rounded":           graph.ShapeRounded,
	"diamond":           graph.ShapeDiamond,
	"circle":            graph.ShapeCircle,
	"double_circle":     graph.ShapeDoubleCircle,
	"stadium":           graph.ShapeStadium,
	"cylinder":          graph.ShapeCylinder,
	"subroutine":        graph.ShapeSubroutine,
	"hexagon":           graph.ShapeHexagon,
	"asymmetric":        graph.ShapeAsymmetric,
	"trapezoid":         graph.ShapeTrapezoid,
	"trapezoid_alt":     graph.ShapeTrapezoidAlt,
	"parallelogram":     graph.ShapeParallelogram,
	"parallelogram_alt": graph.ShapeParallelogramAlt,
}

// ── parser regexes ───────────────────────────────────────────────

var (
	reLinkLabel  = regexp.MustCompile(`^(\S+)\s*--\s*"([^"]*)"\s*-->\s*(\S+)$`)
	reLinkSimple = regexp.MustCompile(`^(\S+)\s*-->\s*(\S+)$`)
	reBlockArrow = regexp.MustCompile(`^(\w+)<\["([^"]*)"\]>\([^)]+\)$`)
	reSpaceBlock = regexp.MustCompile(`(?i)^space(?::(\d+))?$`)
	reColSpan    = regexp.MustCompile(`:(\d+)$`)
	reBareID     = regexp.MustCompile(`^[\w][\w.\-]*$`)
)

var blockAnonCounter int

// ── parser ───────────────────────────────────────────────────────

// RenderBlockDiagram parses and renders a Mermaid block diagram.
func RenderBlockDiagram(source string, useASCII bool) *renderer.Canvas {
	bd := parseBlockDiagram(source)
	return renderBlockDiagram(bd, useASCII)
}

func parseBlockDiagram(source string) *blockDiagram {
	blockAnonCounter = 0
	lines := blockPreprocess(source)
	blocks, links, columns, _ := parseBlockGroup(lines, 0)
	return &blockDiagram{blocks: blocks, links: links, columns: columns}
}

func blockPreprocess(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n")
	var result []string
	for _, line := range raw {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		if idx := strings.Index(stripped, "%%"); idx >= 0 {
			stripped = strings.TrimSpace(stripped[:idx])
			if stripped == "" {
				continue
			}
		}
		result = append(result, stripped)
	}
	// Remove header line
	if len(result) > 0 {
		lower := strings.ToLower(result[0])
		if lower == "block-beta" || lower == "block" {
			result = result[1:]
		}
	}
	return result
}

func nextBlockAnonID() string {
	blockAnonCounter++
	return fmt.Sprintf("_anon_group_%d", blockAnonCounter)
}

func parseBlockGroup(lines []string, i int) ([]blockNode, []blockLink, int, int) {
	var blocks []blockNode
	var links []blockLink
	columns := 0

	for i < len(lines) {
		line := lines[i]
		lower := strings.ToLower(strings.TrimSpace(line))

		// End of group
		if lower == "end" {
			return blocks, links, columns, i + 1
		}

		// Skip directives
		if strings.HasPrefix(lower, "classdef ") || strings.HasPrefix(lower, "style ") || strings.HasPrefix(lower, "class ") {
			i++
			continue
		}

		// Columns directive
		if m := regexp.MustCompile(`(?i)columns\s+(\d+)`).FindStringSubmatch(line); m != nil {
			columns, _ = strconv.Atoi(m[1])
			i++
			continue
		}

		// Named nested group: block:ID or block:ID:N
		if m := regexp.MustCompile(`(?i)block\s*:\s*(\w+)(?:\s*:\s*(\d+))?$`).FindStringSubmatch(line); m != nil {
			groupID := m[1]
			colSpan := 1
			if m[2] != "" {
				colSpan, _ = strconv.Atoi(m[2])
			}
			children, childLinks, childCols, nextI := parseBlockGroup(lines, i+1)
			blocks = append(blocks, blockNode{
				id:       groupID,
				label:    groupID,
				shape:    "rectangle",
				colSpan:  colSpan,
				children: children,
				columns:  childCols,
			})
			links = append(links, childLinks...)
			i = nextI
			continue
		}

		// Anonymous nested group: bare "block"
		if lower == "block" {
			groupID := nextBlockAnonID()
			children, childLinks, childCols, nextI := parseBlockGroup(lines, i+1)
			blocks = append(blocks, blockNode{
				id:       groupID,
				label:    "",
				shape:    "rectangle",
				children: children,
				columns:  childCols,
			})
			links = append(links, childLinks...)
			i = nextI
			continue
		}

		// Try to parse link
		if linkResult, srcToken, tgtToken, ok := tryParseBlockLink(line); ok {
			allBlocks := collectAllBlocks(blocks)
			for _, token := range []string{srcToken, tgtToken} {
				parsed := parseBlockToken(token)
				if parsed == nil {
					continue
				}
				if existing, exists := allBlocks[parsed.id]; exists {
					if parsed.shape != "rectangle" || parsed.label != parsed.id {
						existing.shape = parsed.shape
						existing.label = parsed.label
					}
				} else {
					blocks = append(blocks, *parsed)
					allBlocks[parsed.id] = &blocks[len(blocks)-1]
				}
			}
			links = append(links, linkResult)
			i++
			continue
		}

		// Parse space-separated block tokens on one line
		tokens := tokenizeBlockLine(line)
		for _, token := range tokens {
			block := parseBlockToken(token)
			if block != nil {
				blocks = append(blocks, *block)
			}
		}
		i++
	}

	return blocks, links, columns, i
}

func collectAllBlocks(blocks []blockNode) map[string]*blockNode {
	result := make(map[string]*blockNode)
	for idx := range blocks {
		result[blocks[idx].id] = &blocks[idx]
		if len(blocks[idx].children) > 0 {
			for k, v := range collectAllBlocks(blocks[idx].children) {
				result[k] = v
			}
		}
	}
	return result
}

func tryParseBlockLink(line string) (blockLink, string, string, bool) {
	if m := reLinkLabel.FindStringSubmatch(line); m != nil {
		srcToken, tgtToken := m[1], m[3]
		srcID := extractBlockID(srcToken)
		tgtID := extractBlockID(tgtToken)
		return blockLink{source: srcID, target: tgtID, label: m[2]}, srcToken, tgtToken, true
	}
	if m := reLinkSimple.FindStringSubmatch(line); m != nil {
		srcToken, tgtToken := m[1], m[2]
		srcID := extractBlockID(srcToken)
		tgtID := extractBlockID(tgtToken)
		return blockLink{source: srcID, target: tgtID, label: ""}, srcToken, tgtToken, true
	}
	return blockLink{}, "", "", false
}

func extractBlockID(token string) string {
	for _, sp := range shapePatterns {
		idx := strings.Index(token, sp.open)
		if idx > 0 {
			return strings.TrimSpace(token[:idx])
		}
	}
	return strings.TrimSpace(token)
}

func tokenizeBlockLine(line string) []string {
	var tokens []string
	var current []rune
	depth := 0

	for _, ch := range line {
		switch {
		case ch == '(' || ch == '[' || ch == '{':
			depth++
			current = append(current, ch)
		case ch == ')' || ch == ']' || ch == '}':
			if depth > 0 {
				depth--
			}
			current = append(current, ch)
		case ch == '<':
			depth++
			current = append(current, ch)
		case ch == '>' && depth > 0:
			depth--
			current = append(current, ch)
		case ch == ' ' && depth == 0:
			tok := strings.TrimSpace(string(current))
			if tok != "" {
				tokens = append(tokens, tok)
			}
			current = nil
		default:
			current = append(current, ch)
		}
	}
	tok := strings.TrimSpace(string(current))
	if tok != "" {
		tokens = append(tokens, tok)
	}
	return tokens
}

func parseBlockToken(token string) *blockNode {
	if token == "" {
		return nil
	}

	// Space block
	if m := reSpaceBlock.FindStringSubmatch(token); m != nil {
		span := 1
		if m[1] != "" {
			span, _ = strconv.Atoi(m[1])
		}
		return &blockNode{id: fmt.Sprintf("_space_%d", blockAnonCounter), isSpace: true, colSpan: span}
	}

	// blockArrow syntax
	if m := reBlockArrow.FindStringSubmatch(token); m != nil {
		label := unescapeBlockHTML(m[2])
		if strings.TrimSpace(label) == "" {
			return &blockNode{id: m[1], isSpace: true, colSpan: 1}
		}
		return &blockNode{id: m[1], label: label, shape: "rectangle", colSpan: 1}
	}

	// Check for col_span suffix
	colSpan := 1
	if m := reColSpan.FindStringSubmatchIndex(token); m != nil {
		spanStr := token[m[2]:m[3]]
		colSpan, _ = strconv.Atoi(spanStr)
		token = token[:m[0]]
	}

	// Try shape patterns
	for _, sp := range shapePatterns {
		idx := strings.Index(token, sp.open)
		if idx > 0 {
			rest := token[idx+len(sp.open):]
			if strings.HasSuffix(rest, sp.close) {
				blockID := strings.TrimSpace(token[:idx])
				label := strings.TrimSpace(rest[:len(rest)-len(sp.close)])
				label = stripBlockQuotes(label)
				if blockID != "" {
					return &blockNode{id: blockID, label: label, shape: sp.shape, colSpan: colSpan}
				}
			}
		}
	}

	// Bare ID
	blockID := strings.TrimSpace(token)
	if blockID != "" && reBareID.MatchString(blockID) {
		return &blockNode{id: blockID, label: blockID, shape: "rectangle", colSpan: colSpan}
	}

	return nil
}

func stripBlockQuotes(text string) string {
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		return text[1 : len(text)-1]
	}
	return text
}

func unescapeBlockHTML(text string) string {
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	return text
}

// ── renderer ─────────────────────────────────────────────────────

// blockGridEntry is a block placed in a grid cell.
type blockGridEntry struct {
	block    *blockNode
	startCol int
	span     int
}

func renderBlockDiagram(bd *blockDiagram, useASCII bool) *renderer.Canvas {
	cs := renderer.UNICODE
	if useASCII {
		cs = renderer.ASCII
	}

	if len(bd.blocks) == 0 {
		return renderer.NewCanvas(1, 1)
	}

	columns := bd.columns
	if columns <= 0 {
		columns = 0
		for _, b := range bd.blocks {
			columns += b.colSpan
		}
	}

	// Layout all blocks into a grid
	grid, colCount := blockLayoutGrid(bd.blocks, columns)

	// Compute block sizes
	blockSizes := make(map[string][2]int) // id -> {w, h}
	blockComputeAllSizes(bd.blocks, blockSizes, cs)

	// Compute column gap scaled to available width
	contentW := 0
	for _, sz := range blockSizes {
		contentW += sz[0]
	}
	colGap := blockColumnGap(colCount, contentW)

	// Compute column widths and row heights
	colWidths, rowHeights := blockComputeGridDimensions(grid, colCount, blockSizes, colGap)

	// Compute block positions
	positions := make(map[string][2]int) // id -> {x, y}
	blockComputePositions(grid, colWidths, rowHeights, blockSizes, positions, colGap)

	// Canvas size
	totalW := blockMargin*2 + intSum(colWidths) + colGap*max(0, colCount-1)
	totalH := blockMargin*2 + intSum(rowHeights) + blockRowGap*max(0, len(grid)-1)
	totalW = max(totalW, 20)
	totalH = max(totalH, 5)

	c := renderer.NewCanvas(totalW, totalH)

	// Draw group borders (background)
	blockDrawGroups(c, bd.blocks, positions, blockSizes, cs)

	// Draw links (middle layer)
	for _, link := range bd.links {
		blockDrawLink(c, link, positions, blockSizes, cs, useASCII)
	}

	// Draw block shapes (foreground)
	blockDrawBlocks(c, bd.blocks, positions, blockSizes, cs)

	return c
}

func blockLayoutGrid(blocks []blockNode, columns int) ([][]blockGridEntry, int) {
	var grid [][]blockGridEntry
	var row []blockGridEntry
	col := 0

	for i := range blocks {
		span := blocks[i].colSpan
		if span > columns {
			span = columns
		}
		if col+span > columns {
			if len(row) > 0 {
				grid = append(grid, row)
			}
			row = nil
			col = 0
		}
		row = append(row, blockGridEntry{block: &blocks[i], startCol: col, span: span})
		col += span
	}
	if len(row) > 0 {
		grid = append(grid, row)
	}
	return grid, columns
}

func blockComputeBlockSize(block *blockNode, cs renderer.CharSet) (int, int) {
	if block.isSpace {
		return minBlockW, minBlockH
	}

	if len(block.children) > 0 {
		innerCols := block.columns
		if innerCols <= 0 {
			for _, c := range block.children {
				innerCols += c.colSpan
			}
		}
		innerGrid, innerColCount := blockLayoutGrid(block.children, innerCols)
		childSizes := make(map[string][2]int)
		blockComputeAllSizes(block.children, childSizes, cs)
		cww, rhh := blockComputeGridDimensions(innerGrid, innerColCount, childSizes, 4)
		innerW := intSum(cww) + 4*max(0, innerColCount-1)
		innerH := intSum(rhh) + blockRowGap*max(0, len(innerGrid)-1)
		w := innerW + groupPad*2 + 2
		labelRows := 0
		if block.label != "" {
			labelRows = 1
		}
		h := innerH + groupPad*2 + 2 + labelRows
		return max(w, minBlockW), max(h, minBlockH)
	}

	label := block.label
	if label == "" {
		label = block.id
	}
	w := max(len(label)+blockPad*2, minBlockW)
	h := minBlockH
	return w, h
}

func blockComputeAllSizes(blocks []blockNode, sizes map[string][2]int, cs renderer.CharSet) {
	for i := range blocks {
		w, h := blockComputeBlockSize(&blocks[i], cs)
		sizes[blocks[i].id] = [2]int{w, h}
		if len(blocks[i].children) > 0 {
			blockComputeAllSizes(blocks[i].children, sizes, cs)
		}
	}
}

func blockComputeGridDimensions(grid [][]blockGridEntry, colCount int, sizes map[string][2]int, colGap int) ([]int, []int) {
	colWidths := make([]int, colCount)
	for i := range colWidths {
		colWidths[i] = minBlockW
	}
	rowHeights := make([]int, len(grid))
	for i := range rowHeights {
		rowHeights[i] = minBlockH
	}

	// First pass: single-span blocks
	for ri, row := range grid {
		for _, entry := range row {
			sz := sizes[entry.block.id]
			w, h := sz[0], sz[1]
			if h > rowHeights[ri] {
				rowHeights[ri] = h
			}
			if entry.span == 1 && w > colWidths[entry.startCol] {
				colWidths[entry.startCol] = w
			}
		}
	}

	// Second pass: spanning blocks
	for _, row := range grid {
		for _, entry := range row {
			if entry.span <= 1 {
				continue
			}
			sz := sizes[entry.block.id]
			w := sz[0]
			available := 0
			endCol := entry.startCol + entry.span
			if endCol > colCount {
				endCol = colCount
			}
			for c := entry.startCol; c < endCol; c++ {
				available += colWidths[c]
			}
			available += colGap * (entry.span - 1)
			if w > available {
				extra := w - available
				perCol := extra / entry.span
				remainder := extra % entry.span
				for c := entry.startCol; c < endCol; c++ {
					colWidths[c] += perCol
					if c-entry.startCol < remainder {
						colWidths[c]++
					}
				}
			}
		}
	}

	return colWidths, rowHeights
}

func blockComputePositions(grid [][]blockGridEntry, colWidths, rowHeights []int, sizes map[string][2]int, positions map[string][2]int, colGap int) {
	// Precompute column x positions
	colX := make([]int, len(colWidths))
	x := blockMargin
	for c := range colWidths {
		colX[c] = x
		x += colWidths[c] + colGap
	}

	// Precompute row y positions
	rowY := make([]int, len(rowHeights))
	y := blockMargin
	for r := range rowHeights {
		rowY[r] = y
		y += rowHeights[r] + blockRowGap
	}

	for ri, row := range grid {
		if ri >= len(rowY) {
			break
		}
		for _, entry := range row {
			// A nested `block ... end` can put entries in the grid that the
			// column pass never sized, leaving colX shorter than the grid is
			// wide. Indexing blindly panics; skipping the entry drops one box
			// but still draws the rest of the diagram.
			if entry.startCol < 0 || entry.startCol >= len(colX) {
				continue
			}
			bx := colX[entry.startCol]
			by := rowY[ri]

			var bw int
			if entry.span > 1 {
				endCol := entry.startCol + entry.span - 1
				if endCol >= len(colWidths) {
					endCol = len(colWidths) - 1
				}
				bw = colX[endCol] + colWidths[endCol] - colX[entry.startCol]
			} else {
				bw = colWidths[entry.startCol]
			}
			bh := rowHeights[ri]
			sizes[entry.block.id] = [2]int{bw, bh}
			positions[entry.block.id] = [2]int{bx, by}

			if len(entry.block.children) > 0 {
				blockPositionChildren(entry.block, bx, by, bw, bh, sizes, positions)
			}
		}
	}
}

func blockPositionChildren(group *blockNode, gx, gy, gw, gh int, sizes map[string][2]int, positions map[string][2]int) {
	innerX := gx + groupPad + 1
	labelRows := 0
	if group.label != "" {
		labelRows = 1
	}
	innerY := gy + groupPad + 1 + labelRows

	innerCols := group.columns
	if innerCols <= 0 {
		for _, c := range group.children {
			innerCols += c.colSpan
		}
	}
	innerGrid, innerColCount := blockLayoutGrid(group.children, innerCols)
	cww, rhh := blockComputeGridDimensions(innerGrid, innerColCount, sizes, 4)

	childColX := make([]int, len(cww))
	cx := innerX
	for c := range cww {
		childColX[c] = cx
		cx += cww[c] + 4
	}

	childRowY := make([]int, len(rhh))
	cy := innerY
	for r := range rhh {
		childRowY[r] = cy
		cy += rhh[r] + blockRowGap
	}

	for ri, row := range innerGrid {
		for _, entry := range row {
			bx := childColX[entry.startCol]
			by := childRowY[ri]

			var bw int
			if entry.span > 1 && entry.startCol+entry.span-1 < len(cww) {
				endCol := entry.startCol + entry.span - 1
				bw = childColX[endCol] + cww[endCol] - childColX[entry.startCol]
			} else {
				bw = cww[entry.startCol]
			}
			bh := rhh[ri]
			sizes[entry.block.id] = [2]int{bw, bh}
			positions[entry.block.id] = [2]int{bx, by}
		}
	}
}

func blockDrawBlocks(c *renderer.Canvas, blocks []blockNode, positions map[string][2]int, sizes map[string][2]int, cs renderer.CharSet) {
	for i := range blocks {
		block := &blocks[i]
		if block.isSpace {
			continue
		}
		if _, ok := positions[block.id]; !ok {
			continue
		}
		if len(block.children) > 0 {
			blockDrawBlocks(c, block.children, positions, sizes, cs)
			continue
		}

		pos := positions[block.id]
		sz := sizes[block.id]
		x, y := pos[0], pos[1]
		w, h := sz[0], sz[1]

		// Ensure canvas is big enough
		if x+w+2 > c.Width || y+h+2 > c.Height {
			c.Resize(x+w+2, y+h+2)
		}

		ns, ok := shapeToNodeShape[block.shape]
		if !ok {
			ns = graph.ShapeRectangle
		}

		drawFn, ok := renderer.ShapeRenderers[ns]
		if !ok {
			drawFn = renderer.DrawRectangle
		}

		label := block.label
		if label == "" {
			label = block.id
		}
		drawFn(c, x, y, w, h, label, cs, "node")
	}
}

func blockDrawGroups(c *renderer.Canvas, blocks []blockNode, positions map[string][2]int, sizes map[string][2]int, cs renderer.CharSet) {
	for i := range blocks {
		block := &blocks[i]
		if len(block.children) == 0 {
			continue
		}
		if _, ok := positions[block.id]; !ok {
			continue
		}

		pos := positions[block.id]
		sz := sizes[block.id]
		x, y := pos[0], pos[1]
		w, h := sz[0], sz[1]

		// Ensure canvas is big enough
		if x+w+2 > c.Width || y+h+2 > c.Height {
			c.Resize(x+w+2, y+h+2)
		}

		style := "subgraph"

		// Draw border using subgraph chars
		c.Put(y, x, cs.SGTopLeft, true, style)
		for col := x + 1; col < x+w-1; col++ {
			c.Put(y, col, cs.SGHorizontal, true, style)
		}
		c.Put(y, x+w-1, cs.SGTopRight, true, style)

		c.Put(y+h-1, x, cs.SGBottomLeft, true, style)
		for col := x + 1; col < x+w-1; col++ {
			c.Put(y+h-1, col, cs.SGHorizontal, true, style)
		}
		c.Put(y+h-1, x+w-1, cs.SGBottomRight, true, style)

		for row := y + 1; row < y+h-1; row++ {
			c.Put(row, x, cs.SGVertical, true, style)
			c.Put(row, x+w-1, cs.SGVertical, true, style)
		}

		// Draw group label (skip for anonymous groups)
		if block.label != "" {
			labelCol := x + (w-len(block.label))/2
			c.PutText(y+1, labelCol, block.label, "label")
		}

		// Recurse for nested groups within children
		blockDrawGroups(c, block.children, positions, sizes, cs)
	}
}

func blockDrawLink(c *renderer.Canvas, link blockLink, positions map[string][2]int, sizes map[string][2]int, cs renderer.CharSet, useASCII bool) {
	srcPos, srcOK := positions[link.source]
	tgtPos, tgtOK := positions[link.target]
	if !srcOK || !tgtOK {
		return
	}

	srcSz := sizes[link.source]
	tgtSz := sizes[link.target]

	sx, sy := srcPos[0], srcPos[1]
	sw, sh := srcSz[0], srcSz[1]
	tx, ty := tgtPos[0], tgtPos[1]
	tw, th := tgtSz[0], tgtSz[1]

	sCX := sx + sw/2
	tCX := tx + tw/2

	hChar := cs.LineHorizontal
	vChar := cs.LineVertical
	style := "edge"

	// Determine connection type based on overlap
	sRight, tRight := sx+sw, tx+tw
	hOverlap := min(sRight, tRight) - max(sx, tx)
	sBottom, tBottom := sy+sh, ty+th
	vOverlap := min(sBottom, tBottom) - max(sy, ty)

	useVertical := hOverlap > 0 || (vOverlap <= 0 && absInt(tCX-sCX) <= absInt((ty+th/2)-(sy+sh/2)))

	var r1, c1, r2, c2 int

	if !useVertical {
		// Horizontal: exit/enter from sides
		dx := tCX - sCX
		var arrow rune
		if dx > 0 {
			r1, c1 = sy+sh/2, sx+sw
			r2, c2 = ty+th/2, tx-1
			arrow = cs.ArrowRight
		} else {
			r1, c1 = sy+sh/2, sx-1
			r2, c2 = ty+th/2, tx+tw
			arrow = cs.ArrowLeft
		}

		blockDrawRoutedLine(c, r1, c1, r2, c2, hChar, vChar, useASCII, style)
		c.Put(r2, c2, arrow, false, style)
	} else {
		// Vertical: exit/enter from top/bottom
		dy := (ty + th/2) - (sy + sh/2)
		exitCol := sCX
		enterCol := max(tx, min(tCX, sRight-1))
		if hOverlap > 0 {
			enterCol = exitCol
		}

		var arrow rune
		if dy > 0 {
			r1, c1 = sy+sh, exitCol
			r2, c2 = ty-1, enterCol
		} else {
			r1, c1 = sy-1, exitCol
			r2, c2 = ty+th, enterCol
		}

		if c1 == c2 {
			// Straight vertical
			rMin, rMax := min(r1, r2), max(r1, r2)
			for r := rMin; r <= rMax; r++ {
				c.Put(r, c1, vChar, true, style)
			}
		} else {
			// L-route: vertical to bend row, then horizontal to target x
			bendRow := r2
			rMin, rMax := min(r1, bendRow), max(r1, bendRow)
			for r := rMin; r <= rMax; r++ {
				c.Put(r, c1, vChar, true, style)
			}
			cMin, cMax := min(c1, c2), max(c1, c2)
			for col := cMin; col <= cMax; col++ {
				c.Put(bendRow, col, hChar, true, style)
			}
			if !useASCII {
				var corner rune
				if r1 < bendRow {
					if c2 < c1 {
						corner = '┘'
					} else {
						corner = '└'
					}
				} else {
					if c2 < c1 {
						corner = '┐'
					} else {
						corner = '┌'
					}
				}
				c.Put(bendRow, c1, corner, false, style)
			}
		}

		if dy > 0 {
			arrow = cs.ArrowDown
		} else {
			arrow = cs.ArrowUp
		}
		c.Put(r2, c2, arrow, false, style)
	}

	// Draw label
	if link.label != "" {
		midR := (r1 + r2) / 2
		midC := (c1 + c2) / 2
		labelCol := midC - len(link.label)/2
		c.PutText(midR, labelCol, link.label, "edge_label")
	}
}

// blockDrawRoutedLine draws a Z-shaped or straight line using block routing (different from classdiagram's drawRoutedLine).
func blockDrawRoutedLine(c *renderer.Canvas, r1, c1, r2, c2 int, hChar, vChar rune, useASCII bool, style string) {
	if c1 == c2 {
		rMin, rMax := min(r1, r2), max(r1, r2)
		for r := rMin; r <= rMax; r++ {
			c.Put(r, c1, vChar, true, style)
		}
	} else if r1 == r2 {
		cMin, cMax := min(c1, c2), max(c1, c2)
		for col := cMin; col <= cMax; col++ {
			c.Put(r1, col, hChar, true, style)
		}
	} else {
		midRow := (r1 + r2) / 2
		rMin, rMax := min(r1, midRow), max(r1, midRow)
		for r := rMin; r <= rMax; r++ {
			c.Put(r, c1, vChar, true, style)
		}
		cMin, cMax := min(c1, c2), max(c1, c2)
		for col := cMin; col <= cMax; col++ {
			c.Put(midRow, col, hChar, true, style)
		}
		rMin2, rMax2 := min(midRow, r2), max(midRow, r2)
		for r := rMin2; r <= rMax2; r++ {
			c.Put(r, c2, vChar, true, style)
		}
		if !useASCII {
			var corner1 rune
			if r1 < midRow {
				if c2 < c1 {
					corner1 = '┘'
				} else {
					corner1 = '└'
				}
			} else {
				if c2 < c1 {
					corner1 = '┐'
				} else {
					corner1 = '┌'
				}
			}
			c.Put(midRow, c1, corner1, false, style)

			var corner2 rune
			if r2 > midRow {
				if c2 < c1 {
					corner2 = '┌'
				} else {
					corner2 = '┐'
				}
			} else {
				if c2 < c1 {
					corner2 = '└'
				} else {
					corner2 = '┘'
				}
			}
			c.Put(midRow, c2, corner2, false, style)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────

func intSum(s []int) int {
	total := 0
	for _, v := range s {
		total += v
	}
	return total
}
