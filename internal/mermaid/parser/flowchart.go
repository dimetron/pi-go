// Package parser implements Mermaid diagram parsers.
//
// This file implements a recursive descent parser for Mermaid flowchart syntax.
// It supports: graph/flowchart directives, all directions (TB/TD/LR/BT/RL),
// node shapes ([], (), {}, ([]), [[]], (()), ((())) etc.), edge types
// (-->, -.->, ==>, <-->, ---), edge labels, subgraphs, classDef, comments,
// chained arrows, & operator.
package parser

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

// segment is a piece of a parsed line: either a node group or an arrow.
type segment struct {
	text           string
	isArrow        bool
	edgeStyle      graph.EdgeStyle
	hasArrowStart  bool
	hasArrowEnd    bool
	arrowTypeStart graph.ArrowType
	arrowTypeEnd   graph.ArrowType
	label          string
	minLength      int
}

// newSegment returns a segment with sensible defaults (non-arrow, hasArrowEnd true).
func newSegment(text string) segment {
	return segment{
		text:           text,
		edgeStyle:      graph.EdgeSolid,
		hasArrowEnd:    true,
		arrowTypeStart: graph.ArrowTypeArrow,
		arrowTypeEnd:   graph.ArrowTypeArrow,
		minLength:      1,
	}
}

// shapePattern maps opening/closing delimiters to a node shape.
type shapePattern struct {
	open  string
	close string
	shape graph.NodeShape
}

// Shape patterns ordered by specificity (most specific first).
var shapePatterns = []shapePattern{
	{"(((", ")))", graph.ShapeDoubleCircle},
	{"((", "))", graph.ShapeCircle},
	{"([", "])", graph.ShapeStadium},
	{"[(", ")]", graph.ShapeCylinder},
	{"[[", "]]", graph.ShapeSubroutine},
	{"[/", "\\]", graph.ShapeTrapezoid},
	{"[\\", "/]", graph.ShapeTrapezoidAlt},
	{"[/", "/]", graph.ShapeParallelogram},
	{"[\\", "\\]", graph.ShapeParallelogramAlt},
	{"{{", "}}", graph.ShapeHexagon},
	{"{", "}", graph.ShapeDiamond},
	{"(", ")", graph.ShapeRounded},
	{">", "]", graph.ShapeAsymmetric},
	{"[", "]", graph.ShapeRectangle},
}

// Map @{shape: name} values to NodeShape.
var atShapeMap = map[string]graph.NodeShape{
	"rect":       graph.ShapeRectangle,
	"rectangle":  graph.ShapeRectangle,
	"rounded":    graph.ShapeRounded,
	"circle":     graph.ShapeCircle,
	"circ":       graph.ShapeCircle,
	"diam":       graph.ShapeDiamond,
	"diamond":    graph.ShapeDiamond,
	"hex":        graph.ShapeHexagon,
	"hexagon":    graph.ShapeHexagon,
	"stadium":    graph.ShapeStadium,
	"terminal":   graph.ShapeStadium,
	"cyl":        graph.ShapeCylinder,
	"cylinder":   graph.ShapeCylinder,
	"db":         graph.ShapeCylinder,
	"subroutine": graph.ShapeSubroutine,
	"lean-r":     graph.ShapeParallelogram,
	"lean-l":     graph.ShapeParallelogramAlt,
	"trap-t":     graph.ShapeTrapezoid,
	"trap-b":     graph.ShapeTrapezoidAlt,
	"dbl-circ":   graph.ShapeDoubleCircle,
}

// validDirections lists all valid flowchart directions.
var validDirections = []graph.Direction{
	graph.DirTB, graph.DirTD, graph.DirLR, graph.DirBT, graph.DirRL,
}

