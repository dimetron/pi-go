package piagent

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/memory"
)

// sessionSummarizerLLM stands in for the summary model. It exists alongside
// fakeLLM rather than replacing it because the two carry different payloads:
// fakeLLM records the system instruction, which is where an agent instruction
// lands, while a summarizer sends its prompt as user content.
type sessionSummarizerLLM struct {
	reply string
	err   error

	mu     sync.Mutex
	calls  int
	prompt string
}

func (f *sessionSummarizerLLM) Name() string { return "session-summarizer" }

func (f *sessionSummarizerLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	f.mu.Lock()
	f.calls++
	if req != nil {
		var b strings.Builder
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				b.WriteString(p.Text)
			}
		}
		f.prompt = b.String()
	}
	reply, err := f.reply, f.err
	f.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		if err != nil {
			yield(nil, err)
			return
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(reply, genai.RoleModel)}, nil)
	}
}

func (f *sessionSummarizerLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *sessionSummarizerLLM) sentPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompt
}

var _ model.LLM = (*sessionSummarizerLLM)(nil)

// memoryDBPathFor returns the observation store path an agent built under the
// HOME set by isolate() will use.
func memoryDBPathFor(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
}

// openMemoryStore opens the store the agent has been writing to, so the test can
// assert on rows rather than on the agent's opinion of itself.
func openMemoryStore(t *testing.T, home string) *memory.SQLiteStore {
	t.Helper()
	db, err := memory.OpenDB(memoryDBPathFor(t, home))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return memory.NewSQLiteStore(db)
}

// sessionStatus reads a session's status straight from the database. The store
// type keeps its handle unexported and exposes no status reader, and opening the
// file through the exported memory.OpenDB is the shortest honest route to the
// column this test is about.
func sessionStatus(t *testing.T, home, sessionID string) string {
	t.Helper()
	db, err := memory.OpenDB(memoryDBPathFor(t, home))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	var status string
	if err := db.QueryRow("SELECT status FROM sessions WHERE session_id = ?", sessionID).Scan(&status); err != nil {
		t.Fatalf("reading session status: %v", err)
	}
	return status
}

// sessionSummaryReply is a well-formed summary body, as the summarizer model
// would return it.
const sessionSummaryReply = `{"request":"add session summaries","investigated":"the memory store","learned":"summaries were never written","completed":"wired them up","next_steps":"watch it run"}`

// The whole point: a session that runs and closes ends up with a summary row and
// a completed status in the memory database. Before this, both were permanently
// empty regardless of how many sessions ran.
func TestCloseWritesSessionSummary(t *testing.T) {
	home := isolate(t)

	// A second model stands in for the summarizer so the test can tell the
	// summarizer's reply apart from the conversation model's.
	conversation := &fakeLLM{name: "conversation", reply: "done"}
	summarizer := &sessionSummarizerLLM{reply: sessionSummaryReply}

	ag := newTestAgent(t, conversation,
		WithMemory(true),
		WithSessionSummary(summarizer),
	)

	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// The summarizer reads observations, so there must be one. Recording
	// normally happens through the after-tool callback; write it directly
	// because this test is about the summary, not about tool-call capture.
	store := openMemoryStore(t, home)
	if err := store.InsertObservation(ctx, &memory.Observation{
		SessionID:   sessionID,
		Project:     ag.WorkingDir(),
		Title:       "Added a per-session observation query",
		Type:        memory.TypeFeature,
		Text:        "Added SessionObservations to the memory store.",
		SourceFiles: []string{"internal/memory/store.go"},
		ToolName:    "edit",
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	if err := ag.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The summarizer must have been consulted, and with the observation text.
	if summarizer.callCount() == 0 {
		t.Error("the summarizer model was never called on Close")
	} else if p := summarizer.sentPrompt(); !strings.Contains(p, "per-session observation query") {
		t.Errorf("summarizer prompt did not carry the observation; got %q", p)
	}

	// Re-open after Close: the store the agent held is now shut.
	store = openMemoryStore(t, home)

	summaries, err := store.RecentSummaries(ctx, ag.WorkingDir(), 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(summaries))
	}
	got := summaries[0]
	if got.SessionID != sessionID {
		t.Errorf("summary SessionID = %q, want %q", got.SessionID, sessionID)
	}
	if got.Completed != "wired them up" {
		t.Errorf("summary Completed = %q, want it parsed from the model reply", got.Completed)
	}
	if got.NextSteps != "watch it run" {
		t.Errorf("summary NextSteps = %q", got.NextSteps)
	}

	// The session must also be marked completed. Leaving it 'active' is the
	// state that made the store's session table unreadable.
	if status := sessionStatus(t, home, sessionID); status != "completed" {
		t.Errorf("session status = %q, want completed", status)
	}
}

// With no summary model, sessions are still completed — otherwise a store
// accumulates 'active' rows that no reader can distinguish from live sessions.
func TestCloseCompletesSessionWithoutSummarizer(t *testing.T) {
	home := isolate(t)

	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
	)

	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := ag.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store := openMemoryStore(t, home)
	if status := sessionStatus(t, home, sessionID); status != "completed" {
		t.Errorf("session status = %q, want completed even with no summarizer", status)
	}

	summaries, err := store.RecentSummaries(ctx, ag.WorkingDir(), 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("got %d summaries with no summarizer configured, want 0", len(summaries))
	}
}

