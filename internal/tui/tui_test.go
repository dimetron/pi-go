package tui

import (
	"context"
	"fmt"
	"iter"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"
)

// stubLLM is a minimal adkmodel.LLM implementation for TUI tests.
type stubLLM struct {
	name string
}

func (s *stubLLM) Name() string { return s.name }
func (s *stubLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(&adkmodel.LLMResponse{Content: genai.NewContentFromText("ok", genai.RoleModel)}, nil)
	}
}

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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{ModelName: "test-model"},
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

func TestHandleSlashCommandCopy(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{
			{role: "user", content: "hello"},
			{role: "assistant", content: "hi"},
		}},
	}

	newM, cmd := m.handleSlashCommand("/copy")
	mm := newM.(*model)

	if cmd == nil {
		t.Fatal("expected clipboard command")
	}
	if got := fmt.Sprint(cmd()); got != "User:\nhello\n\nAssistant:\nhi" {
		t.Errorf("unexpected copied transcript: %q", got)
	}
	if len(mm.chatModel.Messages) != 3 {
		t.Fatalf("expected confirmation message, got %d messages", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[2].content, "Copied") {
		t.Errorf("expected copy confirmation, got %q", mm.chatModel.Messages[2].content)
	}
}

func TestHandleSlashCommandModelShowsRoles(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:  "claude-sonnet-4-6",
			ActiveRole: "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "claude-sonnet-4-6"},
				"smol":    {Model: "gemini-2.5-flash"},
				"slow":    {Model: "claude-opus-4-7", Provider: "anthropic"},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
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

func TestHandleSlashCommandModelSwitch(t *testing.T) {
	newLLM := &stubLLM{name: "claude-sonnet-4-6"}
	m := &model{
		inputModel: InputModel{Text: "/model claude-sonnet-4-6"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:    "gpt-5.6-sol",
			ProviderName: "openai",
			ActiveRole:   "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "gpt-5.6-sol"},
			},
			ModelSwitcher: func(_ context.Context, modelName string) (adkmodel.LLM, string, string, error) {
				if modelName != "claude-sonnet-4-6" {
					t.Errorf("ModelSwitcher received %q, want %q", modelName, "claude-sonnet-4-6")
				}
				return newLLM, modelName, "anthropic", nil
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model claude-sonnet-4-6")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Switched model to **claude-sonnet-4-6**") {
		t.Errorf("expected switch message, got %q", content)
	}
	if !strings.Contains(content, "anthropic") {
		t.Errorf("expected provider in message, got %q", content)
	}
	if mm.cfg.ModelName != "claude-sonnet-4-6" {
		t.Errorf("cfg.ModelName = %q, want %q", mm.cfg.ModelName, "claude-sonnet-4-6")
	}
	if mm.cfg.ProviderName != "anthropic" {
		t.Errorf("cfg.ProviderName = %q, want %q", mm.cfg.ProviderName, "anthropic")
	}
	if mm.cfg.LLM != newLLM {
		t.Errorf("cfg.LLM was not updated")
	}
}

func TestHandleSlashCommandModelSwitchByRole(t *testing.T) {
	newLLM := &stubLLM{name: "gemini-2.5-flash"}
	m := &model{
		inputModel: InputModel{Text: "/model smol"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:    "gpt-5.6-sol",
			ProviderName: "openai",
			ActiveRole:   "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "gpt-5.6-sol"},
				"smol":    {Model: "gemini-2.5-flash"},
			},
			ModelSwitcher: func(_ context.Context, modelName string) (adkmodel.LLM, string, string, error) {
				if modelName != "gemini-2.5-flash" {
					t.Errorf("ModelSwitcher received %q, want %q", modelName, "gemini-2.5-flash")
				}
				return newLLM, modelName, "gemini", nil
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model smol")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	content := mm.chatModel.Messages[0].content
	if !strings.Contains(content, "Switched model to **gemini-2.5-flash**") {
		t.Errorf("expected switch message, got %q", content)
	}
	if !strings.Contains(content, "Role: `smol`") {
		t.Errorf("expected role annotation, got %q", content)
	}
	if mm.cfg.ActiveRole != "smol" {
		t.Errorf("cfg.ActiveRole = %q, want %q", mm.cfg.ActiveRole, "smol")
	}
}

func TestHandleSlashCommandModelSwitchNoSwitcher(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model claude-sonnet-4-6"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:    "gpt-5.6-sol",
			ProviderName: "openai",
			ActiveRole:   "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "gpt-5.6-sol"},
			},
			ModelSwitcher: nil,
		},
	}

	newM, _ := m.handleSlashCommand("/model claude-sonnet-4-6")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandModelSwitchWhileRunning(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model claude-sonnet-4-6"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		running:    true,
		cfg: Config{
			ModelName:    "gpt-5.6-sol",
			ProviderName: "openai",
			ActiveRole:   "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "gpt-5.6-sol"},
			},
			ModelSwitcher: func(_ context.Context, _ string) (adkmodel.LLM, string, string, error) {
				t.Error("ModelSwitcher should not be called while running")
				return nil, "", "", nil
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model claude-sonnet-4-6")
	mm := newM.(*model)

	if !strings.Contains(mm.chatModel.Messages[0].content, "Cannot switch") {
		t.Errorf("expected 'Cannot switch' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandModelSwitchError(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model bad-model"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:    "gpt-5.6-sol",
			ProviderName: "openai",
			ActiveRole:   "default",
			Roles: map[string]config.RoleConfig{
				"default": {Model: "gpt-5.6-sol"},
			},
			ModelSwitcher: func(_ context.Context, _ string) (adkmodel.LLM, string, string, error) {
				return nil, "", "", fmt.Errorf("no API key for provider")
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model bad-model")
	mm := newM.(*model)

	if !strings.Contains(mm.chatModel.Messages[0].content, "Failed to switch model") {
		t.Errorf("expected error message, got %q", mm.chatModel.Messages[0].content)
	}
	if mm.cfg.ModelName != "gpt-5.6-sol" {
		t.Errorf("ModelName should be unchanged on error, got %q", mm.cfg.ModelName)
	}
}

func TestHandleSlashCommandModelSwitchEmptyRoleModel(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/model empty-role"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:    "gpt-5.6-sol",
			ProviderName: "openai",
			ActiveRole:   "default",
			Roles: map[string]config.RoleConfig{
				"default":    {Model: "gpt-5.6-sol"},
				"empty-role": {Model: ""},
			},
			ModelSwitcher: func(_ context.Context, _ string) (adkmodel.LLM, string, string, error) {
				t.Error("ModelSwitcher should not be called for empty model")
				return nil, "", "", nil
			},
		},
	}

	newM, _ := m.handleSlashCommand("/model empty-role")
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.chatModel.Messages))
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "no model configured") {
		t.Errorf("expected 'no model configured' message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleSlashCommandExit(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/exit"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
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
		running: true,
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
		running: true,
		chatModel: ChatModel{
			Streaming: "accumulated text",
			Messages:  make([]message, 0),
		},
		agentCh: make(chan agentMsg, 1),
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
		running:   true,
		chatModel: ChatModel{Messages: make([]message, 0)},
		agentCh:   make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentToolCallMsg{name: "read"})
	mm := newM.(*model)

	if mm.statusModel.ActiveTool != "read" {
		t.Errorf("expected activeTool %q, got %q", "read", mm.statusModel.ActiveTool)
	}
}

func TestAgentToolResultMsg(t *testing.T) {
	m := &model{
		running:     true,
		statusModel: StatusModel{ActiveTool: "read"},
		chatModel:   ChatModel{Messages: make([]message, 0)},
		agentCh:     make(chan agentMsg, 1),
	}

	newM, _ := m.Update(agentToolResultMsg{name: "read"})
	mm := newM.(*model)

	if mm.statusModel.ActiveTool != "" {
		t.Errorf("expected activeTool to be empty, got %q", mm.statusModel.ActiveTool)
	}
}

// Arrow-key history behavior now lives in history_key_test.go: Up opens the
// history window instead of cycling entries into the prompt.

func TestTextInput(t *testing.T) {
	m := &model{
		inputModel: InputModel{},
		chatModel:  ChatModel{Messages: make([]message, 0)},
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
	m := &model{ //nolint:govet // width/height needed for valid model
		width:     80,
		height:    24,
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
	if !strings.Contains(v.Content, "Loading Pi..") || !strings.ContainsAny(v.Content, matrixChars) {
		t.Errorf("expected loading matrix startup line, got %q", v.Content)
	}
}

func TestViewLoadingShowsProgress(t *testing.T) {
	m := &model{
		width:        0,
		height:       0,
		loading:      true,
		loadingItems: map[string]bool{"tools": true, "agent": false},
		loadingTotal: 4,
	}

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Loading Pi [██░░░░░░ 25% 1/4] working: agent..") {
		t.Fatalf("expected loading progress in startup line, got %q", out)
	}
}

func TestViewLoadingShowsInitPipelineStart(t *testing.T) {
	m := &model{
		width:        0,
		height:       0,
		loading:      true,
		loadingItems: map[string]bool{},
	}

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Loading Pi [░░░░░░░░ 0%] starting init pipeline..") {
		t.Fatalf("expected startup line to show init pipeline start, got %q", out)
	}
}

func TestViewLoadingShowsFinalizingInit(t *testing.T) {
	m := &model{
		width:        0,
		height:       0,
		loading:      true,
		loadingItems: map[string]bool{"tools": true, "agent": true},
		loadingTotal: 2,
	}

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Loading Pi [████████ 100% 2/2] finalizing init..") {
		t.Fatalf("expected startup line to explain finalization wait, got %q", out)
	}
}

func TestMaxScrollEmpty(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		height:    24,
	}
	if max := m.chatModel.MaxScroll(m.height); max != 0 {
		t.Errorf("expected 0, got %d", max)
	}
}

