package palace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-path ---

// KGPathToolInput defines parameters for the palace-kg-path tool.
type KGPathToolInput struct {
	// Start entity. At least one of from and to is required.
	From string `json:"from,omitempty"`
	// Target entity.
	To string `json:"to,omitempty"`
	// Only follow these predicates (default: any).
	Predicates []string `json:"predicates,omitempty"`
	// out (default), in, or both.
	Direction string `json:"direction,omitempty"`
	// reach, shortest (default), all_shortest, or trails.
	Mode string `json:"mode,omitempty"`
	// Each step must happen at or after the previous one in valid time.
	TimeRespecting bool `json:"time_respecting,omitempty"`
	// Hop limits (default 1..6, max 16).
	MinHops int `json:"min_hops,omitempty"`
	MaxHops int `json:"max_hops,omitempty"`
	// Maximum paths (default 20).
	Limit int `json:"limit,omitempty"`
	// Only facts in this named graph.
	Graph string `json:"graph,omitempty"`
	// What was believed then: a transaction number or a time.
	TxAsOf string `json:"tx_as_of,omitempty"`
	// Only facts valid at this time.
	AsOf string `json:"as_of,omitempty"`
}

// KGPathToolOutput contains the result.
type KGPathToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGPathTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-path",
		Description: "Find how entities connect in the knowledge graph: paths from one entity to another (or everything " +
			"reachable). mode shortest/all_shortest/reach/trails; time_respecting follows only chains where each fact " +
			"held at or after the previous one (causal journeys). Each step shows its fact id.",
	}, func(ctx agent.Context, input KGPathToolInput) (KGPathToolOutput, error) {
		return palaceKGPathHandler(ctx, p, input)
	})
}

func palaceKGPathHandler(ctx context.Context, p *Palace, input KGPathToolInput) (KGPathToolOutput, error) {
	if input.From == "" && input.To == "" {
		return KGPathToolOutput{Content: "Error: from or to is required"}, nil
	}
	if input.Mode == "" {
		input.Mode = "shortest"
	}
	mode, err := ParsePathMode(input.Mode)
	if err != nil {
		return KGPathToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	v, err := p.KG().View(ctx, input.TxAsOf, input.AsOf)
	if err != nil {
		return KGPathToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if input.Graph != "" {
		v = v.InGraph(input.Graph)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	paths, err := v.Paths(ctx, PathQuery{
		From: input.From, To: input.To, Predicates: input.Predicates, Direction: input.Direction,
		MinHops: input.MinHops, MaxHops: input.MaxHops, Mode: mode, TimeRespecting: input.TimeRespecting,
		Limit: limit,
	})
	if err != nil {
		return KGPathToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if len(paths) == 0 {
		return KGPathToolOutput{Content: "No path found."}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d path(s), %s:\n", len(paths), v)
	for _, path := range paths {
		sb.WriteString("- ")
		for i, st := range path.Steps {
			if i == 0 {
				sb.WriteString(path.Nodes[0].String())
			}
			fmt.Fprintf(&sb, " -[e%d]-> %s", st.EID, path.Nodes[i+1].String())
		}
		if path.Arrival != nil {
			sb.WriteString(" (arrives " + path.Arrival.Format(time.RFC3339) + ")")
		}
		sb.WriteString("\n")
	}
	return KGPathToolOutput{Content: sb.String()}, nil
}
