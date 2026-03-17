package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func newTestService(t *testing.T) *FileService {
	t.Helper()
	dir := t.TempDir()
	svc, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService() error: %v", err)
	}
	return svc
}

func createTestSession(t *testing.T, svc *FileService) string {
	t.Helper()
	ctx := context.Background()
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	return resp.Session.ID()
}

func TestCreateSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if resp.Session.ID() == "" {
		t.Error("session ID should not be empty")
	}
	if resp.Session.AppName() != "test-app" {
		t.Errorf("AppName = %q, want %q", resp.Session.AppName(), "test-app")
	}
	if resp.Session.UserID() != "test-user" {
		t.Errorf("UserID = %q, want %q", resp.Session.UserID(), "test-user")
	}

	// Verify files created on disk.
	sessionDir := filepath.Join(svc.baseDir, resp.Session.ID())
	if _, err := os.Stat(filepath.Join(sessionDir, "meta.json")); err != nil {
		t.Errorf("meta.json not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "events.jsonl")); err != nil {
		t.Errorf("events.jsonl not found: %v", err)
	}
}

func TestCreateSessionWithCustomID(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "custom-id-123",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if resp.Session.ID() != "custom-id-123" {
		t.Errorf("ID = %q, want %q", resp.Session.ID(), "custom-id-123")
	}
}

func TestCreateDuplicateSessionFails(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "dup-id",
	})
	if err != nil {
		t.Fatalf("first Create() error: %v", err)
	}

	_, err = svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "dup-id",
	})
	if err == nil {
		t.Error("expected error creating duplicate session")
	}
}

func TestGetSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sessionID := createTestSession(t, svc)

	resp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if resp.Session.ID() != sessionID {
		t.Errorf("ID = %q, want %q", resp.Session.ID(), sessionID)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "nonexistent",
	})
	if err == nil {
		t.Error("expected error getting nonexistent session")
	}
}

func TestListSessions(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Create two sessions.
	createTestSession(t, svc)
	createTestSession(t, svc)

	resp, err := svc.List(ctx, &session.ListRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Errorf("List() returned %d sessions, want 2", len(resp.Sessions))
	}
}

func TestListSessionsFiltersByApp(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Create session for different app.
	svc.Create(ctx, &session.CreateRequest{
		AppName:   "other-app",
		UserID:    "test-user",
		SessionID: "other-session",
	})
	createTestSession(t, svc) // test-app session

	resp, err := svc.List(ctx, &session.ListRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Errorf("List() returned %d sessions, want 1", len(resp.Sessions))
	}
}

func TestDeleteSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sessionID := createTestSession(t, svc)

	err := svc.Delete(ctx, &session.DeleteRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	// Verify session is gone.
	_, err = svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: sessionID,
	})
	if err == nil {
		t.Error("expected error getting deleted session")
	}

	// Verify directory is gone.
	sessionDir := filepath.Join(svc.baseDir, sessionID)
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Error("session directory should be deleted")
	}
}

func TestAppendEvent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	event := &session.Event{
		ID:        "event-1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	event.Content = genai.NewContentFromText("Hello", genai.RoleUser)

	err = svc.AppendEvent(ctx, resp.Session, event)
	if err != nil {
		t.Fatalf("AppendEvent() error: %v", err)
	}

	// Get session and verify event is there.
	getResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if getResp.Session.Events().Len() != 1 {
		t.Errorf("Events.Len() = %d, want 1", getResp.Session.Events().Len())
	}
}

func TestAppendEventPersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Create service and session, append an event.
	svc1, _ := NewFileService(dir)
	resp, _ := svc1.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "persist-test",
	})

	event := &session.Event{
		ID:        "event-1",
		Timestamp: time.Now(),
		Author:    "model",
	}
	event.Content = genai.NewContentFromText("Response", genai.RoleModel)
	svc1.AppendEvent(ctx, resp.Session, event)

	// Create a NEW service pointing to the same dir (simulates restart).
	svc2, _ := NewFileService(dir)
	getResp, err := svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "persist-test",
	})
	if err != nil {
		t.Fatalf("Get() on reloaded service error: %v", err)
	}
	if getResp.Session.Events().Len() != 1 {
		t.Errorf("Events.Len() after reload = %d, want 1", getResp.Session.Events().Len())
	}
}

func TestAppendEventSkipsPartial(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	event := &session.Event{
		ID:        "partial-1",
		Timestamp: time.Now(),
		Author:    "model",
	}
	event.Partial = true
	event.Content = genai.NewContentFromText("partial...", genai.RoleModel)

	err := svc.AppendEvent(ctx, resp.Session, event)
	if err != nil {
		t.Fatalf("AppendEvent() error: %v", err)
	}

	// Partial events should not be stored.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	if getResp.Session.Events().Len() != 0 {
		t.Errorf("partial event should not be stored, got %d events", getResp.Session.Events().Len())
	}
}

