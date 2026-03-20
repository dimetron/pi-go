package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/session"
)

func TestHandleSlashCommandHelp(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.handleSlashCommand("/help")
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd for /help")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if mm.chatModel.Messages[0].role != "assistant" {
		t.Errorf("expected assistant role, got %q", mm.chatModel.Messages[0].role)
	}
}

func TestHandleSlashCommandClear(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/clear"},
		chatModel: ChatModel{
			Messages: []message{
				{role: "user", content: "hello"},
				{role: "assistant", content: "hi"},
			},
		},
	}

	newM, _ := m.handleSlashCommand("/clear")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 0 {
		t.Errorf("expected 0 messages after /clear, got %d", len(mm.chatModel.Messages))
	}
}

func TestHandleSlashCommandModel(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{ModelName: "test-model"},
	}

	newM, _ := m.handleSlashCommand("/model")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Current model: **test-model**") {
		t.Errorf("unexpected content: %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandModelShowsRoles(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:  "claude-sonnet-4-6",
			ActiveRole: "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "claude-sonnet-4-6"},
				"smol":    {Model: "gemini-2.5-flash"},
				"slow":    {Model: "claude-opus-4-6", Provider: "anthropic"},
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Configured roles:") {
		t.Errorf("expected roles section, got %q", content)
	}
	if !strings.Contains(content, "smol") {
		t.Errorf("expected smol role listed, got %q", content)
	}
	if !strings.Contains(content, "slow") {
		t.Errorf("expected slow role listed, got %q", content)
	}
	if !strings.Contains(content, "[anthropic]") {
		t.Errorf("expected provider annotation for slow role, got %q", content)
	}
}

func TestHandleSlashCommandModelShowsActiveRole(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:  "gemini-2.5-flash",
			ActiveRole: "smol",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "claude-sonnet-4-6"},
				"smol":    {Model: "gemini-2.5-flash"},
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model")
	mm := newM.(*model)

	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "(role: smol)") {
		t.Errorf("expected active role indicator, got %q", content)
	}
}

func TestHandleSlashCommandExit(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/exit"},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.handleSlashCommand("/exit")
	mm := newM.(*model)

	if !mm.quitting {
		t.Error("expected quitting to be true after /exit")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestHandleSlashCommandUnknown(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/unknown"},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleSlashCommand("/unknown")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if mm.chatModel.Messages[0].content != "Unknown command: `/unknown`. Type `/help` for available commands." {
		t.Errorf("unexpected content: %q", mm.chatModel.Messages[0].content)
	}
}

func TestUpdateWindowSize(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	mm := newM.(*model)

	if mm.width != 80 {
		t.Errorf("expected width 80, got %d", mm.width)
	}
	if mm.height != 24 {
		t.Errorf("expected height 24, got %d", mm.height)
	}
}

func TestAgentTextMsg(t *testing.T) {
	m := &model{
		running:   true,
		chatModel: ChatModel{
			Streaming: "",
			Messages: []message{
				{role: "user", content: "hello"},
				{role: "assistant", content: ""},
			},
		},
		agentCh: make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentTextMsg{text: "Hello "})
	mm := newM.(*model)

	if mm.chatModel.Streaming != "Hello " {
		t.Errorf("expected streaming %q, got %q", "Hello ", mm.chatModel.Streaming)
	}
	if mm.chatModel.Messages[1].content != "Hello " {
		t.Errorf("expected message content %q, got %q", "Hello ", mm.chatModel.Messages[1].content)
	}
}

func TestAgentDoneMsg(t *testing.T) {
	m := &model{
		running:   true,
		chatModel: ChatModel{
			Streaming: "accumulated text",
			Messages: make([]message, 0),
		},
		agentCh:   make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentDoneMsg{})
	mm := newM.(*model)

	if mm.running {
		t.Error("expected running to be false after agentDoneMsg")
	}
	if mm.chatModel.Streaming != "" {
		t.Errorf("expected streaming to be cleared, got %q", mm.chatModel.Streaming)
	}
}

func TestAgentToolCallMsg(t *testing.T) {
	m := &model{
		running:  true,
		chatModel: ChatModel{Messages: make([]message, 0)},
		agentCh:  make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentToolCallMsg{name: "read"})
	mm := newM.(*model)

	if mm.activeTool != "read" {
		t.Errorf("expected activeTool %q, got %q", "read", mm.activeTool)
	}
}

func TestAgentToolResultMsg(t *testing.T) {
	m := &model{
		running:    true,
		activeTool: "read",
		chatModel: ChatModel{Messages: make([]message, 0)},
		agentCh:    make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentToolResultMsg{name: "read"})
	mm := newM.(*model)

	if mm.activeTool != "" {
		t.Errorf("expected activeTool to be empty, got %q", mm.activeTool)
	}
}

func TestHistoryNavigation(t *testing.T) {
	m := &model{
		inputModel: InputModel{
			History:    []string{"first", "second", "third"},
			HistoryIdx: -1,
			CyclingIdx: -1,
		},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// Press Up → should get "third" (last entry)
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	mm := newM.(*model)
	if mm.inputModel.Text != "third" {
		t.Errorf("expected %q, got %q", "third", mm.inputModel.Text)
	}

	// Press Up again → should get "second"
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	mm = newM.(*model)
	if mm.inputModel.Text != "second" {
		t.Errorf("expected %q, got %q", "second", mm.inputModel.Text)
	}

	// Press Down → should get "third"
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	mm = newM.(*model)
	if mm.inputModel.Text != "third" {
		t.Errorf("expected %q, got %q", "third", mm.inputModel.Text)
	}

	// Press Down again → should clear input
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	mm = newM.(*model)
	if mm.inputModel.Text != "" {
		t.Errorf("expected empty input, got %q", mm.inputModel.Text)
	}
}

func TestTextInput(t *testing.T) {
	m := &model{
		inputModel: InputModel{CyclingIdx: -1},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// Type "hi"
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "h", Code: 'h'}))
	mm := newM.(*model)
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Text: "i", Code: 'i'}))
	mm = newM.(*model)

	if mm.inputModel.Text != "hi" {
		t.Errorf("expected %q, got %q", "hi", mm.inputModel.Text)
	}
	if mm.inputModel.CursorPos != 2 {
		t.Errorf("expected cursorPos 2, got %d", mm.inputModel.CursorPos)
	}

	// Backspace
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	mm = newM.(*model)
	if mm.inputModel.Text != "h" {
		t.Errorf("expected %q after backspace, got %q", "h", mm.inputModel.Text)
	}
}

