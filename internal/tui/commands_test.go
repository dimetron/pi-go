package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"charm.land/lipgloss/v2"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"
)

// --- handleCompactCommand tests ---

func TestHandleCompactCommand_NilSessionService(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{SessionService: nil},
	}

	m.handleCompactCommand()

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	msg := m.chatModel.Messages[0]
	if msg.role != "assistant" {
		t.Errorf("expected assistant role, got %q", msg.role)
	}
	if !strings.Contains(msg.content, "not available") {
		t.Errorf("expected 'not available' message, got %q", msg.content)
	}
}

// A successful /compact must push the post-compaction token count into the
// configured TokenTracker so the bottom gauge updates on the very next
// render. Without this push the gauge would stay stuck on the pre-compaction
// reading until the next LLM response arrives.
func TestHandleCompactCommand_UpdatesGaugeTracker(t *testing.T) {
	tracker := &cmdMockTokenTracker{}
	svc, id := newCompactTestSession(t)

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      id,
			TokenTracker:   tracker,
		},
	}

	m.handleCompactCommand()

	if tracker.setCount == 0 {
		t.Fatalf("expected SetLastPromptTokens to be called at least once, got 0 calls")
	}
	if tracker.lastPrompt <= 0 {
		t.Errorf("expected a positive post-compaction prompt count, got %d", tracker.lastPrompt)
	}
	if got := tracker.LastPromptTokens(); got <= 0 {
		t.Errorf("tracker.LastPromptTokens() = %d, want > 0", got)
	}
}

// --- /clear context reset tests ---

// /clear must reset what the model sees, not just what the user sees: the
// session's events go away and the context gauge is zeroed. Clearing only the
// transcript would leave the user paying for every prior turn while the screen
// claims the conversation is gone.
func TestSlashClear_ClearsSessionEventsAndGauge(t *testing.T) {
	svc, id := newCompactTestSession(t)
	tracker := &cmdMockTokenTracker{lastPrompt: 12345}

	m := newTestModel(t)
	m.cfg.SessionService = svc
	m.cfg.SessionID = id
	m.cfg.TokenTracker = tracker
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: "hi"})

	m.handleSlashCommand("/clear")

	if len(m.chatModel.Messages) != 0 {
		t.Errorf("expected transcript cleared, got %d messages: %+v",
			len(m.chatModel.Messages), m.chatModel.Messages)
	}
	if tracker.resetCount != 1 {
		t.Errorf("ResetContextWindow calls = %d, want 1", tracker.resetCount)
	}
	if got := tracker.LastPromptTokens(); got != 0 {
		t.Errorf("tracker.LastPromptTokens() = %d, want 0 (gauge must read empty)", got)
	}

	est, err := svc.EstimateTokens(id, agent.AppName, agent.DefaultUserID)
	if err != nil {
		t.Fatalf("EstimateTokens: %v", err)
	}
	if est != 0 {
		t.Errorf("session still holds ~%d tokens of events after /clear, want 0", est)
	}
}

// If the events survive, the gauge must too: zeroing it would under-report a
// still-full window, which is a worse lie than a stale reading.
func TestClearSessionContext_KeepsGaugeWhenClearFails(t *testing.T) {
	svc, _ := newCompactTestSession(t)
	tracker := &cmdMockTokenTracker{lastPrompt: 999}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "no-such-session",
			TokenTracker:   tracker,
		},
	}

	m.clearSessionContext()

	if tracker.resetCount != 0 {
		t.Errorf("ResetContextWindow called %d times on a failed clear, want 0", tracker.resetCount)
	}
	if got := tracker.LastPromptTokens(); got != 999 {
		t.Errorf("tracker.LastPromptTokens() = %d, want 999 (unchanged)", got)
	}
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected the failure to be surfaced, got %d messages", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not cleared") {
		t.Errorf("expected a 'not cleared' notice, got %q", m.chatModel.Messages[0].content)
	}
}

