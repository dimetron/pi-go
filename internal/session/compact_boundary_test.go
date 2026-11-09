package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
)

// FileService.Compact cut at len(events)-KeepRecent without asking
// advanceToCleanBoundary to check call/result pairing, so a KeepRecent that
// lands between a tool call and its result compacted the call away and kept the
// result. AutoCompact has always adjusted its cut (autocompact.go:115);
// /compact did not.
//
// The assertion is the one a provider makes: the surviving tail must not start
// with a result whose call is gone, and the compacted side must not end with a
// call whose result is all that survived.
func TestCompactKeepsCallAndResultTogether(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sid := created.Session.ID()
	cur := created.Session

	big := strings.Repeat("word ", 400)
	appendEv := func(ev *session.Event) {
		t.Helper()
		if err := svc.AppendEvent(ctx, cur, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		got, err := svc.Get(ctx, &session.GetRequest{
			AppName: "test-app", UserID: "test-user", SessionID: sid,
		})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		cur = got.Session
	}

	// [text, call(A), result(A), text x6] = 9 events. KeepRecent = 7 puts the
	// cut at 9-7 = 2, which is result(A): a boundary-blind cut compacts call(A)
	// away and keeps its result. The boundary-aware cut moves to 3 instead, so
	// call(A) and result(A) are compacted together.
	appendEv(textEvent("user", "user", fmt.Sprintf("intro %s", big)))
	appendEv(toolCallEvent("A", "read", map[string]any{"file_path": "/a.go"}))
	appendEv(toolResultForCall("A", "read", big))
	for i := 0; i < 6; i++ {
		appendEv(textEvent("user", "user", fmt.Sprintf("later %d %s", i, big)))
	}

	summarizer := func(events []*session.Event) (string, error) {
		return fmt.Sprintf("summary of %d", len(events)), nil
	}
	if err := svc.Compact(sid, "test-app", "test-user", summarizer, CompactConfig{
		MaxTokens:  1, // force compaction regardless of estimate
		KeepRecent: 7, // cut at index 2: exactly on result(A)
	}); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	after, err := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sid,
	})
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	events := make([]*session.Event, 0, after.Session.Events().Len())
	for i := 0; i < after.Session.Events().Len(); i++ {
		events = append(events, after.Session.Events().At(i))
	}
	if len(events) < 2 {
		t.Fatalf("only %d events after compaction; expected a summary plus a tail", len(events))
	}

	// events[0] is the summary the service prepends; the caller's tail starts at 1.
	const tail = 1
	if id := firstPartID(events[tail], functionResponseID); id != "" &&
		!rangeHasPartID(events, tail, len(events), functionCallID, id) {
		t.Errorf("the surviving tail starts with a result for call %q whose call was "+
			"compacted away; a provider that validates pairing rejects this request", id)
	}
	if id := firstPartID(events[tail-1], functionCallID); id != "" &&
		!rangeHasPartID(events, tail, len(events), functionResponseID, id) {
		t.Errorf("the compacted side ends with a call for %q whose result was kept on "+
			"the tail; the call and its answer were separated", id)
	}
}

// When the boundary adjustment would move the cut past the end of the events,
// there is nothing left to summarize. Compacting anyway would either panic on an
// empty slice or summarize the whole transcript into a single event, so Compact
// must return without touching the history.
//
// This is the branch that keeps advanceToCleanBoundary from turning a
// "keep everything" KeepRecent into a no-op that silently deleted the session.
func TestCompactLeavesHistoryAloneWhenTheBoundaryEatsTheTail(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app", UserID: "test-user",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sid := created.Session.ID()
	cur := created.Session

	big := strings.Repeat("word ", 400)
	appendEv := func(ev *session.Event) {
		t.Helper()
		if err := svc.AppendEvent(ctx, cur, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		got, err := svc.Get(ctx, &session.GetRequest{
			AppName: "test-app", UserID: "test-user", SessionID: sid,
		})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		cur = got.Session
	}

	// Three events with KeepRecent = 2 puts the cut at 1, which is the result of
	// call(A). The boundary moves it to 2, which is a text event — fine. To force
	// the boundary past the end, the last event must be an orphanable position:
	// [text, call(A), result(A)] with KeepRecent = 1 cuts at 2 (result(A)), and the
	// boundary moves it to 3, i.e. len(events).
	appendEv(textEvent("user", "user", fmt.Sprintf("intro %s", big)))
	appendEv(toolCallEvent("A", "read", map[string]any{"file_path": "/a.go"}))
	appendEv(toolResultForCall("A", "read", big))

	cfg := DefaultCompactConfig()
	cfg.KeepRecent = 1
	cfg.MaxTokens = 1 // force compaction to be considered

	if err := svc.Compact(sid, "test-app", "test-user",
		func([]*session.Event) (string, error) { return "summary", nil }, cfg); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	got, err := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sid,
	})
	if err != nil {
		t.Fatalf("Get after Compact: %v", err)
	}
	if n := got.Session.Events().Len(); n != 3 {
		t.Errorf("events = %d after a Compact that could only orphan a call, want the 3 "+
			"originals untouched", n)
	}
	for i := 0; i < got.Session.Events().Len(); i++ {
		if got.Session.Events().At(i).ID == "compaction-summary" {
			t.Error("a summary event was written even though the boundary left nothing to compact")
		}
	}
}

// A cut can be pushed all the way past the end. Parallel tool calls are emitted
// as call(A), call(B), then their responses, so every candidate split orphans a
// pair and advanceToCleanBoundary walks to len(events) — the case
// TestAdvanceToCleanBoundary_RunsOutOfEvents pins for the helper.
//
// Compact must then do nothing. Without the guard it would summarize the whole
// transcript into one event and keep an empty tail, losing every event for no
// reclaimable gain. The summarizer must not even be called.
func TestCompactDoesNothingWhenBoundaryEatsTheTail(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sid := created.Session.ID()
	cur := created.Session

	big := strings.Repeat("word ", 400)
	appendEv := func(ev *session.Event) {
		t.Helper()
		if err := svc.AppendEvent(ctx, cur, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		got, err := svc.Get(ctx, &session.GetRequest{
			AppName: "test-app", UserID: "test-user", SessionID: sid,
		})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		cur = got.Session
	}

	// [intro, call(A), call(B), resp(A), resp(B)]: the calls precede their
	// results, so no cut between them keeps a pair together.
	appendEv(textEvent("user", "user", fmt.Sprintf("intro %s", big)))
	appendEv(toolCallEvent("A", "read", map[string]any{"file_path": "/a.go"}))
	appendEv(toolCallEvent("B", "read", map[string]any{"file_path": "/b.go"}))
	appendEv(respEvent("read", "A", map[string]any{"content": big}))
	appendEv(respEvent("read", "B", map[string]any{"content": big}))

	before, err := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sid,
	})
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	wantLen := before.Session.Events().Len()

	summarized := false
	if err := svc.Compact(sid, "test-app", "test-user",
		func(events []*session.Event) (string, error) {
			summarized = true
			return "should not be called", nil
		},
		CompactConfig{MaxTokens: 1, KeepRecent: 1},
	); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if summarized {
		t.Error("the summarizer ran for a cut with no surviving tail; the whole " +
			"transcript would be replaced by one summary and every event lost")
	}
	after, err := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sid,
	})
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if got := after.Session.Events().Len(); got != wantLen {
		t.Errorf("events after Compact = %d, want %d unchanged", got, wantLen)
	}
}