func TestGetWithNumRecentEvents(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add 5 events.
	for i := 0; i < 5; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("msg-%d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatal(err)
		}
	}

	// Get last 2 events.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:         "test-app",
		UserID:          "test-user",
		SessionID:       resp.Session.ID(),
		NumRecentEvents: 2,
	})
	if getResp.Session.Events().Len() != 2 {
		t.Errorf("NumRecentEvents=2: got %d events, want 2", getResp.Session.Events().Len())
	}
}

func TestLastSessionID(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// No sessions yet.
	if id := svc.LastSessionID("test-app", "test-user"); id != "" {
		t.Errorf("LastSessionID() = %q, want empty", id)
	}

	// Create two sessions with different update times.
	svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "old-session",
	})

	time.Sleep(10 * time.Millisecond) // Ensure different timestamps.

	svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
}

func TestFilteredSessionMethods(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add 5 events.
	for i := 0; i < 5; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("msg-%d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatal(err)
		}
	}

	// Get filtered session with NumRecentEvents - this creates filteredSession
	getResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:         "test-app",
		UserID:          "test-user",
		SessionID:       resp.Session.ID(),
		NumRecentEvents: 2,
	})
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}

	// Test filteredSession methods
	if getResp.Session.ID() != resp.Session.ID() {
		t.Errorf("filteredSession ID = %q, want %q", getResp.Session.ID(), resp.Session.ID())
	}
	if getResp.Session.AppName() != "test-app" {
		t.Errorf("filteredSession AppName = %q, want %q", getResp.Session.AppName(), "test-app")
	}
	if getResp.Session.UserID() != "test-user" {
		t.Errorf("filteredSession UserID = %q, want %q", getResp.Session.UserID(), "test-user")
	}

	// LastUpdateTime should return a valid time
	if getResp.Session.LastUpdateTime().IsZero() {
		t.Error("filteredSession LastUpdateTime should not be zero")
	}

	// State should work
	_, err = getResp.Session.State().Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key in filtered session state")
	}
}

func TestFilteredSessionAll(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add events
	for i := 0; i < 3; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now(),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("msg-%d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatal(err)
		}
	}

	// Get filtered session with NumRecentEvents
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:         "test-app",
		UserID:          "test-user",
		SessionID:       resp.Session.ID(),
		NumRecentEvents: 2,
	})

	// Test that we can iterate over events
	count := 0
	for range getResp.Session.Events().All() {
		count++
	}
	if count != 2 {
		t.Errorf("Events().All() count = %d, want 2", count)
	}
}

func TestDefaultCompactConfig(t *testing.T) {
	cfg := DefaultCompactConfig()
	if cfg.MaxTokens != 100000 {
		t.Errorf("MaxTokens = %d, want 100000", cfg.MaxTokens)
	}
	if cfg.KeepRecent != 10 {
		t.Errorf("KeepRecent = %d, want 10", cfg.KeepRecent)
	}
}

func TestSessionState(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Append event with state delta.
	event := &session.Event{
		ID:        "event-1",
		Timestamp: time.Now(),
		Author:    "model",
	}
	event.Content = genai.NewContentFromText("done", genai.RoleModel)
	event.Actions.StateDelta = map[string]any{
		"key1": "value1",
	}
	svc.AppendEvent(ctx, resp.Session, event)

	// Get and verify state.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	val, err := getResp.Session.State().Get("key1")
	if err != nil {
		t.Fatalf("State.Get() error: %v", err)
	}
	if val != "value1" {
		t.Errorf("State[key1] = %v, want %q", val, "value1")
	}
}

func TestCompactReducesEvents(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add 20 events with substantial text to exceed token threshold.
	for i := 0; i < 20; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		// Large text to push over token threshold.
		text := fmt.Sprintf("Message %d: %s", i, strings.Repeat("word ", 200))
		event.Content = genai.NewContentFromText(text, genai.RoleUser)
		svc.AppendEvent(ctx, resp.Session, event)
	}

	// Verify we have 20 events.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	if getResp.Session.Events().Len() != 20 {
		t.Fatalf("expected 20 events before compact, got %d", getResp.Session.Events().Len())
	}

	// Compact with a low threshold and keep 5 recent events.
	mockSummarizer := func(events []*session.Event) (string, error) {
		return fmt.Sprintf("Summary of %d events", len(events)), nil
	}

	err := svc.Compact(resp.Session.ID(), "test-app", "test-user", mockSummarizer, CompactConfig{
		MaxTokens:  100, // Low threshold to trigger compaction.
		KeepRecent: 5,
	})
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}

	// Should now have 1 summary + 5 recent = 6 events.
	getResp, _ = svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	if getResp.Session.Events().Len() != 6 {
		t.Errorf("after compact: got %d events, want 6", getResp.Session.Events().Len())
	}

	// First event should be the summary.
	firstEvent := getResp.Session.Events().At(0)
	if firstEvent.ID != "compaction-summary" {
		t.Errorf("first event ID = %q, want %q", firstEvent.ID, "compaction-summary")
	}
	if firstEvent.Content == nil || len(firstEvent.Content.Parts) == 0 {
		t.Fatal("summary event has no content")
	}
	summaryText := firstEvent.Content.Parts[0].Text
	if !strings.Contains(summaryText, "Summary of 15 events") {
		t.Errorf("summary text = %q, want to contain 'Summary of 15 events'", summaryText)
	}
}

