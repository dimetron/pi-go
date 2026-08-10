package tui

import (
	"context"
	"encoding/json"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/agent"
)

// restoreTranscript rebuilds the visible chat transcript from a resumed
// session's persisted events.
//
// Resuming used to start with an empty screen: the events were replayed to the
// model, so the conversation continued correctly, but the user saw the welcome
// splash and no sign of the history the model was answering from. This maps
// those same events back to the messages the live agent loop would have built.
//
// Partial events are never persisted (FileService.AppendEvent drops them), so
// each text part here is a whole chunk rather than a stream delta — consecutive
// assistant chunks are merged the way streaming would have merged them.
// Thinking is skipped: it is scratch work the live UI itself replaces with the
// answer as soon as one arrives.
func restoreTranscript(events []*session.Event) []message {
	var msgs []message
	for _, ev := range events {
		if ev == nil || ev.Content == nil || ev.Content.Role == "thinking" {
			continue
		}
		for _, part := range ev.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				fc := part.FunctionCall
				msgs = append(msgs, message{
					role:   "tool",
					tool:   fc.Name,
					toolIn: toolCallSummary(fc.Name, fc.Args),
					toolID: fc.ID,
				})
			case part.FunctionResponse != nil:
				fr := part.FunctionResponse
				respJSON, _ := json.Marshal(fr.Response)
				attachToolResult(msgs, fr.ID, fr.Name, toolResultSummary(string(respJSON)))
			case part.Text != "":
				msgs = appendText(msgs, ev.Content.Role, part.Text)
			}
		}
	}
	return msgs
}

// appendText adds one text part, merging into a trailing assistant message so a
// reply split across several persisted events reads as one answer. A tool
// message in between ends the run, which keeps rendered order matching event
// order — the same rule handleAgentText follows live.
func appendText(msgs []message, role, text string) []message {
	if role == "user" {
		return append(msgs, message{role: "user", content: text})
	}
	if n := len(msgs); n > 0 && msgs[n-1].role == "assistant" {
		msgs[n-1].content += text
		return msgs
	}
	return append(msgs, message{role: "assistant", content: text})
}

// attachToolResult fills in the output of the call this response answers,
// mirroring handleAgentToolResult — a replayed transcript must pair calls with
// results the same way the live one did, or a resumed session shows parallel
// same-tool calls holding each other's output. See matchToolResultCard for why
// the call ID and not the tool name is what pairs them.
func attachToolResult(msgs []message, id, name, content string) {
	if i := matchToolResultCard(msgs, id, name); i >= 0 {
		msgs[i].content = content
	}
}

// restoreSession loads a resumed session's transcript into the chat and seeds
// the context gauge from it. Both are best-effort: a session that cannot be
// read is worth continuing with an empty screen, not worth refusing to start.
func (m *model) restoreSession() {
	svc := m.cfg.SessionService
	if svc == nil || m.cfg.SessionID == "" {
		return
	}
	resp, err := svc.Get(context.Background(), &session.GetRequest{
		AppName:   agent.AppName,
		UserID:    agent.DefaultUserID,
		SessionID: m.cfg.SessionID,
	})
	if err != nil || resp == nil || resp.Session == nil {
		return
	}

	var events []*session.Event
	for ev := range resp.Session.Events().All() {
		events = append(events, ev)
	}
	if restored := restoreTranscript(events); len(restored) > 0 {
		m.chatModel.Messages = append(restored, m.chatModel.Messages...)
		m.chatModel.Scroll = 0
	}

	// Seed the gauge, or a resumed session reads "ctx: 0" until the first reply
	// lands — the emptiest possible reading of a window that is already full.
	// EstimateTokens is the same chars/4 heuristic /compact refreshes with.
	if tt := m.cfg.TokenTracker; tt != nil {
		if est, estErr := svc.EstimateTokens(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		); estErr == nil && est > 0 {
			tt.SetLastPromptTokens(int64(est))
		}
	}
}
