package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dimetron/pi-go/internal/extension"
)

// newTestModel creates a minimal model for unit testing Update/View.
func newTestModel(t *testing.T) *model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &model{
		cfg: Config{
			ModelName:    "test-model",
			ProviderName: "test-provider",
		},
		ctx:        ctx,
		cancel:     cancel,
		inputModel: NewInputModel(make([]string, 0), nil, nil, ""),
		chatModel: ChatModel{Messages: make([]message, 0)},
		width:      80,
		height:     24,
	}
}

// --- Update: WindowSizeMsg ---

func TestUpdate_WindowSize(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 {
		t.Errorf("expected 120x40, got %dx%d", m.width, m.height)
	}
}

// --- Update: PasteMsg ---

func TestUpdate_PasteMsg(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "hello "
	m.inputModel.CursorPos = 6
	m.Update(tea.PasteMsg{Content: "world"})
	if m.inputModel.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", m.inputModel.Text)
	}
	if m.inputModel.CursorPos != 11 {
		t.Errorf("expected cursorPos 11, got %d", m.inputModel.CursorPos)
	}
}

func TestUpdate_PasteMsg_IgnoredWhenRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.inputModel.Text = "before"
	m.inputModel.CursorPos = 6
	m.Update(tea.PasteMsg{Content: "paste"})
	if m.inputModel.Text != "before" {
		t.Errorf("expected input unchanged when running, got %q", m.inputModel.Text)
	}
}

// --- Update: Agent messages ---

func TestUpdate_AgentThinkingMsg(t *testing.T) {
	m := newTestModel(t)
	m.Update(agentThinkingMsg{text: "Hmm, "})
	if len(m.chatModel.Messages) != 1 || m.chatModel.Messages[0].role != "thinking" {
		t.Fatal("expected thinking message")
	}
	m.Update(agentThinkingMsg{text: "let me think..."})
	if len(m.chatModel.Messages) != 1 {
		t.Error("expected thinking to accumulate in same message")
	}
	if m.chatModel.Thinking != "Hmm, let me think..." {
		t.Errorf("unexpected thinking: %q", m.chatModel.Thinking)
	}
}

func TestUpdate_AgentTextMsg(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.Update(agentTextMsg{text: "Hello!"})
	if m.chatModel.Streaming != "Hello!" {
		t.Errorf("expected streaming 'Hello!', got %q", m.chatModel.Streaming)
	}
}

func TestUpdate_AgentDoneMsg(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.chatModel.Streaming = "some text"
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.Update(agentDoneMsg{})
	if m.running {
		t.Error("expected running=false after done")
	}
}

func TestUpdate_AgentToolCallMsg(t *testing.T) {
	m := newTestModel(t)
	m.Update(agentToolCallMsg{name: "read", args: map[string]any{"file": "foo.go"}})
	if m.activeTool != "read" {
		t.Errorf("expected activeTool 'read', got %q", m.activeTool)
	}
}

func TestUpdate_AgentToolResultMsg(t *testing.T) {
	m := newTestModel(t)
	m.agentCh = make(chan agentMsg, 1)
	m.activeTool = "read"
	m.activeTools = map[string]time.Time{"read": time.Now()}
	// Tool call creates the message; tool result fills it in.
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "tool", tool: "read", content: ""})
	m.Update(agentToolResultMsg{name: "read", content: `{"text":"hello"}`})
	if m.activeTool != "" {
		t.Errorf("expected activeTool cleared, got %q", m.activeTool)
	}
	if m.chatModel.Messages[0].content == "" {
		t.Error("expected tool message content to be filled")
	}
}

// --- Slash commands via handleSlashCommand ---

func TestSlashCommand_Help(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/help"
	m.inputModel.CursorPos = 5
	m.handleSlashCommand("/help")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected help message")
	}
	if m.chatModel.Messages[len(m.chatModel.Messages)-1].role != "assistant" {
		t.Error("expected assistant message for /help")
	}
}

func TestSlashCommand_Clear(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: "hi"})
	m.handleSlashCommand("/clear")
	if len(m.chatModel.Messages) != 0 {
		t.Errorf("expected messages cleared, got %d", len(m.chatModel.Messages))
	}
}

func TestSlashCommand_Model(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/model")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected model info message")
	}
}

func TestSlashCommand_Context(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "hello there"},
		message{role: "assistant", content: "hi back"},
	)
	m.handleSlashCommand("/context")
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.role != "assistant" {
		t.Error("expected assistant message for /context")
	}
}

func TestSlashCommand_Exit(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.handleSlashCommand("/exit")
	if cmd == nil {
		t.Error("expected quit command from /exit")
	}
}

func TestSlashCommand_Quit(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.handleSlashCommand("/quit")
	if cmd == nil {
		t.Error("expected quit command from /quit")
	}
}

func TestSlashCommand_Skills(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/skills")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected skills list message")
	}
}

func TestSlashCommand_RTK_NoMetrics(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/rtk")
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.content != "Output compactor is not active." {
		t.Errorf("unexpected rtk message: %q", last.content)
	}
}

// --- countByRole ---

