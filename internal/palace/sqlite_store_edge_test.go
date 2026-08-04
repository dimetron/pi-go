package palace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenDBFileBackedAndReopen(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "nested", "palace.db")

	// First open creates the directory + file and runs all migrations.
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if !HasFTS5(db) {
		t.Error("expected FTS5 support on fresh db")
	}
	_ = db.Close()

	// Reopen: migrations are already applied, exercising the no-pending path.
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB reopen: %v", err)
	}
	_ = db2.Close()
}

func TestOpenDBBadPath(t *testing.T) {
	t.Parallel()
	// Create a regular file, then try to use it as a parent directory so
	// MkdirAll fails.
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDB(filepath.Join(file, "palace.db")); err == nil {
		t.Error("OpenDB on invalid path error = nil, want error")
	}
}

func TestParseFlexibleTimeBranches(t *testing.T) {
	t.Parallel()
	// RFC3339 branch.
	if _, err := parseFlexibleTime("2026-03-15T10:30:00Z"); err != nil {
		t.Errorf("RFC3339 parse error: %v", err)
	}
	// YYYY-MM-DD fallback branch.
	if _, err := parseFlexibleTime("2026-03-15"); err != nil {
		t.Errorf("date parse error: %v", err)
	}
	// Invalid input returns an error from the fallback branch.
	if _, err := parseFlexibleTime("nonsense"); err == nil {
		t.Error("parseFlexibleTime(nonsense) error = nil, want error")
	}
}

