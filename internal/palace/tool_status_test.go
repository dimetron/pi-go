package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolStatus_EmptyPalace(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	tools, err := PalaceTools(p)
	if err != nil {
		t.Fatalf("PalaceTools: %v", err)
	}
	if len(tools) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(tools))
	}

	out, err := palaceStatusHandler(context.Background(), p)
	if err != nil {
		t.Fatalf("palaceStatusHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Drawers") {
		t.Error("expected output to contain 'Drawers'")
	}
	if !strings.Contains(out.Content, "| 0 |") {
		t.Error("expected zero drawer count in output")
	}
}

func TestToolStatus_WithDrawers(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "backend", Room: "auth", Content: "JWT validation"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	_, err = p.AddDrawer(ctx, DrawerInput{Wing: "backend", Room: "db", Content: "Migration runner"})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	out, err := palaceStatusHandler(ctx, p)
	if err != nil {
		t.Fatalf("palaceStatusHandler: %v", err)
	}

	if !strings.Contains(out.Content, "| 2 |") {
		t.Errorf("expected 2 drawers in output, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "Wings") {
		t.Error("expected Wings label in output")
	}
}

func TestToolStatus_WithKGStats(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	_, err = p.KGAdd(ctx, TripleInput{Subject: "Alice", Predicate: "works_on", Object: "auth"})
	if err != nil {
		t.Fatalf("KGAdd: %v", err)
	}

	out, err := palaceStatusHandler(ctx, p)
	if err != nil {
		t.Fatalf("palaceStatusHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Knowledge Graph") {
		t.Error("expected Knowledge Graph section")
	}
	if !strings.Contains(out.Content, "works_on") {
		t.Error("expected predicate 'works_on' in output")
	}
}

func TestPalaceTools_NilPalace(t *testing.T) {
	t.Parallel()

	tools, err := PalaceTools(nil)
	if err != nil {
		t.Fatalf("PalaceTools(nil): %v", err)
	}
	if tools != nil {
		t.Error("expected nil tools for nil palace")
	}
}