// Both dependencies are optional; neither may panic when absent.
func TestClearSessionContext_NilDependencies(t *testing.T) {
	t.Run("no session service still zeroes the gauge", func(t *testing.T) {
		tracker := &cmdMockTokenTracker{lastPrompt: 42}
		m := &model{
			chatModel: ChatModel{Messages: make([]message, 0)},
			cfg:       Config{TokenTracker: tracker},
		}

		m.clearSessionContext()

		if tracker.resetCount != 1 {
			t.Errorf("ResetContextWindow calls = %d, want 1", tracker.resetCount)
		}
	})

	t.Run("no token tracker still clears the events", func(t *testing.T) {
		svc, id := newCompactTestSession(t)
		m := &model{
			chatModel: ChatModel{Messages: make([]message, 0)},
			cfg:       Config{SessionService: svc, SessionID: id},
		}

		m.clearSessionContext()

		est, err := svc.EstimateTokens(id, agent.AppName, agent.DefaultUserID)
		if err != nil {
			t.Fatalf("EstimateTokens: %v", err)
		}
		if est != 0 {
			t.Errorf("events survived /clear: ~%d tokens", est)
		}
		if len(m.chatModel.Messages) != 0 {
			t.Errorf("expected no notice on success, got %+v", m.chatModel.Messages)
		}
	})
}

// --- handleThemeCommand tests ---

func TestHandleThemeCommand_NilThemeManager(t *testing.T) {
	m := &model{
		chatModel:    ChatModel{Messages: make([]message, 0)},
		themeManager: nil,
	}

	newM, cmd := m.handleThemeCommand(nil)
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleThemeCommand_ListThemes(t *testing.T) {
	// Bare /theme opens the picker rather than appending a rendered list. The
	// list still has to name every theme and keep the light/dark icons, so this
	// asserts the same properties against the popup rows that the message used
	// to carry.
	tm := NewThemeManager()
	m := &model{
		chatModel:    ChatModel{Messages: make([]message, 0)},
		themeManager: tm,
		width:        100,
		height:       30,
	}

	newM, cmd := m.handleThemeCommand(nil)
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if mm.searchPopup == nil {
		t.Fatal("bare /theme did not open the theme picker")
	}
	if mm.searchPopup.mode != searchModeThemes {
		t.Errorf("popup mode = %q, want %q", mm.searchPopup.mode, searchModeThemes)
	}
	if len(mm.chatModel.Messages) != 0 {
		t.Errorf("bare /theme appended %d messages, want the picker only", len(mm.chatModel.Messages))
	}

	var sawName, sawMoon, sawSun bool
	for _, item := range mm.searchPopup.entries {
		if item.Text == "tokyo-night" {
			sawName = true
		}
		switch item.Icon {
		case "🌙":
			sawMoon = true
		case "☀️":
			sawSun = true
		}
	}
	if !sawName {
		t.Error("expected tokyo-night among the picker rows")
	}
	if !sawMoon || !sawSun {
		t.Errorf("expected light and dark icons in the picker, got moon=%v sun=%v", sawMoon, sawSun)
	}
}

func TestHandleThemeCommand_SwitchInvalidTheme(t *testing.T) {
	tm := NewThemeManager()
	m := &model{
		chatModel:    ChatModel{Messages: make([]message, 0)},
		themeManager: tm,
	}

	newM, cmd := m.handleThemeCommand([]string{"nonexistent-theme-xyz"})
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Unknown theme") {
		t.Errorf("expected 'Unknown theme' error, got %q", content)
	}
}

func TestHandleThemeCommand_SwitchValidTheme(t *testing.T) {
	tm := NewThemeManager()
	m := &model{
		chatModel:    ChatModel{Messages: make([]message, 0)},
		themeManager: tm,
	}

	newM, cmd := m.handleThemeCommand([]string{"dracula"})
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Theme switched to") {
		t.Errorf("expected 'Theme switched to' message, got %q", content)
	}
	if !strings.Contains(content, "dracula") {
		t.Errorf("expected theme name 'dracula' in message, got %q", content)
	}
	if tm.CurrentName() != "dracula" {
		t.Errorf("expected current theme to be 'dracula', got %q", tm.CurrentName())
	}
}

// --- handleBranchCommand tests ---

