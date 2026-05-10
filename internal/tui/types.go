package tui

import (
	"context"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	llmmodel "google.golang.org/adk/model"
	adktool "google.golang.org/adk/tool"
)

// Config holds configuration for the TUI.
type Config struct {
	Agent          *agent.Agent
	LLM            llmmodel.LLM // The active LLM, used by /ping.
	SessionID      string
	AppVersion     string
	ModelName      string
	ProviderName   string
	ActiveRole     string
	Roles          map[string]config.RoleConfig
	SessionService *pisession.FileService
	WorkDir        string
	Orchestrator   *subagent.Orchestrator
	// GenerateCommitMsg is called by /commit to generate a conventional commit message from diffs.
	// If nil, /commit is disabled.
	GenerateCommitMsg func(ctx context.Context, diffs string) (string, error)
	// Logger is the session logger. If nil, logging is disabled.
	Logger *logger.Logger
	// Skills is loaded from skill directories for command completion.
	Skills []extension.Skill
	// SkillDirs are the directories to re-scan for skills on each completion.
	SkillDirs []string
	// AgentEventCh receives subagent events from the agent tool for live display.
	AgentEventCh <-chan AgentSubEvent
	// TokenTracker tracks daily token usage and enforces limits. May be nil.
	TokenTracker TokenTracker
	// CompactMetrics tracks output compaction statistics. May be nil.
	CompactMetrics CompactStatsProvider
	// ThemeName is the configured theme name from config. Empty or "default" uses tokyo-night.
	ThemeName string

	// DeferredInit, if non-nil, is a channel of InitEvent messages.
	// When set, the TUI starts immediately in loading state and receives
	// initialization progress updates. The final event carries the fully
	// initialized subsystems in its Result field.
	DeferredInit <-chan InitEvent

	// MCPToolsets holds the live MCP toolsets, used by /mcp to show status.
	MCPToolsets []adktool.Toolset
	// MCPServers holds the configured MCP server definitions.
	MCPServers []extension.MCPServerConfig
}

// InitEvent reports progress from deferred initialization.
type InitEvent struct {
	Item   string      // subsystem name (e.g. "lsp", "memory", "mcp")
	Done   bool        // true when this item finished loading
	Total  int         // planned subsystem count when known; 0 means derive from seen items
	Result *InitResult // set on the final event when all init is complete
	Err    error       // fatal initialization error
}

// InitResult holds the fully initialized subsystems delivered by deferred init.
type InitResult struct {
	Agent             *agent.Agent
	SessionID         string
	SessionService    *pisession.FileService
	Orchestrator      *subagent.Orchestrator
	Logger            *logger.Logger
	Skills            []extension.Skill
	SkillDirs         []string
	GenerateCommitMsg func(context.Context, string) (string, error)
	AgentEventCh      <-chan AgentSubEvent
	TokenTracker      TokenTracker
	CompactMetrics    CompactStatsProvider
	GitBranch         string
	DiffAdded         int
	DiffRemoved       int
	// MCPToolsets holds the live MCP toolsets for /mcp status display.
	MCPToolsets []adktool.Toolset
	// MCPServers holds the configured MCP server definitions.
	MCPServers []extension.MCPServerConfig
}

// CompactStatsProvider provides compaction statistics for TUI display.
type CompactStatsProvider interface {
	FormatStats() string
}

// TokenTracker provides read access to daily token usage for the status bar.
type TokenTracker interface {
	Limit() int64
	Remaining() int64     // -1 if unlimited
	PercentUsed() float64 // 0-100+
	TotalUsed() int64     // total tokens consumed today

	// Session context window tracking.
	LastPromptTokens() int64     // most recent prompt tokens from LLM response
	ContextWindowSize() int64    // model's context window size (0 = unknown)
	ContextPercentUsed() float64 // context window usage 0-100+
}

// AgentSubEvent carries a subagent event from the agent tool to the TUI.
type AgentSubEvent struct {
	AgentID    string
	Kind       string // "tool_call", "tool_result", "text_delta", etc.
	Content    string
	PipelineID string // groups agents in same call
	Mode       string // "single", "parallel", "chain"
	Step       int    // 1-based position in pipeline
	Total      int    // total agents in pipeline
}
