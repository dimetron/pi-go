package subagent

import (
	"fmt"
	"time"
)

// AgentTypeDef defines the configuration for a subagent type.
type AgentTypeDef struct {
	Role        string   // Config role name for model resolution
	Worktree    bool     // Whether this type runs in an isolated git worktree
	Instruction string   // System instruction for the subagent
	Tools       []string // Tool names available to this type
}

// AgentTypes maps type names to their definitions.
var AgentTypes = map[string]AgentTypeDef{
	"explore": {
		Role:        "smol",
		Worktree:    false,
		Instruction: "You are an exploration agent. Quickly search and read code to answer questions about the codebase. Be concise.",
		Tools:       []string{"read", "grep", "find", "tree", "ls"},
	},
	"plan": {
		Role:        "plan",
		Worktree:    false,
		Instruction: "You are a planning agent. Analyze the codebase and create detailed implementation plans. Consider trade-offs and edge cases.",
		Tools:       []string{"read", "grep", "find", "tree", "ls", "git-overview"},
	},
	"designer": {
		Role:        "slow",
		Worktree:    true,
		Instruction: "You are a design agent. Create and modify code following established patterns. Write clean, idiomatic code.",
		Tools:       []string{"read", "write", "edit", "grep", "find", "tree", "ls", "bash"},
	},
	"reviewer": {
		Role:        "slow",
		Worktree:    false,
		Instruction: "You are a code review agent. Examine changes for bugs, style issues, and potential improvements. Be thorough but constructive.",
		Tools:       []string{"read", "grep", "find", "git-overview", "git-file-diff", "git-hunk"},
	},
	"task": {
		Role:        "default",
		Worktree:    true,
		Instruction: "You are a task execution agent. Complete the assigned coding task. Write tests, implement features, fix bugs.",
		Tools:       []string{"read", "write", "edit", "bash", "grep", "find", "tree", "ls", "git-overview"},
	},
	"quick_task": {
		Role:        "smol",
		Worktree:    false,
		Instruction: "You are a quick task agent. Complete small, focused tasks efficiently. Minimize changes.",
		Tools:       []string{"read", "write", "edit", "bash", "grep", "find"},
	},
}

// ValidateType checks if the given type name is a known agent type.
func ValidateType(typeName string) error {
	if _, ok := AgentTypes[typeName]; !ok {
		return fmt.Errorf("unknown agent type %q; valid types: explore, plan, designer, reviewer, task, quick_task", typeName)
	}
	return nil
}

// AgentInput is the input to spawn a subagent.
type AgentInput struct {
	Type       string `json:"type"`                 // Agent type name
	Prompt     string `json:"prompt"`               // Task prompt for the agent
	Worktree   *bool  `json:"worktree,omitempty"`   // Override worktree setting
	Background bool   `json:"background,omitempty"` // Run in background
}

// AgentOutput is the result of a completed subagent.
type AgentOutput struct {
	AgentID  string `json:"agent_id"`
	Type     string `json:"type"`
	Result   string `json:"result"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

// AgentStatus represents the current state of a subagent.
type AgentStatus struct {
	AgentID   string    `json:"agent_id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"` // "running", "completed", "failed", "cancelled"
	Prompt    string    `json:"prompt"`
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration,omitempty"`
}

// Event is a streaming event from a subagent process.
type Event struct {
	Type    string `json:"type"`              // "text_delta", "tool_call", "tool_result", "message_end", "error"
	Content string `json:"content,omitempty"` // Text content for text_delta
	Error   string `json:"error,omitempty"`   // Error message for error events
}