func TestHandleBranchCommand_NilSessionService(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{SessionService: nil},
	}

	m.handleBranchCommand([]string{"list"})

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_NoArgs(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{SessionService: nil},
	}

	// With nil service, any args trigger the nil-service guard first.
	// To test the no-args path, we need a non-nil service, but that is complex.
	// Instead, we verify via handleSlashCommand routing that no args shows usage.
	// Reset with a fresh model that has a non-nil pointer check path.
	// Actually, let's test with nil service and no args:
	// nil check comes first, so it returns "not available".
	m.handleBranchCommand(nil)
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected not-available message for nil service, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_SwitchNilService(t *testing.T) {
	// nil service guard fires before we can get to the switch-no-name path.
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{SessionService: nil},
	}

	m.handleBranchCommand([]string{"switch"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected not-available for nil service, got %q", m.chatModel.Messages[0].content)
	}
}

// --- handleSkillsCommand tests ---

func TestHandleSkillsCommand_UnknownSubcommand(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.handleSkillsCommand([]string{"bogus"})
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd for unknown subcommand")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Usage:") {
		t.Errorf("expected usage message, got %q", content)
	}
}

func TestHandleSkillsCommand_NoArgs_EmptySkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{Skills: nil},
	}

	newM, cmd := m.handleSkillsCommand(nil)
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "No skills loaded") {
		t.Errorf("expected 'No skills loaded' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSkillsCommand_NoArgs_WithSkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Skills: []extension.Skill{
				{Name: "test-skill", Description: "A test skill"},
			},
		},
	}

	newM, cmd := m.handleSkillsCommand(nil)
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "test-skill") {
		t.Errorf("expected skill name in list, got %q", content)
	}
	if !strings.Contains(content, "A test skill") {
		t.Errorf("expected skill description in list, got %q", content)
	}
}

func TestHandleSkillsCommand_ListSubcommand(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Skills: []extension.Skill{
				{Name: "my-skill", Description: "desc"},
			},
		},
	}

	newM, _ := m.handleSkillsCommand([]string{"list"})
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "my-skill") {
		t.Errorf("expected skill name, got %q", mm.chatModel.Messages[0].content)
	}
}

// --- formatContextUsage tests ---

func TestCommandFormatContextUsage_EmptyMessages(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{ModelName: "test-model"},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "Context Usage") {
		t.Error("expected 'Context Usage' header")
	}
	if !strings.Contains(result, "test-model") {
		t.Errorf("expected model name in output, got %q", result)
	}
	if !strings.Contains(result, "0 msgs") {
		t.Errorf("expected '0 msgs' for empty user messages, got %q", result)
	}
}

func TestCommandFormatContextUsage_WithMessages(t *testing.T) {
	m := &model{
		chatModel: ChatModel{
			Messages: []message{
				{role: "user", content: "Hello world"},
				{role: "assistant", content: "Hi there, how can I help?"},
				{role: "tool", content: "result", tool: "bash", toolIn: "ls"},
				{role: "user", content: "Another question"},
			},
		},
		cfg: Config{ModelName: "gpt-4"},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "2 msgs") {
		t.Errorf("expected '2 msgs' for user messages, got %q", result)
	}
	if !strings.Contains(result, "1 msgs") {
		t.Errorf("expected '1 msgs' for assistant messages, got %q", result)
	}
	if !strings.Contains(result, "1 calls") {
		t.Errorf("expected '1 calls' for tool messages, got %q", result)
	}
	if !strings.Contains(result, "4 messages") {
		t.Errorf("expected '4 messages' total, got %q", result)
	}
}

func TestCommandFormatContextUsage_WithProviderName(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:    "claude-sonnet",
			ProviderName: "anthropic",
		},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "anthropic") {
		t.Errorf("expected provider name in output, got %q", result)
	}
	if !strings.Contains(result, "claude-sonnet") {
		t.Errorf("expected model name in output, got %q", result)
	}
}

// cmdMockTokenTracker implements TokenTracker for testing.
type cmdMockTokenTracker struct {
	totalUsed   int64
	limit       int64
	remaining   int64
	percentUsed float64
	lastPrompt  int64
	setCount    int
	resetCount  int
}

