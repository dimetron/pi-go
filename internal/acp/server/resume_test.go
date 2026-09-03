package server

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	adkmodel "google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	piagent "github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
)

func textPart(text string) *genai.Part { return genai.NewPartFromText(text) }

// fakeStore is an in-memory SessionStore keyed by ACP session id.
type fakeStore struct {
	transcripts map[string][]string // agent replies to replay
	summaries   []SessionSummary
	listErr     error
	replayErr   error
	replayed    []string
}

func (f *fakeStore) Exists(_ context.Context, id string) bool {
	_, ok := f.transcripts[id]
	return ok
}

func (f *fakeStore) Replay(ctx context.Context, id string, u SessionUpdater) error {
	f.replayed = append(f.replayed, id)
	if f.replayErr != nil {
		return f.replayErr
	}
	for _, text := range f.transcripts[id] {
		if err := u.Update(ctx, acp.UpdateAgentMessageText(text)); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStore) List(context.Context) ([]SessionSummary, error) {
	return f.summaries, f.listErr
}

func TestAgentInitializeAdvertisesResumeAndList(t *testing.T) {
	t.Parallel()

	resp, err := (&Agent{}).Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if resp.AgentCapabilities.SessionCapabilities.Resume == nil {
		t.Error("Resume capability not advertised without a store")
	}
	if resp.AgentCapabilities.SessionCapabilities.List != nil {
		t.Error("List capability advertised without a store to answer it")
	}

	resp, err = (&Agent{Sessions: &fakeStore{}}).Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if resp.AgentCapabilities.SessionCapabilities.List == nil {
		t.Error("List capability not advertised with a store")
	}
}

func TestAgentResumeSessionWithoutStoreBindsAnyID(t *testing.T) {
	t.Parallel()
	a := &Agent{}

	if _, err := a.ResumeSession(context.Background(), acp.ResumeSessionRequest{Cwd: "/tmp"}); err == nil {
		t.Error("ResumeSession() with an empty id error = nil, want rejection")
	}

	captured := make(chan string, 1)
	a.Handler = func(_ context.Context, turn PromptTurn) (PromptResult, error) {
		captured <- turn.CWD
		return PromptResult{StopReason: acp.StopReasonEndTurn}, nil
	}
	const sid = "sess_resumed_fresh"
	if _, err := a.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: sid, Cwd: "/tmp/resumed"}); err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if _, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sid, Prompt: []acp.ContentBlock{acp.TextBlock("hi")},
	}); err != nil {
		t.Fatalf("Prompt() after resume error = %v", err)
	}
	select {
	case got := <-captured:
		if got != "/tmp/resumed" {
			t.Errorf("turn.CWD = %q, want the resumed cwd", got)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not run")
	}
}

func TestAgentResumeSessionWithStore(t *testing.T) {
	t.Parallel()
	store := &fakeStore{transcripts: map[string][]string{"sess_on_disk": {"earlier reply"}}}
	a := &Agent{Sessions: store}

	if _, err := a.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: "sess_unknown", Cwd: "/tmp"}); err == nil {
		t.Error("ResumeSession() of an id the store lacks error = nil, want not found")
	}

	if _, err := a.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: "sess_on_disk", Cwd: "/tmp"}); err != nil {
		t.Fatalf("ResumeSession() of a persisted id error = %v", err)
	}
	if len(store.replayed) != 0 {
		t.Errorf("ResumeSession() replayed %v; resume must not replay the transcript", store.replayed)
	}

	// An id that only lives in memory (created by session/new, not yet
	// prompted) resumes too: the store is consulted for unknown ids only.
	sess, err := a.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := a.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: sess.SessionId, Cwd: "/tmp/moved"}); err != nil {
		t.Errorf("ResumeSession() of an in-memory session error = %v", err)
	}
}