// A configured summarizer must not reach the store when memory is off. This is
// the "reading is on, writing is opt-in" rule the package documents.
func TestSessionSummaryInertWithoutMemory(t *testing.T) {
	home := isolate(t)

	summarizer := &sessionSummarizerLLM{reply: sessionSummaryReply}
	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithSessionSummary(summarizer), // note: no WithMemory(true)
	)

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if ag.memSummarizer != nil {
		t.Error("a summarizer was held despite memory being off")
	}
	if err := ag.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := summarizer.callCount(); n != 0 {
		t.Errorf("summarizer called %d time(s) with memory off, want 0", n)
	}

	// And nothing was written to the shared store.
	if _, err := os.Stat(memoryDBPathFor(t, home)); err == nil {
		store := openMemoryStore(t, home)
		if summaries, err := store.RecentSummaries(t.Context(), ag.WorkingDir(), 10); err == nil && len(summaries) > 0 {
			t.Errorf("a summary was written with memory off: %+v", summaries[0])
		}
		if _, err := store.SessionObservations(t.Context(), sessionID); err != nil {
			t.Errorf("SessionObservations: %v", err)
		}
	}
}

// SummarizeSession is the explicit form; it must work without waiting for Close.
func TestSummarizeSessionExplicit(t *testing.T) {
	home := isolate(t)

	summarizer := &sessionSummarizerLLM{reply: sessionSummaryReply}
	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(summarizer),
	)

	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	store := openMemoryStore(t, home)
	if err := store.InsertObservation(ctx, &memory.Observation{
		SessionID: sessionID,
		Project:   ag.WorkingDir(),
		Title:     "did some work",
		Type:      memory.TypeChange,
		Text:      "text",
		ToolName:  "edit",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	if err := ag.SummarizeSession(ctx, sessionID); err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}

	// Read through a fresh connection so the assertion does not depend on the
	// agent's own handle.
	store = openMemoryStore(t, home)
	summaries, err := store.RecentSummaries(ctx, ag.WorkingDir(), 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries after an explicit call, want 1", len(summaries))
	}

	// Calling again must not duplicate the row: the store upserts by session.
	if err := ag.SummarizeSession(ctx, sessionID); err != nil {
		t.Fatalf("SummarizeSession (second call): %v", err)
	}
	store = openMemoryStore(t, home)
	summaries, err = store.RecentSummaries(ctx, ag.WorkingDir(), 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Errorf("got %d summaries after two calls, want 1 (upsert)", len(summaries))
	}
}

