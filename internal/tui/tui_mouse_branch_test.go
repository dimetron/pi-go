package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// -----------------------------------------------------------------------
// handleMouseClick tests
// -----------------------------------------------------------------------

func TestHandleMouseClick(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// Mouse click should return the model unchanged
	newM, cmd := m.handleMouseClick(tea.MouseClickMsg{})
	mm := newM.(*model)

	if mm != m {
		t.Error("handleMouseClick should return the same model")
	}
	if cmd != nil {
		t.Error("handleMouseClick should not return a command")
	}
}

// -----------------------------------------------------------------------
// handleBranchSelect tests
// -----------------------------------------------------------------------

func TestHandleBranchSelect_NilPopup(t *testing.T) {
	m := &model{
		branchPopup: nil,
		chatModel:   ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleBranchSelect()
	mm := newM.(*model)

	if mm.branchPopup != nil {
		t.Error("branchPopup should remain nil when it was nil")
	}
}

func TestHandleBranchSelect_EmptyBranches(t *testing.T) {
	m := &model{
		branchPopup: &branchPopupState{
			branches: []string{},
		},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleBranchSelect()
	mm := newM.(*model)

	if mm.branchPopup != nil {
		t.Error("branchPopup should be cleared when branches is empty")
	}
}

func TestHandleBranchSelect_SameBranch(t *testing.T) {
	m := &model{
		branchPopup: &branchPopupState{
			branches: []string{"main", "feature"},
			selected: 0,
			active:   "main",
		},
		statusModel: StatusModel{GitBranch: "main"},
		chatModel:   ChatModel{Messages: make([]message, 0)},
		cfg:         Config{WorkDir: ""},
	}

	newM, _ := m.handleBranchSelect()
	mm := newM.(*model)

	if mm.branchPopup != nil {
		t.Error("branchPopup should be cleared when selecting same branch")
	}
}

func TestHandleBranchSelect_GitCheckoutFails(t *testing.T) {
	m := &model{
		branchPopup: &branchPopupState{
			branches: []string{"nonexistent-branch"},
			selected: 0,
			active:   "main",
		},
		statusModel: StatusModel{GitBranch: "main"},
		chatModel:   ChatModel{Messages: make([]message, 0)},
		cfg:         Config{WorkDir: ""},
	}

	newM, _ := m.handleBranchSelect()
	mm := newM.(*model)

	if mm.branchPopup != nil {
		t.Error("branchPopup should be cleared after selection attempt")
	}
}

// -----------------------------------------------------------------------
// waitForInitEvent tests
// -----------------------------------------------------------------------

func TestWaitForInitEvent(t *testing.T) {
	ch := make(chan InitEvent, 1)
	ch <- InitEvent{Item: "test", Done: true}

	cmd := waitForInitEvent(ch)
	msg := cmd()

	evtMsg, ok := msg.(initEventMsg)
	if !ok {
		t.Fatalf("expected initEventMsg, got %T", msg)
	}

	if evtMsg.event.Item != "test" {
		t.Errorf("expected Item 'test', got %q", evtMsg.event.Item)
	}
	if !evtMsg.event.Done {
		t.Error("expected Done to be true")
	}
}

func TestWaitForInitEvent_ChannelClosed(t *testing.T) {
	ch := make(chan InitEvent)
	close(ch)

	cmd := waitForInitEvent(ch)
	msg := cmd()

	evtMsg, ok := msg.(initEventMsg)
	if !ok {
		t.Fatalf("expected initEventMsg, got %T", msg)
	}

	if evtMsg.event.Err == nil {
		t.Error("expected error when channel is closed")
	}
}

// -----------------------------------------------------------------------
// handleInitEvent tests
// -----------------------------------------------------------------------

func TestHandleInitEvent_Error(t *testing.T) {
	sentinelErr := errors.New("init failed")

	m := &model{
		loading:      true,
		loadingItems: map[string]bool{"tools": false},
		initCh:       make(chan InitEvent),
	}

	msg := initEventMsg{
		event: InitEvent{Err: sentinelErr},
		ch:    m.initCh,
	}

	newM, cmd := m.handleInitEvent(msg)
	mm := newM.(*model)

	// On error, loading is set to false and tea.Quit is returned
	if mm.loading {
		t.Error("loading should be false on error")
	}
	if mm.initErr == nil {
		t.Error("initErr should be set on error")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command on error")
	}
}

func TestHandleInitEvent_ProgressItem(t *testing.T) {
	m := &model{
		loading:      true,
		loadingItems: map[string]bool{},
		initCh:       make(chan InitEvent),
	}

	msg := initEventMsg{
		event: InitEvent{Item: "tools", Done: true},
		ch:    m.initCh,
	}

	newM, _ := m.handleInitEvent(msg)
	mm := newM.(*model)

	if !mm.loading {
		t.Error("loading should remain true during progress")
	}
	if !mm.loadingItems["tools"] {
		t.Error("tools should be marked as done")
	}
}

func TestHandleInitEvent_FinalResult(t *testing.T) {
	agentEventCh := make(chan AgentSubEvent, 1)
	restartCh := make(chan struct{}, 1)

	m := &model{
		loading:      true,
		loadingItems: map[string]bool{},
		initCh:       make(chan InitEvent),
	}

	msg := initEventMsg{
		event: InitEvent{
			Done: true,
			Result: &InitResult{
				AgentEventCh: agentEventCh,
				RestartCh:    restartCh,
				GitBranch:    "main",
				DiffAdded:    10,
				DiffRemoved:  5,
			},
		},
		ch: m.initCh,
	}

	newM, cmd := m.handleInitEvent(msg)
	mm := newM.(*model)

	if mm.loading {
		t.Error("loading should be false after final result")
	}
	if mm.cfg.AgentEventCh == nil {
		t.Error("AgentEventCh should be set")
	}
	if mm.cfg.RestartCh == nil {
		t.Error("RestartCh should be set")
	}
	if mm.statusModel.GitBranch != "main" {
		t.Errorf("expected GitBranch 'main', got %q", mm.statusModel.GitBranch)
	}
	if mm.diffAdded != 10 {
		t.Errorf("expected diffAdded 10, got %d", mm.diffAdded)
	}
	if mm.diffRemoved != 5 {
		t.Errorf("expected diffRemoved 5, got %d", mm.diffRemoved)
	}
	if cmd == nil {
		t.Error("expected command to continue listening")
	}
}

func TestHandleInitEvent_KeepReading(t *testing.T) {
	m := &model{
		loading:      true,
		loadingItems: map[string]bool{},
		initCh:       make(chan InitEvent),
	}

	msg := initEventMsg{
		event: InitEvent{Item: "tools", Done: false},
		ch:    m.initCh,
	}

	newM, cmd := m.handleInitEvent(msg)
	mm := newM.(*model)

	if !mm.loading {
		t.Error("loading should remain true")
	}
	if cmd == nil {
		t.Error("expected command to keep reading init events")
	}
}

// -----------------------------------------------------------------------
// resetCtrlCCount tests
// -----------------------------------------------------------------------

func TestResetCtrlCCount(t *testing.T) {
	m := &model{
		ctrlCCount: 2,
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	newM, _ := m.handleResetCtrlCCount()
	mm := newM.(*model)

	if mm.ctrlCCount != 0 {
		t.Errorf("expected ctrlCCount 0, got %d", mm.ctrlCCount)
	}
}

func TestResetCtrlCCountCmd(t *testing.T) {
	// Test that resetCtrlCCount returns a command that will eventually
	// send the reset message (after 2 second delay - we don't actually wait)
	cmd := resetCtrlCCount(nil)
	if cmd == nil {
		t.Error("expected non-nil command")
	}
}

// -----------------------------------------------------------------------
// newBranchPopup tests
// -----------------------------------------------------------------------

func TestNewBranchPopup_NoBranches(t *testing.T) {
	m := &model{
		statusModel: StatusModel{GitBranch: "main"},
		branchPopup: nil,
		cfg:         Config{WorkDir: "/nonexistent"},
	}

	m.newBranchPopup()

	if m.branchPopup != nil {
		t.Error("branchPopup should be nil when no branches found")
	}
}

func TestNewBranchPopup_WithBranches(t *testing.T) {
	m := &model{
		statusModel: StatusModel{GitBranch: "main"},
		branchPopup: nil,
		cfg:         Config{WorkDir: ""},
	}

	m.newBranchPopup()

	if m.branchPopup == nil {
		t.Error("branchPopup should be created")
	}
	if len(m.branchPopup.branches) == 0 {
		t.Error("branches should not be empty for a git repo")
	}
}

func TestNewBranchPopup_SetsActiveIndex(t *testing.T) {
	m := &model{
		statusModel: StatusModel{GitBranch: "feature"},
		branchPopup: nil,
		cfg:         Config{WorkDir: ""},
	}

	m.newBranchPopup()

	if m.branchPopup == nil {
		t.Skip("no git repo available")
	}

	if m.branchPopup.active != "feature" {
		t.Errorf("expected active 'feature', got %q", m.branchPopup.active)
	}

	// Find the index of "feature" branch
	found := false
	for i, b := range m.branchPopup.branches {
		if b == "feature" {
			found = true
			if m.branchPopup.selected != i {
				t.Errorf("expected selected %d (feature), got %d", i, m.branchPopup.selected)
			}
			break
		}
	}
	if !found {
		t.Log("feature branch not found in branches list")
	}
}

// -----------------------------------------------------------------------
// renderBranchPopup tests
// -----------------------------------------------------------------------

func TestRenderBranchPopup_Nil(t *testing.T) {
	m := &model{
		branchPopup: nil,
	}

	result := m.renderBranchPopup()
	if result != "" {
		t.Errorf("expected empty string for nil popup, got %q", result)
	}
}

func TestRenderBranchPopup_WithData(t *testing.T) {
	m := &model{
		branchPopup: &branchPopupState{
			branches:  []string{"main", "feature"},
			selected:  1,
			active:    "main",
			height:    10,
			scrollOff: 0,
		},
		width:  80,
		height: 24,
	}

	result := m.renderBranchPopup()
	if result == "" {
		t.Error("expected non-empty result for valid popup")
	}
}

func TestRenderBranchPopup_Scrolling(t *testing.T) {
	m := &model{
		branchPopup: &branchPopupState{
			branches:  []string{"b1", "b2", "b3", "b4", "b5"},
			selected:  4, // beyond visible height
			active:    "b1",
			height:    3,
			scrollOff: 2, // scrolled down
		},
		width:  80,
		height: 24,
	}

	result := m.renderBranchPopup()
	if result == "" {
		t.Error("expected non-empty result for valid popup")
	}
}
