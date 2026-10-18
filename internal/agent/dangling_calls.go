package agent

import (
	"context"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// interruptedCallError is the result recorded for a tool call whose turn was
// cut off before the call returned. It says "may have run" rather than "did
// not run": a bash command interrupted mid-flight can have had effects.
const interruptedCallError = "tool call was interrupted before it returned a result; it may or may not have run"

// repairDanglingCalls closes every FunctionCall in the session that never got
// a FunctionResponse by appending a synthetic one.
//
// An interrupted turn — steer, Esc, a stuck-loop abort or a killed process —
// leaves a model event with calls and no responses. ADK then prunes those calls
// from the next request ("adk: dropping function calls with no matching
// function response"), so the model loses the record that a command may have
// run, and the TUI restores the call as a card that never finishes. Recording
// the outcome keeps the history whole for both.
//
// It runs before each turn rather than at the interrupt site because every
// interrupt path converges here, including the ones where no process survives
// to clean up.
func (a *Agent) repairDanglingCalls(ctx context.Context, sessionID string) error {
	resp, err := a.sessionService.Get(ctx, &session.GetRequest{
		AppName: AppName, UserID: DefaultUserID, SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	parts := danglingResponses(resp.Session.Events())
	if len(parts) == 0 {
		return nil
	}
	return a.sessionService.AppendEvent(ctx, resp.Session, &session.Event{
		Timestamp:   time.Now(),
		Author:      "pi",
		LLMResponse: model.LLMResponse{Content: &genai.Content{Role: genai.RoleUser, Parts: parts}},
	})
}

// danglingResponses returns one synthetic FunctionResponse part per call in
// events that has no response, in call order.
func danglingResponses(events session.Events) []*genai.Part {
	var calls []*genai.FunctionCall
	answered := map[string]bool{}
	for ev := range events.All() {
		if ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				calls = append(calls, p.FunctionCall)
			case p.FunctionResponse != nil:
				answered[p.FunctionResponse.ID] = true
			}
		}
	}
	var out []*genai.Part
	for _, c := range calls {
		if answered[c.ID] {
			continue
		}
		out = append(out, &genai.Part{FunctionResponse: &genai.FunctionResponse{
			ID:       c.ID,
			Name:     c.Name,
			Response: map[string]any{"error": interruptedCallError},
		}})
	}
	return out
}
