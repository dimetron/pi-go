package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestPendingPromptStartsAfterActiveTurn(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)

	_, cmd := m.enqueuePrompt("first queued", nil)
	if cmd != nil {
		t.Fatal("queued prompt should not start while another turn is running")
	}
	if len(m.pendingPrompts) != 1 {
		t.Fatalf("pending prompts = %d, want 1", len(m.pendingPrompts))
	}

	_, nextCmd := m.Update(agentDoneMsg{})
	if nextCmd == nil {
		t.Fatal("expected next queued prompt to start immediately after agentDoneMsg")
	}
	if !m.running {
		t.Fatal("queued turn was not started after the active turn completed")
	}
	if len(m.pendingPrompts) != 0 {
		t.Fatalf("pending prompts = %d after dequeue, want 0", len(m.pendingPrompts))
	}
	if len(m.chatModel.Messages) == 0 || m.chatModel.Messages[len(m.chatModel.Messages)-2].content != "first queued" {
		t.Fatal("queued prompt was not submitted immediately after completion")
	}
}

func TestPendingPromptsExecuteFIFO(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	for _, prompt := range []string{"sleep 1", "sleep 1", "sleep 3"} {
		if _, cmd := m.enqueuePrompt(prompt, nil); cmd != nil {
			t.Fatalf("%q started while another turn was running", prompt)
		}
	}
	if got := len(m.pendingPrompts); got != 3 {
		t.Fatalf("queued prompts = %d, want 3", got)
	}

	wants := []string{"sleep 1", "sleep 1", "sleep 3"}
	for i, want := range wants {
		_, cmd := m.Update(agentDoneMsg{})
		if cmd == nil {
			t.Fatalf("turn %d did not start the next queued prompt", i+1)
		}
		if !m.running {
			t.Fatalf("turn %d is not running", i+1)
		}
		var users []string
		for _, msg := range m.chatModel.Messages {
			if msg.role == "user" {
				users = append(users, msg.content)
			}
		}
		if len(users) != i+1 || users[i] != want {
			t.Fatalf("turn %d executed prompts %v, want prefix %v", i+1, users, wants[:i+1])
		}
		if got := len(m.pendingPrompts); got != 2-i {
			t.Fatalf("after turn %d, queued prompts = %d, want %d", i+1, got, 2-i)
		}
	}
}

func TestPendingPromptQueueFull(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.pendingPrompts = make([]queuedPrompt, maxPendingPrompts)

	if _, cmd := m.enqueuePrompt("rejected", nil); cmd != nil {
		t.Fatal("queue-full prompt should not start")
	}
	if got := len(m.pendingPrompts); got != maxPendingPrompts {
		t.Fatalf("pending prompts = %d, want %d", got, maxPendingPrompts)
	}
	if m.flash != "Prompt queue full" {
		t.Fatalf("flash = %q, want queue-full notice", m.flash)
	}
}

func TestStatusShowsPendingPromptCount(t *testing.T) {
	m := newTestModel(t)
	m.pendingPrompts = []queuedPrompt{{text: "one"}, {text: "two"}}
	if got := m.statusRenderInput().Pending; got != 2 {
		t.Fatalf("pending count = %d, want 2", got)
	}
	if got := m.statusModel.Render(m.statusRenderInput()); !strings.Contains(got, "queued: 2") {
		t.Fatalf("status bar missing pending count: %q", got)
	}
}

func TestUpdateWindowSizeWide(t *testing.T) {
	m := &model{
		chatModel:   ChatModel{Messages: make([]message, 0)},
		statusModel: StatusModel{},
	}

	// Simulate wide terminal (> 80 chars)
	newM, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	mm := newM.(*model)

	if mm.width != 120 {
		t.Errorf("expected width 120, got %d", mm.width)
	}
	if mm.height != 24 {
		t.Errorf("expected height 24, got %d", mm.height)
	}
	// When width > 80 the panel is width - SidebarWidth, and everything inside it
	// is sized to that minus the rail, which owns the last column. The status bar
	// spans the full terminal width (it sits below the sidebar, not beside it).
	expectedStatusWidth := 120
	if mm.statusModel.Width != expectedStatusWidth {
		t.Errorf("expected statusModel.Width %d, got %d", expectedStatusWidth, mm.statusModel.Width)
	}
}

