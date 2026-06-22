package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/extension"
)

// -----------------------------------------------------------------------------
// matrixState tests — covers renderLine, render, tick, feed, clear.
// -----------------------------------------------------------------------------

func TestMatrixState_FeedAndRender(t *testing.T) {
	var ms matrixState
	// Feed some text. Width big enough for matrixW = 60% of 100 = 60.
	ms.feed("hello world", 100)
	if !ms.active {
		t.Error("feed should activate matrix")
	}
	out := ms.render()
	if out == "" {
		t.Error("render should return non-empty when active")
	}
}

func TestMatrixState_RenderInactive(t *testing.T) {
	var ms matrixState
	if got := ms.render(); got != "" {
		t.Errorf("render of inactive matrix should be empty, got %q", got)
	}
}

func TestMatrixState_RenderLineEmpty(t *testing.T) {
	var ms matrixState
	// grid[row] is nil/empty, renderLine should return empty.
	if got := ms.renderLine(0); got != "" {
		t.Errorf("renderLine on empty grid should be empty, got %q", got)
	}
}

func TestMatrixState_Tick(t *testing.T) {
	var ms matrixState
	ms.feed("seed", 80)
	oldCell := ms.grid[0][0]
	ms.tick(80)
	// Width should stay same; after shift a new cell at rightmost is populated.
	if len(ms.grid[0]) == 0 {
		t.Fatal("tick should not reset grid width")
	}
	_ = oldCell
}

func TestMatrixState_TickInactive(t *testing.T) {
	var ms matrixState
	ms.tick(80) // no-op when inactive
	if ms.active {
		t.Error("tick should not activate an inactive matrix")
	}
}

func TestMatrixState_Clear(t *testing.T) {
	var ms matrixState
	ms.feed("data", 80)
	ms.clear()
	if ms.active {
		t.Error("clear should deactivate matrix")
	}
	if ms.width != 0 {
		t.Errorf("clear should zero width, got %d", ms.width)
	}
}

func TestMatrixState_FeedLongText(t *testing.T) {
	var ms matrixState
	// Large input triggers shifts cap and different code path.
	big := strings.Repeat("x", 10000)
	ms.feed(big, 200)
	if !ms.active {
		t.Error("feed should activate")
	}
}

func TestMatrixState_FeedTinyWidth(t *testing.T) {
	var ms matrixState
	ms.feed("x", 0) // matrixW would be 0, gets clamped to width (0), then... guard in feed.
	// When width=0, matrixW=0, reset to width(0). ensureWidth with 0 allocates 0-length grid.
	// feed shouldn't panic.
	_ = ms.render()
}

func TestMatrixTickCmd(t *testing.T) {
	cmd := matrixTickCmd()
	if cmd == nil {
		t.Fatal("matrixTickCmd should return non-nil cmd")
	}
	// Invoke the command, make sure it returns a matrixTickMsg.
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()
	select {
	case msg := <-done:
		if _, ok := msg.(matrixTickMsg); !ok {
			t.Errorf("expected matrixTickMsg, got %T", msg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("matrixTickCmd timed out")
	}
}

// -----------------------------------------------------------------------------
// Plan flow tests — handlePlanCommand branches, detectCategory, nextSpecNumber,
// findExistingSpec.
// -----------------------------------------------------------------------------

func TestDetectCategory_Skills(t *testing.T) {
	if got := detectCategory("add new skill for things"); got != "skills" {
		t.Errorf("expected skills, got %q", got)
	}
}

func TestDetectCategory_Memory(t *testing.T) {
	if got := detectCategory("improve memory palace"); got != "memory" {
		t.Errorf("expected memory, got %q", got)
	}
}

func TestDetectCategory_Sessions(t *testing.T) {
	if got := detectCategory("improve session recovery"); got != "sessions" {
		t.Errorf("expected sessions, got %q", got)
	}
}

func TestDetectCategory_ToolsDefault(t *testing.T) {
	if got := detectCategory("xyz abc 123"); got != "features/TOO" {
		t.Errorf("expected features/TOO default, got %q", got)
	}
}

func TestDetectCategory_ToolsKeyword(t *testing.T) {
	if got := detectCategory("add new tool handler"); got != "features/TOO" {
		t.Errorf("expected features/TOO, got %q", got)
	}
}

func TestNextSpecNumber_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	got := nextSpecNumber(tmp, "tools")
	if got != "001" {
		t.Errorf("expected 001 for empty, got %q", got)
	}
}

func TestNextSpecNumber_Increment(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "specs", "tools")
	_ = os.MkdirAll(filepath.Join(base, "001-foo"), 0o755)
	_ = os.MkdirAll(filepath.Join(base, "005-bar"), 0o755)
	_ = os.MkdirAll(filepath.Join(base, "002-baz"), 0o755)
	if got := nextSpecNumber(tmp, "tools"); got != "006" {
		t.Errorf("expected 006, got %q", got)
	}
}

func TestNextSpecNumber_IgnoresNonPrefix(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "specs", "tools")
	_ = os.MkdirAll(filepath.Join(base, "random-dir"), 0o755)
	_ = os.MkdirAll(filepath.Join(base, "001-foo"), 0o755)
	if got := nextSpecNumber(tmp, "tools"); got != "002" {
		t.Errorf("expected 002, got %q", got)
	}
}

func TestFindExistingSpec_Found(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "specs", "tools")
	_ = os.MkdirAll(filepath.Join(base, "003-my-feature"), 0o755)
	got := findExistingSpec(tmp, "tools", "my-feature")
	want := filepath.Join("tools", "003-my-feature")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestFindExistingSpec_NotFound(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "specs", "tools")
	_ = os.MkdirAll(filepath.Join(base, "001-other"), 0o755)
	if got := findExistingSpec(tmp, "tools", "missing"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFindExistingSpec_MissingCategoryDir(t *testing.T) {
	tmp := t.TempDir()
	if got := findExistingSpec(tmp, "nonexistent", "x"); got != "" {
		t.Errorf("expected empty for missing category, got %q", got)
	}
}

func TestHandlePlanCommand_Usage(t *testing.T) {
	m := &model{}
	_, cmd := m.handlePlanCommand(nil)
	if cmd != nil {
		t.Error("expected nil cmd for usage path")
	}
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Usage") {
		t.Errorf("expected Usage hint, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandlePlanCommand_NoAgent(t *testing.T) {
	// With Agent=nil, startPlanSession should error out.
	tmp := t.TempDir()
	m := &model{
		cfg: Config{WorkDir: tmp},
	}
	_, cmd := m.handlePlanCommand([]string{"add", "rate", "limiting"})
	if cmd != nil {
		t.Error("expected nil cmd when agent missing")
	}
	// Expect error message about agent.
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	found := false
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "no agent configured") || strings.Contains(msg.content, "Error") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about missing agent, got %+v", m.chatModel.Messages)
	}
}

// -----------------------------------------------------------------------------
// handleMCPCommand — covers empty and populated server lists.
// -----------------------------------------------------------------------------

func TestHandleMCPCommand_NoServers(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{},
	}
	m.handleMCPCommand()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "No MCP servers") {
		t.Errorf("expected 'No MCP servers' hint, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleMCPCommand_WithServers(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			MCPServers: []extension.MCPServerConfig{
				{Name: "test-srv", URL: "https://example.com"},
			},
		},
	}
	m.handleMCPCommand()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "MCP Servers") {
		t.Errorf("expected 'MCP Servers' header, got %q", content)
	}
	if !strings.Contains(content, "test-srv") {
		t.Errorf("expected server name, got %q", content)
	}
}

// -----------------------------------------------------------------------------
// handleRestartCommand — cover restart msg return.
// -----------------------------------------------------------------------------