// The error paths must be plain errors, not panics: an embedder treats
// "summarization unavailable" as an ordinary degraded state.
func TestSummarizeSessionErrors(t *testing.T) {
	t.Run("no summary model", func(t *testing.T) {
		isolate(t)
		ag := newTestAgent(t, &fakeLLM{name: "m", reply: "ok"}, WithMemory(true))
		sessionID, err := ag.NewSession(t.Context())
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		err = ag.SummarizeSession(t.Context(), sessionID)
		if err == nil {
			t.Fatal("expected an error when no summary model is configured")
		}
		if !strings.Contains(err.Error(), "WithSessionSummary") {
			t.Errorf("error should name the missing option, got %q", err)
		}
	})

	t.Run("memory off", func(t *testing.T) {
		isolate(t)
		ag := newTestAgent(t, &fakeLLM{name: "m", reply: "ok"},
			WithSessionSummary(&fakeLLM{name: "s", reply: sessionSummaryReply}))
		err := ag.SummarizeSession(t.Context(), "whatever")
		if err == nil {
			t.Fatal("expected an error when memory is off")
		}
		if !strings.Contains(err.Error(), "WithMemory") {
			t.Errorf("error should name the missing option, got %q", err)
		}
	})

	t.Run("session with no observations", func(t *testing.T) {
		isolate(t)
		ag := newTestAgent(t, &fakeLLM{name: "m", reply: "ok"},
			WithMemory(true),
			WithSessionSummary(&sessionSummarizerLLM{reply: sessionSummaryReply}),
		)
		sessionID, err := ag.NewSession(t.Context())
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if err := ag.SummarizeSession(t.Context(), sessionID); err == nil {
			t.Error("expected an error for a session with no observations")
		}
	})
}

// A provider failure must not stop Close from releasing resources, and must not
// turn into a Close error the caller has to handle.
func TestCloseSurvivesSummarizerFailure(t *testing.T) {
	home := isolate(t)

	failing := &sessionSummarizerLLM{err: os.ErrDeadlineExceeded}
	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(failing),
	)

	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	store := openMemoryStore(t, home)
	if err := store.InsertObservation(ctx, &memory.Observation{
		SessionID: sessionID, Project: ag.WorkingDir(), Title: "work",
		Type: memory.TypeChange, Text: "t", ToolName: "edit", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	// Close must still succeed: summarization is best-effort.
	if err := ag.Close(); err != nil {
		t.Fatalf("Close returned an error from a failed summary: %v", err)
	}
}

// WithSummarizer and WithSessionSummary are different seams and must not share
// state; setting one must not populate the other.
func TestSessionSummaryIsSeparateFromCompactionSummarizer(t *testing.T) {
	home := isolate(t)

	compaction := &fakeLLM{name: "compaction-summarizer", reply: "handoff"}
	summary := &sessionSummarizerLLM{reply: sessionSummaryReply}

	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSummarizer(compaction),
		WithSessionSummary(summary),
	)

	if ag.memSummarizer == nil {
		t.Fatal("session summary model was not wired")
	}
	if got := resolveSummarizer(options{summarizer: compaction}, &fakeLLM{name: "session"}).Name(); got != "compaction-summarizer" {
		t.Errorf("resolveSummarizer = %q, want compaction-summarizer", got)
	}

	// The session summarizer holds the session model, not the compaction one.
	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	store := openMemoryStore(t, home)
	if err := store.InsertObservation(ctx, &memory.Observation{
		SessionID: sessionID, Project: ag.WorkingDir(), Title: "work",
		Type: memory.TypeChange, Text: "t", ToolName: "edit", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}
	if err := ag.SummarizeSession(ctx, sessionID); err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if summary.callCount() == 0 {
		t.Error("the session summarizer was not the model consulted")
	}
	if n := compaction.callCount(); n != 0 {
		t.Errorf("the compaction summarizer was called %d time(s) for a session summary", n)
	}
}

// Agent documents that it is safe for concurrent use across sessions. NewSession
// appends to the session list and Close reads and clears it, so without a lock
// that claim is false: -race flags the append, and concurrent callers can lose
// session IDs, which would silently skip their summaries and completions.
func TestConcurrentNewSessionIsRaceFree(t *testing.T) {
	home := isolate(t)

	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(&sessionSummarizerLLM{reply: sessionSummaryReply}),
	)

	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := ag.NewSession(context.Background())
			if err != nil {
				t.Errorf("NewSession: %v", err)
				return
			}
			ids[i] = id
		}()
	}
	wg.Wait()

	// Every session must be recorded, or its summary and completion are skipped.
	ag.memSessionsMu.Lock()
	recorded := len(ag.memSessions)
	ag.memSessionsMu.Unlock()
	if recorded != n {
		t.Errorf("recorded %d sessions, want %d — concurrent appends lost entries", recorded, n)
	}

	// Each session must be distinct; a lost append can also mean a duplicated id.
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Error("a concurrent NewSession returned an empty ID")
			continue
		}
		if seen[id] {
			t.Errorf("session %q was issued twice", id)
		}
		seen[id] = true
	}

	if err := ag.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// And every one of them must have been completed by the flush.
	store := openMemoryStore(t, home)
	for id := range seen {
		if status := sessionStatus(t, home, id); status != "completed" {
			t.Errorf("session %s status = %q, want completed", id, status)
		}
	}
	_ = store
}

