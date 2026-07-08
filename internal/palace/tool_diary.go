package palace

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-diary-write ---

// DiaryWriteToolInput defines parameters for the palace-diary-write tool.
type DiaryWriteToolInput struct {
	// The agent name writing the entry.
	AgentName string `json:"agent_name"`
	// The diary entry content.
	Entry string `json:"entry"`
	// Optional topic/category for the entry.
	Topic string `json:"topic,omitempty"`
}

// DiaryWriteToolOutput contains the result of writing a diary entry.
type DiaryWriteToolOutput struct {
	Content string `json:"content"`
}

func newPalaceDiaryWriteTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-diary-write",
		Description: "Write a diary entry to the memory palace. Each agent maintains its own diary. Diary entries are stored for later retrieval and also filed as searchable drawers in hall_diary.",
	}, func(ctx agent.Context, input DiaryWriteToolInput) (DiaryWriteToolOutput, error) {
		return palaceDiaryWriteHandler(ctx, p, input)
	})
}

func palaceDiaryWriteHandler(ctx context.Context, p *Palace, input DiaryWriteToolInput) (DiaryWriteToolOutput, error) {
	if input.AgentName == "" {
		return DiaryWriteToolOutput{Content: "Error: agent_name is required"}, nil
	}
	if input.Entry == "" {
		return DiaryWriteToolOutput{Content: "Error: entry is required"}, nil
	}

	// Write the diary entry.
	err := p.DiaryWrite(ctx, input.AgentName, input.Entry, input.Topic)
	if err != nil {
		return DiaryWriteToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	// Also store as a drawer in hall_diary for searchability.
	wing := "agent_" + input.AgentName
	room := input.Topic
	if room == "" {
		room = "diary"
	}
	_, drawerErr := p.AddDrawer(ctx, DrawerInput{
		Wing:       wing,
		Room:       room,
		Hall:       "hall_diary",
		Content:    input.Entry,
		AddedBy:    input.AgentName,
		Importance: 3,
	})
	if drawerErr != nil {
		// Drawer filing is best-effort; diary entry is already stored.
		_ = drawerErr
	}

	topicMsg := ""
	if input.Topic != "" {
		topicMsg = fmt.Sprintf(" (topic: %s)", input.Topic)
	}
	return DiaryWriteToolOutput{
		Content: fmt.Sprintf("Diary entry written for agent %q%s.", input.AgentName, topicMsg),
	}, nil
}

// --- palace-diary-read ---

// DiaryReadToolInput defines parameters for the palace-diary-read tool.
type DiaryReadToolInput struct {
	// The agent name whose diary to read.
	AgentName string `json:"agent_name"`
	// Number of recent entries to return (default 10).
	LastN int `json:"last_n,omitempty"`
}

// DiaryReadToolOutput contains formatted diary entries.
type DiaryReadToolOutput struct {
	Content string `json:"content"`
}

func newPalaceDiaryReadTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-diary-read",
		Description: "Read recent diary entries for an agent. Returns entries in reverse chronological order. Use to review past reflections, session notes, and ongoing thoughts.",
	}, func(ctx agent.Context, input DiaryReadToolInput) (DiaryReadToolOutput, error) {
		return palaceDiaryReadHandler(ctx, p, input)
	})
}

func palaceDiaryReadHandler(ctx context.Context, p *Palace, input DiaryReadToolInput) (DiaryReadToolOutput, error) {
	if input.AgentName == "" {
		return DiaryReadToolOutput{Content: "Error: agent_name is required"}, nil
	}

	lastN := input.LastN
	if lastN <= 0 {
		lastN = 10
	}

	entries, err := p.DiaryRead(ctx, input.AgentName, lastN)
	if err != nil {
		return DiaryReadToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	if len(entries) == 0 {
		return DiaryReadToolOutput{Content: fmt.Sprintf("No diary entries for agent %q.", input.AgentName)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Diary for %q (%d entries)\n\n", input.AgentName, len(entries))

	for _, e := range entries {
		date := e.CreatedAt.Format("2006-01-02 15:04")
		topicTag := ""
		if e.Topic != "" {
			topicTag = fmt.Sprintf(" [%s]", e.Topic)
		}
		fmt.Fprintf(&sb, "### %s%s\n%s\n\n", date, topicTag, e.Entry)
	}

	return DiaryReadToolOutput{Content: sb.String()}, nil
}