// Pre-compiled regex patterns for arrow matching.
var (
	// Labeled arrow: -->|text| pattern
	reLabeledArrowPipe = regexp.MustCompile(`(<--|<-\.|-\.|-+|=+|<)([-=.]+)(>?)(\|)([^|]*)(\|)`)

	// Labeled arrow: -- text --> pattern
	reLabeledSolid  = regexp.MustCompile(`(--)(\s+.+?\s+)(-->)`)
	reLabeledThick  = regexp.MustCompile(`(==)(\s+.+?\s+)(==>)`)
	reLabeledDotted = regexp.MustCompile(`(-\.)(\s+.+?\s+)(\.+->)`)

	// Plain arrow patterns (ordered by specificity)
	reArrowBiDotted   = regexp.MustCompile(`<-\.+->`)
	reArrowBiThick    = regexp.MustCompile(`<=+=>`)
	reArrowBiSolid    = regexp.MustCompile(`<-+->`)
	reArrowBiCircle   = regexp.MustCompile(`o-+o`)
	reArrowBiCross    = regexp.MustCompile(`x-+x`)
	reArrowDotted     = regexp.MustCompile(`-\.+->`)
	reArrowThick      = regexp.MustCompile(`=+=>`)
	reArrowSolid      = regexp.MustCompile(`-+->`)
	reArrowSolidO     = regexp.MustCompile(`-+o(\s|$)`)
	reArrowSolidX     = regexp.MustCompile(`-+x(\s|$)`)
	reArrowInvisible  = regexp.MustCompile(`~~~`)
	reArrowOpenDotted = regexp.MustCompile(`-\.-`)
	reArrowOpenThick  = regexp.MustCompile(`={3,}`)
	reArrowOpenSolid  = regexp.MustCompile(`-{3,}`)

	// Node ID patterns
	reAtShape   = regexp.MustCompile(`(?s)^([a-zA-Z_]\w*)\s*@\{(.+)\}$`)
	rePlainNode = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

	// Subgraph bracket pattern
	// The space between the id and its bracketed title is optional: Mermaid
	// accepts both `subgraph SUB [Title]` and `subgraph SUB[Title]`, and models
	// overwhelmingly emit the second. Requiring the space left the whole token
	// as the title, so a group was captioned `SUB["Title"]` in raw source.
	reSubgraphBracket = regexp.MustCompile(`^([^\s\[]+)\s*\[(.+)\]`)
)

// plainArrowDef defines a plain arrow pattern for matching.
type plainArrowDef struct {
	re         *regexp.Regexp
	style      graph.EdgeStyle
	arrStart   bool
	arrEnd     bool
	typeStart  graph.ArrowType
	typeEnd    graph.ArrowType
	trimSuffix bool // if true, matched group may include trailing char to trim
}

var plainArrowDefs = []plainArrowDef{
	{reArrowBiDotted, graph.EdgeDotted, true, true, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowBiThick, graph.EdgeThick, true, true, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowBiSolid, graph.EdgeSolid, true, true, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowBiCircle, graph.EdgeSolid, true, true, graph.ArrowTypeCircle, graph.ArrowTypeCircle, false},
	{reArrowBiCross, graph.EdgeSolid, true, true, graph.ArrowTypeCross, graph.ArrowTypeCross, false},
	{reArrowDotted, graph.EdgeDotted, false, true, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowThick, graph.EdgeThick, false, true, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowSolid, graph.EdgeSolid, false, true, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowSolidO, graph.EdgeSolid, false, true, graph.ArrowTypeArrow, graph.ArrowTypeCircle, true},
	{reArrowSolidX, graph.EdgeSolid, false, true, graph.ArrowTypeArrow, graph.ArrowTypeCross, true},
	{reArrowInvisible, graph.EdgeInvisible, false, false, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowOpenDotted, graph.EdgeDotted, false, false, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowOpenThick, graph.EdgeThick, false, false, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
	{reArrowOpenSolid, graph.EdgeSolid, false, false, graph.ArrowTypeArrow, graph.ArrowTypeArrow, false},
}

// ParseFlowchart parses mermaid flowchart/graph text into a Graph model.
func ParseFlowchart(text string) *graph.Graph {
	p := &flowchartParser{
		text:          text,
		g:             graph.NewGraph(),
		shapedNodeIDs: make(map[string]struct{}),
	}
	return p.parse()
}

type flowchartParser struct {
	text          string
	g             *graph.Graph
	subgraphStack []*graph.Subgraph
	shapedNodeIDs map[string]struct{}
}

func (p *flowchartParser) parse() *graph.Graph {
	lines := p.preprocess(p.text)
	if len(lines) == 0 {
		return p.g
	}

	p.parseHeader(lines[0])

	for _, line := range lines[1:] {
		p.parseLine(line)
	}

	p.resolveSubgraphEdges()
	return p.g
}

func (p *flowchartParser) preprocess(text string) []string {
	var rawLines []string
	for _, line := range strings.Split(text, "\n") {
		parts := strings.Split(line, ";")
		rawLines = append(rawLines, parts...)
	}

	var result []string
	for _, line := range rawLines {
		stripped := strings.TrimSpace(stripComments(line))
		if stripped != "" {
			result = append(result, stripped)
		}
	}
	return result
}

func (p *flowchartParser) parseHeader(line string) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return
	}
	keyword := strings.ToLower(parts[0])
	if keyword != "graph" && keyword != "flowchart" {
		return
	}

	if len(parts) >= 2 {
		dirStr := strings.ToUpper(parts[1])
		found := false
		for _, d := range validDirections {
			if string(d) == dirStr {
				p.g.Direction = d
				p.g.DirectionExplicit = true
				found = true
				break
			}
		}
		if !found {
			p.g.Direction = graph.DirTB
		}
	} else {
		p.g.Direction = graph.DirTB
	}
}

