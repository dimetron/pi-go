package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolKGAdd_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGAddHandler(context.Background(), p, KGAddToolInput{
		Subject:   "Alice",
		Predicate: "works_on",
		Object:    "auth-service",
	})
	if err != nil {
		t.Fatalf("palaceKGAddHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Triple added") {
		t.Errorf("expected success message, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "alice") {
		t.Errorf("expected normalized entity in output, got: %s", out.Content)
	}
}

func TestToolKGAdd_WithValidFrom(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGAddHandler(context.Background(), p, KGAddToolInput{
		Subject:   "Bob",
		Predicate: "assigned_to",
		Object:    "billing",
		ValidFrom: "2026-03-15",
	})
	if err != nil {
		t.Fatalf("palaceKGAddHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Triple added") {
		t.Errorf("expected success, got: %s", out.Content)
	}
}

func TestToolKGAdd_InvalidValidFrom(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGAddHandler(context.Background(), p, KGAddToolInput{
		Subject:   "Bob",
		Predicate: "assigned_to",
		Object:    "billing",
		ValidFrom: "not-a-date",
	})
	if err != nil {
		t.Fatalf("palaceKGAddHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error for invalid date, got: %s", out.Content)
	}
}

func TestToolKGAdd_MissingFields(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	cases := []KGAddToolInput{
		{Predicate: "p", Object: "o"},
		{Subject: "s", Object: "o"},
		{Subject: "s", Predicate: "p"},
	}

	for _, tc := range cases {
		out, err := palaceKGAddHandler(context.Background(), p, tc)
		if err != nil {
			t.Fatalf("palaceKGAddHandler: %v", err)
		}
		if !strings.Contains(out.Content, "Error") {
			t.Errorf("expected error for input %+v, got: %s", tc, out.Content)
		}
	}
}

func TestToolKGQuery_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Add a fact first.
	_, err = palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "Alice", Predicate: "works_on", Object: "auth",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := palaceKGQueryHandler(ctx, p, KGQueryToolInput{
		Entity: "Alice",
	})
	if err != nil {
		t.Fatalf("palaceKGQueryHandler: %v", err)
	}

	if !strings.Contains(out.Content, "alice") {
		t.Errorf("expected entity in output, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "works_on") {
		t.Errorf("expected predicate in output, got: %s", out.Content)
	}
}

func TestToolKGQuery_NoResults(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGQueryHandler(context.Background(), p, KGQueryToolInput{
		Entity: "nobody",
	})
	if err != nil {
		t.Fatalf("palaceKGQueryHandler: %v", err)
	}

	if !strings.Contains(out.Content, "No facts found") {
		t.Errorf("expected no results message, got: %s", out.Content)
	}
}

func TestToolKGQuery_MissingEntity(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGQueryHandler(context.Background(), p, KGQueryToolInput{})
	if err != nil {
		t.Fatalf("palaceKGQueryHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error, got: %s", out.Content)
	}
}

func TestToolKGInvalidate_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Add a fact.
	_, _ = palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "Alice", Predicate: "works_on", Object: "auth",
	})

	// Invalidate it.
	out, err := palaceKGInvalidateHandler(ctx, p, KGInvalidateToolInput{
		Subject: "Alice", Predicate: "works_on", Object: "auth",
	})
	if err != nil {
		t.Fatalf("palaceKGInvalidateHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Invalidated") {
		t.Errorf("expected invalidation message, got: %s", out.Content)
	}

	// Query with as_of = now should still return the fact (valid_to is set to now).
	// But the timeline should show it as ended.
	tlOut, _ := palaceKGTimelineHandler(ctx, p, KGTimelineToolInput{Entity: "Alice"})
	if !strings.Contains(tlOut.Content, "ended") {
		t.Errorf("expected 'ended' status in timeline after invalidation, got: %s", tlOut.Content)
	}
}

func TestToolKGInvalidate_MissingFields(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGInvalidateHandler(context.Background(), p, KGInvalidateToolInput{
		Subject: "Alice",
	})
	if err != nil {
		t.Fatalf("palaceKGInvalidateHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error, got: %s", out.Content)
	}
}

func TestToolKGTimeline_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Add two facts.
	_, _ = palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "Alice", Predicate: "works_on", Object: "auth",
	})
	_, _ = palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "Alice", Predicate: "assigned_to", Object: "billing",
	})

	// Invalidate first.
	_, _ = palaceKGInvalidateHandler(ctx, p, KGInvalidateToolInput{
		Subject: "Alice", Predicate: "works_on", Object: "auth",
	})

	out, err := palaceKGTimelineHandler(ctx, p, KGTimelineToolInput{
		Entity: "Alice",
	})
	if err != nil {
		t.Fatalf("palaceKGTimelineHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Timeline") {
		t.Errorf("expected timeline header, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "works_on") {
		t.Errorf("expected invalidated fact in timeline, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "assigned_to") {
		t.Errorf("expected active fact in timeline, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "ended") {
		t.Errorf("expected 'ended' status for invalidated fact, got: %s", out.Content)
	}
}

func TestToolKGTimeline_Empty(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceKGTimelineHandler(context.Background(), p, KGTimelineToolInput{
		Entity: "nobody",
	})
	if err != nil {
		t.Fatalf("palaceKGTimelineHandler: %v", err)
	}

	if !strings.Contains(out.Content, "No timeline") {
		t.Errorf("expected no timeline message, got: %s", out.Content)
	}
}
