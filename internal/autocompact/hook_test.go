package autocompact

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"

	llmmodel "google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// TestBuildHookDeclines covers the shapes that cannot compact at all, where
// returning nil lets a caller install the result unconditionally.
func TestBuildHookDeclines(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	on := pisession.DefaultAutoCompactConfig()
	on.Enabled = true
	off := on
	off.Enabled = false

	tests := []struct {
		desc     string
		deps     Deps
		wantHook bool
	}{
		{desc: "nothing wired", deps: Deps{}},
		{desc: "no session service", deps: Deps{Tracker: NewMeter(), Cfg: on}},
		{desc: "no meter", deps: Deps{SessionSvc: svc, Cfg: on}},
		{desc: "disabled by config", deps: Deps{SessionSvc: svc, Tracker: NewMeter(), Cfg: off}},
		{desc: "fully wired", deps: Deps{SessionSvc: svc, Tracker: NewMeter(), Cfg: on}, wantHook: true},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			if got := BuildHook(tt.deps) != nil; got != tt.wantHook {
				t.Errorf("BuildHook() non-nil = %v, want %v", got, tt.wantHook)
			}
		})
	}
}

// TestHookReportsUnknownWindowOnce covers the state that used to be silent: a
// model whose context window pi-go cannot resolve makes compaction inert, and
// a session can then grow until the provider rejects it with nothing having
// said so. The notice fires once, not once per turn, or it would bury itself.
func TestHookReportsUnknownWindowOnce(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	cfg := pisession.DefaultAutoCompactConfig()
	cfg.Enabled = true

	meter := NewMeter() // window deliberately left unknown
	meter.Observe(500_000)

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Cfg:        cfg,
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil for a fully-wired Deps")
	}

	for range 5 {
		if err := hook(context.Background(), "session-1"); err != nil {
			t.Fatalf("hook: %v", err)
		}
	}

	if len(notices) != 1 {
		t.Fatalf("got %d notices %q, want exactly 1", len(notices), notices)
	}
	if !strings.Contains(notices[0], "context window unknown") {
		t.Errorf("notice = %q, want it to name the unknown context window", notices[0])
	}
}

// --- the hook body: Decide firing and AutoCompact outcome handling --------

// hookToolCallEvent is the FunctionCall event a tool turn leaves behind. The
// shed stage pairs responses with their calls through it, and the estimate
// counts its args, so seeding real calls keeps the fixtures honest.
func hookToolCallEvent(name string, args map[string]any) *adksession.Event {
	ev := &adksession.Event{Timestamp: time.Unix(0, 0), Author: "model"}
	ev.Content = &genai.Content{
		Role:  string(genai.RoleModel),
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: name, Args: args}}},
	}
	return ev
}

// seedCompactableSession persists a session whose three reads of /a.go carry
// 1200-byte payloads — well past shedMinBytes, so the shed stage has real
// work to do on superseded ones.
func seedCompactableSession(t *testing.T, svc *pisession.FileService) string {
	t.Helper()

	ctx := t.Context()
	resp, err := svc.Create(ctx, &adksession.CreateRequest{AppName: agent.AppName, UserID: agent.DefaultUserID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	body := strings.Repeat("x", 1200)
	for _, ev := range []*adksession.Event{
		hookUserEvent("please read /a.go"),
		hookToolCallEvent("read", map[string]any{"file_path": "/a.go"}),
		hookToolResultEvent("read", "/a.go", body),
		hookToolCallEvent("read", map[string]any{"file_path": "/a.go"}),
		hookToolResultEvent("read", "/a.go", body),
		hookToolCallEvent("read", map[string]any{"file_path": "/a.go"}),
		hookToolResultEvent("read", "/a.go", body),
	} {
		if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	return resp.Session.ID()
}

// TestHookSkipsEmptySessionID covers the guard that keeps a hook installed
// before the ADK session exists from touching the service with an empty ID.
func TestHookSkipsEmptySessionID(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    NewMeter(),
		Cfg:        mustEnabled(pisession.DefaultAutoCompactConfig()),
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil")
	}
	if err := hook(context.Background(), ""); err != nil {
		t.Fatalf("hook with an empty session ID: %v", err)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %q, want none — nothing should have run", notices)
	}
}

// TestHookNoneBelowThreshold covers Decide returning CompactionNone: the hook
// must return before the session service is touched at all.
func TestHookNoneBelowThreshold(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	id := seedCompactableSession(t, svc)

	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(20_000) // first request establishes the prefix
	meter.Observe(40_000) // body = 20_000 — 10% of the window, far below shed

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Cfg:        mustEnabled(pisession.DefaultAutoCompactConfig()),
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if err := hook(t.Context(), id); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %q, want none — the pass is a silent no-op here", notices)
	}
	// The no-op turn must not have rewritten the tracker either.
	if got := meter.BodyTokens(); got != 20_000 {
		t.Errorf("BodyTokens() = %d, want 20000 — no pushback without an outcome", got)
	}
}

// TestHookShedStageEndToEnd covers the shed happy path: Decide fires at the
// shed threshold, AutoCompact drops the two superseded results, the
// post-compaction count is pushed back into the tracker, no deduper reset
// runs (shed keeps pointers valid), and the outcome is reported.
func TestHookShedStageEndToEnd(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	id := seedCompactableSession(t, svc)

	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(60_000)  // first request establishes the cached prefix
	meter.Observe(200_000) // body = 140_000 — 70% of the window, past shed

	// Body must sit past the 60% shed threshold: re-read through the tracker
	// the hook will use, so a fixture drift fails here rather than silently
	// no-op'ing the whole test.
	if got := meter.BodyTokens(); got != 140_000 {
		t.Fatalf("fixture BodyTokens() = %d, want 140000", got)
	}

	// The default tail of 10 covers the whole 7-event fixture; tighten it so
	// the shed pass may actually reach the superseded results.
	cfg := mustEnabled(pisession.DefaultAutoCompactConfig())
	cfg.KeepRecentEvents = 2

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Deduper:    tools.NewResultDeduper(),
		Cfg:        cfg,
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil")
	}

	if err := hook(t.Context(), id); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "Shed") {
		t.Fatalf("notices = %q, want exactly one Shed outcome", notices)
	}

	// The post-compaction count was pushed back: the tracker now reads the
	// reclaimed window, not the pre-compaction 140_000.
	if got := meter.BodyTokens(); got == 80_000 || got >= 140_000 {
		t.Errorf("BodyTokens() = %d, want the compactor's smaller count", got)
	}
}

