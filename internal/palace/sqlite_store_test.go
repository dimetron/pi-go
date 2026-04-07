package palace

import (
	"context"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLitePalaceStore {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSQLitePalaceStore(db)
}

func makeDrawer(id, wing, room, content string) *Drawer {
	return &Drawer{
		ID:        id,
		Wing:      wing,
		Room:      room,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
}

func TestInsertGetDrawer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	d := makeDrawer("d1", "backend", "auth", "authentication logic")
	d.Hall = "decisions"
	d.Importance = 5

	if err := store.InsertDrawer(ctx, d); err != nil {
		t.Fatalf("InsertDrawer: %v", err)
	}

	got, err := store.GetDrawer(ctx, "d1")
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if got.Wing != "backend" || got.Room != "auth" || got.Content != "authentication logic" {
		t.Errorf("unexpected drawer: %+v", got)
	}
	if got.Hall != "decisions" {
		t.Errorf("hall = %q, want %q", got.Hall, "decisions")
	}
	if got.Importance != 5 {
		t.Errorf("importance = %d, want 5", got.Importance)
	}
}

func TestDeleteDrawer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	d := makeDrawer("d1", "backend", "auth", "to delete")
	_ = store.InsertDrawer(ctx, d)

	if err := store.DeleteDrawer(ctx, "d1"); err != nil {
		t.Fatalf("DeleteDrawer: %v", err)
	}

	_, err := store.GetDrawer(ctx, "d1")
	if err == nil {
		t.Error("expected error getting deleted drawer")
	}
}

func TestDeleteDrawerNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.DeleteDrawer(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent drawer")
	}
}

func TestListDrawersWithFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	drawers := []*Drawer{
		makeDrawer("d1", "backend", "auth", "auth stuff"),
		makeDrawer("d2", "backend", "db", "db stuff"),
		makeDrawer("d3", "frontend", "ui", "ui stuff"),
	}
	for _, d := range drawers {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("InsertDrawer: %v", err)
		}
	}

	// Filter by wing
	got, err := store.ListDrawers(ctx, DrawerFilter{Wing: "backend"})
	if err != nil {
		t.Fatalf("ListDrawers: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("backend drawers = %d, want 2", len(got))
	}

	// Filter by wing + room
	got, err = store.ListDrawers(ctx, DrawerFilter{Wing: "backend", Room: "auth"})
	if err != nil {
		t.Fatalf("ListDrawers: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("backend/auth drawers = %d, want 1", len(got))
	}

	// No filter
	got, err = store.ListDrawers(ctx, DrawerFilter{})
	if err != nil {
		t.Fatalf("ListDrawers: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("all drawers = %d, want 3", len(got))
	}
}

func TestCountDrawers(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	count, err := store.CountDrawers(ctx)
	if err != nil {
		t.Fatalf("CountDrawers: %v", err)
	}
	if count != 0 {
		t.Errorf("empty count = %d, want 0", count)
	}

	_ = store.InsertDrawer(ctx, makeDrawer("d1", "w", "r", "c1"))
	_ = store.InsertDrawer(ctx, makeDrawer("d2", "w", "r", "c2"))

	count, err = store.CountDrawers(ctx)
	if err != nil {
		t.Fatalf("CountDrawers: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestEmbeddingRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	embedding := []float32{0.1, 0.2, 0.3, -0.5, 1.0}
	d := makeDrawer("d1", "w", "r", "embedded content")
	d.Embedding = embedding

	if err := store.InsertDrawer(ctx, d); err != nil {
		t.Fatalf("InsertDrawer: %v", err)
	}

	row, err := store.GetEmbedding(ctx, "d1")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}

	if len(row.Embedding) != len(embedding) {
		t.Fatalf("embedding length = %d, want %d", len(row.Embedding), len(embedding))
	}
	for i, v := range row.Embedding {
		if v != embedding[i] {
			t.Errorf("embedding[%d] = %f, want %f", i, v, embedding[i])
		}
	}
}

func TestGetAllEmbeddings(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	d1 := makeDrawer("d1", "backend", "auth", "with embedding")
	d1.Embedding = []float32{0.1, 0.2}
	_ = store.InsertDrawer(ctx, d1)

	d2 := makeDrawer("d2", "backend", "db", "no embedding")
	_ = store.InsertDrawer(ctx, d2)

	d3 := makeDrawer("d3", "frontend", "ui", "also embedded")
	d3.Embedding = []float32{0.3, 0.4}
	_ = store.InsertDrawer(ctx, d3)

	// All embeddings
	rows, err := store.GetAllEmbeddings(ctx, DrawerFilter{})
	if err != nil {
		t.Fatalf("GetAllEmbeddings: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("total embeddings = %d, want 2", len(rows))
	}

	// Filtered by wing
	rows, err = store.GetAllEmbeddings(ctx, DrawerFilter{Wing: "backend"})
	if err != nil {
		t.Fatalf("GetAllEmbeddings: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("backend embeddings = %d, want 1", len(rows))
	}
}

func TestMarshalUnmarshalEmbedding(t *testing.T) {
	tests := []struct {
		name  string
		input []float32
	}{
		{"nil", nil},
		{"empty", []float32{}},
		{"single", []float32{1.0}},
		{"multiple", []float32{0.1, -0.5, 3.14, 0.0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob := MarshalEmbedding(tt.input)
			got := UnmarshalEmbedding(blob)

			if len(tt.input) == 0 {
				if got != nil {
					t.Errorf("expected nil for empty input, got %v", got)
				}
				return
			}

			if len(got) != len(tt.input) {
				t.Fatalf("length = %d, want %d", len(got), len(tt.input))
			}
			for i, v := range got {
				if v != tt.input[i] {
					t.Errorf("[%d] = %f, want %f", i, v, tt.input[i])
				}
			}
		})
	}
}

func TestGenerateDrawerID(t *testing.T) {
	// Deterministic: same inputs → same ID
	id1 := GenerateDrawerID("backend", "auth", "internal/auth/handler.go", 0, "")
	id2 := GenerateDrawerID("backend", "auth", "internal/auth/handler.go", 0, "")
	if id1 != id2 {
		t.Errorf("same inputs produced different IDs: %s vs %s", id1, id2)
	}

	// Different chunk → different ID
	id3 := GenerateDrawerID("backend", "auth", "internal/auth/handler.go", 1, "")
	if id1 == id3 {
		t.Error("different chunk index should produce different ID")
	}

	// No source file → uses content
	id4 := GenerateDrawerID("backend", "auth", "", 0, "some content")
	if id4 == "" {
		t.Error("generated empty ID for content-based drawer")
	}

	// Contains wing and room
	if got := id1; len(got) == 0 {
		t.Error("generated empty ID")
	}
}

func TestListWings(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_ = store.InsertDrawer(ctx, makeDrawer("d1", "backend", "auth", "c1"))
	_ = store.InsertDrawer(ctx, makeDrawer("d2", "backend", "db", "c2"))
	_ = store.InsertDrawer(ctx, makeDrawer("d3", "frontend", "ui", "c3"))

	wings, err := store.ListWings(ctx)
	if err != nil {
		t.Fatalf("ListWings: %v", err)
	}
	if len(wings) != 2 {
		t.Fatalf("wings = %d, want 2", len(wings))
	}

	// Wings are sorted alphabetically
	if wings[0].Wing != "backend" {
		t.Errorf("first wing = %q, want backend", wings[0].Wing)
	}
	if wings[0].DrawerCount != 2 {
		t.Errorf("backend drawers = %d, want 2", wings[0].DrawerCount)
	}
	if wings[0].RoomCount != 2 {
		t.Errorf("backend rooms = %d, want 2", wings[0].RoomCount)
	}
}

func TestListRooms(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	d1 := makeDrawer("d1", "backend", "auth", "c1")
	d1.Hall = "decisions"
	_ = store.InsertDrawer(ctx, d1)

	d2 := makeDrawer("d2", "backend", "auth", "c2")
	d2.Hall = "bugs"
	_ = store.InsertDrawer(ctx, d2)

	_ = store.InsertDrawer(ctx, makeDrawer("d3", "backend", "db", "c3"))

	rooms, err := store.ListRooms(ctx, "backend")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 2 {
		t.Fatalf("rooms = %d, want 2", len(rooms))
	}

	// auth room should have 2 drawers and 2 halls
	if rooms[0].Room != "auth" {
		t.Errorf("first room = %q, want auth", rooms[0].Room)
	}
	if rooms[0].DrawerCount != 2 {
		t.Errorf("auth drawers = %d, want 2", rooms[0].DrawerCount)
	}
	if len(rooms[0].Halls) != 2 {
		t.Errorf("auth halls = %d, want 2", len(rooms[0].Halls))
	}
}

func TestGetTaxonomy(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_ = store.InsertDrawer(ctx, makeDrawer("d1", "backend", "auth", "c1"))
	_ = store.InsertDrawer(ctx, makeDrawer("d2", "backend", "db", "c2"))
	_ = store.InsertDrawer(ctx, makeDrawer("d3", "frontend", "ui", "c3"))

	tax, err := store.GetTaxonomy(ctx)
	if err != nil {
		t.Fatalf("GetTaxonomy: %v", err)
	}
	if len(tax.Wings) != 2 {
		t.Fatalf("taxonomy wings = %d, want 2", len(tax.Wings))
	}
	if tax.Wings[0].Name != "backend" {
		t.Errorf("first wing = %q, want backend", tax.Wings[0].Name)
	}
	if len(tax.Wings[0].Rooms) != 2 {
		t.Errorf("backend rooms = %d, want 2", len(tax.Wings[0].Rooms))
	}
}

func TestGetTaxonomyEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tax, err := store.GetTaxonomy(ctx)
	if err != nil {
		t.Fatalf("GetTaxonomy: %v", err)
	}
	if len(tax.Wings) != 0 {
		t.Errorf("empty taxonomy wings = %d, want 0", len(tax.Wings))
	}
}

func TestEntityCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	e := &Entity{
		ID:        "alice",
		Name:      "Alice",
		Type:      "person",
		CreatedAt: time.Now().UTC(),
	}

	if err := store.InsertEntity(ctx, e); err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}

	got, err := store.GetEntity(ctx, "alice")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.Name != "Alice" || got.Type != "person" {
		t.Errorf("unexpected entity: %+v", got)
	}

	// Insert OR IGNORE: should not error on duplicate
	if err := store.InsertEntity(ctx, e); err != nil {
		t.Fatalf("duplicate InsertEntity: %v", err)
	}
}

func TestTripleCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = store.InsertEntity(ctx, &Entity{ID: "alice", Name: "Alice", CreatedAt: now})
	_ = store.InsertEntity(ctx, &Entity{ID: "auth", Name: "auth-migration", CreatedAt: now})

	triple := &Triple{
		ID:          "t1",
		SubjectID:   "alice",
		PredicateID: "works_on",
		ObjectID:    "auth",
		ExtractedAt: now,
	}
	if err := store.InsertTriple(ctx, triple); err != nil {
		t.Fatalf("InsertTriple: %v", err)
	}

	// Query by subject
	triples, err := store.QueryTriples(ctx, "alice", "", "")
	if err != nil {
		t.Fatalf("QueryTriples: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("triples = %d, want 1", len(triples))
	}
	if triples[0].PredicateID != "works_on" {
		t.Errorf("predicate = %q, want works_on", triples[0].PredicateID)
	}
}

func TestTripleInvalidation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = store.InsertEntity(ctx, &Entity{ID: "alice", Name: "Alice", CreatedAt: now})
	_ = store.InsertEntity(ctx, &Entity{ID: "auth", Name: "auth", CreatedAt: now})

	triple := &Triple{
		ID:          "t1",
		SubjectID:   "alice",
		PredicateID: "works_on",
		ObjectID:    "auth",
		ExtractedAt: now,
	}
	_ = store.InsertTriple(ctx, triple)

	if err := store.InvalidateTriple(ctx, "alice", "works_on", "auth"); err != nil {
		t.Fatalf("InvalidateTriple: %v", err)
	}

	// Should still be in timeline
	timeline, err := store.TimelineTriples(ctx, "alice")
	if err != nil {
		t.Fatalf("TimelineTriples: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("timeline = %d, want 1", len(timeline))
	}
	if timeline[0].ValidTo == nil {
		t.Error("expected valid_to to be set after invalidation")
	}
}

func TestKGStats(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = store.InsertEntity(ctx, &Entity{ID: "alice", Name: "Alice", CreatedAt: now})
	_ = store.InsertEntity(ctx, &Entity{ID: "auth", Name: "auth", CreatedAt: now})
	_ = store.InsertTriple(ctx, &Triple{
		ID: "t1", SubjectID: "alice", PredicateID: "works_on", ObjectID: "auth", ExtractedAt: now,
	})

	stats, err := store.KGStats(ctx)
	if err != nil {
		t.Fatalf("KGStats: %v", err)
	}
	if stats.EntityCount != 2 {
		t.Errorf("entities = %d, want 2", stats.EntityCount)
	}
	if stats.TripleCount != 1 {
		t.Errorf("triples = %d, want 1", stats.TripleCount)
	}
	if stats.ActiveTriples != 1 {
		t.Errorf("active = %d, want 1", stats.ActiveTriples)
	}
	if len(stats.Predicates) != 1 || stats.Predicates[0] != "works_on" {
		t.Errorf("predicates = %v, want [works_on]", stats.Predicates)
	}
}

func TestDiaryEntries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	entry := &DiaryEntry{
		Agent:     "pi",
		Entry:     "Learned about auth middleware today",
		Topic:     "auth",
		CreatedAt: time.Now().UTC(),
	}

	if err := store.InsertDiaryEntry(ctx, entry); err != nil {
		t.Fatalf("InsertDiaryEntry: %v", err)
	}
	if entry.ID == 0 {
		t.Error("expected non-zero ID after insert")
	}

	entries, err := store.ListDiaryEntries(ctx, "pi", 10)
	if err != nil {
		t.Fatalf("ListDiaryEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Entry != "Learned about auth middleware today" {
		t.Errorf("entry = %q, unexpected", entries[0].Entry)
	}

	// Different agent → empty results
	entries, err = store.ListDiaryEntries(ctx, "other", 10)
	if err != nil {
		t.Fatalf("ListDiaryEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("other agent entries = %d, want 0", len(entries))
	}
}

func TestFTS5TriggersFireOnInsertDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	d := makeDrawer("d1", "w", "r", "unique searchable content xyz789")
	_ = store.InsertDrawer(ctx, d)

	// FTS5 should find it
	var count int
	err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM drawers_fts WHERE drawers_fts MATCH ?",
		`"unique" "searchable"`).Scan(&count)
	if err != nil {
		t.Fatalf("FTS5 query: %v", err)
	}
	if count != 1 {
		t.Errorf("FTS5 matches after insert = %d, want 1", count)
	}

	// Delete → FTS5 should be updated
	_ = store.DeleteDrawer(ctx, "d1")
	err = store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM drawers_fts WHERE drawers_fts MATCH ?",
		`"unique" "searchable"`).Scan(&count)
	if err != nil {
		t.Fatalf("FTS5 query after delete: %v", err)
	}
	if count != 0 {
		t.Errorf("FTS5 matches after delete = %d, want 0", count)
	}
}