func (p *flowchartParser) parseLine(line string) {
	lower := strings.TrimSpace(strings.ToLower(line))

	if strings.HasPrefix(lower, "subgraph") {
		p.parseSubgraph(line)
		return
	}
	if lower == "end" {
		p.closeSubgraph()
		return
	}
	if strings.HasPrefix(lower, "direction ") {
		p.parseDirectionOverride(line)
		return
	}
	if strings.HasPrefix(lower, "classdef ") {
		p.parseClassDef(line)
		return
	}
	if strings.HasPrefix(lower, "class ") {
		p.parseClassAssignment(line)
		return
	}
	if strings.HasPrefix(lower, "linkstyle ") {
		p.parseLinkStyle(line)
		return
	}
	if strings.HasPrefix(lower, "style ") {
		p.parseStyle(line)
		return
	}
	if strings.HasPrefix(lower, "click ") {
		return
	}

	p.parseStatement(line)
}

func (p *flowchartParser) parseSubgraph(line string) {
	rest := strings.TrimSpace(line[len("subgraph"):])

	sgID := rest
	sgLabel := rest

	// Check for 'id [label]' pattern
	m := reSubgraphBracket.FindStringSubmatch(rest)
	if m != nil {
		sgID = m[1]
		sgLabel = m[2]
	} else if strings.Contains(rest, " ") {
		// Could be 'subgraph title text'
		sgID = strings.Fields(rest)[0]
		sgLabel = rest
	}

	sgID = stripQuotes(sgID)
	sgLabel = sanitizeLabel(stripQuotes(sgLabel))

	var parent *graph.Subgraph
	if len(p.subgraphStack) > 0 {
		parent = p.subgraphStack[len(p.subgraphStack)-1]
	}

	sg := &graph.Subgraph{
		ID:     sgID,
		Label:  sgLabel,
		Parent: parent,
	}

	if parent != nil {
		parent.Children = append(parent.Children, sg)
	} else {
		p.g.Subgraphs = append(p.g.Subgraphs, sg)
	}

	p.subgraphStack = append(p.subgraphStack, sg)
}

func (p *flowchartParser) closeSubgraph() {
	if len(p.subgraphStack) > 0 {
		p.subgraphStack = p.subgraphStack[:len(p.subgraphStack)-1]
	}
}

func (p *flowchartParser) parseDirectionOverride(line string) {
	parts := strings.Fields(line)
	if len(parts) >= 2 && len(p.subgraphStack) > 0 {
		dirStr := strings.ToUpper(parts[1])
		for _, d := range validDirections {
			if string(d) == dirStr {
				dir := d
				p.subgraphStack[len(p.subgraphStack)-1].Direction = &dir
				break
			}
		}
	}
}

func (p *flowchartParser) parseClassDef(line string) {
	parts := splitN(line, 3)
	if len(parts) < 3 {
		return
	}
	name := parts[1]
	propsStr := parts[2]
	props := make(map[string]string)
	for _, prop := range strings.Split(propsStr, ",") {
		if idx := strings.Index(prop, ":"); idx >= 0 {
			k := strings.TrimSpace(prop[:idx])
			v := strings.TrimSpace(prop[idx+1:])
			props[k] = v
		}
	}
	p.g.ClassDefs[name] = props
}

func (p *flowchartParser) parseClassAssignment(line string) {
	parts := strings.Fields(line)
	if len(parts) >= 3 {
		nodeID := parts[1]
		className := parts[2]
		if node, ok := p.g.Nodes[nodeID]; ok {
			node.StyleClass = className
		}
	}
}

func (p *flowchartParser) parseStyle(line string) {
	parts := splitN(line, 3)
	if len(parts) >= 3 {
		nodeID := parts[1]
		props := parseCSSProps(parts[2])
		p.g.NodeStyles[nodeID] = props
	}
}

func (p *flowchartParser) parseLinkStyle(line string) {
	parts := splitN(line, 3)
	if len(parts) < 3 {
		return
	}
	indicesStr := parts[1]
	props := parseCSSProps(parts[2])

	if strings.ToLower(indicesStr) == "default" {
		p.g.LinkStyles[-1] = props
	} else {
		for _, idxStr := range strings.Split(indicesStr, ",") {
			idxStr = strings.TrimSpace(idxStr)
			if idx, err := strconv.Atoi(idxStr); err == nil {
				p.g.LinkStyles[idx] = props
			}
		}
	}
}

