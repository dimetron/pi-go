package palace

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-graph ---

// KGGraphToolInput defines parameters for the palace-kg-graph tool.
type KGGraphToolInput struct {
	// add (put a fact into a graph), remove (take it out; the fact stays),
	// or list (graphs and their sizes, or the graphs of one fact with eid).
	Action string `json:"action"`
	// The statement id, the number in e12 (add, remove; optional for list).
	EID int64 `json:"eid,omitempty"`
	// The graph name, e.g. a project, session or topic (add, remove).
	Graph string `json:"graph,omitempty"`
}

// KGGraphToolOutput contains the result.
type KGGraphToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGGraphTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-graph",
		Description: "Group knowledge-graph facts into named graphs (a project, session or topic). add puts fact <eid> " +
			"into <graph>, remove takes it out (the fact itself stays), list shows the graphs or the graphs of one fact. " +
			"Then pass graph=<name> to palace-kg-query, palace-kg-select or palace-kg-path to read only that graph. " +
			"Membership is versioned like any fact.",
	}, func(ctx agent.Context, input KGGraphToolInput) (KGGraphToolOutput, error) {
		return palaceKGGraphHandler(ctx, p, input)
	})
}

func palaceKGGraphHandler(ctx context.Context, p *Palace, input KGGraphToolInput) (KGGraphToolOutput, error) {
	kg := p.KG()
	author, session := txMeta(ctx)
	opts := TxOptions{Author: author, Source: session}
	switch input.Action {
	case "add", "remove":
		if input.EID <= 0 || strings.TrimSpace(input.Graph) == "" {
			return KGGraphToolOutput{Content: "Error: eid and graph are required"}, nil
		}
		var (
			changed bool
			err     error
		)
		_, err = kg.Transact(ctx, opts, func(w *KGTx) error {
			if input.Action == "add" {
				_, changed, err = w.AddToGraph(input.EID, input.Graph)
				return err
			}
			changed, err = w.RemoveFromGraph(input.EID, input.Graph)
			return err
		})
		if err != nil {
			return KGGraphToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		g := entityID(input.Graph)
		switch {
		case input.Action == "add" && changed:
			return KGGraphToolOutput{Content: fmt.Sprintf("e%d added to graph %s.", input.EID, g)}, nil
		case input.Action == "add":
			return KGGraphToolOutput{Content: fmt.Sprintf("e%d is already in graph %s.", input.EID, g)}, nil
		case changed:
			return KGGraphToolOutput{Content: fmt.Sprintf("e%d removed from graph %s.", input.EID, g)}, nil
		}
		return KGGraphToolOutput{Content: fmt.Sprintf("e%d was not in graph %s.", input.EID, g)}, nil
	case "list", "":
		if input.EID > 0 {
			gs, err := kg.Now().GraphsOf(ctx, input.EID)
			if err != nil {
				return KGGraphToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
			}
			if len(gs) == 0 {
				return KGGraphToolOutput{Content: fmt.Sprintf("e%d is in no named graph.", input.EID)}, nil
			}
			return KGGraphToolOutput{Content: fmt.Sprintf("e%d is in: %s", input.EID, strings.Join(gs, ", "))}, nil
		}
		gs, err := kg.Now().ListGraphs(ctx)
		if err != nil {
			return KGGraphToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		if len(gs) == 0 {
			return KGGraphToolOutput{Content: "No named graphs yet."}, nil
		}
		var sb strings.Builder
		sb.WriteString("Named graphs:\n")
		for _, g := range gs {
			fmt.Fprintf(&sb, "- %s (%d facts)\n", g.Name, g.Members)
		}
		return KGGraphToolOutput{Content: sb.String()}, nil
	}
	return KGGraphToolOutput{Content: "Error: action must be add, remove or list"}, nil
}
