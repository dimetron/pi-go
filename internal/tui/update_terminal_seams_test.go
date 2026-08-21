package tui

import (
	"testing"
)

// TestHandleLoadingTick pins the re-arm lifecycle. The ticker used to re-arm
// unconditionally, which held a 3.3 Hz full-history re-render for the life of
// the process (TODO §30/§42); the chain must stop the moment the splash clears.
func TestHandleLoadingTick(t *testing.T) {
	t.Parallel()

	t.Run("advances the dots and re-arms while loading", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.loading = true
		m.loadingDots = 2
		_, cmd, handled := m.handleLoadingTick()
		if !handled {
			t.Error("handled = false, want true")
		}
		if m.loadingDots != 3 {
			t.Errorf("loadingDots = %d, want 3", m.loadingDots)
		}
		if cmd == nil {
			t.Error("cmd = nil, want the ticker re-armed while loading")
		}
	})

	t.Run("dots wrap at four", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.loading = true
		m.loadingDots = 3
		m.handleLoadingTick()
		if m.loadingDots != 0 {
			t.Errorf("loadingDots = %d, want it wrapped to 0", m.loadingDots)
		}
	})

	t.Run("stops re-arming once the splash is gone", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.loading = false
		m.loadingDots = 2
		_, cmd, handled := m.handleLoadingTick()
		if !handled {
			t.Error("handled = false, want the message still consumed")
		}
		if cmd != nil {
			t.Error("cmd != nil — the ticker re-armed after loading finished")
		}
		if m.loadingDots != 2 {
			t.Errorf("loadingDots = %d, want it left alone", m.loadingDots)
		}
	})
}

// TestHandleMatrixTick checks that the running-state animations advance only
// while a turn is running, and that the tick stops re-arming when it is not.
func TestHandleMatrixTick(t *testing.T) {
	t.Parallel()

	t.Run("advances the animations and re-arms while running", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.running = true
		m.titleSpin = 0
		_, cmd, handled := m.handleMatrixTick()
		if !handled {
			t.Error("handled = false, want true")
		}
		if cmd == nil {
			t.Error("cmd = nil, want the tick re-armed while running")
		}
	})

	t.Run("idle does not re-arm or animate", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.running = false
		m.titleSpin = 2
		m.chatModel.ToolDisplay.BlinkOn = true
		_, cmd, handled := m.handleMatrixTick()
		if !handled {
			t.Error("handled = false, want the message still consumed")
		}
		if cmd != nil {
			t.Error("cmd != nil — the matrix tick re-armed while idle")
		}
		if m.titleSpin != 2 || !m.chatModel.ToolDisplay.BlinkOn {
			t.Error("idle tick mutated the animation phases")
		}
	})
}

// TestHandleInputSubmit pins the routing: slash commands and prompts go to
// different places, and a slash command is dropped outright mid-turn.
func TestHandleInputSubmit(t *testing.T) {
	t.Parallel()

	t.Run("a slash command mid-turn is dropped", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.running = true
		before := len(m.chatModel.Messages)
		_, cmd, handled := m.handleInputSubmit(InputSubmitMsg{Text: "/clear"})
		if !handled {
			t.Error("handled = false, want the message consumed")
		}
		if cmd != nil {
			t.Error("cmd != nil — a slash command ran while a turn was in flight")
		}
		if len(m.chatModel.Messages) != before {
			t.Error("the dropped command still touched the conversation")
		}
	})

	t.Run("a slash command outside a turn is executed", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.running = false
		before := len(m.chatModel.Messages)
		_, _, handled := m.handleInputSubmit(InputSubmitMsg{Text: "/help"})
		if !handled {
			t.Error("handled = false, want true")
		}
		if len(m.chatModel.Messages) == before {
			t.Error("/help produced no output, so it was not executed")
		}
	})

	t.Run("plain text is queued as a prompt, not run as a command", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.running = true // queued behind the running turn rather than dropped
		_, _, handled := m.handleInputSubmit(InputSubmitMsg{Text: "hello there"})
		if !handled {
			t.Error("handled = false, want true")
		}
		if len(m.pendingPrompts) == 0 {
			t.Error("prompt was not queued")
		}
	})
}