func (p *flowchartParser) parseStatement(line string) {
	// Try to find arrows in the line
	segments := p.splitByArrows(line)

	if len(segments) == 1 {
		// No arrows found - just node declarations (possibly with &)
		nodeGroups := splitAmpersand(segments[0].text)
		for _, nodeText := range nodeGroups {
			node := p.parseNode(strings.TrimSpace(nodeText))
			if node != nil {
				p.g.AddNode(node)
				p.registerInSubgraph(node.ID)
			}
		}
		return
	}

	// Process chained arrow segments
	// segments alternate: [node_group, arrow, node_group, arrow, node_group, ...]
	var prevNodes []string
	for i := 0; i < len(segments); i++ {
		seg := segments[i]
		if seg.isArrow {
			continue
		}

		// Parse node group (handles &)
		nodeGroup := splitAmpersand(seg.text)
		var currentNodes []string
		for _, nodeText := range nodeGroup {
			node := p.parseNode(strings.TrimSpace(nodeText))
			if node != nil {
				p.g.AddNode(node)
				p.registerInSubgraph(node.ID)
				currentNodes = append(currentNodes, node.ID)
			}
		}

		// If there's a previous group, create edges
		if len(prevNodes) > 0 && i > 0 {
			arrowSeg := segments[i-1]
			for _, src := range prevNodes {
				for _, tgt := range currentNodes {
					edge := graph.NewEdge(src, tgt)
					edge.Label = sanitizeLabel(arrowSeg.label)
					edge.Style = arrowSeg.edgeStyle
					edge.HasArrowStart = arrowSeg.hasArrowStart
					edge.HasArrowEnd = arrowSeg.hasArrowEnd
					edge.ArrowTypeStart = arrowSeg.arrowTypeStart
					edge.ArrowTypeEnd = arrowSeg.arrowTypeEnd
					edge.MinLength = arrowSeg.minLength
					p.g.AddEdge(edge)
				}
			}
		}

		prevNodes = currentNodes
	}
}

func (p *flowchartParser) registerInSubgraph(nodeID string) {
	if len(p.subgraphStack) == 0 {
		return
	}
	sg := p.subgraphStack[len(p.subgraphStack)-1]
	for _, id := range sg.NodeIDs {
		if id == nodeID {
			return
		}
	}
	sg.NodeIDs = append(sg.NodeIDs, nodeID)
}

func (p *flowchartParser) resolveSubgraphEdges() {
	// Collect all subgraph IDs
	sgIDs := make(map[string]struct{})
	var collect func(subs []*graph.Subgraph)
	collect = func(subs []*graph.Subgraph) {
		for _, sg := range subs {
			sgIDs[sg.ID] = struct{}{}
			collect(sg.Children)
		}
	}
	collect(p.g.Subgraphs)

	if len(sgIDs) == 0 {
		return
	}

	// Find nodes that are actually subgraph references
	toRemove := make(map[string]struct{})
	for i := range p.g.Edges {
		edge := &p.g.Edges[i]
		if _, ok := sgIDs[edge.Source]; ok {
			if _, shaped := p.shapedNodeIDs[edge.Source]; !shaped {
				edge.SourceIsSubgraph = true
				toRemove[edge.Source] = struct{}{}
			}
		}
		if _, ok := sgIDs[edge.Target]; ok {
			if _, shaped := p.shapedNodeIDs[edge.Target]; !shaped {
				edge.TargetIsSubgraph = true
				toRemove[edge.Target] = struct{}{}
			}
		}
	}

	// Remove spurious nodes
	for nid := range toRemove {
		delete(p.g.Nodes, nid)
		// Remove from NodeOrder
		for i, id := range p.g.NodeOrder {
			if id == nid {
				p.g.NodeOrder = append(p.g.NodeOrder[:i], p.g.NodeOrder[i+1:]...)
				break
			}
		}
		// Remove from subgraph node_ids
		var removeFromSG func(subs []*graph.Subgraph)
		removeFromSG = func(subs []*graph.Subgraph) {
			for _, sg := range subs {
				for i, id := range sg.NodeIDs {
					if id == nid {
						sg.NodeIDs = append(sg.NodeIDs[:i], sg.NodeIDs[i+1:]...)
						break
					}
				}
				removeFromSG(sg.Children)
			}
		}
		removeFromSG(p.g.Subgraphs)
	}
}

// arrowMatch holds a unified arrow match result (labeled or plain).
type arrowMatch struct {
	pos       int
	end       int
	style     graph.EdgeStyle
	arrStart  bool
	arrEnd    bool
	label     string
	length    int
	typeStart graph.ArrowType
	typeEnd   graph.ArrowType
}

// splitByArrows splits a line into alternating node and arrow segments.
// insideQuotedRegion returns true if position pos falls inside a quoted
// string ("...") or bracket pair ([...], (...), etc.) in text.
func insideQuotedRegion(text string, pos int) bool {
	inQuote := false
	depth := 0 // bracket nesting depth
	for i := 0; i < pos && i < len(text); i++ {
		switch text[i] {
		case '"':
			inQuote = !inQuote
		case '[', '(':
			if !inQuote {
				depth++
			}
		case ']', ')':
			if !inQuote && depth > 0 {
				depth--
			}
		}
	}
	return inQuote || depth > 0
}