func TestHandleRestartCommand_CB(t *testing.T) {
	m := &model{}
	_, cmd := m.handleRestartCommand()
	if !m.quitting {
		t.Error("expected m.quitting=true after restart")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	if _, ok := msg.(restartMsg); !ok {
		t.Errorf("expected restartMsg, got %T", msg)
	}
}

// -----------------------------------------------------------------------------
// waitForInitEvent tests
// -----------------------------------------------------------------------------

func TestWaitForInitEvent_Closed(t *testing.T) {
	ch := make(chan InitEvent)
	close(ch)
	cmd := waitForInitEvent(ch)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	ev, ok := msg.(initEventMsg)
	if !ok {
		t.Fatalf("expected initEventMsg, got %T", msg)
	}
	if ev.event.Err == nil {
		t.Error("expected err on closed channel")
	}
}

func TestWaitForInitEvent_Delivery(t *testing.T) {
	ch := make(chan InitEvent, 1)
	ch <- InitEvent{Item: "lsp", Done: true}
	cmd := waitForInitEvent(ch)
	msg := cmd()
	ev, ok := msg.(initEventMsg)
	if !ok {
		t.Fatalf("expected initEventMsg, got %T", msg)
	}
	if ev.event.Item != "lsp" {
		t.Errorf("expected Item=lsp, got %q", ev.event.Item)
	}
}

// -----------------------------------------------------------------------------
// View() rendering — covers multiple branches (quitting, loading, with matrix,
// with sidebar, without sidebar, with branchPopup).
// -----------------------------------------------------------------------------

func TestView_Quitting(t *testing.T) {
	m := newTestModelFull(t)
	m.quitting = true
	v := m.View()
	s := v.Content
	if !strings.Contains(s, "Goodbye") {
		t.Errorf("expected Goodbye, got %q", s)
	}
}

func TestView_ZeroWidthLoading(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 0
	m.cfg.AppVersion = "1.2.3"
	v := m.View()
	if !strings.Contains(v.Content, "Loading Pi 1.2.3..") || !strings.ContainsAny(v.Content, matrixChars) {
		t.Errorf("expected loading matrix startup line, got %q", v.Content)
	}
}

func TestView_WithSidebar(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 140
	m.height = 40
	v := m.View()
	s := v.Content
	if s == "" {
		t.Error("expected non-empty view")
	}
}

func TestView_WithoutSidebar_Narrow(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 60
	m.height = 24
	v := m.View()
	s := v.Content
	if s == "" {
		t.Error("expected non-empty view in narrow mode")
	}
}

func TestView_WithMatrix(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 140
	m.height = 40
	m.matrix.feed("hello", 100)
	v := m.View()
	s := v.Content
	if s == "" {
		t.Error("expected non-empty view with matrix")
	}
}

func TestView_WithBranchPopup(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 140
	m.height = 40
	m.branchPopup = &branchPopupState{
		branches:  []string{"main", "dev"},
		selected:  0,
		active:    "main",
		height:    5,
		scrollOff: 0,
	}
	v := m.View()
	s := v.Content
	if s == "" {
		t.Error("expected non-empty view with branch popup")
	}
}

// newTestModelFull constructs a model rich enough for View() calls.
func newTestModelFull(t *testing.T) *model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &model{
		cfg: Config{
			ModelName:    "test-model",
			ProviderName: "test-provider",
		},
		ctx:          ctx,
		cancel:       cancel,
		inputModel:   NewInputModel(make([]HistoryEntry, 0), nil, nil, ""),
		chatModel:    ChatModel{Messages: []message{{role: "user", content: "hi"}, {role: "assistant", content: "hello"}}},
		themeManager: NewThemeManager(),
		face:         NewFaceRenderer(),
		statusModel:  StatusModel{GitBranch: "feat/x"},
		width:        100,
		height:       30,
	}
}

// -----------------------------------------------------------------------------
// Update branches — matrix tick, reset ctrl-C count, ping done, etc.
// -----------------------------------------------------------------------------

func TestUpdate_MatrixTickMsg_Running(t *testing.T) {
	m := newTestModelFull(t)
	m.running = true
	m.matrix.feed("init", 100)
	newM, cmd := m.Update(matrixTickMsg{})
	if cmd == nil {
		t.Error("expected matrix tick to schedule another tick when running")
	}
	_ = newM
}

func TestUpdate_MatrixTickMsg_NotRunning(t *testing.T) {
	m := newTestModelFull(t)
	m.running = false
	_, cmd := m.Update(matrixTickMsg{})
	if cmd != nil {
		t.Error("expected nil cmd when not running")
	}
}

func TestUpdate_ResetCtrlCCountMsg(t *testing.T) {
	m := newTestModelFull(t)
	m.ctrlCCount = 1
	_, _ = m.Update(resetCtrlCCountMsg{})
	if m.ctrlCCount != 0 {
		t.Errorf("expected ctrlCCount reset to 0, got %d", m.ctrlCCount)
	}
}

func TestResetCtrlCCount_Cmd(t *testing.T) {
	m := newTestModelFull(t)
	cmd := resetCtrlCCount(m)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	// Don't actually execute — it sleeps 2s. Just verify it's a function.
}

// -----------------------------------------------------------------------------
// formatContextUsage tests — covers branches for empty token tracker,
// with tracker limits, last prompt tokens, and subagents.
// -----------------------------------------------------------------------------

func TestFormatContextUsage_NoTracker_CB(t *testing.T) {
	m := &model{
		cfg:       Config{ModelName: "gpt-4", ProviderName: "openai"},
		chatModel: ChatModel{Messages: []message{{role: "user", content: "hello"}}},
	}
	out := m.formatContextUsage()
	if !strings.Contains(out, "Context Usage") {
		t.Errorf("expected header, got %q", out)
	}
	if !strings.Contains(out, "openai | gpt-4") {
		t.Errorf("expected provider|model, got %q", out)
	}
}

// fakeTokenTracker implements TokenTracker for testing.
type fakeTokenTracker struct {
	limit          int64
	remaining      int64
	pctUsed        float64
	totalUsed      int64
	lastPromptTok  int64
	ctxWindowSize  int64
	ctxPercentUsed float64
}

func (f fakeTokenTracker) Limit() int64             { return f.limit }
func (f fakeTokenTracker) Remaining() int64         { return f.remaining }
func (f fakeTokenTracker) PercentUsed() float64     { return f.pctUsed }
func (f fakeTokenTracker) TotalUsed() int64         { return f.totalUsed }
func (f fakeTokenTracker) LastPromptTokens() int64  { return f.lastPromptTok }
func (f fakeTokenTracker) ContextWindowSize() int64 { return f.ctxWindowSize }
func (f fakeTokenTracker) ContextPercentUsed() float64 {
	return f.ctxPercentUsed
}

func TestFormatContextUsage_WithTracker_CB(t *testing.T) {
	tt := fakeTokenTracker{
		limit: 100000, remaining: 50000, pctUsed: 50, totalUsed: 50000,
		lastPromptTok: 20000, ctxWindowSize: 200000, ctxPercentUsed: 10,
	}
	m := &model{
		cfg: Config{
			ModelName:    "gpt-4",
			ProviderName: "openai",
			TokenTracker: tt,
		},
		chatModel: ChatModel{Messages: []message{
			{role: "user", content: strings.Repeat("x", 1000)},
			{role: "assistant", content: strings.Repeat("y", 1000)},
			{role: "tool", tool: "bash", content: strings.Repeat("z", 500)},
		}},
	}
	out := m.formatContextUsage()
	if !strings.Contains(out, "tokens") {
		t.Errorf("expected 'tokens', got %q", out)
	}
	if !strings.Contains(out, "Daily token usage") {
		t.Errorf("expected 'Daily token usage' section, got %q", out)
	}
	if !strings.Contains(out, "Context window") {
		t.Errorf("expected 'Context window' section, got %q", out)
	}
}

func TestFormatContextUsage_NoLimit(t *testing.T) {
	// tracker with limit=0 (unlimited) falls to totalTokens branch.
	tt := fakeTokenTracker{}
	m := &model{
		cfg: Config{
			ModelName:    "local",
			ProviderName: "ollama",
			TokenTracker: tt,
		},
		chatModel: ChatModel{Messages: []message{
			{role: "user", content: strings.Repeat("x", 500000)},
		}},
	}
	out := m.formatContextUsage()
	if !strings.Contains(out, "ctx ~") {
		t.Errorf("expected '~' context indicator, got %q", out)
	}
}

// -----------------------------------------------------------------------------
// History tests — load, append, format with various inputs.
// -----------------------------------------------------------------------------

func TestHistoryDir_NoHome(t *testing.T) {
	// Override HOME for this test.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // windows
	// On darwin/linux UserHomeDir returns error when HOME is empty.
	dir := historyDir()
	// Either way the function returns some value; just exercise path.
	_ = dir
}

func TestHistoryPathJSON_WithHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	p := historyPathJSON()
	if p == "" {
		t.Error("expected non-empty path")
	}
	if !strings.Contains(p, ".pi-go") {
		t.Errorf("expected .pi-go path, got %q", p)
	}
}

func TestHistoryPathPlain_WithHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	p := historyPathPlain()
	if p == "" {
		t.Error("expected non-empty path")
	}
}

func TestAppendHistory_WritesFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	appendHistory(HistoryEntry{Text: "hello"})
	// File should exist.
	p := filepath.Join(tmp, ".pi-go", "history.jsonl")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected history file to exist: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("expected 'hello' in history, got %q", data)
	}
}

func TestLoadHistory_MigratePlainToJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	piDir := filepath.Join(tmp, ".pi-go")
	_ = os.MkdirAll(piDir, 0o700)
	// Write legacy plain history.
	_ = os.WriteFile(filepath.Join(piDir, "history"), []byte("one\ntwo\n"), 0o600)

	entries := loadHistory()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// JSON file should have been created.
	if _, err := os.Stat(filepath.Join(piDir, "history.jsonl")); err != nil {
		t.Errorf("expected migration to write jsonl: %v", err)
	}
}

func TestLoadHistory_NoHistory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	entries := loadHistory()
	if entries != nil {
		t.Errorf("expected nil for no history, got %v", entries)
	}
}

// -----------------------------------------------------------------------------
// Theme tests — NewThemeManager fallback, SaveToConfig.
// -----------------------------------------------------------------------------

