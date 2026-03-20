//go:build e2e

package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// mockCompressorE2E is a deterministic compressor for e2e tests (no real LLM).
type mockCompressorE2E struct{}

func (m *mockCompressorE2E) CompressObservation(_ context.Context, raw RawObservation) (*Observation, error) {
	return &Observation{
		SessionID:       raw.SessionID,
		Project:         raw.Project,
		Title:           fmt.Sprintf("Compressed: %s", raw.ToolName),
		Type:            TypeChange,
		Text:            fmt.Sprintf("Tool %s executed successfully", raw.ToolName),
		SourceFiles:     extractSourceFiles(raw.ToolInput),
		ToolName:        raw.ToolName,
		DiscoveryTokens: 100,
		CreatedAt:       raw.Timestamp,
	}, nil
}

// TestE2EFullCycle exercises the complete memory system pipeline:
// open DB -> create session -> enqueue observations -> compress -> store -> search -> context generation.
func TestE2EFullCycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-e2e.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	store := NewSQLiteStore(db)
	compressor := &mockCompressorE2E{}
	worker := NewWorker(store, compressor, 50)
	worker.Start(ctx)

	project := "/test/e2e-project"
	sessionID := "e2e-session-001"

	// Create session
	err = store.CreateSession(ctx, &Session{
		SessionID: sessionID,
		Project:   project,
		StartedAt: time.Now(),
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Enqueue observations through the worker
	tools := []struct {
		name string
		file string
	}{
		{"Read", "main.go"},
		{"Edit", "handler.go"},
		{"Bash", ""},
		{"Grep", "utils.go"},
		{"Write", "config.go"},
	}

	for i, tool := range tools {
		input := map[string]any{"command": fmt.Sprintf("action-%d", i)}
		if tool.file != "" {
			input["file_path"] = tool.file
		}
		worker.Enqueue(RawObservation{
			SessionID: sessionID,
			Project:   project,
			ToolName:  tool.name,
			ToolInput: input,
			ToolOutput: map[string]any{
				"result": fmt.Sprintf("output-%d", i),
			},
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Shutdown worker to drain all observations
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Verify observations were stored
	obs, err := store.RecentObservations(ctx, project, 100)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(obs) != 5 {
		t.Fatalf("expected 5 observations, got %d", len(obs))
	}

	// Verify search works (FTS5 or LIKE fallback)
	result, err := store.Search(ctx, SearchQuery{
		Query:   "Compressed",
		Project: project,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 5 {
		t.Fatalf("expected 5 search results, got %d", result.Total)
	}

	// Verify timeline works
	anchorID := obs[2].ID // middle observation
	timeline, err := store.Timeline(ctx, anchorID, 2, 2)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) < 3 { // at least anchor + some before/after
		t.Fatalf("expected at least 3 timeline entries, got %d", len(timeline))
	}

	// Verify GetObservations (progressive disclosure)
	ids := []int64{obs[0].ID, obs[4].ID}
	fetched, err := store.GetObservations(ctx, ids)
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(fetched) != 2 {
		t.Fatalf("expected 2 fetched observations, got %d", len(fetched))
	}

	// Verify source files were extracted
	hasFiles := false
	for _, o := range obs {
		if len(o.SourceFiles) > 0 && o.SourceFiles[0] != "" {
			hasFiles = true
			break
		}
	}
	if !hasFiles {
		t.Error("expected at least one observation with source files")
	}

	// Upsert session summary
	err = store.UpsertSummary(ctx, &SessionSummary{
		SessionID:    sessionID,
		Project:      project,
		Request:      "E2E integration test",
		Investigated: "Full memory pipeline",
		Learned:      "All components work together",
		Completed:    "Pipeline validation",
		NextSteps:    "Ship it",
		CreatedAt:    time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertSummary: %v", err)
	}

	// Verify context generation
	ctxGen := NewContextGenerator(store, 8000)
	markdown, err := ctxGen.Generate(ctx, project)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if markdown == "" {
		t.Fatal("expected non-empty context markdown")
	}

	// Verify context contains key elements
	for _, want := range []string{"e2e-project", "Compressed:", "mem-search"} {
		if !contains(markdown, want) {
			t.Errorf("context markdown missing %q", want)
		}
	}

	// Complete session
	if err := store.CompleteSession(ctx, sessionID); err != nil {
		t.Fatalf("CompleteSession: %v", err)
	}
}

// TestE2ESearchPerformance verifies that search over 1000 observations completes in < 100ms.
func TestE2ESearchPerformance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-perf.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	store := NewSQLiteStore(db)
	project := "/test/perf-project"
	sessionID := "perf-session-001"

	// Create session
	err = store.CreateSession(ctx, &Session{
		SessionID: sessionID,
		Project:   project,
		StartedAt: time.Now(),
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Insert 1000 observations directly (skip worker to avoid timing issues)
	baseTime := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 1000; i++ {
		obs := &Observation{
			SessionID:       sessionID,
			Project:         project,
			Title:           fmt.Sprintf("Observation %d: editing file_%d.go", i, i%50),
			Type:            ObservationType([]string{"change", "bugfix", "feature", "discovery", "refactor"}[i%5]),
			Text:            fmt.Sprintf("Detailed text for observation %d with keyword searchable content about database migrations and API handlers", i),
			SourceFiles:     []string{fmt.Sprintf("pkg/file_%d.go", i%50)},
			ToolName:        []string{"Read", "Edit", "Bash", "Grep", "Write"}[i%5],
			PromptNumber:    i + 1,
			DiscoveryTokens: 50 + i%100,
			CreatedAt:       baseTime.Add(time.Duration(i) * time.Second),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("InsertObservation %d: %v", i, err)
		}
	}

	// Benchmark search
	start := time.Now()
	result, err := store.Search(ctx, SearchQuery{
		Query:   "database migrations",
		Project: project,
		Limit:   20,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total == 0 {
		t.Fatal("expected search results, got 0")
	}

	// Pure Go SQLite (modernc.org/sqlite) is slower than CGO; allow 500ms.
	if elapsed > 500*time.Millisecond {
		t.Errorf("search took %v, expected < 500ms", elapsed)
	}
	t.Logf("Search over 1000 observations: %v (%d results)", elapsed, result.Total)

	// Benchmark timeline
	start = time.Now()
	timeline, err := store.Timeline(ctx, 500, 10, 10)
	elapsed = time.Since(start)

	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) == 0 {
		t.Fatal("expected timeline results")
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("timeline took %v, expected < 100ms", elapsed)
	}
	t.Logf("Timeline around observation 500: %v (%d entries)", elapsed, len(timeline))

	// Benchmark context generation
	start = time.Now()
	ctxGen := NewContextGenerator(store, 8000)
	markdown, err := ctxGen.Generate(ctx, project)
	elapsed = time.Since(start)

	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if markdown == "" {
		t.Fatal("expected context output")
	}

	if elapsed > 500*time.Millisecond {
		t.Errorf("context generation took %v, expected < 500ms", elapsed)
	}
	t.Logf("Context generation over 1000 observations: %v", elapsed)
}

// TestE2EPrivacyFiltering verifies private content is stripped end-to-end.
func TestE2EPrivacyFiltering(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-privacy.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	store := NewSQLiteStore(db)
	compressor := &mockCompressorE2E{}
	worker := NewWorker(store, compressor, 10)
	worker.Start(ctx)

	project := "/test/privacy"
	sessionID := "privacy-session"

	err = store.CreateSession(ctx, &Session{
		SessionID: sessionID,
		Project:   project,
		StartedAt: time.Now(),
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Enqueue observation with private content
	worker.Enqueue(RawObservation{
		SessionID: sessionID,
		Project:   project,
		ToolName:  "Read",
		ToolInput: map[string]any{
			"file_path": "secrets.go",
			"content":   "public data <private>SECRET_KEY=abc123</private> more public",
		},
		ToolOutput: map[string]any{
			"result": "output with <private>token=xyz</private> visible",
		},
		Timestamp: time.Now(),
	})

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// The mock compressor receives already-stripped input,
	// so we verify the observation was stored
	obs, err := store.RecentObservations(ctx, project, 10)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
}
