package tui

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/session"
)

func TestHandleSlashCommandHelp(t *testing.T) {
	m := &model{
		input:    "/help",
		messages: make([]message, 0),
	}

	newM, cmd := m.handleSlashCommand("/help")
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd for /help")
	}
	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if mm.messages[0].role != "assistant" {
		t.Errorf("expected assistant role, got %q", mm.messages[0].role)
	}
	if mm.input != "" {
		t.Errorf("input should be cleared, got %q", mm.input)
	}
}

func TestHandleSlashCommandClear(t *testing.T) {
	m := &model{
		input: "/clear",
		messages: []message{
			{role: "user", content: "hello"},
			{role: "assistant", content: "hi"},
		},
	}

	newM, _ := m.handleSlashCommand("/clear")
	mm := newM.(*model)

	if len(mm.messages) != 0 {
		t.Errorf("expected 0 messages after /clear, got %d", len(mm.messages))
	}
}

func TestHandleSlashCommandModel(t *testing.T) {
	m := &model{
		input:    "/model",
		messages: make([]message, 0),
		cfg:      Config{ModelName: "test-model"},
	}

	newM, _ := m.handleSlashCommand("/model")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "Current model: **test-model**") {
		t.Errorf("unexpected content: %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandModelShowsRoles(t *testing.T) {
	m := &model{
		input:    "/model",
		messages: make([]message, 0),
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

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	content := mm.messages[0].content
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
		input:    "/model",
		messages: make([]message, 0),
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

	content := mm.messages[0].content
	if !strings.Contains(content, "(role: smol)") {
		t.Errorf("expected active role indicator, got %q", content)
	}
}

func TestHandleSlashCommandExit(t *testing.T) {
	m := &model{
		input:    "/exit",
		messages: make([]message, 0),
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
		input:    "/unknown",
		messages: make([]message, 0),
	}

	newM, _ := m.handleSlashCommand("/unknown")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if mm.messages[0].content != "Unknown command: `/unknown`. Type `/help` for available commands." {
		t.Errorf("unexpected content: %q", mm.messages[0].content)
	}
}

func TestUpdateWindowSize(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
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
		streaming: "",
		messages: []message{
			{role: "user", content: "hello"},
			{role: "assistant", content: ""},
		},
		agentCh: make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentTextMsg{text: "Hello "})
	mm := newM.(*model)

	if mm.streaming != "Hello " {
		t.Errorf("expected streaming %q, got %q", "Hello ", mm.streaming)
	}
	if mm.messages[1].content != "Hello " {
		t.Errorf("expected message content %q, got %q", "Hello ", mm.messages[1].content)
	}
}

func TestAgentDoneMsg(t *testing.T) {
	m := &model{
		running:   true,
		streaming: "accumulated text",
		agentCh:   make(chan agentMsg, 1),
		messages:  make([]message, 0),
	}

	newM, _ := m.Update(agentDoneMsg{})
	mm := newM.(*model)

	if mm.running {
		t.Error("expected running to be false after agentDoneMsg")
	}
	if mm.streaming != "" {
		t.Errorf("expected streaming to be cleared, got %q", mm.streaming)
	}
}

func TestAgentToolCallMsg(t *testing.T) {
	m := &model{
		running:  true,
		messages: make([]message, 0),
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
		messages:   make([]message, 0),
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
		input:      "",
		history:    []string{"first", "second", "third"},
		historyIdx: -1,
		cyclingIdx: -1,
		messages:   make([]message, 0),
	}

	// Press Up → should get "third" (last entry)
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	mm := newM.(*model)
	if mm.input != "third" {
		t.Errorf("expected %q, got %q", "third", mm.input)
	}

	// Press Up again → should get "second"
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	mm = newM.(*model)
	if mm.input != "second" {
		t.Errorf("expected %q, got %q", "second", mm.input)
	}

	// Press Down → should get "third"
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	mm = newM.(*model)
	if mm.input != "third" {
		t.Errorf("expected %q, got %q", "third", mm.input)
	}

	// Press Down again → should clear input
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	mm = newM.(*model)
	if mm.input != "" {
		t.Errorf("expected empty input, got %q", mm.input)
	}
}

func TestTextInput(t *testing.T) {
	m := &model{
		input:     "",
		cursorPos: 0,
		messages:  make([]message, 0),
	}

	// Type "hi"
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "h", Code: 'h'}))
	mm := newM.(*model)
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Text: "i", Code: 'i'}))
	mm = newM.(*model)

	if mm.input != "hi" {
		t.Errorf("expected %q, got %q", "hi", mm.input)
	}
	if mm.cursorPos != 2 {
		t.Errorf("expected cursorPos 2, got %d", mm.cursorPos)
	}

	// Backspace
	newM, _ = mm.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	mm = newM.(*model)
	if mm.input != "h" {
		t.Errorf("expected %q after backspace, got %q", "h", mm.input)
	}
}

func TestRenderMessagesEmpty(t *testing.T) {
	m := &model{
		width:    80,
		height:   24,
		messages: make([]message, 0),
	}
	output := m.renderMessages()
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
		messages: make([]message, 0),
		height:   24,
	}
	if max := m.maxScroll(); max != 0 {
		t.Errorf("expected 0, got %d", max)
	}
}

