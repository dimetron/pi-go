package palace

import (
	"context"
	"testing"
	"time"
)

func setupKG(t *testing.T) (*KnowledgeGraph, PalaceStore) {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	t.Cleanup(func() { store.Close() })
	return NewKnowledgeGraph(store), store
}

func TestKG_Add_BasicTriple(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	triple, err := kg.Add(ctx, TripleInput{
		Subject:   "Alice",
		Predicate: "works_on",
		Object:    "auth-migration",
	})
	if err != nil {
		t.Fatalf("add triple: %v", err)
	}

	if triple.SubjectID != "alice" {
		t.Errorf("subject_id = %q, want %q", triple.SubjectID, "alice")
	}
	if triple.PredicateID != "works_on" {
		t.Errorf("predicate_id = %q, want %q", triple.PredicateID, "works_on")
	}
	if triple.ObjectID != "auth-migration" {
		t.Errorf("object_id = %q, want %q", triple.ObjectID, "auth-migration")
	}
	if triple.ValidTo != nil {
		t.Error("new triple should have nil valid_to")
	}
}

func TestKG_Add_AutoCreatesEntities(t *testing.T) {
	kg, store := setupKG(t)
	ctx := context.Background()

	_, err := kg.Add(ctx, TripleInput{
		Subject:   "Bob",
		Predicate: "owns",
		Object:    "billing-service",
	})
	if err != nil {
		t.Fatalf("add triple: %v", err)
	}

	// Verify entities were created
	subj, err := store.GetEntity(ctx, "bob")
	if err != nil {
		t.Fatalf("get subject entity: %v", err)
	}
	if subj.Name != "Bob" {
		t.Errorf("subject name = %q, want %q", subj.Name, "Bob")
	}

	obj, err := store.GetEntity(ctx, "billing-service")
	if err != nil {
		t.Fatalf("get object entity: %v", err)
	}
	if obj.Name != "billing-service" {
		t.Errorf("object name = %q, want %q", obj.Name, "billing-service")
	}
}

func TestKG_Add_DuplicateIsIdempotent(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	first, err := kg.Add(ctx, TripleInput{
		Subject:   "Alice",
		Predicate: "works_on",
		Object:    "auth-migration",
	})
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	second, err := kg.Add(ctx, TripleInput{
		Subject:   "Alice",
		Predicate: "works_on",
		Object:    "auth-migration",
	})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("duplicate add returned different triple: %q vs %q", first.ID, second.ID)
	}
}

func TestKG_Add_ValidationErrors(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	tests := []TripleInput{
		{Subject: "", Predicate: "works_on", Object: "x"},
		{Subject: "x", Predicate: "", Object: "x"},
		{Subject: "x", Predicate: "works_on", Object: ""},
	}
	for _, input := range tests {
		_, err := kg.Add(ctx, input)
		if err == nil {
			t.Errorf("expected error for input %+v", input)
		}
	}
}

func TestKG_Query_ReturnsRelatedTriples(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	// Alice works_on auth, Alice works_on billing
	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "billing"})
	kg.Add(ctx, TripleInput{Subject: "Bob", Predicate: "works_on", Object: "infra"})

	triples, err := kg.Query(ctx, "Alice", "", "")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(triples) != 2 {
		t.Fatalf("got %d triples, want 2", len(triples))
	}
}

func TestKG_Query_DirectionFilter(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "manages", Object: "Bob"})
	kg.Add(ctx, TripleInput{Subject: "Carol", Predicate: "reports_to", Object: "Alice"})

	// As subject only
	subj, err := kg.Query(ctx, "Alice", "", "subject")
	if err != nil {
		t.Fatalf("query subject: %v", err)
	}
	if len(subj) != 1 {
		t.Errorf("subject query got %d, want 1", len(subj))
	}

	// As object only
	obj, err := kg.Query(ctx, "Alice", "", "object")
	if err != nil {
		t.Fatalf("query object: %v", err)
	}
	if len(obj) != 1 {
		t.Errorf("object query got %d, want 1", len(obj))
	}

	// Both directions
	both, err := kg.Query(ctx, "Alice", "", "")
	if err != nil {
		t.Fatalf("query both: %v", err)
	}
	if len(both) != 2 {
		t.Errorf("both query got %d, want 2", len(both))
	}
}

func TestKG_Query_PointInTime(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth", ValidFrom: &jan})
	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "assigned_to", Object: "billing", ValidFrom: &mar})

	// Query as of Feb 2026 — only auth should match
	feb := "2026-02-01T00:00:00Z"
	triples, err := kg.Query(ctx, "Alice", feb, "")
	if err != nil {
		t.Fatalf("point-in-time query: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("got %d triples, want 1 (only auth)", len(triples))
	}
	if triples[0].ObjectID != "auth" {
		t.Errorf("expected auth, got %q", triples[0].ObjectID)
	}

	// Query as of Apr 2026 — both should match
	apr := "2026-04-01T00:00:00Z"
	triples, err = kg.Query(ctx, "Alice", apr, "")
	if err != nil {
		t.Fatalf("query apr: %v", err)
	}
	if len(triples) != 2 {
		t.Errorf("got %d triples, want 2", len(triples))
	}
}

