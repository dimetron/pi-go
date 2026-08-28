package diagram

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// member represents a class member (field or method).
type member struct {
	name       string
	visibility string // "+", "-", "#", "~"
	returnType string
	isMethod   bool
	classifier string // "$" for static, "*" for abstract
}

// classDef represents a class definition.
type classDef struct {
	name       string
	annotation string // e.g. "interface", "abstract", "enumeration"
	members    []member
}

// classRelationship represents a relationship between two classes.
type classRelationship struct {
	source       string
	target       string
	sourceMarker string // "<|", "<", "*", "o", ""
	targetMarker string // "|>", ">", "*", "o", ""
	lineStyle    string // "--", ".."
	label        string
	sourceCard   string // cardinality
	targetCard   string
}

// classNote represents a note attached to a class.
type classNote struct {
	text   string
	target string
}

// classDiagram represents a parsed class diagram.
type classDiagram struct {
	classes       map[string]*classDef
	classOrder    []string
	relationships []classRelationship
	notes         []classNote
	direction     string // "TB" or "LR"
}

// Regex patterns for class diagram parsing.
var (
	reClassDiagramHeader  = regexp.MustCompile(`(?i)^\s*classDiagram\s*$`)
	reClassDirection      = regexp.MustCompile(`(?i)^\s*direction\s+(LR|RL|TB|BT|TD)\s*$`)
	reClassDeclaration    = regexp.MustCompile(`^\s*class\s+(\w+)\s*$`)
	reClassWithBody       = regexp.MustCompile(`^\s*class\s+(\w+)\s*\{\s*$`)
	reClassBodyClose      = regexp.MustCompile(`^\s*\}\s*$`)
	reClassAnnotation     = regexp.MustCompile(`^\s*<<(\w+)>>\s*$`)
	reClassColonMember    = regexp.MustCompile(`^\s*(\w+)\s*:\s*(.+)$`)
	reClassAnnotationLine = regexp.MustCompile(`^\s*<<(\w+)>>\s+(\w+)\s*$`)
	reClassNote           = regexp.MustCompile(`(?i)^\s*note\s+(?:for\s+)?(\w+)\s*:\s*(.*)$`)
	// Relationship regex: Class1 "card1" markers--markers "card2" Class2 : label
	reClassRelationship = regexp.MustCompile(
		`^\s*(\w+)\s*` + // source
			`(?:"([^"]*)")?\s*` + // optional source cardinality
			`([<*o]?\|?|(?:<\|)?)` + // source marker
			`(--|\.\.|\-\-)` + // line style
			`(\|?[>*o]?|(?:\|>)?)` + // target marker
			`\s*(?:"([^"]*)")?\s*` + // optional target cardinality
			`(\w+)` + // target
			`(?:\s*:\s*(.*))?$`, // optional label
	)
)