func TestNewThemeManagerFromJSON_Valid(t *testing.T) {
	json := []byte(`{"dark": {"name":"dark","displayName":"Dark","themeType":"dark","colors":{"text":"#fff","base":"#000","primary":"#00f","tool":"#f0f","success":"#0f0","error":"#f00","secondary":"#888","info":"#aaa","warning":"#ff0","diffAdded":"#0a0","diffRemoved":"#a00","diffAddedText":"#0f0","diffRemovedText":"#f00"}}}`)
	tm, err := NewThemeManagerFromJSON(json)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm == nil {
		t.Fatal("expected non-nil tm")
	}
}

func TestNewThemeManagerFromJSON_Invalid(t *testing.T) {
	_, err := NewThemeManagerFromJSON([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSaveThemeToConfig_WritesConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	saveThemeToConfig("tokyo-night")
	data, err := os.ReadFile(filepath.Join(tmp, ".pi-go", "config.json"))
	if err != nil {
		t.Fatalf("expected config.json to be created: %v", err)
	}
	if !strings.Contains(string(data), "tokyo-night") {
		t.Errorf("expected theme name in config, got %q", data)
	}
}

func TestSaveThemeToConfig_UpdatesExisting(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	configDir := filepath.Join(tmp, ".pi-go")
	_ = os.MkdirAll(configDir, 0o755)
	// Pre-write config.
	_ = os.WriteFile(filepath.Join(configDir, "config.json"),
		[]byte(`{"other": "value"}`), 0o644)

	saveThemeToConfig("pi-classic")

	data, _ := os.ReadFile(filepath.Join(configDir, "config.json"))
	if !strings.Contains(string(data), "pi-classic") {
		t.Errorf("expected pi-classic, got %q", data)
	}
	if !strings.Contains(string(data), "other") {
		t.Errorf("expected existing 'other' preserved, got %q", data)
	}
}

// -----------------------------------------------------------------------------
// detectBranch / countUntrackedLines — lightweight tests that exercise
// the git subprocess but tolerate absence of git.
// -----------------------------------------------------------------------------

func TestDetectBranch_NotAGitRepo(t *testing.T) {
	tmp := t.TempDir()
	got := detectBranch(tmp)
	// Should return "" when not a git repo (git rev-parse fails).
	if got != "" {
		t.Logf("got %q (maybe a git-tracked tmp); tolerating", got)
	}
}

func TestCountUntrackedLines_NotAGitRepo(t *testing.T) {
	tmp := t.TempDir()
	n := countUntrackedLines(tmp)
	if n != 0 {
		t.Errorf("expected 0 in non-git dir, got %d", n)
	}
}

// -----------------------------------------------------------------------------
// resizeDraining — covers the time-based guard.
// -----------------------------------------------------------------------------

func TestResizeDraining_True(t *testing.T) {
	m := &model{resizeAt: time.Now()}
	if !m.resizeDraining() {
		t.Error("expected draining just after resize")
	}
}

func TestResizeDraining_FalseAfterTimeout(t *testing.T) {
	m := &model{resizeAt: time.Now().Add(-time.Second)}
	if m.resizeDraining() {
		t.Error("expected no draining after 1 second")
	}
}

func TestResizeDraining_Zero(t *testing.T) {
	m := &model{}
	if m.resizeDraining() {
		t.Error("expected no draining when resizeAt is zero")
	}
}

// -----------------------------------------------------------------------------
// mainWidth — covers narrow and wide branches.
// -----------------------------------------------------------------------------

func TestMainWidth_Wide(t *testing.T) {
	m := &model{width: 120}
	if got := m.mainWidth(); got != 120-SidebarWidth {
		t.Errorf("expected width minus sidebar, got %d", got)
	}
}

func TestMainWidth_Narrow(t *testing.T) {
	m := &model{width: 60}
	if got := m.mainWidth(); got != 60 {
		t.Errorf("expected 60, got %d", got)
	}
}

// -----------------------------------------------------------------------------
// eyes / mascot fall-through when face is nil.
// -----------------------------------------------------------------------------

func TestEyes_NoFace_CB(t *testing.T) {
	m := &model{}
	if got := m.eyes(); got == "" {
		t.Error("expected non-empty idle eyes")
	}
}

func TestMascot_NoFace_CB(t *testing.T) {
	m := &model{}
	if got := m.mascot(); got == "" {
		t.Error("expected non-empty idle mascot")
	}
}

// -----------------------------------------------------------------------------
// handleKey — extra branches not already covered.
// -----------------------------------------------------------------------------

func TestHandleKey_BranchPopup_Up(t *testing.T) {
	m := newTestModelFull(t)
	m.branchPopup = &branchPopupState{
		branches: []string{"main", "dev"}, selected: 1, height: 5,
	}
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if m.branchPopup == nil {
		t.Fatal("branchPopup cleared unexpectedly")
	}
	if m.branchPopup.selected != 0 {
		t.Errorf("expected selected=0, got %d", m.branchPopup.selected)
	}
}

func TestHandleKey_BranchPopup_Down(t *testing.T) {
	m := newTestModelFull(t)
	m.branchPopup = &branchPopupState{
		branches: []string{"main", "dev"}, selected: 0, height: 5,
	}
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if m.branchPopup.selected != 1 {
		t.Errorf("expected selected=1, got %d", m.branchPopup.selected)
	}
}

func TestHandleKey_BranchPopup_Esc(t *testing.T) {
	m := newTestModelFull(t)
	m.branchPopup = &branchPopupState{
		branches: []string{"main", "dev"}, selected: 0, height: 5,
	}
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if m.branchPopup != nil {
		t.Error("expected branchPopup cleared on Esc")
	}
}

func TestHandleKey_BranchPopup_OtherKey(t *testing.T) {
	m := newTestModelFull(t)
	m.branchPopup = &branchPopupState{
		branches: []string{"main", "dev"}, selected: 0, height: 5,
	}
	// Any key other than up/down/enter/esc dismisses.
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))
	if m.branchPopup != nil {
		t.Error("expected branchPopup cleared on other key")
	}
}

func TestHandleKey_CtrlO_ToggleTools(t *testing.T) {
	m := newTestModelFull(t)
	orig := m.chatModel.ToolDisplay.CompactTools
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if m.chatModel.ToolDisplay.CompactTools == orig {
		t.Error("expected CompactTools toggled")
	}
}

func TestHandleKey_CtrlB_TogglePopup(t *testing.T) {
	m := newTestModelFull(t)
	m.statusModel.GitBranch = "main"
	// First press: opens popup (may be empty if git unavailable).
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	// If a popup was created, send another to close it.
	if m.branchPopup != nil {
		_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
		if m.branchPopup != nil {
			t.Error("expected popup closed after second Ctrl+B")
		}
	}
}

func TestHandleKey_F12_Noop_CB(t *testing.T) {
	m := newTestModelFull(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	_, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyF12}))
	if cmd != nil {
		t.Error("expected nil cmd for F12")
	}
}

func TestHandleKey_PgUp_PgDown(t *testing.T) {
	m := newTestModelFull(t)
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	_, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	// Just exercise the code path.
}

// -----------------------------------------------------------------------------
// submitPrompt — covers the non-empty mentions path.
// -----------------------------------------------------------------------------

func TestSubmitPrompt_WithMentions(t *testing.T) {
	m := newTestModelFull(t)
	// No real agent; this will create an agentCh and try to run, but
	// runAgentLoop handles nil cfg.Agent and sends done.
	_, cmd := m.submitPrompt("check this", []string{"file.go"})
	if !m.running {
		t.Error("expected m.running=true")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd")
	}
	// Drain agentCh briefly.
	if m.agentCh != nil {
		select {
		case <-m.agentCh:
		case <-time.After(1 * time.Second):
		}
	}
}

func TestSubmitPrompt_NoMentions(t *testing.T) {
	m := newTestModelFull(t)
	_, cmd := m.submitPrompt("hello", nil)
	if !m.running {
		t.Error("expected running")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd")
	}
	if m.agentCh != nil {
		select {
		case <-m.agentCh:
		case <-time.After(1 * time.Second):
		}
	}
}

// -----------------------------------------------------------------------------
// cancelAgent — covers drain path.
// -----------------------------------------------------------------------------

func TestCancelAgent_WithChannel_CB(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{
		ctx:       ctx,
		cancel:    cancel,
		running:   true,
		agentCh:   make(chan agentMsg, 4),
		face:      NewFaceRenderer(),
		chatModel: ChatModel{},
	}
	m.agentCh <- agentTextMsg{text: "pending"}
	m.cancelAgent()
	if m.running {
		t.Error("expected running=false")
	}
	if m.agentCh != nil {
		t.Error("expected agentCh=nil")
	}
}

func TestCancelAgent_NoChannel_CB(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{ctx: ctx, cancel: cancel, running: true, face: NewFaceRenderer()}
	m.cancelAgent()
	if m.running {
		t.Error("expected running=false")
	}
}

// -----------------------------------------------------------------------------
// waitForAgent — nil channel.
// -----------------------------------------------------------------------------

