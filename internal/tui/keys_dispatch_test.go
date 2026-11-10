package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestIsCancelKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  tea.Key
		want bool
	}{
		{"esc cancels", tea.Key{Code: tea.KeyEsc}, true},
		{"ctrl+c cancels", tea.Key{Code: 'c', Mod: tea.ModCtrl}, true},
		{"plain c does not", tea.Key{Code: 'c'}, false},
		{"ctrl+d does not", tea.Key{Code: 'd', Mod: tea.ModCtrl}, false},
		{"enter does not", tea.Key{Code: tea.KeyEnter}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCancelKey(tt.key); got != tt.want {
				t.Errorf("isCancelKey(%+v) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestBranchPopupMoveUp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		popup         branchPopupState
		wantSelected  int
		wantScrollOff int
	}{
		{
			name:          "stops at the top",
			popup:         branchPopupState{branches: []string{"a", "b"}, selected: 0, height: 2},
			wantSelected:  0,
			wantScrollOff: 0,
		},
		{
			name:          "moves up within the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 2, scrollOff: 0, height: 3},
			wantSelected:  1,
			wantScrollOff: 0,
		},
		{
			name:          "scrolls when leaving the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 2, scrollOff: 2, height: 1},
			wantSelected:  1,
			wantScrollOff: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.popup
			p.moveUp()
			if p.selected != tt.wantSelected || p.scrollOff != tt.wantScrollOff {
				t.Errorf("moveUp() = selected %d/scrollOff %d, want %d/%d",
					p.selected, p.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestBranchPopupMoveDown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		popup         branchPopupState
		wantSelected  int
		wantScrollOff int
	}{
		{
			name:          "stops at the bottom",
			popup:         branchPopupState{branches: []string{"a", "b"}, selected: 1, height: 2},
			wantSelected:  1,
			wantScrollOff: 0,
		},
		{
			name:          "moves down within the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 0, height: 3},
			wantSelected:  1,
			wantScrollOff: 0,
		},
		{
			name:          "scrolls when leaving the window",
			popup:         branchPopupState{branches: []string{"a", "b", "c"}, selected: 0, scrollOff: 0, height: 1},
			wantSelected:  1,
			wantScrollOff: 1,
		},
		{
			name:          "empty list cannot move",
			popup:         branchPopupState{branches: nil, selected: 0, height: 1},
			wantSelected:  0,
			wantScrollOff: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.popup
			p.moveDown()
			if p.selected != tt.wantSelected || p.scrollOff != tt.wantScrollOff {
				t.Errorf("moveDown() = selected %d/scrollOff %d, want %d/%d",
					p.selected, p.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestBranchPopupNavigationStaysInBounds(t *testing.T) {
	t.Parallel()
	p := branchPopupState{branches: []string{"a", "b", "c"}, height: 2}

	// Hammer both directions well past the ends; the selection must stay valid.
	for range 10 {
		p.moveDown()
	}
	if p.selected != len(p.branches)-1 {
		t.Errorf("selected = %d after many moveDown, want %d", p.selected, len(p.branches)-1)
	}
	for range 10 {
		p.moveUp()
	}
	if p.selected != 0 {
		t.Errorf("selected = %d after many moveUp, want 0", p.selected)
	}
	if p.scrollOff < 0 || p.scrollOff > len(p.branches) {
		t.Errorf("scrollOff = %d is out of range", p.scrollOff)
	}
}

func TestHandleScrollKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		key         tea.Key
		wantHandled bool
	}{
		{"page up is handled", tea.Key{Code: tea.KeyPgUp}, true},
		{"page down is handled", tea.Key{Code: tea.KeyPgDown}, true},
		{"other keys fall through", tea.Key{Code: 'x'}, false},
		{"enter falls through", tea.Key{Code: tea.KeyEnter}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &model{height: 24}
			_, _, handled := m.handleScrollKey(tt.key)
			if handled != tt.wantHandled {
				t.Errorf("handleScrollKey(%+v) handled = %v, want %v", tt.key, handled, tt.wantHandled)
			}
		})
	}
}

func TestHandleInterruptKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		key         tea.Key
		wantHandled bool
	}{
		{"esc is handled", tea.Key{Code: tea.KeyEsc}, true},
		{"ctrl+c is handled", tea.Key{Code: 'c', Mod: tea.ModCtrl}, true},
		{"f12 is swallowed", tea.Key{Code: tea.KeyF12}, true},
		{"ordinary keys fall through", tea.Key{Code: 'a'}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(t)
			_, _, handled := m.handleInterruptKey(tt.key)
			if handled != tt.wantHandled {
				t.Errorf("handleInterruptKey(%+v) handled = %v, want %v", tt.key, handled, tt.wantHandled)
			}
		})
	}
}

func TestHandleInterruptKeyEscDismissesSearchPopupFirst(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.newSearchPopup(searchModeCommands)
	if m.searchPopup == nil {
		t.Skip("search popup could not be opened in this fixture")
	}

	if _, _, handled := m.handleInterruptKey(tea.Key{Code: tea.KeyEsc}); !handled {
		t.Fatal("esc should be handled")
	}
	if m.searchPopup != nil {
		t.Error("esc should dismiss the search popup")
	}
}

func TestHandleInterruptKeyCtrlCQuitsOnSecondPress(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	ctrlC := tea.Key{Code: 'c', Mod: tea.ModCtrl}

	if _, _, _ = m.handleInterruptKey(ctrlC); m.quitting {
		t.Fatal("a single Ctrl+C should warn, not quit")
	}
	if _, _, _ = m.handleInterruptKey(ctrlC); !m.quitting {
		t.Error("a second Ctrl+C should quit")
	}
}

func TestHandleCommitKeyIgnoredWhenNotConfirming(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	// No commit in flight: every key must fall through to the layers below.
	if _, _, handled := m.handleCommitKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("commit handler should not claim keys when no commit is confirming")
	}
}

func TestHandleSkillCreateKeyIgnoredWhenNoPendingSkill(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	if _, _, handled := m.handleSkillCreateKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("skill-create handler should not claim keys with nothing pending")
	}
}

func TestHandleBranchPopupKeyIgnoredWhenClosed(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	if _, _, handled := m.handleBranchPopupKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("branch popup handler should not claim keys while closed")
	}
}

func TestHandleBranchPopupKeyDismissesOnUnknownKey(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.branchPopup = &branchPopupState{branches: []string{"main", "dev"}, height: 2}

	if _, _, handled := m.handleBranchPopupKey(tea.Key{Code: 'z'}); !handled {
		t.Fatal("an open branch popup should claim the key")
	}
	if m.branchPopup != nil {
		t.Error("an unrecognized key should dismiss the branch popup")
	}
}

func TestHandleBranchPopupKeyArrowsMoveSelection(t *testing.T) {
	t.Parallel()
	m := newTestModel(t)
	m.branchPopup = &branchPopupState{branches: []string{"main", "dev", "feat"}, height: 3}

	if _, _, handled := m.handleBranchPopupKey(tea.Key{Code: tea.KeyDown}); !handled {
		t.Fatal("down should be handled")
	}
	if m.branchPopup == nil {
		t.Fatal("arrow keys must not dismiss the popup")
	}
	if m.branchPopup.selected != 1 {
		t.Errorf("selected = %d after down, want 1", m.branchPopup.selected)
	}

	m.handleBranchPopupKey(tea.Key{Code: tea.KeyUp})
	if m.branchPopup.selected != 0 {
		t.Errorf("selected = %d after up, want 0", m.branchPopup.selected)
	}
}

// TestEscArmsThenInterrupts pins the two-press interrupt confirmation. Esc is
// also how a user dismisses a popup or changes their mind, and a single stray
// press during a long turn used to discard the turn outright.
func TestEscArmsThenInterrupts(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	esc := tea.Key{Code: tea.KeyEsc}

	// First press: armed, not interrupted, and it says so.
	if _, cmd, handled := m.handleInterruptKey(esc); !handled {
		t.Fatal("esc should be handled")
	} else if cmd == nil {
		t.Error("the first Esc should flash the confirmation, so it must return a command")
	}
	if !m.running {
		t.Fatal("a single Esc must not interrupt")
	}
	if m.escArmedUntil.IsZero() {
		t.Fatal("the first Esc must arm the confirmation")
	}

	// Second press inside the window: interrupt.
	if _, _, handled := m.handleInterruptKey(esc); !handled {
		t.Fatal("esc should be handled")
	}
	if m.running {
		t.Error("the second Esc inside the window must interrupt")
	}
	if !m.escArmedUntil.IsZero() {
		t.Error("the arm must be cleared once it has fired")
	}
}

// TestEscArmExpires covers the window itself: after escArmWindow the confirmation
// is stale, so the next press re-arms rather than interrupting. This is the case a
// bare "did they press Esc recently" boolean cannot express.
func TestEscArmExpires(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	esc := tea.Key{Code: tea.KeyEsc}

	m.handleInterruptKey(esc)
	if m.escArmedUntil.IsZero() {
		t.Fatal("the first Esc must arm")
	}
	// Backdate the deadline rather than sleeping: the rule is about the deadline,
	// not about wall-clock time passing during the test.
	m.escArmedUntil = time.Now().Add(-time.Millisecond)

	m.handleInterruptKey(esc)
	if !m.running {
		t.Error("an Esc after the window expired must re-arm, not interrupt")
	}
	if m.escArmedUntil.IsZero() {
		t.Error("the stale arm should have been replaced by a fresh one")
	}
}

// TestEscArmClearedWhenTurnEnds guards the failure that would make the feature
// harmful: if a first Esc armed and the turn then finished on its own, a stale
// deadline would let one press interrupt the *next* turn the user starts.
func TestEscArmClearedWhenTurnEnds(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	m.handleInterruptKey(tea.Key{Code: tea.KeyEsc})
	if m.escArmedUntil.IsZero() {
		t.Fatal("the first Esc must arm")
	}

	m.handleAgentDone(agentDoneMsg{})
	if !m.escArmedUntil.IsZero() {
		t.Error("a turn that ended on its own must disarm the pending confirmation")
	}

	// A new turn then needs its own two presses.
	m.running = true
	m.handleInterruptKey(tea.Key{Code: tea.KeyEsc})
	if !m.running {
		t.Error("the first Esc of a new turn must not interrupt")
	}
}

// TestEscDismissesPopupWithoutArming checks that backing out of a popup is not
// treated as the first half of an interrupt.
func TestEscDismissesPopupWithoutArming(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 1)
	m.searchPopup = &searchPopupState{mode: searchModeCommands}

	m.handleInterruptKey(tea.Key{Code: tea.KeyEsc})
	if m.searchPopup != nil {
		t.Error("esc should dismiss the popup")
	}
	if !m.escArmedUntil.IsZero() {
		t.Error("dismissing a popup must not arm the interrupt")
	}
	if !m.running {
		t.Error("dismissing a popup must not interrupt the turn")
	}
}