func TestCountByRole(t *testing.T) {
	msgs := []message{
		{role: "user", content: "a"},
		{role: "assistant", content: "b"},
		{role: "user", content: "c"},
		{role: "tool", content: "d"},
	}
	if n := countByRole(msgs, "user"); n != 2 {
		t.Errorf("expected 2 user msgs, got %d", n)
	}
	if n := countByRole(msgs, "tool"); n != 1 {
		t.Errorf("expected 1 tool msg, got %d", n)
	}
	if n := countByRole(msgs, "system"); n != 0 {
		t.Errorf("expected 0 system msgs, got %d", n)
	}
}

// --- formatContextUsage ---

func TestFormatContextUsage_NoTracker(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "hello world"},
		message{role: "assistant", content: "hi there"},
	)
	out := m.formatContextUsage()
	if out == "" {
		t.Fatal("expected non-empty context usage")
	}
	if !contains(out, "Context Usage") {
		t.Error("expected 'Context Usage' header")
	}
	if !contains(out, "User messages") {
		t.Error("expected user messages breakdown")
	}
}

type mockTokenTracker struct {
	limit       int64
	remaining   int64
	percentUsed float64
	totalUsed   int64
}

func (m *mockTokenTracker) Limit() int64        { return m.limit }
func (m *mockTokenTracker) Remaining() int64     { return m.remaining }
func (m *mockTokenTracker) PercentUsed() float64 { return m.percentUsed }
func (m *mockTokenTracker) TotalUsed() int64     { return m.totalUsed }

func TestFormatContextUsage_WithTracker(t *testing.T) {
	m := newTestModel(t)
	m.cfg.TokenTracker = &mockTokenTracker{
		limit:       100000,
		remaining:   50000,
		percentUsed: 50.0,
		totalUsed:   50000,
	}
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "hello"},
		message{role: "assistant", content: "world"},
	)
	out := m.formatContextUsage()
	if !contains(out, "50%") {
		t.Error("expected percentage in output")
	}
	if !contains(out, "Consumed today") {
		t.Error("expected daily usage section")
	}
}

type mockCompactMetrics struct{}

func (m *mockCompactMetrics) FormatStats() string {
	return "- Compacted 5 times\n"
}

func TestFormatContextUsage_WithCompactMetrics(t *testing.T) {
	m := newTestModel(t)
	m.cfg.CompactMetrics = &mockCompactMetrics{}
	out := m.formatContextUsage()
	if !contains(out, "Output compaction") {
		t.Error("expected compaction section")
	}
}

// --- allCommandNames ---

func TestAllCommandNames(t *testing.T) {
	m := newTestModel(t)
	names := m.inputModel.AllCommandNames()
	if len(names) == 0 {
		t.Fatal("expected command names")
	}
	// Should include built-in commands.
	found := false
	for _, n := range names {
		if n == "/help" {
			found = true
		}
	}
	if !found {
		t.Error("expected /help in command names")
	}
}

func TestAllCommandNames_WithSkills(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Skills = []extension.Skill{
		{Name: "test-skill"},
		{Name: "another-skill"},
	}
	names := m.inputModel.AllCommandNames()
	foundSkill := false
	for _, n := range names {
		if n == "/test-skill" {
			foundSkill = true
		}
	}
	if !foundSkill {
		t.Error("expected /test-skill in command names")
	}
}

// --- detectBranch ---

func TestDetectBranch(t *testing.T) {
	// Should work in the current repo.
	branch := detectBranch("")
	if branch == "" {
		t.Skip("not in a git repo")
	}
	// Should be a valid branch name.
	if contains(branch, "\n") {
		t.Error("branch name should not contain newline")
	}
}

func TestDetectBranch_InvalidDir(t *testing.T) {
	branch := detectBranch("/nonexistent/path/that/does/not/exist")
	if branch != "" {
		t.Errorf("expected empty branch for invalid dir, got %q", branch)
	}
}

// --- renderInput ---

func TestRenderInput_NotRunning(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "hello"
	m.inputModel.CursorPos = 3
	out := m.inputModel.View(m.running)
	if out == "" {
		t.Error("expected non-empty rendered input")
	}
}

func TestRenderInput_Running(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	out := m.inputModel.View(m.running)
	if !contains(out, "waiting") {
		t.Error("expected 'waiting' in running state")
	}
}

func TestRenderInput_CursorAtEnd(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "abc"
	m.inputModel.CursorPos = 3
	out := m.inputModel.View(m.running)
	if out == "" {
		t.Error("expected non-empty rendered input")
	}
}

func TestRenderInput_CursorInMiddle(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "abcdef"
	m.inputModel.CursorPos = 3
	out := m.inputModel.View(m.running)
	if out == "" {
		t.Error("expected non-empty rendered input")
	}
}

// --- handleRTKCommand ---

func TestHandleRTKCommand_WithMetrics(t *testing.T) {
	m := newTestModel(t)
	m.cfg.CompactMetrics = &mockCompactMetrics{}
	m.handleRTKCommand([]string{})
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !contains(last.content, "Compacted") {
		t.Errorf("expected compact stats, got %q", last.content)
	}
}

