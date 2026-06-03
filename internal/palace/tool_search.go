package palace

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// SearchToolInput defines parameters for the palace-search tool.
type SearchToolInput struct {
	// The search query.
	Query string `json:"query"`
	// Optional wing filter.
	Wing string `json:"wing,omitempty"`
	// Optional room filter.
	Room string `json:"room,omitempty"`
	// Max results (default 5).
	Limit int `json:"limit,omitempty"`
}

// SearchToolOutput contains formatted search results.
type SearchToolOutput struct {
	Content string `json:"content"`
	Total   int    `json:"total"`
}

func newPalaceSearchTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-search",
		Description: "Search the memory palace for relevant knowledge. Uses semantic similarity when the embedding model is loaded, falls back to keyword search otherwise. Returns matching drawers with similarity scores.",
	}, func(ctx agent.ToolContext, input SearchToolInput) (SearchToolOutput, error) {
		return palaceSearchHandler(ctx, p, input)
	})
}

func palaceSearchHandler(ctx context.Context, p *Palace, input SearchToolInput) (SearchToolOutput, error) {
	if input.Query == "" {
		return SearchToolOutput{Content: "Error: query is required"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	results, err := p.Search(ctx, SearchQuery{
		Query: input.Query,
		Wing:  input.Wing,
		Room:  input.Room,
		Limit: limit,
	})
	if err != nil {
		return SearchToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	if len(results) == 0 {
		return SearchToolOutput{Content: "No results found."}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Search Results (%d)\n\n", len(results))
	fmt.Fprintf(&sb, "| # | Sim | Wing | Room | Content |\n|---|-----|------|------|---------|\n")

	for i, r := range results {
		content := r.Drawer.Content
		if len(content) > 120 {
			content = content[:120] + "…"
		}
		// Escape pipe characters in content for markdown table.
		content = strings.ReplaceAll(content, "|", "\\|")
		content = strings.ReplaceAll(content, "\n", " ")

		fmt.Fprintf(&sb, "| %d | %.2f | %s | %s | %s |\n",
			i+1, r.Similarity, r.Drawer.Wing, r.Drawer.Room, content)
	}

	return SearchToolOutput{Content: sb.String(), Total: len(results)}, nil
}