func TestHandleSlashCommandSession(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/session"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionID: "test-session-123"},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionService: nil},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionService: svc, SessionID: "s1"},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionService: svc, SessionID: sessionID},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionService: svc, SessionID: sessionID},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionService: svc, SessionID: sessionID},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg:        Config{SessionService: nil},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
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
	// Should match: /clear, /context, /compact, /commit, /copy
	if len(matches) != 5 {
		t.Errorf("expected 5 matches for '/c', got %d: %v", len(matches), matches)
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
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

func TestRenderSlashCommandPopup_AllCommands(t *testing.T) {
	m := &model{inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 24}
	m.inputModel.SetText("/")

	// The unified search popup renders when searchPopup is active.
	m.newSearchPopup(searchModeCommands)

	out := ansi.Strip(m.renderSearchPopup(70))

	if !strings.Contains(out, "Commands") {
		t.Fatalf("expected popup header, got %q", out)
	}
	// Verify some commands are listed (first alphabetical commands).
	for _, cmd := range []string{"/clear", "/commit", "/help"} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, cmd) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected popup to include %q, got %q", cmd, out)
		}
	}
}

func TestSlashCommandPopup_UsesHistoryWindowHeight(t *testing.T) {
	skills := make([]extension.Skill, 30)
	for i := range skills {
		skills[i] = extension.Skill{Name: fmt.Sprintf("skill-%02d", i)}
	}
	m := &model{
		cfg:        Config{Skills: skills},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      80,
		height:     40,
	}
	m.inputModel.SetText("/")
	m.newSearchPopup(searchModeCommands)

	if m.searchPopup == nil {
		t.Fatal("expected search popup")
	}
	if m.searchPopup.height != 25 {
		t.Fatalf("slash command popup height = %d, want history popup max height 25", m.searchPopup.height)
	}
}