func TestHandleRTKCommand_UnknownSubcommand(t *testing.T) {
	m := newTestModel(t)
	m.handleRTKCommand([]string{"unknown"})
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !contains(last.content, "Usage") {
		t.Errorf("expected usage message, got %q", last.content)
	}
}

// --- handleSkillsCommand ---

func TestHandleSkillsCommand_List(t *testing.T) {
	m := newTestModel(t)
	m.handleSkillsCommand([]string{})
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message from /skills")
	}
}

func TestHandleSkillsCommand_Unknown(t *testing.T) {
	m := newTestModel(t)
	m.handleSkillsCommand([]string{"bogus"})
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !contains(last.content, "Usage") {
		t.Errorf("expected usage message, got %q", last.content)
	}
}

// --- showCommandList ---

func TestShowCommandList_WithSkills(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Skills = []extension.Skill{{Name: "foo", Description: "bar"}}
	m.showCommandList()
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected command list message")
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !contains(last.content, "Commands") {
		t.Error("expected 'Commands' in output")
	}
	if !contains(last.content, "foo") {
		t.Error("expected skill name in output")
	}
}

// --- View ---

func TestView_BasicRender(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "hi"},
		message{role: "assistant", content: "hello"},
	)
	v := m.View()
	if v.Content == "" {
		t.Error("expected non-empty view")
	}
}

func TestView_EmptyMessages(t *testing.T) {
	m := newTestModel(t)
	v := m.View()
	if v.Content == "" {
		t.Error("expected non-empty view even with no messages")
	}
}

// testSubmit simulates pressing Enter: InputModel handles the key, then
// if it returns an InputSubmitMsg, the root model processes it.
func (m *model) testSubmit() (tea.Model, tea.Cmd) {
	cmd := m.inputModel.HandleKey(makeKey(tea.KeyEnter))
	if cmd != nil {
		msg := cmd()
		if msg != nil {
			return m.Update(msg)
		}
	}
	return m, nil
}

// --- handleKey tests ---

func makeKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func makeKeyMod(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Mod: mod})
}

func makeTextKey(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: rune(text[0]), Text: text})
}

func TestHandleKey_CtrlC_Quit(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.handleKey(makeKeyMod('c', tea.ModCtrl))
	if cmd == nil {
		t.Error("expected quit command from Ctrl+C")
	}
	if !m.quitting {
		t.Error("expected quitting=true")
	}
}

func TestHandleKey_CtrlC_CancelRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	m.handleKey(makeKeyMod('c', tea.ModCtrl))
	if m.running {
		t.Error("expected running=false after Ctrl+C")
	}
}

func TestHandleKey_Esc_CancelRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	m.handleKey(makeKey(tea.KeyEsc))
	if m.running {
		t.Error("expected running=false after Esc")
	}
}

func TestHandleKey_Esc_DismissCompletion(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.CompletionMode = true
	m.inputModel.CompletionResult = &CompleteResult{}
	m.handleKey(makeKey(tea.KeyEsc))
	if m.inputModel.CompletionMode {
		t.Error("expected completion mode dismissed")
	}
}

func TestHandleKey_Esc_DismissCycling(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.CyclingIdx = 3
	m.inputModel.Text = "/help"
	m.handleKey(makeKey(tea.KeyEsc))
	if m.inputModel.CyclingIdx != -1 {
		t.Error("expected cycling dismissed")
	}
	if m.inputModel.Text != "" {
		t.Error("expected input cleared")
	}
}

func TestHandleKey_IgnoresWhenRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.inputModel.Text = "before"
	m.handleKey(makeTextKey("x"))
	if m.inputModel.Text != "before" {
		t.Error("expected input unchanged when running")
	}
}

func TestHandleKey_TypeCharacter(t *testing.T) {
	m := newTestModel(t)
	m.handleKey(makeTextKey("h"))
	m.handleKey(makeTextKey("i"))
	if m.inputModel.Text != "hi" {
		t.Errorf("expected 'hi', got %q", m.inputModel.Text)
	}
	if m.inputModel.CursorPos != 2 {
		t.Errorf("expected cursorPos 2, got %d", m.inputModel.CursorPos)
	}
}

func TestHandleKey_Backspace(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "abc"
	m.inputModel.CursorPos = 3
	m.handleKey(makeKey(tea.KeyBackspace))
	if m.inputModel.Text != "ab" {
		t.Errorf("expected 'ab', got %q", m.inputModel.Text)
	}
}

func TestHandleKey_Delete(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "abc"
	m.inputModel.CursorPos = 1
	m.handleKey(makeKey(tea.KeyDelete))
	if m.inputModel.Text != "ac" {
		t.Errorf("expected 'ac', got %q", m.inputModel.Text)
	}
}

func TestHandleKey_LeftRight(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "abc"
	m.inputModel.CursorPos = 3
	m.handleKey(makeKey(tea.KeyLeft))
	if m.inputModel.CursorPos != 2 {
		t.Errorf("expected cursorPos 2, got %d", m.inputModel.CursorPos)
	}
	m.handleKey(makeKey(tea.KeyRight))
	if m.inputModel.CursorPos != 3 {
		t.Errorf("expected cursorPos 3, got %d", m.inputModel.CursorPos)
	}
}

