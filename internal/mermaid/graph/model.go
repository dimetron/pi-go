// Package graph defines the core types for representing parsed Mermaid flowcharts.
package graph

import "slices"

// NodeShape represents the visual shape of a graph node.
type NodeShape int

const (
	ShapeRectangle        NodeShape = iota // A[text] or plain A
	ShapeRounded                           // A(text)
	ShapeStadium                           // A([text])
	ShapeSubroutine                        // A[[text]]
	ShapeDiamond                           // A{text}
	ShapeHexagon                           // A{{text}}
	ShapeCircle                            // A((text))
	ShapeDoubleCircle                      // A(((text)))
	ShapeAsymmetric                        // A>text]
	ShapeCylinder                          // A[(text)]
	ShapeParallelogram                     // A[/text/]
	ShapeParallelogramAlt                  // A[\text\]
	ShapeTrapezoid                         // A[/text\]
	ShapeTrapezoidAlt                      // A[\text/]
	ShapeStartState                        // [*] start (filled circle)
	ShapeEndState                          // [*] end (bullseye)
	ShapeForkJoin                          // <<fork>>/<<join>> (thick bar)
)

// ArrowType represents the tip style of an edge arrow.
type ArrowType int

const (
	ArrowTypeArrow  ArrowType = iota // --> (filled triangle)
	ArrowTypeCircle                  // --o
	ArrowTypeCross                   // --x
)

// EdgeStyle represents the line style of an edge.
type EdgeStyle int

const (
	EdgeSolid     EdgeStyle = iota // -->
	EdgeDotted                     // -.->
	EdgeThick                      // ==>
	EdgeInvisible                  // ~~~
)

// Direction represents the layout direction of a graph or subgraph.
type Direction string

const (
	DirTB Direction = "TB"
	DirTD Direction = "TD"
	DirLR Direction = "LR"
	DirBT Direction = "BT"
	DirRL Direction = "RL"
)

// IsVertical reports whether the direction flows top-to-bottom or bottom-to-top.
func (d Direction) IsVertical() bool {
	return d == DirTB || d == DirTD || d == DirBT
}

// IsHorizontal reports whether the direction flows left-to-right or right-to-left.
func (d Direction) IsHorizontal() bool {
	return d == DirLR || d == DirRL
}

// IsReversed reports whether the direction is reversed (BT or RL).
func (d Direction) IsReversed() bool {
	return d == DirBT || d == DirRL
}

// Normalized returns the canonical equivalent direction (BT->TB, RL->LR, TD->TB).
func (d Direction) Normalized() Direction {
	switch d {
	case DirBT, DirTD:
		return DirTB
	case DirRL:
		return DirLR
	default:
		return d
	}
}

// LabelSegment is a segment of a node label with optional bold/italic styling.
type LabelSegment struct {
	Text   string
	Bold   bool
	Italic bool
}

// GraphNote is a note attached to a node in the graph.
type GraphNote struct {
	Text     string // note content
	Position string // "rightof" or "leftof"
	Target   string // node ID
}

// Node represents a single node in a flowchart.
type Node struct {
	ID            string
	Label         string
	Shape         NodeShape
	StyleClass    string         // empty string means no class
	LabelSegments []LabelSegment // nil means no rich segments
}

// Edge represents a connection between two nodes.
type Edge struct {
	Source           string
	Target           string
	Label            string
	Style            EdgeStyle
	HasArrowStart    bool
	HasArrowEnd      bool
	ArrowTypeStart   ArrowType
	ArrowTypeEnd     ArrowType
	MinLength        int
	SourceIsSubgraph bool
	TargetIsSubgraph bool
}

// IsBidirectional reports whether the edge has arrows on both ends.
func (e *Edge) IsBidirectional() bool {
	return e.HasArrowStart && e.HasArrowEnd
}

// IsSelfReference reports whether the edge connects a node to itself.
func (e *Edge) IsSelfReference() bool {
	return e.Source == e.Target
}

// NewEdge returns an Edge with sensible defaults matching Mermaid's default arrow.
func NewEdge(source, target string) Edge {
	return Edge{
		Source:       source,
		Target:       target,
		Style:        EdgeSolid,
		HasArrowEnd:  true,
		ArrowTypeEnd: ArrowTypeArrow,
		MinLength:    1,
	}
}