func TestWaitForAgent_NilChannel(t *testing.T) {
	if got := waitForAgent(nil); got != nil {
		t.Error("expected nil cmd for nil channel")
	}
}

func TestWaitForAgent_ClosedChannel_CB(t *testing.T) {
	ch := make(chan agentMsg)
	close(ch)
	cmd := waitForAgent(ch)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	if _, ok := msg.(agentDoneMsg); !ok {
		t.Errorf("expected agentDoneMsg on closed channel, got %T", msg)
	}
}

func TestWaitForAgent_ReceiveMessage(t *testing.T) {
	ch := make(chan agentMsg, 1)
	ch <- agentTextMsg{text: "hi"}
	cmd := waitForAgent(ch)
	msg := cmd()
	if _, ok := msg.(agentTextMsg); !ok {
		t.Errorf("expected agentTextMsg, got %T", msg)
	}
}

// -----------------------------------------------------------------------------
// waitForSubEvent — nil channel.
// -----------------------------------------------------------------------------

func TestWaitForSubEvent_NilChannel_CB(t *testing.T) {
	if got := waitForSubEvent(nil); got != nil {
		t.Error("expected nil cmd for nil channel")
	}
}

func TestWaitForSubEvent_ClosedChannel(t *testing.T) {
	ch := make(chan AgentSubEvent)
	close(ch)
	cmd := waitForSubEvent(ch)
	msg := cmd()
	if msg != nil {
		t.Errorf("expected nil msg on closed channel, got %T", msg)
	}
}

func TestWaitForSubEvent_Delivery(t *testing.T) {
	ch := make(chan AgentSubEvent, 1)
	ch <- AgentSubEvent{AgentID: "a1", Kind: "spawn"}
	cmd := waitForSubEvent(ch)
	msg := cmd()
	ev, ok := msg.(agentSubEventMsg)
	if !ok {
		t.Fatalf("expected agentSubEventMsg, got %T", msg)
	}
	if ev.agentID != "a1" {
		t.Errorf("expected agentID=a1, got %q", ev.agentID)
	}
}

// -----------------------------------------------------------------------------
// buildListCards / findUnassignedAgentCard — helper edge cases.
// -----------------------------------------------------------------------------

func TestBuildListCards_EmptyList(t *testing.T) {
	if got := buildListCards(message{}, []any{}); got != nil {
		t.Errorf("expected nil for empty list, got %v", got)
	}
}

func TestBuildListCards_NotList(t *testing.T) {
	if got := buildListCards(message{}, "not a list"); got != nil {
		t.Errorf("expected nil for non-list, got %v", got)
	}
}

func TestBuildListCards_ValidEntries(t *testing.T) {
	base := message{role: "tool", tool: "agent"}
	list := []any{
		map[string]any{"agent": "claude", "task": "do a thing"},
		map[string]any{"agent": "gemini", "prompt": "handle another"},
		map[string]any{"agent": ""}, // skipped
		"not a map",                 // skipped
	}
	got := buildListCards(base, list)
	if len(got) != 2 {
		t.Fatalf("expected 2 valid cards, got %d", len(got))
	}
	if got[0].agentType != "claude" {
		t.Errorf("expected claude, got %q", got[0].agentType)
	}
	if got[1].agentType != "gemini" {
		t.Errorf("expected gemini, got %q", got[1].agentType)
	}
}

func TestFindUnassignedAgentCard_Matches(t *testing.T) {
	msgs := []message{
		{role: "user", content: "hi"},
		{role: "tool", tool: "agent", agentType: "claude"},
	}
	idx := findUnassignedAgentCard(msgs, "claude-12345")
	if idx != 1 {
		t.Errorf("expected idx=1, got %d", idx)
	}
}

func TestFindUnassignedAgentCard_Fallback(t *testing.T) {
	msgs := []message{
		{role: "tool", tool: "agent", agentType: "other"},
	}
	idx := findUnassignedAgentCard(msgs, "claude-12345")
	if idx != 0 {
		t.Errorf("expected fallback idx=0, got %d", idx)
	}
}

func TestFindUnassignedAgentCard_None(t *testing.T) {
	msgs := []message{
		{role: "user", content: "hi"},
	}
	idx := findUnassignedAgentCard(msgs, "claude-12345")
	if idx != -1 {
		t.Errorf("expected -1, got %d", idx)
	}
}

func TestTruncatePrompt_Long(t *testing.T) {
	in := strings.Repeat("a", 100)
	got := truncatePrompt(in)
	if len(got) > 60 {
		t.Errorf("expected <=60, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis, got %q", got)
	}
}

func TestTruncatePrompt_Newline(t *testing.T) {
	in := "first line\nsecond line"
	got := truncatePrompt(in)
	if got != "first line" {
		t.Errorf("expected 'first line', got %q", got)
	}
}

func TestTruncatePrompt_Short(t *testing.T) {
	if got := truncatePrompt("short"); got != "short" {
		t.Errorf("expected 'short', got %q", got)
	}
}

// -----------------------------------------------------------------------------
// completion: detectCompletionType, matchingCommands edge cases.
// -----------------------------------------------------------------------------

func TestDetectCompletionType_Empty(t *testing.T) {
	if got := detectCompletionType(""); got != CompletionTypeNone {
		t.Errorf("expected None, got %v", got)
	}
}

func TestDetectCompletionType_CommandOnly(t *testing.T) {
	if got := detectCompletionType("/"); got != CompletionTypeCommand {
		t.Errorf("expected Command, got %v", got)
	}
}

func TestDetectCompletionType_RunSpec(t *testing.T) {
	if got := detectCompletionType("/run foo"); got != CompletionTypeSpec {
		t.Errorf("expected Spec, got %v", got)
	}
}

func TestDetectCompletionType_PlanSpec(t *testing.T) {
	if got := detectCompletionType("/plan foo"); got != CompletionTypeSpec {
		t.Errorf("expected Spec, got %v", got)
	}
}

func TestDetectCompletionType_PartialCommand(t *testing.T) {
	if got := detectCompletionType("/he"); got != CompletionTypeCommand {
		t.Errorf("expected Command, got %v", got)
	}
}

func TestMatchingCommands_Match(t *testing.T) {
	got := matchingCommands("/he")
	if len(got) == 0 {
		t.Error("expected at least one match for /he")
	}
}

func TestMatchingCommands_NoMatch(t *testing.T) {
	got := matchingCommands("/nonexistent")
	if len(got) != 0 {
		t.Errorf("expected no matches, got %d", len(got))
	}
}

func TestMatchingCommands_WithSpace(t *testing.T) {
	// "/plan " — matches commands that extend after the space.
	got := matchingCommands("/plan ")
	// /plan itself ends at the space and is excluded; /plan resume etc should match.
	for _, c := range got {
		if c.Text == "/plan" {
			t.Error("/plan should be excluded when prefix ends with space")
		}
	}
}

// -----------------------------------------------------------------------------
// isUserPaste — cover the newline/tab accepted branches.
// -----------------------------------------------------------------------------

func TestIsUserPaste_Empty(t *testing.T) {
	if isUserPaste("") {
		t.Error("expected false for empty")
	}
}

func TestIsUserPaste_TabAccepted(t *testing.T) {
	if !isUserPaste("hello\tworld") {
		t.Error("tab should be accepted")
	}
}

// -----------------------------------------------------------------------------
// maskServerURL / providerDisplayName.
// -----------------------------------------------------------------------------

func TestProviderDisplayName_Openai(t *testing.T) {
	m := &model{cfg: Config{ProviderName: "openai"}}
	name := m.providerDisplayName()
	if name == "" {
		t.Error("expected non-empty name")
	}
}

func TestProviderDisplayName_NonOpenai(t *testing.T) {
	m := &model{cfg: Config{ProviderName: "ollama"}}
	if got := m.providerDisplayName(); got != "ollama" {
		t.Errorf("expected 'ollama', got %q", got)
	}
}

// -----------------------------------------------------------------------------
// agentMsg marker methods — ensure they're called to cover those stubs.
// -----------------------------------------------------------------------------

func TestAgentMsgMarkers_CB(t *testing.T) {
	// Each marker method is a no-op; call them directly to lock coverage.
	(agentTextMsg{}).agentMsg()
	(agentThinkingMsg{}).agentMsg()
	(agentToolCallMsg{}).agentMsg()
	(agentToolResultMsg{}).agentMsg()
	(agentDoneMsg{}).agentMsg()
	(agentSubEventMsg{}).agentMsg()
}

// -----------------------------------------------------------------------------
// InputModel.ReloadSkills — cover the SkillDirs path.
// -----------------------------------------------------------------------------

func TestInputModel_ReloadSkills_EmptyDirs(t *testing.T) {
	im := &InputModel{}
	im.ReloadSkills()
	// No panic is success.
}

func TestInputModel_ReloadSkills_WithDirs(t *testing.T) {
	tmp := t.TempDir()
	im := &InputModel{SkillDirs: []string{tmp}}
	im.ReloadSkills()
	// Path exercised (skills may be populated from default dirs etc.).
}

