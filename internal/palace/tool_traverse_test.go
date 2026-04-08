package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolTraverse_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Create rooms across wings with "auth" as a tunnel room.
	for _, d := range []DrawerInput{
		{Wing: "backend", Room: "auth", Content: "JWT validation"},
		{Wing: "backend", Room: "database", Content: "PostgreSQL schema"},
		{Wing: "backend", Room: "api", Content: "REST endpoints"},
		{Wing: "frontend", Room: "auth", Content: "Login form"},
		{Wing: "frontend", Room: "ui", Content: "Component library"},
	} {
		_, err := p.AddDrawer(ctx, d)
		if err != nil {
			t.Fatalf("AddDrawer: %v", err)
		}
	}

	out, err := palaceTraverseHandler(ctx, p, TraverseToolInput{
		StartRoom: "auth",
	})
	if err != nil {
		t.Fatalf("palaceTraverseHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Traversal from") {
		t.Errorf("expected traversal header, got: %s", out.Content)
	}
	// auth is the start room at hop 0, should find connected rooms.
	if !strings.Contains(out.Content, "auth") {
		t.Error("expected auth in results")
	}
}

func TestToolTraverse_NoResults(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceTraverseHandler(context.Background(), p, TraverseToolInput{
		StartRoom: "nonexistent",
	})
	if err != nil {
		t.Fatalf("palaceTraverseHandler: %v", err)
	}

	if !strings.Contains(out.Content, "No connected rooms") {
		t.Errorf("expected no results message, got: %s", out.Content)
	}
}

func TestToolTraverse_MissingStartRoom(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceTraverseHandler(context.Background(), p, TraverseToolInput{})
	if err != nil {
		t.Fatalf("palaceTraverseHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error, got: %s", out.Content)
	}
}

func TestToolTraverse_MaxHops(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Build a chain: backend has auth+api, frontend has auth+ui.
	for _, d := range []DrawerInput{
		{Wing: "backend", Room: "auth", Content: "auth backend"},
		{Wing: "backend", Room: "api", Content: "api backend"},
		{Wing: "frontend", Room: "auth", Content: "auth frontend"},
		{Wing: "frontend", Room: "ui", Content: "ui frontend"},
	} {
		_, _ = p.AddDrawer(ctx, d)
	}

	// With maxHops=1, should only get immediate neighbors.
	out, err := palaceTraverseHandler(ctx, p, TraverseToolInput{
		StartRoom: "api",
		MaxHops:   1,
	})
	if err != nil {
		t.Fatalf("palaceTraverseHandler: %v", err)
	}

	// api is in backend → should find auth (also in backend) at hop 1.
	if !strings.Contains(out.Content, "auth") {
		t.Errorf("expected auth at hop 1, got: %s", out.Content)
	}
}