// Subgraph represents a named group of nodes.
type Subgraph struct {
	ID        string
	Label     string
	NodeIDs   []string
	Children  []*Subgraph
	Direction *Direction // nil means inherit from parent
	Parent    *Subgraph  // nil for top-level subgraphs
}

// Graph is the top-level container for a parsed flowchart.
type Graph struct {
	Direction         Direction
	DirectionExplicit bool // true if direction was set in source (not default)
	Nodes             map[string]*Node
	Edges             []Edge
	Subgraphs         []*Subgraph
	NodeOrder         []string
	ClassDefs         map[string]map[string]string
	NodeStyles        map[string]map[string]string
	LinkStyles        map[int]map[string]string
	Warnings          []string
	Notes             []GraphNote
}

// NewGraph returns a Graph initialized with default values.
func NewGraph() *Graph {
	return &Graph{
		Direction:  DirTB,
		Nodes:      make(map[string]*Node),
		ClassDefs:  make(map[string]map[string]string),
		NodeStyles: make(map[string]map[string]string),
		LinkStyles: make(map[int]map[string]string),
	}
}

// AddNode adds a node to the graph. If a node with the same ID already exists,
// it merges label, shape, and style class from the new node when they carry
// non-default values and the existing node still has defaults.
func (g *Graph) AddNode(node *Node) {
	if existing, ok := g.Nodes[node.ID]; ok {
		// Update label if new node has an explicit label and existing is still the default.
		if node.Label != node.ID && existing.Label == existing.ID {
			existing.Label = node.Label
		}
		// Update shape if new node specifies a non-default shape.
		if node.Shape != ShapeRectangle {
			existing.Shape = node.Shape
		}
		// Update style class if new node specifies one.
		if node.StyleClass != "" {
			existing.StyleClass = node.StyleClass
		}
		return
	}
	g.Nodes[node.ID] = node
	g.NodeOrder = append(g.NodeOrder, node.ID)
}

// AddEdge appends an edge to the graph.
func (g *Graph) AddEdge(edge Edge) {
	g.Edges = append(g.Edges, edge)
}

// GetRoots returns node IDs that have no incoming edges, in definition order.
// If no roots are found, the first node in definition order is returned.
func (g *Graph) GetRoots() []string {
	targets := make(map[string]struct{}, len(g.Edges))
	for _, e := range g.Edges {
		targets[e.Target] = struct{}{}
	}
	var roots []string
	for _, nid := range g.NodeOrder {
		if _, isTarget := targets[nid]; !isTarget {
			roots = append(roots, nid)
		}
	}
	if len(roots) == 0 && len(g.NodeOrder) > 0 {
		return []string{g.NodeOrder[0]}
	}
	return roots
}

// GetChildren returns the target node IDs of outgoing edges from nodeID,
// in edge definition order, with duplicates removed.
func (g *Graph) GetChildren(nodeID string) []string {
	seen := make(map[string]struct{})
	var children []string
	for _, e := range g.Edges {
		if e.Source == nodeID && e.Target != nodeID {
			if _, ok := seen[e.Target]; !ok {
				seen[e.Target] = struct{}{}
				children = append(children, e.Target)
			}
		}
	}
	return children
}

// FindSubgraphByID searches recursively for a subgraph with the given ID.
func (g *Graph) FindSubgraphByID(sgID string) *Subgraph {
	return searchSubgraphs(g.Subgraphs, func(sg *Subgraph) bool {
		return sg.ID == sgID
	})
}

// FindSubgraphForNode returns the innermost subgraph that contains nodeID.
func (g *Graph) FindSubgraphForNode(nodeID string) *Subgraph {
	return findInnermostSubgraph(g.Subgraphs, nodeID)
}

// searchSubgraphs performs a recursive depth-first search for a subgraph
// matching the predicate.
func searchSubgraphs(subs []*Subgraph, match func(*Subgraph) bool) *Subgraph {
	for _, sg := range subs {
		if match(sg) {
			return sg
		}
		if result := searchSubgraphs(sg.Children, match); result != nil {
			return result
		}
	}
	return nil
}

// findInnermostSubgraph searches children first so the deepest match wins.
func findInnermostSubgraph(subs []*Subgraph, nodeID string) *Subgraph {
	for _, sg := range subs {
		if result := findInnermostSubgraph(sg.Children, nodeID); result != nil {
			return result
		}
		if slices.Contains(sg.NodeIDs, nodeID) {
			return sg
		}
	}
	return nil
}