func TestSlashCommandCandidates_AllCommandsAlphabetical(t *testing.T) {
	candidates := allSlashCommandCandidates([]extension.Skill{
		{Name: "zeta", Description: "Zeta skill"},
		{Name: "alpha", Description: "Alpha skill"},
	})

	for i := 1; i < len(candidates); i++ {
		prev := strings.ToLower(candidates[i-1].Text)
		curr := strings.ToLower(candidates[i].Text)
		if prev > curr {
			t.Fatalf("slash commands should be alphabetical: %q before %q in %+v", candidates[i-1].Text, candidates[i].Text, candidates)
		}
	}
}

func TestRenderSlashCommandPopup_UsesWiderWindow(t *testing.T) {
	const longDesc = "Search, synthesize, verify, and summarize project notes across local workspaces"
	m := &model{
		cfg: Config{Skills: []extension.Skill{
			{Name: "agent", Description: longDesc},
		}},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      140,
		height:     40,
	}
	m.inputModel.SetText("/")
	m.newSearchPopup(searchModeCommands)

	out := ansi.Strip(m.renderSearchPopup(120))
	lines := strings.Split(out, "\n")
	firstLine := lines[0]

	if len([]rune(firstLine)) < 110 {
		t.Fatalf("expected popup to use wider window, first line was only %d columns: %q", len([]rune(firstLine)), firstLine)
	}
	// Verify /agent line contains the description.
	line := findLineContaining(out, "/agent")
	if line == "" {
		t.Fatalf("expected agent line, got %q", out)
	}
	if !strings.Contains(line, longDesc[:20]) {
		t.Fatalf("expected long description to fit in wider popup, got line %q", line)
	}
}

func TestRenderSlashCommandPopup_CutsDescriptionToSeventyPercentWidth(t *testing.T) {
	const width = 100
	longDesc := strings.Repeat("abcdefghij", 12)
	m := &model{
		cfg: Config{Skills: []extension.Skill{
			{Name: "agent", Description: longDesc},
		}},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      width,
		height:     40,
	}
	m.inputModel.SetText("/")
	m.newSearchPopup(searchModeCommands)

	out := ansi.Strip(m.renderSearchPopup(width))
	line := findLineContaining(out, "/agent")
	if line == "" {
		t.Fatalf("expected agent line, got %q", out)
	}
	desc := renderedSlashDescription(line, "/agent")
	if got, want := len([]rune(desc)), width*50/100; got != want {
		t.Fatalf("rendered description length = %d, want %d: %q from line %q in output %q", got, want, desc, line, out)
	}
}

func findLineContaining(s string, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func renderedSlashDescription(line string, command string) string {
	idx := strings.Index(line, command)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(command):]
	rest = strings.TrimLeft(rest, " ")
	return strings.Trim(rest, " │")
}

func TestRenderSlashCommandPopup_FiltersAsUserTypes(t *testing.T) {
	m := &model{inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 24}
	m.inputModel.SetText("/sk")

	// Create search popup and filter.
	m.newSearchPopup(searchModeCommands)
	m.searchPopup.search = "sk"
	m.searchPopup.filterSearch()

	out := ansi.Strip(m.renderSearchPopup(70))

	// Should show /skills command (filtered by "sk").
	if !strings.Contains(out, "/skills") && !strings.Contains(out, "/skill") {
		t.Fatalf("expected skill commands in filtered popup, got %q", out)
	}
	// /help should not be visible when filtering for "sk".
	if strings.Contains(out, "/help") {
		t.Fatalf("expected filtered popup to omit /help, got %q", out)
	}
}

func TestRenderSlashCommandPopup_HighlightsSelectedCommand(t *testing.T) {
	m := &model{inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 24}
	m.inputModel.SetText("/c")
	m.newSearchPopup(searchModeCommands)

	// Move selection to 1 (second item).
	m.searchPopup.selected = 1

	out := ansi.Strip(m.renderSearchPopup(70))
	// The second item (index 1) should be highlighted with "> ".
	// Check that at least one line contains "> /clear" or similar.
	highlighted := false
	for _, line := range strings.Split(out, "\n") {
		// Use Contains since the line may have extra spaces/pipes.
		if strings.Contains(line, "> /clear") || strings.Contains(line, "> /commit") {
			highlighted = true
			break
		}
	}
	if !highlighted {
		t.Fatalf("expected a highlighted selection in popup, got %q", out)
	}
}

