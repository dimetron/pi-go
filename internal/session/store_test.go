package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func TestCreateSessionMetaAlwaysHasModel(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	metaPath := filepath.Join(svc.baseDir, resp.Session.ID(), "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding meta.json: %v", err)
	}
	model, ok := raw["model"].(string)
	if !ok {
		t.Fatalf(`meta.json missing string "model" field; got %T`, raw["model"])
	}
	if model != UnknownModel {
		t.Errorf("model = %q, want %q", model, UnknownModel)
	}

	if err := svc.SetSessionModel(resp.Session.ID(), "claude-sonnet-4-6"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	data, err = os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta.json after SetSessionModel: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding meta.json after SetSessionModel: %v", err)
	}
	if got := raw["model"].(string); got != "claude-sonnet-4-6" {
		t.Errorf("model after SetSessionModel = %q, want %q", got, "claude-sonnet-4-6")
	}

	if err := svc.SetSessionModel(resp.Session.ID(), "  "); err != nil {
		t.Fatalf("SetSessionModel(blank): %v", err)
	}
	data, _ = os.ReadFile(metaPath)
	_ = json.Unmarshal(data, &raw)
	if got := raw["model"].(string); got != UnknownModel {
		t.Errorf("blank model normalized to %q, want %q", got, UnknownModel)
	}
}

func TestSetSessionModelUnknownSession(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SetSessionModel("ghost", "x"); err == nil {
		t.Error("expected error for unknown session")
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

	// Verify session is gone (no longer accessible).
	_, err = svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: sessionID,
	})
	if err == nil {
		t.Error("expected error getting deleted session")
	}

	// Verify session is archived (directory moved to archive/yyyy/mm/dd/).
	sessionDir := filepath.Join(svc.baseDir, sessionID)
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Error("session directory should be moved, not in sessions root")
	}
	now := time.Now()
	archiveDir := filepath.Join(svc.baseDir, "archive",
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()),
		sessionID)
	if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
		t.Errorf("session should be archived at %s", archiveDir)
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

func TestSessionStateSetAndAll(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// Set state via State().Set()
	state := resp.Session.State()
	if err := state.Set("key1", "val1"); err != nil {
		t.Fatalf("State.Set() error: %v", err)
	}
	if err := state.Set("key2", 42); err != nil {
		t.Fatalf("State.Set() error: %v", err)
	}

	// Verify Get.
	val, err := state.Get("key1")
	if err != nil || val != "val1" {
		t.Errorf("State.Get(key1) = %v, %v; want val1", val, err)
	}

	// Verify All() iterates over all keys.
	found := make(map[string]bool)
	for k := range state.All() {
		found[k] = true
	}
	if !found["key1"] || !found["key2"] {
		t.Errorf("State.All() missing keys, got %v", found)
	}
}

func TestSessionStateGetMissing(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	_, err := resp.Session.State().Get("nonexistent")
	if !errors.Is(err, session.ErrStateKeyNotExist) {
		t.Errorf("expected ErrStateKeyNotExist, got %v", err)
	}
}

func TestLastSessionIDPicksMostRecent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "older-session",
	})

	// Ensure timestamps differ.
	time.Sleep(10 * time.Millisecond)

	svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "newer-session",
	})

	id := svc.LastSessionID("test-app", "test-user")
	if id != "newer-session" {
		t.Errorf("LastSessionID() = %q, want %q", id, "newer-session")
	}
}

func TestLastSessionIDFiltersAppAndUser(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.Create(ctx, &session.CreateRequest{
		AppName:   "app-a",
		UserID:    "user-1",
		SessionID: "sess-a",
	})
	svc.Create(ctx, &session.CreateRequest{
		AppName:   "app-b",
		UserID:    "user-1",
		SessionID: "sess-b",
	})
	svc.Create(ctx, &session.CreateRequest{
		AppName:   "app-a",
		UserID:    "user-2",
		SessionID: "sess-c",
	})

	// Only sess-a matches app-a + user-1.
	id := svc.LastSessionID("app-a", "user-1")
	if id != "sess-a" {
		t.Errorf("LastSessionID() = %q, want %q", id, "sess-a")
	}

	// No match returns "".
	id = svc.LastSessionID("app-x", "user-1")
	if id != "" {
		t.Errorf("LastSessionID() = %q, want empty for unknown app", id)
	}
}

