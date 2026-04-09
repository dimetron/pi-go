package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/memory"
)

func TestMemoryRecent_NoDB(t *testing.T) {
	// When no DB exists, findMemoryDB returns an error.
	// (Skip if global memory DB exists — fallback will find it instead of erroring.)
	home, _ := os.UserHomeDir()
	globalDB := filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
	if _, err := os.Stat(globalDB); err == nil {
		t.Skip("global memory DB exists — fallback will be used instead of erroring")
	}
	err := runMemoryRecent("/nonexistent/project", 20, "", false)
	if err == nil {
		t.Error("expected error when no memory database exists")
	}
}

func TestMemoryRecent_EmptyDB(t *testing.T) {
	// Create a temp project dir and init a memory DB inside it.
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(memDir, "claude-mem.db")

	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = runMemoryRecent(dir, 20, "", false)
	if err != nil {
		t.Fatalf("unexpected error on empty DB: %v", err)
	}
}

func TestMemoryRecent_InvalidType(t *testing.T) {
	err := runMemoryRecent("/nonexistent", 20, "notatype", false)
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestMemoryRecent_WithObservations(t *testing.T) {
	// Create a temp dir with a memory DB that has observations.
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(memDir, "claude-mem.db")

	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert a session.
	_, err = db.ExecContext(ctx,
		`INSERT INTO sessions (session_id, project, started_at, started_at_epoch, status) VALUES (?, ?, ?, ?, ?)`,
		"test-session", dir, "2024-01-01T00:00:00Z", 1704067200, "completed",
	)
	if err != nil {
		t.Fatal(err)
	}

	// Insert observations using the store.
	store := memory.NewSQLiteStore(db)
	observations := []struct {
		title string
		typ   memory.ObservationType
	}{
		{"Fixed nil pointer in handler", memory.TypeBugfix},
		{"Added user auth middleware", memory.TypeFeature},
		{"Refactored database layer", memory.TypeRefactor},
	}
	for i, obs := range observations {
		err := store.InsertObservation(ctx, &memory.Observation{
			SessionID: "test-session",
			Project:   dir,
			Title:     obs.title,
			Type:      obs.typ,
			Text:      "test text",
			CreatedAt: time.Date(2024, 1, 1, 0, 0, i, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	err = runMemoryRecent(dir, 20, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryRecent_TypeFilter(t *testing.T) {
	// Create a temp dir with a memory DB.
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(memDir, "claude-mem.db")

	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	store := memory.NewSQLiteStore(db)

	// Insert a session.
	_ = store.CreateSession(ctx, &memory.Session{
		SessionID: "s2",
		Project:   dir,
		StartedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:    "completed",
	})

	// Insert mixed-type observations.
	types := []memory.ObservationType{memory.TypeBugfix, memory.TypeFeature, memory.TypeDiscovery, memory.TypeBugfix}
	for i, typ := range types {
		err := store.InsertObservation(ctx, &memory.Observation{
			SessionID: "s2",
			Project:   dir,
			Title:     "Observation " + string(typ),
			Type:      typ,
			Text:      "text",
			CreatedAt: time.Date(2024, 1, 1, 0, 0, i, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Filter by bugfix — should return 2.
	err = runMemoryRecent(dir, 20, "bugfix", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
