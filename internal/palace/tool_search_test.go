package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolSearch_ReturnsResults(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Insert drawers with distinct content.
	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "backend", Room: "auth", Content: "JWT token validation logic for API authentication"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "backend", Room: "db", Content: "PostgreSQL migration runner handles schema changes"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "frontend", Room: "ui", Content: "React component for cooking recipe display"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	// Search for "JWT" should find the auth drawer via FTS5.
	out, err := palaceSearchHandler(ctx, p, SearchToolInput{Query: "JWT"})
	if err != nil {
		t.Fatalf("palaceSearchHandler: %v", err)
	}

	if out.Total == 0 {
		t.Fatal("expected at least one result")
	}
	if !strings.Contains(out.Content, "JWT") {
		t.Errorf("expected JWT in results, got: %s", out.Content)
	}
}

func TestToolSearch_EmptyQuery(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceSearchHandler(context.Background(), p, SearchToolInput{Query: ""})
	if err != nil {
		t.Fatalf("palaceSearchHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Error("expected error for empty query")
	}
}

func TestToolSearch_NoResults(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceSearchHandler(context.Background(), p, SearchToolInput{Query: "nonexistent"})
	if err != nil {
		t.Fatalf("palaceSearchHandler: %v", err)
	}

	if !strings.Contains(out.Content, "No results") {
		t.Errorf("expected 'No results' message, got: %s", out.Content)
	}
}

func TestToolSearch_WingFilter(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "backend", Room: "auth", Content: "backend authentication module"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "frontend", Room: "auth", Content: "frontend authentication component"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	out, err := palaceSearchHandler(ctx, p, SearchToolInput{Query: "authentication", Wing: "backend"})
	if err != nil {
		t.Fatalf("palaceSearchHandler: %v", err)
	}

	if out.Total == 0 {
		t.Fatal("expected at least one result with wing filter")
	}
	// All results should be from backend wing.
	if strings.Contains(out.Content, "frontend") {
		t.Error("expected only backend results with wing filter")
	}
}

func TestToolSearch_LimitRespected(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Insert several drawers with the keyword "testing".
	contents := []string{
		"testing patterns for unit tests in Go",
		"testing integration with database connections",
		"testing end-to-end API workflows",
		"testing performance benchmarks and profiling",
		"testing security vulnerability scanners",
	}
	for i, c := range contents {
		_, err = p.AddDrawer(ctx, DrawerInput{
			Wing:    "backend",
			Room:    "tests",
			Content: c,
		})
		if err != nil {
			t.Fatalf("AddDrawer[%d]: %v", i, err)
		}
	}

	out, err := palaceSearchHandler(ctx, p, SearchToolInput{Query: "testing", Limit: 2})
	if err != nil {
		t.Fatalf("palaceSearchHandler: %v", err)
	}

	if out.Total > 2 {
		t.Errorf("expected at most 2 results, got %d", out.Total)
	}
}