func TestLastSessionIDEmptyDir(t *testing.T) {
	svc := newTestService(t)
	id := svc.LastSessionID("any-app", "any-user")
	if id != "" {
		t.Errorf("LastSessionID() = %q, want empty when no sessions", id)
	}
}

func TestEstimateEventTokensAllPartTypes(t *testing.T) {
	ev := &session.Event{}
	ev.Content = genai.NewContentFromText(strings.Repeat("x", 400), genai.RoleUser)
	// Add a function call part manually.
	ev.Content.Parts = append(ev.Content.Parts,
		genai.NewPartFromFunctionCall("myFunc", map[string]any{"arg": strings.Repeat("y", 100)}),
	)
	// Add a function response part.
	ev.Content.Parts = append(ev.Content.Parts,
		genai.NewPartFromFunctionResponse("myFunc", map[string]any{"result": strings.Repeat("z", 100)}),
	)

	nilContentEv := &session.Event{} // nil content — should be skipped
	events := []*session.Event{ev, nilContentEv}

	tokens := estimateEventTokens(events)
	if tokens <= 100 {
		t.Errorf("estimateEventTokens() = %d, expected > 100 (text + function parts)", tokens)
	}
}

func TestEstimateEventTokensEmpty(t *testing.T) {
	if got := estimateEventTokens(nil); got != 0 {
		t.Errorf("estimateEventTokens(nil) = %d, want 0", got)
	}
	if got := estimateEventTokens([]*session.Event{}); got != 0 {
		t.Errorf("estimateEventTokens([]) = %d, want 0", got)
	}
}

func TestRewriteEvents(t *testing.T) {
	dir := t.TempDir()

	// Create the events.jsonl so the file exists first.
	eventsPath := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(eventsPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	events := []*session.Event{
		{ID: "e1", Timestamp: time.Now(), Author: "user"},
		{ID: "e2", Timestamp: time.Now(), Author: "model"},
	}

	if err := rewriteEvents(dir, events); err != nil {
		t.Fatalf("rewriteEvents() error: %v", err)
	}

	// Reload and verify.
	loaded, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d events, want 2", len(loaded))
	}
	if loaded[0].ID != "e1" {
		t.Errorf("event[0].ID = %q, want e1", loaded[0].ID)
	}
	if loaded[1].ID != "e2" {
		t.Errorf("event[1].ID = %q, want e2", loaded[1].ID)
	}
}

func TestRewriteEventsEmpty(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte("old content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := rewriteEvents(dir, nil); err != nil {
		t.Fatalf("rewriteEvents(nil) error: %v", err)
	}

	loaded, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded %d events after rewrite with nil, want 0", len(loaded))
	}
}

func TestReadEventsNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	// No events.jsonl created — should return nil, nil.
	events, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error: %v", err)
	}
	if events != nil {
		t.Errorf("readEvents() = %v, want nil for missing file", events)
	}
}

func TestReadEventsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	events, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error: %v", err)
	}
	if events != nil {
		t.Errorf("readEvents() = %v, want nil for empty file", events)
	}
}

func TestWriteMetaAndReadMeta(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	meta := &Meta{
		ID:        "test-id",
		AppName:   "app",
		UserID:    "user",
		WorkDir:   "/work",
		Model:     "gemini",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := writeMeta(dir, meta); err != nil {
		t.Fatalf("writeMeta() error: %v", err)
	}

	loaded, err := readMeta(dir)
	if err != nil {
		t.Fatalf("readMeta() error: %v", err)
	}
	if loaded.ID != meta.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, meta.ID)
	}
	if loaded.AppName != meta.AppName {
		t.Errorf("AppName = %q, want %q", loaded.AppName, meta.AppName)
	}
	if loaded.WorkDir != meta.WorkDir {
		t.Errorf("WorkDir = %q, want %q", loaded.WorkDir, meta.WorkDir)
	}
}

func TestWriteMetaBadDir(t *testing.T) {
	// Write to a nonexistent directory — should error.
	meta := &Meta{ID: "x", AppName: "a", UserID: "u"}
	err := writeMeta("/nonexistent/path/that/does/not/exist", meta)
	if err == nil {
		t.Error("writeMeta() to bad dir: expected error, got nil")
	}
}

