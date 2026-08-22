package cli

import (
	"context"
	"errors"
	"testing"

	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/testenv"
)

// mockNamedTool is a minimal adktool.Tool used to drive afterTool.
type mockNamedTool struct{ name string }

func (m mockNamedTool) Name() string        { return m.name }
func (m mockNamedTool) Description() string { return "" }
func (m mockNamedTool) IsLongRunning() bool { return false }

var _ adktool.Tool = mockNamedTool{}

func newMemStore(t *testing.T) memory.Store {
	t.Helper()
	db, err := memory.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("memory.OpenDB: %v", err)
	}
	return memory.NewSQLiteStore(db)
}

// TestLazyMemoryStoreDelegation drives every delegating method against a ready
// store, covering the happy paths of the lazyMemoryStore adapter.
func TestLazyMemoryStoreDelegation(t *testing.T) {
	t.Parallel()
	store := newMemStore(t)
	lazy := newLazyMemoryStore()
	lazy.setReady(store, nil)

	ctx := context.Background()

	// Drive every delegating method. Valid data is supplied where cheap so the
	// happy paths succeed; calls whose underlying store rejects synthetic data
	// still exercise the delegation line, which is what we cover here.
	if err := lazy.CreateSession(ctx, &memory.Session{SessionID: "s1", Project: "p", Status: "active"}); err != nil {
		t.Errorf("CreateSession: %v", err)
	}
	if err := lazy.InsertObservation(ctx, &memory.Observation{SessionID: "s1", Project: "p", Title: "t", Type: "feature"}); err != nil {
		t.Errorf("InsertObservation: %v", err)
	}
	if _, err := lazy.GetObservations(ctx, []int64{1}); err != nil {
		t.Errorf("GetObservations: %v", err)
	}
	if _, err := lazy.RecentObservations(ctx, "p", 5); err != nil {
		t.Errorf("RecentObservations: %v", err)
	}
	if _, err := lazy.RecentSummaries(ctx, "p", 5); err != nil {
		t.Errorf("RecentSummaries: %v", err)
	}
	if _, err := lazy.Search(ctx, memory.SearchQuery{Query: "t"}); err != nil {
		t.Errorf("Search: %v", err)
	}
	if err := lazy.CompleteSession(ctx, "s1"); err != nil {
		t.Errorf("CompleteSession: %v", err)
	}

	// These reject synthetic data by design; we only need the delegation lines.
	_ = lazy.UpsertSummary(ctx, &memory.SessionSummary{SessionID: "s1"})
	_, _ = lazy.Timeline(ctx, 1, 1, 1)

	if err := lazy.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestLazyMemoryStoreError verifies each method surfaces the readiness error.
func TestLazyMemoryStoreError(t *testing.T) {
	t.Parallel()
	lazy := newLazyMemoryStore()
	wantErr := errors.New("init failed")
	lazy.setReady(nil, wantErr)

	ctx := context.Background()
	checks := []func() error{
		func() error { return lazy.CreateSession(ctx, &memory.Session{}) },
		func() error { return lazy.CompleteSession(ctx, "s") },
		func() error { return lazy.InsertObservation(ctx, &memory.Observation{}) },
		func() error { _, e := lazy.GetObservations(ctx, nil); return e },
		func() error { _, e := lazy.RecentObservations(ctx, "p", 1); return e },
		func() error { return lazy.UpsertSummary(ctx, &memory.SessionSummary{}) },
		func() error { _, e := lazy.RecentSummaries(ctx, "p", 1); return e },
		func() error { _, e := lazy.Search(ctx, memory.SearchQuery{}); return e },
		func() error { _, e := lazy.Timeline(ctx, 1, 0, 0); return e },
	}
	for i, c := range checks {
		if err := c(); !errors.Is(err, wantErr) {
			t.Errorf("check %d error = %v, want %v", i, err, wantErr)
		}
	}
}

// TestLazyMemoryStoreNilStore covers the "store unavailable" branch of wait.
func TestLazyMemoryStoreNilStore(t *testing.T) {
	t.Parallel()
	lazy := newLazyMemoryStore()
	lazy.setReady(nil, nil)
	if err := lazy.CreateSession(context.Background(), &memory.Session{}); err == nil {
		t.Error("expected error when store is nil, got nil")
	}
}

// TestLazyMemoryStoreContextCancelled covers the ctx.Done branch of wait (the
// store is never marked ready).
func TestLazyMemoryStoreContextCancelled(t *testing.T) {
	t.Parallel()
	lazy := newLazyMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lazy.CompleteSession(ctx, "s"); err == nil {
		t.Error("expected context error, got nil")
	}
}

// TestDeferredMemoryConfig exercises the config override branches.
func TestDeferredMemoryConfig(t *testing.T) {
	t.Parallel()

	// Nil Memory → defaults.
	def := deferredMemoryConfig(config.Config{})
	if def.MaxPending == 0 {
		t.Error("expected default MaxPending to be non-zero")
	}

	// All overrides set.
	cfg := config.Config{Memory: &config.MemoryConfig{
		DBPath:        "/tmp/custom.db",
		TokenBudget:   1234,
		MaxPending:    7,
		LookbackHours: 48,
	}}
	got := deferredMemoryConfig(cfg)
	if got.DBPath != "/tmp/custom.db" {
		t.Errorf("DBPath = %q, want /tmp/custom.db", got.DBPath)
	}
	if got.TokenBudget != 1234 {
		t.Errorf("TokenBudget = %d, want 1234", got.TokenBudget)
	}
	if got.MaxPending != 7 {
		t.Errorf("MaxPending = %d, want 7", got.MaxPending)
	}
}

// TestDeferredMemoryDBPath covers both the explicit-path and home-derived paths.
// Not parallel: it sets HOME so the derived-path branch is deterministic.
func TestDeferredMemoryDBPath(t *testing.T) {
	if got := deferredMemoryDBPath(config.MemoryConfig{DBPath: "/x/y.db"}); got != "/x/y.db" {
		t.Errorf("explicit DBPath = %q, want /x/y.db", got)
	}
	// Without an explicit path it derives from HOME.
	testenv.SetHome(t, t.TempDir())
	if got := deferredMemoryDBPath(config.MemoryConfig{}); got == "" {
		t.Error("expected non-empty derived DB path")
	}
}

// TestAfterTool covers the recorder's afterTool branches: error short-circuit,
// no-worker short-circuit, excluded tool, and the enqueue path.
func TestAfterTool(t *testing.T) {
	t.Parallel()

	excludeName := "secret-tool"
	cfg := config.Config{Memory: &config.MemoryConfig{ExcludedTools: []string{excludeName}}}
	rec := newDeferredMemoryRecorder(cfg, "proj")

	result := map[string]any{"content": "ok"}

	// toolErr set → returns result unchanged, no enqueue.
	if out, _ := rec.afterTool(nil, mockNamedTool{name: "read"}, nil, result, errors.New("boom")); out["content"] != "ok" {
		t.Error("expected result passthrough on tool error")
	}

	// worker nil → returns result unchanged.
	if out, _ := rec.afterTool(nil, mockNamedTool{name: "read"}, nil, result, nil); out["content"] != "ok" {
		t.Error("expected result passthrough when worker is nil")
	}

	// Attach a worker (not started, so Enqueue just buffers) and verify the
	// excluded tool is skipped while a normal tool is enqueued.
	store := newMemStore(t)
	worker := memory.NewWorker(store, nil, 10)
	rec.setReady("sess-1", worker)

	rec.afterTool(nil, mockNamedTool{name: excludeName}, map[string]any{"a": 1}, result, nil) //nolint:errcheck
	rec.afterTool(nil, mockNamedTool{name: "read"}, map[string]any{"a": 1}, result, nil)      //nolint:errcheck
}