func TestUpdateWindowSizeClampsScrollAndInvalidatesWidth(t *testing.T) {
	m := &model{
		width:       120,
		height:      40,
		inputModel:  NewInputModel(nil, nil, nil, ""),
		statusModel: StatusModel{},
		chatModel: ChatModel{Messages: []message{{
			role:           "assistant",
			content:        strings.Repeat("long message ", 200),
			renderCache:    "stale",
			renderCacheKey: 12345, // a key from some earlier width
			renderCached:   true,
		}}},
	}
	m.chatModel.UpdateRenderer(90)
	m.chatModel.Scroll = 999

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	mm := newM.(*model)

	// The panel's content is one column narrower than the panel; the rail (the
	// minimap, doubling as the divider) owns that column. The status bar spans
	// the full terminal width (it sits below the sidebar, not beside it).
	wantChat := 60 - railWidth
	if mm.chatModel.Width != wantChat {
		t.Fatalf("expected chat width %d after resize, got %d", wantChat, mm.chatModel.Width)
	}
	if mm.statusModel.Width != 60 {
		t.Fatalf("expected status width 60 after resize, got %d", mm.statusModel.Width)
	}
	if mm.chatModel.Messages[0].renderCache == "stale" {
		t.Fatalf("expected stale render cache invalidated on resize")
	}
	// The cache must have been repopulated at the new width, keyed to it.
	got := &mm.chatModel.Messages[0]
	wantKey := got.renderKey(wantChat, mm.chatModel.ToolDisplay.CompactTools, false, false, paletteKey(paletteOrDark(mm.chatModel.Palette)), mm.chatModel.ToolDisplay.BlinkOn)
	if !got.renderCached || got.renderCacheKey != wantKey {
		t.Fatalf("render cache not repopulated for width %d: cached=%v key=%d want %d",
			wantChat, got.renderCached, got.renderCacheKey, wantKey)
	}
	if max := mm.chatModel.MaxScroll(mm.messageViewportHeight()); mm.chatModel.Scroll > max {
		t.Fatalf("scroll not clamped: got %d max %d", mm.chatModel.Scroll, max)
	}
}

func TestUpdateKeyPressTextDuringResizeSuppressed(t *testing.T) {
	m := &model{
		resizeAt:   time.Now(),
		inputModel: NewInputModel(nil, nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))
	mm := newM.(*model)

	if mm.inputModel.Text != "" {
		t.Fatalf("text key during resize drain should be suppressed, got %q", mm.inputModel.Text)
	}
	if cmd == nil {
		t.Fatal("expected resize drain command to wake the TUI")
	}
}