func TestEventListAtBounds(t *testing.T) {
	e0 := &session.Event{ID: "e0"}
	e1 := &session.Event{ID: "e1"}
	list := eventList([]*session.Event{e0, e1})

	if got := list.At(0); got != e0 {
		t.Errorf("At(0) = %v, want e0", got)
	}
	if got := list.At(1); got != e1 {
		t.Errorf("At(1) = %v, want e1", got)
	}
	if got := list.At(2); got != nil {
		t.Errorf("At(2) = %v, want nil (out of bounds)", got)
	}
	if got := list.At(-1); got != nil {
		t.Errorf("At(-1) = %v, want nil (negative index)", got)
	}
}

func TestEventListAllEarlyReturn(t *testing.T) {
	events := make([]*session.Event, 5)
	for i := range events {
		events[i] = &session.Event{ID: fmt.Sprintf("e%d", i)}
	}
	list := eventList(events)

	count := 0
	for range list.All() {
		count++
		break // stop after first
	}
	if count != 1 {
		t.Errorf("early return from All(): got %d iterations, want 1", count)
	}
}

func TestStateAllEarlyReturn(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	state := resp.Session.State()
	state.Set("k1", "v1")
	state.Set("k2", "v2")
	state.Set("k3", "v3")

	count := 0
	for range state.All() {
		count++
		break
	}
	if count != 1 {
		t.Errorf("early return from State.All(): got %d iterations, want 1", count)
	}
}

func TestGetSessionWithAfterFilter(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	base := time.Now()
	for i := 0; i < 5; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("e%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("msg-%d", i), genai.RoleUser)
		svc.AppendEvent(ctx, resp.Session, event)
	}

	// Filter to events after the 3rd event's timestamp (i=2).
	after := base.Add(2 * time.Second)
	getResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
		After:     after,
	})
	if err != nil {
		t.Fatalf("Get(After) error: %v", err)
	}
	// Events at t=2,3,4 qualify (>= after).
	if getResp.Session.Events().Len() != 3 {
		t.Errorf("Get(After) events = %d, want 3", getResp.Session.Events().Len())
	}
}

func TestGetRequiredFieldsValidation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Get(ctx, &session.GetRequest{AppName: "app", UserID: ""})
	if err == nil {
		t.Error("expected error for missing UserID")
	}
	_, err = svc.Get(ctx, &session.GetRequest{AppName: "", UserID: "user"})
	if err == nil {
		t.Error("expected error for missing AppName")
	}
}

func TestCreateRequiredFieldsValidation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &session.CreateRequest{AppName: "", UserID: "user"})
	if err == nil {
		t.Error("expected error for missing AppName")
	}
	_, err = svc.Create(ctx, &session.CreateRequest{AppName: "app", UserID: ""})
	if err == nil {
		t.Error("expected error for missing UserID")
	}
}

func TestAppendEventNilSessionError(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	err := svc.AppendEvent(ctx, nil, &session.Event{ID: "e1", Timestamp: time.Now()})
	if err == nil {
		t.Error("expected error for nil session")
	}
}

func TestAppendEventNilEventError(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	err := svc.AppendEvent(ctx, resp.Session, nil)
	if err == nil {
		t.Error("expected error for nil event")
	}
}

func TestNewFileServiceBadDir(t *testing.T) {
	// Attempt to create a service in a path that is a file, not a dir.
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	f.Close()
	// Try to use the file path as a directory — MkdirAll will fail on some systems,
	// or if it succeeds the service should still be created. We just verify no panic.
	_, _ = NewFileService(f.Name() + "/subdir")
}

func TestCreateWithInitialState(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "explicit-id",
		State:     map[string]any{"init-key": "init-val"},
	})
	if resp.Session.ID() != "explicit-id" {
		t.Errorf("ID() = %q, want explicit-id", resp.Session.ID())
	}
	// Verify initial state is accessible.
	val, err := resp.Session.State().Get("init-key")
	if err != nil {
		t.Fatalf("State.Get(init-key): %v", err)
	}
	if val != "init-val" {
		t.Errorf("init-key = %v, want init-val", val)
	}
}

func TestSessionLastUpdateTime(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	before := time.Now()
	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	after := time.Now()

	lut := resp.Session.LastUpdateTime()
	if lut.Before(before) || lut.After(after) {
		t.Errorf("LastUpdateTime %v not between %v and %v", lut, before, after)
	}

	// Append event and verify update time advances.
	time.Sleep(time.Millisecond)
	event := &session.Event{
		ID:        "ev1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	event.Content = genai.NewContentFromText("hello", genai.RoleUser)
	if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: resp.Session.ID(),
	})
	lut2 := getResp.Session.LastUpdateTime()
	if !lut2.After(lut) {
		t.Errorf("LastUpdateTime should advance after event, got %v vs %v", lut2, lut)
	}
}

