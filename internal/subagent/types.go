package subagent

import (
	"fmt"
	"time"
)

// SpawnInput is the input to spawn a subagent with an AgentConfig.
type SpawnInput struct {
	Agent       AgentConfig `json:"agent"`                  // Agent configuration
	Prompt      string      `json:"prompt"`                 // Task prompt for the agent
	Worktree    *bool       `json:"worktree,omitempty"`     // Override worktree setting
	WorkDir     string      `json:"work_dir,omitempty"`     // Override working directory (e.g. existing worktree path)
	Background  bool        `json:"background,omitempty"`   // Run in background
	SkipCleanup bool        `json:"skip_cleanup,omitempty"` // Don't auto-cleanup worktree on completion
	Env         []string    `json:"env,omitempty"`          // Additional environment variables
}

// AgentInput is the legacy input to spawn a subagent (deprecated, use SpawnInput).
// It is kept for backward compatibility with existing callers.
// Convert to SpawnInput using ToSpawnInput() which looks up the bundled agent.
type AgentInput struct {
	Type        string `json:"type"`                   // Agent type name
	Prompt      string `json:"prompt"`                 // Task prompt for the agent
	Worktree    *bool  `json:"worktree,omitempty"`     // Override worktree setting
	WorkDir     string `json:"work_dir,omitempty"`     // Override working directory (e.g. existing worktree path)
	Background  bool   `json:"background,omitempty"`   // Run in background
	SkipCleanup bool   `json:"skip_cleanup,omitempty"` // Don't auto-cleanup worktree on completion
}

// ToSpawnInput converts a legacy AgentInput to the new SpawnInput format.
// It looks up the agent config from bundled agents.
func (a AgentInput) ToSpawnInput() (SpawnInput, error) {
	// Look up the agent from bundled agents
	bundled, err := LoadBundledAgents()
	if err != nil {
		return SpawnInput{}, fmt.Errorf("loading bundled agents: %w", err)
	}

	var agent AgentConfig
	found := false
	for _, cfg := range bundled {
		if cfg.Name == a.Type {
			agent = cfg
			found = true
			break
		}
	}
	if !found {
		return SpawnInput{}, fmt.Errorf("unknown agent type %q; valid types: explore, plan, designer, task, quick-task, worker, code-reviewer, spec-reviewer, memory-compressor", a.Type)
	}

	return SpawnInput{
		Agent:       agent,
		Prompt:      a.Prompt,
		Worktree:    a.Worktree,
		WorkDir:     a.WorkDir,
		Background:  a.Background,
		SkipCleanup: a.SkipCleanup,
	}, nil
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
	Status    string    `json:"status"` // "running", "completed", "failed", "canceled"
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
