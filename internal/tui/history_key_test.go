package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func historyModel(t *testing.T, entries ...string) *model {
	t.Helper()
	history := make([]HistoryEntry, len(entries))
	for i, e := range entries {
		history[i] = HistoryEntry{Text: e}
	}
	return &model{
		width:       100,
		height:      40,
		inputModel:  NewInputModel(history, nil, nil, ""),
		statusModel: StatusModel{},
		chatModel:   NewChatModel(nil),
	}
}

func press(t *testing.T, m *model, code rune) *model {
	t.Helper()
	next, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: code}))
	mm, ok := next.(*model)
	if !ok {
		t.Fatalf("handleKey returned %T, want *model", next)
	}
	return mm
}

// Up opens the history window rather than cycling entries into the prompt.
func TestUpOpensHistoryWindow(t *testing.T) {
	m := historyModel(t, "first", "second", "third")

	m = press(t, m, tea.KeyUp)

	if m.searchPopup == nil {
		t.Fatal("Up did not open a popup")
	}
	if m.searchPopup.mode != searchModeHistory {
		t.Fatalf("popup mode = %q, want %q", m.searchPopup.mode, searchModeHistory)
	}
	if len(m.searchPopup.filtered) != 3 {
		t.Fatalf("popup shows %d entries, want 3", len(m.searchPopup.filtered))
	}
	// Most recent is at the top and preselected, so Up-then-Enter recalls the
	// last prompt — the thing the old inline binding did in one keystroke.
	if got := m.searchPopup.filtered[0].Text; got != "third" {
		t.Errorf("top entry = %q, want the most recent (third)", got)
	}
	if m.searchPopup.selected != 0 {
		t.Errorf("selected = %d, want 0 (most recent)", m.searchPopup.selected)
	}
}

// Opening the window must not touch the prompt — that is what makes a stray Up
// (or an over-eager terminal) harmless, unlike the old inline cycling which
// overwrote whatever was typed.
func TestUpLeavesPromptUntouched(t *testing.T) {
	m := historyModel(t, "first")
	m.inputModel.SetText("a draft I care about")

	m = press(t, m, tea.KeyUp)

	if m.inputModel.Text != "a draft I care about" {
		t.Fatalf("prompt = %q, want the draft preserved", m.inputModel.Text)
	}

	// Esc dismisses without cost.
	m = press(t, m, tea.KeyEsc)
	if m.searchPopup != nil {
		t.Fatal("Esc did not dismiss the history window")
	}
	if m.inputModel.Text != "a draft I care about" {
		t.Fatalf("prompt after Esc = %q, want the draft preserved", m.inputModel.Text)
	}
}

// Enter on a selection puts it in the prompt.
func TestHistoryWindowEnterRecallsEntry(t *testing.T) {
	m := historyModel(t, "first", "second", "third")
	m = press(t, m, tea.KeyUp)
	m = press(t, m, tea.KeyDown) // move off the newest, onto "second"
	m = press(t, m, tea.KeyEnter)

	if m.searchPopup != nil {
		t.Fatal("Enter did not close the history window")
	}
	if m.inputModel.Text != "second" {
		t.Fatalf("prompt = %q, want the selected entry (second)", m.inputModel.Text)
	}
}

// fillChat gives the chat enough content to have somewhere to scroll.
// ChatModel.Scroll counts lines back from the bottom: ScrollUp raises it,
// ScrollDown lowers it toward 0 (pinned to the newest message).
func fillChat(m *model) {
	for i := range 200 {
		m.chatModel.Messages = append(m.chatModel.Messages,
			message{role: "assistant", content: "line " + string(rune('a'+i%26))})
	}
}

// With no history there is nothing to show, so Up keeps its scroll fallback and
// the keyboard can still scroll on a fresh session.
func TestUpWithoutHistoryScrollsChat(t *testing.T) {
	m := historyModel(t)
	fillChat(m)
	m.chatModel.Scroll = 10

	m = press(t, m, tea.KeyUp)

	if m.searchPopup != nil {
		t.Fatal("Up opened a history window with no history to show")
	}
	if m.chatModel.Scroll <= 10 {
		t.Fatalf("Scroll = %d, want scrolled further back than 10", m.chatModel.Scroll)
	}
}

// Down scrolls the chat; it must never open the history window.
func TestDownScrollsChatAndNeverOpensHistory(t *testing.T) {
	m := historyModel(t, "first", "second")
	fillChat(m)
	m.chatModel.Scroll = 40

	m = press(t, m, tea.KeyDown)

	if m.searchPopup != nil {
		t.Fatal("Down opened the history window")
	}
	if m.chatModel.Scroll >= 40 {
		t.Fatalf("Scroll = %d, want scrolled toward the newest message from 40", m.chatModel.Scroll)
	}
}

// A prompt that starts with "/" belongs to the slash-command popup; Up there
// must not hijack into history.
func TestUpOnSlashPromptDoesNotOpenHistory(t *testing.T) {
	m := historyModel(t, "first")
	m.inputModel.SetText("/mod")

	m = press(t, m, tea.KeyUp)

	if m.searchPopup != nil && m.searchPopup.mode == searchModeHistory {
		t.Fatal("Up on a slash prompt opened the history window instead of commands")
	}
}

// --- mouse wheel -----------------------------------------------------------

// The wheel must scroll the chat and must never reach the history window. With
// mouse reporting on, a wheel tick is a MouseWheelMsg, so it cannot arrive as an
// Up key — the two are structurally separate, not merely disambiguated.
func TestMouseWheelScrollsChatWithoutOpeningHistory(t *testing.T) {
	m := historyModel(t, "first", "second")
	fillChat(m)
	m.chatModel.Scroll = 20

	wheel := func(button tea.MouseButton) {
		t.Helper()
		next, _ := m.Update(tea.MouseWheelMsg(tea.Mouse{Button: button}))
		mm, ok := next.(*model)
		if !ok {
			t.Fatalf("Update returned %T, want *model", next)
		}
		m = mm
	}

	wheel(tea.MouseWheelUp)
	if m.searchPopup != nil {
		t.Fatal("wheel-up opened the history window; it must only scroll")
	}
	scrolledBack := m.chatModel.Scroll
	if scrolledBack <= 20 {
		t.Fatalf("Scroll = %d after wheel-up, want scrolled further back than 20", scrolledBack)
	}

	wheel(tea.MouseWheelDown)
	if m.searchPopup != nil {
		t.Fatal("wheel-down opened a popup; it must only scroll")
	}
	if m.chatModel.Scroll >= scrolledBack {
		t.Fatalf("Scroll = %d after wheel-down, want scrolled forward from %d",
			m.chatModel.Scroll, scrolledBack)
	}
}

// Mouse reporting has to stay on, or handleMouseWheel never receives anything
// and the wheel silently scrolls the terminal's scrollback instead of the chat.
func TestViewEnablesMouseReporting(t *testing.T) {
	m := historyModel(t, "first")
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "assistant", content: "hello"})

	v := m.View()

	if v.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want MouseModeCellMotion so the wheel reaches the app", v.MouseMode)
	}
	if v.AltScreen {
		t.Error("AltScreen = true; the UI is meant to stay on the normal screen")
	}
}