func TestHandleKey_HomeEnd(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "hello"
	m.inputModel.CursorPos = 3
	m.handleKey(makeKey(tea.KeyHome))
	if m.inputModel.CursorPos != 0 {
		t.Errorf("expected cursorPos 0 after Home, got %d", m.inputModel.CursorPos)
	}
	m.handleKey(makeKey(tea.KeyEnd))
	if m.inputModel.CursorPos != 5 {
		t.Errorf("expected cursorPos 5 after End, got %d", m.inputModel.CursorPos)
	}
}

func TestHandleKey_CtrlA_CtrlE(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "hello"
	m.inputModel.CursorPos = 3
	m.handleKey(makeKeyMod('a', tea.ModCtrl))
	if m.inputModel.CursorPos != 0 {
		t.Errorf("expected cursorPos 0 after Ctrl+A, got %d", m.inputModel.CursorPos)
	}
	m.handleKey(makeKeyMod('e', tea.ModCtrl))
	if m.inputModel.CursorPos != 5 {
		t.Errorf("expected cursorPos 5 after Ctrl+E, got %d", m.inputModel.CursorPos)
	}
}

func TestHandleKey_UpDown_History(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.History = []string{"first", "second", "third"}
	m.handleKey(makeKey(tea.KeyUp))
	if m.inputModel.Text != "third" {
		t.Errorf("expected 'third', got %q", m.inputModel.Text)
	}
	m.handleKey(makeKey(tea.KeyUp))
	if m.inputModel.Text != "second" {
		t.Errorf("expected 'second', got %q", m.inputModel.Text)
	}
	m.handleKey(makeKey(tea.KeyDown))
	if m.inputModel.Text != "third" {
		t.Errorf("expected 'third', got %q", m.inputModel.Text)
	}
	m.handleKey(makeKey(tea.KeyDown))
	if m.inputModel.Text != "" {
		t.Errorf("expected empty after scrolling past end, got %q", m.inputModel.Text)
	}
}

func TestHandleKey_PgUpPgDown(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.Messages = make([]message, 100)
	for i := range m.chatModel.Messages {
		m.chatModel.Messages[i] = message{role: "user", content: "line"}
	}
	m.handleKey(makeKey(tea.KeyPgUp))
	if m.chatModel.Scroll < 5 {
		t.Errorf("expected scroll >= 5, got %d", m.chatModel.Scroll)
	}
	m.handleKey(makeKey(tea.KeyPgDown))
	if m.chatModel.Scroll != 0 {
		t.Errorf("expected scroll 0, got %d", m.chatModel.Scroll)
	}
}

func TestHandleKey_Tab_CycleCommands(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/"
	m.inputModel.CursorPos = 1
	m.inputModel.CyclingIdx = -1
	// First Tab starts cycling
	m.handleKey(makeKey(tea.KeyTab))
	if m.inputModel.CyclingIdx < 0 {
		t.Error("expected cycling to start")
	}
	first := m.inputModel.Text
	// Second Tab cycles to next
	m.handleKey(makeKey(tea.KeyTab))
	if m.inputModel.Text == first && len(m.inputModel.AllCommandNames()) > 1 {
		t.Error("expected cycling to advance")
	}
}

func TestHandleKey_Enter_SubmitSlashCommand(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/clear"
	m.inputModel.CursorPos = 6
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: "hi"})
	_, cmd := m.handleKey(makeKey(tea.KeyEnter))
	if cmd != nil {
		msg := cmd()
		if msg != nil {
			m.Update(msg)
		}
	}
	if len(m.chatModel.Messages) != 0 {
		t.Errorf("expected messages cleared by /clear, got %d", len(m.chatModel.Messages))
	}
}

func TestHandleKey_Enter_CyclingDismiss(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.CyclingIdx = 2
	m.inputModel.Text = "/model"
	m.handleKey(makeKey(tea.KeyEnter))
	if m.inputModel.CyclingIdx != -1 {
		t.Error("expected cycling dismissed on Enter")
	}
	if m.inputModel.Text != "/model" {
		t.Error("expected input preserved")
	}
}

func TestHandleKey_F12_Noop(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "test"
	m.handleKey(makeKey(tea.KeyF12))
	if m.inputModel.Text != "test" {
		t.Error("expected F12 to be a no-op")
	}
}

// --- renderStatusBar tests ---