func (m *cmdMockTokenTracker) TotalUsed() int64        { return m.totalUsed }
func (m *cmdMockTokenTracker) Limit() int64            { return m.limit }
func (m *cmdMockTokenTracker) Remaining() int64        { return m.remaining }
func (m *cmdMockTokenTracker) PercentUsed() float64    { return m.percentUsed }
func (m *cmdMockTokenTracker) LastPromptTokens() int64 { return m.lastPrompt }
func (m *cmdMockTokenTracker) SetLastPromptTokens(n int64) {
	m.lastPrompt, m.setCount = n, m.setCount+1
}
func (m *cmdMockTokenTracker) ResetContextWindow() {
	m.lastPrompt, m.resetCount = 0, m.resetCount+1
}
func (m *cmdMockTokenTracker) ContextWindowSize() int64    { return 0 }
func (m *cmdMockTokenTracker) ContextPercentUsed() float64 { return 0 }
func (m *cmdMockTokenTracker) LastCachedTokens() int64     { return 0 }
func (m *cmdMockTokenTracker) CachedTokensToday() int64    { return 0 }
func (m *cmdMockTokenTracker) CacheHitRateToday() float64  { return 0 }
func (m *cmdMockTokenTracker) BodyTokens() int64           { return 0 }
func (m *cmdMockTokenTracker) CachePrefixTokens() int64    { return 0 }

func TestCommandFormatContextUsage_WithTokenTracker(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName: "test-model",
			TokenTracker: &cmdMockTokenTracker{
				totalUsed:   5000,
				limit:       100000,
				remaining:   95000,
				percentUsed: 5.0,
			},
		},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "Daily token usage") {
		t.Errorf("expected 'Daily token usage' section, got %q", result)
	}
	if !strings.Contains(result, "Consumed today") {
		t.Errorf("expected 'Consumed today' in output, got %q", result)
	}
	if !strings.Contains(result, "Remaining") {
		t.Errorf("expected 'Remaining' in output, got %q", result)
	}
}

func TestCommandFormatContextUsage_WithTokenTrackerNoLimit(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName: "test-model",
			TokenTracker: &cmdMockTokenTracker{
				totalUsed:   1000,
				limit:       0, // no limit
				remaining:   -1,
				percentUsed: 0,
			},
		},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "Consumed today") {
		t.Errorf("expected 'Consumed today' in output, got %q", result)
	}
	// Should not contain "Remaining" when limit is 0.
	if strings.Contains(result, "Remaining") {
		t.Errorf("should not show 'Remaining' when no limit, got %q", result)
	}
}

// mockCompactStats implements CompactStatsProvider for testing.
type mockCompactStats struct {
	stats string
}

func (m *mockCompactStats) FormatStats() string { return m.stats }

func TestCommandFormatContextUsage_WithCompactMetrics(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:      "test-model",
			CompactMetrics: &mockCompactStats{stats: "Saved 42% tokens"},
		},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "Output compaction") {
		t.Errorf("expected 'Output compaction' section, got %q", result)
	}
	if !strings.Contains(result, "Saved 42%% tokens") {
		// The stats string is embedded directly.
		if !strings.Contains(result, "Saved 42% tokens") {
			t.Errorf("expected compact stats in output, got %q", result)
		}
	}
}

// --- Helper function tests ---

func TestCommandFormatThemeList(t *testing.T) {
	themes := []Theme{
		{Name: "dark-one", DisplayName: "Dark One", ThemeType: "dark"},
		{Name: "light-one", DisplayName: "Light One", ThemeType: "light"},
	}

	result := formatThemeList(themes, "dark-one", darkPalette)

	if !strings.Contains(result, "dark-one") || !strings.Contains(result, "light-one") {
		t.Error("expected every theme name in the list")
	}
	if lines := strings.Count(result, "\n") + 1; lines != len(themes) {
		t.Errorf("expected one row per theme, got %d rows for %d themes", lines, len(themes))
	}
	if !strings.Contains(result, swatchGlyph[:2]) {
		t.Error("expected a color swatch strip on each row")
	}
}

