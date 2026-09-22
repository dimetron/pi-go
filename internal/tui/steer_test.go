package tui

import (
	"context"

	"strings"
	"testing"
)

// Steering: a prompt submitted while a turn runs cancels that turn and waits for
// the canceled turn's own agentDoneMsg to start it.
//
// The design rule these pin is that a steer must not create a second turn racing
// the one being unwound. Canceling without draining is what makes the canceled
// loop's own done the start signal, so no turn-identity plumbing is needed — but
// it also means every hazard below has to be closed explicitly.

func TestSteer_CancelsTurnAndQueuesText(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel

	_, cmd := m.steerPrompt("actually, do X instead", nil)
	if cmd != nil {
		t.Fatal("steer started the replacement immediately; it must wait for the old turn to unwind")
	}
	if ctx.Err() == nil {
		t.Fatal("steer did not cancel the running turn")
	}
	if !m.running {
		t.Fatal("steer cleared running; the UI would look idle while the old loop unwinds")
	}
	if got := len(m.pendingPrompts); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
}

// The canceled loop's done is what starts the replacement, and the replacement
// must survive it. A done that arrived while treating the new turn as current
// would stop it instead — which is what makes cancel-then-submit wrong without
// startNextPrompt in handleAgentDone.
func TestSteer_StaleDoneStartsReplacementAndLeavesItRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	_, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel

	// The steer itself; its command is nil because the replacement waits.
	_, _ = m.steerPrompt("new direction", nil)

	// The canceled loop unwinds: its channel closes and its done arrives.
	close(m.agentCh)
	m.agentCh = nil
	_, cmd := m.Update(agentDoneMsg{})
	if cmd == nil {
		t.Fatal("the canceled turn's done did not start the replacement")
	}
	if !m.running {
		t.Fatal("replacement turn is not running")
	}
	if got := len(m.pendingPrompts); got != 0 {
		t.Fatalf("pending = %d after start, want 0", got)
	}
	if got := m.chatModel.Messages[len(m.chatModel.Messages)-2].content; got != "new direction" {
		t.Fatalf("replacement prompt = %q, want %q", got, "new direction")
	}
}

// A canceled turn reports context.Canceled — that is the only thing
// cancellation can produce. A steer is not a failure, so it must not print an
// error or offer a Ctrl+R retry for the prompt the user just replaced.
func TestSteer_CancelReadsAsSteerNotFailure(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	_, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel
	m.lastPrompt = "do something long"

	// The steer itself; its command is nil because the replacement waits.
	_, _ = m.steerPrompt("actually, do X instead", nil)

	// The canceled loop's real report.
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	m.Update(agentDoneMsg{err: context.Canceled})

	if m.lastPromptFailed {
		t.Error("steering offered a Ctrl+R retry for the replaced prompt")
	}
	for _, msg := range m.chatModel.Messages {
		if msg.role == "assistant" && strings.Contains(msg.content, "context canceled") {
			t.Errorf("steering printed %q in the transcript", msg.content)
		}
	}
}

// An Esc cancel must not be silenced by the steer flag; a real cancellation is
// still a cancellation and keeps its error and retry offer.
func TestSteer_EscCancelStillReportsFailure(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	m.lastPrompt = "do something long"
	m.steering = false

	m.Update(agentDoneMsg{err: context.Canceled})

	if !m.lastPromptFailed {
		t.Error("an Esc cancel stopped offering the retry it used to offer")
	}
}

// A full queue cannot take the replacement, so a steer must refuse and leave the
// running turn alone rather than canceling it into nothing.
func TestSteer_FullQueueLeavesTurnRunning(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel
	m.pendingPrompts = make([]queuedPrompt, maxPendingPrompts)

	_, cmd := m.steerPrompt("won't fit", nil)
	if cmd != nil {
		t.Fatal("steer returned a command with a full queue")
	}
	if ctx.Err() != nil {
		t.Error("steer canceled the running turn with nowhere for the replacement to go")
	}
	if !m.running {
		t.Error("steer stopped the turn instead of refusing")
	}
	if m.flash != "Prompt queue full" {
		t.Errorf("flash = %q, want the queue-full notice", m.flash)
	}
}

// Esc during a turn must not strand a prompt the user already queued.
// cancelAgent drains the channel, and startNextPrompt is otherwise only reached
// from handleAgentDone, so cancelAgent has to start it itself.
func TestCancel_StartsQueuedPrompt(t *testing.T) {
	m := newTestModel(t)
	m.running = true
	m.agentCh = make(chan agentMsg, 4)

	if _, cmd := m.enqueuePrompt("queued while running", nil); cmd != nil {
		t.Fatal("queued prompt started while a turn was running")
	}
	m.cancelAgent()

	if len(m.pendingPrompts) != 0 {
		t.Error("pending prompt stranded after Esc: nothing will ever start it")
	}
	if !m.running {
		t.Error("queued prompt did not start after Esc")
	}
}
