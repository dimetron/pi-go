package session

import (
	"fmt"
	"path/filepath"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// AutoCompactOutcome reports what an auto-compaction pass did, so callers can
// tell the user and reset any state that assumed the old window.
type AutoCompactOutcome struct {
	Action        CompactionAction
	TokensBefore  int
	TokensAfter   int
	ResultsShed   int
	EventsDropped int
}

// Reclaimed returns the estimated tokens freed.
func (o AutoCompactOutcome) Reclaimed() int {
	if o.TokensBefore <= o.TokensAfter {
		return 0
	}
	return o.TokensBefore - o.TokensAfter
}

// String renders a one-line summary for the TUI and session log.
func (o AutoCompactOutcome) String() string {
	switch o.Action {
	case CompactionShed:
		return fmt.Sprintf("Shed %d superseded tool result(s) — context %s → %s tokens.",
			o.ResultsShed, formatCount(o.TokensBefore), formatCount(o.TokensAfter))
	case CompactionSummarize:
		return fmt.Sprintf("Summarized %d event(s) — context %s → %s tokens.",
			o.EventsDropped, formatCount(o.TokensBefore), formatCount(o.TokensAfter))
	default:
		return "No compaction needed."
	}
}

func formatCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// AutoCompact runs the two-stage compaction for a session.
//
// bodyTokens is the context accumulated after the stable cached prefix and
// windowSize is the model's full context window; the caller reads both from the
// token tracker. Passing a zero windowSize disables auto-compaction rather than
// guessing, because compacting on a guessed budget can discard a session's
// history for no reason.
//
// The summarizer is only invoked for the summarize stage, so the cheap shed
// path never costs an LLM call.
func (s *FileService) AutoCompact(
	sessionID, appName, userID string,
	bodyTokens, windowSize int64,
	cfg AutoCompactConfig,
	summarizer Summarizer,
) (AutoCompactOutcome, error) {
	action := cfg.Decide(bodyTokens, windowSize)
	if action == CompactionNone {
		return AutoCompactOutcome{Action: CompactionNone}, nil
	}
	n := cfg.normalize()

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSession(sessionID, appName, userID)
	if err != nil {
		return AutoCompactOutcome{}, fmt.Errorf("loading session for auto-compaction: %w", err)
	}

	outcome := AutoCompactOutcome{
		Action:       action,
		TokensBefore: estimateEventTokens(sess.events),
	}

	switch action {
	case CompactionShed:
		events, shed := ShedSupersededToolResults(sess.events, n.KeepRecentEvents)
		if shed.ResultsShed == 0 {
			// Nothing superseded yet. Report honestly rather than rewriting the
			// events file for no change.
			outcome.Action = CompactionNone
			outcome.TokensAfter = outcome.TokensBefore
			return outcome, nil
		}
		sess.events = events
		outcome.ResultsShed = shed.ResultsShed

	case CompactionSummarize:
		if summarizer == nil {
			return AutoCompactOutcome{}, fmt.Errorf("summarizer is required for the summarize stage")
		}
		// Everything except the tail is handed to the summarizer; the rebuild
		// then decides what survives verbatim.
		splitIdx := len(sess.events) - n.KeepRecentEvents
		if splitIdx <= 0 {
			outcome.Action = CompactionNone
			outcome.TokensAfter = outcome.TokensBefore
			return outcome, nil
		}
		// Adjust the split so a FunctionCall and its FunctionResponse never
		// end up on different sides: providers that validate call/response
		// pairing (Anthropic, OpenAI Responses) reject or hallucinate when a
		// call is on the compacted side and its response lives on the
		// surviving tail, or vice versa.
		splitIdx = advanceToCleanBoundary(sess.events, splitIdx)
		toCompact := sess.events[:splitIdx]
		tail := sess.events[splitIdx:]

		summary, sErr := summarizer(toCompact)
		if sErr != nil {
			return AutoCompactOutcome{}, fmt.Errorf("summarizing events: %w", sErr)
		}

		rebuilt := BuildSummarizedEvents(toCompact, summary, n.KeepUserMessageTokens)
		rebuilt = append(rebuilt, tail...)
		outcome.EventsDropped = len(sess.events) - len(rebuilt)
		sess.events = rebuilt

	case CompactionNone:
		return outcome, nil
	}

	outcome.TokensAfter = estimateEventTokens(sess.events)

	sessionDir := filepath.Join(s.baseDir, sessionID)
	if err := rewriteEvents(sessionDir, sess.events); err != nil {
		return AutoCompactOutcome{}, fmt.Errorf("rewriting events after auto-compaction: %w", err)
	}
	return outcome, nil
}

// advanceToCleanBoundary returns the smallest index i ≥ splitIdx such that the
// cut [0:i) / [i:len) does not separate a FunctionCall from its
// FunctionResponse. If the conversation has no calls, splitIdx is returned
// unchanged. The shift is one-sided — only the right edge moves — so the
// compacted side grows by at most a handful of events, never shrinks past the
// caller's budget.
func advanceToCleanBoundary(events []*session.Event, splitIdx int) int {
	for splitIdx < len(events) {
		if callOrphansResponseOnTail(events, splitIdx-1, splitIdx, len(events)) {
			splitIdx++
			continue
		}
		if responseOrphansCallOnCompacted(events, splitIdx) {
			splitIdx++
			continue
		}
		return splitIdx
	}
	return len(events)
}

// callOrphansResponseOnTail reports whether events[lastCompactedIdx] is a
// FunctionCall whose matching FunctionResponse is on the surviving-tail side
// of the cut, which would strand a tool call with no result for the model to
// reason about.
func callOrphansResponseOnTail(events []*session.Event, lastCompactedIdx, splitIdx, n int) bool {
	if lastCompactedIdx < 0 || lastCompactedIdx >= n {
		return false
	}
	callID := firstPartID(events[lastCompactedIdx], functionCallID)
	if callID == "" {
		return false
	}
	return rangeHasPartID(events, splitIdx, n, functionResponseID, callID)
}

// responseOrphansCallOnCompacted reports whether events[splitIdx] contains a
// FunctionResponse whose FunctionCall lives on the compacted side, which
// would let the model see a tool result with no record of the call that
// produced it.
func responseOrphansCallOnCompacted(events []*session.Event, splitIdx int) bool {
	if splitIdx >= len(events) {
		return false
	}
	respID := firstPartID(events[splitIdx], functionResponseID)
	if respID == "" {
		return false
	}
	return rangeHasPartID(events, 0, splitIdx, functionCallID, respID)
}

// partIDFunc reads the tool-pairing ID out of a single part, returning "" when
// the part is nil, is not the kind being looked for, or carries no ID. The two
// orphan checks above are mirror images of each other — same scan, opposite
// direction and opposite part kind — so the kind is a parameter rather than
// duplicated control flow.
type partIDFunc func(part *genai.Part) string

// functionCallID reads the ID of a FunctionCall part.
func functionCallID(part *genai.Part) string {
	if part == nil || part.FunctionCall == nil {
		return ""
	}
	return part.FunctionCall.ID
}

// functionResponseID reads the ID of a FunctionResponse part.
func functionResponseID(part *genai.Part) string {
	if part == nil || part.FunctionResponse == nil {
		return ""
	}
	return part.FunctionResponse.ID
}

// firstPartID returns the first non-empty ID that id reads out of ev's parts,
// or "" when ev holds no part of that kind with an ID.
func firstPartID(ev *session.Event, id partIDFunc) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	for _, part := range ev.Content.Parts {
		if got := id(part); got != "" {
			return got
		}
	}
	return ""
}

// rangeHasPartID reports whether any event in events[lo:hi) carries a part
// whose ID, as read by id, equals want. want must be non-empty: id returns ""
// for parts of the wrong kind, so an empty want would match them.
func rangeHasPartID(events []*session.Event, lo, hi int, id partIDFunc, want string) bool {
	for j := lo; j < hi; j++ {
		ev := events[j]
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if id(part) == want {
				return true
			}
		}
	}
	return false
}
