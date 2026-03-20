package memory

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// newTestStore creates an in-memory SQLiteStore for testing.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSQLiteStore(db)
}

// insertTestSession creates a session required by foreign key constraints.
func insertTestSession(t *testing.T, store *SQLiteStore, sessionID, project string) {
	t.Helper()
	err := store.CreateSession(context.Background(), &Session{
		SessionID: sessionID,
		Project:   project,
		Status:    "active",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

func TestCreateSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		SessionID:  "sess-001",
		Project:    "/home/user/project",
		UserPrompt: "fix the bug",
		StartedAt:  time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Status:     "active",
	}

	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Verify the session was inserted
	var status, project string
	err := store.db.QueryRow("SELECT status, project FROM sessions WHERE session_id = ?", "sess-001").Scan(&status, &project)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if project != "/home/user/project" {
		t.Errorf("project = %q, want /home/user/project", project)
	}
}

func TestCreateSession_Duplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		SessionID: "sess-dup",
		Project:   "/project",
		Status:    "active",
		StartedAt: time.Now(),
	}

	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}

	err := store.CreateSession(ctx, sess)
	if err == nil {
		t.Error("expected error for duplicate session_id, got nil")
	}
}

func TestCompleteSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-complete", "/project")

	if err := store.CompleteSession(ctx, "sess-complete"); err != nil {
		t.Fatalf("CompleteSession: %v", err)
	}

	var status string
	err := store.db.QueryRow("SELECT status FROM sessions WHERE session_id = ?", "sess-complete").Scan(&status)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
}

func TestCompleteSession_NotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.CompleteSession(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session, got nil")
	}
}

func TestInsertObservation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-obs", "/project")

	obs := &Observation{
		SessionID:       "sess-obs",
		Project:         "/project",
		Title:           "Read main.go implementation",
		Type:            TypeDiscovery,
		Text:            "Examined the main entry point",
		SourceFiles:     []string{"main.go", "cmd/root.go"},
		ToolName:        "Read",
		PromptNumber:    1,
		DiscoveryTokens: 500,
		CreatedAt:       time.Now(),
	}

	if err := store.InsertObservation(ctx, obs); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	if obs.ID == 0 {
		t.Error("expected non-zero ID after insert")
	}
}

func TestGetObservations(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-get", "/project")

	// Insert 3 observations
	var ids []int64
	for i := 0; i < 3; i++ {
		obs := &Observation{
			SessionID:   "sess-get",
			Project:     "/project",
			Title:       fmt.Sprintf("obs-%d", i),
			Type:        TypeChange,
			Text:        fmt.Sprintf("observation %d", i),
			SourceFiles: []string{fmt.Sprintf("file%d.go", i)},
			ToolName:    "Read",
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("insert obs %d: %v", i, err)
		}
		ids = append(ids, obs.ID)
	}

	// Fetch all 3
	results, err := store.GetObservations(ctx, ids)
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d observations, want 3", len(results))
	}

	// Verify ordering (ASC by created_at)
	if results[0].Title != "obs-0" {
		t.Errorf("first result title = %q, want obs-0", results[0].Title)
	}
	if results[2].Title != "obs-2" {
		t.Errorf("last result title = %q, want obs-2", results[2].Title)
	}

	// Verify source_files deserialized
	if len(results[0].SourceFiles) != 1 || results[0].SourceFiles[0] != "file0.go" {
		t.Errorf("source_files = %v, want [file0.go]", results[0].SourceFiles)
	}
}

func TestGetObservations_MixedIDs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-mixed", "/project")

	obs := &Observation{
		SessionID:   "sess-mixed",
		Project:     "/project",
		Title:       "real observation",
		Type:        TypeFeature,
		Text:        "exists",
		SourceFiles: []string{},
		ToolName:    "Write",
		CreatedAt:   time.Now(),
	}
	if err := store.InsertObservation(ctx, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Fetch with one valid and one invalid ID
	results, err := store.GetObservations(ctx, []int64{obs.ID, 99999})
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1 (invalid ID should be skipped)", len(results))
	}
}

func TestGetObservations_Empty(t *testing.T) {
	store := newTestStore(t)
	results, err := store.GetObservations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetObservations(nil): %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty IDs, got %v", results)
	}
}

func TestRecentObservations(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-recent", "/project-a")
	insertTestSession(t, store, "sess-other", "/project-b")

	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	// Insert 5 observations for project-a
	for i := 0; i < 5; i++ {
		obs := &Observation{
			SessionID:   "sess-recent",
			Project:     "/project-a",
			Title:       fmt.Sprintf("obs-a-%d", i),
			Type:        TypeChange,
			Text:        "text",
			SourceFiles: []string{},
			ToolName:    "Read",
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
		}
		store.InsertObservation(ctx, obs)
	}

	// Insert 2 for project-b (should not appear)
	for i := 0; i < 2; i++ {
		obs := &Observation{
			SessionID:   "sess-other",
			Project:     "/project-b",
			Title:       fmt.Sprintf("obs-b-%d", i),
			Type:        TypeChange,
			Text:        "text",
			SourceFiles: []string{},
			ToolName:    "Read",
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
		}
		store.InsertObservation(ctx, obs)
	}

	// Fetch recent for project-a with limit 3
	results, err := store.RecentObservations(ctx, "/project-a", 3)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d, want 3", len(results))
	}

	// Should be DESC order (most recent first)
	if results[0].Title != "obs-a-4" {
		t.Errorf("first result = %q, want obs-a-4 (most recent)", results[0].Title)
	}
	if results[2].Title != "obs-a-2" {
		t.Errorf("last result = %q, want obs-a-2", results[2].Title)
	}
}