// parseClassDiagram parses class diagram source into a classDiagram model.
func parseClassDiagram(source string) *classDiagram {
	cd := &classDiagram{
		classes:   make(map[string]*classDef),
		direction: "TB",
	}

	lines := strings.Split(source, "\n")
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])

		// Skip empty lines, comments, header
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") || reClassDiagramHeader.MatchString(trimmed) {
			i++
			continue
		}

		// Direction
		if m := reClassDirection.FindStringSubmatch(trimmed); m != nil {
			dir := strings.ToUpper(m[1])
			if dir == "TD" {
				dir = "TB"
			}
			cd.direction = dir
			i++
			continue
		}

		// Annotation line: <<interface>> ClassName
		if m := reClassAnnotationLine.FindStringSubmatch(trimmed); m != nil {
			annotation := m[1]
			className := m[2]
			cls := cd.ensureClass(className)
			cls.annotation = annotation
			i++
			continue
		}

		// Note
		if m := reClassNote.FindStringSubmatch(trimmed); m != nil {
			cd.notes = append(cd.notes, classNote{
				target: m[1],
				text:   strings.TrimSpace(m[2]),
			})
			i++
			continue
		}

		// Class with body: class Foo {
		if m := reClassWithBody.FindStringSubmatch(trimmed); m != nil {
			className := m[1]
			cls := cd.ensureClass(className)
			i++
			// Read members until closing brace
			for i < len(lines) {
				memberLine := strings.TrimSpace(lines[i])
				if reClassBodyClose.MatchString(memberLine) {
					i++
					break
				}
				if memberLine == "" {
					i++
					continue
				}
				// Check for annotation inside body
				if am := reClassAnnotation.FindStringSubmatch(memberLine); am != nil {
					cls.annotation = am[1]
					i++
					continue
				}
				// Parse member
				mem := parseClassMember(memberLine)
				if mem != nil {
					cls.members = append(cls.members, *mem)
				}
				i++
			}
			continue
		}

		// Simple class declaration: class Foo
		if m := reClassDeclaration.FindStringSubmatch(trimmed); m != nil {
			cd.ensureClass(m[1])
			i++
			continue
		}

		// Colon member syntax: ClassName : +method()
		if m := reClassColonMember.FindStringSubmatch(trimmed); m != nil {
			className := m[1]
			memberText := strings.TrimSpace(m[2])
			// Check if this is actually a relationship (contains -- or ..)
			if !strings.Contains(memberText, "--") && !strings.Contains(memberText, "..") {
				cls := cd.ensureClass(className)
				mem := parseClassMember(memberText)
				if mem != nil {
					cls.members = append(cls.members, *mem)
				}
				i++
				continue
			}
		}

		// Relationship
		if m := reClassRelationship.FindStringSubmatch(trimmed); m != nil {
			rel := classRelationship{
				source:       m[1],
				sourceCard:   m[2],
				sourceMarker: m[3],
				lineStyle:    m[4],
				targetMarker: m[5],
				targetCard:   m[6],
				target:       m[7],
			}
			if len(m) > 8 {
				rel.label = strings.TrimSpace(m[8])
			}
			cd.ensureClass(rel.source)
			cd.ensureClass(rel.target)
			cd.relationships = append(cd.relationships, rel)
			i++
			continue
		}

		i++
	}

	return cd
}

// ensureClass creates a class entry if it doesn't exist and returns it.
func (cd *classDiagram) ensureClass(name string) *classDef {
	if cls, ok := cd.classes[name]; ok {
		return cls
	}
	cls := &classDef{name: name}
	cd.classes[name] = cls
	cd.classOrder = append(cd.classOrder, name)
	return cls
}

// parseClassMember parses a single class member line.
func parseClassMember(text string) *member {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	m := &member{}

	// Extract visibility
	if len(text) > 0 {
		switch text[0] {
		case '+', '-', '#', '~':
			m.visibility = string(text[0])
			text = text[1:]
		}
	}

	// Check for classifier at end
	text = strings.TrimSpace(text)
	if strings.HasSuffix(text, "$") {
		m.classifier = "$"
		text = text[:len(text)-1]
	} else if strings.HasSuffix(text, "*") {
		m.classifier = "*"
		text = text[:len(text)-1]
	}

	// Check for return type after last colon (not inside parens)
	if idx := strings.LastIndex(text, ":"); idx >= 0 {
		beforeColon := text[:idx]
		afterColon := strings.TrimSpace(text[idx+1:])
		// Only treat as return type if it doesn't contain parens
		if !strings.Contains(afterColon, "(") && !strings.Contains(afterColon, ")") {
			m.returnType = afterColon
			text = strings.TrimSpace(beforeColon)
		}
	}

	// Check for method (contains parentheses)
	if strings.Contains(text, "(") {
		m.isMethod = true
	}

	m.name = strings.TrimSpace(text)
	return m
}

// classBoxInfo holds computed layout info for a class box.
type classBoxInfo struct {
	name   string
	x, y   int
	width  int
	height int
	layer  int
	col    int
}

