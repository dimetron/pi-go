package tools

import (
	"sync"
	"time"
)

// fileContentCache stores recently read file contents to reduce duplicate reads.
// It uses LRU eviction and mtime-based invalidation to ensure freshness.
type fileContentCache struct {
	mu      sync.RWMutex
	entries map[string]*cachedFile
	maxSize int           // max entries before eviction
	maxAge  time.Duration // max age before refresh
}

type cachedFile struct {
	content []byte
	readAt  time.Time
	mtime   int64 // modification time for invalidation
}

// NewFileContentCache creates a new file content cache.
func NewFileContentCache(maxSize int, maxAge time.Duration) *fileContentCache {
	return &fileContentCache{
		entries: make(map[string]*cachedFile),
		maxSize: maxSize,
		maxAge:  maxAge,
	}
}

// Get returns cached content if valid (mtime matches and not expired).
func (c *fileContentCache) Get(path string, mtime int64) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[path]
	if !ok {
		return nil
	}
	if entry.mtime != mtime {
		return nil // invalidated by file modification
	}
	if time.Since(entry.readAt) > c.maxAge {
		return nil // expired
	}
	return entry.content
}

// Put stores content in cache, evicting the oldest entry if at capacity.
func (c *fileContentCache) Put(path string, content []byte, mtime int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[path] = &cachedFile{
		content: content,
		readAt:  time.Now(),
		mtime:   mtime,
	}
}

// Invalidate removes a path from cache.
func (c *fileContentCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
}

// Len returns the number of entries in the cache.
func (c *fileContentCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *fileContentCache) evictOldest() {
	var oldest string
	var oldestTime time.Time
	for path, entry := range c.entries {
		if oldestTime.IsZero() || entry.readAt.Before(oldestTime) {
			oldest = path
			oldestTime = entry.readAt
		}
	}
	if oldest != "" {
		delete(c.entries, oldest)
	}
}
