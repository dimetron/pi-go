package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The context gauge's [x] is a mouse control with no m.running guard: unlike
// /clear, which returns early while a turn runs, the click reaches
// clearConversation directly. Clearing history mid-turn empties the events the
// running turn is still writing to, and because clearConversation sets
// m.running = false without canceling anything, the turn keeps running with
// no key left that can stop it.
//
// This drives the real path — handleMouseClick -> hitContextClear -> the fix —
// rather than calling clearConversation, because the guard being missing is the
// bug: a test that calls the helper would pass either way.
func TestContextGaugeClearStopsTheRunningTurnFirst(t *testing.T) {
	m := historyModel(t, "")
	m.width, m.height = 100, 24
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "a turn in flight"})

	// A turn is running, with a cancel handle and a live event channel.
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.running = true
	m.agentCancel = cancel
	ch := make(chan agentMsg, 8)
	m.agentCh = ch

	// Populate the frame so hitContextClear has a layout row to hit.
	m.View()
	if m.frameRows <= 0 {
		t.Fatalf("frameRows = %d, want a rendered frame", m.frameRows)
	}
	lay, ok := layoutContextRule(m.contextRuleFor(m.width))
	if !ok || !lay.ShowClear {
		t.Fatalf("context rule has no clear button at width %d; the test cannot reach it", m.width)
	}

	canceled := false
	m.agentCancel = func() { canceled = true; cancel() }

	x := lay.ClearStart
	y := m.frameRows - 1
	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{
		X: x, Y: y, Button: tea.MouseLeft,
	}))
	got, ok := next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}

	if !canceled {
		t.Error("the click cleared history without canceling the running turn; " +
			"the turn would keep writing to the events that were just emptied")
	}
	if got.running {
		t.Error("m.running is still set after the click; the turn must be stopped")
	}
	if got.agentCancel != nil {
		t.Error("agentCancel is still set after the click; a second cancel would leak it")
	}
	if len(got.chatModel.Messages) != 0 {
		t.Errorf("transcript has %d messages after the clear, want 0", len(got.chatModel.Messages))
	}
}

// The idle path must still just clear: no turn to stop, nothing to cancel.
func TestContextGaugeClearWhileIdleStillClears(t *testing.T) {
	m := historyModel(t, "")
	m.width, m.height = 100, 24
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "old turn"})

	m.View()
	lay, ok := layoutContextRule(m.contextRuleFor(m.width))
	if !ok || !lay.ShowClear {
		t.Fatalf("context rule has no clear button at width %d", m.width)
	}

	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{
		X: lay.ClearStart, Y: m.frameRows - 1, Button: tea.MouseLeft,
	}))
	got := next.(*model)
	if len(got.chatModel.Messages) != 0 {
		t.Errorf("transcript has %d messages after the clear, want 0", len(got.chatModel.Messages))
	}
	if got.running {
		t.Error("m.running set by an idle clear")
	}
}