func TestHandleSlashCommandSession(t *testing.T) {
	m := &model{
		input:    "/session",
		messages: make([]message, 0),
		cfg:      Config{SessionID: "test-session-123"},
	}

	newM, _ := m.handleSlashCommand("/session")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if mm.messages[0].content != "Session: `test-session-123`" {
		t.Errorf("unexpected content: %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandBranchNoService(t *testing.T) {
	m := &model{
		input:    "/branch experiment",
		messages: make([]message, 0),
		cfg:      Config{SessionService: nil},
	}

	newM, _ := m.handleSlashCommand("/branch experiment")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandBranchUsage(t *testing.T) {
	svc := setupTestSessionService(t)
	m := &model{
		input:    "/branch",
		messages: make([]message, 0),
		cfg:      Config{SessionService: svc, SessionID: "s1"},
	}

	newM, _ := m.handleSlashCommand("/branch")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "Usage") {
		t.Errorf("expected usage message, got %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandBranchCreate(t *testing.T) {
	svc, sessionID := setupTestSessionWithID(t)
	m := &model{
		input:    "/branch experiment",
		messages: make([]message, 0),
		cfg:      Config{SessionService: svc, SessionID: sessionID},
	}

	newM, _ := m.handleSlashCommand("/branch experiment")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "Created and switched to branch") {
		t.Errorf("expected success message, got %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandBranchList(t *testing.T) {
	svc, sessionID := setupTestSessionWithID(t)
	m := &model{
		input:    "/branch list",
		messages: make([]message, 0),
		cfg:      Config{SessionService: svc, SessionID: sessionID},
	}

	newM, _ := m.handleSlashCommand("/branch list")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "main") {
		t.Errorf("expected branch list containing 'main', got %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandBranchSwitchNoName(t *testing.T) {
	svc, sessionID := setupTestSessionWithID(t)
	m := &model{
		input:    "/branch switch",
		messages: make([]message, 0),
		cfg:      Config{SessionService: svc, SessionID: sessionID},
	}

	newM, _ := m.handleSlashCommand("/branch switch")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "Usage") {
		t.Errorf("expected usage message, got %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandCompactNoService(t *testing.T) {
	m := &model{
		input:    "/compact",
		messages: make([]message, 0),
		cfg:      Config{SessionService: nil},
	}

	newM, _ := m.handleSlashCommand("/compact")
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.messages[0].content)
	}
}

func TestHandleSlashCommandHelpContainsBranch(t *testing.T) {
	m := &model{
		input:    "/help",
		messages: make([]message, 0),
	}

	newM, _ := m.handleSlashCommand("/help")
	mm := newM.(*model)

	if !strings.Contains(mm.messages[0].content, "/branch") {
		t.Errorf("expected /help to mention /branch, got %q", mm.messages[0].content)
	}
	if !strings.Contains(mm.messages[0].content, "/compact") {
		t.Errorf("expected /help to mention /compact, got %q", mm.messages[0].content)
	}
	if !strings.Contains(mm.messages[0].content, "/session") {
		t.Errorf("expected /help to mention /session, got %q", mm.messages[0].content)
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
		input:    "/help",
		messages: make([]message, 0),
	}

	newM, _ := m.handleSlashCommand("/help")
	mm := newM.(*model)

	content := mm.messages[0].content
	if !strings.Contains(content, "/plan") {
		t.Errorf("expected /help to mention /plan, got %q", content)
	}
	if !strings.Contains(content, "/run") {
		t.Errorf("expected /help to mention /run, got %q", content)
	}
	if !strings.Contains(content, "PDD planning session") {
		t.Errorf("expected /help to describe /plan, got %q", content)
	}
	if !strings.Contains(content, "PROMPT.md") {
		t.Errorf("expected /help to mention PROMPT.md for /run, got %q", content)
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
	// Should match: /clear, /compact, /commit
	if len(matches) != 3 {
		t.Errorf("expected 3 matches for '/c', got %d: %v", len(matches), matches)
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
		messages: make([]message, 0),
	}
	m.showCommandList()

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	content := m.messages[0].content
	if !strings.Contains(content, "Available commands") {
		t.Error("expected 'Available commands' header")
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
		input:    "/",
		messages: make([]message, 0),
	}

	// Simulate Tab press.
	m.showCommandList()

	if len(m.messages) == 0 {
		t.Fatal("expected command list message")
	}
	content := m.messages[0].content
	if !strings.Contains(content, "/plan") {
		t.Error("command list should include /plan")
	}
	if !strings.Contains(content, "/run") {
		t.Error("command list should include /run")
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
		messages: make([]message, 0),
	}
	m.handleAgentsCommand()
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].content != "Subagent system not available." {
		t.Errorf("unexpected message: %q", m.messages[0].content)
	}
}

func TestHandleAgentsCommand_EmptyList(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "")
	m := &model{
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
	}
	m.handleAgentsCommand()
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].content != "No subagents have been spawned yet." {
		t.Errorf("unexpected message: %q", m.messages[0].content)
	}
}