// RenderClassDiagram parses and renders a Mermaid class diagram.
func RenderClassDiagram(source string, useASCII bool) *renderer.Canvas {
	cd := parseClassDiagram(source)
	if len(cd.classOrder) == 0 {
		c := renderer.NewCanvas(35, 1)
		c.PutText(0, 0, "[class] no classes defined", "default")
		return c
	}

	cs := renderer.UNICODE
	if useASCII {
		cs = renderer.ASCII
	}

	isLR := cd.direction == "LR"

	// BFS layer assignment
	layers := classAssignLayers(cd)

	// Compute box sizes
	boxes := make(map[string]*classBoxInfo)
	for _, name := range cd.classOrder {
		cls := cd.classes[name]
		box := computeClassBox(cls)
		box.layer = layers[name]
		boxes[name] = box
	}

	// Group boxes by layer
	maxLayer := 0
	for _, box := range boxes {
		if box.layer > maxLayer {
			maxLayer = box.layer
		}
	}
	layerGroups := make([][]string, maxLayer+1)
	for _, name := range cd.classOrder {
		l := boxes[name].layer
		layerGroups[l] = append(layerGroups[l], name)
	}

	// Compute natural width to inform gap scaling
	naturalW := 0
	for _, group := range layerGroups {
		layerW := 0
		for _, name := range group {
			layerW += boxes[name].width
		}
		if layerW > naturalW {
			naturalW = layerW
		}
	}

	// Position boxes
	var canvasWidth, canvasHeight int
	if isLR {
		canvasWidth, canvasHeight = positionClassBoxesLR(layerGroups, boxes, naturalW)
	} else {
		canvasWidth, canvasHeight = positionClassBoxesTB(layerGroups, boxes, naturalW)
	}

	// Create canvas with margin
	c := renderer.NewCanvas(canvasWidth+4, canvasHeight+4)

	// Draw class boxes
	for _, name := range cd.classOrder {
		drawClassBox(c, cd.classes[name], boxes[name], cs)
	}

	// Draw relationships
	for _, rel := range cd.relationships {
		srcBox := boxes[rel.source]
		tgtBox := boxes[rel.target]
		if srcBox == nil || tgtBox == nil {
			continue
		}
		drawClassRelationship(c, rel, srcBox, tgtBox, cs, useASCII, isLR)
	}

	// Draw notes
	for _, note := range cd.notes {
		box := boxes[note.target]
		if box == nil {
			continue
		}
		noteWidth := len(note.text) + 4
		noteHeight := 3
		noteX := box.x + box.width + 2
		noteY := box.y
		if noteX+noteWidth+2 > c.Width || noteY+noteHeight+2 > c.Height {
			c.Resize(noteX+noteWidth+2, noteY+noteHeight+2)
		}
		renderer.DrawRectangle(c, noteX, noteY, noteWidth, noteHeight, note.text, cs, "node")
	}

	return c
}

// classAssignLayers assigns BFS layers to classes based on relationships.
func classAssignLayers(cd *classDiagram) map[string]int {
	layers := make(map[string]int)
	// Build adjacency: relationship source -> targets
	adj := make(map[string][]string)
	incoming := make(map[string]int)
	for _, name := range cd.classOrder {
		incoming[name] = 0
	}
	for _, rel := range cd.relationships {
		adj[rel.source] = append(adj[rel.source], rel.target)
		incoming[rel.target]++
	}

	// Find roots (no incoming edges)
	var queue []string
	for _, name := range cd.classOrder {
		if incoming[name] == 0 {
			queue = append(queue, name)
			layers[name] = 0
		}
	}
	if len(queue) == 0 && len(cd.classOrder) > 0 {
		queue = append(queue, cd.classOrder[0])
		layers[cd.classOrder[0]] = 0
	}

	// BFS
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		currLayer := layers[curr]
		for _, next := range adj[curr] {
			if _, ok := layers[next]; !ok {
				layers[next] = currLayer + 1
				queue = append(queue, next)
			}
		}
	}

	// Assign unvisited nodes to layer 0
	for _, name := range cd.classOrder {
		if _, ok := layers[name]; !ok {
			layers[name] = 0
		}
	}

	return layers
}