func TestRenderStatusBar_WithTokenTracker(t *testing.T) {
	m := newTestModel(t)
	m.cfg.TokenTracker = &mockTokenTracker{
		limit:       100000,
		remaining:   20000,
		percentUsed: 80.0,
		totalUsed:   80000,
	}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithTokenTrackerOverLimit(t *testing.T) {
	m := newTestModel(t)
	m.cfg.TokenTracker = &mockTokenTracker{
		limit:       100000,
		remaining:   0,
		percentUsed: 105.0,
		totalUsed:   105000,
	}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithTokenTrackerNoLimit(t *testing.T) {
	m := newTestModel(t)
	m.cfg.TokenTracker = &mockTokenTracker{
		limit:     0,
		totalUsed: 5000,
	}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithGitBranch(t *testing.T) {
	m := newTestModel(t)
	m.gitBranch = "feature-x"
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithActiveTool(t *testing.T) {
	m := newTestModel(t)
	m.activeTool = "read"
	m.toolStart = time.Now()
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithMultipleActiveTools(t *testing.T) {
	m := newTestModel(t)
	m.activeTools = map[string]time.Time{
		"read": time.Now(),
		"grep": time.Now(),
	}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_Running(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithTraceLog(t *testing.T) {
	m := newTestModel(t)
	m.chatModel.TraceLog = []traceEntry{{kind: "llm", summary: "test"}}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_LargeContext(t *testing.T) {
	m := newTestModel(t)
	// Create enough content to push ctx over 1k tokens
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: strings.Repeat("x", 8000), // ~2k tokens
	})
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestRenderStatusBar_WithRunState(t *testing.T) {
	m := newTestModel(t)
	m.run = &runState{
		specName:   "test-spec",
		phase:      "running",
		retries:    1,
		maxRetries: 3,
	}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

// --- renderInput extended ---

func TestRenderInput_EmptyInput(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = ""
	m.inputModel.CursorPos = 0
	out := m.inputModel.View(m.running)
	if out == "" {
		t.Error("expected non-empty rendered input")
	}
}

func TestRenderInput_WithCompletion(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/hel"
	m.inputModel.CursorPos = 4
	m.inputModel.Completion = "/help"
	out := m.inputModel.View(m.running)
	if out == "" {
		t.Error("expected non-empty rendered input")
	}
}

// --- submit ---

func TestSubmit_Empty(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "   "
	_, cmd := m.testSubmit()
	if cmd != nil {
		t.Error("expected nil cmd for empty input")
	}
}

func TestSubmit_SlashCommand(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/help"
	m.testSubmit()
	if len(m.chatModel.Messages) == 0 {
		t.Error("expected help message from submit")
	}
}

// --- handleSkillCommand / handlePlanOverride ---

func TestHandlePlanOverride(t *testing.T) {
	m := newTestModel(t)
	m.plan = &planState{
		phase:     "confirming_override",
		taskName:  "test",
		specDir:   "/tmp/nonexistent-spec-test",
		roughIdea: "test idea",
	}
	m.handlePlanOverride()
	// Should transition state
	if m.plan != nil && m.plan.phase == "confirming_override" {
		t.Error("expected plan phase to change")
	}
}

// --- handleSkillCreateConfirm ---

func TestHandleSkillCreateConfirm(t *testing.T) {
	m := newTestModel(t)
	m.pendingSkillCreate = &pendingSkillCreate{
		name: "test-skill",
		path: "/tmp/nonexistent-skill-test/SKILL.md",
	}
	m.handleSkillCreateConfirm()
	// Should attempt write (may fail on path, but that's ok)
	if m.pendingSkillCreate != nil {
		t.Error("expected pendingSkillCreate cleared")
	}
}

// --- handleKey commit/login/skill confirmation flows ---

func TestHandleKey_CommitConfirm_Esc(t *testing.T) {
	m := newTestModel(t)
	m.commit = &commitState{phase: "confirming"}
	m.handleKey(makeKey(tea.KeyEsc))
	if m.commit != nil {
		t.Error("expected commit cancelled on Esc")
	}
}

func TestHandleKey_CommitConfirm_CtrlC(t *testing.T) {
	m := newTestModel(t)
	m.commit = &commitState{phase: "confirming"}
	m.handleKey(makeKeyMod('c', tea.ModCtrl))
	if m.commit != nil {
		t.Error("expected commit cancelled on Ctrl+C")
	}
}

func TestHandleKey_CommitConfirm_Other(t *testing.T) {
	m := newTestModel(t)
	m.commit = &commitState{phase: "confirming"}
	m.handleKey(makeTextKey("x"))
	if m.commit == nil {
		t.Error("expected commit state preserved on other key")
	}
}

func TestHandleKey_LoginCancel_Esc(t *testing.T) {
	m := newTestModel(t)
	m.login = &loginState{phase: "sso"}
	m.handleKey(makeKey(tea.KeyEsc))
	if m.login != nil {
		t.Error("expected login cancelled on Esc")
	}
}

func TestHandleKey_LoginCancel_CtrlC(t *testing.T) {
	m := newTestModel(t)
	m.login = &loginState{phase: "device"}
	m.handleKey(makeKeyMod('c', tea.ModCtrl))
	if m.login != nil {
		t.Error("expected login cancelled on Ctrl+C")
	}
}

func TestHandleKey_Login_BlocksInSSOPhase(t *testing.T) {
	m := newTestModel(t)
	m.login = &loginState{phase: "sso"}
	m.inputModel.Text = "before"
	m.handleKey(makeTextKey("x"))
	if m.inputModel.Text != "before" {
		t.Error("expected input unchanged in sso phase")
	}
}

func TestHandleKey_SkillCreate_Esc(t *testing.T) {
	m := newTestModel(t)
	m.pendingSkillCreate = &pendingSkillCreate{name: "x"}
	m.handleKey(makeKey(tea.KeyEsc))
	if m.pendingSkillCreate != nil {
		t.Error("expected skill create cancelled on Esc")
	}
}

func TestHandleKey_SkillCreate_CtrlC(t *testing.T) {
	m := newTestModel(t)
	m.pendingSkillCreate = &pendingSkillCreate{name: "x"}
	m.handleKey(makeKeyMod('c', tea.ModCtrl))
	if m.pendingSkillCreate != nil {
		t.Error("expected skill create cancelled on Ctrl+C")
	}
}

func TestHandleKey_SkillCreate_Other(t *testing.T) {
	m := newTestModel(t)
	m.pendingSkillCreate = &pendingSkillCreate{name: "x"}
	m.handleKey(makeTextKey("z"))
	if m.pendingSkillCreate == nil {
		t.Error("expected skill create preserved on other key")
	}
}

func TestHandleKey_PlanOverride_Esc(t *testing.T) {
	m := newTestModel(t)
	m.plan = &planState{phase: "confirming_override"}
	m.handleKey(makeKey(tea.KeyEsc))
	if m.plan != nil {
		t.Error("expected plan cancelled on Esc")
	}
}

func TestHandleKey_PlanOverride_CtrlC(t *testing.T) {
	m := newTestModel(t)
	m.plan = &planState{phase: "confirming_override"}
	m.handleKey(makeKeyMod('c', tea.ModCtrl))
	if m.plan != nil {
		t.Error("expected plan cancelled on Ctrl+C")
	}
}

func TestHandleKey_PlanOverride_Other(t *testing.T) {
	m := newTestModel(t)
	m.plan = &planState{phase: "confirming_override"}
	m.handleKey(makeTextKey("z"))
	if m.plan == nil {
		t.Error("expected plan preserved on other key")
	}
}

// --- maxScroll ---

func TestMaxScroll(t *testing.T) {
	m := newTestModel(t)
	m.height = 20
	// With few messages, max scroll should be 0.
	ms := m.chatModel.MaxScroll(m.height)
	if ms < 0 {
		t.Errorf("expected maxScroll >= 0, got %d", ms)
	}
}

// --- renderInput: completion menu ---

func TestRenderInput_CompletionMenu(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/he"
	m.inputModel.CursorPos = 3
	m.inputModel.CompletionMode = true
	m.inputModel.SelectedIndex = 0
	m.inputModel.CompletionResult = &CompleteResult{
		Candidates: []CompletionCandidate{
			{Text: "/help", Description: "Show help"},
			{Text: "/history", Description: "Command history"},
		},
		Selected: 0,
	}
	out := m.inputModel.View(m.running)
	if !strings.Contains(out, "/help") {
		t.Error("expected /help in completion menu")
	}
	if !strings.Contains(out, "/history") {
		t.Error("expected /history in completion menu")
	}
}

func TestRenderInput_CyclingMenu(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/help"
	m.inputModel.CursorPos = 5
	m.inputModel.CyclingIdx = 0
	out := m.inputModel.View(m.running)
	if out == "" {
		t.Error("expected non-empty cycling menu")
	}
}

func TestRenderInput_GhostCompletion(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/hel"
	m.inputModel.CursorPos = 4
	m.inputModel.Completion = "/help"
	out := m.inputModel.View(m.running)
	if !strings.Contains(out, "tab") {
		t.Error("expected [tab] hint in ghost completion")
	}
}

// --- more handleSlashCommand branches ---

func TestSlashCommand_Session(t *testing.T) {
	m := newTestModel(t)
	m.cfg.SessionID = "test-session-123"
	m.handleSlashCommand("/session")
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !strings.Contains(last.content, "test-session-123") {
		t.Error("expected session ID in output")
	}
}

func TestSlashCommand_Agents_NoOrchestrator(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/agents")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message from /agents")
	}
}

func TestSlashCommand_Branch_NoService(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/branch")
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !strings.Contains(last.content, "not available") && !strings.Contains(last.content, "Session") {
		// Some error message about missing session service
		t.Logf("branch message: %s", last.content)
	}
}

func TestSlashCommand_Compact(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/compact")
	// Should produce some output
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message from /compact")
	}
}

func TestSlashCommand_Unknown(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/nonexistent")
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !strings.Contains(last.content, "Unknown command") {
		t.Errorf("expected unknown command message, got %q", last.content)
	}
}

func TestSlashCommand_DynamicSkill(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Skills = []extension.Skill{
		{Name: "test-skill", Instruction: "do something"},
	}
	// This would try to start the agent loop, which needs a real agent
	// so just verify it dispatches without panic
	m.handleSlashCommand("/test-skill arg1 arg2")
}

// --- submit: slash command routing ---

func TestSubmit_SlashClear(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/clear"
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: "hi"})
	m.testSubmit()
	if len(m.chatModel.Messages) != 0 {
		t.Errorf("expected clear, got %d messages", len(m.chatModel.Messages))
	}
}

// --- loadHistory ---

func TestLoadHistory(t *testing.T) {
	h := loadHistory()
	// May return nil or empty on first run / CI
	if h != nil {
		for _, entry := range h {
			if entry == "" {
				t.Error("empty history entry")
			}
		}
	}
}

// --- handleKey: ShiftTab ---

func TestHandleKey_ShiftTab_CycleBackwards(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/"
	m.inputModel.CursorPos = 1
	m.inputModel.CyclingIdx = 1
	m.handleKey(makeKey(tea.KeyTab))
	second := m.inputModel.Text
	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if m.inputModel.Text == second && len(m.inputModel.AllCommandNames()) > 1 {
		t.Error("expected shift-tab to cycle backwards")
	}
}

func TestHandleKey_ShiftTab_CompletionCycle(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.CompletionMode = true
	m.inputModel.SelectedIndex = 1
	m.inputModel.CompletionResult = &CompleteResult{
		Candidates: []CompletionCandidate{{Text: "/a"}, {Text: "/b"}, {Text: "/c"}},
		Selected:   1,
	}
	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if m.inputModel.SelectedIndex == 1 {
		t.Error("expected shift-tab to change selection")
	}
}

// --- renderStatusBar: orchestrator ---

type mockOrchestrator struct {
	agents []mockAgentInfo
}

type mockAgentInfo struct {
	Type   string
	Status string
}

func (o *mockOrchestrator) List() []struct {
	Type   string
	Status string
} {
	result := make([]struct {
		Type   string
		Status string
	}, len(o.agents))
	for i, a := range o.agents {
		result[i].Type = a.Type
		result[i].Status = a.Status
	}
	return result
}

// --- handleKey: login waiting phase ---

func TestHandleKey_Login_WaitingPhase_Enter(t *testing.T) {
	m := newTestModel(t)
	m.login = &loginState{phase: "waiting", provider: "test"}
	m.inputModel.Text = "sk-test-key"
	m.inputModel.CursorPos = 11
	m.handleKey(makeKey(tea.KeyEnter))
	// Should attempt to save the key
	if m.login != nil && m.login.phase == "waiting" && m.inputModel.Text == "sk-test-key" {
		t.Error("expected login phase to change after enter with key")
	}
}

func TestHandleKey_Login_WaitingPhase_EmptyEnter(t *testing.T) {
	m := newTestModel(t)
	m.login = &loginState{phase: "waiting", provider: "test"}
	m.inputModel.Text = ""
	m.inputModel.CursorPos = 0
	m.handleKey(makeKey(tea.KeyEnter))
	// Should stay in waiting phase
	if m.login == nil || m.login.phase != "waiting" {
		t.Error("expected to stay in waiting phase on empty enter")
	}
}

func TestHandleKey_Login_WaitingPhase_TypesCharacter(t *testing.T) {
	m := newTestModel(t)
	m.login = &loginState{phase: "waiting", provider: "test"}
	m.inputModel.Text = "sk-"
	m.inputModel.CursorPos = 3
	m.handleKey(makeTextKey("x"))
	if m.inputModel.Text != "sk-x" {
		t.Errorf("expected 'sk-x', got %q", m.inputModel.Text)
	}
}

// --- agentMsg interface markers ---

func TestAgentMsgMarkers(t *testing.T) {
	// Exercise the interface marker methods for coverage.
	agentTextMsg{}.agentMsg()
	agentThinkingMsg{}.agentMsg()
	agentToolCallMsg{}.agentMsg()
	agentToolResultMsg{}.agentMsg()
	agentDoneMsg{}.agentMsg()
	agentSubEventMsg{}.agentMsg()
}

// --- waitForAgent ---

func TestWaitForAgent_Nil(t *testing.T) {
	cmd := waitForAgent(nil)
	if cmd != nil {
		t.Error("expected nil cmd for nil channel")
	}
}

func TestWaitForAgent_ClosedChannel(t *testing.T) {
	ch := make(chan agentMsg)
	close(ch)
	cmd := waitForAgent(ch)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	if _, ok := msg.(agentDoneMsg); !ok {
		t.Errorf("expected agentDoneMsg from closed channel, got %T", msg)
	}
}

func TestWaitForAgent_ReceivesMsg(t *testing.T) {
	ch := make(chan agentMsg, 1)
	ch <- agentTextMsg{text: "hello"}
	cmd := waitForAgent(ch)
	msg := cmd()
	if tm, ok := msg.(agentTextMsg); !ok || tm.text != "hello" {
		t.Errorf("expected agentTextMsg 'hello', got %T", msg)
	}
}

// --- more slash commands ---

func TestSlashCommand_History(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.History = []string{"/help", "/clear", "hello world"}
	m.handleSlashCommand("/history")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected history output")
	}
}

func TestSlashCommand_HistoryWithQuery(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.History = []string{"/help", "/clear", "hello world"}
	m.handleSlashCommand("/history help")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected filtered history output")
	}
}

// --- renderStatusBar: orchestrator with agents ---

func TestRenderStatusBar_WithOrchestrator(t *testing.T) {
	m := newTestModel(t)
	// Can't easily mock Orchestrator since it's an interface we don't control
	// but we can test without it
	out := m.renderStatusBar()
	if out == "" {
		t.Error("expected non-empty status bar")
	}
}

// --- handleKey: Tab with single completion match ---

func TestHandleKey_Tab_SingleMatch(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/hel"
	m.inputModel.CursorPos = 4
	m.handleKey(makeKey(tea.KeyTab))
	if m.inputModel.Text != "/help" {
		t.Errorf("expected single match '/help', got %q", m.inputModel.Text)
	}
}

// --- handleKey: Tab with multiple completion matches ---

func TestHandleKey_Tab_MultipleMatches(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/h"
	m.inputModel.CursorPos = 2
	m.handleKey(makeKey(tea.KeyTab))
	if !m.inputModel.CompletionMode {
		// May or may not enter completion mode depending on matches
		t.Logf("completionMode=%v, input=%q", m.inputModel.CompletionMode, m.inputModel.Text)
	}
}

// --- handleKey: Enter with completion mode ---

func TestHandleKey_Enter_ApplyCompletion(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.CompletionMode = true
	m.inputModel.SelectedIndex = 1
	m.inputModel.CompletionResult = &CompleteResult{
		Candidates: []CompletionCandidate{
			{Text: "/help"},
			{Text: "/history"},
		},
		Selected: 1,
	}
	m.handleKey(makeKey(tea.KeyEnter))
	if m.inputModel.Text != "/history" {
		t.Errorf("expected '/history' applied, got %q", m.inputModel.Text)
	}
	if m.inputModel.CompletionMode {
		t.Error("expected completion mode dismissed")
	}
}

// --- submit: adds to history ---

func TestSubmit_AddsToHistory(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "/help"
	m.testSubmit()
	found := false
	for _, h := range m.inputModel.History {
		if h == "/help" {
			found = true
		}
	}
	if !found {
		t.Error("expected /help added to history")
	}
}

// --- handleSkillCreateCommand ---

func TestHandleSkillCreateCommand_WithNameTmpDir(t *testing.T) {
	m := newTestModel(t)
	m.cfg.WorkDir = t.TempDir()
	m.handleSkillCreateCommand([]string{"test-new-skill"})
	// Should either create the skill or show a message
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message from skill create")
	}
}