func TestSlashCommandPopup_NavigationKeys(t *testing.T) {
	m := &model{inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 24}
	m.inputModel.SetText("/c")
	m.newSearchPopup(searchModeCommands)

	// Start with selection at 0.
	if m.searchPopup.selected != 0 {
		t.Fatalf("expected initial selected=0, got %d", m.searchPopup.selected)
	}

	// Down moves selection.
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if m.searchPopup.selected != 1 {
		t.Fatalf("Down should move selected command to 1, got %d", m.searchPopup.selected)
	}

	// Up moves selection back.
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if m.searchPopup.selected != 0 {
		t.Fatalf("Up should move selected command back to 0, got %d", m.searchPopup.selected)
	}

	// Tab moves forward.
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if m.searchPopup.selected != 1 {
		t.Fatalf("Tab should move selected command to 1, got %d", m.searchPopup.selected)
	}

	// Shift+Tab moves back.
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if m.searchPopup.selected != 0 {
		t.Fatalf("Shift+Tab should move selected command back to 0, got %d", m.searchPopup.selected)
	}
}

func TestSlashCommandPopup_EnterCopiesSelectedCommandToInput(t *testing.T) {
	m := &model{inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 24}
	m.inputModel.SetText("/c")
	m.newSearchPopup(searchModeCommands)
	m.searchPopup.selected = 1

	_, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil {
		t.Fatal("Enter should copy the selected slash command without submitting")
	}
	// The input should be set to the selected command with a trailing space.
	if !strings.HasSuffix(m.inputModel.Text, " ") {
		t.Fatalf("input text should end with space, got %q", m.inputModel.Text)
	}
}

func TestSlashCommandPopup_OpensWhenSlashTyped(t *testing.T) {
	m := &model{
		inputModel: NewInputModel(nil, nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
		width:      80,
		height:     24,
	}

	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))

	if m.searchPopup == nil {
		t.Fatal("expected slash command popup to open after typing /")
	}
	if m.searchPopup.mode != searchModeCommands {
		t.Fatalf("expected commands popup, got %v", m.searchPopup.mode)
	}
}

// Regression: a previously-scrolled popup whose height grew on resize used to
// keep the old scrollOff, which then overshot the visible window. The
// renderSearchPopup loop indexed past len(filtered) and panicked. The fix
// reconciles scrollOff with the new height so the visible range always
// stays within the slice and the selected item stays in view.
func TestRefreshSearchPopupHeight_ReclampsScrollOff(t *testing.T) {
	// 10 items, short terminal that gave a 3-row popup, user scrolled to
	// the bottom so scrollOff = 7 (selected = 9).
	items := make([]SearchItem, 10)
	for i := range items {
		items[i] = SearchItem{Text: fmt.Sprintf("item-%d", i)}
	}
	m := &model{
		cfg:        Config{},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      80,
		height:     12, // short — small availableRows, small popup height
	}
	m.searchPopup = &searchPopupState{
		mode:      searchModeCommands,
		entries:   items,
		filtered:  items,
		selected:  9,
		search:    "",
		height:    3,
		scrollOff: 7,
	}
	// Simulate a resize to a tall terminal where the popup now fits all 10
	// items, but the old scrollOff=7 is still set.
	m.height = 60
	m.refreshSearchPopupHeight()

	if m.searchPopup.scrollOff+m.searchPopup.height > len(items) {
		t.Errorf("scrollOff+height = %d, want <= %d (filtered len)", m.searchPopup.scrollOff+m.searchPopup.height, len(items))
	}
	if m.searchPopup.selected < m.searchPopup.scrollOff ||
		m.searchPopup.selected >= m.searchPopup.scrollOff+m.searchPopup.height {
		t.Errorf("selected %d not in visible window [%d, %d) after refresh",
			m.searchPopup.selected, m.searchPopup.scrollOff, m.searchPopup.scrollOff+m.searchPopup.height)
	}
	// And rendering must not panic.
	_ = m.renderSearchPopup(70)
}

// Regression: a tall-to-short resize that leaves scrollOff > maxScroll
// must not be allowed to push the visible window past the end of the
// filtered slice.
func TestRefreshSearchPopupHeight_ShrinkClampsOvershoot(t *testing.T) {
	items := make([]SearchItem, 4)
	for i := range items {
		items[i] = SearchItem{Text: fmt.Sprintf("item-%d", i)}
	}
	m := &model{
		cfg:        Config{},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      80,
		height:     60, // tall — popup can show everything
	}
	m.searchPopup = &searchPopupState{
		mode:      searchModeCommands,
		entries:   items,
		filtered:  items,
		selected:  3,
		search:    "",
		height:    4,
		scrollOff: 0,
	}
	// Shrink the terminal so the popup height drops to 1. scrollOff must
	// not be left pointing past the new visible window.
	m.height = 8
	m.refreshSearchPopupHeight()

	if m.searchPopup.scrollOff+m.searchPopup.height > len(items) {
		t.Errorf("scrollOff+height = %d, want <= %d (filtered len)", m.searchPopup.scrollOff+m.searchPopup.height, len(items))
	}
	if m.searchPopup.selected < m.searchPopup.scrollOff ||
		m.searchPopup.selected >= m.searchPopup.scrollOff+m.searchPopup.height {
		t.Errorf("selected %d not in visible window [%d, %d) after refresh",
			m.searchPopup.selected, m.searchPopup.scrollOff, m.searchPopup.scrollOff+m.searchPopup.height)
	}
	_ = m.renderSearchPopup(70)
}