func TestUpsertSummary(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-sum", "/project")

	sum := &SessionSummary{
		SessionID:    "sess-sum",
		Project:      "/project",
		Request:      "implement feature X",
		Investigated: "read source files",
		Learned:      "uses MVC pattern",
		Completed:    "added controller",
		NextSteps:    "write tests",
		CreatedAt:    time.Now(),
	}

	if err := store.UpsertSummary(ctx, sum); err != nil {
		t.Fatalf("UpsertSummary (insert): %v", err)
	}
	if sum.ID == 0 {
		t.Error("expected non-zero ID after insert")
	}

	// Upsert again with updated fields
	sum.Learned = "uses MVVM pattern"
	if err := store.UpsertSummary(ctx, sum); err != nil {
		t.Fatalf("UpsertSummary (update): %v", err)
	}

	// Verify only one summary exists
	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM session_summaries WHERE session_id = ?", "sess-sum").Scan(&count)
	if count != 1 {
		t.Errorf("count = %d, want 1 after upsert", count)
	}

	// Verify updated value
	var learned string
	store.db.QueryRow("SELECT learned FROM session_summaries WHERE session_id = ?", "sess-sum").Scan(&learned)
	if learned != "uses MVVM pattern" {
		t.Errorf("learned = %q, want 'uses MVVM pattern'", learned)
	}
}

func TestRecentSummaries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("sess-rsum-%d", i)
		insertTestSession(t, store, sid, "/project")
		sum := &SessionSummary{
			SessionID: sid,
			Project:   "/project",
			Request:   fmt.Sprintf("request %d", i),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		}
		store.UpsertSummary(ctx, sum)
	}

	results, err := store.RecentSummaries(ctx, "/project", 3)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d, want 3", len(results))
	}

	// Most recent first
	if results[0].Request != "request 4" {
		t.Errorf("first = %q, want 'request 4'", results[0].Request)
	}
}

func TestRecentSummaries_Empty(t *testing.T) {
	store := newTestStore(t)
	results, err := store.RecentSummaries(context.Background(), "/empty-project", 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestFullCRUDCycle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create session
	sess := &Session{
		SessionID: "sess-crud",
		Project:   "/test",
		Status:    "active",
		StartedAt: time.Now(),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Insert observations
	for i := 0; i < 3; i++ {
		obs := &Observation{
			SessionID:   "sess-crud",
			Project:     "/test",
			Title:       fmt.Sprintf("step %d", i),
			Type:        TypeFeature,
			Text:        fmt.Sprintf("did thing %d", i),
			SourceFiles: []string{"a.go"},
			ToolName:    "Edit",
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("InsertObservation %d: %v", i, err)
		}
	}

	// Fetch recent
	obs, err := store.RecentObservations(ctx, "/test", 10)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want 3", len(obs))
	}

	// Upsert summary
	sum := &SessionSummary{
		SessionID: "sess-crud",
		Project:   "/test",
		Request:   "full test",
		Completed: "all steps",
		CreatedAt: time.Now(),
	}
	if err := store.UpsertSummary(ctx, sum); err != nil {
		t.Fatalf("UpsertSummary: %v", err)
	}

	// Complete session
	if err := store.CompleteSession(ctx, "sess-crud"); err != nil {
		t.Fatalf("CompleteSession: %v", err)
	}

	// Verify summaries
	sums, err := store.RecentSummaries(ctx, "/test", 5)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(sums) != 1 {
		t.Fatalf("got %d summaries, want 1", len(sums))
	}
	if sums[0].Request != "full test" {
		t.Errorf("summary request = %q, want 'full test'", sums[0].Request)
	}
}

func TestRecentObservations_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-deflimit", "/project")
	now := time.Now()

	// Insert 60 observations.
	for i := 0; i < 60; i++ {
		obs := &Observation{
			SessionID:   "sess-deflimit",
			Project:     "/project",
			Title:       fmt.Sprintf("obs-%d", i),
			Type:        TypeChange,
			Text:        "text",
			SourceFiles: []string{},
			ToolName:    "Read",
			CreatedAt:   now.Add(time.Duration(i) * time.Second),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("InsertObservation %d: %v", i, err)
		}
	}

	// limit=0 should default to 50.
	results, err := store.RecentObservations(ctx, "/project", 0)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(results) != 50 {
		t.Errorf("got %d, want 50 (default limit)", len(results))
	}
}

func TestRecentObservations_NoResults(t *testing.T) {
	store := newTestStore(t)
	results, err := store.RecentObservations(context.Background(), "/unknown-project", 10)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 for unknown project", len(results))
	}
}

func TestClose(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLiteStore(db)
	if err := store.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestFTS5SyncTriggers(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-fts", "/project")

	obs := &Observation{
		SessionID:   "sess-fts",
		Project:     "/project",
		Title:       "implemented quantum flux capacitor",
		Type:        TypeFeature,
		Text:        "built the core capacitor module with flux compensation",
		SourceFiles: []string{"flux.go"},
		ToolName:    "Write",
		CreatedAt:   time.Now(),
	}
	if err := store.InsertObservation(ctx, obs); err != nil {
		t.Fatalf("InsertObservation: %v", err)
	}

	// Search via FTS5 - should find by the unique term "capacitor"
	var count int
	err := store.db.QueryRow(
		"SELECT COUNT(*) FROM observations_fts WHERE observations_fts MATCH ?", "capacitor",
	).Scan(&count)
	if err != nil {
		t.Fatalf("FTS5 query: %v", err)
	}
	if count != 1 {
		t.Errorf("FTS5 match count = %d, want 1", count)
	}
}
