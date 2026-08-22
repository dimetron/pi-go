package palace

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// cogInsertDrawers inserts one drawer per (wing, room) entry, repeated `count`
// times so drawer counts — which drive the result ordering — are controllable.
func cogInsertDrawers(t *testing.T, store *SQLitePalaceStore, rows []cogDrawerRow) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	n := 0
	for _, r := range rows {
		for range max(r.count, 1) {
			n++
			d := &Drawer{
				ID:        fmt.Sprintf("cog-%d", n),
				Wing:      r.wing,
				Room:      r.room,
				Content:   fmt.Sprintf("content %d", n),
				CreatedAt: now,
			}
			if err := store.InsertDrawer(ctx, d); err != nil {
				t.Fatalf("insert %s: %v", d.ID, err)
			}
		}
	}
}

type cogDrawerRow struct {
	wing  string
	room  string
	count int
}

// cogFailingStore fails every ListDrawers, which is the only store call
// Traverse makes. The embedded interface is nil on purpose: any other call
// would panic and so prove the test was measuring something else.
type cogFailingStore struct {
	PalaceStore
	err error
}

func (s cogFailingStore) ListDrawers(_ context.Context, _ DrawerFilter) ([]*Drawer, error) {
	return nil, s.err
}

// cogHopsByRoom indexes a traversal by room so assertions do not depend on the
// ordering of ties, which map iteration makes nondeterministic.
func cogHopsByRoom(results []TraverseResult) map[string]int {
	hops := make(map[string]int, len(results))
	for _, r := range results {
		hops[r.Room] = r.Hops
	}
	return hops
}

// A chain of wings, each overlapping the next by one room, gives every room a
// single unambiguous hop distance from "start":
//
//	w1: start, m     w2: m, n     w3: n, p     w4: p, q
func cogChainFixture(t *testing.T, store *SQLitePalaceStore) {
	t.Helper()
	cogInsertDrawers(t, store, []cogDrawerRow{
		{"w1", "start", 1}, {"w1", "m", 1},
		{"w2", "m", 1}, {"w2", "n", 1},
		{"w3", "n", 1}, {"w3", "p", 1},
		{"w4", "p", 1}, {"w4", "q", 1},
	})
}

// maxHops is what stops the BFS, and the pre-refactor code substitutes 2 for
// any non-positive value. Each case pins exactly which rooms of the chain come
// back, so a changed loop bound cannot pass unnoticed.
func TestGraphTraverseHopBoundary(t *testing.T) {
	tests := []struct {
		name     string
		maxHops  int
		wantHops map[string]int
	}{
		{"zero means two hops", 0, map[string]int{"start": 0, "m": 1, "n": 2}},
		{"negative means two hops", -5, map[string]int{"start": 0, "m": 1, "n": 2}},
		{"one hop stops at the first ring", 1, map[string]int{"start": 0, "m": 1}},
		{"two hops", 2, map[string]int{"start": 0, "m": 1, "n": 2}},
		{"three hops", 3, map[string]int{"start": 0, "m": 1, "n": 2, "p": 3}},
		{"four hops reaches the whole chain", 4, map[string]int{"start": 0, "m": 1, "n": 2, "p": 3, "q": 4}},
		{"more hops than the chain is long adds nothing", 9, map[string]int{"start": 0, "m": 1, "n": 2, "p": 3, "q": 4}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			cogChainFixture(t, store)

			results, err := NewGraph(store).Traverse(context.Background(), "start", tt.maxHops)
			if err != nil {
				t.Fatalf("Traverse: %v", err)
			}

			got := cogHopsByRoom(results)
			if len(got) != len(tt.wantHops) {
				t.Fatalf("reached %v, want %v", got, tt.wantHops)
			}
			for room, want := range tt.wantHops {
				if h, ok := got[room]; !ok || h != want {
					t.Errorf("room %q: hops = %d (present: %v), want %d", room, h, ok, want)
				}
			}
		})
	}
}

// The start room is always first, carries hop 0, and reports its own wings and
// drawer count rather than a neighbor's.
func TestGraphTraverseStartRoomLeadsTheResults(t *testing.T) {
	store := newTestStore(t)
	cogInsertDrawers(t, store, []cogDrawerRow{
		{"w1", "start", 3},
		{"w2", "start", 2},
		{"w1", "other", 1},
	})

	results, err := NewGraph(store).Traverse(context.Background(), "start", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}

	first := results[0]
	if first.Room != "start" {
		t.Errorf("results[0].Room = %q, want \"start\"", first.Room)
	}
	if first.Hops != 0 {
		t.Errorf("results[0].Hops = %d, want 0", first.Hops)
	}
	if first.DrawerCount != 5 {
		t.Errorf("results[0].DrawerCount = %d, want 5", first.DrawerCount)
	}
	if len(first.Wings) != 2 || first.Wings[0] != "w1" || first.Wings[1] != "w2" {
		t.Errorf("results[0].Wings = %v, want [w1 w2] sorted", first.Wings)
	}

	for _, r := range results[1:] {
		if r.Room == "start" {
			t.Error("the start room appeared twice")
		}
	}
}