func TestAgentLoadSessionReplaysBeforeResponding(t *testing.T) {
	clientRW, agentRW := pipePair()
	defer clientRW.Close()

	store := &fakeStore{transcripts: map[string][]string{"sess_replayed": {"first reply", "second reply"}}}
	a := &Agent{Sessions: store}
	defer wireAgent(t, a, agentRW)()

	client := &capturingClient{}
	clientConn := acp.NewClientSideConnection(client, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	initResp, err := clientConn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber)})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if initResp.AgentCapabilities.SessionCapabilities.Resume == nil || initResp.AgentCapabilities.SessionCapabilities.List == nil {
		t.Fatalf("capabilities over the wire = %+v, want resume and list", initResp.AgentCapabilities.SessionCapabilities)
	}

	if _, err := clientConn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId: "sess_replayed", Cwd: "/tmp/rpc", McpServers: []acp.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	// The response must trail the replay, so by now every chunk has landed.
	got := client.messages.Load()
	if got == nil || strings.Join(*got, "|") != "first reply|second reply" {
		t.Fatalf("replayed messages before the load response = %v, want both replies in order", got)
	}

	if _, err := clientConn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId: "sess_never", Cwd: "/tmp/rpc", McpServers: []acp.McpServer{},
	}); err == nil {
		t.Error("LoadSession() of an unknown id error = nil, want not found")
	}

	if _, err := clientConn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: "sess_replayed", Cwd: "/tmp/rpc"}); err != nil {
		t.Errorf("ResumeSession() over the wire error = %v", err)
	}
}

func TestAgentLoadSessionSurfacesReplayFailure(t *testing.T) {
	clientRW, agentRW := pipePair()
	defer clientRW.Close()

	store := &fakeStore{
		transcripts: map[string][]string{"sess_broken": {"one"}},
		replayErr:   errors.New("events.jsonl truncated"),
	}
	a := &Agent{Sessions: store}
	defer wireAgent(t, a, agentRW)()
	go func() { _ = acp.NewClientSideConnection(&capturingClient{}, clientRW, clientRW) }()

	_, err := a.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId: "sess_broken", Cwd: "/tmp", McpServers: []acp.McpServer{},
	})
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("LoadSession() error = %v, want the replay failure surfaced", err)
	}
	if len(store.replayed) != 1 {
		t.Errorf("replay attempted %d times, want 1", len(store.replayed))
	}
}