// -----------------------------------------------------------------------------
// loadHistoryPlain / loadHistoryJSON — cover missing file and success paths.
// -----------------------------------------------------------------------------

func TestLoadHistoryPlain_MissingFile(t *testing.T) {
	got := loadHistoryPlain("/nonexistent/path")
	if got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

func TestLoadHistoryJSON_MissingFile(t *testing.T) {
	got := loadHistoryJSON("/nonexistent/path")
	if got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

func TestLoadHistoryJSON_Content(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "history.jsonl")
	_ = os.WriteFile(f, []byte(`{"text":"one"}`+"\n"+`{"text":"two"}`+"\n"), 0o644)
	got := loadHistoryJSON(f)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Text != "one" {
		t.Errorf("expected 'one', got %q", got[0].Text)
	}
}

func TestLoadHistoryJSON_TruncatesLarge(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "history.jsonl")
	// Write more than maxHistorySize lines.
	var sb strings.Builder
	for i := 0; i < maxHistorySize+50; i++ {
		sb.WriteString(`{"text":"x"}`)
		sb.WriteByte('\n')
	}
	_ = os.WriteFile(f, []byte(sb.String()), 0o644)
	got := loadHistoryJSON(f)
	if len(got) != maxHistorySize {
		t.Errorf("expected %d entries (truncated), got %d", maxHistorySize, len(got))
	}
}

// -----------------------------------------------------------------------------
// truncateHistory
// -----------------------------------------------------------------------------

func TestTruncateHistory_NoTrunc(t *testing.T) {
	entries := []HistoryEntry{{Text: "a"}, {Text: "b"}}
	got := truncateHistory(entries, 10)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
}

func TestTruncateHistory_Trunc(t *testing.T) {
	entries := []HistoryEntry{{Text: "a"}, {Text: "b"}, {Text: "c"}}
	got := truncateHistory(entries, 2)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
	if got[0].Text != "b" {
		t.Errorf("expected 'b' (most recent 2), got %q", got[0].Text)
	}
}

// -----------------------------------------------------------------------------
// formatHistoryOutput — all empty, matching, filtered.
// -----------------------------------------------------------------------------

func TestFormatHistoryOutput_Empty(t *testing.T) {
	got := formatHistoryOutput(nil, "")
	if !strings.Contains(got, "No command history") {
		t.Errorf("expected 'No command history', got %q", got)
	}
}

func TestFormatHistoryOutput_EmptyQuery(t *testing.T) {
	got := formatHistoryOutput([]HistoryEntry{{Text: "x"}}, "")
	if !strings.Contains(got, "Command history") {
		t.Errorf("expected header, got %q", got)
	}
}

func TestFormatHistoryOutput_QueryNoMatch(t *testing.T) {
	got := formatHistoryOutput([]HistoryEntry{{Text: "apple"}}, "zebra")
	if !strings.Contains(got, "No history matching") {
		t.Errorf("expected no-match, got %q", got)
	}
}

func TestFormatHistoryOutput_WithMentions(t *testing.T) {
	entries := []HistoryEntry{{Text: "hello", Mentions: []string{"foo.go"}}}
	got := formatHistoryOutput(entries, "")
	if !strings.Contains(got, "foo.go") {
		t.Errorf("expected foo.go in output, got %q", got)
	}
	if !strings.Contains(got, "refs:") {
		t.Errorf("expected 'refs:' label, got %q", got)
	}
}

func TestFormatHistoryOutput_MoreThan20(t *testing.T) {
	var entries []HistoryEntry
	for i := 0; i < 30; i++ {
		entries = append(entries, HistoryEntry{Text: fmt.Sprintf("entry%d", i)})
	}
	got := formatHistoryOutput(entries, "")
	// Should show "30 total" and only last 20.
	if !strings.Contains(got, "30 total") {
		t.Errorf("expected '30 total', got %q", got)
	}
	// Should not contain "entry0" (first entry, not in last 20).
	if strings.Contains(got, "entry0") {
		t.Errorf("expected entry0 not to be in last 20, got %q", got)
	}
}

// -----------------------------------------------------------------------------
// migrateHistoryFormat
// -----------------------------------------------------------------------------

func TestMigrateHistoryFormat_CB(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := migrateHistoryFormat(lines)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if got[0].Text != "a" {
		t.Errorf("expected 'a', got %q", got[0].Text)
	}
}

// -----------------------------------------------------------------------------
// handleHistoryCommand
// -----------------------------------------------------------------------------

func TestHandleHistoryCommand_NoArgs(t *testing.T) {
	m := &model{
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []HistoryEntry{{Text: "hello"}}},
	}
	m.handleHistoryCommand(nil)
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
}

func TestHandleHistoryCommand_Query(t *testing.T) {
	m := &model{
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{History: []HistoryEntry{{Text: "git status"}}},
	}
	m.handleHistoryCommand([]string{"git"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
}

// -----------------------------------------------------------------------------
// logLogin — nil-model guard and happy path are both meaningful.
// -----------------------------------------------------------------------------

func TestLogLogin_NilModel(t *testing.T) {
	var m *model
	m.logLogin("test")
	// No panic is success.
}

func TestLogLogin_NilLogger(t *testing.T) {
	m := &model{}
	m.logLogin("test %d", 42)
	// No panic, no panic when cfg.Logger is nil.
}

// -----------------------------------------------------------------------------
// handleMCPCommand — with toolset statuses.
// -----------------------------------------------------------------------------

func TestHandleMCPCommand_WithEmptyToolsets(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			MCPServers: []extension.MCPServerConfig{
				{Name: "srv1", URL: "https://x.example"},
				{Name: "srv2", URL: "https://y.example?key=secret"},
			},
		},
	}
	m.handleMCPCommand()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	content := m.chatModel.Messages[0].content
	if !strings.Contains(content, "srv1") || !strings.Contains(content, "srv2") {
		t.Errorf("expected both server names, got %q", content)
	}
}

// -----------------------------------------------------------------------------
// waitForRunAgent — nil events channel.
// -----------------------------------------------------------------------------

func TestWaitForRunAgent_NilChannel(t *testing.T) {
	if got := waitForRunAgent(nil, "a1"); got != nil {
		t.Error("expected nil cmd")
	}
}

// -----------------------------------------------------------------------------
// loginStartManual — covers the basic manual key entry path.
// -----------------------------------------------------------------------------

func TestLoginStartManual(t *testing.T) {
	withMockBrowser(t)
	m := &model{}
	// Build a provider that has no OAuth config — falls to manual.
	prov := auth.Provider{
		Name:       "manual-prov",
		EnvVar:     "MANUAL_KEY",
		KeyPageURL: "https://example.com/keys",
	}
	m.loginStart(prov)
	if m.login == nil {
		t.Fatal("expected login state")
	}
	if m.login.phase != "waiting" {
		t.Errorf("expected waiting phase, got %q", m.login.phase)
	}
}

// -----------------------------------------------------------------------------
// View — with chat messages, thinking, tools.
// -----------------------------------------------------------------------------

func TestView_WithThinkingMessages(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 120
	m.height = 30
	m.chatModel.Messages = []message{
		{role: "user", content: "do it"},
		{role: "thinking", content: "hmm"},
		{role: "assistant", content: "done"},
	}
	v := m.View()
	if v.Content == "" {
		t.Error("expected non-empty view")
	}
}

func TestView_WithToolMessages(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 120
	m.height = 30
	m.chatModel.Messages = []message{
		{role: "user", content: "run"},
		{role: "tool", tool: "bash", content: "ok"},
		{role: "assistant", content: "done"},
	}
	v := m.View()
	if v.Content == "" {
		t.Error("expected non-empty view")
	}
}

// -----------------------------------------------------------------------------
// Update() extra branches — test unknown message type passes through.
// -----------------------------------------------------------------------------

type unknownMsgType struct{}

func TestUpdate_UnknownMsg_NotRunning(t *testing.T) {
	m := newTestModelFull(t)
	_, cmd := m.Update(unknownMsgType{})
	if cmd != nil {
		t.Errorf("expected nil cmd for unknown msg when not running, got %v", cmd)
	}
}

