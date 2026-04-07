package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolAddDrawer_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	out, err := palaceAddDrawerHandler(ctx, p, AddDrawerToolInput{
		Wing:    "backend",
		Room:    "auth",
		Content: "JWT token validation logic",
	})
	if err != nil {
		t.Fatalf("palaceAddDrawerHandler: %v", err)
	}

	if out.ID == "" {
		t.Error("expected non-empty drawer ID")
	}
	if !strings.Contains(out.Content, "Drawer added") {
		t.Errorf("expected success message, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "backend") {
		t.Error("expected wing in output")
	}

	// Verify drawer was actually stored.
	d, err := p.GetDrawer(ctx, out.ID)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if d.Content != "JWT token validation logic" {
		t.Errorf("content = %q", d.Content)
	}
	if d.AddedBy != "agent" {
		t.Errorf("added_by = %q, want 'agent'", d.AddedBy)
	}
}

func TestToolAddDrawer_DefaultImportance(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	out, err := palaceAddDrawerHandler(ctx, p, AddDrawerToolInput{
		Wing:    "backend",
		Room:    "misc",
		Content: "some content",
	})
	if err != nil {
		t.Fatalf("palaceAddDrawerHandler: %v", err)
	}

	d, err := p.GetDrawer(ctx, out.ID)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if d.Importance != 5 {
		t.Errorf("importance = %d, want 5 (default)", d.Importance)
	}
}

func TestToolAddDrawer_MissingWing(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceAddDrawerHandler(context.Background(), p, AddDrawerToolInput{
		Room:    "auth",
		Content: "some content",
	})
	if err != nil {
		t.Fatalf("palaceAddDrawerHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Error("expected error for missing wing")
	}
}

func TestToolAddDrawer_MissingRoom(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceAddDrawerHandler(context.Background(), p, AddDrawerToolInput{
		Wing:    "backend",
		Content: "some content",
	})
	if err != nil {
		t.Fatalf("palaceAddDrawerHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Error("expected error for missing room")
	}
}

func TestToolAddDrawer_MissingContent(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceAddDrawerHandler(context.Background(), p, AddDrawerToolInput{
		Wing: "backend",
		Room: "auth",
	})
	if err != nil {
		t.Fatalf("palaceAddDrawerHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Error("expected error for missing content")
	}
}

func TestToolAddDrawer_WithHallAndSourceFile(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	out, err := palaceAddDrawerHandler(ctx, p, AddDrawerToolInput{
		Wing:       "backend",
		Room:       "auth",
		Hall:       "hall_decisions",
		Content:    "Chose PASETO over JWT for token format",
		SourceFile: "internal/auth/token.go",
		Importance: 9,
	})
	if err != nil {
		t.Fatalf("palaceAddDrawerHandler: %v", err)
	}

	d, err := p.GetDrawer(ctx, out.ID)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if d.Hall != "hall_decisions" {
		t.Errorf("hall = %q, want 'hall_decisions'", d.Hall)
	}
	if d.SourceFile != "internal/auth/token.go" {
		t.Errorf("source_file = %q", d.SourceFile)
	}
	if d.Importance != 9 {
		t.Errorf("importance = %d, want 9", d.Importance)
	}
}
