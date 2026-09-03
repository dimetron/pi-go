// Package diagram implements parsers and renderers for specialized Mermaid diagram types.
package diagram

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/graph"
)

// stateParser holds parsing state for a state diagram.
type stateParser struct {
	g           *graph.Graph
	aliases     map[string]string // "state X as Y" mappings
	stereotypes map[string]string // node ID -> stereotype (choice, fork, join)
	idCounter   int               // unique ID counter for start/end pseudo-nodes
}

func newStateParser() *stateParser {
	return &stateParser{
		g:           graph.NewGraph(),
		aliases:     make(map[string]string),
		stereotypes: make(map[string]string),
	}
}

func (sp *stateParser) nextStartID() string {
	sp.idCounter++
	return fmt.Sprintf("__start_%d__", sp.idCounter)
}

func (sp *stateParser) nextEndID() string {
	sp.idCounter++
	return fmt.Sprintf("__end_%d__", sp.idCounter)
}

// resolveID returns the canonical node ID for an alias or raw ID.
func (sp *stateParser) resolveID(id string) string {
	if canonical, ok := sp.aliases[id]; ok {
		return canonical
	}
	return id
}

// ensureNode adds a node if it doesn't already exist.
func (sp *stateParser) ensureNode(id, label string, shape graph.NodeShape) {
	if _, ok := sp.g.Nodes[id]; ok {
		return
	}
	if label == "" {
		label = id
	}
	sp.g.AddNode(&graph.Node{
		ID:    id,
		Label: label,
		Shape: shape,
	})
}

// shapeForStereotype returns the node shape for a given stereotype.
func shapeForStereotype(stereotype string) graph.NodeShape {
	switch stereotype {
	case "choice":
		return graph.ShapeDiamond
	case "fork", "join":
		return graph.ShapeForkJoin
	default:
		return graph.ShapeRounded
	}
}

// Regex patterns for state diagram parsing.
var (
	reStateDiagramHeader  = regexp.MustCompile(`(?i)^\s*stateDiagram(?:-v2)?\s*$`)
	reTransition          = regexp.MustCompile(`^\s*(\S+)\s*-->\s*(\S+)\s*(?::\s*(.*))?$`)
	reStateAlias          = regexp.MustCompile(`(?i)^\s*state\s+"([^"]+)"\s+as\s+(\S+)\s*$`)
	reStateStereotype     = regexp.MustCompile(`(?i)^\s*state\s+(\S+)\s+<<(\w+)>>\s*$`)
	reStateCompositeOpen  = regexp.MustCompile(`(?i)^\s*state\s+(\S+)\s*\{\s*$`)
	reStateCompositeAlias = regexp.MustCompile(`(?i)^\s*state\s+"([^"]+)"\s+as\s+(\S+)\s*\{\s*$`)
	reStateCompositeClose = regexp.MustCompile(`^\s*\}\s*$`)
	reStateDirection      = regexp.MustCompile(`(?i)^\s*direction\s+(LR|RL|TB|BT|TD)\s*$`)
	reStateNoteRight      = regexp.MustCompile(`(?i)^\s*note\s+right\s+of\s+(\S+)\s*:\s*(.*)$`)
	reStateNoteLeft       = regexp.MustCompile(`(?i)^\s*note\s+left\s+of\s+(\S+)\s*:\s*(.*)$`)
)

// ParseStateDiagram parses a stateDiagram / stateDiagram-v2 block into a *graph.Graph
// suitable for rendering by the existing flowchart renderer.
func ParseStateDiagram(text string) *graph.Graph {
	sp := newStateParser()
	sp.g.Direction = graph.DirTB

	lines := strings.Split(text, "\n")
	sp.parseStateLines(lines, nil)

	return sp.g
}