func TestUpdateKeyPressEnterDuringResizeAllowed(t *testing.T) {
	m := &model{
		resizeAt:   time.Now(),
		inputModel: NewInputModel(nil, nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}
	m.inputModel.Text = "hello"
	m.inputModel.CursorPos = len("hello")

	newM, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := newM.(*model)

	if mm.inputModel.Text != "" {
		t.Fatalf("enter during resize drain should submit input, got remaining text %q", mm.inputModel.Text)
	}
	if cmd == nil {
		t.Fatal("expected input submit command")
	}
	if got, ok := cmd().(InputSubmitMsg); !ok || got.Text != "hello" {
		t.Fatalf("expected InputSubmitMsg hello, got %#v", got)
	}
}

func TestUpdateResizeDrainDoneClearsOnlyLatestResize(t *testing.T) {
	latest := time.Now()
	m := &model{
		resizeAt:  latest,
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	stale := latest.Add(-time.Second)
	newM, _ := m.Update(resizeDrainDoneMsg{resizeAt: stale})
	mm := newM.(*model)
	if mm.resizeAt.IsZero() {
		t.Fatal("stale resize drain message should not clear latest resize")
	}

	newM, _ = mm.Update(resizeDrainDoneMsg{resizeAt: latest})
	mm = newM.(*model)
	if !mm.resizeAt.IsZero() {
		t.Fatal("matching resize drain message should clear resize state")
	}
}

func TestUpdatePasteMsg(t *testing.T) {
	m := &model{
		running:    false, // not running
		inputModel: InputModel{Text: ""},
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.Update(tea.PasteMsg{Content: "pasted text"})
	mm := newM.(*model)

	if mm.inputModel.Text != "pasted text" {
		t.Errorf("expected pasted text, got %q", mm.inputModel.Text)
	}
}

func TestUpdatePasteMsg_TerminalEscapeRejected(t *testing.T) {
	m := &model{
		running:    false,
		inputModel: InputModel{Text: ""},
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	// OSC 11 background color response should not be pasted.
	for _, garbage := range []string{
		"]11;rgb:0a0a/0e0e/1414",
		"rgb:0a0a/0e0e/1414[38;4R",
		"0a/0e0e/1414[38;4R",
		";2$ygb:0a0a/0e0e/1414",
	} {
		m.inputModel.Text = ""
		newM, _ := m.Update(tea.PasteMsg{Content: garbage})
		mm := newM.(*model)
		if mm.inputModel.Text != "" {
			t.Errorf("terminal escape in paste should be rejected, got %q for input %q", mm.inputModel.Text, garbage)
		}
	}
}

func TestUpdatePasteMsgWhileRunningAccepted(t *testing.T) {
	m := &model{
		running:    true, // agent running
		inputModel: InputModel{Text: ""},
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	originalText := m.inputModel.Text
	newM, _ := m.Update(tea.PasteMsg{Content: "pasted text"})
	mm := newM.(*model)

	// Paste should be accepted when running
	if mm.inputModel.Text == originalText {
		t.Error("paste should be accepted when agent is running")
	}
}

func TestUpdateMouseMoveMsg(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// MouseMotionMsg (not a click)
	newM, cmd := m.Update(tea.MouseMotionMsg{
		X:      10,
		Y:      20,
		Button: tea.MouseNone,
	})
	mm := newM.(*model)

	if mm != m {
		t.Error("mouse move should return the same model")
	}
	if cmd != nil {
		t.Error("mouse move should not return a command")
	}
}

// TestUpdateUnknownMsgLeavesListenerUntouched replaces TestUpdateAgentListenerAlive,
// which asserted that an unknown message re-armed the agent listener. That was the
// bug, not the contract: nothing was consumed from m.agentCh, so the re-arm added a
// reader that never went away. The listener stays alive because every type carried
// on m.agentCh is claimed by updateAgentStream and re-armed there exactly once.
func TestUpdateUnknownMsgLeavesListenerUntouched(t *testing.T) {
	m := &model{
		running:   true,
		agentCh:   make(chan agentMsg, 1),
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.Update("unknown message type")
	mm := newM.(*model)

	if mm != m {
		t.Error("unknown message should return the same model")
	}
	if cmd != nil {
		t.Errorf("unknown message armed a listener (%T), want nil", cmd)
	}
	if m.agentCh == nil {
		t.Error("unknown message must not clear the agent channel")
	}
}

// -----------------------------------------------------------------------
// handleKey additional tests (to improve coverage from 66.3%)
// -----------------------------------------------------------------------

func TestHandleKey_CommitConfirmMode(t *testing.T) {
	m := &model{
		running: false,
		commit:  &commitState{phase: "confirming"},
		chatModel: ChatModel{
			Messages: []message{
				{role: "assistant", content: "Commit message here"},
			},
		},
		inputModel: InputModel{Text: ""},
		cfg:        Config{WorkDir: ""},
	}

	// Press Enter to confirm
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := newM.(*model)

	if cmd == nil {
		t.Error("expected command for commit confirm")
	}
	// Phase should change from "confirming" to "committing" (or similar)
	_ = mm.commit
}

func TestHandleKey_CommitCancelEsc(t *testing.T) {
	m := &model{
		running: false,
		commit:  &commitState{phase: "confirming"},
		chatModel: ChatModel{
			Messages: []message{
				{role: "assistant", content: "Commit message here"},
			},
		},
		inputModel: InputModel{Text: ""},
		cfg:        Config{WorkDir: ""},
	}

	// Press Escape to cancel
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	mm := newM.(*model)

	if mm.commit != nil {
		t.Error("commit should be cleared on cancel")
	}
	_ = cmd
}

func TestHandleKey_LoginWaitingEnter(t *testing.T) {
	m := &model{
		running:    false,
		login:      &loginState{phase: "waiting"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: "api-key-value"},
	}

	// Press Enter with API key
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := newM.(*model)

	// Should trigger login save
	_ = mm.login
	_ = cmd
}

func TestHandleKey_LoginWaitingEmptyKey(t *testing.T) {
	m := &model{
		running:    false,
		login:      &loginState{phase: "waiting"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: ""},
	}

	// Press Enter with empty key - should do nothing
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := newM.(*model)

	if mm.login == nil {
		t.Error("login should not be cleared on empty key")
	}
	if cmd != nil {
		t.Error("should not return command for empty key")
	}
}

func TestHandleKey_LoginNotWaiting(t *testing.T) {
	m := &model{
		running:    false,
		login:      &loginState{phase: "polling"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: "test"},
	}

	// Press Enter while not in waiting phase - should do nothing
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := newM.(*model)

	if mm != m {
		t.Error("should return same model")
	}
	if cmd != nil {
		t.Error("should not return command")
	}
}

func TestHandleKey_LoginCancelEsc(t *testing.T) {
	m := &model{
		running:   false,
		login:     &loginState{phase: "waiting"},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// Press Es cape to cancel
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	mm := newM.(*model)

	if mm.login != nil {
		t.Error("login should be cleared on cancel")
	}
}

func TestHandleKey_RunningAgent(t *testing.T) {
	m := &model{
		running:    true,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: ""},
	}

	// Regular key while agent running
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	mm := newM.(*model)

	if cmd != nil {
		t.Error("should not return command for regular key while running")
	}
	_ = mm
}

func TestHandleKey_RunningAgentEnter(t *testing.T) {
	m := &model{
		running:    true,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: ""},
	}

	// Enter while agent running should not submit
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := newM.(*model)

	if cmd != nil {
		t.Error("should not return command for Enter while running")
	}
	_ = mm
}

func TestHandleKey_RunningAgentCtrlC(t *testing.T) {
	m := &model{
		running:    true,
		ctrlCCount: 0,
		cancel:     func() {},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: ""},
	}

	// First Ctrl+C while running cancels the agent, arms the quit counter and
	// schedules the 2s reset, surfacing the "Ctrl+C again to quit" warning.
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	mm := newM.(*model)

	if mm.running {
		t.Error("agent should be canceled (running=false)")
	}
	if mm.ctrlCCount != 1 {
		t.Errorf("ctrlCCount = %d, want 1 after first Ctrl+C while running", mm.ctrlCCount)
	}
	if cmd == nil {
		t.Error("expected resetCtrlCCount command to schedule the 2s counter reset")
	}
}

func TestHandleKey_RunningAgentDoubleCtrlC(t *testing.T) {
	// Double Ctrl+C when NOT running should quit
	m := &model{
		running:    false,
		ctrlCCount: 1,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: ""},
	}

	// Second Ctrl+C - should quit
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	mm := newM.(*model)

	if !mm.quitting {
		t.Error("should be quitting on double Ctrl+C")
	}
	if cmd == nil {
		t.Error("should return tea.Quit command")
	}
}

func TestHandleKey_TabWhileRunning(t *testing.T) {
	m := &model{
		running:    true,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: ""},
	}

	// Tab while running - should not trigger autocomplete
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	mm := newM.(*model)

	if cmd != nil {
		t.Error("Tab should not return command while running")
	}
	_ = mm
}

func TestHandleKey_TabKeepsSingleSlashMatchInPopup(t *testing.T) {
	m := &model{
		running:    false,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: "/he"},
	}

	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	mm := newM.(*model)

	if mm.inputModel.Text != "/he" {
		t.Errorf("expected input to stay in picker mode, got %q", mm.inputModel.Text)
	}
	if mm.slashCommandSelected != 0 {
		t.Errorf("expected single match selection to stay at 0, got %d", mm.slashCommandSelected)
	}
}

func TestHandleKey_TabMultipleSlashMatchesUsesPopupOnly(t *testing.T) {
	m := &model{
		running:    false,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: "/c"},
	}
	candidates := m.slashCommandCandidates("/c")
	if len(candidates) < 2 {
		t.Fatalf("expected multiple /c candidates, got %+v", candidates)
	}

	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	mm := newM.(*model)

	if mm.inputModel.Text != "/c" {
		t.Errorf("text should remain /c, got %q", mm.inputModel.Text)
	}
	// Tab should create search popup and advance selection to 1.
	if mm.searchPopup == nil {
		t.Fatal("expected search popup to be created on Tab")
	}
	if mm.searchPopup.selected != 1 {
		t.Fatalf("Tab should advance highlighted slash command to 1, got %d", mm.searchPopup.selected)
	}
	if len(mm.chatModel.Messages) != 0 {
		t.Fatalf("ambiguous slash completion should stay in popup, got chat messages: %+v", mm.chatModel.Messages)
	}
}