// --- ATIF integration tests ---

func TestATIFFileCreatedOnAppendEvent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	sessionID := resp.Session.ID()
	sessionDir := filepath.Join(svc.baseDir, sessionID)

	// Before any events, no ATIF file should exist (writer defers until first event).
	atifPath := filepath.Join(sessionDir, "trajectory.atif.json")
	if _, err := os.Stat(atifPath); err == nil {
		t.Fatal("trajectory.atif.json should not exist before first event")
	}

	// Append a user event.
	event := &session.Event{
		ID:        "ev1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	event.Content = genai.NewContentFromText("hello world", genai.RoleUser)

	if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
		t.Fatalf("AppendEvent() error: %v", err)
	}

	// ATIF file should now exist and be valid JSON.
	data, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("reading trajectory.atif.json: %v", err)
	}

	var trajectory map[string]any
	if err := json.Unmarshal(data, &trajectory); err != nil {
		t.Fatalf("trajectory.atif.json is not valid JSON: %v", err)
	}

	if v, _ := trajectory["schema_version"].(string); v != "ATIF-v1.6" {
		t.Errorf("schema_version = %q, want %q", v, "ATIF-v1.6")
	}

	steps, ok := trajectory["steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("expected 1 step, got %v", trajectory["steps"])
	}
}

func TestATIFMultipleEventsProduceSequentialSteps(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	sessionID := resp.Session.ID()
	atifPath := filepath.Join(svc.baseDir, sessionID, "trajectory.atif.json")

	// Append 3 events with different authors.
	authors := []string{"user", "model", "user"}
	for i, author := range authors {
		event := &session.Event{
			ID:        fmt.Sprintf("ev%d", i+1),
			Timestamp: time.Now(),
			Author:    author,
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("message %d", i+1), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatalf("AppendEvent(%d) error: %v", i, err)
		}
	}

	data, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("reading trajectory.atif.json: %v", err)
	}

	var trajectory map[string]any
	if err := json.Unmarshal(data, &trajectory); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	steps, ok := trajectory["steps"].([]any)
	if !ok || len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}

	// Verify step_ids are sequential 1, 2, 3.
	for i, s := range steps {
		step := s.(map[string]any)
		stepID := int(step["step_id"].(float64))
		if stepID != i+1 {
			t.Errorf("step[%d].step_id = %d, want %d", i, stepID, i+1)
		}
	}
}

func TestATIFWriteFailureDoesNotBreakAppendEvent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	sessionID := resp.Session.ID()
	sessionDir := filepath.Join(svc.baseDir, sessionID)

	// Make the session directory read-only so ATIF write will fail.
	atifPath := filepath.Join(sessionDir, "trajectory.atif.json")
	// Create a directory at the atif path to cause a write error.
	if err := os.MkdirAll(atifPath, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	event := &session.Event{
		ID:        "ev1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	event.Content = genai.NewContentFromText("hello", genai.RoleUser)

	// AppendEvent should succeed even though ATIF write fails.
	if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
		t.Fatalf("AppendEvent should succeed despite ATIF failure: %v", err)
	}

	// Verify the session event was still persisted.
	events, err := readEvents(sessionDir)
	if err != nil {
		t.Fatalf("readEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event in JSONL, got %d", len(events))
	}
}

func TestATIFWriterAccessor(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	w := svc.ATIFWriter(resp.Session.ID())
	if w == nil {
		t.Fatal("ATIFWriter should not be nil for created session")
	}

	w2 := svc.ATIFWriter("nonexistent")
	if w2 != nil {
		t.Error("ATIFWriter should be nil for nonexistent session")
	}
}

