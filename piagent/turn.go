package piagent

import (
	"context"
	"fmt"
	"iter"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/agent"
)

// TurnInfo describes a turn that has finished, and is what an [AfterTurnFunc]
// receives. It is a snapshot: the hook may keep it, but must not assume the
// agent is idle by the time it runs.
type TurnInfo struct {
	// SessionID is the session the turn belonged to.
	SessionID string

	// Message is the user message that opened the turn.
	Message string

	// Duration covers the whole turn, from the first iteration to the last
	// event — including time the caller spent inside its own loop body.
	Duration time.Duration

	// Events counts the events the turn produced. Streaming turns report far
	// more than non-streaming ones for the same reply, because SSE delivers
	// text as deltas and then repeats it as an aggregate.
	Events int

	// ToolCalls counts the tool invocations the model requested.
	ToolCalls int

	// Err is the failure that ended the turn, or nil. It covers both an
	// iteration error and a provider failure reported on an ordinary event,
	// which are different channels for the same thing.
	Err error

	// Abandoned reports that the caller stopped iterating before the turn
	// finished — a break in the range loop, or a canceled context. The
	// counts describe what was consumed, not what the model produced.
	Abandoned bool
}

// BeforeTurnFunc runs before a turn is dispatched. Returning an error aborts
// the turn: no request reaches the model, and the error surfaces through the
// event sequence like any other turn failure.
//
// This is the seam for admission control — budget checks, rate limiting,
// moderation of the outgoing message, injecting session state.
type BeforeTurnFunc func(ctx context.Context, sessionID, message string) error

// AfterTurnFunc runs once a turn has finished, whether it succeeded, failed or
// was abandoned. It cannot change the outcome; it is for metrics, audit logs
// and persistence.
type AfterTurnFunc func(ctx context.Context, info TurnInfo)

// runner is the shape of [agent.Agent].Run and RunStreaming, so one wrapper
// serves both.
type runner func(ctx context.Context, sessionID, message string) iter.Seq2[*session.Event, error]

// observeTurn wraps a turn with the before- and after-turn hooks.
//
// Both run inside the returned sequence rather than at call time, because a
// sequence is lazy: a caller that never ranges over it has not taken a turn,
// and firing admission control for a turn that never happens would be a lie in
// whatever the hook writes down.
func (a *Agent) observeTurn(ctx context.Context, sessionID, message string, run runner) iter.Seq2[*session.Event, error] {
	if len(a.beforeTurn) == 0 && len(a.afterTurn) == 0 {
		return run(ctx, sessionID, message)
	}

	return func(yield func(*session.Event, error) bool) {
		for _, fn := range a.beforeTurn {
			if err := fn(ctx, sessionID, message); err != nil {
				// Matches how internal/agent reports a pre-turn failure: as
				// the sole element of the sequence, so a caller's existing
				// error branch handles it without a second code path.
				yield(nil, fmt.Errorf("piagent: before-turn hook: %w", err))
				return
			}
		}

		info := TurnInfo{SessionID: sessionID, Message: message, Abandoned: true}
		started := time.Now()
		// Deferred so the hooks still run when the caller breaks out of its
		// range loop, which is the case an explicit call at the end misses.
		defer func() {
			info.Duration = time.Since(started)
			for _, fn := range a.afterTurn {
				fn(ctx, info)
			}
		}()

		for ev, err := range run(ctx, sessionID, message) {
			if err != nil {
				info.Err = err
			}
			if ev != nil {
				info.Events++
				if evErr := agent.EventError(ev); evErr != nil && info.Err == nil {
					info.Err = evErr
				}
				if ev.Content != nil {
					for _, part := range ev.Content.Parts {
						if part.FunctionCall != nil {
							info.ToolCalls++
						}
					}
				}
			}
			if !yield(ev, err) {
				return // abandoned; the defer above still reports it
			}
		}
		info.Abandoned = false
	}
}