func TestParseJSONL(t *testing.T) {
	t.Parallel()

	// Missing file returns an error.
	if _, err := parseJSONL(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("parseJSONL on missing file error = nil, want error")
	}

	// A mix of valid pi-go (type) + Claude (role) lines, plus blank/malformed
	// lines and entries that must be skipped (empty content, unknown role).
	content := `
{"type":"user","content":"hello"}
not-json
{"role":"assistant","content":"hi there"}
{"role":"system","content":"ignored role"}
{"role":"user","content":""}
{"type":"user","content":"second question"}
{"role":"assistant","content":"second answer"}
`
	path := filepath.Join(t.TempDir(), "convo.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	exchanges, err := parseJSONL(path)
	if err != nil {
		t.Fatalf("parseJSONL: %v", err)
	}
	if len(exchanges) == 0 {
		t.Error("expected at least one exchange pair from valid lines")
	}
}

func TestPalaceNewAndLifecycle(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "palace.db")

	// ModelPath points at a nonexistent file: os.Stat fails so the embedder is
	// skipped, exercising that branch of New without needing a real model.
	// WithLocalEmbedder pins the backend: without it the result would depend on
	// whether an Ollama daemon happens to be running on the machine.
	p, err := New(
		WithDBPath(dbPath),
		WithModelPath(filepath.Join(t.TempDir(), "missing.onnx")),
		WithLocalEmbedder(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	st, err := p.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.ModelLoaded {
		t.Error("ModelLoaded = true, want false when no embedder is loaded")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStoreEdgeCases(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed drawers across wings/rooms/halls, one with an embedding.
	drawers := []*Drawer{
		{ID: "d1", Wing: "backend", Room: "auth", Hall: "jwt", Content: "JWT validation logic", AddedBy: "test", Embedding: []float32{0.1, 0.2, 0.3}, CreatedAt: now},
		{ID: "d2", Wing: "backend", Room: "auth", Hall: "", Content: "session handling", AddedBy: "test", CreatedAt: now},
		{ID: "d3", Wing: "frontend", Room: "ui", Hall: "", Content: "button component", AddedBy: "test", CreatedAt: now},
	}
	for _, d := range drawers {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("InsertDrawer(%s): %v", d.ID, err)
		}
	}

	t.Run("CountDrawers", func(t *testing.T) {
		n, err := store.CountDrawers(ctx)
		if err != nil {
			t.Fatalf("CountDrawers: %v", err)
		}
		if n != 3 {
			t.Errorf("CountDrawers = %d, want 3", n)
		}
	})

	t.Run("GetEmbedding found and missing", func(t *testing.T) {
		row, err := store.GetEmbedding(ctx, "d1")
		if err != nil {
			t.Fatalf("GetEmbedding(d1): %v", err)
		}
		if len(row.Embedding) != 3 {
			t.Errorf("embedding len = %d, want 3", len(row.Embedding))
		}
		if _, err := store.GetEmbedding(ctx, "missing"); err == nil {
			t.Error("GetEmbedding(missing) error = nil, want not-found")
		}
	})

	t.Run("GetDrawer missing", func(t *testing.T) {
		if _, err := store.GetDrawer(ctx, "missing"); err == nil {
			t.Error("GetDrawer(missing) error = nil, want not-found")
		}
	})

	t.Run("ListWings", func(t *testing.T) {
		wings, err := store.ListWings(ctx)
		if err != nil {
			t.Fatalf("ListWings: %v", err)
		}
		if len(wings) != 2 {
			t.Errorf("ListWings len = %d, want 2", len(wings))
		}
	})

	t.Run("ListRooms with halls", func(t *testing.T) {
		rooms, err := store.ListRooms(ctx, "backend")
		if err != nil {
			t.Fatalf("ListRooms: %v", err)
		}
		if len(rooms) != 1 || rooms[0].Room != "auth" {
			t.Fatalf("ListRooms = %+v, want one 'auth' room", rooms)
		}
		// d1 has hall "jwt" which should surface in the halls list.
		var foundHall bool
		for _, h := range rooms[0].Halls {
			if h == "jwt" {
				foundHall = true
			}
		}
		if !foundHall {
			t.Errorf("rooms[0].Halls = %v, want to contain 'jwt'", rooms[0].Halls)
		}
	})

	t.Run("GetTaxonomy", func(t *testing.T) {
		tax, err := store.GetTaxonomy(ctx)
		if err != nil {
			t.Fatalf("GetTaxonomy: %v", err)
		}
		if len(tax.Wings) != 2 {
			t.Errorf("taxonomy wings = %d, want 2", len(tax.Wings))
		}
	})
}

func TestStoreKnowledgeGraphEdges(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	entities := []*Entity{
		{ID: "alice", Name: "Alice", Type: "person", CreatedAt: now},
		{ID: "auth", Name: "auth", Type: "module", CreatedAt: now},
	}
	for _, e := range entities {
		if err := store.InsertEntity(ctx, e); err != nil {
			t.Fatalf("InsertEntity(%s): %v", e.ID, err)
		}
	}

	t.Run("GetEntity found and missing", func(t *testing.T) {
		e, err := store.GetEntity(ctx, "alice")
		if err != nil {
			t.Fatalf("GetEntity(alice): %v", err)
		}
		if e.Name != "Alice" {
			t.Errorf("entity name = %q, want Alice", e.Name)
		}
		if _, err := store.GetEntity(ctx, "ghost"); err == nil {
			t.Error("GetEntity(ghost) error = nil, want not-found")
		}
	})

	validFrom := now.Add(-time.Hour)
	triples := []*Triple{
		{ID: "t1", SubjectID: "alice", PredicateID: "works_on", ObjectID: "auth", ValidFrom: &validFrom, ExtractedAt: now},
		{ID: "t2", SubjectID: "auth", PredicateID: "owned_by", ObjectID: "alice", ExtractedAt: now},
	}
	for _, tr := range triples {
		if err := store.InsertTriple(ctx, tr); err != nil {
			t.Fatalf("InsertTriple(%s): %v", tr.ID, err)
		}
	}

	t.Run("QueryTriples directions and asOf", func(t *testing.T) {
		for _, dir := range []string{"subject", "object", "both", ""} {
			got, err := store.QueryTriples(ctx, "alice", "", dir)
			if err != nil {
				t.Fatalf("QueryTriples(dir=%q): %v", dir, err)
			}
			if len(got) == 0 {
				t.Errorf("QueryTriples(dir=%q) returned no triples", dir)
			}
		}
		asOf := now.Format(time.RFC3339)
		if _, err := store.QueryTriples(ctx, "alice", asOf, "subject"); err != nil {
			t.Fatalf("QueryTriples(asOf): %v", err)
		}
	})

	t.Run("TimelineTriples", func(t *testing.T) {
		got, err := store.TimelineTriples(ctx, "alice")
		if err != nil {
			t.Fatalf("TimelineTriples: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("TimelineTriples = %d, want 2", len(got))
		}
	})

	t.Run("InvalidateTriple then KGStats", func(t *testing.T) {
		if err := store.InvalidateTriple(ctx, "alice", "works_on", "auth"); err != nil {
			t.Fatalf("InvalidateTriple: %v", err)
		}
		stats, err := store.KGStats(ctx)
		if err != nil {
			t.Fatalf("KGStats: %v", err)
		}
		if stats.EntityCount != 2 {
			t.Errorf("EntityCount = %d, want 2", stats.EntityCount)
		}
		if stats.TripleCount != 2 {
			t.Errorf("TripleCount = %d, want 2", stats.TripleCount)
		}
		if stats.ActiveTriples != 1 {
			t.Errorf("ActiveTriples = %d, want 1 after invalidation", stats.ActiveTriples)
		}
		if len(stats.Predicates) == 0 {
			t.Error("expected at least one predicate in stats")
		}
	})
}

func TestStoreDiaryEntries(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		if err := store.InsertDiaryEntry(ctx, &DiaryEntry{
			Agent:     "pi",
			Entry:     strings.Repeat("note ", i+1),
			Topic:     "progress",
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("InsertDiaryEntry: %v", err)
		}
	}

	// Default limit path (limit <= 0).
	entries, err := store.ListDiaryEntries(ctx, "pi", 0)
	if err != nil {
		t.Fatalf("ListDiaryEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("ListDiaryEntries = %d, want 3", len(entries))
	}

	// Unknown agent returns no entries.
	none, err := store.ListDiaryEntries(ctx, "ghost", 5)
	if err != nil {
		t.Fatalf("ListDiaryEntries(ghost): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("ListDiaryEntries(ghost) = %d, want 0", len(none))
	}
}