// computeClassBox computes the size of a class box.
func computeClassBox(cls *classDef) *classBoxInfo {
	box := &classBoxInfo{name: cls.name}

	// Minimum width from class name
	minWidth := len(cls.name) + 4

	// Account for annotation
	if cls.annotation != "" {
		annotationWidth := len(cls.annotation) + 6 // <<annotation>>
		if annotationWidth > minWidth {
			minWidth = annotationWidth
		}
	}

	// Account for members
	for _, mem := range cls.members {
		memberStr := formatClassMember(mem)
		memberWidth := len(memberStr) + 4
		if memberWidth > minWidth {
			minWidth = memberWidth
		}
	}

	box.width = minWidth

	// Height: border(1) + annotation(0-1) + name(1) + divider(0-1) + members + border(1)
	box.height = 2 // top and bottom border
	if cls.annotation != "" {
		box.height++ // annotation line
	}
	box.height++ // class name
	if len(cls.members) > 0 {
		box.height++                   // divider line
		box.height += len(cls.members) // member lines
	}

	return box
}

// formatClassMember formats a member for display.
func formatClassMember(m member) string {
	var b strings.Builder
	b.WriteString(m.visibility)
	b.WriteString(m.name)
	if m.classifier != "" {
		b.WriteString(m.classifier)
	}
	if m.returnType != "" {
		b.WriteString(" : ")
		b.WriteString(m.returnType)
	}
	return b.String()
}

// positionClassBoxesTB positions boxes in top-to-bottom layout.
func positionClassBoxesTB(layerGroups [][]string, boxes map[string]*classBoxInfo, naturalW int) (int, int) {
	nGaps := 0
	for _, g := range layerGroups {
		if len(g)-1 > nGaps {
			nGaps = len(g) - 1
		}
	}
	hGap := scaleGap(6, max(1, nGaps), naturalW, 4, 12)
	vGap := 4

	y := 1
	maxWidth := 0

	for _, group := range layerGroups {
		if len(group) == 0 {
			continue
		}

		// Calculate total width for this layer
		totalWidth := 0
		maxHeight := 0
		for _, name := range group {
			box := boxes[name]
			totalWidth += box.width
			if box.height > maxHeight {
				maxHeight = box.height
			}
		}
		totalWidth += (len(group) - 1) * hGap

		if totalWidth > maxWidth {
			maxWidth = totalWidth
		}

		// Position boxes centered
		x := 1
		for colIdx, name := range group {
			box := boxes[name]
			box.x = x
			box.y = y
			box.col = colIdx
			x += box.width + hGap
		}

		y += maxHeight + vGap
	}

	return maxWidth + 2, y
}

// positionClassBoxesLR positions boxes in left-to-right layout.
func positionClassBoxesLR(layerGroups [][]string, boxes map[string]*classBoxInfo, naturalW int) (int, int) {
	hGap := scaleGap(8, max(1, len(layerGroups)-1), naturalW, 4, 16)
	vGap := 3

	x := 1
	maxHeight := 0

	for _, group := range layerGroups {
		if len(group) == 0 {
			continue
		}

		maxWidth := 0
		totalHeight := 0
		for _, name := range group {
			box := boxes[name]
			totalHeight += box.height
			if box.width > maxWidth {
				maxWidth = box.width
			}
		}
		totalHeight += (len(group) - 1) * vGap

		if totalHeight > maxHeight {
			maxHeight = totalHeight
		}

		// Position boxes vertically stacked
		y := 1
		for colIdx, name := range group {
			box := boxes[name]
			box.x = x
			box.y = y
			box.col = colIdx
			y += box.height + vGap
		}

		x += maxWidth + hGap
	}

	return x, maxHeight + 2
}

// drawClassBox draws a class box on the canvas.
func drawClassBox(c *renderer.Canvas, cls *classDef, box *classBoxInfo, cs renderer.CharSet) {
	x, y, w, h := box.x, box.y, box.width, box.height

	// Ensure canvas is big enough
	if x+w+2 > c.Width || y+h+2 > c.Height {
		c.Resize(x+w+2, y+h+2)
	}

	// Draw border
	renderer.DrawRectangle(c, x, y, w, h, "", cs, "node")

	// Fill content
	row := y + 1

	// Annotation
	if cls.annotation != "" {
		ann := fmt.Sprintf("<<%s>>", cls.annotation)
		col := x + (w-len(ann))/2
		c.PutText(row, col, ann, "node")
		row++
	}

	// Class name (centered)
	nameCol := x + (w-len(cls.name))/2
	c.PutText(row, nameCol, cls.name, "node")
	row++

	// Divider and members
	if len(cls.members) > 0 {
		// Draw divider
		for col := x + 1; col < x+w-1; col++ {
			c.Put(row, col, cs.Horizontal, true, "node")
		}
		// Connect divider to sides
		c.Put(row, x, cs.TeeRight, true, "node")
		c.Put(row, x+w-1, cs.TeeLeft, true, "node")
		row++

		// Members
		for _, mem := range cls.members {
			memberStr := formatClassMember(mem)
			c.PutText(row, x+2, memberStr, "node")
			row++
		}
	}
}

