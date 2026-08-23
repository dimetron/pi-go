package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// unclaimedMsg is a message type no handler group in Update claims.
type unclaimedMsg struct{}

// TestUnclaimedMessageDoesNotArmAgentListener pins the rule that only a message
// actually consumed from m.agentCh may re-arm the listener.
//
// Update used to re-arm for *any* unhandled message while running, on the theory
// that this kept the listener alive. It could not: every type that travels on
// m.agentCh is claimed by updateAgentStream, and each of those handlers re-arms
// exactly once. Re-arming for a message that never came off the channel adds a
// reader without consuming one, so readers accumulated for the whole turn — 61
// of them were observed parked on one channel in a live session.
func TestUnclaimedMessageDoesNotArmAgentListener(t *testing.T) {
	ch := make(chan agentMsg, 4)
	m := &model{running: true, agentCh: ch}

	for i := 0; i < 5; i++ {
		if _, cmd := m.Update(unclaimedMsg{}); cmd != nil {
			t.Fatalf("unclaimed message #%d armed a listener (cmd %T), want nil", i, cmd)
		}
	}
}

// TestWindowSizeDoesNotArmAgentListener covers the same invariant for a resize,
// which is handled (so it never reaches Update's fallback) and used to batch a
// waitForAgent of its own. Every mid-turn resize therefore leaked one reader.
func TestWindowSizeDoesNotArmAgentListener(t *testing.T) {
	ch := make(chan agentMsg, 4)
	m := &model{running: true, agentCh: ch}
	m.chatModel.Messages = make([]message, 0)

	_, cmd, handled := m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !handled {
		t.Fatal("handleWindowSize did not claim the resize")
	}
	if cmd == nil {
		t.Fatal("handleWindowSize returned no command")
	}

	// The command must be the resize drain tick, not something reading agentCh.
	// Closing the channel would make any parked reader yield agentDoneMsg.
	close(ch)
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()

	select {
	case msg := <-got:
		if _, bad := msg.(agentDoneMsg); bad {
			t.Fatalf("resize command read from agentCh: got %T", msg)
		}
		if _, ok := msg.(resizeDrainDoneMsg); !ok {
			t.Fatalf("resize command returned %T, want resizeDrainDoneMsg", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resize command did not complete")
	}
}

// TestMatrixVisibleMatchesRender pins matrixState.visible to render's own notion
// of emptiness. messageViewportHeight reserves three rows on visible() while View
// draws them on render() != "", so the two disagreeing would mis-size the frame.
func TestMatrixVisibleMatchesRender(t *testing.T) {
	cases := []struct {
		name  string
		setup func(ms *matrixState)
	}{
		{"inactive", func(*matrixState) {}},
		{"active, empty grid", func(ms *matrixState) { ms.active = true }},
		{"active, fed", func(ms *matrixState) {
			ms.active = true
			ms.feed("hello world", 40)
		}},
		{"active, padded", func(ms *matrixState) {
			ms.active = true
			ms.feed("hi", 20)
			ms.fullWidth = ms.width + 10
		}},
		{"active, pad rounds to zero", func(ms *matrixState) {
			ms.active = true
			ms.width = 10
			ms.fullWidth = 11
		}},
		{"cleared after feed", func(ms *matrixState) {
			ms.active = true
			ms.feed("hello", 40)
			ms.clear()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ms matrixState
			tc.setup(&ms)
			want := ms.render() != ""
			if got := ms.visible(); got != want {
				t.Fatalf("visible()=%v but render()!=%q is %v", got, "", want)
			}
		})
	}
}

// TestClipMessagesToViewportPadding checks the single-allocation padding builds
// exactly the rows the loop it replaced did: availableHeight rows, the last one
// materialized as "\n " so the composed frame is not a line short.
func TestClipMessagesToViewportPadding(t *testing.T) {
	for _, tc := range []struct {
		name            string
		view            string
		availableHeight int
	}{
		{"pads a short conversation", "one\ntwo", 6},
		{"pads a single line", "only", 4},
		{"already full", "a\nb\nc", 3},
		{"taller than viewport", "a\nb\nc\nd\ne", 3},
		{"empty view", "", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			visible, _, _ := clipMessagesToViewport(tc.view, tc.availableHeight, 0)
			gotRows := strings.Count(visible, "\n") + 1
			wantRows := tc.availableHeight
			if gotRows < wantRows {
				t.Fatalf("got %d rows, want at least %d: %q", gotRows, wantRows, visible)
			}
			if strings.HasSuffix(visible, "\n") {
				t.Fatalf("padding left a bare trailing newline: %q", visible)
			}
		})
	}
}