func TestRenderMessagesEmpty(t *testing.T) {
	m := &model{
		width:    80,
		height:   24,
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	output := m.chatModel.RenderMessages(m.running)
	if output == "" {
		t.Error("expected welcome message for empty conversation")
	}
}

func TestViewQuitting(t *testing.T) {
	m := &model{
		quitting: true,
		width:    80,
		height:   24,
	}
	v := m.View()
	if v.Content != "Goodbye!\n" {
		t.Errorf("expected goodbye message, got %q", v.Content)
	}
}

func TestViewLoading(t *testing.T) {
	m := &model{
		width:  0,
		height: 0,
	}
	v := m.View()
	if v.Content != "Loading..." {
		t.Errorf("expected loading message, got %q", v.Content)
	}
}

func TestMaxScrollEmpty(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		height:   24,
	}
	if max := m.chatModel.MaxScroll(m.height); max != 0 {
		t.Errorf("expected 0, got %d", max)
	}
}

func TestHandleSlashCommandSession(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/session"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionID: "test-session-123"},
	}

	newM, _ := m.handleSlashCommand("/session")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if mm.chatModel.Messages[0].content != "Session: `test-session-123`" {
		t.Errorf("unexpected content: %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandBranchNoService(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/branch experiment"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionService: nil},
	}

	newM, _ := m.handleSlashCommand("/branch experiment")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandBranchUsage(t *testing.T) {
	svc := setupTestSessionService(t)
	m := &model{
		inputModel: InputModel{Text: "/branch"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionService: svc, SessionID: "s1"},
	}

	newM, _ := m.handleSlashCommand("/branch")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Usage") {
		t.Errorf("expected usage message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandBranchCreate(t *testing.T) {
	svc, sessionID := setupTestSessionWithID(t)
	m := &model{
		inputModel: InputModel{Text: "/branch experiment"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionService: svc, SessionID: sessionID},
	}

	newM, _ := m.handleSlashCommand("/branch experiment")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Created and switched to branch") {
		t.Errorf("expected success message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandBranchList(t *testing.T) {
	svc, sessionID := setupTestSessionWithID(t)
	m := &model{
		inputModel: InputModel{Text: "/branch list"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionService: svc, SessionID: sessionID},
	}

	newM, _ := m.handleSlashCommand("/branch list")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "main") {
		t.Errorf("expected branch list containing 'main', got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandBranchSwitchNoName(t *testing.T) {
	svc, sessionID := setupTestSessionWithID(t)
	m := &model{
		inputModel: InputModel{Text: "/branch switch"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionService: svc, SessionID: sessionID},
	}

	newM, _ := m.handleSlashCommand("/branch switch")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Usage") {
		t.Errorf("expected usage message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandCompactNoService(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/compact"},
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionService: nil},
	}

	newM, _ := m.handleSlashCommand("/compact")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandHelpContainsBranch(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/help"},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleSlashCommand("/help")
	mm := newM.(*model)

	if !strings.Contains(mm.chatModel.Messages[0].content, "/branch") {
		t.Errorf("expected /help to mention /branch, got %q", mm.chatModel.Messages[0].content)
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "/compact") {
		t.Errorf("expected /help to mention /compact, got %q", mm.chatModel.Messages[0].content)
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "/session") {
		t.Errorf("expected /help to mention /session, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestSlashCommands_PlanRegistered(t *testing.T) {
	found := false
	for _, cmd := range slashCommands {
		if cmd == "/plan" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /plan in slashCommands list")
	}
}

func TestSlashCommands_RunRegistered(t *testing.T) {
	found := false
	for _, cmd := range slashCommands {
		if cmd == "/run" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /run in slashCommands list")
	}
}

func TestHelpText_IncludesPlanAndRun(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/help"},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleSlashCommand("/help")
	mm := newM.(*model)

	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "/plan") {
		t.Errorf("expected /help to mention /plan, got %q", content)
	}
	if !strings.Contains(content, "/run") {
		t.Errorf("expected /help to mention /run, got %q", content)
	}
	if !strings.Contains(content, "PDD planning session") {
		t.Errorf("expected /help to describe /plan, got %q", content)
	}
	if !strings.Contains(content, "spec") {
		t.Errorf("expected /help to mention spec for /run, got %q", content)
	}
}

func TestCompleteSlashCommand_Plan(t *testing.T) {
	result := completeSlashCommand("/pl")
	if result != "/plan" {
		t.Errorf("expected /plan completion, got %q", result)
	}
}

func TestCompleteSlashCommand_Run(t *testing.T) {
	result := completeSlashCommand("/ru")
	if result != "/run" {
		t.Errorf("expected /run completion, got %q", result)
	}
}

func TestCompleteSlashCommand_SlashOnly_NoGhost(t *testing.T) {
	// Just "/" should NOT produce a ghost completion (Tab shows the list instead).
	result := completeSlashCommand("/")
	if result != "" {
		t.Errorf("expected no ghost completion for '/', got %q", result)
	}
}

func TestCompleteSlashCommand_ExactMatch_NoGhost(t *testing.T) {
	result := completeSlashCommand("/help")
	if result != "" {
		t.Errorf("exact match should not produce ghost, got %q", result)
	}
}

func TestMatchingSlashCommands_All(t *testing.T) {
	matches := matchingSlashCommands("/")
	if len(matches) != len(slashCommands) {
		t.Errorf("expected %d matches for '/', got %d", len(slashCommands), len(matches))
	}
}

func TestMatchingSlashCommands_Partial(t *testing.T) {
	matches := matchingSlashCommands("/c")
	// Should match: /clear, /context, /compact, /commit
	if len(matches) != 4 {
		t.Errorf("expected 4 matches for '/c', got %d: %v", len(matches), matches)
	}
	for _, m := range matches {
		if !strings.HasPrefix(m, "/c") {
			t.Errorf("unexpected match %q for '/c'", m)
		}
	}
}

func TestMatchingSlashCommands_NoMatch(t *testing.T) {
	matches := matchingSlashCommands("/z")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for '/z', got %d: %v", len(matches), matches)
	}
}

func TestShowCommandList(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	m.showCommandList()

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "Commands:") {
		t.Error("expected 'Commands:' header")
	}
	// Verify all commands are listed.
	for _, cmd := range slashCommands {
		if !strings.Contains(content, cmd) {
			t.Errorf("command list should contain %q", cmd)
		}
	}
	// Verify descriptions are included.
	if !strings.Contains(content, "Show help") {
		t.Error("expected description for /help")
	}
	if !strings.Contains(content, "PDD planning session") {
		t.Error("expected description for /plan")
	}
}

func TestSlashCommandDesc_AllCommandsHaveDescs(t *testing.T) {
	for _, cmd := range slashCommands {
		desc := slashCommandDesc(cmd)
		if desc == "" {
			t.Errorf("command %q has no description", cmd)
		}
	}
}

func TestTabOnSlash_ShowsCommandList(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/"},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// Simulate Tab press.
	m.showCommandList()

	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected command list message")
	}
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "/plan") {
		t.Error("command list should include /plan")
	}
	if !strings.Contains(content, "/run") {
		t.Error("command list should include /run")
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{52000, "52.0k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{5200000, "5.2M"},
		{123456789, "123.5M"},
	}
	for _, tt := range tests {
		got := formatTokenCount(tt.n)
		if got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestHandleHistoryCommand_Empty(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	m.handleHistoryCommand(nil)
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "No command history") {
		t.Errorf("expected no history message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleHistoryCommand_WithEntries(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []string{"/help", "/model", "/ping", "/clear"}},
	}
	m.handleHistoryCommand(nil)
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "/help") || !strings.Contains(content, "/ping") {
		t.Errorf("expected history entries, got %q", content)
	}
}

func TestHandleHistoryCommand_WithFilter(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []string{"/help", "/model", "/ping", "/plan"}},
	}
	m.handleHistoryCommand([]string{"p"})
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "/ping") || !strings.Contains(content, "/plan") {
		t.Errorf("expected filtered entries with 'p', got %q", content)
	}
	if strings.Contains(content, "/model") {
		t.Errorf("should not contain /model, got %q", content)
	}
}