// drawClassRelationship draws a relationship line between two class boxes.
func drawClassRelationship(c *renderer.Canvas, rel classRelationship, src, tgt *classBoxInfo, cs renderer.CharSet, useASCII bool, isLR bool) {
	// Determine line character
	lineH := cs.LineHorizontal
	lineV := cs.LineVertical
	if rel.lineStyle == ".." {
		lineH = cs.LineDottedH
		lineV = cs.LineDottedV
	}

	// Compute connection points
	srcCX := src.x + src.width/2
	srcCY := src.y + src.height/2
	tgtCX := tgt.x + tgt.width/2
	tgtCY := tgt.y + tgt.height/2

	var srcX, srcY, tgtX, tgtY int

	if isLR {
		// Connect horizontally
		if src.x < tgt.x {
			srcX = src.x + src.width
			srcY = srcCY
			tgtX = tgt.x
			tgtY = tgtCY
		} else {
			srcX = src.x
			srcY = srcCY
			tgtX = tgt.x + tgt.width
			tgtY = tgtCY
		}
	} else {
		// Connect vertically
		if src.y < tgt.y {
			srcX = srcCX
			srcY = src.y + src.height
			tgtX = tgtCX
			tgtY = tgt.y
		} else {
			srcX = srcCX
			srcY = src.y
			tgtX = tgtCX
			tgtY = tgt.y + tgt.height
		}
	}

	// Draw the routed line
	drawRoutedLine(c, srcX, srcY, tgtX, tgtY, lineH, lineV, cs)

	// Draw markers
	drawClassMarker(c, tgtX, tgtY, srcX, srcY, rel.targetMarker, cs, useASCII)
	drawClassMarker(c, srcX, srcY, tgtX, tgtY, rel.sourceMarker, cs, useASCII)

	// Draw label at midpoint
	if rel.label != "" {
		midX := (srcX + tgtX) / 2
		midY := (srcY + tgtY) / 2
		labelCol := midX - len(rel.label)/2
		labelRow := midY - 1
		if labelRow < 0 {
			labelRow = midY + 1
		}
		if labelCol+len(rel.label)+1 > c.Width || labelRow+1 > c.Height {
			c.Resize(labelCol+len(rel.label)+2, labelRow+2)
		}
		c.PutText(labelRow, labelCol, rel.label, "edge_label")
	}

	// Draw cardinalities
	if rel.sourceCard != "" {
		cardCol := srcX + 1
		cardRow := srcY - 1
		if cardRow < 0 {
			cardRow = srcY + 1
		}
		if cardCol+len(rel.sourceCard)+1 > c.Width || cardRow+1 > c.Height {
			c.Resize(cardCol+len(rel.sourceCard)+2, cardRow+2)
		}
		c.PutText(cardRow, cardCol, rel.sourceCard, "edge_label")
	}
	if rel.targetCard != "" {
		cardCol := tgtX + 1
		cardRow := tgtY - 1
		if cardRow < 0 {
			cardRow = tgtY + 1
		}
		if cardCol+len(rel.targetCard)+1 > c.Width || cardRow+1 > c.Height {
			c.Resize(cardCol+len(rel.targetCard)+2, cardRow+2)
		}
		c.PutText(cardRow, cardCol, rel.targetCard, "edge_label")
	}
}

