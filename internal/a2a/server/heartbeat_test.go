package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

// silentHandler models the shape that broke the kagent chat: a turn that
// produces no session updates at all while it works, the way the subagent tool
// does between its start and end frames. It blocks until release is closed.
func silentHandler(release <-chan struct{}) acpserver.PromptHandler {
	return func(ctx context.Context, _ acpserver.PromptTurn) (acpserver.PromptResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return acpserver.PromptResult{}, ctx.Err()
		}
		return acpserver.PromptResult{FinalText: "done"}, nil
	}
}

// TestExecuteHeartbeatsDuringSilentTurn is the regression test for the stall:
// a turn that says nothing for longer than the client's idle timeout must
// still put frames on the wire.
func TestExecuteHeartbeatsDuringSilentTurn(t *testing.T) {
	release := make(chan struct{})
	e := &piExecutor{
		handler:   silentHandler(release),
		logger:    discardLogger(),
		heartbeat: 10 * time.Millisecond,
	}

	var working atomic.Int32
	done := make(chan []a2a.Event, 1)
	go func() {
		var events []a2a.Event
		e.Execute(context.Background(), newExecCtx("hi"))(func(ev a2a.Event, _ error) bool {
			events = append(events, ev)
			if su, ok := ev.(*a2a.TaskStatusUpdateEvent); ok && su.Status.State == a2a.TaskStateWorking {
				// The first Working is the turn opening, not a heartbeat.
				if working.Add(1) >= 4 {
					close(release)
				}
			}
			return true
		})
		done <- events
	}()

	select {
	case events := <-done:
		got := states(events)
		if n := working.Load(); n < 4 {
			t.Fatalf("saw %d working updates, want the heartbeat to keep emitting them", n)
		}
		if len(got) == 0 || got[len(got)-1] != a2a.TaskStateCompleted {
			t.Errorf("states = %v, want the turn to still complete", got)
		}
		// Heartbeats must not put anything in the transcript.
		for _, ev := range events {
			su, ok := ev.(*a2a.TaskStatusUpdateEvent)
			if ok && su.Status.State == a2a.TaskStateWorking && su.Status.Message != nil {
				t.Errorf("heartbeat carried a message %+v, want none", su.Status.Message)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return; heartbeat never released the handler")
	}
}

// TestExecuteHeartbeatQuietWhenStreaming checks the Reset: a turn that streams
// steadily is already proving itself alive, so it should not also accumulate
// heartbeat frames.
func TestExecuteHeartbeatQuietWhenStreaming(t *testing.T) {
	e := &piExecutor{
		handler: streamingHandler,
		logger:  discardLogger(),
		// Far longer than the streaming handler takes, so any Working
		// update beyond the opening one means the ticker was not reset.
		heartbeat: time.Hour,
	}

	events, _ := drain(e.Execute(context.Background(), newExecCtx("hi")))

	var working int
	for _, s := range states(events) {
		if s == a2a.TaskStateWorking {
			working++
		}
	}
	if working != 1 {
		t.Errorf("got %d working updates, want only the one that opens the turn", working)
	}
}

// TestHeartbeatEvery covers the zero-value fallback, since every existing test
// and Serve itself construct a piExecutor without setting heartbeat.
func TestHeartbeatEvery(t *testing.T) {
	if got := (&piExecutor{}).heartbeatEvery(); got != heartbeatInterval {
		t.Errorf("heartbeatEvery() = %v, want the %v default", got, heartbeatInterval)
	}
	if got := (&piExecutor{heartbeat: time.Second}).heartbeatEvery(); got != time.Second {
		t.Errorf("heartbeatEvery() = %v, want the 1s override", got)
	}
}
