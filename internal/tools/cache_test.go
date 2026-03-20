package tools

import (
	"testing"
	"time"
)

func TestCacheGetMiss(t *testing.T) {
	c := NewFileContentCache(10, time.Minute)
	if got := c.Get("nonexistent", 0); got != nil {
		t.Errorf("expected nil for cache miss, got %v", got)
	}
}

func TestCachePutAndGet(t *testing.T) {
	c := NewFileContentCache(10, time.Minute)
	c.Put("file.go", []byte("content"), 1000)

	got := c.Get("file.go", 1000)
	if got == nil {
		t.Fatal("expected cache hit")
	}
	if string(got) != "content" {
		t.Errorf("expected 'content', got %q", string(got))
	}
}

func TestCacheMtimeInvalidation(t *testing.T) {
	c := NewFileContentCache(10, time.Minute)
	c.Put("file.go", []byte("old"), 1000)

	// Different mtime should miss
	if got := c.Get("file.go", 2000); got != nil {
		t.Error("expected nil for different mtime")
	}

	// Same mtime should hit
	if got := c.Get("file.go", 1000); got == nil {
		t.Error("expected hit for same mtime")
	}
}

func TestCacheInvalidate(t *testing.T) {
	c := NewFileContentCache(10, time.Minute)
	c.Put("file.go", []byte("content"), 1000)
	c.Invalidate("file.go")

	if got := c.Get("file.go", 1000); got != nil {
		t.Error("expected nil after invalidation")
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewFileContentCache(2, time.Minute)
	c.Put("a.go", []byte("a"), 1)
	time.Sleep(time.Millisecond) // ensure different readAt
	c.Put("b.go", []byte("b"), 2)
	time.Sleep(time.Millisecond)
	c.Put("c.go", []byte("c"), 3) // should evict "a.go"

	if c.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", c.Len())
	}
	if got := c.Get("a.go", 1); got != nil {
		t.Error("expected 'a.go' to be evicted")
	}
	if got := c.Get("b.go", 2); got == nil {
		t.Error("expected 'b.go' to still be cached")
	}
	if got := c.Get("c.go", 3); got == nil {
		t.Error("expected 'c.go' to still be cached")
	}
}

func TestCacheExpiration(t *testing.T) {
	c := NewFileContentCache(10, 10*time.Millisecond)
	c.Put("file.go", []byte("content"), 1000)

	// Should hit immediately
	if got := c.Get("file.go", 1000); got == nil {
		t.Error("expected cache hit before expiration")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	if got := c.Get("file.go", 1000); got != nil {
		t.Error("expected cache miss after expiration")
	}
}

func TestCacheLen(t *testing.T) {
	c := NewFileContentCache(10, time.Minute)
	if c.Len() != 0 {
		t.Errorf("expected 0 entries, got %d", c.Len())
	}
	c.Put("a.go", []byte("a"), 1)
	c.Put("b.go", []byte("b"), 2)
	if c.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", c.Len())
	}
}
