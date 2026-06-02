package palace

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// AddDrawerToolInput defines parameters for the palace-add-drawer tool.
type AddDrawerToolInput struct {
	// Wing name (project or domain).
	Wing string `json:"wing"`
	// Room name (topic or subdirectory).
	Room string `json:"room"`
	// The knowledge content to store.
	Content string `json:"content"`
	// Optional source file path.
	SourceFile string `json:"source_file,omitempty"`
	// Optional hall (sub-category).
	Hall string `json:"hall,omitempty"`
	// Importance score 0-10 (default 5).
	Importance int `json:"importance,omitempty"`
}

// AddDrawerToolOutput contains the result of adding a drawer.
type AddDrawerToolOutput struct {
	Content string `json:"content"`
	ID      string `json:"id,omitempty"`
}

func newPalaceAddDrawerTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-add-drawer",
		Description: "Add a new knowledge drawer to the memory palace. Requires wing (project/domain), room (topic), and content. Automatically embeds the content for semantic search and checks for duplicates.",
	}, func(ctx agent.ToolContext, input AddDrawerToolInput) (AddDrawerToolOutput, error) {
		return palaceAddDrawerHandler(ctx, p, input)
	})
}

func palaceAddDrawerHandler(ctx context.Context, p *Palace, input AddDrawerToolInput) (AddDrawerToolOutput, error) {
	if input.Wing == "" {
		return AddDrawerToolOutput{Content: "Error: wing is required"}, nil
	}
	if input.Room == "" {
		return AddDrawerToolOutput{Content: "Error: room is required"}, nil
	}
	if input.Content == "" {
		return AddDrawerToolOutput{Content: "Error: content is required"}, nil
	}

	importance := input.Importance
	if importance <= 0 {
		importance = 5
	}

	drawer, err := p.AddDrawer(ctx, DrawerInput{
		Wing:       input.Wing,
		Room:       input.Room,
		Hall:       input.Hall,
		Content:    input.Content,
		SourceFile: input.SourceFile,
		AddedBy:    "agent",
		Importance: importance,
	})
	if err != nil {
		if dupErr, ok := errors.AsType[*DuplicateError](err); ok {
			return AddDrawerToolOutput{
				Content: fmt.Sprintf("Duplicate detected (similarity %.2f with drawer %s). Content not added.",
					dupErr.Result.Similarity, dupErr.Result.ExistingID),
			}, nil
		}
		return AddDrawerToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	return AddDrawerToolOutput{
		Content: fmt.Sprintf("Drawer added: %s (wing=%s, room=%s)", drawer.ID, drawer.Wing, drawer.Room),
		ID:      drawer.ID,
	}, nil
}
