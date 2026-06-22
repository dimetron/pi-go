package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// --- input history navigation ----------------------------------------------

func TestInputModel_HistoryNavigation(t *testing.T) {
	im := NewInputModel([]HistoryEntry{
		{Text: "first"}, {Text: "second"}, {Text: "third"},
	}, nil, nil, "")
	im.HistoryIdx = -1

	// Up from fresh state goes to most recent.
	im.historyUp()
	if im.Text != "third" {
		t.Fatalf("first up = %q, want third", im.Text)
	}
	im.historyUp()
	if im.Text != "second" {
		t.Fatalf("second up = %q, want second", im.Text)
	}
	im.historyUp()
	if im.Text != "first" {
		t.Fatalf("third up = %q, want first", im.Text)
	}
	// Up at the oldest entry stays put.
	im.historyUp()
	if im.Text != "first" {
		t.Fatalf("clamped up = %q, want first", im.Text)
	}
	// Down walks back toward newest, then clears past the end.
	im.historyDown()
	if im.Text != "second" {
		t.Fatalf("down = %q, want second", im.Text)
	}
	im.historyDown() // third
	im.historyDown() // past end -> cleared
	if im.Text != "" {
		t.Fatalf("down past end = %q, want empty", im.Text)
	}
	// Down with no active history index is a no-op.
	im.historyDown()
}

func TestInputModel_HistoryEmpty(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.HistoryIdx = -1
	im.historyUp() // must not panic on empty history
	if im.Text != "" {
		t.Fatalf("empty history up = %q, want empty", im.Text)
	}
	im.restoreHistoryEntry(5) // out of range -> no-op
}

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