func TestCommandFormatThemeError(t *testing.T) {
	result := formatThemeError("darck", []string{"dark", "dracula"})

	if !strings.Contains(result, "Unknown theme") {
		t.Error("expected 'Unknown theme' prefix")
	}
	if !strings.Contains(result, "darck") {
		t.Error("expected the misspelled name")
	}
	if !strings.Contains(result, "Did you mean") {
		t.Error("expected 'Did you mean' suggestion")
	}
	if !strings.Contains(result, "dark") {
		t.Error("expected close match 'dark'")
	}
}

func TestCommandFormatThemeError_NoMatches(t *testing.T) {
	result := formatThemeError("xyz", nil)

	if !strings.Contains(result, "Unknown theme") {
		t.Error("expected 'Unknown theme' prefix")
	}
	if strings.Contains(result, "Did you mean") {
		t.Error("should not contain suggestions when no matches")
	}
}

func TestCommandAgentStatusIcon(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"running", "▶ "},
		{"completed", "✓ "},
		{"failed", "✗ "},
		{"canceled", "◼ "},
		{"killed", "⚠ "},
		{"unknown", "  "},
	}
	for _, tt := range tests {
		got := agentStatusIcon(tt.status)
		if got != tt.want {
			t.Errorf("agentStatusIcon(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

// --- handleRTKCommand tests ---

func TestCommandRTK_NilCompactMetrics(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{CompactMetrics: nil},
	}

	m.handleRTKCommand(nil)

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not active") {
		t.Errorf("expected 'not active' message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestCommandRTK_WithStats(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			CompactMetrics: &mockCompactStats{stats: "Compacted 10 messages"},
		},
	}

	m.handleRTKCommand(nil)

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Compacted 10 messages") {
		t.Errorf("expected stats content, got %q", m.chatModel.Messages[0].content)
	}
}

func TestCommandRTK_UnknownSubcommand(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{CompactMetrics: &mockCompactStats{stats: "stats"}},
	}

	m.handleRTKCommand([]string{"bogus"})

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Usage:") {
		t.Errorf("expected usage message, got %q", m.chatModel.Messages[0].content)
	}
}

// --- handleAgentsCommand tests ---

// --- formatContextUsage edge cases ---

func TestCommandFormatContextUsage_LargeContext(t *testing.T) {
	// Generate enough content to exceed 10000 tokens (~40000 chars)
	bigContent := strings.Repeat("x", 50000)
	m := &model{
		chatModel: ChatModel{
			Messages: []message{
				{role: "user", content: bigContent},
			},
		},
		cfg: Config{ModelName: "test-model"},
	}

	result := m.formatContextUsage()
	if !strings.Contains(result, "Context Usage") {
		t.Error("expected Context Usage header")
	}
	// Should hit the totalTokens > 10000 branch.
	if !strings.Contains(result, "tokens") {
		t.Error("expected token count in output")
	}
}

func TestCommandFormatContextUsage_TokenTrackerOverflow(t *testing.T) {
	// Token tracker with usage exceeding limit (>100%).
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName: "test-model",
			TokenTracker: &cmdMockTokenTracker{
				totalUsed:   200000,
				limit:       100000,
				remaining:   0,
				percentUsed: 200.0,
			},
		},
	}

	result := m.formatContextUsage()
	// Should hit the usedBlocks > barLen clamping.
	if !strings.Contains(result, "200") {
		t.Errorf("expected overflow percentage, got %q", result)
	}
}

func TestCommandFormatContextUsage_EmptyCompactStats(t *testing.T) {
	// CompactMetrics returns empty string — should not show section.
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:      "test-model",
			CompactMetrics: &mockCompactStats{stats: ""},
		},
	}

	result := m.formatContextUsage()
	if strings.Contains(result, "Output compaction") {
		t.Error("should not show Output compaction when stats is empty")
	}
}

// --- formatHelp edge cases ---

