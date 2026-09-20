package cli

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/memory"
)

// quietLogger writes nowhere, so these tests exercise the real logging calls
// without polluting test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// summaryRecordingStore records what the summary step asked of it, so a test asserts a
// summary was written rather than merely attempted.
type summaryRecordingStore struct {
	observations []*memory.Observation
	summaries    []*memory.SessionSummary

	observationsErr error
}

func (s *summaryRecordingStore) CreateSession(context.Context, *memory.Session) error { return nil }
func (s *summaryRecordingStore) CompleteSession(context.Context, string) error        { return nil }
func (s *summaryRecordingStore) InsertObservation(context.Context, *memory.Observation) error {
	return nil
}
func (s *summaryRecordingStore) GetObservations(context.Context, []int64) ([]*memory.Observation, error) {
	return nil, nil
}
func (s *summaryRecordingStore) RecentObservations(context.Context, string, int) ([]*memory.Observation, error) {
	return nil, nil
}
func (s *summaryRecordingStore) SessionObservations(context.Context, string) ([]*memory.Observation, error) {
	if s.observationsErr != nil {
		return nil, s.observationsErr
	}
	return s.observations, nil
}
func (s *summaryRecordingStore) UpsertSummary(_ context.Context, sum *memory.SessionSummary) error {
	s.summaries = append(s.summaries, sum)
	return nil
}
func (s *summaryRecordingStore) RecentSummaries(context.Context, string, int) ([]*memory.SessionSummary, error) {
	return nil, nil
}
func (s *summaryRecordingStore) Search(context.Context, memory.SearchQuery) (*memory.SearchResult, error) {
	return nil, nil
}
func (s *summaryRecordingStore) Timeline(context.Context, int64, int, int) ([]*memory.Observation, error) {
	return nil, nil
}
func (s *summaryRecordingStore) Close() error { return nil }

// summaryLLM is a model.LLM that returns a fixed reply and counts its calls, so
// a test can prove the per-session step makes exactly one call.
type summaryLLM struct {
	reply string
	err   error

	mu    sync.Mutex
	calls int
}

func (l *summaryLLM) Name() string { return "fake-summary-model" }

func (l *summaryLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	l.mu.Lock()
	l.calls++
	reply, err := l.reply, l.err
	l.mu.Unlock()

	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		if err != nil {
			yield(nil, err)
			return
		}
		yield(&llmmodel.LLMResponse{
			Content: genai.NewContentFromText(reply, genai.RoleModel),
		}, nil)
	}
}

func (l *summaryLLM) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

const validSummaryJSON = `{"request":"wire session summaries","investigated":"memory store","learned":"no per-session query existed","completed":"added one","next_steps":"wire the CLI"}`

func observationsFor(sessionID string) []*memory.Observation {
	return []*memory.Observation{
		{
			SessionID: sessionID,
			Project:   "/proj",
			Title:     "read (uncompressed)",
			Type:      memory.TypeChange,
			Text:      `{"tool_name":"read"}`,
			ToolName:  "read",
			CreatedAt: time.Now(),
		},
	}
}

// A clean drain must produce exactly one stored summary, for one model call.
func TestSummarizeSessionAfterDrain_WritesSummary(t *testing.T) {
	store := &summaryRecordingStore{observations: observationsFor("sess-1")}
	llm := &summaryLLM{reply: validSummaryJSON}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "sess-1",
		project:    "/proj",
		log:        quietLogger(),
	}, nil, true)

	if len(store.summaries) != 1 {
		t.Fatalf("expected exactly one stored summary, got %d", len(store.summaries))
	}
	sum := store.summaries[0]
	if sum.SessionID != "sess-1" || sum.Project != "/proj" {
		t.Errorf("summary filed under the wrong key: session=%q project=%q", sum.SessionID, sum.Project)
	}
	if sum.Learned != "no per-session query existed" {
		t.Errorf("summary content did not survive the round trip: %+v", sum)
	}
	if got := llm.callCount(); got != 1 {
		t.Errorf("expected exactly one model call for the whole session, got %d", got)
	}
}

// A drain timeout means the worker may still be storing, so a summary taken now
// would describe a prefix of the session and read as complete. It must be
// skipped, and no model call may happen.
func TestSummarizeSessionAfterDrain_SkipsOnDrainTimeout(t *testing.T) {
	store := &summaryRecordingStore{observations: observationsFor("sess-1")}
	llm := &summaryLLM{reply: validSummaryJSON}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "sess-1",
		project:    "/proj",
		log:        quietLogger(),
	}, context.DeadlineExceeded, true)

	if len(store.summaries) != 0 {
		t.Errorf("expected no summary after a drain timeout, got %d", len(store.summaries))
	}
	if got := llm.callCount(); got != 0 {
		t.Errorf("expected no model call after a drain timeout, got %d", got)
	}
}

// Without a model there is nothing to summarize with. The step must be a no-op
// rather than an error, because it runs on the shutdown path.
func TestSummarizeSessionAfterDrain_NoModelIsANoop(t *testing.T) {
	store := &summaryRecordingStore{observations: observationsFor("sess-1")}
	llm := &summaryLLM{reply: validSummaryJSON}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "sess-1",
		project:    "/proj",
		log:        quietLogger(),
	}, nil, false)

	if got := llm.callCount(); got != 0 {
		t.Errorf("expected no model call without a model, got %d", got)
	}
	if len(store.summaries) != 0 {
		t.Errorf("expected no summary without a model, got %d", len(store.summaries))
	}
}