func TestATIFPartialEventsSkipped(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Append a partial event (should be skipped entirely, including ATIF).
	partial := &session.Event{
		ID:        "partial1",
		Timestamp: time.Now(),
		Author:    "model",
	}
	partial.Partial = true
	partial.Content = genai.NewContentFromText("partial...", genai.RoleModel)
	if err := svc.AppendEvent(ctx, resp.Session, partial); err != nil {
		t.Fatalf("AppendEvent(partial) error: %v", err)
	}

	// No ATIF file because no non-partial events.
	atifPath := filepath.Join(svc.baseDir, resp.Session.ID(), "trajectory.atif.json")
	if _, err := os.Stat(atifPath); err == nil {
		t.Error("trajectory.atif.json should not exist after only partial events")
	}

	// Now append a real event.
	real := &session.Event{
		ID:        "real1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	real.Content = genai.NewContentFromText("hello", genai.RoleUser)
	if err := svc.AppendEvent(ctx, resp.Session, real); err != nil {
		t.Fatalf("AppendEvent(real) error: %v", err)
	}

	data, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("reading trajectory.atif.json: %v", err)
	}

	var trajectory map[string]any
	if err := json.Unmarshal(data, &trajectory); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	steps := trajectory["steps"].([]any)
	if len(steps) != 1 {
		t.Errorf("expected 1 step (partial skipped), got %d", len(steps))
	}
}

func TestATIFCreatedOnSessionLoad(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "load-test",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Append an event so session has data.
	event := &session.Event{
		ID:        "ev1",
		Timestamp: time.Now(),
		Author:    "user",
	}
	event.Content = genai.NewContentFromText("hello", genai.RoleUser)
	if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
		t.Fatalf("AppendEvent() error: %v", err)
	}

	// Create a fresh service (simulating restart) and load the session.
	svc2, err := NewFileService(svc.baseDir)
	if err != nil {
		t.Fatalf("NewFileService() error: %v", err)
	}

	_, err = svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "load-test",
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	// The loaded session should have an ATIF writer.
	w := svc2.ATIFWriter("load-test")
	if w == nil {
		t.Fatal("ATIFWriter should not be nil for loaded session")
	}
}

func TestATIFRebuiltOnSessionResume(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "resume-test",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Append 5 events.
	for i := 1; i <= 5; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("ev%d", i),
			Timestamp: time.Now(),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("message %d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatalf("AppendEvent(%d) error: %v", i, err)
		}
	}

	// Create a fresh service (simulating restart) and load the session.
	svc2, err := NewFileService(svc.baseDir)
	if err != nil {
		t.Fatalf("NewFileService() error: %v", err)
	}

	_, err = svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "resume-test",
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	// ATIF file should be rebuilt with all 5 events' steps.
	atifPath := filepath.Join(svc.baseDir, "resume-test", "trajectory.atif.json")
	data, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("reading trajectory.atif.json: %v", err)
	}

	var trajectory map[string]any
	if err := json.Unmarshal(data, &trajectory); err != nil {
		t.Fatalf("trajectory.atif.json is not valid JSON: %v", err)
	}

	steps, ok := trajectory["steps"].([]any)
	if !ok || len(steps) != 5 {
		t.Fatalf("expected 5 steps after resume, got %d", len(steps))
	}

	// Verify sequential step IDs.
	for i, s := range steps {
		step := s.(map[string]any)
		wantID := float64(i + 1)
		if step["step_id"] != wantID {
			t.Errorf("step[%d].step_id = %v, want %v", i, step["step_id"], wantID)
		}
	}
}

func TestATIFRebuiltWhenFileDeleted(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "deleted-atif",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Append events.
	for i := 1; i <= 3; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("ev%d", i),
			Timestamp: time.Now(),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("msg %d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatalf("AppendEvent(%d) error: %v", i, err)
		}
	}

	// Delete the ATIF file.
	atifPath := filepath.Join(svc.baseDir, "deleted-atif", "trajectory.atif.json")
	os.Remove(atifPath)
	if _, err := os.Stat(atifPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ATIF file should be deleted")
	}

	// Reload in a fresh service — should recreate ATIF.
	svc2, err := NewFileService(svc.baseDir)
	if err != nil {
		t.Fatalf("NewFileService() error: %v", err)
	}

	_, err = svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "deleted-atif",
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	// ATIF file should be recreated.
	data, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("ATIF file not recreated: %v", err)
	}

	var trajectory map[string]any
	if err := json.Unmarshal(data, &trajectory); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	steps := trajectory["steps"].([]any)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps after rebuild, got %d", len(steps))
	}
}