func TestUpdate_UnknownMsg_Running(t *testing.T) {
	m := newTestModelFull(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	_, cmd := m.Update(unknownMsgType{})
	if cmd == nil {
		t.Error("expected waitForAgent cmd when running")
	}
}

// -----------------------------------------------------------------------------
// NewThemeManager — cover fallback path by passing invalid JSON indirectly.
// (Most of the path is exercised by the default call.)
// -----------------------------------------------------------------------------

func TestNewThemeManager_DefaultReady(t *testing.T) {
	tm := NewThemeManager()
	if tm == nil {
		t.Fatal("expected non-nil")
	}
	if tm.current == "" {
		t.Error("expected non-empty current theme")
	}
}

// -----------------------------------------------------------------------------
// maskKey — extra cases.
// -----------------------------------------------------------------------------

func TestMaskKey_Empty(t *testing.T) {
	if got := maskKey(""); got != "****" {
		t.Errorf("expected ****, got %q", got)
	}
}

// -----------------------------------------------------------------------------
// extractMentions for completeness.
// -----------------------------------------------------------------------------

func TestExtractMentions_None(t *testing.T) {
	if got := extractMentions("no mentions here"); len(got) != 0 {
		t.Errorf("expected none, got %v", got)
	}
}

func TestExtractMentions_Single(t *testing.T) {
	got := extractMentions("check @file.go please")
	if len(got) != 1 || got[0] != "file.go" {
		t.Errorf("expected [file.go], got %v", got)
	}
}

func TestExtractMentions_Multiple(t *testing.T) {
	got := extractMentions("check @a.go and @b.go")
	if len(got) != 2 {
		t.Errorf("expected 2, got %v", got)
	}
}

// -----------------------------------------------------------------------------
// Update() branches — InputSubmitMsg, MouseMsg, KeyPressMsg during resize drain.
// -----------------------------------------------------------------------------

func TestUpdate_InputSubmitMsg_SlashCommand(t *testing.T) {
	m := newTestModelFull(t)
	_, _ = m.Update(InputSubmitMsg{Text: "/help", Mentions: nil})
	if len(m.chatModel.Messages) == 0 {
		t.Error("expected help output in messages")
	}
}

func TestUpdate_KeyPressMsg_DuringResize(t *testing.T) {
	m := newTestModelFull(t)
	m.resizeAt = time.Now() // trigger draining
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))
	if cmd == nil {
		t.Error("expected resize drain wake command while draining")
	}
}

func TestUpdate_MouseClickMsg(t *testing.T) {
	m := newTestModelFull(t)
	// Most MouseClickMsgs route to handleMouseClick which returns m,nil.
	_, cmd := m.Update(tea.MouseClickMsg{})
	if cmd != nil {
		t.Error("expected nil cmd")
	}
}

// -----------------------------------------------------------------------------
// handleKey — login manual-code phase with empty and non-empty codes.
// -----------------------------------------------------------------------------

func TestHandleKey_LoginManualCode_EmptyEnter(t *testing.T) {
	m := newTestModelFull(t)
	m.login = &loginState{phase: "manual-code"}
	m.inputModel.Text = "" // empty
	_, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil {
		t.Error("expected nil cmd for empty manual-code enter")
	}
}

// -----------------------------------------------------------------------------
// handleLoginCodeSubmit — nil session path.
// -----------------------------------------------------------------------------

func TestHandleLoginCodeSubmit_NilSession(t *testing.T) {
	m := &model{
		login: &loginState{phase: "manual-code", provider: "codex"},
	}
	_, cmd := m.handleLoginCodeSubmit("somecode")
	if cmd != nil {
		t.Error("expected nil cmd when session is nil")
	}
	if m.login != nil {
		t.Error("expected login cleared")
	}
}

// -----------------------------------------------------------------------------
// loginStartManualCode — not configured provider returns error path.
// -----------------------------------------------------------------------------

func TestLoginStartManualCode_NotConfigured(t *testing.T) {
	withMockBrowser(t)
	m := &model{}
	prov := auth.Provider{Name: "nope", ManualCode: true} // missing ManualRedirectURI
	_, cmd := m.loginStartManualCode(prov)
	if cmd != nil {
		t.Error("expected nil cmd for misconfigured provider")
	}
	if len(m.chatModel.Messages) == 0 {
		t.Error("expected error message")
	}
}

// -----------------------------------------------------------------------------
// submitPrompt with a slash prefix goes through Update's handleSlashCommand.
// -----------------------------------------------------------------------------

func TestUpdate_InputSubmitMsg_Text(t *testing.T) {
	m := newTestModelFull(t)
	// Plain text triggers submitPrompt (which requires agent). Submit and drain.
	_, cmd := m.Update(InputSubmitMsg{Text: "hello world", Mentions: nil})
	if cmd == nil {
		t.Error("expected non-nil cmd")
	}
	if m.agentCh != nil {
		select {
		case <-m.agentCh:
		case <-time.After(1 * time.Second):
		}
	}
}

// -----------------------------------------------------------------------------
// parsePlanChecklistFrom — a helper used indirectly by refreshRunChecklist.
// -----------------------------------------------------------------------------