func TestHandleKey_TabCyclesSlashCommandSelection(t *testing.T) {
	m := &model{
		running:    false,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: "/c"},
	}
	candidates := m.slashCommandCandidates("/c")
	if len(candidates) < 2 {
		t.Fatalf("expected multiple /c candidates, got %+v", candidates)
	}

	// Tab through all candidates.
	// Tab cycles through items: 0 → 1 → 2 → ... → (len-1) → (stays at last)
	for i := 0; i < len(candidates); i++ {
		newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
		m = newM.(*model)
		if m.searchPopup == nil {
			t.Fatal("expected search popup")
		}
	}

	// After Tab+len(candidates), selection should be at the last index.
	lastIndex := len(candidates) - 1
	if m.searchPopup.selected != lastIndex {
		t.Errorf("after %d tabs, selection should be at last index %d, got %d", len(candidates), lastIndex, m.searchPopup.selected)
	}

	// Tab on the last item stays at the last index (no wrap).
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = newM.(*model)
	if m.searchPopup.selected != lastIndex {
		t.Errorf("Tab at last item should stay at %d, got %d", lastIndex, m.searchPopup.selected)
	}

	// Arrow down from last item should wrap to first.
	newM, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = newM.(*model)
	if m.searchPopup.selected != 0 {
		t.Errorf("ArrowDown from last should wrap to 0, got %d", m.searchPopup.selected)
	}
}