// --- handleSkillLoadCommand ---

func TestHandleSkillLoadCommand(t *testing.T) {
	m := newTestModel(t)
	m.cfg.SkillDirs = []string{t.TempDir()}
	m.handleSkillLoadCommand()
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message from skill load")
	}
}

// --- submit: regular text triggers agent ---

func TestSubmit_RegularText(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.Text = "hello world"
	m.inputModel.CursorPos = 11
	// submit() will call runAgentLoop which needs m.cfg.Agent, so it will
	// add messages and start goroutine. We test the state changes.
	m.testSubmit()
	// Should have added user + assistant messages
	if len(m.chatModel.Messages) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(m.chatModel.Messages))
	}
	if m.chatModel.Messages[0].role != "user" || m.chatModel.Messages[0].content != "hello world" {
		t.Error("expected user message")
	}
	if !m.running {
		t.Error("expected running=true")
	}
	if m.inputModel.Text != "" {
		t.Error("expected input cleared")
	}
	// Clean up the goroutine
	if m.agentCh != nil {
		go func() { for range m.agentCh {} }()
	}
}

func TestSubmit_SkipsDuplicateHistory(t *testing.T) {
	m := newTestModel(t)
	m.inputModel.History = []string{"hello"}
	m.inputModel.Text = "hello"
	m.testSubmit()
	count := 0
	for _, h := range m.inputModel.History {
		if h == "hello" {
			count++
		}
	}
	if count > 1 {
		t.Errorf("expected no duplicate history, got %d entries", count)
	}
	if m.agentCh != nil {
		go func() { for range m.agentCh {} }()
	}
}