// TestHookReportsCompactionError covers the never-abort rule: when the
// compaction pass itself fails, the error is reported and the turn proceeds.
func TestHookReportsCompactionError(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(60_000)
	meter.Observe(200_000) // body = 140_000 — 70% of the window, past shed

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc, // wired, but no session exists under the ID below
		Tracker:    meter,
		Cfg:        mustEnabled(pisession.DefaultAutoCompactConfig()),
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil")
	}

	// The hook must swallow the error: compaction failing is a degraded
	// state, never a reason to refuse the user's request.
	if err := hook(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("hook returned error %v, want nil (the turn proceeds)", err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "Auto-compaction failed") {
		t.Fatalf("notices = %q, want exactly one naming the failure", notices)
	}

	// A second turn tries again — the failure is per-turn, not sticky.
	if err := hook(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("second hook: %v", err)
	}
	if len(notices) != 2 {
		t.Errorf("got %d notices, want 2 — the error path is not once-per-session", len(notices))
	}
}

// TestHookSummarizeStageEndToEnd covers the summarize path and everything it
// alone touches: SimpleSummarizer (no LLM wired), the pushback of the rebuilt
// window's count, and the Deduper.Reset a summarizing rebuild requires.
func TestHookSummarizeStageEndToEnd(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	id := seedCompactableSession(t, svc)

	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(60_000)
	meter.Observe(200_000) // body = 140_000 — 70% of the window, past shed

	// Drive past the 90% summarize threshold with a small config instead of a
	// giant fixture: SummarizePercent 50 puts 65% past it, and a tight
	// KeepRecentEvents lets the split actually drop events.
	cfg := mustEnabled(pisession.DefaultAutoCompactConfig())
	cfg.KeepRecentEvents = 2
	// The seeded body is 70% of the window; lowering the threshold to 60 makes
	// that past the summarize stage without a giant fixture. Shed runs first
	// only while below SummarizePercent, so keep the two at 60/65.
	cfg.SummarizePercent = 65
	cfg.ShedPercent = 60

	deduper := tools.NewResultDeduper()
	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Deduper:    deduper,
		Cfg:        cfg,
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil")
	}

	if err := hook(t.Context(), id); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "Summarized") {
		t.Fatalf("notices = %q, want exactly one Summarized outcome", notices)
	}

	// The pushback reflects the rebuilt window, and the reset cleared the
	// prefix baseline, so the tracker reads exactly the new count.
	if got := meter.BodyTokens(); got >= 130_000 {
		t.Errorf("BodyTokens() = %d, want the rebuilt (smaller) count", got)
	}

	// A reset deduper stays usable — Reset must not leave it poisoned.
	calls, bytes := deduper.Stats()
	if calls != 0 || bytes != 0 {
		t.Errorf("deduper Stats() = (%d, %d), want (0, 0) after a fresh reset", calls, bytes)
	}

	// The next turn reads a now-tiny body: Decide returns None and nothing
	// further is reported.
	if err := hook(t.Context(), id); err != nil {
		t.Fatalf("second hook: %v", err)
	}
	if len(notices) != 1 {
		t.Errorf("got %d notices after the no-op turn, want still 1: %q", len(notices), notices)
	}
}