// Regression: even if a stale scrollOff somehow makes it past the resize
// reconciliation, renderSearchPopup must not index past the end of the
// filtered slice and panic. The defense-in-depth break is what catches
// the bug in production even when the upstream guard regresses.
func TestRenderSearchPopup_BoundsScrollOff(t *testing.T) {
	items := make([]SearchItem, 4)
	for i := range items {
		items[i] = SearchItem{Text: fmt.Sprintf("item-%d", i)}
	}
	m := &model{
		cfg:        Config{},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      80,
		height:     24,
	}
	m.searchPopup = &searchPopupState{
		mode:      searchModeCommands,
		entries:   items,
		filtered:  items,
		selected:  0,
		search:    "",
		height:    10,
		scrollOff: 100, // far past the end of filtered
	}
	// Should not panic.
	_ = m.renderSearchPopup(70)
}

// Covers the remaining branches in refreshSearchPopupHeight that the two
// resize tests don't reach: the nil-popup early return, the empty-filtered
// early return, the selected-clamp (selected >= len(filtered)), and the
// upper-clamp (selected < scrollOff).
func TestRefreshSearchPopupHeight_NilAndClamps(t *testing.T) {
	// 1. Nil popup — early-return path, no panic, no work.
	m := &model{
		cfg:        Config{},
		inputModel: NewInputModel(nil, nil, nil, ""),
		width:      80,
		height:     24,
	}
	m.refreshSearchPopupHeight() // no panic with searchPopup == nil

	// 2. Empty filtered list (n == 0) — exercises the inner early-return
	//    path. The popup exists but its filtered slice is nil/empty.
	m.searchPopup = &searchPopupState{
		mode:      searchModeCommands,
		entries:   nil,
		filtered:  nil,
		selected:  5, // would be out of range if not clamped
		search:    "",
		height:    3,
		scrollOff: 2,
	}
	m.refreshSearchPopupHeight()
	if m.searchPopup.selected != 0 || m.searchPopup.scrollOff != 0 {
		t.Errorf("empty filtered: selected=%d scrollOff=%d, want both 0",
			m.searchPopup.selected, m.searchPopup.scrollOff)
	}

	// 3. selected out of range (selected >= len(filtered)) — clamps to n-1.
	items := make([]SearchItem, 3)
	for i := range items {
		items[i] = SearchItem{Text: fmt.Sprintf("i-%d", i)}
	}
	m.searchPopup = &searchPopupState{
		mode:      searchModeCommands,
		entries:   items,
		filtered:  items, // n=3
		selected:  7,     // > 2, must clamp
		search:    "",
		height:    3,
		scrollOff: 0,
	}
	m.refreshSearchPopupHeight()
	if m.searchPopup.selected != 2 {
		t.Errorf("selected-out-of-range: selected=%d, want 2 (n-1)", m.searchPopup.selected)
	}

	// 4. selected < scrollOff — upper-clamp path (selected moves scrollOff).
	m.searchPopup = &searchPopupState{
		mode:      searchModeCommands,
		entries:   items,
		filtered:  items,
		selected:  0, // < scrollOff
		search:    "",
		height:    3,
		scrollOff: 4, // stale
	}
	m.refreshSearchPopupHeight()
	if m.searchPopup.scrollOff != 0 {
		t.Errorf("selected<scrollOff: scrollOff=%d, want 0", m.searchPopup.scrollOff)
	}
}

func TestView_RendersSlashPopupNearInput(t *testing.T) {
	m := newTestModelFull(t)
	m.inputModel.SetText("/co")
	m.newSearchPopup(searchModeCommands)

	out := ansi.Strip(m.View().Content)

	if !strings.Contains(out, "Commands") {
		t.Fatalf("expected slash popup in view, got %q", out)
	}
	if !strings.Contains(out, "/context") || !strings.Contains(out, "/compact") {
		t.Fatalf("expected /co commands in popup, got %q", out)
	}
	popupIndex := strings.LastIndex(out, "Commands")
	inputIndex := strings.LastIndex(out, "> /co")
	if popupIndex < 0 || inputIndex < 0 {
		t.Fatalf("expected popup and input prompt in view, got %q", out)
	}
	if popupIndex > inputIndex {
		t.Fatalf("expected slash popup above input prompt, got %q", out)
	}
}

