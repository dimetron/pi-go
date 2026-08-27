package sop

import (
	"sort"

	"google.golang.org/adk/v2/workflow"
)

// GraphEdge is one compiled edge flattened to plain strings.
//
// It exists so a UI can draw the graph without importing the workflow package
// or type-switching on its Route interface: the renderer needs three strings,
// not three node objects.
type GraphEdge struct {
	From string
	To   string
	// Route is the value that must be produced for this edge to be taken —
	// "PASS", "FAIL", RecheckSignal — and empty for an unconditional edge or
	// the default forward edge of a stage that can fail over.
	Route string
}

// GraphEdges returns the compiled edges in a deterministic order: by source
// stage, unconditional edge first, then by route. Rendering order must not
// depend on map iteration, or the sidebar would redraw differently each frame.
func (c *Compiled) GraphEdges() []GraphEdge {
	out := make([]GraphEdge, 0, len(c.Edges))
	for _, e := range c.Edges {
		route := ""
		if sr, ok := e.Route.(workflow.StringRoute); ok {
			route = string(sr)
		}
		out = append(out, GraphEdge{From: e.From.Name(), To: e.To.Name(), Route: route})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].Route != out[j].Route {
			return out[i].Route < out[j].Route
		}
		return out[i].To < out[j].To
	})
	return out
}

// StartNodeName is the name of the graph's entry sentinel. Edges out of it are
// scaffolding, not a stage transition, so renderers skip them.
var StartNodeName = workflow.Start.Name()
