package session

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// newAutoCompactSession creates a persisted session seeded with the given
// events, returning the service and the session ID.
func newAutoCompactSession(t *testing.T, events []*session.Event) (*FileService, string) {
	t.Helper()
	svc, err := NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	ctx := t.Context()
	resp, err := svc.Create(ctx, &session.CreateRequest{AppName: "pi-go", UserID: "local"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i, ev := range events {
		if ev.ID == "" {
			ev.ID = "ev-" + string(rune('a'+i))
		}
		if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	return svc, resp.Session.ID()
}

func TestAutoCompact_BelowThresholdIsNoOp(t *testing.T) {
	body := strings.Repeat("a", shedMinBytes*2)
	svc, id := newAutoCompactSession(t, []*session.Event{
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
	})

	cfg := DefaultAutoCompactConfig()
	// 10% of the window — well below the 60% shed threshold.
	out, err := svc.AutoCompact(id, "pi-go", "local", 20_000, 200_000, cfg, SimpleSummarizer)
	if err != nil {
		t.Fatalf("AutoCompact: %v", err)
	}
	if out.Action != CompactionNone {
		t.Errorf("Action = %v, want none below the shed threshold", out.Action)
	}
}

func TestAutoCompact_ShedStageDropsSupersededOnly(t *testing.T) {
	body := strings.Repeat("b", shedMinBytes*3)
	svc, id := newAutoCompactSession(t, []*session.Event{
		userEvent("do the thing"),
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
	})

	cfg := DefaultAutoCompactConfig()
	cfg.KeepRecentEvents = 1

	// 70% of the window: past shed, below summarize.
	out, err := svc.AutoCompact(id, "pi-go", "local", 140_000, 200_000, cfg, nil)
	if err != nil {
		t.Fatalf("AutoCompact: %v", err)
	}
	if out.Action != CompactionShed {
		t.Fatalf("Action = %v, want shed", out.Action)
	}
	if out.ResultsShed == 0 {
		t.Error("expected superseded results to be shed")
	}
	if out.TokensAfter >= out.TokensBefore {
		t.Errorf("shedding reclaimed nothing: %d → %d", out.TokensBefore, out.TokensAfter)
	}

	// The shed stage must not need an LLM: a nil summarizer got us here fine.
	// It must also preserve the user's message.
	sess, err := svc.loadSession(id, "pi-go", "local")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	var found bool
	for _, ev := range sess.events {
		for _, p := range ev.Content.Parts {
			if strings.Contains(p.Text, "do the thing") {
				found = true
			}
		}
	}
	if !found {
		t.Error("shedding must not drop user messages")
	}
}

func TestAutoCompact_SummarizeStageRebuilds(t *testing.T) {
	body := strings.Repeat("c", shedMinBytes*3)
	svc, id := newAutoCompactSession(t, []*session.Event{
		userEvent("first ask"),
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/b.go", body),
		userEvent("second ask"),
		toolResultEvent("read", "/c.go", body),
	})

	cfg := DefaultAutoCompactConfig()
	cfg.KeepRecentEvents = 1

	called := false
	summarizer := func(events []*session.Event) (string, error) {
		called = true
		return "handoff summary text", nil
	}

	// 95% of the window: past the summarize threshold.
	out, err := svc.AutoCompact(id, "pi-go", "local", 190_000, 200_000, cfg, summarizer)
	if err != nil {
		t.Fatalf("AutoCompact: %v", err)
	}
	if out.Action != CompactionSummarize {
		t.Fatalf("Action = %v, want summarize", out.Action)
	}
	if !called {
		t.Error("summarizer must be invoked for the summarize stage")
	}
	if out.TokensAfter >= out.TokensBefore {
		t.Errorf("summarization reclaimed nothing: %d → %d", out.TokensBefore, out.TokensAfter)
	}

	sess, err := svc.loadSession(id, "pi-go", "local")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	var joined string
	for _, ev := range sess.events {
		for _, p := range ev.Content.Parts {
			joined += p.Text
		}
	}
	for _, want := range []string{"first ask", "second ask", "handoff summary text"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rebuilt session missing %q", want)
		}
	}
}

func TestAutoCompact_SummarizeRequiresSummarizer(t *testing.T) {
	svc, id := newAutoCompactSession(t, []*session.Event{
		userEvent("a"), userEvent("b"), userEvent("c"),
	})
	cfg := DefaultAutoCompactConfig()
	cfg.KeepRecentEvents = 1

	if _, err := svc.AutoCompact(id, "pi-go", "local", 190_000, 200_000, cfg, nil); err == nil {
		t.Error("summarize stage must reject a nil summarizer rather than silently drop history")
	}
}

func TestAutoCompact_UnknownWindowNeverCompacts(t *testing.T) {
	body := strings.Repeat("d", shedMinBytes*2)
	svc, id := newAutoCompactSession(t, []*session.Event{
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
	})

	// A zero window means "unknown", not "zero budget": compacting on a guessed
	// budget would discard history for no reason.
	out, err := svc.AutoCompact(id, "pi-go", "local", 999_999, 0,
		DefaultAutoCompactConfig(), SimpleSummarizer)
	if err != nil {
		t.Fatalf("AutoCompact: %v", err)
	}
	if out.Action != CompactionNone {
		t.Errorf("Action = %v, want none when the window size is unknown", out.Action)
	}
}

func TestAutoCompact_ShedWithNothingSupersededReportsNone(t *testing.T) {
	body := strings.Repeat("e", shedMinBytes*2)
	svc, id := newAutoCompactSession(t, []*session.Event{
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/b.go", body),
		toolResultEvent("read", "/c.go", body),
	})

	cfg := DefaultAutoCompactConfig()
	cfg.KeepRecentEvents = 1

	out, err := svc.AutoCompact(id, "pi-go", "local", 140_000, 200_000, cfg, nil)
	if err != nil {
		t.Fatalf("AutoCompact: %v", err)
	}
	if out.Action != CompactionNone {
		t.Errorf("Action = %v, want none — distinct files are not supersessions", out.Action)
	}
}

func TestAdvanceToCleanBoundary_KeepsCallResponsePaired(t *testing.T) {
	body := strings.Repeat("k", shedMinBytes*2)
	call := toolCallEvent("A", "read", map[string]any{"file_path": "/a.go"})
	resp := toolResultForCall("A", "read", body)
	// [user, call(A), response(A), model_text] with splitIdx=2 would orphan
	// the call on the compacted side. The adjustment must push splitIdx to 3.
	events := []*session.Event{
		userEvent("ask"),
		call,
		resp,
		func() *session.Event {
			ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "pi"}
			ev.Content = genai.NewContentFromText("answer", genai.RoleModel)
			return ev
		}(),
	}
	got := advanceToCleanBoundary(events, 2)
	if got != 3 {
		t.Fatalf("advanceToCleanBoundary = %d, want 3 — call(A) at index 1 is paired with response(A) at index 2", got)
	}
}

func TestAdvanceToCleanBoundary_OrphanResponseShifts(t *testing.T) {
	body := strings.Repeat("m", shedMinBytes*2)
	// The call sits on the tail and its response would land on the compacted
	// side if we cut at index 2: that's the symmetric case the helper must
	// also fix.
	events := []*session.Event{
		userEvent("ask"),
		toolCallEvent("A", "read", map[string]any{"file_path": "/a.go"}),
		toolResultForCall("A", "read", body),
		func() *session.Event {
			ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "pi"}
			ev.Content = genai.NewContentFromText("ok", genai.RoleModel)
			return ev
		}(),
	}
	// Cut at index 3 leaves [user, call, response] compacted and [model_text]
	// on the tail — but if we wanted to cut at index 2 instead, [user, call]
	// would survive and the response would be on the tail-side (orphaned call).
	// For the orphan-response case, the cut at index 2 means events[2] is a
	// response whose call is at index 1 (compacted side). The helper should
	// shift to 3.
	got := advanceToCleanBoundary(events, 2)
	if got != 3 {
		t.Fatalf("advanceToCleanBoundary = %d, want 3 — response(A) at index 2 has its call at index 1", got)
	}
}

func TestAdvanceToCleanBoundary_NoOrphansUnchanged(t *testing.T) {
	// Plain text-only events: no call/response to break.
	events := []*session.Event{userEvent("a"), userEvent("b"), userEvent("c")}
	if got := advanceToCleanBoundary(events, 1); got != 1 {
		t.Errorf("splitIdx = %d, want 1 (no orphans to fix)", got)
	}
}