func TestKG_Invalidate(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})

	err := kg.Invalidate(ctx, "Alice", "works_on", "auth")
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	// Active query (no asOf) should still return it
	all, err := kg.Query(ctx, "Alice", "", "")
	if err != nil {
		t.Fatalf("query after invalidate: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d triples, want 1", len(all))
	}
	if all[0].ValidTo == nil {
		t.Error("valid_to should be set after invalidation")
	}

	// Point-in-time query for future should NOT return it
	future := "2099-01-01T00:00:00Z"
	futureTriples, err := kg.Query(ctx, "Alice", future, "")
	if err != nil {
		t.Fatalf("future query: %v", err)
	}
	// The triple has valid_to set (to now), and future > valid_to, so it should be filtered
	if len(futureTriples) != 0 {
		t.Errorf("expected 0 triples for future query after invalidation, got %d", len(futureTriples))
	}
}

func TestKG_Invalidate_ValidationErrors(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	if err := kg.Invalidate(ctx, "", "works_on", "auth"); err == nil {
		t.Error("expected error for empty subject")
	}
	if err := kg.Invalidate(ctx, "Alice", "", "auth"); err == nil {
		t.Error("expected error for empty predicate")
	}
	if err := kg.Invalidate(ctx, "Alice", "works_on", ""); err == nil {
		t.Error("expected error for empty object")
	}
}

func TestKG_Timeline_ChronologicalOrder(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	// Small delay so extracted_at differs
	time.Sleep(10 * time.Millisecond)
	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "assigned_to", Object: "billing"})
	time.Sleep(10 * time.Millisecond)
	kg.Add(ctx, TripleInput{Subject: "Bob", Predicate: "reports_to", Object: "Alice"})

	timeline, err := kg.Timeline(ctx, "Alice")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("got %d triples in timeline, want 3", len(timeline))
	}

	// Verify chronological order (ASC)
	for i := 1; i < len(timeline); i++ {
		if timeline[i].ExtractedAt.Before(timeline[i-1].ExtractedAt) {
			t.Errorf("timeline not chronological: [%d]=%v before [%d]=%v",
				i, timeline[i].ExtractedAt, i-1, timeline[i-1].ExtractedAt)
		}
	}
}

func TestKG_Timeline_EmptyEntity(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	_, err := kg.Timeline(ctx, "")
	if err == nil {
		t.Error("expected error for empty entity")
	}
}

func TestKG_Timeline_NonExistentEntity(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	timeline, err := kg.Timeline(ctx, "nobody")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline) != 0 {
		t.Errorf("expected empty timeline, got %d", len(timeline))
	}
}

func TestKG_Stats(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "manages", Object: "Bob"})
	kg.Add(ctx, TripleInput{Subject: "Carol", Predicate: "works_on", Object: "billing"})

	// Invalidate one
	kg.Invalidate(ctx, "Alice", "works_on", "auth")

	stats, err := kg.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	// 4 entities: alice, auth, bob, carol + billing = 5? Let's count: alice, auth, bob, carol, billing = 5
	if stats.EntityCount != 5 {
		t.Errorf("entity count = %d, want 5", stats.EntityCount)
	}
	if stats.TripleCount != 3 {
		t.Errorf("triple count = %d, want 3", stats.TripleCount)
	}
	if stats.ActiveTriples != 2 {
		t.Errorf("active triples = %d, want 2", stats.ActiveTriples)
	}
	if len(stats.Predicates) != 2 {
		t.Errorf("predicates = %v, want 2 (manages, works_on)", stats.Predicates)
	}
}

func TestKG_Stats_EmptyGraph(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	stats, err := kg.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.EntityCount != 0 || stats.TripleCount != 0 || stats.ActiveTriples != 0 {
		t.Errorf("empty graph stats should all be 0, got %+v", stats)
	}
}

func TestKG_EntityID_Normalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Alice", "alice"},
		{"auth-migration", "auth-migration"},
		{"O'Brien", "obrien"},
		{"John Doe", "john_doe"},
		{"  spaces  ", "spaces"},
		{"UPPER CASE", "upper_case"},
	}
	for _, tc := range tests {
		got := entityID(tc.input)
		if got != tc.want {
			t.Errorf("entityID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestKG_InvalidatedTriple_StillInTimeline(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	kg.Add(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	kg.Invalidate(ctx, "Alice", "works_on", "auth")

	timeline, err := kg.Timeline(ctx, "Alice")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("invalidated triple should still appear in timeline, got %d", len(timeline))
	}
	if timeline[0].ValidTo == nil {
		t.Error("expected valid_to to be set")
	}
}

func TestKG_Add_WithValidFrom(t *testing.T) {
	kg, _ := setupKG(t)
	ctx := context.Background()

	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	triple, err := kg.Add(ctx, TripleInput{
		Subject:   "Alice",
		Predicate: "joined",
		Object:    "team-alpha",
		ValidFrom: &jan,
	})
	if err != nil {
		t.Fatalf("add with valid_from: %v", err)
	}
	if triple.ValidFrom == nil || !triple.ValidFrom.Equal(jan) {
		t.Errorf("valid_from = %v, want %v", triple.ValidFrom, jan)
	}
}