func (p *flowchartParser) splitByArrows(line string) []segment {
	var segments []segment
	remaining := strings.TrimSpace(line)

	for remaining != "" {
		var best *arrowMatch

		// First check for labeled arrows: -->|text| or -- text -->
		if lm := p.findLabeledArrow(remaining); lm != nil && !insideQuotedRegion(remaining, lm.pos) {
			best = &arrowMatch{
				pos: lm.pos, end: lm.end, style: lm.style,
				arrStart: lm.arrStart, arrEnd: lm.arrEnd,
				label: lm.label, length: lm.length,
				typeStart: lm.typeStart, typeEnd: lm.typeEnd,
			}
		}

		// Then check for plain arrows
		if pm := p.findPlainArrow(remaining); pm != nil && !insideQuotedRegion(remaining, pm.pos) {
			if best == nil || pm.pos < best.pos {
				best = &arrowMatch{
					pos: pm.pos, end: pm.end, style: pm.style,
					arrStart: pm.arrStart, arrEnd: pm.arrEnd,
					label: "", length: pm.length,
					typeStart: pm.typeStart, typeEnd: pm.typeEnd,
				}
			}
		}

		if best == nil {
			// No more arrows - rest is a node group
			text := strings.TrimSpace(remaining)
			if text != "" {
				segments = append(segments, newSegment(text))
			}
			break
		}

		// Text before the arrow is a node group
		before := strings.TrimSpace(remaining[:best.pos])
		if before != "" {
			segments = append(segments, newSegment(before))
		}

		// The arrow itself
		segments = append(segments, segment{
			text:           remaining[best.pos:best.end],
			isArrow:        true,
			edgeStyle:      best.style,
			hasArrowStart:  best.arrStart,
			hasArrowEnd:    best.arrEnd,
			arrowTypeStart: best.typeStart,
			arrowTypeEnd:   best.typeEnd,
			label:          best.label,
			minLength:      best.length,
		})

		remaining = strings.TrimSpace(remaining[best.end:])
	}

	if len(segments) == 0 {
		return []segment{newSegment(strings.TrimSpace(line))}
	}
	return segments
}

// labeledArrowResult holds the result of finding a labeled arrow.
type labeledArrowResult struct {
	pos       int
	end       int
	style     graph.EdgeStyle
	arrStart  bool
	arrEnd    bool
	label     string
	length    int
	typeStart graph.ArrowType
	typeEnd   graph.ArrowType
}

func (p *flowchartParser) findLabeledArrow(text string) *labeledArrowResult {
	// Pattern: -->|text|
	m := reLabeledArrowPipe.FindStringSubmatchIndex(text)
	if m != nil {
		fullMatch := text[m[0]:m[1]]
		// Find position of first |
		pipeIdx := strings.Index(fullMatch, "|")
		arrowPart := text[m[0] : m[0]+pipeIdx]

		// Label is group 5 (indices m[10]:m[11])
		label := strings.TrimSpace(text[m[10]:m[11]])

		style, arrStart, arrEnd, typeStart, typeEnd := classifyArrow(arrowPart + ">")
		length := computeArrowLength(arrowPart, style)
		return &labeledArrowResult{
			pos:       m[0],
			end:       m[1],
			style:     style,
			arrStart:  arrStart,
			arrEnd:    arrEnd,
			label:     label,
			length:    length,
			typeStart: typeStart,
			typeEnd:   typeEnd,
		}
	}

	// Pattern: -- text --> or == text ==> or -. text .->
	type labeledPattern struct {
		re       *regexp.Regexp
		style    graph.EdgeStyle
		arrStart bool
		arrEnd   bool
	}
	labeledPatterns := []labeledPattern{
		{reLabeledSolid, graph.EdgeSolid, false, true},
		{reLabeledThick, graph.EdgeThick, false, true},
		{reLabeledDotted, graph.EdgeDotted, false, true},
	}
	for _, lp := range labeledPatterns {
		mi := lp.re.FindStringSubmatchIndex(text)
		if mi != nil {
			// Group 2 is the label (mi[4]:mi[5])
			labelText := strings.TrimSpace(text[mi[4]:mi[5]])
			// Group 1 is the prefix, group 3 is the suffix arrow
			arrowPortion := text[mi[2]:mi[3]] + text[mi[6]:mi[7]]
			length := computeArrowLength(arrowPortion, lp.style)
			return &labeledArrowResult{
				pos:       mi[0],
				end:       mi[1],
				style:     lp.style,
				arrStart:  lp.arrStart,
				arrEnd:    lp.arrEnd,
				label:     labelText,
				length:    length,
				typeStart: graph.ArrowTypeArrow,
				typeEnd:   graph.ArrowTypeArrow,
			}
		}
	}

	return nil
}