func TestSearchPopup_DoesNotReduceMessageViewportHeight(t *testing.T) {
	m := newTestModelFull(t)
	before := m.messageViewportHeight()

	m.inputModel.SetText("/co")
	m.newSearchPopup(searchModeCommands)
	after := m.messageViewportHeight()

	if after != before {
		t.Fatalf("search popup should overlay chat without changing message viewport height: before=%d after=%d", before, after)
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []HistoryEntry{{Text: "/help"}, {Text: "/model"}, {Text: "/ping"}, {Text: "/clear"}}},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []HistoryEntry{{Text: "/help"}, {Text: "/model"}, {Text: "/ping"}, {Text: "/plan"}}},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []HistoryEntry{{Text: "/help"}, {Text: "/model"}}},
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
		commit:    &commitState{phase: "committing"},
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
		cfg:         Config{ProviderName: "ollama", ModelName: "qwen3.5:latest"},
		width:       120,
		statusModel: StatusModel{Width: 120},
	}
	// Provider and model now live in the sidebar, not the status bar.
	sidebar := ansi.Strip(RenderSidebar(SidebarRenderInput{
		Width:        SidebarWidth,
		Height:       40,
		ProviderName: m.cfg.ProviderName,
		ModelName:    m.cfg.ModelName,
	}))
	if !strings.Contains(sidebar, "ollama") {
		t.Errorf("sidebar should contain provider, got %q", sidebar)
	}
	if !strings.Contains(sidebar, "qwen3.5:latest") {
		t.Errorf("sidebar should contain model, got %q", sidebar)
	}
	// And they are not in the status bar anymore.
	bar := ansi.Strip(m.statusModel.Render(m.statusRenderInput()))
	if strings.Contains(bar, "ollama") {
		t.Errorf("status bar should not contain provider, got %q", bar)
	}
	if strings.Contains(bar, "qwen3.5:latest") {
		t.Errorf("status bar should not contain model, got %q", bar)
	}
}

func TestRenderStatusBar_WithDirectoryAndHost(t *testing.T) {
	bar := (&StatusModel{Width: 120}).Render(StatusRenderInput{
		ModelName:  "test-model",
		FolderName: "pi-go",
		HostName:   "dev-host",
	})
	if !strings.Contains(ansi.Strip(bar), "pi-go | dev-host") {
		t.Errorf("status bar should contain directory and host, got %q", bar)
	}
}

func TestRenderStatusBar_WithDirectoryOnly(t *testing.T) {
	bar := (&StatusModel{Width: 120}).Render(StatusRenderInput{
		ModelName:  "test-model",
		FolderName: "pi-go",
	})
	if !strings.Contains(bar, "pi-go") {
		t.Errorf("status bar should contain directory, got %q", bar)
	}
	if strings.Contains(bar, " | ") {
		t.Errorf("status bar should not contain separator without host, got %q", bar)
	}
}

func TestRenderStatusBar_WithHostOnly(t *testing.T) {
	bar := (&StatusModel{Width: 120}).Render(StatusRenderInput{
		ModelName: "test-model",
		HostName:  "dev-host",
	})
	if !strings.Contains(bar, "dev-host") {
		t.Errorf("status bar should contain host, got %q", bar)
	}
}

func TestProviderDisplayName_CodexOAuthTokenRelabelsOpenAI(t *testing.T) {
	// Construct a minimal fake codex OAuth JWT: header.payload.sig, where the
	// payload carries the OpenAI auth claim IdentifyKey looks for.
	header := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0"
	// {"https://api.openai.com/auth":{"chatgpt_account_id":"acct-1"}}
	payload := "eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdC0xIn19"
	jwt := header + "." + payload + ".sig"

	t.Setenv("OPENAI_API_KEY", jwt)
	m := &model{cfg: Config{ProviderName: "openai", ModelName: "gpt-5"}}
	if got := m.providerDisplayName(); got != "codex" {
		t.Errorf("providerDisplayName with codex JWT = %q, want %q", got, "codex")
	}
}

func TestProviderDisplayName_PlainAPIKeyStaysOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-proj-abcdef")
	m := &model{cfg: Config{ProviderName: "openai", ModelName: "gpt-5"}}
	if got := m.providerDisplayName(); got != "openai" {
		t.Errorf("providerDisplayName with sk- key = %q, want %q", got, "openai")
	}
}

func TestProviderDisplayName_NonOpenAIUnchanged(t *testing.T) {
	// Even if OPENAI_API_KEY happens to be a JWT, non-openai providers
	// should render their own label.
	t.Setenv("OPENAI_API_KEY", "eyJh.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnt9fQ.sig")
	m := &model{cfg: Config{ProviderName: "anthropic", ModelName: "claude"}}
	if got := m.providerDisplayName(); got != "anthropic" {
		t.Errorf("providerDisplayName for anthropic = %q, want %q", got, "anthropic")
	}
}

func TestRenderStatusBar_WithoutProvider(t *testing.T) {
	m := &model{
		cfg:         Config{ModelName: "gpt-4o"},
		width:       120,
		statusModel: StatusModel{Width: 120},
	}
	// Model now lives in the sidebar, not the status bar or info line.
	sidebar := ansi.Strip(RenderSidebar(SidebarRenderInput{
		Width:     SidebarWidth,
		Height:    40,
		ModelName: m.cfg.ModelName,
	}))
	if !strings.Contains(sidebar, "gpt-4o") {
		t.Errorf("sidebar should contain model, got %q", sidebar)
	}
	bar := ansi.Strip(m.statusModel.Render(m.statusRenderInput()))
	if strings.Contains(bar, "gpt-4o") {
		t.Errorf("status bar should not contain model, got %q", bar)
	}
}

func TestRenderStatusBar_ContextEstimate(t *testing.T) {
	m := &model{
		cfg:         Config{ModelName: "test"},
		width:       120,
		statusModel: StatusModel{Width: 120},
		chatModel: ChatModel{
			Messages: []message{
				{content: strings.Repeat("a", 4000)}, // ~1k tokens
			},
		},
	}
	bar := m.statusModel.Render(m.statusRenderInput())
	if !strings.Contains(bar, "ctx:") {
		t.Errorf("status bar should show context estimate, got %q", bar)
	}
}

