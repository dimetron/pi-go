package palace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPalace_FullLifecycle(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Add drawers across wings.
	d1, err := p.AddDrawer(ctx, DrawerInput{
		Wing: "backend", Room: "auth", Content: "JWT token validation logic", Importance: 8,
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	if d1.ID == "" {
		t.Fatal("expected non-empty drawer ID")
	}

	_, err = p.AddDrawer(ctx, DrawerInput{
		Wing: "backend", Room: "database", Content: "Migration runner for PostgreSQL", Importance: 5,
	})
	if err != nil {
		t.Fatalf("AddDrawer db: %v", err)
	}

	_, err = p.AddDrawer(ctx, DrawerInput{
		Wing: "frontend", Room: "auth", Content: "Login form component", Importance: 7,
	})
	if err != nil {
		t.Fatalf("AddDrawer frontend: %v", err)
	}

	// GetDrawer.
	got, err := p.GetDrawer(ctx, d1.ID)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if got.Content != "JWT token validation logic" {
		t.Errorf("content = %q", got.Content)
	}

	// ListDrawers with filter.
	drawers, err := p.ListDrawers(ctx, DrawerFilter{Wing: "backend"})
	if err != nil {
		t.Fatalf("ListDrawers: %v", err)
	}
	if len(drawers) != 2 {
		t.Errorf("backend drawers = %d, want 2", len(drawers))
	}

	// Search (FTS5 fallback since no embedder).
	results, err := p.Search(ctx, SearchQuery{Query: "JWT"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Drawer.Content != "JWT token validation logic" {
		t.Errorf("search result = %q", results[0].Drawer.Content)
	}

	// Hierarchy.
	wings, err := p.ListWings(ctx)
	if err != nil {
		t.Fatalf("ListWings: %v", err)
	}
	if len(wings) != 2 {
		t.Errorf("wings = %d, want 2", len(wings))
	}

	rooms, err := p.ListRooms(ctx, "backend")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 2 {
		t.Errorf("backend rooms = %d, want 2", len(rooms))
	}

	taxonomy, err := p.GetTaxonomy(ctx)
	if err != nil {
		t.Fatalf("GetTaxonomy: %v", err)
	}
	if len(taxonomy.Wings) != 2 {
		t.Errorf("taxonomy wings = %d, want 2", len(taxonomy.Wings))
	}

	// Graph — auth room bridges backend+frontend.
	traversed, err := p.Traverse(ctx, "auth", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(traversed) < 2 {
		t.Errorf("traverse results = %d, want >= 2 (auth + database)", len(traversed))
	}

	tunnels, err := p.FindTunnels(ctx, "backend", "frontend")
	if err != nil {
		t.Fatalf("FindTunnels: %v", err)
	}
	if len(tunnels) != 1 || tunnels[0].Room != "auth" {
		t.Errorf("tunnels = %v, want [auth]", tunnels)
	}

	gStats, err := p.GraphStats(ctx)
	if err != nil {
		t.Fatalf("GraphStats: %v", err)
	}
	if gStats.TunnelCount != 1 {
		t.Errorf("tunnel count = %d, want 1", gStats.TunnelCount)
	}

	// Knowledge Graph.
	triple, err := p.KGAdd(ctx, TripleInput{
		Subject: "Alice", Predicate: "works_on", Object: "auth",
	})
	if err != nil {
		t.Fatalf("KGAdd: %v", err)
	}
	if triple.ID == "" {
		t.Fatal("expected non-empty triple ID")
	}

	triples, err := p.KGQuery(ctx, "Alice", "", "")
	if err != nil {
		t.Fatalf("KGQuery: %v", err)
	}
	if len(triples) != 1 {
		t.Errorf("triples = %d, want 1", len(triples))
	}

	timeline, err := p.KGTimeline(ctx, "Alice")
	if err != nil {
		t.Fatalf("KGTimeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Errorf("timeline = %d, want 1", len(timeline))
	}

	kgStats, err := p.KGStats(ctx)
	if err != nil {
		t.Fatalf("KGStats: %v", err)
	}
	if kgStats.EntityCount != 2 {
		t.Errorf("entities = %d, want 2", kgStats.EntityCount)
	}

	// Diary.
	if err := p.DiaryWrite(ctx, "test-agent", "Session went well", "reflection"); err != nil {
		t.Fatalf("DiaryWrite: %v", err)
	}
	entries, err := p.DiaryRead(ctx, "test-agent", 10)
	if err != nil {
		t.Fatalf("DiaryRead: %v", err)
	}
	if len(entries) != 1 || entries[0].Entry != "Session went well" {
		t.Errorf("diary entries = %v", entries)
	}

	// Status.
	status, err := p.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.DrawerCount != 3 {
		t.Errorf("drawer count = %d, want 3", status.DrawerCount)
	}
	if status.WingCount != 2 {
		t.Errorf("wing count = %d, want 2", status.WingCount)
	}
	if status.ModelLoaded {
		t.Error("model should not be loaded (nil embedder)")
	}
	if status.KG == nil || status.KG.ActiveTriples != 1 {
		t.Errorf("kg active triples = %v", status.KG)
	}

	// Delete drawer.
	if err := p.DeleteDrawer(ctx, d1.ID); err != nil {
		t.Fatalf("DeleteDrawer: %v", err)
	}
	_, err = p.GetDrawer(ctx, d1.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestPalace_WakeUp(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)

	// Create identity file.
	tmpDir := t.TempDir()
	idFile := filepath.Join(tmpDir, "identity.txt")
	if err := os.WriteFile(idFile, []byte("I am Pi, a coding assistant."), 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	p := NewWithStore(store, nil, WithIdentityFile(idFile))
	defer p.Close()
	ctx := context.Background()

	// Add a drawer for L1.
	_, err = p.AddDrawer(ctx, DrawerInput{
		Wing: "project", Room: "api", Content: "REST API handles authentication", Importance: 9,
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	wakeup, err := p.WakeUp(ctx, "project")
	if err != nil {
		t.Fatalf("WakeUp: %v", err)
	}

	if wakeup == "" {
		t.Fatal("expected non-empty wakeup")
	}
	if !strings.Contains(wakeup, "I am Pi") {
		t.Error("wakeup missing identity")
	}
	if !strings.Contains(wakeup, "REST API") {
		t.Error("wakeup missing essential knowledge")
	}
}

func TestPalace_New_InMemory(t *testing.T) {
	t.Parallel()

	p, err := New(WithDBPath(":memory:"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	status, err := p.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.DrawerCount != 0 {
		t.Errorf("empty palace drawer count = %d", status.DrawerCount)
	}
}

func TestPalace_New_WithFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	p, err := New(WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Add a drawer and close.
	ctx := context.Background()
	_, err = p.AddDrawer(ctx, DrawerInput{
		Wing: "test", Room: "room1", Content: "persistent data",
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	p.Close()

	// Reopen and verify data persisted.
	p2, err := New(WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New reopen: %v", err)
	}
	defer p2.Close()

	status, err := p2.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.DrawerCount != 1 {
		t.Errorf("after reopen drawer count = %d, want 1", status.DrawerCount)
	}
}

func TestPalace_Recall(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	_, err = p.AddDrawer(ctx, DrawerInput{
		Wing: "backend", Room: "auth", Content: "OAuth2 flow implementation details",
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	recall, err := p.Recall(ctx, "backend", "auth")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !strings.Contains(recall, "OAuth2") {
		t.Errorf("recall missing content: %q", recall)
	}
}

func TestPalace_KGInvalidate(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	_, err = p.KGAdd(ctx, TripleInput{
		Subject: "Bob", Predicate: "owns", Object: "project-x",
	})
	if err != nil {
		t.Fatalf("KGAdd: %v", err)
	}

	if err := p.KGInvalidate(ctx, "Bob", "owns", "project-x"); err != nil {
		t.Fatalf("KGInvalidate: %v", err)
	}

	// Active query should return nothing.
	triples, err := p.KGQuery(ctx, "Bob", "", "")
	if err != nil {
		t.Fatalf("KGQuery: %v", err)
	}
	// All triples should have valid_to set.
	for _, tr := range triples {
		if tr.ValidTo == nil {
			t.Errorf("expected valid_to set on invalidated triple %s", tr.ID)
		}
	}

	// Timeline still shows it.
	timeline, err := p.KGTimeline(ctx, "Bob")
	if err != nil {
		t.Fatalf("KGTimeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Errorf("timeline = %d, want 1", len(timeline))
	}
}