// drawRoutedLine draws a Z-shaped or straight line between two points.
func drawRoutedLine(c *renderer.Canvas, x1, y1, x2, y2 int, lineH, lineV rune, cs renderer.CharSet) {
	// Ensure canvas is big enough
	maxX := x1
	if x2 > maxX {
		maxX = x2
	}
	maxY := y1
	if y2 > maxY {
		maxY = y2
	}
	if maxX+2 > c.Width || maxY+2 > c.Height {
		c.Resize(maxX+2, maxY+2)
	}

	if x1 == x2 {
		// Straight vertical
		c.DrawVertical(x1, y1, y2, lineV, "edge")
	} else if y1 == y2 {
		// Straight horizontal
		c.DrawHorizontal(y1, x1, x2, lineH, "edge")
	} else {
		// Z-shaped routing: vertical to midpoint, horizontal bend, vertical to end
		midY := (y1 + y2) / 2

		// Vertical from source to midY
		c.DrawVertical(x1, y1, midY, lineV, "edge")

		// Horizontal from x1 to x2 at midY
		c.DrawHorizontal(midY, x1, x2, lineH, "edge")

		// Vertical from midY to target
		c.DrawVertical(x2, midY, y2, lineV, "edge")

		// Corners
		if y1 < midY {
			if x1 < x2 {
				c.Put(midY, x1, cs.CornerBottomLeft, true, "edge")
				c.Put(midY, x2, cs.CornerTopRight, true, "edge")
			} else {
				c.Put(midY, x1, cs.CornerBottomRight, true, "edge")
				c.Put(midY, x2, cs.CornerTopLeft, true, "edge")
			}
		} else {
			if x1 < x2 {
				c.Put(midY, x1, cs.CornerTopLeft, true, "edge")
				c.Put(midY, x2, cs.CornerBottomRight, true, "edge")
			} else {
				c.Put(midY, x1, cs.CornerTopRight, true, "edge")
				c.Put(midY, x2, cs.CornerBottomLeft, true, "edge")
			}
		}
	}
}

// drawClassMarker draws a relationship marker (arrow, diamond, etc.) at a connection point.
func drawClassMarker(c *renderer.Canvas, atX, atY, fromX, fromY int, marker string, cs renderer.CharSet, useASCII bool) {
	if marker == "" {
		return
	}

	// Determine direction from connection toward the other end
	dx := 0
	dy := 0
	if fromX > atX {
		dx = 1
	} else if fromX < atX {
		dx = -1
	}
	if fromY > atY {
		dy = 1
	} else if fromY < atY {
		dy = -1
	}

	var ch rune
	switch marker {
	case "|>":
		// Inheritance arrow pointing toward target
		if useASCII {
			ch = '>'
		} else {
			if dx > 0 {
				ch = '\u25B7' // ▷
			} else if dx < 0 {
				ch = '\u25C1' // ◁
			} else if dy > 0 {
				ch = '\u25BD' // ▽
			} else {
				ch = '\u25B3' // △
			}
		}
	case "<|":
		// Inheritance arrow pointing toward source
		if useASCII {
			ch = '<'
		} else {
			if dx > 0 {
				ch = '\u25B7' // ▷
			} else if dx < 0 {
				ch = '\u25C1' // ◁
			} else if dy > 0 {
				ch = '\u25BD' // ▽
			} else {
				ch = '\u25B3' // △
			}
		}
	case ">":
		if dx > 0 {
			ch = cs.ArrowRight
		} else if dx < 0 {
			ch = cs.ArrowLeft
		} else if dy > 0 {
			ch = cs.ArrowDown
		} else {
			ch = cs.ArrowUp
		}
	case "<":
		if dx > 0 {
			ch = cs.ArrowRight
		} else if dx < 0 {
			ch = cs.ArrowLeft
		} else if dy > 0 {
			ch = cs.ArrowDown
		} else {
			ch = cs.ArrowUp
		}
	case "*":
		// Composition: filled diamond
		if useASCII {
			ch = '#'
		} else {
			ch = '\u25C6' // ◆
		}
	case "o":
		// Aggregation: open diamond
		if useASCII {
			ch = 'o'
		} else {
			ch = '\u25C7' // ◇
		}
	default:
		return
	}

	// Place marker one step inside from the connection point (toward from)
	markerX := atX + dx
	markerY := atY + dy
	if markerX >= 0 && markerX < c.Width && markerY >= 0 && markerY < c.Height {
		c.Put(markerY, markerX, ch, false, "edge")
	}
}
