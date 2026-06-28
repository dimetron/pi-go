package palace

import (
	"context"
	"testing"
)

func TestKnowledgeGraphReachablePaths(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	kg := NewKnowledgeGraph(store)
	ctx := context.Background()

	// Add a triple, then add the identical triple again: the second call hits
	// the idempotent "return existing active triple" path.
	first, err := kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	again, err := kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	if err != nil {
		t.Fatalf("Add idempotent: %v", err)
	}
	if first.ID != again.ID {
		t.Errorf("idempotent Add returned different IDs: %s vs %s", first.ID, again.ID)
	}

	// Missing-field validation paths.
	if _, err := kg.Add(ctx, TripleInput{Subject: "x"}); err == nil {
		t.Error("Add with missing predicate/object should error")
	}
	if _, err := kg.Query(ctx, "", "", ""); err == nil {
		t.Error("Query with empty entity should error")
	}
	if err := kg.Invalidate(ctx, "", "", ""); err == nil {
		t.Error("Invalidate with empty fields should error")
	}
	if _, err := kg.Timeline(ctx, ""); err == nil {
		t.Error("Timeline with empty entity should error")
	}

	// Query across all direction + as-of branches.
	for _, dir := range []string{"subject", "object", "both", ""} {
		if _, err := kg.Query(ctx, "Alice", "", dir); err != nil {
			t.Errorf("Query dir=%q: %v", dir, err)
		}
	}
	if _, err := kg.Query(ctx, "Alice", "2030-01-01T00:00:00Z", "subject"); err != nil {
		t.Errorf("Query asOf: %v", err)
	}

	// Timeline + Stats (Stats exercises the predicate enumeration loop).
	if _, err := kg.Timeline(ctx, "Alice"); err != nil {
		t.Errorf("Timeline: %v", err)
	}
	stats, err := kg.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TripleCount == 0 || len(stats.Predicates) == 0 {
		t.Errorf("expected non-empty KG stats, got %+v", stats)
	}

	// Invalidate the active triple, then confirm timeline still returns it.
	if err := kg.Invalidate(ctx, "Alice", "works_on", "auth"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
}

func TestPalaceStatusWithData(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Add drawers across rooms/halls so Status walks a non-empty wing list and
	// ListRooms fetches halls.
	for _, d := range []DrawerInput{
		{Wing: "proj", Room: "auth", Hall: "hall_a", Content: "a", AddedBy: "t", Importance: 5},
		{Wing: "proj", Room: "billing", Hall: "hall_b", Content: "b", AddedBy: "t", Importance: 5},
	} {
		if _, err := p.AddDrawer(ctx, d); err != nil {
			t.Fatalf("AddDrawer: %v", err)
		}
	}

	st, err := p.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.DrawerCount != 2 {
		t.Errorf("DrawerCount = %d, want 2", st.DrawerCount)
	}
	if st.WingCount != 1 {
		t.Errorf("WingCount = %d, want 1", st.WingCount)
	}
	if st.RoomCount != 2 {
		t.Errorf("RoomCount = %d, want 2", st.RoomCount)
	}

	// ListRooms with halls populated.
	rooms, err := store.ListRooms(ctx, "proj")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 2 {
		t.Errorf("ListRooms = %d rooms, want 2", len(rooms))
	}
}
