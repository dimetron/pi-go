package palace

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// TraverseToolInput defines parameters for the palace-traverse tool.
type TraverseToolInput struct {
	// The room to start BFS traversal from.
	StartRoom string `json:"start_room"`
	// Maximum hops from start room (default 2).
	MaxHops int `json:"max_hops,omitempty"`
}

// TraverseToolOutput contains formatted traversal results.
type TraverseToolOutput struct {
	Content string `json:"content"`
}

func newPalaceTraverseTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-traverse",
		Description: "Traverse the memory palace graph via BFS from a starting room. Discovers connected rooms across wings through shared topics. Useful for finding related knowledge areas you might not have searched for directly.",
	}, func(ctx agent.Context, input TraverseToolInput) (TraverseToolOutput, error) {
		return palaceTraverseHandler(ctx, p, input)
	})
}

func palaceTraverseHandler(ctx context.Context, p *Palace, input TraverseToolInput) (TraverseToolOutput, error) {
	if input.StartRoom == "" {
		return TraverseToolOutput{Content: "Error: start_room is required"}, nil
	}

	maxHops := input.MaxHops
	if maxHops <= 0 {
		maxHops = 2
	}

	results, err := p.Traverse(ctx, input.StartRoom, maxHops)
	if err != nil {
		return TraverseToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	if len(results) == 0 {
		return TraverseToolOutput{Content: fmt.Sprintf("No connected rooms found from %q.", input.StartRoom)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Traversal from %q (%d rooms)\n\n", input.StartRoom, len(results))
	fmt.Fprintf(&sb, "| Hops | Room | Wings | Drawers |\n")
	fmt.Fprintf(&sb, "|------|------|-------|---------|\n")

	for _, r := range results {
		fmt.Fprintf(&sb, "| %d | %s | %s | %d |\n",
			r.Hops, r.Room, strings.Join(r.Wings, ", "), r.DrawerCount)
	}

	return TraverseToolOutput{Content: sb.String()}, nil
}