func TestMaxScroll_EmptyMessages(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: nil},
		height:    40,
	}
	if m.chatModel.MaxScroll(m.height) != 0 {
		t.Error("maxScroll should be 0 for empty messages")
	}
}

func TestMaxScroll_SmallHeight(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{{content: "test"}}},
		height:    0,
	}
	if m.chatModel.MaxScroll(m.height) != 0 {
		t.Error("maxScroll should be 0 for zero height")
	}
}

func TestHandleSlashCommand_Session(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{SessionID: "test-session-123"},
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
		cfg:       Config{LLM: mockLLM},
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
		chatModel:          ChatModel{Messages: make([]message, 0)},
		pendingSkillCreate: &pendingSkillCreate{name: "test"},
	}
	newM, _ := m.handleSkillCreateCancel()
	mm := newM.(*model)
	if mm.pendingSkillCreate != nil {
		t.Error("pending should be cleared")
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "canceled") {
		t.Errorf("expected canceled message, got %q", mm.chatModel.Messages[0].content)
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
		cfg:       Config{ModelName: "gpt-4o", ActiveRole: "default", Roles: map[string]config.RoleConfig{"default": {Model: "gpt-4o"}}},
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
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []HistoryEntry{{Text: "/help"}, {Text: "/model"}}},
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
		cfg:       Config{},
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