func TestFormatHelp_WithSkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Skills: []extension.Skill{
				{Name: "test-skill", Description: "A test skill"},
				{Name: "deploy", Description: "Deploy to production"},
			},
		},
	}

	result := m.formatHelp()
	if !strings.Contains(result, "Available skills:") {
		t.Error("expected Available skills section")
	}
	if !strings.Contains(result, "test-skill") {
		t.Error("expected test-skill in help")
	}
	if !strings.Contains(result, "deploy") {
		t.Error("expected deploy in help")
	}
}

// --- handleSkillsCommand edge cases ---

func TestHandleSkillsCommand_CreateSubcommand(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// "create" without name should show usage or error.
	newM, _ := m.handleSkillsCommand([]string{"create"})
	mm := newM.(*model)

	if len(mm.chatModel.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
}

func TestHandleSkillsCommand_ReloadSubcommand(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleSkillsCommand([]string{"reload"})
	mm := newM.(*model)

	if len(mm.chatModel.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
}

// --- showCommandList ---

func TestCommandShowCommandList_NoSkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{Skills: nil},
	}

	m.showCommandList()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Commands") {
		t.Error("expected 'Commands' header")
	}
	if strings.Contains(m.chatModel.Messages[0].content, "Skills") {
		t.Error("should not show Skills section when no skills loaded")
	}
}

func TestCommandShowCommandList_WithLoadedSkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Skills: []extension.Skill{
				{Name: "deploy", Description: "Deploy app"},
			},
		},
	}

	m.showCommandList()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "Skills") {
		t.Error("expected Skills section")
	}
	if !strings.Contains(content, "deploy") {
		t.Error("expected deploy skill")
	}
}

// --- formatModelInfo additional ---

func TestFormatModelInfo_DefaultRole(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:  "test-model",
			ActiveRole: "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "test-model"},
			},
		},
	}

	result := m.formatModelInfo()
	// "default" role should not show "(role: default)".
	if strings.Contains(result, "(role: default)") {
		t.Error("should not show '(role: default)' for default role")
	}
	// But should show star marker.
	if !strings.Contains(result, "* **default**") {
		t.Errorf("expected active marker on default role, got %q", result)
	}
}

func TestHandleAgentsCommand_NilOrchestrator(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{Orchestrator: nil},
	}

	m.handleAgentsCommand()

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", m.chatModel.Messages[0].content)
	}
}

// --- maskServerURL tests ---

func TestMaskServerURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "_none_"},
		{"https://example.com/mcp", "https://example.com/mcp"},
		{"https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-real-key", "https://mcp.tavily.com/mcp/?tavilyApiKey=***"},
		{"https://api.example.com/mcp?key=secret123", "https://api.example.com/mcp?key=***"},
		{"https://api.example.com/mcp?apiKey=abc123&other=value", "https://api.example.com/mcp?apiKey=***&other=value"},
	}

	for _, tt := range tests {
		result := maskServerURL(tt.input)
		if result != tt.expected {
			t.Errorf("maskServerURL(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestCommandFormatContextUsage_SkillsSection(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName: "test-model",
			Skills: []extension.Skill{
				{Name: "alpha", Description: "First skill", Source: "bundled"},
				{Name: "beta", Description: "Second skill", Source: "user"},
			},
		},
	}

	result := m.formatContextUsage()

	if !strings.Contains(result, "*Skills*") {
		t.Errorf("expected Skills header, got %q", result)
	}
	if !strings.Contains(result, "(2 loaded)") {
		t.Errorf("expected '(2 loaded)', got %q", result)
	}
	// Alpha should come before beta (alphabetical).
	alphaIdx := strings.Index(result, "/alpha")
	betaIdx := strings.Index(result, "/beta")
	if alphaIdx < 0 || betaIdx < 0 || alphaIdx > betaIdx {
		t.Errorf("expected alpha before beta, got alpha=%d beta=%d", alphaIdx, betaIdx)
	}
	// Source tag should be present.
	if !strings.Contains(result, "[bundled]") {
		t.Errorf("expected [bundled] tag, got %q", result)
	}
	if !strings.Contains(result, "[user]") {
		t.Errorf("expected [user] tag, got %q", result)
	}
	// No body loaded yet → "body: not loaded" for both.
	if !strings.Contains(result, "body: not loaded") {
		t.Errorf("expected 'body: not loaded' for unloaded skills, got %q", result)
	}
}

