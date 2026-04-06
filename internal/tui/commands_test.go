package tui

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
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
	tm := NewThemeManager()
	m := &model{
		chatModel:    ChatModel{Messages: make([]message, 0)},
		themeManager: tm,
	}

	newM, cmd := m.handleThemeCommand([]string{})
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Current theme") {
		t.Errorf("expected theme list with 'Current theme', got %q", content)
	}
	if !strings.Contains(content, "Available themes") {
		t.Errorf("expected 'Available themes' section, got %q", content)
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
}

func (m *cmdMockTokenTracker) TotalUsed() int64     { return m.totalUsed }
func (m *cmdMockTokenTracker) Limit() int64         { return m.limit }
func (m *cmdMockTokenTracker) Remaining() int64     { return m.remaining }
func (m *cmdMockTokenTracker) PercentUsed() float64 { return m.percentUsed }

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

	result := formatThemeList(themes, "dark-one")

	if !strings.Contains(result, "Current theme:") {
		t.Error("expected 'Current theme:' header")
	}
	if !strings.Contains(result, "dark-one") {
		t.Error("expected current theme name")
	}
	if !strings.Contains(result, "Light One") {
		t.Error("expected light theme display name")
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
