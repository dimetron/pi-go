package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// --- View() across many model states ---------------------------------------

func TestView_AcrossStates(t *testing.T) {
	t.Run("loading", func(t *testing.T) {
		m := newTestModelFull(t)
		m.loading = true
		m.loadingItems = map[string]bool{"lsp": true, "mcp": false}
		m.loadingTotal = 2
		if m.View().Content == "" {
			t.Fatal("loading View empty")
		}
	})
	t.Run("running", func(t *testing.T) {
		m := newTestModelFull(t)
		m.running = true
		m.matrix.feed("data", m.mainWidth())
		if m.View().Content == "" {
			t.Fatal("running View empty")
		}
	})
	t.Run("plan-mode", func(t *testing.T) {
		m := newTestModelFull(t)
		m.mode = "plan"
		if m.View().Content == "" {
			t.Fatal("plan View empty")
		}
	})
	t.Run("quitting", func(t *testing.T) {
		m := newTestModelFull(t)
		m.quitting = true
		_ = m.View()
	})
	t.Run("narrow-width", func(t *testing.T) {
		m := newTestModelFull(t)
		m.width = 40
		m.height = 20
		if m.View().Content == "" {
			t.Fatal("narrow View empty")
		}
	})
	t.Run("with-thinking", func(t *testing.T) {
		m := newTestModelFull(t)
		m.chatModel.Thinking = "let me think"
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "thinking", content: "let me think"})
		if m.View().Content == "" {
			t.Fatal("thinking View empty")
		}
	})
}

// --- Update() with a battery of messages -----------------------------------

func TestUpdate_MessageBattery(t *testing.T) {
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 120, Height: 50},
		tea.WindowSizeMsg{Width: 60, Height: 20},
		agentTextMsg{text: "hello"},
		agentThinkingMsg{text: "hmm"},
		agentToolCallMsg{name: "bash", args: map[string]any{"command": "ls"}},
		agentToolResultMsg{name: "bash", content: `{"stdout":"x"}`},
		agentSubEventMsg{agentID: "a-1", kind: "spawn"},
		agentDoneMsg{err: nil},
		matrixTickMsg{},
		resetCtrlCCountMsg{},
		loadingTickMsg{},
	}
	for _, msg := range msgs {
		m := newTestModelFull(t)
		m.cfg.WorkDir = t.TempDir()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Update panicked on %T: %v", msg, r)
				}
			}()
			_, _ = m.Update(msg)
		}()
	}
}

func TestUpdate_KeyBattery(t *testing.T) {
	keys := []tea.Key{
		{Code: tea.KeyEnter},
		{Code: tea.KeyUp},
		{Code: tea.KeyDown},
		{Code: tea.KeyLeft},
		{Code: tea.KeyRight},
		{Code: tea.KeyEsc},
		{Code: tea.KeyTab},
		{Code: tea.KeyBackspace},
		{Code: 'a'},
		{Code: 'a', Mod: tea.ModCtrl},
		{Code: 'e', Mod: tea.ModCtrl},
	}
	for _, k := range keys {
		m := newTestModelFull(t)
		m.cfg.WorkDir = t.TempDir()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Update panicked on key %v: %v", k, r)
				}
			}()
			_, _ = m.Update(tea.KeyPressMsg(k))
		}()
	}
}

func TestUpdate_CtrlCFlow(t *testing.T) {
	m := newTestModelFull(t)
	m.cfg.WorkDir = t.TempDir()
	// First Ctrl+C while not running: arms the quit warning.
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if m.ctrlCCount == 0 {
		t.Fatal("expected ctrlCCount to increment on first Ctrl+C")
	}
	// resetCtrlCCountMsg clears it.
	_, _ = m.Update(resetCtrlCCountMsg{})
	if m.ctrlCCount != 0 {
		t.Fatalf("expected ctrlCCount reset, got %d", m.ctrlCCount)
	}
}
