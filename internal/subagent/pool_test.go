package subagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPool_AcquireRelease(t *testing.T) {
	p := NewPool(2)
	if p.Size() != 2 {
		t.Fatalf("expected size 2, got %d", p.Size())
	}
	if p.Available() != 2 {
		t.Fatalf("expected 2 available, got %d", p.Available())
	}

	err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if p.Available() != 1 {
		t.Fatalf("expected 1 available after acquire, got %d", p.Available())
	}

	p.Release()
	if p.Available() != 2 {
		t.Fatalf("expected 2 available after release, got %d", p.Available())
	}
}

func TestPool_BlocksAtLimit(t *testing.T) {
	p := NewPool(1)

	// Acquire the only slot.
	err := p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Second acquire should block; use a short-lived context to prove it.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = p.Acquire(ctx)
	if err == nil {
		t.Fatal("expected acquire to fail due to timeout, but it succeeded")
	}

	// Release and verify next acquire succeeds.
	p.Release()
	err = p.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	p.Release()
}

func TestPool_ContextCancellation(t *testing.T) {
	p := NewPool(1)

	// Fill the pool.
	_ = p.Acquire(context.Background())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- p.Acquire(ctx)
	}()

	// Cancel after a short delay.
	time.Sleep(20 * time.Millisecond)
	cancel()

	err := <-done
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}

	p.Release()
}

func TestPool_ConcurrentAccess(t *testing.T) {
	const maxConcurrent = 3
	const goroutines = 20

	p := NewPool(maxConcurrent)
	var active int64
	var maxActive int64
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := p.Acquire(context.Background())
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}

			cur := atomic.AddInt64(&active, 1)
			// Track the maximum concurrent.
			for {
				old := atomic.LoadInt64(&maxActive)
				if cur <= old || atomic.CompareAndSwapInt64(&maxActive, old, cur) {
					break
				}
			}

			// Simulate work.
			time.Sleep(5 * time.Millisecond)

			atomic.AddInt64(&active, -1)
			p.Release()
		}()
	}

	wg.Wait()

	peak := atomic.LoadInt64(&maxActive)
	if peak > int64(maxConcurrent) {
		t.Fatalf("peak concurrent %d exceeded limit %d", peak, maxConcurrent)
	}
	if peak < 2 {
		t.Logf("warning: peak concurrent was only %d (may be system scheduling)", peak)
	}
}

func TestPool_MinimumSize(t *testing.T) {
	p := NewPool(0)
	if p.Size() != 1 {
		t.Fatalf("expected minimum size 1, got %d", p.Size())
	}

	p2 := NewPool(-5)
	if p2.Size() != 1 {
		t.Fatalf("expected minimum size 1 for negative input, got %d", p2.Size())
	}
}