func TestCountAgentsByStatus(t *testing.T) {
	agents := []subagent.AgentStatus{
		{Status: "running"},
		{Status: "running"},
		{Status: "completed"},
		{Status: "failed"},
		{Status: "canceled"},
	}
	running, done, failed := countAgentsByStatus(agents)
	if running != 2 {
		t.Errorf("running = %d, want 2", running)
	}
	if done != 1 {
		t.Errorf("done = %d, want 1", done)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

func TestAgentStatusIcon(t *testing.T) {
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
		if got := agentStatusIcon(tt.status); got != tt.want {
			t.Errorf("agentStatusIcon(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestFormatAgentsList_Empty(t *testing.T) {
	got := formatAgentsList(nil)
	if got != "No subagents have been spawned yet." {
		t.Errorf("got %q", got)
	}
}

func TestFormatAgentsList_WithAgents(t *testing.T) {
	agents := []subagent.AgentStatus{
		{AgentID: "agent-abc12345", Type: "task", Status: "running", Prompt: "do something", Duration: "5s"},
		{AgentID: "agent-def67890", Type: "plan", Status: "completed", Prompt: "plan it", Duration: "10s"},
		{AgentID: "agent-ghi11111", Type: "fix", Status: "failed", Prompt: "fix bug", Duration: "2s"},
	}
	got := formatAgentsList(agents)

	if !strings.Contains(got, "3 total") {
		t.Errorf("missing total count in %q", got)
	}
	if !strings.Contains(got, "1 running") {
		t.Errorf("missing running count")
	}
	if !strings.Contains(got, "1 failed") {
		t.Errorf("missing failed count")
	}
	if !strings.Contains(got, "agent-ab") {
		t.Errorf("missing agent ID prefix")
	}
}

func TestFormatAgentsList_LongPromptTruncation(t *testing.T) {
	agents := []subagent.AgentStatus{
		{AgentID: "agent-abc12345", Type: "task", Status: "running",
			Prompt: strings.Repeat("x", 100), Duration: "1s"},
	}
	got := formatAgentsList(agents)
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncated prompt with '...'")
	}
}

func TestFormatThemeList(t *testing.T) {
	themes := []Theme{
		{Name: "dark", DisplayName: "Dark Theme", ThemeType: "dark"},
		{Name: "light", DisplayName: "Light Theme", ThemeType: "light"},
	}
	got := formatThemeList(themes, "dark", darkPalette)

	if !strings.Contains(got, "active: dark") {
		t.Errorf("missing current theme header")
	}
	if !strings.Contains(got, "☀️") {
		t.Errorf("missing light theme icon")
	}
	if !strings.Contains(got, "🌙") {
		t.Errorf("missing dark theme icon")
	}
	if !strings.Contains(got, "▸ ") {
		t.Errorf("missing current theme marker")
	}
}

func TestFormatThemeError(t *testing.T) {
	got := formatThemeError("darq", []string{"dark", "dracula"})
	if !strings.Contains(got, "darq") {
		t.Errorf("missing theme name in error")
	}
	if !strings.Contains(got, "Did you mean") {
		t.Errorf("missing suggestion")
	}
	if !strings.Contains(got, "dark") {
		t.Errorf("missing match suggestion")
	}

	// No matches
	got2 := formatThemeError("xyz", nil)
	if strings.Contains(got2, "Did you mean") {
		t.Errorf("should not suggest when no matches")
	}
}

func TestMigrateHistoryFormat(t *testing.T) {
	lines := []string{"cmd1", "cmd2", "cmd3"}
	entries := migrateHistoryFormat(lines)

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Text != "cmd1" {
		t.Errorf("first entry = %q, want cmd1", entries[0].Text)
	}
	if entries[2].Text != "cmd3" {
		t.Errorf("third entry = %q, want cmd3", entries[2].Text)
	}
}

func TestTruncateHistory(t *testing.T) {
	entries := make([]HistoryEntry, 50)
	for i := range entries {
		entries[i] = HistoryEntry{Text: fmt.Sprintf("cmd%d", i)}
	}

	result := truncateHistory(entries, 10)
	if len(result) != 10 {
		t.Fatalf("expected 10, got %d", len(result))
	}
	if result[0].Text != "cmd40" {
		t.Errorf("first entry = %q, want cmd40", result[0].Text)
	}

	// Under limit — no truncation
	small := truncateHistory(entries[:5], 10)
	if len(small) != 5 {
		t.Errorf("expected 5, got %d", len(small))
	}
}

func TestFormatHistoryOutput(t *testing.T) {
	entries := []HistoryEntry{
		{Text: "git status"},
		{Text: "go test ./...", Mentions: []string{"@file.go"}},
		{Text: "git diff"},
	}

	// No filter
	got := formatHistoryOutput(entries, "")
	if !strings.Contains(got, "**Command history**") {
		t.Errorf("missing header")
	}
	if !strings.Contains(got, "git status") {
		t.Errorf("missing entry")
	}

	// With filter
	got2 := formatHistoryOutput(entries, "git")
	if !strings.Contains(got2, "**History matching `git`**") {
		t.Errorf("missing filter header")
	}
	if strings.Contains(got2, "go test") {
		t.Errorf("should not include non-matching entry")
	}

	// With mentions
	if !strings.Contains(got, "@file.go") {
		t.Errorf("missing mention in output")
	}

	// No matches
	got3 := formatHistoryOutput(entries, "xyz")
	if !strings.Contains(got3, "No history matching") {
		t.Errorf("expected no-match message, got %q", got3)
	}

	// Empty history
	got4 := formatHistoryOutput(nil, "")
	if !strings.Contains(got4, "No command history") {
		t.Errorf("expected empty history message")
	}
}

func TestRenderWelcome(t *testing.T) {
	renderer, _ := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(80))
	cm := ChatModel{Renderer: renderer}
	got := cm.renderWelcome(darkPalette)
	// Check for key content (some words may be split by ANSI style codes).
	checks := []string{
		"Welcome to pi-go",
		"coding agent",
		"help",
		"commit",
		"plan",
		"Tab",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("welcome screen missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// mascot and eyes helper tests
// ---------------------------------------------------------------------------

func TestMascot_WithFace(t *testing.T) {
	m := &model{
		face: NewFaceRenderer(),
	}
	got := m.mascot()
	// With a face, it delegates to the face renderer
	if got == "" {
		t.Error("mascot() returned empty string with face")
	}
}

func TestMascot_NoFace(t *testing.T) {
	m := &model{face: nil}
	got := m.mascot()
	// Without a face, returns MoodIdle.Mascot()
	if got == "" {
		t.Error("mascot() returned empty string without face")
	}
}

func TestEyes_WithFace(t *testing.T) {
	m := &model{
		face: NewFaceRenderer(),
	}
	got := m.eyes()
	if got == "" {
		t.Error("eyes() returned empty string with face")
	}
}

func TestEyes_NoFace(t *testing.T) {
	m := &model{face: nil}
	got := m.eyes()
	// Without a face, returns MoodIdle.Eyes()
	if got == "" {
		t.Error("eyes() returned empty string without face")
	}
}

func TestEyes_IdleMood(t *testing.T) {
	m := &model{
		face: nil,
	}
	got := m.eyes()
	if got == "" {
		t.Error("eyes() returned empty for nil face (uses MoodIdle)")
	}
}

// ---------------------------------------------------------------------------
// countUntrackedLines tests
// ---------------------------------------------------------------------------

func TestCountUntrackedLines_GitError(t *testing.T) {
	// Calling with a directory that isn't a git repo returns 0
	got := countUntrackedLines("/nonexistent/path")
	if got != 0 {
		t.Errorf("countUntrackedLines(non-git-dir) = %d, want 0", got)
	}
}

func TestCountUntrackedLines_EmptyRepo(t *testing.T) {
	// Create a temp git repo with no untracked files
	tmp := t.TempDir()

	// Initialize git repo
	runGit(t, tmp, "init")
	runGit(t, tmp, "config", "user.email", "test@test.com")
	runGit(t, tmp, "config", "user.name", "Test")

	got := countUntrackedLines(tmp)
	if got != 0 {
		t.Errorf("countUntrackedLines(empty repo) = %d, want 0", got)
	}
}

func TestCountUntrackedLines_WithUntrackedFiles(t *testing.T) {
	tmp := t.TempDir()
	runGit(t, tmp, "init")
	runGit(t, tmp, "config", "user.email", "test@test.com")
	runGit(t, tmp, "config", "user.name", "Test")

	// Create untracked files with newlines using sh -c with echo
	// echo adds trailing newline, so wc -l will count correctly
	cmd := exec.Command("sh", "-c", "echo -e 'line1\\nline2\\nline3' > untracked1.txt")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Logf("creating untracked1.txt: %v", err)
	}
	cmd = exec.Command("sh", "-c", "echo -e 'lineA\\nlineB\\nlineC' > untracked2.txt")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Logf("creating untracked2.txt: %v", err)
	}

	got := countUntrackedLines(tmp)
	// 3 + 3 = 6 lines total
	if got != 6 {
		t.Errorf("countUntrackedLines() = %d, want 6", got)
	}
}

// runGit runs a git command in the specified directory.
func runGit(t *testing.T, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Logf("git %v in %s: %v", args, dir, err)
	}
}