func TestHandleHistoryCommand_FilterNoMatch(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []string{"/help", "/model"}},
	}
	m.handleHistoryCommand([]string{"xyz"})
	if !strings.Contains(m.chatModel.Messages[0].content, "No history matching") {
		t.Errorf("expected no match message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleCommitDone_Success(t *testing.T) {
	m := &model{
		chatModel: ChatModel{
			Messages: []message{
				{role: "assistant", content: "Committing..."},
			},
		},
		commit: &commitState{phase: "committing"},
	}
	newM, _ := m.handleCommitDone(commitDoneMsg{output: "commit abc123"})
	mm := newM.(*model)
	found := false
	for _, msg := range mm.chatModel.Messages {
		if strings.Contains(msg.content, "Committed successfully") {
			found = true
		}
	}
	if !found {
		t.Error("expected success message")
	}
}

func TestHandleCommitDone_Error(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		commit:   &commitState{phase: "committing"},
	}
	newM, _ := m.handleCommitDone(commitDoneMsg{err: fmt.Errorf("git error")})
	mm := newM.(*model)
	found := false
	for _, msg := range mm.chatModel.Messages {
		if strings.Contains(msg.content, "git error") {
			found = true
		}
	}
	if !found {
		t.Error("expected error in messages")
	}
}

