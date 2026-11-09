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