// A session that recorded nothing has nothing to summarize: no model call, and
// no summary invented for it.
func TestSummarizeSessionAfterDrain_NoObservationsMakesNoCall(t *testing.T) {
	store := &summaryRecordingStore{}
	llm := &summaryLLM{reply: validSummaryJSON}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "sess-empty",
		project:    "/proj",
		log:        quietLogger(),
	}, nil, true)

	if got := llm.callCount(); got != 0 {
		t.Errorf("expected no model call for an empty session, got %d", got)
	}
	if len(store.summaries) != 0 {
		t.Errorf("expected no summary for an empty session, got %d", len(store.summaries))
	}
}

// A store read failure must not be followed by a model call over nothing.
func TestSummarizeSessionAfterDrain_StoreReadErrorSkipsModel(t *testing.T) {
	store := &summaryRecordingStore{observationsErr: errors.New("db is gone")}
	llm := &summaryLLM{reply: validSummaryJSON}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "sess-1",
		project:    "/proj",
		log:        quietLogger(),
	}, nil, true)

	if got := llm.callCount(); got != 0 {
		t.Errorf("expected no model call when the store read fails, got %d", got)
	}
	if len(store.summaries) != 0 {
		t.Errorf("expected no stored summary when the read fails, got %d", len(store.summaries))
	}
}

// A malformed model reply must not be stored: a summary that reads as a summary
// but says nothing is worse than an absent one.
func TestSummarizeSessionAfterDrain_MalformedReplyStoresNothing(t *testing.T) {
	store := &summaryRecordingStore{observations: observationsFor("sess-1")}
	llm := &summaryLLM{reply: "not json at all"}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "sess-1",
		project:    "/proj",
		log:        quietLogger(),
	}, nil, true)

	if len(store.summaries) != 0 {
		t.Errorf("expected no stored summary from an unparseable reply, got %d", len(store.summaries))
	}
}

// An empty session ID means the session was never created, so there is nothing
// to file a summary under.
func TestSummarizeSessionAfterDrain_EmptySessionIDIsANoop(t *testing.T) {
	store := &summaryRecordingStore{observations: observationsFor("sess-1")}
	llm := &summaryLLM{reply: validSummaryJSON}

	summarizeSessionAfterDrain(summarizeParams{
		store:      store,
		summarizer: memory.NewSessionSummarizer(store, llm),
		sessionID:  "",
		project:    "/proj",
		log:        quietLogger(),
	}, nil, true)

	if got := llm.callCount(); got != 0 || len(store.summaries) != 0 {
		t.Errorf("expected no work without a session ID: calls=%d summaries=%d",
			got, len(store.summaries))
	}
}

// A model failure must leave nothing stored and must not hang the exit path.
func TestSummarizeSessionAfterDrain_ModelErrorStoresNothing(t *testing.T) {
	store := &summaryRecordingStore{observations: observationsFor("sess-1")}
	llm := &summaryLLM{err: errors.New("provider exploded")}

	done := make(chan struct{})
	go func() {
		defer close(done)
		summarizeSessionAfterDrain(summarizeParams{
			store:      store,
			summarizer: memory.NewSessionSummarizer(store, llm),
			sessionID:  "sess-1",
			project:    "/proj",
			log:        quietLogger(),
		}, nil, true)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("summarizeSessionAfterDrain did not return")
	}

	if len(store.summaries) != 0 {
		t.Errorf("expected no summary when the model call fails, got %d", len(store.summaries))
	}
}

// derefString is how the closer reaches a session ID created after the memory
// subsystem is wired.
func TestDerefString(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Errorf("derefString(nil) = %q, want empty", got)
	}
	s := "sess-1"
	if got := derefString(&s); got != "sess-1" {
		t.Errorf("derefString(&s) = %q, want %q", got, "sess-1")
	}
}

// The drain budget must exceed the 5s it used to be, and the summary must have
// its own budget: a summary starts only after the drain has finished, so
// sharing one deadline would starve it.
func TestMemoryTimeoutsAreSane(t *testing.T) {
	if memoryDrainTimeout <= 5*time.Second {
		t.Errorf("memoryDrainTimeout = %v; the old 5s budget abandoned the session tail", memoryDrainTimeout)
	}
	if sessionSummaryTimeout <= 0 {
		t.Errorf("sessionSummaryTimeout = %v; must be a positive bound", sessionSummaryTimeout)
	}
}

// The summary prompt must name the session's observations, or the model would
// be asked to summarize nothing and would invent a session.
func TestSessionSummarizerPromptCarriesObservations(t *testing.T) {
	store := &summaryRecordingStore{observations: []*memory.Observation{{
		SessionID: "sess-1",
		Project:   "/proj",
		Title:     "edited store.go",
		Text:      "added SessionObservations",
		ToolName:  "edit",
		CreatedAt: time.Now(),
	}}}

	var sawPrompt string
	llm := &promptCapturingLLM{reply: validSummaryJSON, capture: &sawPrompt}

	if err := memory.NewSessionSummarizer(store, llm).SummarizeSession(context.Background(), "sess-1", "/proj"); err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if !strings.Contains(sawPrompt, "edited store.go") {
		t.Errorf("summary prompt does not mention the observation: %q", sawPrompt)
	}
}

// promptCapturingLLM records the prompt it was handed.
type promptCapturingLLM struct {
	reply   string
	capture *string
}

func (l *promptCapturingLLM) Name() string { return "capturing" }

func (l *promptCapturingLLM) GenerateContent(_ context.Context, req *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	if req != nil {
		var b strings.Builder
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				b.WriteString(p.Text)
			}
		}
		*l.capture = b.String()
	}
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			Content: genai.NewContentFromText(l.reply, genai.RoleModel),
		}, nil)
	}
}
