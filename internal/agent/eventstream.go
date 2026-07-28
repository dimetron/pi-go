package agent

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/session"
)

// EventError extracts a provider-level failure from an event, or nil if the
// event is fine.
//
// Providers report LLM failures (HTTP 4xx/5xx, stream aborts, guardrail
// rejections) by yielding an event that carries ErrorCode/ErrorMessage with a
// *nil* Go error and no Content. ADK then ends the turn normally, so every
// consumer that only guards on `err != nil` and `ev.Content == nil` drops the
// failure on the floor and shows the user an empty response. Call this right
// after the nil-event check, before the Content guard, so the error reaches the
// surface instead of being swallowed.
func EventError(ev *session.Event) error {
	if ev == nil || ev.ErrorCode == "" {
		return nil
	}
	msg := strings.TrimSpace(ev.ErrorMessage)
	if msg == "" {
		return fmt.Errorf("%s", ev.ErrorCode)
	}
	// STREAM_ERROR is the generic "the request failed" code every provider
	// uses; the message already says what went wrong, so prefixing it with the
	// code only adds noise. Named codes (API_ERROR, DAILY_LIMIT_EXCEEDED,
	// server-sent codes) do carry information, so keep those.
	if ev.ErrorCode == "STREAM_ERROR" {
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %s", ev.ErrorCode, msg)
}

// modelRoles are the roles ADK uses for text the model itself produced. Text on
// any other role (a user turn, a tool result) belongs to a new turn.
var modelRoles = map[string]bool{"model": true, "thinking": true}

// StreamDedup suppresses the duplicate copy of assistant text that SSE
// streaming produces.
//
// Under StreamingModeSSE, ADK yields each chunk as a Partial event and then
// re-yields the whole turn once more as a non-partial aggregate. A consumer
// that emits every text part it sees therefore emits the reply twice — which
// is what `pi --mode print` and `--mode json` did until this existed.
//
// Usage, once per run:
//
//	var dedup agent.StreamDedup
//	for ev := range events {
//	    dedup.BeginEvent(ev)          // once per event, before its parts
//	    for _, part := range ev.Content.Parts {
//	        if part.Text != "" && dedup.SkipText(ev) {
//	            continue              // aggregate re-send; deltas already went out
//	        }
//	        ...
//	    }
//	}
//
// The zero value is ready to use. Not safe for concurrent use; one value
// tracks one event stream.
type StreamDedup struct{ streamed bool }

// BeginEvent starts a new turn's bookkeeping when ev is not model-authored.
// A user or tool-result event means the next model turn may arrive as a bare
// aggregate with no deltas in front of it, and that aggregate must pass
// through. Call once per event, before walking its parts.
func (d *StreamDedup) BeginEvent(ev *session.Event) {
	if ev == nil || ev.Content == nil {
		return
	}
	if !modelRoles[ev.Content.Role] {
		d.streamed = false
	}
}

// SkipText reports whether the non-empty text part currently being walked is
// the aggregate re-send of text already emitted as deltas. Call it only for
// parts with non-empty text: it is what records that deltas were seen, so
// calling it for empty parts would suppress the following aggregate even when
// nothing was emitted.
func (d *StreamDedup) SkipText(ev *session.Event) bool {
	if ev == nil {
		return false
	}
	if ev.Partial {
		d.streamed = true
		return false
	}
	return d.streamed
}