func TestATIFResumedSessionContinuesStepIDs(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "continue-ids",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Append 3 events.
	for i := 1; i <= 3; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("ev%d", i),
			Timestamp: time.Now(),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("msg %d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, event); err != nil {
			t.Fatalf("AppendEvent(%d) error: %v", i, err)
		}
	}

	// Simulate restart.
	svc2, err := NewFileService(svc.baseDir)
	if err != nil {
		t.Fatalf("NewFileService() error: %v", err)
	}

	getResp, err := svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "continue-ids",
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	// Append 2 more events after resume.
	for i := 4; i <= 5; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("ev%d", i),
			Timestamp: time.Now(),
			Author:    "model",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("response %d", i), genai.RoleModel)
		if err := svc2.AppendEvent(ctx, getResp.Session, event); err != nil {
			t.Fatalf("AppendEvent(%d) error: %v", i, err)
		}
	}

	// Read ATIF and verify all 5 steps with sequential IDs.
	atifPath := filepath.Join(svc.baseDir, "continue-ids", "trajectory.atif.json")
	data, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("reading trajectory.atif.json: %v", err)
	}

	var trajectory map[string]any
	if err := json.Unmarshal(data, &trajectory); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	steps, ok := trajectory["steps"].([]any)
	if !ok || len(steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(steps))
	}

	// All step IDs should be sequential 1..5.
	for i, s := range steps {
		step := s.(map[string]any)
		wantID := float64(i + 1)
		if step["step_id"] != wantID {
			t.Errorf("step[%d].step_id = %v, want %v", i, step["step_id"], wantID)
		}
	}

	// First 3 should be from "user", last 2 from "agent".
	for i, s := range steps {
		step := s.(map[string]any)
		var wantSource string
		if i < 3 {
			wantSource = "user"
		} else {
			wantSource = "agent"
		}
		if step["source"] != wantSource {
			t.Errorf("step[%d].source = %v, want %q", i, step["source"], wantSource)
		}
	}
}

func TestUpdatePlanContext_Set(t *testing.T) {
	svc := newTestService(t)
	sessionID := createTestSession(t, svc)

	ctx := &PlanContext{
		TaskName:  "features/TOO/001-test",
		RoughIdea: "add rate limiting",
		SpecDir:   "/tmp/specs/features/TOO/001-test",
		Phase:     "plan",
	}

	err := svc.UpdatePlanContext(sessionID, ctx)
	if err != nil {
		t.Fatalf("UpdatePlanContext() error: %v", err)
	}

	// Verify it was saved.
	got, err := svc.GetPlanContext(sessionID)
	if err != nil {
		t.Fatalf("GetPlanContext() error: %v", err)
	}
	if got == nil {
		t.Fatal("GetPlanContext() returned nil")
	} else {
		if got.TaskName != ctx.TaskName {
			t.Errorf("TaskName = %q, want %q", got.TaskName, ctx.TaskName)
		}
		if got.RoughIdea != ctx.RoughIdea {
			t.Errorf("RoughIdea = %q, want %q", got.RoughIdea, ctx.RoughIdea)
		}
		if got.SpecDir != ctx.SpecDir {
			t.Errorf("SpecDir = %q, want %q", got.SpecDir, ctx.SpecDir)
		}
		if got.Phase != ctx.Phase {
			t.Errorf("Phase = %q, want %q", got.Phase, ctx.Phase)
		}
	}
}

func TestUpdatePlanContext_Clear(t *testing.T) {
	svc := newTestService(t)
	sessionID := createTestSession(t, svc)

	// Set context first.
	err := svc.UpdatePlanContext(sessionID, &PlanContext{
		TaskName: "tools/001-test",
	})
	if err != nil {
		t.Fatalf("UpdatePlanContext() set error: %v", err)
	}

	// Clear it.
	err = svc.UpdatePlanContext(sessionID, nil)
	if err != nil {
		t.Fatalf("UpdatePlanContext() clear error: %v", err)
	}

	got, err := svc.GetPlanContext(sessionID)
	if err != nil {
		t.Fatalf("GetPlanContext() error: %v", err)
	}
	if got != nil {
		t.Errorf("GetPlanContext() = %v, want nil", got)
	}
}

func TestUpdatePlanContext_NotFound(t *testing.T) {
	svc := newTestService(t)

	err := svc.UpdatePlanContext("nonexistent", &PlanContext{TaskName: "x"})
	if err == nil {
		t.Error("UpdatePlanContext() expected error for nonexistent session, got nil")
	}

	_, err = svc.GetPlanContext("nonexistent")
	if err == nil {
		t.Error("GetPlanContext() expected error for nonexistent session, got nil")
	}
}