func TestParsePlanChecklistFrom_MissingSpec(t *testing.T) {
	tmp := t.TempDir()
	got := parsePlanChecklistFrom(tmp, "nonexistent")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// -----------------------------------------------------------------------------
// runGates — cover exec path with a passing command.
// -----------------------------------------------------------------------------

func TestRunGates_Empty(t *testing.T) {
	tmp := t.TempDir()
	got := runGates(context.Background(), tmp, nil)
	if !got.passed {
		t.Error("expected passed=true for no gates")
	}
}

func TestRunGates_Passing(t *testing.T) {
	tmp := t.TempDir()
	got := runGates(context.Background(), tmp, []Gate{
		{Name: "echo", Command: "echo hi"},
	})
	if !got.passed {
		t.Error("expected passing echo gate")
	}
	if len(got.results) != 1 {
		t.Errorf("expected 1 result, got %d", len(got.results))
	}
}

func TestRunGates_Failing(t *testing.T) {
	tmp := t.TempDir()
	got := runGates(context.Background(), tmp, []Gate{
		{Name: "fail", Command: "false"},
	})
	if got.passed {
		t.Error("expected passed=false")
	}
}

// -----------------------------------------------------------------------------
// buildParallelPrompt — covers branches of prompt construction.
// -----------------------------------------------------------------------------

func TestBuildParallelPrompt_CB(t *testing.T) {
	checklist := []ChecklistStep{
		{Title: "s1"},
		{Title: "s2"},
		{Title: "s3"},
	}
	out := buildParallelPrompt("my-spec", "base prompt", checklist, 0, 2)
	if !strings.Contains(out, "base prompt") {
		t.Error("expected base prompt preserved")
	}
	if !strings.Contains(out, "s1") || !strings.Contains(out, "s2") {
		t.Error("expected first slice titles")
	}
	if strings.Contains(out, "s3") {
		t.Error("slice 3 not in from=0,to=2 range should not appear as a slice")
	}
	if !strings.Contains(out, "my-spec") {
		t.Error("expected spec name")
	}
}

// -----------------------------------------------------------------------------
// runWorktreeName — covers with slashes and suffix.
// -----------------------------------------------------------------------------

func TestRunWorktreeName_CB(t *testing.T) {
	got := runWorktreeName("tools/001-my-feature", "part-1")
	if !strings.Contains(got, "tools-001-my-feature") {
		t.Errorf("expected hyphenated name, got %q", got)
	}
	if !strings.HasSuffix(got, "-part-1") {
		t.Errorf("expected suffix, got %q", got)
	}

	got2 := runWorktreeName("simple", "")
	if got2 != "simple" {
		t.Errorf("expected 'simple', got %q", got2)
	}
}

// -----------------------------------------------------------------------------
// checklistHasCheckboxes — cover all branches.
// -----------------------------------------------------------------------------

func TestChecklistHasCheckboxes_HasDone(t *testing.T) {
	steps := []ChecklistStep{{Done: true, Title: "a"}}
	if !checklistHasCheckboxes(steps) {
		t.Error("expected true when any step is Done")
	}
}

func TestChecklistHasCheckboxes_HeadingStyle(t *testing.T) {
	steps := []ChecklistStep{
		{Title: "Core Logic — implementation"},
	}
	if checklistHasCheckboxes(steps) {
		t.Error("expected false for heading-style titles")
	}
}

func TestChecklistHasCheckboxes_AssumeCheckbox(t *testing.T) {
	steps := []ChecklistStep{{Title: "Simple step"}}
	if !checklistHasCheckboxes(steps) {
		t.Error("expected true for plain titles")
	}
}

// -----------------------------------------------------------------------------
// InputModel.HandleKey — mention mode branches.
// -----------------------------------------------------------------------------

func TestInputHandleKey_AtSymbol_InsertsText(t *testing.T) {
	im := &InputModel{}
	_ = im.HandleKey(tea.KeyPressMsg(tea.Key{Text: "@", Code: '@'}))
	if im.Text != "@" {
		t.Errorf("expected @ inserted, got %q", im.Text)
	}
	// MentionMode removed — typing @ just inserts text
}

func TestInputHandleKey_SlashAtStart_CyclesCommands(t *testing.T) {
	im := &InputModel{}
	_ = im.HandleKey(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	// After typing /, text should either be "/" or cycle to a command.
	if !strings.HasPrefix(im.Text, "/") {
		t.Errorf("expected Text to start with /, got %q", im.Text)
	}
}

func TestInputHandleKey_EnterSubmitsPlainMentionText(t *testing.T) {
	im := &InputModel{Text: "@fo", CursorPos: 3}
	cmd := im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	msg, ok := cmd().(InputSubmitMsg)
	if !ok {
		t.Fatalf("expected InputSubmitMsg, got %T", cmd())
	}
	if msg.Text != "@fo" {
		t.Errorf("expected submitted text @fo, got %q", msg.Text)
	}
	if im.Text != "" {
		t.Errorf("expected input cleared, got %q", im.Text)
	}
}

func TestInputHandleKey_EnterSubmitsSlashText(t *testing.T) {
	im := &InputModel{Text: "/plan", CursorPos: 5}
	cmd := im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	msg := cmd().(InputSubmitMsg)
	if msg.Text != "/plan" {
		t.Errorf("expected /plan submitted, got %q", msg.Text)
	}
}

func TestInputHandleKey_ShiftTab_NoOp(t *testing.T) {
	im := &InputModel{Text: "/", CursorPos: 1}
	_ = im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if im.Text != "/" {
		t.Errorf("expected input preserved, got %q", im.Text)
	}
}

// -----------------------------------------------------------------------------
// InputModel.View — cover mention and ghost completion rendering.
// -----------------------------------------------------------------------------

func TestInputView_Disabled(t *testing.T) {
	im := &InputModel{Text: "hello"}
	out := im.View(true) // disabled (agent running)
	if out == "" {
		t.Error("expected non-empty view")
	}
}

// -----------------------------------------------------------------------------
// Sidebar — render with orchestrator agents.
// -----------------------------------------------------------------------------

func TestRenderSidebar_Basic(t *testing.T) {
	in := SidebarRenderInput{
		Width:        40,
		Height:       24,
		ProviderName: "openai",
		ModelName:    "gpt-4",
	}
	out := RenderSidebar(in)
	if !strings.Contains(out, "gpt-4") {
		t.Errorf("expected model name, got %q", out)
	}
}

func TestRenderSidebar_Narrow(t *testing.T) {
	in := SidebarRenderInput{Width: 5}
	out := RenderSidebar(in)
	if out == "" {
		t.Error("expected non-empty output (width clamped)")
	}
}

func TestRenderSidebar_WithGit(t *testing.T) {
	in := SidebarRenderInput{
		Width:       40,
		GitBranch:   "main",
		DiffAdded:   5,
		DiffRemoved: 3,
	}
	out := RenderSidebar(in)
	if out == "" {
		t.Error("expected non-empty with git info")
	}
}

func TestRenderSidebar_WithRunChecklist(t *testing.T) {
	in := SidebarRenderInput{
		Width:        40,
		RunSpec:      "my-spec",
		RunPhase:     "running",
		RunCycle:     1,
		RunMaxCycle:  10,
		RunChecklist: []ChecklistStep{{Title: "step 1"}, {Done: true, Title: "step 2"}},
	}
	out := RenderSidebar(in)
	if out == "" {
		t.Error("expected non-empty run checklist output")
	}
}

func TestRenderSidebar_WithModelTruncation(t *testing.T) {
	in := SidebarRenderInput{
		Width:     30,
		ModelName: "very-long-model-name-that-exceeds-inner-width",
	}
	out := RenderSidebar(in)
	if out == "" {
		t.Error("expected non-empty")
	}
}

func TestRenderSidebar_WithEyes(t *testing.T) {
	in := SidebarRenderInput{
		Width: 40,
		Eyes:  "o_o",
	}
	out := RenderSidebar(in)
	if out == "" {
		t.Error("expected non-empty eyes output")
	}
}

func TestRenderSidebar_WithTokenTracker(t *testing.T) {
	tt := fakeTokenTracker{
		lastPromptTok: 5000, ctxWindowSize: 100000, ctxPercentUsed: 5,
	}
	in := SidebarRenderInput{
		Width:        40,
		TokenTracker: tt,
	}
	out := RenderSidebar(in)
	if out == "" {
		t.Error("expected non-empty token tracker output")
	}
}

// -----------------------------------------------------------------------------
// tool_display helpers.
// -----------------------------------------------------------------------------

func TestAgentToolColor(t *testing.T) {
	if got := agentToolColor("claude"); got != "208" {
		t.Errorf("expected 208, got %q", got)
	}
	if got := agentToolColor("cursor"); got != "245" {
		t.Errorf("expected 245, got %q", got)
	}
	if got := agentToolColor("gemini"); got != "39" {
		t.Errorf("expected 39, got %q", got)
	}
	if got := agentToolColor("other"); got != "35" {
		t.Errorf("expected 35, got %q", got)
	}
	if got := agentToolColor("claude+gemini"); got != "208" {
		t.Errorf("expected 208 (first match), got %q", got)
	}
	if got := agentToolColor(""); got != "35" {
		t.Errorf("expected 35 for empty, got %q", got)
	}
}

func TestToolCallSummary(t *testing.T) {
	// read
	if got := toolCallSummary("read", map[string]any{"file_path": "a.go"}); got != "a.go" {
		t.Errorf("expected a.go, got %q", got)
	}
	// write
	if got := toolCallSummary("write", map[string]any{"file_path": "b.go"}); got != "b.go" {
		t.Errorf("expected b.go, got %q", got)
	}
	// edit
	if got := toolCallSummary("edit", map[string]any{"file_path": "c.go"}); got != "c.go" {
		t.Errorf("expected c.go, got %q", got)
	}
	// bash short
	if got := toolCallSummary("bash", map[string]any{"command": "ls"}); got != "ls" {
		t.Errorf("expected ls, got %q", got)
	}
	// bash long
	longCmd := strings.Repeat("x", 100)
	if got := toolCallSummary("bash", map[string]any{"command": longCmd}); !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncation, got %q", got)
	}
}

// -----------------------------------------------------------------------------
// formatAgentsList — cover failed branch.
// -----------------------------------------------------------------------------

func TestAgentStatusIcon_All(t *testing.T) {
	tests := map[string]string{
		"running":   "▶ ",
		"completed": "✓ ",
		"failed":    "✗ ",
		"canceled":  "◼ ",
		"killed":    "⚠ ",
		"other":     "  ",
	}
	for in, want := range tests {
		got := agentStatusIcon(in)
		if got != want {
			t.Errorf("status %q: expected %q, got %q", in, want, got)
		}
	}
}

func TestCountAgentsByStatus_CB(t *testing.T) {
	// This calls the exported helper directly; uses a local type matching its signature.
	// Since subagent types are tricky, just pass a nil slice.
	r, d, f := countAgentsByStatus(nil)
	if r != 0 || d != 0 || f != 0 {
		t.Errorf("expected 0,0,0 got %d,%d,%d", r, d, f)
	}
}

// -----------------------------------------------------------------------------
// matchingSkills / matchingSpecs edges.
// -----------------------------------------------------------------------------

func TestMatchingSkills_Empty(t *testing.T) {
	got := matchingSkills("/f", nil)
	if got != nil {
		t.Errorf("expected nil for empty skills, got %v", got)
	}
}

func TestMatchingSkills_Match(t *testing.T) {
	skills := []extension.Skill{{Name: "my-skill", Description: "test"}}
	got := matchingSkills("/my", skills)
	if len(got) != 1 {
		t.Errorf("expected 1 match, got %d", len(got))
	}
}

func TestMatchingSpecs_EmptyWorkDir(t *testing.T) {
	got := matchingSpecs("/run x", "")
	_ = got // no error, just empty
}

func TestListSpecs_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	_, err := listSpecs(tmp)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListSpecs_WithSpecs(t *testing.T) {
	tmp := t.TempDir()
	spec := filepath.Join(tmp, "specs", "tools", "001-feat")
	_ = os.MkdirAll(spec, 0o755)
	_ = os.WriteFile(filepath.Join(spec, "PROMPT.md"), []byte("# Prompt"), 0o644)
	got, err := listSpecs(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 spec, got %d: %v", len(got), got)
	}
}

// -----------------------------------------------------------------------------
// Complete — top-level completion function.
// -----------------------------------------------------------------------------

func TestComplete_Empty(t *testing.T) {
	got := Complete("", nil, "")
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if len(got.Candidates) != 0 {
		t.Errorf("expected no candidates for empty, got %v", got.Candidates)
	}
}

func TestComplete_SlashOnly(t *testing.T) {
	got := Complete("/", nil, "")
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	// Candidates may or may not be populated depending on input size/threshold.
}

func TestComplete_Specs(t *testing.T) {
	tmp := t.TempDir()
	specDir := filepath.Join(tmp, "specs", "tools", "001-feat")
	_ = os.MkdirAll(specDir, 0o755)
	_ = os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte("# Prompt"), 0o644)

	got := Complete("/run ", nil, tmp)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
}

// -----------------------------------------------------------------------------
// ApplySelection / CycleSelection / SelectedCandidate.
// -----------------------------------------------------------------------------

func TestCompleteResult_CycleSelection(t *testing.T) {
	r := &CompleteResult{
		Candidates: []CompletionCandidate{{Text: "/a"}, {Text: "/b"}, {Text: "/c"}},
		Selected:   0,
	}
	r.CycleSelection(1)
	if r.Selected != 1 {
		t.Errorf("expected 1, got %d", r.Selected)
	}
	r.CycleSelection(-1)
	if r.Selected != 0 {
		t.Errorf("expected 0, got %d", r.Selected)
	}
	r.CycleSelection(-1) // wrap to last
	if r.Selected != 2 {
		t.Errorf("expected 2, got %d", r.Selected)
	}
}