// A slow summary must not leave a later session 'active'.
//
// With one shared deadline across the loop, a first session that consumed the
// budget left every later session with an already-canceled context — so
// CompleteSession failed and the sessions stayed 'active', which is precisely the
// state this change exists to remove. Completion now gets a fresh context.
func TestCompletionSurvivesASummaryOverrun(t *testing.T) {
	home := isolate(t)

	// A summarizer that fails instantly stands in for one that overran its
	// deadline: either way the session must still be completed.
	failing := &sessionSummarizerLLM{err: os.ErrDeadlineExceeded}
	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(failing),
	)

	ctx := t.Context()
	var ids []string
	for i := 0; i < 3; i++ {
		id, err := ag.NewSession(ctx)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		ids = append(ids, id)
	}

	// Give each session something to summarize, so the summarizer is consulted.
	store := openMemoryStore(t, home)
	for _, id := range ids {
		if err := store.InsertObservation(ctx, &memory.Observation{
			SessionID: id, Project: ag.WorkingDir(), Title: "work",
			Type: memory.TypeChange, Text: "t", ToolName: "edit", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("InsertObservation: %v", err)
		}
	}

	if err := ag.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if n := failing.callCount(); n != len(ids) {
		t.Errorf("summarizer consulted %d time(s), want %d — one deadline must not starve later sessions", n, len(ids))
	}
	for _, id := range ids {
		if status := sessionStatus(t, home, id); status != "completed" {
			t.Errorf("session %s status = %q, want completed even though its summary failed", id, status)
		}
	}
}

// Closing an agent that never created a session must be a no-op, not a panic.
//
// summarizeSessions returns early when there is nothing to summarize. An
// embedder that constructs an agent and closes it without running a turn is the
// cheapest way to reach Close on the memory path, so the snapshot-and-clear has
// to be safe on an empty list.
func TestSummarizeSessionsNoSessions(t *testing.T) {
	isolate(t)

	summarizer := &sessionSummarizerLLM{reply: sessionSummaryReply}
	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(summarizer),
	)

	ag.summarizeSessions()

	ag.memSessionsMu.Lock()
	left := len(ag.memSessions)
	ag.memSessionsMu.Unlock()
	if left != 0 {
		t.Errorf("memSessions = %d, want 0", left)
	}
	if n := summarizer.callCount(); n != 0 {
		t.Errorf("summarizer called %d time(s) with no sessions, want 0", n)
	}

	if err := ag.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// summarizeSessions must not re-summarize the same sessions when run twice.