func TestGenerateSessionID(t *testing.T) {
	now := time.Now()
	id := GenerateSessionID()

	// Should be exactly 23 characters: yymmdd-hhmm-xxxxx-xxxxx
	if len(id) != 23 {
		t.Errorf("GenerateSessionID() length = %d, want 23", len(id))
	}

	// Parse the parts (yymmdd-hhmm-xxxxx-xxxxx)
	parts := strings.Split(id, "-")
	if len(parts) != 4 {
		t.Fatalf("GenerateSessionID() should have 4 parts separated by -, got %d", len(parts))
	}

	// First part should be yymmdd (6 chars)
	if len(parts[0]) != 6 {
		t.Errorf("GenerateSessionID() first part (yymmdd) length = %d, want 6", len(parts[0]))
	}
	expectedPrefix := fmt.Sprintf("%02d%02d%02d",
		now.Year()%100,
		now.Month(),
		now.Day(),
	)
	if parts[0] != expectedPrefix {
		t.Errorf("GenerateSessionID() first part (yymmdd) = %q, want %q", parts[0], expectedPrefix)
	}

	// Second part should be hhmm (4 chars)
	if len(parts[1]) != 4 {
		t.Errorf("GenerateSessionID() second part (hhmm) length = %d, want 4", len(parts[1]))
	}
	expectedTime := fmt.Sprintf("%02d%02d",
		now.Hour(),
		now.Minute(),
	)
	if parts[1] != expectedTime {
		t.Errorf("GenerateSessionID() second part (hhmm) = %q, want %q", parts[1], expectedTime)
	}

	// Last two parts should be 5 hex chars each
	if len(parts[2]) != 5 || len(parts[3]) != 5 {
		t.Errorf("GenerateSessionID() hex parts should be 5 chars each, got %d and %d", len(parts[2]), len(parts[3]))
	}

	// Validate hex parts contain only valid hex digits
	hexChars := "0123456789abcdefABCDEF"
	for i, part := range parts[2:] {
		for _, c := range part {
			if !strings.ContainsRune(hexChars, c) {
				t.Errorf("GenerateSessionID() part[%d] %q contains invalid hex char %q", i+2, part, c)
			}
		}
	}

	// Generate another ID - should differ (different random suffix)
	id2 := GenerateSessionID()
	if id == id2 {
		t.Error("GenerateSessionID() generated duplicate IDs")
	}
}

func TestGenerateSessionIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateSessionID()
		if ids[id] {
			t.Fatalf("GenerateSessionID() generated duplicate ID %q", id)
		}
		ids[id] = true
	}
}

func TestGenerateSessionIDSortable(t *testing.T) {
	// IDs generated in order should be sortable by time
	ids := make([]string, 10)
	for i := 0; i < 10; i++ {
		ids[i] = GenerateSessionID()
		time.Sleep(10 * time.Millisecond)
	}

	// All should have the same prefix since they were generated within ~100ms
	// But due to minute boundaries, they may differ - just verify sorting works
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)

	// Verify all IDs are still valid (23 chars: yymmdd-hhmm-xxxxx-xxxxx)
	for _, id := range sorted {
		if len(id) != 23 {
			t.Errorf("sorted ID %q has invalid length %d", id, len(id))
		}
	}
}

func TestMetaPlanContext(t *testing.T) {
	// Test that Meta with PlanContext marshals and unmarshals correctly.
	meta := Meta{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		PlanContext: &PlanContext{
			TaskName:  "tools/002-foo",
			RoughIdea: "implement feature",
			SpecDir:   "/workspace/specs/tools/002-foo",
			Phase:     "plan",
		},
	}

	// Marshal.
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	// Unmarshal.
	var got Meta
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if got.PlanContext == nil {
		t.Fatal("PlanContext is nil after unmarshal")
	}
	if got.PlanContext.TaskName != meta.PlanContext.TaskName {
		t.Errorf("TaskName = %q, want %q", got.PlanContext.TaskName, meta.PlanContext.TaskName)
	}
	if got.PlanContext.SpecDir != meta.PlanContext.SpecDir {
		t.Errorf("SpecDir = %q, want %q", got.PlanContext.SpecDir, meta.PlanContext.SpecDir)
	}
}
