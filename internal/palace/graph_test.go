package palace

import (
	"context"
	"testing"
	"time"
)

// insertGraphDrawers creates a multi-wing fixture where "auth" and "logging"
// bridge wings, enabling cross-wing traversal.
//
//	backend:  auth, database, api, logging
//	frontend: auth, ui, routing
//	devops:   logging, ci, deploy
func insertGraphDrawers(t *testing.T, store *SQLitePalaceStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	drawers := []struct {
		id, wing, room, content string
	}{
		// backend wing
		{"g1", "backend", "auth", "JWT validation"},
		{"g2", "backend", "auth", "Session management"},
		{"g3", "backend", "database", "Migration strategy"},
		{"g4", "backend", "api", "REST endpoints"},
		{"g5", "backend", "logging", "Structured log format"},
		// frontend wing
		{"g6", "frontend", "auth", "Login form component"},
		{"g7", "frontend", "ui", "Button styles"},
		{"g8", "frontend", "routing", "Route guards"},
		// devops wing
		{"g9", "devops", "logging", "Log aggregation pipeline"},
		{"g10", "devops", "ci", "GitHub Actions config"},
		{"g11", "devops", "deploy", "K8s rollout strategy"},
		// "general" room — should be skipped by graph
		{"g12", "backend", "general", "Misc notes"},
	}

	for _, dd := range drawers {
		d := &Drawer{
			ID:        dd.id,
			Wing:      dd.wing,
			Room:      dd.room,
			Content:   dd.content,
			CreatedAt: now,
		}
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", dd.id, err)
		}
	}
}

func TestGraph_Traverse_FromTunnel(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	results, err := g.Traverse(ctx, "auth", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	// Start room should be first at hop 0.
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Room != "auth" || results[0].Hops != 0 {
		t.Errorf("expected auth at hop 0, got %s at hop %d", results[0].Room, results[0].Hops)
	}
	if len(results[0].Wings) != 2 {
		t.Errorf("auth should bridge 2 wings, got %v", results[0].Wings)
	}

	// Hop 1: rooms sharing backend or frontend wings with auth.
	hop1 := filterByHop(results, 1)
	hop1Names := roomNames(hop1)
	for _, expected := range []string{"database", "api", "ui", "routing", "logging"} {
		if !contains(hop1Names, expected) {
			t.Errorf("expected %s at hop 1, got %v", expected, hop1Names)
		}
	}

	// Hop 2: logging bridges to devops, so ci and deploy should appear.
	hop2 := filterByHop(results, 2)
	hop2Names := roomNames(hop2)
	for _, expected := range []string{"ci", "deploy"} {
		if !contains(hop2Names, expected) {
			t.Errorf("expected %s at hop 2, got %v", expected, hop2Names)
		}
	}
}

func TestGraph_Traverse_MaxHopsLimits(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	// With maxHops=1 from auth, ci/deploy should NOT appear.
	results, err := g.Traverse(ctx, "auth", 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	for _, r := range results {
		if r.Room == "ci" || r.Room == "deploy" {
			t.Errorf("room %s should not be reachable at maxHops=1", r.Room)
		}
	}
}

func TestGraph_Traverse_NonExistent_Suggests(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	results, err := g.Traverse(ctx, "aut", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	// Should get suggestions (hop == -1).
	if len(results) == 0 {
		t.Fatal("expected suggestions for 'aut'")
	}
	if results[0].Hops != -1 {
		t.Errorf("expected hop -1 for suggestion, got %d", results[0].Hops)
	}
	if results[0].Room != "auth" {
		t.Errorf("expected 'auth' suggestion, got %s", results[0].Room)
	}
}

func TestGraph_Traverse_EmptyPalace(t *testing.T) {
	store := newTestStore(t)
	g := NewGraph(store)
	ctx := context.Background()

	results, err := g.Traverse(ctx, "anything", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty palace, got %d", len(results))
	}
}

func TestGraph_FindTunnels_All(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	tunnels, err := g.FindTunnels(ctx, "", "")
	if err != nil {
		t.Fatalf("FindTunnels: %v", err)
	}

	// auth (backend+frontend) and logging (backend+devops) are tunnels.
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d: %v", len(tunnels), tunnels)
	}

	names := make(map[string]bool)
	for _, tun := range tunnels {
		names[tun.Room] = true
		if len(tun.Wings) < 2 {
			t.Errorf("tunnel %s has %d wings, want >= 2", tun.Room, len(tun.Wings))
		}
	}
	if !names["auth"] {
		t.Error("expected auth as tunnel")
	}
	if !names["logging"] {
		t.Error("expected logging as tunnel")
	}
}

func TestGraph_FindTunnels_Filtered(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	tunnels, err := g.FindTunnels(ctx, "backend", "frontend")
	if err != nil {
		t.Fatalf("FindTunnels: %v", err)
	}

	if len(tunnels) != 1 || tunnels[0].Room != "auth" {
		t.Errorf("expected [auth] tunnel between backend-frontend, got %v", tunnels)
	}
}

func TestGraph_FindTunnels_NoMatch(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	tunnels, err := g.FindTunnels(ctx, "frontend", "devops")
	if err != nil {
		t.Fatalf("FindTunnels: %v", err)
	}

	if len(tunnels) != 0 {
		t.Errorf("expected no tunnels between frontend-devops, got %v", tunnels)
	}
}

func TestGraph_Stats(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	stats, err := g.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	// 8 distinct non-general rooms: auth, database, api, logging, ui, routing, ci, deploy.
	if stats.TotalRooms != 8 {
		t.Errorf("TotalRooms: want 8, got %d", stats.TotalRooms)
	}
	if stats.TunnelCount != 2 {
		t.Errorf("TunnelCount: want 2, got %d", stats.TunnelCount)
	}
	// auth bridges backend+frontend (1 edge), logging bridges backend+devops (1 edge).
	if stats.EdgeCount != 2 {
		t.Errorf("EdgeCount: want 2, got %d", stats.EdgeCount)
	}
	if len(stats.TopTunnels) != 2 {
		t.Errorf("TopTunnels: want 2 entries, got %v", stats.TopTunnels)
	}
}

func TestGraph_GeneralRoomSkipped(t *testing.T) {
	store := newTestStore(t)
	insertGraphDrawers(t, store)
	g := NewGraph(store)
	ctx := context.Background()

	// "general" room should be excluded from the graph entirely.
	results, err := g.Traverse(ctx, "general", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	// Not found → suggestions (or empty).
	for _, r := range results {
		if r.Room == "general" {
			t.Error("general room should be skipped by graph")
		}
	}
}

// helpers

func filterByHop(results []TraverseResult, hop int) []TraverseResult {
	var out []TraverseResult
	for _, r := range results {
		if r.Hops == hop {
			out = append(out, r)
		}
	}
	return out
}

func roomNames(results []TraverseResult) []string {
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Room
	}
	return names
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
