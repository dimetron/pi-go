package palace

import (
	"context"
	"errors"
	"testing"
)

func TestEmbeddingCache_LoadsOnceThenServesFromMemory(t *testing.T) {
	c := newEmbeddingCache()
	calls := 0
	load := func(context.Context, DrawerFilter) ([]EmbeddingRow, error) {
		calls++
		return []EmbeddingRow{{DrawerID: "a", Embedding: []float32{1, 0}}}, nil
	}

	filter := DrawerFilter{Wing: "w", Room: "r"}
	for range 3 {
		rows, err := c.get(t.Context(), filter, load)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
	}
	if calls != 1 {
		t.Errorf("loader called %d times, want 1 — the cache is not serving hits", calls)
	}
}

func TestEmbeddingCache_DistinctFiltersDoNotAlias(t *testing.T) {
	c := newEmbeddingCache()
	load := func(_ context.Context, f DrawerFilter) ([]EmbeddingRow, error) {
		return []EmbeddingRow{{DrawerID: f.Wing + "/" + f.Room}}, nil
	}

	a, _ := c.get(t.Context(), DrawerFilter{Wing: "w", Room: "r1"}, load)
	b, _ := c.get(t.Context(), DrawerFilter{Wing: "w", Room: "r2"}, load)

	if a[0].DrawerID == b[0].DrawerID {
		t.Errorf("filters aliased: both returned %q", a[0].DrawerID)
	}
}

// A drawer belongs to the (wing,room), (wing,"") and ("","") result sets, so a
// write has to land in every one that is already cached or a warm cache goes
// stale and silently stops seeing recent drawers.
func TestEmbeddingCache_AddReachesAllMatchingFilters(t *testing.T) {
	c := newEmbeddingCache()
	empty := func(context.Context, DrawerFilter) ([]EmbeddingRow, error) { return nil, nil }

	for _, f := range []DrawerFilter{{Wing: "w", Room: "r"}, {Wing: "w"}, {}} {
		if _, err := c.get(t.Context(), f, empty); err != nil {
			t.Fatalf("warm %v: %v", f, err)
		}
	}

	c.add("w", "r", "new", []float32{1, 2, 3})

	for _, f := range []DrawerFilter{{Wing: "w", Room: "r"}, {Wing: "w"}, {}} {
		rows, _ := c.get(t.Context(), f, empty)
		if len(rows) != 1 || rows[0].DrawerID != "new" {
			t.Errorf("filter %+v did not see the new drawer: %v", f, rows)
		}
	}
}

func TestEmbeddingCache_AddSkipsUncachedAndEmptyVectors(t *testing.T) {
	c := newEmbeddingCache()
	empty := func(context.Context, DrawerFilter) ([]EmbeddingRow, error) { return nil, nil }

	// Never warmed: add must not create an entry, or a later get would return a
	// single-row set instead of loading the real contents.
	c.add("w", "r", "x", []float32{1})
	if _, ok := c.byASE["w\x00r"]; ok {
		t.Error("add created an entry for a filter that was never loaded")
	}

	// A drawer stored without an embedding contributes no vector.
	if _, err := c.get(t.Context(), DrawerFilter{Wing: "w", Room: "r"}, empty); err != nil {
		t.Fatal(err)
	}
	c.add("w", "r", "y", nil)
	if rows, _ := c.get(t.Context(), DrawerFilter{Wing: "w", Room: "r"}, empty); len(rows) != 0 {
		t.Errorf("empty embedding was cached: %v", rows)
	}
}

func TestEmbeddingCache_InvalidateForcesReload(t *testing.T) {
	c := newEmbeddingCache()
	calls := 0
	load := func(context.Context, DrawerFilter) ([]EmbeddingRow, error) {
		calls++
		return nil, nil
	}
	f := DrawerFilter{Wing: "w"}

	_, _ = c.get(t.Context(), f, load)
	c.invalidate()
	_, _ = c.get(t.Context(), f, load)

	if calls != 2 {
		t.Errorf("loader called %d times, want 2 — invalidate did not clear", calls)
	}
}

func TestEmbeddingCache_LoadErrorIsNotCached(t *testing.T) {
	c := newEmbeddingCache()
	sentinel := errors.New("boom")
	calls := 0
	load := func(context.Context, DrawerFilter) ([]EmbeddingRow, error) {
		calls++
		if calls == 1 {
			return nil, sentinel
		}
		return []EmbeddingRow{{DrawerID: "ok"}}, nil
	}
	f := DrawerFilter{Wing: "w"}

	if _, err := c.get(t.Context(), f, load); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	rows, err := c.get(t.Context(), f, load)
	if err != nil || len(rows) != 1 {
		t.Errorf("a failed load was cached: rows=%v err=%v", rows, err)
	}
}

// A nil cache must behave as a pass-through so a zero-value DrawerService still
// works.
func TestEmbeddingCache_NilPassesThrough(t *testing.T) {
	var c *embeddingCache
	rows, err := c.get(t.Context(), DrawerFilter{}, func(context.Context, DrawerFilter) ([]EmbeddingRow, error) {
		return []EmbeddingRow{{DrawerID: "a"}}, nil
	})
	if err != nil || len(rows) != 1 {
		t.Errorf("nil cache did not pass through: rows=%v err=%v", rows, err)
	}
	c.add("w", "r", "x", []float32{1}) // must not panic
	c.invalidate()                     // must not panic
}