func TestCompactNoOpWhenBelowThreshold(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add a few small events.
	for i := 0; i < 3; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText("short", genai.RoleUser)
		svc.AppendEvent(ctx, resp.Session, event)
	}

	called := false
	mockSummarizer := func(events []*session.Event) (string, error) {
		called = true
		return "summary", nil
	}

	err := svc.Compact(resp.Session.ID(), "test-app", "test-user", mockSummarizer, CompactConfig{
		MaxTokens:  100000,
		KeepRecent: 5,
	})
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}
	if called {
		t.Error("summarizer should not be called when below threshold")
	}

	// Events should be unchanged.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	if getResp.Session.Events().Len() != 3 {
		t.Errorf("events unchanged: got %d, want 3", getResp.Session.Events().Len())
	}
}

func TestCompactSummarizerError(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	for i := 0; i < 20; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(strings.Repeat("text ", 200), genai.RoleUser)
		svc.AppendEvent(ctx, resp.Session, event)
	}

	failingSummarizer := func(events []*session.Event) (string, error) {
		return "", fmt.Errorf("LLM unavailable")
	}

	err := svc.Compact(resp.Session.ID(), "test-app", "test-user", failingSummarizer, CompactConfig{
		MaxTokens:  100,
		KeepRecent: 5,
	})
	if err == nil {
		t.Error("expected error when summarizer fails")
	}

	// Events should be unchanged after failure.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	if getResp.Session.Events().Len() != 20 {
		t.Errorf("events should be unchanged after error, got %d, want 20", getResp.Session.Events().Len())
	}
}

func TestCompactPersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	svc1, _ := NewFileService(dir)
	resp, _ := svc1.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "compact-persist-test",
	})

	for i := 0; i < 20; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(strings.Repeat("data ", 200), genai.RoleUser)
		svc1.AppendEvent(ctx, resp.Session, event)
	}

	mockSummarizer := func(events []*session.Event) (string, error) {
		return "Persisted summary", nil
	}

	err := svc1.Compact("compact-persist-test", "test-app", "test-user", mockSummarizer, CompactConfig{
		MaxTokens:  100,
		KeepRecent: 5,
	})
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}

	// Load from a new service instance to verify disk persistence.
	svc2, _ := NewFileService(dir)
	getResp, err := svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "compact-persist-test",
	})
	if err != nil {
		t.Fatalf("Get() after reload error: %v", err)
	}
	if getResp.Session.Events().Len() != 6 {
		t.Errorf("after reload: got %d events, want 6", getResp.Session.Events().Len())
	}

	// Verify summary content survives reload.
	firstEvent := getResp.Session.Events().At(0)
	if !strings.Contains(firstEvent.Content.Parts[0].Text, "Persisted summary") {
		t.Errorf("summary not persisted correctly, got %q", firstEvent.Content.Parts[0].Text)
	}
}

func TestEstimateTokens(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add event with known text length.
	event := &session.Event{
		ID:        "event-1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	// 400 chars → ~100 tokens.
	event.Content = genai.NewContentFromText(strings.Repeat("abcd", 100), genai.RoleUser)
	svc.AppendEvent(ctx, resp.Session, event)

	tokens, err := svc.EstimateTokens(resp.Session.ID(), "test-app", "test-user")
	if err != nil {
		t.Fatalf("EstimateTokens() error: %v", err)
	}
	if tokens != 100 {
		t.Errorf("EstimateTokens() = %d, want 100", tokens)
	}
}

func TestCompactNotEnoughEvents(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Add fewer events than KeepRecent.
	for i := 0; i < 3; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(strings.Repeat("text ", 200), genai.RoleUser)
		svc.AppendEvent(ctx, resp.Session, event)
	}

	called := false
	mockSummarizer := func(events []*session.Event) (string, error) {
		called = true
		return "summary", nil
	}

	// KeepRecent=5 but only 3 events, so no compaction even if over threshold.
	err := svc.Compact(resp.Session.ID(), "test-app", "test-user", mockSummarizer, CompactConfig{
		MaxTokens:  1,
		KeepRecent: 5,
	})
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}
	if called {
		t.Error("summarizer should not be called when fewer events than KeepRecent")
	}
}

func TestSessionStateTempKeysStripped(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	event := &session.Event{
		ID:        "event-1",
		Timestamp: time.Now(),
		Author:    "model",
	}
	event.Content = genai.NewContentFromText("done", genai.RoleModel)
	event.Actions.StateDelta = map[string]any{
		"key1":         "persisted",
		"temp:scratch": "temporary",
	}
	svc.AppendEvent(ctx, resp.Session, event)

	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})

	// Persistent key should be there.
	if _, err := getResp.Session.State().Get("key1"); err != nil {
		t.Error("persistent key should be present")
	}
	// Temp key should be stripped.
	if _, err := getResp.Session.State().Get("temp:scratch"); err == nil {
		t.Error("temp key should be stripped from state delta")
	}
}