func TestRenderStatusBar_WithProvider(t *testing.T) {
	m := &model{
		cfg:   Config{ProviderName: "ollama", ModelName: "qwen3.5:latest"},
		width: 120,
	}
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "ollama") {
		t.Errorf("status bar should contain provider, got %q", bar)
	}
	if !strings.Contains(bar, "qwen3.5:latest") {
		t.Errorf("status bar should contain model, got %q", bar)
	}
}

func TestRenderStatusBar_WithoutProvider(t *testing.T) {
	m := &model{
		cfg:   Config{ModelName: "gpt-4o"},
		width: 120,
	}
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "gpt-4o") {
		t.Errorf("status bar should contain model, got %q", bar)
	}
}

func TestRenderStatusBar_ContextEstimate(t *testing.T) {
	m := &model{
		cfg:   Config{ModelName: "test"},
		width: 120,
		chatModel: ChatModel{
			Messages: []message{
				{content: strings.Repeat("a", 4000)}, // ~1k tokens
			},
		},
	}
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "ctx:") {
		t.Errorf("status bar should show context estimate, got %q", bar)
	}
}

func TestMaxScroll_EmptyMessages(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: nil},
		height:   40,
	}
	if m.chatModel.MaxScroll(m.height) != 0 {
		t.Error("maxScroll should be 0 for empty messages")
	}
}

func TestMaxScroll_SmallHeight(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{content: "test"}}},
		height:   0,
	}
	if m.chatModel.MaxScroll(m.height) != 0 {
		t.Error("maxScroll should be 0 for zero height")
	}
}

