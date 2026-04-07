package palace

import (
	"context"
	"testing"
	"time"
)

// insertTestDrawers populates the store with drawers across 3 wings and multiple rooms.
func insertHierarchyDrawers(t *testing.T, store *SQLitePalaceStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	drawers := []struct {
		id, wing, room, hall, content string
	}{
		{"d1", "backend", "auth", "decisions", "JWT token validation"},
		{"d2", "backend", "auth", "bugs", "Session expiry race condition"},
		{"d3", "backend", "database", "", "Migration rollback strategy"},
		{"d4", "backend", "api", "", "REST endpoint design"},
		{"d5", "frontend", "ui", "", "Button component styles"},
		{"d6", "frontend", "routing", "", "Protected route guards"},
		{"d7", "frontend", "ui", "bugs", "Layout shift on mobile"},
		{"d8", "devops", "ci", "", "GitHub Actions pipeline"},
		{"d9", "devops", "ci", "decisions", "Chose buildx over kaniko"},
		{"d10", "devops", "deploy", "", "Kubernetes rollout strategy"},
	}

	for _, dd := range drawers {
		d := &Drawer{
			ID:        dd.id,
			Wing:      dd.wing,
			Room:      dd.room,
			Hall:      dd.hall,
			Content:   dd.content,
			CreatedAt: now,
		}
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", dd.id, err)
		}
	}
}

func TestHierarchy_ListWings_ThreeWings(t *testing.T) {
	store := newTestStore(t)
	insertHierarchyDrawers(t, store)
	ctx := context.Background()

	wings, err := store.ListWings(ctx)
	if err != nil {
		t.Fatalf("ListWings: %v", err)
	}
	if len(wings) != 3 {
		t.Fatalf("wings = %d, want 3", len(wings))
	}

	// Wings sorted alphabetically: backend, devops, frontend
	expected := []struct {
		wing        string
		drawerCount int
		roomCount   int
	}{
		{"backend", 4, 3},
		{"devops", 3, 2},
		{"frontend", 3, 2},
	}

	for i, exp := range expected {
		if wings[i].Wing != exp.wing {
			t.Errorf("wing[%d] = %q, want %q", i, wings[i].Wing, exp.wing)
		}
		if wings[i].DrawerCount != exp.drawerCount {
			t.Errorf("wing %q drawers = %d, want %d", wings[i].Wing, wings[i].DrawerCount, exp.drawerCount)
		}
		if wings[i].RoomCount != exp.roomCount {
			t.Errorf("wing %q rooms = %d, want %d", wings[i].Wing, wings[i].RoomCount, exp.roomCount)
		}
	}
}

func TestHierarchy_ListRooms_WithHalls(t *testing.T) {
	store := newTestStore(t)
	insertHierarchyDrawers(t, store)
	ctx := context.Background()

	rooms, err := store.ListRooms(ctx, "backend")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 3 {
		t.Fatalf("rooms = %d, want 3", len(rooms))
	}

	// Rooms sorted alphabetically: api, auth, database
	if rooms[0].Room != "api" || rooms[0].DrawerCount != 1 {
		t.Errorf("api: got room=%q count=%d", rooms[0].Room, rooms[0].DrawerCount)
	}
	if rooms[1].Room != "auth" || rooms[1].DrawerCount != 2 {
		t.Errorf("auth: got room=%q count=%d", rooms[1].Room, rooms[1].DrawerCount)
	}
	// auth has halls: bugs, decisions
	if len(rooms[1].Halls) != 2 {
		t.Errorf("auth halls = %d, want 2", len(rooms[1].Halls))
	}
	if rooms[2].Room != "database" || rooms[2].DrawerCount != 1 {
		t.Errorf("database: got room=%q count=%d", rooms[2].Room, rooms[2].DrawerCount)
	}
}

func TestHierarchy_ListRooms_NonExistentWing(t *testing.T) {
	store := newTestStore(t)
	insertHierarchyDrawers(t, store)
	ctx := context.Background()

	rooms, err := store.ListRooms(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 0 {
		t.Errorf("rooms for nonexistent wing = %d, want 0", len(rooms))
	}
}

func TestHierarchy_GetTaxonomy_CompleteTree(t *testing.T) {
	store := newTestStore(t)
	insertHierarchyDrawers(t, store)
	ctx := context.Background()

	tax, err := store.GetTaxonomy(ctx)
	if err != nil {
		t.Fatalf("GetTaxonomy: %v", err)
	}
	if len(tax.Wings) != 3 {
		t.Fatalf("taxonomy wings = %d, want 3", len(tax.Wings))
	}

	// backend: api(1), auth(2), database(1)
	bw := tax.Wings[0]
	if bw.Name != "backend" {
		t.Errorf("first wing = %q, want backend", bw.Name)
	}
	if len(bw.Rooms) != 3 {
		t.Fatalf("backend rooms = %d, want 3", len(bw.Rooms))
	}

	// Verify total drawer count across all rooms in backend
	totalBackend := 0
	for _, r := range bw.Rooms {
		totalBackend += r.DrawerCount
	}
	if totalBackend != 4 {
		t.Errorf("backend total drawers = %d, want 4", totalBackend)
	}

	// devops: ci(2), deploy(1)
	dw := tax.Wings[1]
	if dw.Name != "devops" {
		t.Errorf("second wing = %q, want devops", dw.Name)
	}
	if len(dw.Rooms) != 2 {
		t.Fatalf("devops rooms = %d, want 2", len(dw.Rooms))
	}

	// frontend: routing(1), ui(2)
	fw := tax.Wings[2]
	if fw.Name != "frontend" {
		t.Errorf("third wing = %q, want frontend", fw.Name)
	}
	if len(fw.Rooms) != 2 {
		t.Fatalf("frontend rooms = %d, want 2", len(fw.Rooms))
	}
}

func TestHierarchy_EmptyPalace(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	wings, err := store.ListWings(ctx)
	if err != nil {
		t.Fatalf("ListWings: %v", err)
	}
	if len(wings) != 0 {
		t.Errorf("empty palace wings = %d, want 0", len(wings))
	}

	rooms, err := store.ListRooms(ctx, "any")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 0 {
		t.Errorf("empty palace rooms = %d, want 0", len(rooms))
	}

	tax, err := store.GetTaxonomy(ctx)
	if err != nil {
		t.Fatalf("GetTaxonomy: %v", err)
	}
	if len(tax.Wings) != 0 {
		t.Errorf("empty taxonomy wings = %d, want 0", len(tax.Wings))
	}
}