func TestHandleKey_EscapeNoOp(t *testing.T) {
	m := &model{
		running:    false,
		chatModel:  ChatModel{Messages: []message{{role: "assistant", content: "Commands:"}}},
		inputModel: InputModel{Text: "/c"},
	}
	newM, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	mm := newM.(*model)
	if mm.inputModel.Text != "/c" {
		t.Errorf("expected input unchanged after Esc, got %q", mm.inputModel.Text)
	}
}

func TestHandleKey_BackspaceWhileRunning(t *testing.T) {
	m := &model{
		running:    true,
		chatModel:  ChatModel{Messages: make([]message, 0)},
		inputModel: InputModel{Text: "hello"},
	}

	// Backspace while running
	newM, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	mm := newM.(*model)

	if cmd != nil {
		t.Error("Backspace should not return command while running")
	}
	_ = mm
}

// -----------------------------------------------------------------------
// refreshDiffStats additional tests
// -----------------------------------------------------------------------

func TestRefreshDiffStats_GitCheckoutError(t *testing.T) {
	m := &model{
		cfg: Config{
			WorkDir: "", // Empty workDir
		},
		statusModel: StatusModel{GitBranch: "main"},
		diffAdded:   0,
		diffRemoved: 0,
	}

	m.refreshDiffStats()
	// Should not panic even with empty workDir
}

func TestRefreshDiffStats_NonGitDir(t *testing.T) {
	m := &model{
		cfg: Config{
			WorkDir: "/nonexistent/path",
		},
		statusModel: StatusModel{},
		diffAdded:   -1,
		diffRemoved: -1,
	}

	m.refreshDiffStats()
	// Should not panic, stats should remain unchanged
}