// parseStateLines processes lines, handling nesting via composite states.
func (sp *stateParser) parseStateLines(lines []string, parentSG *graph.Subgraph) {
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip empty lines, comments, and header
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") || reStateDiagramHeader.MatchString(trimmed) {
			i++
			continue
		}

		// Direction
		if m := reStateDirection.FindStringSubmatch(trimmed); m != nil {
			dir := graph.Direction(strings.ToUpper(m[1]))
			if dir == graph.DirTD {
				dir = graph.DirTB
			}
			sp.g.Direction = dir
			sp.g.DirectionExplicit = true
			i++
			continue
		}

		// State alias: state "Display Name" as id
		if m := reStateAlias.FindStringSubmatch(trimmed); m != nil {
			displayName := m[1]
			id := m[2]
			sp.aliases[id] = id
			stereotype := sp.stereotypes[id]
			shape := shapeForStereotype(stereotype)
			sp.ensureNode(id, displayName, shape)
			if parentSG != nil {
				sp.addToSubgraph(parentSG, id)
			}
			i++
			continue
		}

		// State stereotype: state id <<choice>>
		if m := reStateStereotype.FindStringSubmatch(trimmed); m != nil {
			id := m[1]
			stereotype := strings.ToLower(m[2])
			sp.stereotypes[id] = stereotype
			shape := shapeForStereotype(stereotype)
			sp.ensureNode(id, id, shape)
			// Update shape if node already existed with default shape
			if n, ok := sp.g.Nodes[id]; ok {
				n.Shape = shape
			}
			if parentSG != nil {
				sp.addToSubgraph(parentSG, id)
			}
			i++
			continue
		}

		// Composite state with alias: state "Name" as id {
		if m := reStateCompositeAlias.FindStringSubmatch(trimmed); m != nil {
			displayName := m[1]
			id := m[2]
			sp.aliases[id] = id
			sg := sp.openComposite(id, displayName, parentSG)
			closeIdx := sp.findStateCloseBrace(lines, i+1)
			if closeIdx > i+1 {
				sp.parseStateLines(lines[i+1:closeIdx], sg)
			}
			i = closeIdx + 1
			continue
		}

		// Composite state: state id {
		if m := reStateCompositeOpen.FindStringSubmatch(trimmed); m != nil {
			id := m[1]
			sg := sp.openComposite(id, id, parentSG)
			closeIdx := sp.findStateCloseBrace(lines, i+1)
			if closeIdx > i+1 {
				sp.parseStateLines(lines[i+1:closeIdx], sg)
			}
			i = closeIdx + 1
			continue
		}

		// Close brace (handled by caller for composites)
		if reStateCompositeClose.MatchString(trimmed) {
			i++
			continue
		}

		// Note right of
		if m := reStateNoteRight.FindStringSubmatch(trimmed); m != nil {
			target := sp.resolveID(m[1])
			noteText := strings.TrimSpace(m[2])
			sp.g.Notes = append(sp.g.Notes, graph.GraphNote{
				Text:     noteText,
				Position: "rightof",
				Target:   target,
			})
			i++
			continue
		}

		// Note left of
		if m := reStateNoteLeft.FindStringSubmatch(trimmed); m != nil {
			target := sp.resolveID(m[1])
			noteText := strings.TrimSpace(m[2])
			sp.g.Notes = append(sp.g.Notes, graph.GraphNote{
				Text:     noteText,
				Position: "leftof",
				Target:   target,
			})
			i++
			continue
		}

		// Transition: State1 --> State2 : label
		if m := reTransition.FindStringSubmatch(trimmed); m != nil {
			sourceRaw := m[1]
			targetRaw := m[2]
			label := ""
			if len(m) > 3 {
				label = strings.TrimSpace(m[3])
			}

			sourceID := sp.resolveStateTransitionRef(sourceRaw, true, parentSG)
			targetID := sp.resolveStateTransitionRef(targetRaw, false, parentSG)

			edge := graph.NewEdge(sourceID, targetID)
			edge.Label = label
			sp.g.AddEdge(edge)
			i++
			continue
		}

		i++
	}
}

// resolveStateTransitionRef resolves a state reference in a transition.
// isSource indicates whether the reference is on the source side of -->.
// [*] as source creates a start state; [*] as target creates an end state.
func (sp *stateParser) resolveStateTransitionRef(raw string, isSource bool, parentSG *graph.Subgraph) string {
	if raw == "[*]" {
		if isSource {
			id := sp.nextStartID()
			sp.g.AddNode(&graph.Node{
				ID:    id,
				Label: "\u25CF", // ●
				Shape: graph.ShapeStartState,
			})
			if parentSG != nil {
				sp.addToSubgraph(parentSG, id)
			}
			return id
		}
		id := sp.nextEndID()
		sp.g.AddNode(&graph.Node{
			ID:    id,
			Label: "\u25C9", // ◉
			Shape: graph.ShapeEndState,
		})
		if parentSG != nil {
			sp.addToSubgraph(parentSG, id)
		}
		return id
	}

	id := sp.resolveID(raw)
	stereotype := sp.stereotypes[id]
	shape := shapeForStereotype(stereotype)
	sp.ensureNode(id, id, shape)
	if parentSG != nil {
		sp.addToSubgraph(parentSG, id)
	}
	return id
}

// openComposite creates a subgraph for a composite state.
func (sp *stateParser) openComposite(id, label string, parentSG *graph.Subgraph) *graph.Subgraph {
	sg := &graph.Subgraph{
		ID:     id,
		Label:  label,
		Parent: parentSG,
	}
	if parentSG != nil {
		parentSG.Children = append(parentSG.Children, sg)
	} else {
		sp.g.Subgraphs = append(sp.g.Subgraphs, sg)
	}
	return sg
}

// addToSubgraph adds a node ID to a subgraph if not already present.
func (sp *stateParser) addToSubgraph(sg *graph.Subgraph, nodeID string) {
	if slices.Contains(sg.NodeIDs, nodeID) {
		return
	}
	sg.NodeIDs = append(sg.NodeIDs, nodeID)
}

// findStateCloseBrace finds the matching closing brace, accounting for nesting.
func (sp *stateParser) findStateCloseBrace(lines []string, startIdx int) int {
	depth := 1
	for i := startIdx; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if reStateCompositeOpen.MatchString(trimmed) || reStateCompositeAlias.MatchString(trimmed) {
			depth++
		}
		if reStateCompositeClose.MatchString(trimmed) {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(lines)
}