// TestHookWiresLLMSummarizer covers the SummarizerLLM branch: a non-nil LLM
// must write the handoff summary instead of the placeholder — observed here
// by the stub's answer landing in the rebuilt session.
func TestHookWiresLLMSummarizer(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	id := seedCompactableSession(t, svc)

	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(60_000)
	meter.Observe(190_000)

	cfg := mustEnabled(pisession.DefaultAutoCompactConfig())
	cfg.KeepRecentEvents = 2
	// The seeded body is 70% of the window; lowering the threshold to 60 makes
	// that past the summarize stage without a giant fixture. Shed runs first
	// only while below SummarizePercent, so keep the two at 60/65.
	cfg.SummarizePercent = 65
	cfg.ShedPercent = 60

	hook := BuildHook(Deps{
		SessionSvc:    svc,
		Tracker:       meter,
		Cfg:           cfg,
		SummarizerLLM: &stubLLM{text: "handoff summary from the stub"},
		Notify:        func(string) {},
	})
	if hook == nil {
		t.Fatal("BuildHook returned nil")
	}

	if err := hook(t.Context(), id); err != nil {
		t.Fatalf("hook: %v", err)
	}

	// The summary the stub wrote must be in the rebuilt session — proof the
	// LLM-backed summarizer ran rather than the placeholder.
	get, err := svc.Get(t.Context(), &adksession.GetRequest{
		AppName:   agent.AppName,
		UserID:    agent.DefaultUserID,
		SessionID: id,
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var found bool
	for ev := range get.Session.Events().All() {
		if ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if strings.Contains(part.Text, "handoff summary from the stub") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the stub's summary never reached the rebuilt session")
	}
}

// TestHookOutcomeNoneLeavesTrackerAlone covers the CompactionNone outcome
// return: Decide fired but the pass found nothing to reclaim, so no pushback
// happens at all and the hook stays silent rather than reporting a rewrite
// that never happened.
func TestHookOutcomeNoneLeavesTrackerAlone(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	ctx := t.Context()
	resp, err := svc.Create(ctx, &adksession.CreateRequest{AppName: agent.AppName, UserID: agent.DefaultUserID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A single small tool result: nothing superseded it, so the shed pass —
	// which Decide does ask for at this body size — reclaims nothing.
	if err := svc.AppendEvent(ctx, resp.Session, hookUserEvent("hi")); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := svc.AppendEvent(ctx, resp.Session, hookToolResultEvent("read", "/a.go", strings.Repeat("z", 1200))); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(5_000)
	meter.Observe(125_000) // body = 120_000 — 60% of the window, exactly at shed

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Cfg:        mustEnabled(pisession.DefaultAutoCompactConfig()),
		Notify:     func(m string) { notices = append(notices, m) },
	})

	if err := hook(ctx, resp.Session.ID()); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %q, want none — a pass that shed nothing reports no rewrite", notices)
	}
	if got := meter.BodyTokens(); got != 120_000 {
		t.Errorf("BodyTokens() = %d, want 120000 — a no-op pass must not push back", got)
	}
}

// TestHookToleratesNilDeduper covers the shape an entry point gets when it
// wires no deduper: a summarizing pass must not nil-panic resetting it.
func TestHookToleratesNilDeduper(t *testing.T) {
	t.Parallel()

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	id := seedCompactableSession(t, svc)

	meter := NewMeter()
	meter.SetContextWindowSize(200_000)
	meter.Observe(60_000)
	meter.Observe(190_000)

	cfg := mustEnabled(pisession.DefaultAutoCompactConfig())
	cfg.KeepRecentEvents = 2
	// The seeded body is 70% of the window; lowering the threshold to 60 makes
	// that past the summarize stage without a giant fixture. Shed runs first
	// only while below SummarizePercent, so keep the two at 60/65.
	cfg.SummarizePercent = 65
	cfg.ShedPercent = 60

	var notices []string
	hook := BuildHook(Deps{
		SessionSvc: svc,
		Tracker:    meter,
		Cfg:        cfg, // Deps.Deduper deliberately left nil
		Notify:     func(m string) { notices = append(notices, m) },
	})
	if err := hook(t.Context(), id); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(notices) != 1 {
		t.Errorf("notices = %q, want exactly 1", notices)
	}
}

// hookUserEvent is the user's message that opens a seeded fixture.
func hookUserEvent(text string) *adksession.Event {
	ev := &adksession.Event{Timestamp: time.Unix(0, 0), Author: "user"}
	ev.Content = genai.NewContentFromText(text, genai.RoleUser)
	return ev
}

// hookToolResultEvent is a FunctionResponse part shaped the way tool turns do,
// with a payload big enough that shedding it is worth the stub it leaves.
func hookToolResultEvent(name, path, body string) *adksession.Event {
	ev := &adksession.Event{Timestamp: time.Unix(0, 0), Author: "tool"}
	ev.Content = &genai.Content{
		Role: string(genai.RoleUser),
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{
				Name:     name,
				Response: map[string]any{"file_path": path, "content": body},
			},
		}},
	}
	return ev
}

// stubLLM is an llmmodel.LLM that yields exactly one text part and nothing
// else — enough for LLMSummarizer's render without any network or provider.
type stubLLM struct {
	text string
}

func (s *stubLLM) Name() string { return "stub" }

func (s *stubLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			Content: &genai.Content{Parts: []*genai.Part{{Text: s.text}}},
		}, nil)
	}
}

func mustEnabled(c pisession.AutoCompactConfig) pisession.AutoCompactConfig {
	c.Enabled = true
	return c
}
