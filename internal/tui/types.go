package tui

import (
	"context"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	llmmodel "google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"
)

// Config holds configuration for the TUI.
type Config struct {
	Agent          *agent.Agent
	LLM            llmmodel.LLM // The active LLM, used by /ping.
	SessionID      string
	AppVersion     string
	ModelName      string
	ProviderName   string
	ThinkingLevel  string // "none", "low", "medium", "high", "max" — drives sidebar indicator
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
	// SystemNoticeCh receives short system messages that must be shown in the
	// chat — auto-compaction outcomes, for instance. Compaction discards
	// history, so it must never happen silently.
	SystemNoticeCh <-chan string
	// ContextBreakdown attributes fixed context overhead (system prompt, tool
	// definitions, rules, skills, MCP tools, subagents) to its origins, so the
	// gauge can show what is filling the window rather than only how much.
	// Nil renders the gauge in a single severity color.
	ContextBreakdown *ContextBreakdown
	// TokenTracker tracks daily token usage and enforces limits. May be nil.
	TokenTracker TokenTracker
	// CompactMetrics tracks output compaction statistics. May be nil.
	CompactMetrics CompactStatsProvider
	// ThemeName is the configured theme name from config. Empty or "default" uses tokyo-night.
	ThemeName string
	// LifecycleHooks are shell-command hooks fired on agent lifecycle events
	// (turn_complete, user_input_required). Each carries the event name and a
	// small data payload as JSON on stdin. Nil disables lifecycle hooks.
	LifecycleHooks []extension.HookConfig

	// DeferredInit, if non-nil, is a channel of InitEvent messages.
	// When set, the TUI starts immediately in loading state and receives
	// initialization progress updates. The final event carries the fully
	// initialized subsystems in its Result field.
	DeferredInit <-chan InitEvent

	// MCPToolsets holds the live MCP toolsets, used by /mcp to show status.
	MCPToolsets []adktool.Toolset
	// MCPServers holds the configured MCP server definitions.
	MCPServers []extension.MCPServerConfig

	// ModelSwitcher creates a new LLM instance for the given model name,
	// updates the token tracker's context window size, and returns the
	// wrapped LLM, resolved model name, and provider. Used by /model <name>.
	// If nil, model switching via /model is disabled.
	ModelSwitcher func(ctx context.Context, modelName string) (llmmodel.LLM, string, string, error)
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
	Agent     *agent.Agent
	SessionID string
	// SessionTitle is the default title the agent applied to the new session
	// (git repo name, or CWD basename). The TUI seeds its terminal window/tab
	// title with it so the user sees a label before the first user prompt
	// arrives. Empty if the agent has no title namer or no CWD to derive from.
	SessionTitle string
	// Resumed reports that SessionID names an existing session rather than one
	// created for this run, so the TUI knows to rebuild its transcript from the
	// session's events instead of opening on the welcome splash.
	Resumed           bool
	SessionService    *pisession.FileService
	Orchestrator      *subagent.Orchestrator
	Logger            *logger.Logger
	Skills            []extension.Skill
	SkillDirs         []string
	GenerateCommitMsg func(context.Context, string) (string, error)
	AgentEventCh      <-chan AgentSubEvent
	SystemNoticeCh    <-chan string
	ContextBreakdown  *ContextBreakdown
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

	// SetLastPromptTokens overrides the last-prompt baseline. Compaction
	// passes call this with the post-pass token count so the context gauge
	// reflects the new window immediately rather than waiting for the next
	// LLM response. Implementations also reset the cached-prefix baseline
	// (the new window has a new prefix by definition).
	SetLastPromptTokens(n int64)

	// ResetContextWindow zeroes the per-window baselines (last prompt,
	// cached-prefix and last-cached counts) so the gauge reads empty. /clear
	// calls this after the session's events are gone. SetLastPromptTokens(0)
	// is deliberately not a substitute: it keeps a non-zero prompt count on
	// purpose, so it can never empty the gauge. Daily usage totals are not
	// affected — clearing a conversation does not un-spend tokens.
	ResetContextWindow()

	// Prompt-cache tracking. Prompt tokens are billed at a steep discount when
	// served from cache, so LastPromptTokens alone overstates cost.
	LastCachedTokens() int64    // cache reads on the most recent response
	CachedTokensToday() int64   // cache reads accumulated today
	CacheHitRateToday() float64 // share of today's prompt tokens cached, 0-100
	BodyTokens() int64          // tokens accumulated after the cached prefix
	CachePrefixTokens() int64   // stable cached prefix for this window
}

// BashEventKind namespaces a live shell-command event so it can share the
// subagent event channel without being mistaken for subagent activity.
// Producers outside this package must route bash events through it rather than
// hard-coding the prefix.
func BashEventKind(kind string) string {
	return bashEventPrefix + kind
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
