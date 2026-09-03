package server

import (
	"context"
	"fmt"
	"iter"
	"strconv"

	acp "github.com/coder/acp-go-sdk"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	piacp "github.com/dimetron/pi-go/internal/acp"
	"github.com/dimetron/pi-go/internal/acp/server/adapter"
)

// replayEvents re-emits a persisted transcript as the session updates
// session/load must send before it responds: user turns as user message
// chunks, model text as agent message chunks, reasoning as thought chunks,
// and each tool call as one completed tool_call carrying its input, followed
// by an update carrying its output. Partial events — streaming deltas — are
// skipped; the aggregate that follows them is what the store keeps.
func replayEvents(ctx context.Context, events iter.Seq[*adksession.Event], updater SessionUpdater) error {
	r := &replayer{updater: updater, lastCallByName: map[string]acp.ToolCallId{}}
	for ev := range events {
		if err := r.event(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

type replayer struct {
	updater SessionUpdater
	seq     int
	// lastCallByName resolves a function response to its call when the
	// provider left both ids empty: the most recent call of that name.
	lastCallByName map[string]acp.ToolCallId
}

func (r *replayer) event(ctx context.Context, ev *adksession.Event) error {
	if ev == nil || ev.Content == nil || ev.Partial {
		return nil
	}
	user := ev.Content.Role == "user"
	for _, part := range ev.Content.Parts {
		update, ok := r.update(part, user)
		if !ok {
			continue
		}
		if err := r.updater.Update(ctx, update); err != nil {
			return fmt.Errorf("replay: %w", err)
		}
	}
	return nil
}

// update classifies one part into the session update that replays it,
// reporting false for parts that carry nothing to show.
func (r *replayer) update(part *genai.Part, user bool) (acp.SessionUpdate, bool) {
	switch {
	case part == nil:
		return acp.SessionUpdate{}, false
	case part.FunctionCall != nil:
		return r.toolCall(part.FunctionCall), true
	case part.FunctionResponse != nil:
		return r.toolResult(part.FunctionResponse)
	case part.Text == "":
		return acp.SessionUpdate{}, false
	case part.Thought:
		return acp.UpdateAgentThoughtText(part.Text), true
	case user:
		return acp.UpdateUserMessageText(part.Text), true
	default:
		return acp.UpdateAgentMessageText(part.Text), true
	}
}

// toolCall replays a function call as a tool_call that is already complete:
// the transcript is history, and a card left in progress for a call whose
// result never made it to disk would spin forever.
func (r *replayer) toolCall(fc *genai.FunctionCall) acp.SessionUpdate {
	r.seq++
	id := acp.ToolCallId(fc.ID)
	if id == "" {
		id = acp.ToolCallId("replay_" + strconv.Itoa(r.seq))
	}
	r.lastCallByName[fc.Name] = id
	var rawInput any
	if len(fc.Args) > 0 {
		rawInput = fc.Args
	}
	return acp.StartToolCall(id, piacp.EnrichToolCallTitle(fc.Name, rawInput),
		acp.WithStartKind(adapter.ToolKind(fc.Name)),
		acp.WithStartStatus(acp.ToolCallStatusCompleted),
		acp.WithStartRawInput(rawInput),
	)
}

// toolResult attaches a function response to the call it answers. A response
// that cannot be paired with a call has no card to land on and is dropped.
func (r *replayer) toolResult(fr *genai.FunctionResponse) (acp.SessionUpdate, bool) {
	id := acp.ToolCallId(fr.ID)
	if id == "" {
		id = r.lastCallByName[fr.Name]
	}
	if id == "" {
		return acp.SessionUpdate{}, false
	}
	var rawOutput any
	if len(fr.Response) > 0 {
		rawOutput = fr.Response
	}
	return acp.UpdateToolCall(id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateRawOutput(rawOutput),
	), true
}
