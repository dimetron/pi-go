package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/memory"
	"google.golang.org/adk/tool"
)

// MemSearchInput defines parameters for the mem-search tool.
type MemSearchInput struct {
	// Full-text search query.
	Query string `json:"query"`
	// Optional project scope.
	Project string `json:"project,omitempty"`
	// Optional observation type filter (bugfix, feature, discovery, etc.).
	Type string `json:"type,omitempty"`
	// Max results to return (default 20).
	Limit int `json:"limit,omitempty"`
}

// MemSearchOutput contains search results.
type MemSearchOutput struct {
	// Markdown-formatted results table.
	Content string `json:"content"`
	// Total number of matching observations.
	Total int `json:"total"`
}

func newMemSearchTool(store memory.Store) (tool.Tool, error) {
	return newTool("mem-search",
		"Search past observations using full-text search. Returns a compact table with IDs, titles, and types. Use mem-get to fetch full details for specific IDs.",
		func(ctx tool.Context, input MemSearchInput) (MemSearchOutput, error) {
			return memSearchHandler(ctx, store, input)
		})
}

func memSearchHandler(ctx context.Context, store memory.Store, input MemSearchInput) (MemSearchOutput, error) {
	if input.Query == "" {
		return MemSearchOutput{Content: "Error: query is required"}, nil
	}

	q := memory.SearchQuery{
		Query:   input.Query,
		Project: input.Project,
		Type:    memory.ObservationType(input.Type),
		Limit:   input.Limit,
	}

	result, err := store.Search(ctx, q)
	if err != nil {
		return MemSearchOutput{}, fmt.Errorf("mem-search: %w", err)
	}

	if len(result.Rows) == 0 {
		return MemSearchOutput{Content: "No observations found.", Total: 0}, nil
	}

	content := formatSearchResults(result)
	return MemSearchOutput{Content: content, Total: result.Total}, nil
}

func formatSearchResults(result *memory.SearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d observations:\n\n", result.Total)
	b.WriteString("| ID | Type | Title | Time | Read |\n")
	b.WriteString("|----|------|-------|------|------|\n")
	for _, row := range result.Rows {
		b.WriteString(fmt.Sprintf("| #%d | %s | %s | %s | %dt |\n",
			row.ID, row.Type, row.Title,
			row.CreatedAt.Format("Jan 2 15:04"),
			row.ReadCost))
	}
	b.WriteString("\nUse `mem-get` with IDs to fetch full details.")
	return b.String()
}

// MemTimelineInput defines parameters for the mem-timeline tool.
type MemTimelineInput struct {
	// Anchor observation ID to center the timeline on.
	Anchor int64 `json:"anchor"`
	// Number of observations to show before the anchor (default 5).
	DepthBefore int `json:"depth_before,omitempty"`
	// Number of observations to show after the anchor (default 5).
	DepthAfter int `json:"depth_after,omitempty"`
}

// MemTimelineOutput contains timeline results.
type MemTimelineOutput struct {
	// Markdown-formatted timeline.
	Content string `json:"content"`
	// Number of observations returned.
	Count int `json:"count"`
}

func newMemTimelineTool(store memory.Store) (tool.Tool, error) {
	return newTool("mem-timeline",
		"Show observations around an anchor observation in chronological order. Useful for understanding the context of a specific observation.",
		func(ctx tool.Context, input MemTimelineInput) (MemTimelineOutput, error) {
			return memTimelineHandler(ctx, store, input)
		})
}

func memTimelineHandler(ctx context.Context, store memory.Store, input MemTimelineInput) (MemTimelineOutput, error) {
	if input.Anchor <= 0 {
		return MemTimelineOutput{Content: "Error: anchor ID is required"}, nil
	}

	obs, err := store.Timeline(ctx, input.Anchor, input.DepthBefore, input.DepthAfter)
	if err != nil {
		return MemTimelineOutput{}, fmt.Errorf("mem-timeline: %w", err)
	}

	if len(obs) == 0 {
		return MemTimelineOutput{Content: "No observations found."}, nil
	}

	content := formatTimeline(obs, input.Anchor)
	return MemTimelineOutput{Content: content, Count: len(obs)}, nil
}

func formatTimeline(obs []*memory.Observation, anchorID int64) string {
	var b strings.Builder
	b.WriteString("| ID | Type | Title | Time | Files |\n")
	b.WriteString("|----|------|-------|------|-------|\n")
	for _, o := range obs {
		marker := ""
		if o.ID == anchorID {
			marker = " **>>>**"
		}
		files := ""
		if len(o.SourceFiles) > 0 {
			files = strings.Join(o.SourceFiles, ", ")
		}
		b.WriteString(fmt.Sprintf("| #%d | %s | %s%s | %s | %s |\n",
			o.ID, o.Type, o.Title, marker,
			o.CreatedAt.Format("15:04:05"),
			files))
	}
	return b.String()
}

// MemGetInput defines parameters for the mem-get tool.
type MemGetInput struct {
	// Observation IDs to fetch.
	IDs []int64 `json:"ids"`
}

// MemGetOutput contains full observation details.
type MemGetOutput struct {
	// Markdown-formatted observation details.
	Content string `json:"content"`
	// Number of observations returned.
	Count int `json:"count"`
}

func newMemGetTool(store memory.Store) (tool.Tool, error) {
	return newTool("mem-get",
		"Fetch full details for specific observations by ID. Use after mem-search or mem-timeline to get complete observation text.",
		func(ctx tool.Context, input MemGetInput) (MemGetOutput, error) {
			return memGetHandler(ctx, store, input)
		})
}

func memGetHandler(ctx context.Context, store memory.Store, input MemGetInput) (MemGetOutput, error) {
	if len(input.IDs) == 0 {
		return MemGetOutput{Content: "Error: at least one ID is required"}, nil
	}

	obs, err := store.GetObservations(ctx, input.IDs)
	if err != nil {
		return MemGetOutput{}, fmt.Errorf("mem-get: %w", err)
	}

	if len(obs) == 0 {
		return MemGetOutput{Content: "No observations found for the given IDs."}, nil
	}

	content := formatObservations(obs)
	return MemGetOutput{Content: content, Count: len(obs)}, nil
}

func formatObservations(obs []*memory.Observation) string {
	var b strings.Builder
	for i, o := range obs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&b, "### #%d %s (%s)\n", o.ID, o.Title, o.Type)
		fmt.Fprintf(&b, "**Time:** %s\n", o.CreatedAt.Format(time.RFC3339))
		if len(o.SourceFiles) > 0 {
			fmt.Fprintf(&b, "**Files:** %s\n", strings.Join(o.SourceFiles, ", "))
		}
		if o.ToolName != "" {
			fmt.Fprintf(&b, "**Tool:** %s\n", o.ToolName)
		}
		fmt.Fprintf(&b, "\n%s\n", o.Text)
	}
	return b.String()
}

// MemoryTools returns memory search tools that operate against the given store.
// Returns nil if store is nil (memory disabled).
func MemoryTools(store memory.Store) ([]tool.Tool, error) {
	if store == nil {
		return nil, nil
	}
	builders := []func(memory.Store) (tool.Tool, error){
		newMemSearchTool,
		newMemTimelineTool,
		newMemGetTool,
	}
	tools := make([]tool.Tool, 0, len(builders))
	for _, b := range builders {
		t, err := b(store)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}