//
// The snapshot-and-clear guarantees this: a repeat run finds an empty list. The
// reason is deliberately NOT "Close is called twice" — Close nils its closer
// list, so a second Close never reaches the memory closer at all. The clear
// matters for a direct repeat invocation.
func TestSummarizeSessionsDoesNotRepeatWork(t *testing.T) {
	home := isolate(t)

	summarizer := &sessionSummarizerLLM{reply: sessionSummaryReply}
	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(summarizer),
	)

	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	store := openMemoryStore(t, home)
	if err := store.InsertObservation(ctx, &memory.Observation{
		SessionID: sessionID, Project: ag.WorkingDir(), Title: "work",
		Type: memory.TypeChange, Text: "t", ToolName: "edit", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	ag.summarizeSessions()
	first := summarizer.callCount()
	if first == 0 {
		t.Fatal("the first summarizeSessions call did not consult the summarizer")
	}

	ag.summarizeSessions()
	if second := summarizer.callCount(); second != first {
		t.Errorf("summarizer called %d time(s) after a repeat run, want still %d — sessions are re-summarized",
			second, first)
	}
}

// A store that fails must produce an error from SummarizeSession, not a silent
// success: the caller has to be able to tell that no summary was written.
func TestSummarizeSessionStoreErrors(t *testing.T) {
	isolate(t)

	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(&sessionSummarizerLLM{reply: sessionSummaryReply}),
	)
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ag.memStore = failingStore{Store: ag.memStore}

	if err := ag.SummarizeSession(t.Context(), sessionID); err == nil {
		t.Error("expected an error when SessionObservations fails")
	} else if !strings.Contains(err.Error(), "reading session observations") {
		t.Errorf("error does not name the failing step: %v", err)
	}
}

// A failed summary write must surface rather than be swallowed.
func TestSummarizeSessionUpsertFailure(t *testing.T) {
	isolate(t)

	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(&sessionSummarizerLLM{reply: sessionSummaryReply}),
	)
	ctx := t.Context()
	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	real := ag.memStore
	if err := real.InsertObservation(ctx, &memory.Observation{
		SessionID: sessionID, Project: ag.WorkingDir(), Title: "work",
		Type: memory.TypeChange, Text: "t", ToolName: "edit", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	ag.memStore = upsertFailStore{Store: real}
	if err := ag.SummarizeSession(ctx, sessionID); err == nil {
		t.Error("expected an error when UpsertSummary fails")
	} else if !strings.Contains(err.Error(), "storing session summary") {
		t.Errorf("error does not name the failing step: %v", err)
	}
}

// A failing store during Close must be logged and skipped, never returned:
// Close is on the exit path, and a summary problem cannot keep resources
// unreleased.
func TestSummarizeSessionsSurvivesStoreFailure(t *testing.T) {
	isolate(t)

	ag := newTestAgent(t, &fakeLLM{name: "conversation", reply: "done"},
		WithMemory(true),
		WithSessionSummary(&sessionSummarizerLLM{reply: sessionSummaryReply}),
	)
	if _, err := ag.NewSession(t.Context()); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ag.memStore = failingStore{Store: ag.memStore}
	if err := ag.Close(); err != nil {
		t.Fatalf("Close returned an error from a failing store: %v", err)
	}
}

// failingStore fails reads and completions, to drive the degraded branches.
type failingStore struct{ memory.Store }

func (failingStore) SessionObservations(context.Context, string) ([]*memory.Observation, error) {
	return nil, errors.New("store read failed")
}

func (failingStore) CompleteSession(context.Context, string) error {
	return errors.New("store complete failed")
}

// upsertFailStore reads successfully but cannot persist a summary.
type upsertFailStore struct{ memory.Store }

func (upsertFailStore) UpsertSummary(context.Context, *memory.SessionSummary) error {
	return errors.New("store write failed")
}