func TestCommandFormatContextUsage_SkillsSectionNoSkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{ModelName: "test-model"},
	}
	result := m.formatContextUsage()
	if strings.Contains(result, "*Skills*") {
		t.Errorf("expected no Skills section when no skills loaded, got %q", result)
	}
}

// newCompactTestSession builds a session with enough text-shaped content for
// Compact to actually rewrite history. The compacted file gets picked up by
// EstimateTokens on the subsequent handleCompactCommand call, exercising the
// post-compaction gauge-update wire-up.
func newCompactTestSession(t *testing.T) (*pisession.FileService, string) {
	t.Helper()
	tmp := t.TempDir()
	svc, err := pisession.NewFileService(filepath.Join(tmp, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: agent.AppName,
		UserID:  agent.DefaultUserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Session.ID()

	// A handful of long user/assistant turns gets us comfortably over the
	// 100k default MaxTokens threshold inside Compact, so the rewrite path
	// runs and EstimateTokens returns a non-trivial post-compaction number.
	body := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 2000)
	events := []*session.Event{
		makeTextEvent("user", "first long message "+body),
		makeTextEvent("assistant", "first long reply "+body),
		makeTextEvent("user", "second long message "+body),
		makeTextEvent("assistant", "second long reply "+body),
		makeTextEvent("user", "third long message "+body),
		makeTextEvent("assistant", "third long reply "+body),
	}
	for i, ev := range events {
		ev.ID = "ev-" + string(rune('a'+i))
		if err := svc.AppendEvent(context.Background(), resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	return svc, id
}

func makeTextEvent(author, text string) *session.Event {
	ev := &session.Event{
		Timestamp: time.Unix(0, 0),
		Author:    author,
	}
	ev.Content = genai.NewContentFromText(text, genai.RoleUser)
	return ev
}

// agentItemsFixture builds a picker over a mixed set of agent states.
func agentItemsFixture() []subagent.AgentStatus {
	return []subagent.AgentStatus{
		{AgentID: "aaaa1111bbbb", Type: "task", Status: "completed", Prompt: "Summarize the diff", Duration: "3s"},
		{AgentID: "cccc2222dddd", Type: "explore", Status: "running", Prompt: "Find the config\nwith more lines", StartedAt: time.Now()},
	}
}

// TestAgentSearchItems covers the picker's rows. The picker is a view over the
// orchestrator's state, so what matters is that each row carries enough to act on:
// the ID to select or cancel, the running flag to gate cancellation, and a title
// that is the first line of the prompt rather than the whole thing.
func TestAgentSearchItems(t *testing.T) {
	items := agentSearchItems(agentItemsFixture())
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	// Running agents sort first so a live one is reachable without scrolling.
	if !items[0].Running {
		t.Error("a running agent should sort first")
	}
	if items[0].AgentID != "cccc2222dddd" {
		t.Errorf("first item = %q, want the running agent", items[0].AgentID)
	}

	for _, it := range items {
		if it.AgentID == "" {
			t.Errorf("item %q has no AgentID; it cannot be accepted or canceled", it.Text)
		}
		if strings.Contains(it.Text, "\n") {
			t.Errorf("title %q carries a newline; it would break the popup row", it.Text)
		}
	}
	// The status and duration are what tell two rows apart.
	if !strings.Contains(items[0].Description, "running") {
		t.Errorf("running row description = %q", items[0].Description)
	}
	if !strings.Contains(items[1].Description, "completed") || !strings.Contains(items[1].Description, "3s") {
		t.Errorf("completed row description = %q, want status and duration", items[1].Description)
	}
	// Status icons distinguish the rows at a glance.
	if !strings.HasPrefix(items[0].Text, "▶ ") {
		t.Errorf("running row should carry the running icon, got %q", items[0].Text)
	}
	if !strings.HasPrefix(items[1].Text, "✓ ") {
		t.Errorf("completed row should carry the done icon, got %q", items[1].Text)
	}
}

// TestExpandAgentCard covers selecting a card from the picker. At most one card is
// expanded, and selecting the same one again collapses it.
func TestExpandAgentCard(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = []message{
		{role: "tool", tool: "subagent", agentID: "aaaa1111", agentEvents: []agentEv{{kind: "text", content: "one"}}},
		{role: "tool", tool: "subagent", agentID: "cccc2222", agentEvents: []agentEv{{kind: "text", content: "two"}}},
	}

	m.expandAgentCard("aaaa1111")
	if m.expandedAgentID() != "aaaa1111" {
		t.Fatalf("expandedAgent = %q, want aaaa1111", m.expandedAgentID())
	}
	// Selecting another moves the expansion rather than adding a second.
	m.expandAgentCard("cccc2222")
	if m.expandedAgentID() != "cccc2222" {
		t.Errorf("expandedAgent = %q, want cccc2222 — only one card may be expanded", m.expandedAgentID())
	}
	// Selecting it again collapses.
	m.expandAgentCard("cccc2222")
	if m.expandedAgentID() != "" {
		t.Errorf("expandedAgent = %q, want empty after re-selecting", m.expandedAgentID())
	}
}

// TestExpandAgentCardMissingReported checks the resume/compaction case: the
// orchestrator can list an agent whose card is no longer in this transcript. Silence
// there reads as a broken key, so it has to say something.
func TestExpandAgentCardMissingReported(t *testing.T) {
	m := newTestModel(t)
	m.expandAgentCard("notinthisss")
	if m.expandedAgentID() != "" {
		t.Errorf("expandedAgent = %q, want empty for an absent card", m.expandedAgentID())
	}
	if m.flash == "" {
		t.Error("selecting an agent with no card should say so, not silently do nothing")
	}
}

// TestExpandedAgentWidensCardWindow is the payoff: the card shows more of its event
// stream when it is the expanded one, and the note accounting for withheld events
// shrinks accordingly.
func TestExpandedAgentWidensCardWindow(t *testing.T) {
	evs := make([]agentEv, 0, 40)
	for i := 0; i < 40; i++ {
		evs = append(evs, agentEv{kind: "tool_call", content: "step"})
	}
	msg := message{role: "tool", tool: "subagent", agentID: "aaaa1111", agentEvents: evs}

	td := &ToolDisplayModel{Width: 100}
	normal := td.renderAgentTool(msg, lipgloss.NewStyle(), darkPalette)

	td.ExpandedAgentID = "aaaa1111"
	expanded := td.renderAgentTool(msg, lipgloss.NewStyle(), darkPalette)

	if n, e := strings.Count(normal, "\n"), strings.Count(expanded, "\n"); e <= n {
		t.Errorf("expanded card has %d rows, normal has %d — expansion did not widen the window", e, n)
	}
	// A card that is not the expanded one keeps the normal window.
	td.ExpandedAgentID = "someoneelse"
	other := td.renderAgentTool(msg, lipgloss.NewStyle(), darkPalette)
	if other != normal {
		t.Error("a card other than the expanded one changed its window")
	}
}

// TestCancelSelectedAgentReportsOrchestratorError pins that a refusal is surfaced.
// The orchestrator rejects a non-running agent, and a user who pressed Ctrl+X on a
// finished one must learn why nothing happened.
func TestCancelSelectedAgentReportsOrchestratorError(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Orchestrator = subagent.NewOrchestrator(&config.Config{}, "", nil)

	m.searchPopup = &searchPopupState{
		mode:    searchModeAgents,
		entries: []SearchItem{{Text: "✓ done", AgentID: "gone0001"}},
		filtered: []SearchItem{
			{Text: "✓ done", AgentID: "gone0001"},
		},
	}
	cmd := m.cancelSelectedAgent()
	if cmd == nil {
		t.Fatal("canceling an unknown agent must flash something")
	}
	if m.flash == "" {
		t.Error("the orchestrator's refusal was swallowed")
	}
	if !strings.Contains(m.flash, "gone0001") && !strings.Contains(m.flash, "not found") {
		t.Errorf("flash %q does not explain the refusal", m.flash)
	}
}