// plainArrowResult holds the result of finding a plain arrow.
type plainArrowResult struct {
	pos       int
	end       int
	style     graph.EdgeStyle
	arrStart  bool
	arrEnd    bool
	length    int
	typeStart graph.ArrowType
	typeEnd   graph.ArrowType
}

func (p *flowchartParser) findPlainArrow(text string) *plainArrowResult {
	var best *plainArrowResult

	for _, def := range plainArrowDefs {
		loc := def.re.FindStringIndex(text)
		if loc == nil {
			continue
		}
		pos := loc[0]
		end := loc[1]
		if def.trimSuffix {
			// For -+o and -+x patterns, the regex includes the lookahead char
			// which may have captured trailing whitespace; trim it.
			// The actual arrow ends one char before (the o or x).
			// The regex `-+o(\s|$)` captures the trailing char in group 1.
			// We want just the arrow part.
			sub := def.re.FindStringSubmatch(text)
			if len(sub) > 1 {
				end = end - len(sub[1])
			}
		}
		if best == nil || pos < best.pos {
			arrowText := text[pos:end]
			length := computeArrowLength(arrowText, def.style)
			best = &plainArrowResult{
				pos:       pos,
				end:       end,
				style:     def.style,
				arrStart:  def.arrStart,
				arrEnd:    def.arrEnd,
				length:    length,
				typeStart: def.typeStart,
				typeEnd:   def.typeEnd,
			}
		}
	}

	return best
}

// classifyArrow classifies an arrow string into style, direction, and endpoint types.
func classifyArrow(arrow string) (graph.EdgeStyle, bool, bool, graph.ArrowType, graph.ArrowType) {
	s := strings.TrimSpace(arrow)

	hasStart := strings.HasPrefix(s, "<") || strings.HasPrefix(s, "o") || strings.HasPrefix(s, "x")
	hasEnd := strings.HasSuffix(s, ">") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "o")

	typeStart := graph.ArrowTypeArrow
	typeEnd := graph.ArrowTypeArrow

	if strings.HasPrefix(s, "o") {
		typeStart = graph.ArrowTypeCircle
	} else if strings.HasPrefix(s, "x") {
		typeStart = graph.ArrowTypeCross
	}
	if strings.HasSuffix(s, "o") {
		typeEnd = graph.ArrowTypeCircle
	} else if strings.HasSuffix(s, "x") {
		typeEnd = graph.ArrowTypeCross
	}

	if strings.Contains(s, ".") {
		return graph.EdgeDotted, hasStart, hasEnd, typeStart, typeEnd
	}
	if strings.Contains(s, "=") {
		return graph.EdgeThick, hasStart, hasEnd, typeStart, typeEnd
	}
	if strings.Contains(s, "~") {
		return graph.EdgeInvisible, hasStart, hasEnd, typeStart, typeEnd
	}
	return graph.EdgeSolid, hasStart, hasEnd, typeStart, typeEnd
}

// computeArrowLength computes the min_length from the number of repeating chars
// in an arrow. Base forms (length 1): -->, ==>, -.->, ---, ===, -.-
// Each extra repeating character adds 1 to the length.
func computeArrowLength(arrowText string, style graph.EdgeStyle) int {
	hasHead := strings.HasSuffix(strings.TrimRight(arrowText, " "), ">") ||
		strings.HasPrefix(strings.TrimLeft(arrowText, " "), "<")

	// Strip directional markers for counting
	s := strings.TrimLeft(arrowText, "<ox")
	s = strings.TrimRight(s, ">ox")

	switch style {
	case graph.EdgeDotted:
		dots := strings.Count(s, ".")
		if dots < 1 {
			return 1
		}
		return dots
	case graph.EdgeThick:
		eqs := strings.Count(s, "=")
		base := 3
		if hasHead {
			base = 2
		}
		length := eqs - base + 1
		if length < 1 {
			return 1
		}
		return length
	case graph.EdgeSolid:
		dashes := strings.Count(s, "-")
		base := 3
		if hasHead {
			base = 2
		}
		length := dashes - base + 1
		if length < 1 {
			return 1
		}
		return length
	}
	return 1
}

