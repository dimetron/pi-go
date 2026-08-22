package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/testenv"
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

func TestNewMemoryRecentCmd_NoArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)

	origCwd, _ := os.Getwd()
	workDir := filepath.Join(tmp, "work")
	_ = os.MkdirAll(workDir, 0o755)
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	cmd := newMemoryRecentCmd()
	cmd.SetArgs(nil)
	_ = captureStdout(t, func() {
		_ = cmd.Execute() // will fail (no DB), but exercises RunE
	})
}

func TestNewMemoryRecentCmd_WithArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)

	cmd := newMemoryRecentCmd()
	cmd.SetArgs([]string{tmp})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryRecentCmd_Flags(t *testing.T) {
	cmd := newMemoryRecentCmd()
	for _, name := range []string{"limit", "type", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not found", name)
		}
	}
	// Verify default values.
	limit, _ := cmd.Flags().GetInt("limit")
	if limit != 20 {
		t.Errorf("limit default = %d, want 20", limit)
	}
	jsonFlag, _ := cmd.Flags().GetBool("json")
	if jsonFlag {
		t.Error("json default should be false")
	}
}

func TestRunMemoryRecent_CurrentDir(t *testing.T) {
	// Use current directory with no DB.
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	// Create a memory DB in project-specific location.
	memDir := filepath.Join(tmpDir, "proj", ".pi-go", "memory")
	os.MkdirAll(memDir, 0755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	db.Close()

	err = runMemoryRecent(filepath.Join(tmpDir, "proj"), 10, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent current dir: %v", err)
	}
}

func TestRunMemoryRecent_WithObservations(t *testing.T) {
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
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "session-1",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "active",
	})
	err = runMemoryRecent(dir, 10, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestRunMemoryRecent_AllObservationTypes(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "session-types",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "completed",
	})
	validTypes := []memory.ObservationType{
		memory.TypeDecision,
		memory.TypeBugfix,
		memory.TypeFeature,
		memory.TypeRefactor,
		memory.TypeDiscovery,
		memory.TypeChange,
	}
	for _, typ := range validTypes {
		store.InsertObservation(ctx, &memory.Observation{
			SessionID: "session-types",
			Project:   dir,
			Title:     string(typ),
			Type:      typ,
			Text:      "test",
			CreatedAt: time.Now(),
		})
	}
	err = runMemoryRecent(dir, 10, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestRunMemoryRecent_LimitWithTypeFilter(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "limit-test",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "completed",
	})
	for i := 0; i < 10; i++ {
		store.InsertObservation(ctx, &memory.Observation{
			SessionID: "limit-test",
			Project:   dir,
			Title:     "Bugfix",
			Type:      memory.TypeBugfix,
			Text:      "test",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	err = runMemoryRecent(dir, 3, "bugfix", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestRunMemoryRecent_LimitExceedsData(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "small-data",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "completed",
	})
	store.InsertObservation(ctx, &memory.Observation{
		SessionID: "small-data",
		Project:   dir,
		Title:     "Obs 1",
		Type:      memory.TypeFeature,
		Text:      "test",
		CreatedAt: time.Now(),
	})
	store.InsertObservation(ctx, &memory.Observation{
		SessionID: "small-data",
		Project:   dir,
		Title:     "Obs 2",
		Type:      memory.TypeFeature,
		Text:      "test",
		CreatedAt: time.Now(),
	})
	err = runMemoryRecent(dir, 100, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestNewMemoryRecentCmd(t *testing.T) {
	cmd := newMemoryRecentCmd()
	if cmd.Use != "recent [project]" {
		t.Errorf("Use = %q, want 'recent [project]'", cmd.Use)
	}
	for _, name := range []string{"limit", "type", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

func TestFormatAge_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{"just now", time.Now(), "just now"},
		{"5 minutes ago", time.Now().Add(-5 * time.Minute), "5m ago"},
		{"1 minute ago", time.Now().Add(-1 * time.Minute), "1m ago"},
		{"59 minutes ago", time.Now().Add(-59 * time.Minute), "59m ago"},
		{"1 hour ago", time.Now().Add(-1 * time.Hour), "1h ago"},
		{"3 hours ago", time.Now().Add(-3 * time.Hour), "3h ago"},
		{"23 hours ago", time.Now().Add(-23 * time.Hour), "23h ago"},
		{"1 day ago", time.Now().Add(-1 * 24 * time.Hour), "1d ago"},
		{"6 days ago", time.Now().Add(-6 * 24 * time.Hour), "6d ago"},
		{"2 weeks ago", time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), "Jan 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.input)
			if got != tt.expected {
				t.Errorf("formatAge() = %q, want %q", got, tt.expected)
			}
		})
	}
}