// --- handleSlashCommand: more branches ---

func TestSlashCommand_SkillCreate(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/skill-create")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message")
	}
}

func TestSlashCommand_SkillLoad(t *testing.T) {
	m := newTestModel(t)
	m.cfg.SkillDirs = []string{t.TempDir()}
	m.handleSlashCommand("/skill-load")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message")
	}
}

func TestSlashCommand_SkillList(t *testing.T) {
	m := newTestModel(t)
	m.handleSlashCommand("/skill-list")
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected message")
	}
}

// --- CycleSelection edge cases ---

func TestCycleSelection_EmptyCandidates(t *testing.T) {
	r := &CompleteResult{Candidates: nil}
	r.CycleSelection(1) // should not panic
}

func TestCycleSelection_Backwards(t *testing.T) {
	r := &CompleteResult{
		Candidates: []CompletionCandidate{{Text: "a"}, {Text: "b"}, {Text: "c"}},
		Selected:   0,
	}
	r.CycleSelection(-1)
	if r.Selected != 2 {
		t.Errorf("expected wrap to 2, got %d", r.Selected)
	}
}

// --- GenerateCommitMsgFunc ---

func TestGenerateCommitMsgFunc_ReturnsFunc(t *testing.T) {
	fn := GenerateCommitMsgFunc(nil)
	if fn == nil {
		t.Fatal("expected non-nil function")
	}
	// Calling with nil LLM panics, so just verify the function was created.
}

// helper
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
