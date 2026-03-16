package tools

import (
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/subagent"
	"google.golang.org/adk/tool"
)

// AgentToolInput defines the parameters for the agent tool.
type AgentToolInput struct {
	// The type of agent to spawn: explore, plan, designer, reviewer, task, quick_task.
	Type string `json:"type"`
	// The task prompt for the agent.
	Prompt string `json:"prompt"`
}

// AgentToolOutput is the result from a completed subagent.
type AgentToolOutput struct {
	AgentID  string `json:"agent_id"`
	Type     string `json:"type"`
	Result   string `json:"result"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

// NewAgentTool creates the agent ADK tool wired to an Orchestrator.
func NewAgentTool(orch *subagent.Orchestrator) (tool.Tool, error) {
	return newTool("agent", `Spawn a subagent to perform a task autonomously.

Available agent types:
- explore: Fast, read-only codebase exploration (smol model, no worktree)
- plan: Analyze codebase and create implementation plans (plan model, no worktree)
- designer: Create and modify code with full tools (slow model, isolated worktree)
- reviewer: Code review with git inspection tools (slow model, no worktree)
- task: Complete coding tasks with full tools (default model, isolated worktree)
- quick_task: Small, focused tasks (smol model, no worktree)

The agent runs as a separate process and returns its final result.`,
		func(ctx tool.Context, input AgentToolInput) (AgentToolOutput, error) {
			return agentHandler(ctx, orch, input)
		})
}

func agentHandler(ctx tool.Context, orch *subagent.Orchestrator, input AgentToolInput) (AgentToolOutput, error) {
	start := time.Now()

	events, agentID, err := orch.Spawn(ctx, subagent.AgentInput{
		Type:   input.Type,
		Prompt: input.Prompt,
	})
	if err != nil {
		return AgentToolOutput{
			Type:     input.Type,
			Error:    err.Error(),
			Duration: time.Since(start).Truncate(time.Millisecond).String(),
		}, nil
	}

	// Consume events, accumulate text result.
	var result strings.Builder
	for ev := range events {
		switch ev.Type {
		case "text_delta":
			result.WriteString(ev.Content)
		case "error":
			return AgentToolOutput{
				AgentID:  agentID,
				Type:     input.Type,
				Error:    ev.Error,
				Duration: time.Since(start).Truncate(time.Millisecond).String(),
			}, nil
		}
	}

	output := AgentToolOutput{
		AgentID:  agentID,
		Type:     input.Type,
		Result:   result.String(),
		Duration: time.Since(start).Truncate(time.Millisecond).String(),
	}

	// Apply truncation to the result.
	output.Result = truncateOutput(output.Result)

	return output, nil
}

// AgentTools returns tools that require an orchestrator (currently just the agent tool).
func AgentTools(orch *subagent.Orchestrator) ([]tool.Tool, error) {
	agentTool, err := NewAgentTool(orch)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{agentTool}, nil
}