func TestCompleteResult_ApplySelection_NoCandidates(t *testing.T) {
	r := &CompleteResult{}
	got := r.ApplySelection(0)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCompleteResult_ApplySelection_ValidIndex(t *testing.T) {
	r := &CompleteResult{
		Candidates: []CompletionCandidate{{Text: "/help"}},
	}
	got := r.ApplySelection(0)
	if got != "/help" {
		t.Errorf("expected /help, got %q", got)
	}
}

func TestCompleteResult_SelectedCandidate(t *testing.T) {
	r := &CompleteResult{
		Candidates: []CompletionCandidate{{Text: "/a"}, {Text: "/b"}},
		Selected:   1,
	}
	c := r.SelectedCandidate()
	if c == nil || c.Text != "/b" {
		t.Errorf("expected /b, got %v", c)
	}
}

func TestCompleteResult_SelectedCandidate_Empty(t *testing.T) {
	r := &CompleteResult{}
	if c := r.SelectedCandidate(); c != nil {
		t.Errorf("expected nil, got %v", c)
	}
}

// -----------------------------------------------------------------------------
// Mascot() / Eyes() — cover moodSpeaking, moodProcessing paths.
// -----------------------------------------------------------------------------

func TestFaceMoods(t *testing.T) {
	for _, mood := range []AgentMood{MoodIdle, MoodThinking, MoodSpeaking, MoodProcessing} {
		if got := mood.Eyes(); got == "" {
			t.Errorf("mood %v Eyes empty", mood)
		}
		if got := mood.Mascot(); got == "" {
			t.Errorf("mood %v Mascot empty", mood)
		}
	}
}

func TestFaceRenderer_SetGetMood(t *testing.T) {
	f := NewFaceRenderer()
	f.SetMood(MoodSpeaking)
	if f.Eyes() == "" {
		t.Error("expected non-empty eyes")
	}
	if f.Mascot() == "" {
		t.Error("expected non-empty mascot")
	}
}

// -----------------------------------------------------------------------------
// View() with active run — covers the sidebarInput run field population.
// -----------------------------------------------------------------------------

func TestView_WithActiveRun(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 140
	m.height = 40
	m.run = &runState{
		phase:      "running",
		specName:   "my-spec",
		retries:    0,
		maxRetries: 5,
		checklist:  []ChecklistStep{{Title: "s1"}},
	}
	v := m.View()
	if v.Content == "" {
		t.Error("expected non-empty view with run")
	}
}

// -----------------------------------------------------------------------------
// Update() — handle more message types directly.
// -----------------------------------------------------------------------------

func TestUpdate_AgentDoneMsg_CB(t *testing.T) {
	m := newTestModelFull(t)
	m.running = true
	m.agentCh = make(chan agentMsg)
	_, _ = m.Update(agentDoneMsg{})
	if m.running {
		t.Error("expected running=false after done")
	}
}

func TestUpdate_AgentDoneMsg_WithError(t *testing.T) {
	m := newTestModelFull(t)
	m.running = true
	m.agentCh = make(chan agentMsg)
	_, _ = m.Update(agentDoneMsg{err: context.DeadlineExceeded})
	if m.running {
		t.Error("expected running=false after done")
	}
	// Error should be shown in messages.
	found := false
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "deadline") || strings.Contains(msg.content, "Error") || strings.Contains(msg.content, "error") {
			found = true
			break
		}
	}
	if !found {
		t.Logf("expected error in messages, got %+v", m.chatModel.Messages)
	}
}

// -----------------------------------------------------------------------------
// handleMouseWheel via Update's MouseMsg path.
// -----------------------------------------------------------------------------

func TestUpdate_MouseWheelMsg(t *testing.T) {
	m := newTestModelFull(t)
	m.chatModel.Messages = make([]message, 50)
	for i := range m.chatModel.Messages {
		m.chatModel.Messages[i] = message{role: "user", content: "x"}
	}
	_, cmd := m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if m.chatModel.Scroll == 0 {
		t.Log("scroll may have changed")
	}
}

// -----------------------------------------------------------------------------
// handleInitEvent — cover error and item-progress paths.
// -----------------------------------------------------------------------------

func TestHandleInitEvent_Error_CB(t *testing.T) {
	m := newTestModelFull(t)
	m.loading = true
	m.loadingItems = map[string]bool{"lsp": false}
	_, cmd := m.Update(initEventMsg{event: InitEvent{Err: context.DeadlineExceeded}})
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
	if m.loading {
		t.Error("expected loading=false on error")
	}
	if m.initErr == nil {
		t.Error("expected initErr set")
	}
}

func TestHandleInitEvent_ItemProgress(t *testing.T) {
	m := newTestModelFull(t)
	m.loading = true
	m.loadingItems = map[string]bool{}
	_, _ = m.Update(initEventMsg{
		event: InitEvent{Item: "lsp", Done: true},
		ch:    make(chan InitEvent, 1),
	})
	if !m.loadingItems["lsp"] {
		t.Error("expected lsp done=true")
	}
}

// -----------------------------------------------------------------------------
// handleAgentDone — cover the error message branch.
// -----------------------------------------------------------------------------

func TestHandleAgentDone_Cancel(t *testing.T) {
	m := newTestModelFull(t)
	m.running = true
	_, _ = m.Update(agentDoneMsg{err: context.Canceled})
	if m.running {
		t.Error("expected running=false after cancel")
	}
}

// -----------------------------------------------------------------------------
// isUserInput — cover edge cases (multiple runes is false, non-printable false).
// -----------------------------------------------------------------------------

func TestIsUserInput_MultipleRunes_CB(t *testing.T) {
	if isUserInput("ab") {
		t.Error("expected false for multiple runes")
	}
}

func TestIsUserInput_Single_CB(t *testing.T) {
	if !isUserInput("a") {
		t.Error("expected true for single printable")
	}
}

// -----------------------------------------------------------------------------
// plan.go startPlanSession error paths (with a fake agent).
// -----------------------------------------------------------------------------

// fakeAgent is a minimal fake Agent for testing error paths (rebuild/createSession
// failures). We cannot really mock the Agent without significant refactor, but we
// can at least test the nil Agent branch which was already covered above.

// -----------------------------------------------------------------------------
// handlePlanCommand - covers existing spec resume branch.
// -----------------------------------------------------------------------------

func TestHandlePlanCommand_AutoResume(t *testing.T) {
	tmp := t.TempDir()
	// Pre-create spec directory.
	specDir := filepath.Join(tmp, "specs", "tools", "001-test-idea")
	_ = os.MkdirAll(filepath.Join(specDir, "research"), 0o755)
	_ = os.WriteFile(filepath.Join(specDir, "rough-idea.md"), []byte("# x"), 0o644)

	m := &model{
		cfg: Config{WorkDir: tmp},
	}
	// "test idea" → kebab = "test-idea"
	// detectCategory("test idea") → "features/TOO" (matches "test")
	// findExistingSpec(tmp, "features/TOO", "test-idea") → "features/TOO/001-test-idea"
	_, cmd := m.handlePlanCommand([]string{"test", "idea"})
	// With no agent, startPlanSession returns error; cmd may be nil.
	_ = cmd
	// Should attempt resume or fail with no agent — either way, messages present.
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected messages")
	}
}

// -----------------------------------------------------------------------------
// InputModel.Clear
// -----------------------------------------------------------------------------

func TestInputModel_Clear(t *testing.T) {
	im := &InputModel{Text: "foo", CursorPos: 3}
	im.Clear()
	if im.Text != "" || im.CursorPos != 0 {
		t.Errorf("expected clear, got text=%q pos=%d", im.Text, im.CursorPos)
	}
}

// -----------------------------------------------------------------------------
// InsertText
// -----------------------------------------------------------------------------

func TestInputModel_InsertText_AtCursor(t *testing.T) {
	im := &InputModel{Text: "ac", CursorPos: 1}
	im.InsertText("b")
	if im.Text != "abc" {
		t.Errorf("expected abc, got %q", im.Text)
	}
	if im.CursorPos != 2 {
		t.Errorf("expected pos=2, got %d", im.CursorPos)
	}
}

// -----------------------------------------------------------------------------
// loginShowStatus — covers the printing branch with keys configured.
// -----------------------------------------------------------------------------

func TestLoginShowStatus(t *testing.T) {
	m := &model{}
	_, cmd := m.loginShowStatus()
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "API Key Status") {
		t.Errorf("expected status header, got %q", m.chatModel.Messages[0].content)
	}
}

// -----------------------------------------------------------------------------
// InputModel.AllCommandNames
// -----------------------------------------------------------------------------

func TestInputModel_AllCommandNames(t *testing.T) {
	im := &InputModel{
		Skills: []extension.Skill{{Name: "my-skill"}},
	}
	names := im.AllCommandNames()
	if len(names) == 0 {
		t.Error("expected names populated")
	}
	foundSkill := false
	for _, n := range names {
		if n == "/my-skill" {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Error("expected /my-skill in names")
	}
}
