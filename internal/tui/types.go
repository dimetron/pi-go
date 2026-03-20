package tui

import (
	"context"
	"sync"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	llmmodel "google.golang.org/adk/model"
)

// Config holds configuration for the TUI.
type Config struct {
	Agent          *agent.Agent
	LLM            llmmodel.LLM // The active LLM, used by /ping.
	SessionID      string
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
	// Screen receives screen content updates for the screen tool.
	// If nil, the screen tool won't have access to TUI content.
	Screen *Screen
	// Skills is loaded from skill directories for command completion.
	Skills []extension.Skill
	// SkillDirs are the directories to re-scan for skills on each completion.
	SkillDirs []string
	// RestartCh receives a signal when the agent calls the restart tool.
	RestartCh chan struct{}
	// AgentEventCh receives subagent events from the agent tool for live display.
	AgentEventCh <-chan AgentSubEvent
	// TokenTracker tracks daily token usage and enforces limits. May be nil.
	TokenTracker TokenTracker
	// CompactMetrics tracks output compaction statistics. May be nil.
	CompactMetrics CompactStatsProvider
	// ThemeName is the configured theme name from config. Empty or "default" uses tokyo-night.
	ThemeName string
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

// Screen provides thread-safe access to the current TUI screen content.
// It implements tools.ScreenProvider so the LLM can read what the user sees.
type Screen struct {
	mu      sync.Mutex
	content string
}

// ScreenContent returns the current screen content.
func (s *Screen) ScreenContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.content
}

func (s *Screen) update(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content = content
}