func TestAgentListSessions(t *testing.T) {
	t.Parallel()

	if _, err := (&Agent{}).ListSessions(context.Background(), acp.ListSessionsRequest{}); !isMethodNotFound(err) {
		t.Errorf("ListSessions() without a store err = %v, want method-not-found", err)
	}

	updated := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{summaries: []SessionSummary{
		{ID: "sess_a", Cwd: "/work/a", Title: "alpha", UpdatedAt: updated},
		{ID: "sess_b", Cwd: "/work/b"},
	}}
	a := &Agent{Sessions: store}

	resp, err := a.ListSessions(context.Background(), acp.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("ListSessions() = %d sessions, want 2", len(resp.Sessions))
	}
	first := resp.Sessions[0]
	if first.SessionId != "sess_a" || first.Cwd != "/work/a" || first.Title == nil || *first.Title != "alpha" {
		t.Errorf("session[0] = %+v, want sess_a with title alpha", first)
	}
	if first.UpdatedAt == nil || *first.UpdatedAt != "2026-09-03T10:00:00Z" {
		t.Errorf("session[0].UpdatedAt = %v, want RFC3339 UTC", first.UpdatedAt)
	}
	if second := resp.Sessions[1]; second.Title != nil || second.UpdatedAt != nil {
		t.Errorf("session[1] = %+v, want no title or timestamp when unset", second)
	}

	cwd := "/work/b"
	resp, err = a.ListSessions(context.Background(), acp.ListSessionsRequest{Cwd: &cwd})
	if err != nil {
		t.Fatalf("ListSessions(cwd) error = %v", err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionId != "sess_b" {
		t.Errorf("ListSessions(cwd=/work/b) = %+v, want only sess_b", resp.Sessions)
	}

	store.listErr = errors.New("disk gone")
	if _, err := a.ListSessions(context.Background(), acp.ListSessionsRequest{}); err == nil {
		t.Error("ListSessions() error = nil, want the store failure")
	}
}

// stubLLM satisfies adkmodel.LLM without ever being called.
type stubLLM struct{}

func (stubLLM) Name() string { return "stub" }
func (stubLLM) GenerateContent(context.Context, *adkmodel.LLMRequest, bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(func(*adkmodel.LLMResponse, error) bool) {}
}

func TestOpenPiSessionPersistsUnderACPID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	inMemory, err := piagent.New(piagent.Config{Model: stubLLM{}})
	if err != nil {
		t.Fatalf("piagent.New: %v", err)
	}
	sid, resumed, err := openPiSession(ctx, RuntimeConfig{}, inMemory, "sess_acp")
	if err != nil {
		t.Fatalf("openPiSession() without a service error = %v", err)
	}
	if resumed || sid == "" || sid == "sess_acp" {
		t.Errorf("without a service got (%q, %v), want a fresh generated id", sid, resumed)
	}

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	rt := RuntimeConfig{SessionService: svc}
	persistent, err := piagent.New(piagent.Config{Model: stubLLM{}, SessionService: svc})
	if err != nil {
		t.Fatalf("piagent.New: %v", err)
	}
	sid, resumed, err = openPiSession(ctx, rt, persistent, "sess_acp")
	if err != nil {
		t.Fatalf("openPiSession() first open error = %v", err)
	}
	if sid != "sess_acp" || resumed {
		t.Errorf("first open = (%q, %v), want the ACP id and no history", sid, resumed)
	}

	// A second process seeing the same id opens the same transcript.
	again, err := piagent.New(piagent.Config{Model: stubLLM{}, SessionService: svc})
	if err != nil {
		t.Fatalf("piagent.New: %v", err)
	}
	sid, resumed, err = openPiSession(ctx, rt, again, "sess_acp")
	if err != nil {
		t.Fatalf("openPiSession() reopen error = %v", err)
	}
	if sid != "sess_acp" || !resumed {
		t.Errorf("reopen = (%q, %v), want the same id with history", sid, resumed)
	}

	sid, _, err = openPiSession(ctx, rt, again, "../escape")
	if err != nil {
		t.Fatalf("openPiSession() hostile id error = %v", err)
	}
	if !strings.HasPrefix(sid, "acp-") {
		t.Errorf("hostile id persisted as %q, want a hashed id", sid)
	}
	if _, err := svc.Get(ctx, &adksession.GetRequest{AppName: piagent.AppName, UserID: piagent.DefaultUserID, SessionID: sid}); err != nil {
		t.Errorf("hashed session not persisted: %v", err)
	}
}

// failingSessionService makes Create and Get fail so error paths that a real
// store reaches only on a broken disk become testable.
type failingSessionService struct {
	adksession.Service
	createErr error
}

func (f failingSessionService) Get(context.Context, *adksession.GetRequest) (*adksession.GetResponse, error) {
	return nil, errors.New("no such session")
}

func (f failingSessionService) Create(context.Context, *adksession.CreateRequest) (*adksession.CreateResponse, error) {
	return nil, f.createErr
}

func TestOpenPiSessionSurfacesFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := failingSessionService{Service: adksession.InMemoryService(), createErr: errors.New("disk full")}

	ag, err := piagent.New(piagent.Config{Model: stubLLM{}, SessionService: svc})
	if err != nil {
		t.Fatalf("piagent.New: %v", err)
	}

	// With a service configured, the failure comes through OpenSession.
	if _, _, err := openPiSession(ctx, RuntimeConfig{SessionService: svc}, ag, "sess_x"); err == nil ||
		!strings.Contains(err.Error(), "opening session") {
		t.Errorf("openPiSession() error = %v, want it wrapped with \"opening session\"", err)
	}

	// Without one, the failure comes through CreateSession instead.
	if _, _, err := openPiSession(ctx, RuntimeConfig{}, ag, "sess_x"); err == nil ||
		!strings.Contains(err.Error(), "creating session") {
		t.Errorf("openPiSession() error = %v, want it wrapped with \"creating session\"", err)
	}
}

func TestAgentLoadSessionWithoutConnectionSkipsReplay(t *testing.T) {
	t.Parallel()
	// No agent-side connection is wired, so there is no updater to replay
	// through. Load must still succeed and leave the session bound.
	store := &fakeStore{transcripts: map[string][]string{"sess_quiet": {"reply"}}}
	a := &Agent{Sessions: store}

	if _, err := a.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId: "sess_quiet", Cwd: "/tmp", McpServers: []acp.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	if len(store.replayed) != 0 {
		t.Errorf("replayed %v with no connection; want the replay skipped", store.replayed)
	}
	a.mu.Lock()
	_, bound := a.sessions["sess_quiet"]
	a.mu.Unlock()
	if !bound {
		t.Error("session not bound after LoadSession")
	}
}
