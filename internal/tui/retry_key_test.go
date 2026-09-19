package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// retryModel is a model that has run one prompt, with the outcome the test wants.
func retryModel(t *testing.T, failed bool) *model {
	t.Helper()
	m := &model{
		width:       100,
		height:      40,
		ctx:         context.Background(),
		inputModel:  NewInputModel(nil, nil, nil, ""),
		statusModel: StatusModel{},
		chatModel:   NewChatModel(nil),
	}
	m.lastPrompt = "hello there"
	m.lastPromptFailed = failed
	return m
}

// Ctrl+R re-sends a prompt whose turn failed.
func TestCtrlRRetriesFailedPrompt(t *testing.T) {
	m := retryModel(t, true)

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)

	if !m.running {
		t.Fatal("Ctrl+R did not start a turn")
	}
	if cmd == nil {
		t.Error("Ctrl+R returned no command to run the retried turn")
	}
	// The prompt is back in the transcript as a user turn, not just in state.
	found := false
	for _, msg := range m.chatModel.Messages {
		if msg.role == "user" && msg.content == "hello there" {
			found = true
		}
	}
	if !found {
		t.Errorf("the retried prompt was not re-submitted; messages = %+v", m.chatModel.Messages)
	}
}

// A successful turn is not retryable: replaying it would duplicate an answer
// that is already on screen.
func TestCtrlRIgnoresSuccessfulTurn(t *testing.T) {
	m := retryModel(t, false)

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)

	if m.running {
		t.Error("Ctrl+R started a turn after a successful one")
	}
	if !strings.Contains(m.flash, "Nothing to retry") {
		t.Errorf("flash = %q, want it to say there is nothing to retry", m.flash)
	}
}

// With no previous turn there is nothing to re-send.
func TestCtrlRWithoutPromptIsQuiet(t *testing.T) {
	m := retryModel(t, false)
	m.lastPrompt = ""

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)

	if m.running {
		t.Error("Ctrl+R started a turn with no prompt to retry")
	}
}

// While a turn is running the key must not queue a second one.
func TestCtrlRIgnoredWhileRunning(t *testing.T) {
	m := retryModel(t, true)
	m.running = true

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)

	if len(m.pendingPrompts) != 0 {
		t.Errorf("Ctrl+R queued %d prompts while running", len(m.pendingPrompts))
	}
}

// A user typing a new prompt must not have it replaced by the retry.
func TestCtrlRIgnoredWithDraftText(t *testing.T) {
	m := retryModel(t, true)
	m.inputModel.SetText("a new prompt I am writing")

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)

	if m.running {
		t.Error("Ctrl+R fired while a draft was in the prompt")
	}
	if got := m.inputModel.Text; got != "a new prompt I am writing" {
		t.Errorf("the draft was clobbered: %q", got)
	}
}

// A failed turn arms the retry and says how to use it.
func TestFailedTurnArmsRetry(t *testing.T) {
	m := retryModel(t, false)
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})

	next, _ := m.Update(agentDoneMsg{err: errors.New("STREAM_ERROR: connection refused")})
	m = next.(*model)

	if !m.lastPromptFailed {
		t.Error("a failed turn did not arm the retry")
	}
	if !strings.Contains(m.chatModel.Messages[len(m.chatModel.Messages)-1].content, "/retry") {
		t.Errorf("no retry hint after the failure: %q",
			m.chatModel.Messages[len(m.chatModel.Messages)-1].content)
	}
}

// /retry is the discoverable form of the same action.
func TestRetrySlashCommand(t *testing.T) {
	m := retryModel(t, true)

	next, cmd := m.handleSlashCommand("/retry")
	m = next.(*model)

	if !m.running {
		t.Fatal("/retry did not start a turn")
	}
	if cmd == nil {
		t.Error("/retry returned no command")
	}
}

// A retry that itself fails stays retryable.
func TestRetryOfRetryStaysArmed(t *testing.T) {
	m := retryModel(t, true)

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)
	next, _ = m.Update(agentDoneMsg{err: errors.New("still refused")})
	m = next.(*model)

	if !m.lastPromptFailed {
		t.Error("a failed retry disarmed the retry")
	}
}

// /clear drops the retry offer along with the transcript it belongs to.
func TestClearConversationDisarmsRetry(t *testing.T) {
	m := retryModel(t, true)

	m.clearConversation()

	if m.lastPrompt != "" || m.lastPromptFailed {
		t.Errorf("clearConversation left retry state: prompt=%q failed=%v",
			m.lastPrompt, m.lastPromptFailed)
	}
}

// Ctrl+R is shared with history search: with a popup open the search owns the
// key, so a retry must not run a turn behind the open popup.
func TestCtrlRRetryDoesNotFireUnderPopup(t *testing.T) {
	m := retryModel(t, true)
	m.inputModel.History = []HistoryEntry{{Text: "older prompt"}}
	m.newSearchPopup(searchModeHistory)
	if m.searchPopup == nil {
		t.Fatal("history popup did not open; the test cannot exercise the gate")
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = next.(*model)

	if m.running {
		t.Error("Ctrl+R retried while a search popup was open")
	}
}

// Ctrl+H is also readline's delete-backward. With text in the prompt the input
// must keep the key rather than losing it to history search.
func TestCtrlHWithTextDoesNotOpenHistory(t *testing.T) {
	m := retryModel(t, false)
	m.inputModel.History = []HistoryEntry{{Text: "older prompt"}}
	m.inputModel.SetText("editing this")

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Mod: tea.ModCtrl}))
	m = next.(*model)

	if m.searchPopup != nil {
		t.Error("Ctrl+H opened history search while text was being edited")
	}
}

// On an empty prompt Ctrl+H is history search.
func TestCtrlHOpensHistorySearch(t *testing.T) {
	m := retryModel(t, false)
	m.inputModel.History = []HistoryEntry{{Text: "older prompt"}}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Mod: tea.ModCtrl}))
	m = next.(*model)

	if m.searchPopup == nil || m.searchPopup.mode != searchModeHistory {
		t.Fatalf("Ctrl+H did not open history search (popup = %+v)", m.searchPopup)
	}
}