// parseNode parses a single node declaration like 'A', 'A[label]', 'A{label}', etc.
func (p *flowchartParser) parseNode(text string) *graph.Node {
	if text == "" {
		return nil
	}

	text = strings.TrimRight(strings.TrimSpace(text), ";")

	// Fold HTML line breaks before any shape matching. sanitizeLabel replaces
	// them too, but it only runs on the label once the shape is already
	// decided — far too late. The shape patterns match on raw punctuation, and
	// `<br/>` carries both a `/` and a `>`: in `n["a<br/>b"]` the asymmetric
	// `>`…`]` pattern matches the `>` inside the tag, so the node ID comes out
	// as `n["a<br/` and the box is drawn in the wrong shape. `<br>` in a label
	// is ordinary GitHub-flavored Mermaid, so this is a common input, not an
	// edge case.
	text = foldHTMLBreaks(text)

	// Handle :::className suffix
	var styleClass string
	if idx := strings.LastIndex(text, ":::"); idx >= 0 {
		styleClass = strings.TrimSpace(text[idx+3:])
		text = strings.TrimSpace(text[:idx])
	}

	// Try @{...} syntax: ID@{ shape: diamond, label: "text" }
	if m := reAtShape.FindStringSubmatch(text); m != nil {
		nodeID := m[1]
		body := m[2]
		props := parseAtShapeProps(body)
		shapeName := "rect"
		if v, ok := props["shape"]; ok {
			shapeName = v
		}
		label := nodeID
		if v, ok := props["label"]; ok {
			label = v
		}
		shape := graph.ShapeRectangle
		if s, ok := atShapeMap[shapeName]; ok {
			shape = s
		}
		p.shapedNodeIDs[nodeID] = struct{}{}
		return &graph.Node{
			ID:         nodeID,
			Label:      sanitizeLabel(label),
			Shape:      shape,
			StyleClass: styleClass,
		}
	}

	// Try each shape pattern
	for _, sp := range shapePatterns {
		idx := strings.Index(text, sp.open)
		if idx <= 0 {
			continue
		}
		rest := text[idx+len(sp.open):]
		if !strings.HasSuffix(rest, sp.close) {
			continue
		}
		nodeID := strings.TrimSpace(text[:idx])
		rawLabel := strings.TrimSpace(rest[:len(rest)-len(sp.close)])
		if nodeID == "" {
			continue
		}
		p.shapedNodeIDs[nodeID] = struct{}{}

		// Check for markdown label
		if md := parseMarkdownLabel(rawLabel); md != nil {
			return &graph.Node{
				ID:            nodeID,
				Label:         sanitizeLabel(md.plain),
				Shape:         sp.shape,
				StyleClass:    styleClass,
				LabelSegments: md.segments,
			}
		}

		label := sanitizeLabel(stripQuotes(rawLabel))
		return &graph.Node{
			ID:         nodeID,
			Label:      label,
			Shape:      sp.shape,
			StyleClass: styleClass,
		}
	}

	// Plain node ID (no shape delimiters)
	nodeID := strings.TrimSpace(text)
	if nodeID == "" {
		return nil
	}
	if !rePlainNode.MatchString(nodeID) {
		// Try with quotes
		nodeID = stripQuotes(nodeID)
		if nodeID == "" {
			return nil
		}
	}

	return &graph.Node{
		ID:         nodeID,
		Label:      nodeID,
		Shape:      graph.ShapeRectangle,
		StyleClass: styleClass,
	}
}

// splitAmpersand splits 'A & B & C' into ['A', 'B', 'C'].
// Only splits on & outside of bracket/brace/paren delimiters.
func splitAmpersand(text string) []string {
	var parts []string
	depth := 0
	var current []byte
	i := 0
	for i < len(text) {
		ch := text[i]
		switch ch {
		case '(', '[', '{':
			depth++
			current = append(current, ch)
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
			current = append(current, ch)
		case '&':
			if depth == 0 && i > 0 && text[i-1] == ' ' {
				// Check for ' & ' pattern
				if i+1 < len(text) && text[i+1] == ' ' {
					part := strings.TrimRight(string(current), " ")
					if part != "" {
						parts = append(parts, part)
					}
					current = current[:0]
					i += 2 // skip '& '
					continue
				}
				current = append(current, ch)
			} else {
				current = append(current, ch)
			}
		default:
			current = append(current, ch)
		}
		i++
	}
	part := strings.TrimSpace(string(current))
	if part != "" {
		parts = append(parts, part)
	}
	return parts
}

// --- Helper functions ---