func TestHandleSlashCommand_Session(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{SessionID: "test-session-123"},
	}
	newM, _ := m.handleSlashCommand("/session")
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "test-session-123") {
		t.Errorf("expected session ID, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommand_Unknown(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	newM, _ := m.handleSlashCommand("/nonexistent")
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "Unknown command") {
		t.Errorf("expected unknown command message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommand_Exit(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	newM, cmd := m.handleSlashCommand("/exit")
	mm := newM.(*model)
	if !mm.quitting {
		t.Error("expected quitting to be true")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestHandleSlashCommand_Ping(t *testing.T) {
	mockLLM := &pingMockLLM{name: "test", response: "Pong"}
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{LLM: mockLLM},
	}
	newM, cmd := m.handleSlashCommand("/ping")
	mm := newM.(*model)
	if cmd == nil {
		t.Error("expected non-nil cmd for /ping")
	}
	if len(mm.chatModel.Messages) < 1 {
		t.Fatal("expected placeholder message")
	}
}

func TestHandleSkillCreateCommand_NoArgs(t *testing.T) {
	m := &model{chatModel: ChatModel{Messages: make([]message, 0)}}
	newM, cmd := m.handleSkillCreateCommand(nil)
	mm := newM.(*model)
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Usage:") {
		t.Errorf("expected usage message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSkillCreateCommand_InvalidName(t *testing.T) {
	m := &model{chatModel: ChatModel{Messages: make([]message, 0)}}
	newM, _ := m.handleSkillCreateCommand([]string{"bad name!"})
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "Invalid skill name") {
		t.Errorf("expected invalid name error, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSkillCreateCancel(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		pendingSkillCreate: &pendingSkillCreate{name: "test"},
	}
	newM, _ := m.handleSkillCreateCancel()
	mm := newM.(*model)
	if mm.pendingSkillCreate != nil {
		t.Error("pending should be cleared")
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "cancelled") {
		t.Errorf("expected cancelled message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSkillListCommand_Empty(t *testing.T) {
	m := &model{chatModel: ChatModel{Messages: make([]message, 0)}, cfg: Config{}}
	newM, _ := m.handleSkillListCommand()
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "No skills loaded") {
		t.Errorf("expected no skills message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSkillListCommand_WithSkills(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Skills: []extension.Skill{
				{Name: "test-skill", Description: "A test skill"},
				{Name: "another", Description: "Another one"},
			},
			SkillDirs: []string{"/tmp/skills"},
		},
	}
	newM, _ := m.handleSkillListCommand()
	mm := newM.(*model)
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "/test-skill") {
		t.Errorf("expected skill name, got %q", content)
	}
	if !strings.Contains(content, "A test skill") {
		t.Errorf("expected skill description, got %q", content)
	}
	if !strings.Contains(content, "/tmp/skills") {
		t.Errorf("expected skill dir, got %q", content)
	}
}

func TestHandleSkillLoadCommand_Empty(t *testing.T) {
	m := &model{chatModel: ChatModel{Messages: make([]message, 0)}, cfg: Config{}}
	newM, _ := m.handleSkillLoadCommand()
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "no skills found") {
		t.Errorf("expected no skills message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommand_Model(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:      Config{ModelName: "gpt-4o", ActiveRole: "default", Roles: map[string]config.RoleConfig{"default": {Model: "gpt-4o"}}},
	}
	newM, _ := m.handleSlashCommand("/model")
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "gpt-4o") {
		t.Errorf("expected model name in output, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommand_Clear(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "user", content: "hello"}, {role: "assistant", content: "hi"}}},
	}
	newM, _ := m.handleSlashCommand("/clear")
	mm := newM.(*model)
	if len(mm.chatModel.Messages) != 0 {
		t.Errorf("expected 0 messages after /clear, got %d", len(mm.chatModel.Messages))
	}
}

func TestHandleSlashCommand_History(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []string{"/help", "/model"}},
	}
	newM, _ := m.handleSlashCommand("/history")
	mm := newM.(*model)
	if !strings.Contains(mm.chatModel.Messages[0].content, "/help") {
		t.Errorf("expected history output, got %q", mm.chatModel.Messages[0].content)
	}
}

// Test helpers

func setupTestSessionService(t *testing.T) *pisession.FileService {
	t.Helper()
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatalf("creating FileService: %v", err)
	}
	return svc
}

func setupTestSessionWithID(t *testing.T) (*pisession.FileService, string) {
	t.Helper()
	svc := setupTestSessionService(t)

	ctx := t.Context()
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: agent.AppName,
		UserID:  agent.DefaultUserID,
	})
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}
	return svc, resp.Session.ID()
}

func TestHandleAgentsCommand_NoOrchestrator(t *testing.T) {
	m := &model{
		cfg:      Config{},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	m.handleAgentsCommand()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if m.chatModel.Messages[0].content != "Subagent system not available." {
		t.Errorf("unexpected message: %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleAgentsCommand_EmptyList(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	m := &model{
		cfg: Config{
			Orchestrator: orch,
		},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
	m.handleAgentsCommand()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if m.chatModel.Messages[0].content != "No subagents have been spawned yet." {
		t.Errorf("unexpected message: %q", m.chatModel.Messages[0].content)
	}
}