// Ordering is (hops ASC, drawer count DESC). The two rings here have distinct
// drawer counts so there are no ties to make the comparison nondeterministic.
func TestGraphTraverseResultOrdering(t *testing.T) {
	store := newTestStore(t)
	cogInsertDrawers(t, store, []cogDrawerRow{
		// Ring one, in the same wing as the start room.
		{"w1", "start", 1},
		{"w1", "near-big", 5},
		{"w1", "near-mid", 3},
		{"w1", "near-small", 1},
		// Ring two, reachable only through near-big's second wing.
		{"w2", "near-big", 1},
		{"w2", "far-big", 4},
		{"w2", "far-small", 2},
	})

	results, err := NewGraph(store).Traverse(context.Background(), "start", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	want := []string{"start", "near-big", "near-mid", "near-small", "far-big", "far-small"}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(results), len(want), results)
	}
	for i, room := range want {
		if results[i].Room != room {
			t.Errorf("results[%d].Room = %q, want %q (full order: %v)", i, results[i].Room, room, cogRoomOrder(results))
		}
	}
	if results[1].Hops != 1 || results[4].Hops != 2 {
		t.Errorf("hop ordering broke: %v", cogRoomOrder(results))
	}
}

func cogRoomOrder(results []TraverseResult) []string {
	rooms := make([]string, 0, len(results))
	for _, r := range results {
		rooms = append(rooms, r.Room)
	}
	return rooms
}

// Neighbors are capped at 50, and the cap is applied to the neighbors only —
// the start room is prepended afterwards, so a full traversal returns 51.
func TestGraphTraverseCapsNeighborsAtFifty(t *testing.T) {
	store := newTestStore(t)
	rows := []cogDrawerRow{{"w1", "start", 1}}
	for i := range 60 {
		rows = append(rows, cogDrawerRow{"w1", fmt.Sprintf("room-%02d", i), 1})
	}
	cogInsertDrawers(t, store, rows)

	results, err := NewGraph(store).Traverse(context.Background(), "start", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(results) != 51 {
		t.Fatalf("got %d results, want 51 (50 neighbors plus the start room)", len(results))
	}
	if results[0].Room != "start" {
		t.Errorf("results[0].Room = %q, want \"start\"", results[0].Room)
	}
	for _, r := range results[1:] {
		if r.Hops != 1 {
			t.Errorf("room %q: hops = %d, want 1", r.Room, r.Hops)
		}
	}
}

// A room already reached keeps the hop count it was first given; a later,
// longer path to it must not overwrite that.
func TestGraphTraverseKeepsTheShortestHop(t *testing.T) {
	store := newTestStore(t)
	cogInsertDrawers(t, store, []cogDrawerRow{
		// "target" sits one hop away through w1 and would also be found two
		// hops away through the w2/w3 detour.
		{"w1", "start", 1}, {"w1", "target", 1},
		{"w2", "start", 1}, {"w2", "detour", 1},
		{"w3", "detour", 1}, {"w3", "target", 1},
	})

	results, err := NewGraph(store).Traverse(context.Background(), "start", 3)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	got := cogHopsByRoom(results)
	if got["target"] != 1 {
		t.Errorf("target hops = %d, want 1 — the shorter path must win", got["target"])
	}
	if got["detour"] != 1 {
		t.Errorf("detour hops = %d, want 1", got["detour"])
	}
}

// An isolated start room yields only itself.
func TestGraphTraverseIsolatedStartRoom(t *testing.T) {
	store := newTestStore(t)
	cogInsertDrawers(t, store, []cogDrawerRow{
		{"w1", "start", 2},
		{"w2", "elsewhere", 1},
	})

	results, err := NewGraph(store).Traverse(context.Background(), "start", 3)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %v", len(results), cogRoomOrder(results))
	}
	if results[0].Room != "start" || results[0].Hops != 0 || results[0].DrawerCount != 2 {
		t.Errorf("results[0] = %+v, want the start room at hop 0 with 2 drawers", results[0])
	}
}

// A store failure surfaces unchanged, before any traversal happens.
func TestGraphTraverseStoreError(t *testing.T) {
	wantErr := errors.New("store is down")
	g := NewGraph(cogFailingStore{err: wantErr})

	results, err := g.Traverse(context.Background(), "start", 2)
	if !errors.Is(err, wantErr) {
		t.Errorf("Traverse error = %v, want %v", err, wantErr)
	}
	if results != nil {
		t.Errorf("Traverse results = %v, want nil on error", results)
	}
}

// The "general" room is excluded from the graph entirely, so it is never a
// start room and never a neighbor.
func TestGraphTraverseExcludesGeneralRoom(t *testing.T) {
	store := newTestStore(t)
	cogInsertDrawers(t, store, []cogDrawerRow{
		{"w1", "start", 1},
		{"w1", "general", 4},
		{"w1", "real", 1},
	})

	results, err := NewGraph(store).Traverse(context.Background(), "start", 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	for _, r := range results {
		if r.Room == "general" {
			t.Error("the general room leaked into the traversal")
		}
	}
	if got := cogRoomOrder(results); len(got) != 2 {
		t.Errorf("rooms = %v, want just start and real", got)
	}
}