func stripComments(line string) string {
	if idx := strings.Index(line, "%%"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func stripQuotes(text string) string {
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		return text[1 : len(text)-1]
	}
	return text
}

// sanitizeLabel cleans up label text for terminal rendering.
// Handles common Mermaid conventions that don't apply to terminal output:
//   - \n (literal backslash-n) → space (Claude and editors often insert these)
//   - <br>, <br/>, <br /> → space (HTML line breaks used in GitHub Mermaid)
//   - Control characters (including ANSI escapes) → stripped
func sanitizeLabel(text string) string {
	text = strings.ReplaceAll(text, `\n`, " ")
	text = foldHTMLBreaks(text)
	return stripControlChars(text)
}

// htmlBreakRe matches the HTML line-break tag in all the spellings Mermaid
// authors use: <br>, <br/>, <br />, and any casing.
var htmlBreakRe = regexp.MustCompile(`(?i)<br\s*/?>`)

// foldHTMLBreaks replaces HTML line breaks with a space.
//
// It runs in two places, and the earlier one matters most: parseNode calls it
// before shape detection, because the `/` and `>` inside the tag otherwise
// match the shape patterns and corrupt both the node ID and the box shape.
func foldHTMLBreaks(text string) string {
	return htmlBreakRe.ReplaceAllString(text, " ")
}

// ansiEscRe matches ANSI escape sequences: ESC followed by [ and parameters.
var ansiEscRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripControlChars removes control characters and ANSI escape sequences from text.
// This prevents terminal control codes from being injected through Mermaid labels.
func stripControlChars(text string) string {
	// Strip full ANSI escape sequences first (ESC[...m etc).
	text = ansiEscRe.ReplaceAllString(text, "")

	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == '\t' {
			b.WriteByte(' ')
		} else if unicode.IsControl(r) {
			continue
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseCSSProps(text string) map[string]string {
	props := make(map[string]string)
	for _, prop := range strings.Split(text, ",") {
		if idx := strings.Index(prop, ":"); idx >= 0 {
			k := strings.TrimSpace(prop[:idx])
			v := strings.TrimSpace(prop[idx+1:])
			props[k] = v
		}
	}
	return props
}

func parseAtShapeProps(body string) map[string]string {
	props := make(map[string]string)
	// Split by commas, but respect quoted strings
	var parts []string
	var current []byte
	inQuote := false
	for i := 0; i < len(body); i++ {
		ch := body[i]
		if ch == '"' && !inQuote {
			inQuote = true
		} else if ch == '"' && inQuote {
			inQuote = false
		} else if ch == ',' && !inQuote {
			parts = append(parts, string(current))
			current = current[:0]
			continue
		}
		current = append(current, ch)
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, ":"); idx >= 0 {
			k := strings.TrimSpace(part[:idx])
			v := strings.TrimSpace(part[idx+1:])
			v = strings.Trim(v, "\"")
			props[k] = v
		}
	}
	return props
}

// markdownResult holds the parsed markdown label.
type markdownResult struct {
	plain    string
	segments []graph.LabelSegment
}

func parseMarkdownLabel(text string) *markdownResult {
	stripped := strings.TrimSpace(text)
	if !strings.HasPrefix(stripped, "\"`") || !strings.HasSuffix(stripped, "`\"") {
		return nil
	}

	md := stripped[2 : len(stripped)-2] // strip "` and `"
	var segments []graph.LabelSegment
	var plainParts []string
	i := 0
	for i < len(md) {
		// Bold: **text**
		if i+1 < len(md) && md[i:i+2] == "**" {
			end := strings.Index(md[i+2:], "**")
			if end != -1 {
				inner := md[i+2 : i+2+end]
				segments = append(segments, graph.LabelSegment{Text: inner, Bold: true})
				plainParts = append(plainParts, inner)
				i = i + 2 + end + 2
				continue
			}
		}
		// Italic: *text*
		if md[i] == '*' {
			end := strings.Index(md[i+1:], "*")
			if end != -1 {
				inner := md[i+1 : i+1+end]
				segments = append(segments, graph.LabelSegment{Text: inner, Italic: true})
				plainParts = append(plainParts, inner)
				i = i + 1 + end + 1
				continue
			}
		}
		// Plain text: collect until next *
		j := i
		for j < len(md) && md[j] != '*' {
			j++
		}
		segments = append(segments, graph.LabelSegment{Text: md[i:j]})
		plainParts = append(plainParts, md[i:j])
		i = j
	}

	plain := strings.Join(plainParts, "")
	return &markdownResult{plain: plain, segments: segments}
}

// splitN splits a string into at most n whitespace-separated fields,
// where the last field contains the remainder of the string.
// This mirrors Python's str.split(None, n-1).
func splitN(s string, n int) []string {
	s = strings.TrimSpace(s)
	if n <= 0 {
		return strings.Fields(s)
	}
	var result []string
	for i := 0; i < n-1; i++ {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		idx := strings.IndexAny(s, " \t")
		if idx < 0 {
			result = append(result, s)
			s = ""
			break
		}
		result = append(result, s[:idx])
		s = s[idx+1:]
	}
	s = strings.TrimSpace(s)
	if s != "" {
		result = append(result, s)
	}
	return result
}
