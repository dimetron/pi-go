package palace

import (
	"context"
	"sync"
)

// embeddingCache keeps decoded embedding vectors in memory, keyed by the filter
// that produced them.
//
// Why this exists: both semantic search and AddDrawer's duplicate check call
// GetAllEmbeddings, which reads and decodes every matching BLOB on every call.
// Measured on this project's palace (17,756 drawers, 384-dim): 217ms, 87MB
// allocated and 142k allocations per call — 95% of a search's total cost. The
// vectors only change when a drawer is written, so re-reading them per query is
// pure waste.
//
// Entries are appended to rather than invalidated on insert: the caller already
// holds the new vector, so a warm cache never needs a second full load.
//
// Scope is deliberately per-process. Another process writing to the same
// database will not invalidate this copy, which is tolerable for both consumers
// — a missed duplicate stores one redundant drawer, and a search misses one
// recent result — but it does mean this is not a general-purpose cache.
type embeddingCache struct {
	mu      sync.RWMutex
	byASE   map[string][]EmbeddingRow
	disable bool
}

func newEmbeddingCache() *embeddingCache {
	return &embeddingCache{byASE: make(map[string][]EmbeddingRow)}
}

// cacheKey identifies a filter. Only Wing and Room take part: GetAllEmbeddings
// ignores Hall, so including it would create keys that alias the same rows.
func cacheKey(f DrawerFilter) string {
	return f.Wing + "\x00" + f.Room
}

// get returns the cached rows for filter, loading them through load on a miss.
func (c *embeddingCache) get(
	ctx context.Context,
	filter DrawerFilter,
	load func(context.Context, DrawerFilter) ([]EmbeddingRow, error),
) ([]EmbeddingRow, error) {
	if c == nil || c.disable {
		return load(ctx, filter)
	}

	key := cacheKey(filter)

	c.mu.RLock()
	rows, ok := c.byASE[key]
	c.mu.RUnlock()
	if ok {
		return rows, nil
	}

	rows, err := load(ctx, filter)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.byASE[key] = rows
	c.mu.Unlock()
	return rows, nil
}

// add records a newly written drawer against every cached filter it belongs to.
//
// A drawer in (wing, room) is a member of both the (wing, room) and the
// (wing, "") result sets, and of ("", "") when something asked for everything,
// so all three keys have to be updated or a warm cache silently goes stale.
func (c *embeddingCache) add(wing, room, drawerID string, embedding []float32) {
	if c == nil || c.disable || len(embedding) == 0 {
		return
	}

	row := EmbeddingRow{DrawerID: drawerID, Embedding: embedding}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range []string{
		wing + "\x00" + room,
		wing + "\x00",
		"\x00",
	} {
		if rows, ok := c.byASE[key]; ok {
			c.byASE[key] = append(rows, row)
		}
	}
}

// invalidate drops everything. Used after deletes, where an append cannot
// express the change.
func (c *embeddingCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.byASE)
	c.mu.Unlock()
}
