package tui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestEnqueueHook_DropsWhenQueueFull pins the backpressure policy: a hook that
// blocks while turns keep completing must not grow an unbounded backlog, so
// submissions past hookQueueDepth are dropped rather than queued or allowed to
// block the Update goroutine that submitted them.
func TestEnqueueHook_DropsWhenQueueFull(t *testing.T) {
	m := &model{ctx: context.Background()}

	release := make(chan struct{})
	started := make(chan struct{})
	var ran atomic.Int64

	// The first job occupies the worker, so everything after it sits in the
	// buffer and the buffer's capacity is what is actually under test. Wait
	// for it to be picked up — until then it holds a buffer slot itself, and
	// the count below would be off by one.
	m.enqueueHook(func() {
		ran.Add(1)
		close(started)
		<-release
	})
	<-started
	for range hookQueueDepth {
		m.enqueueHook(func() { ran.Add(1) })
	}

	// The queue is full and the worker is blocked, so this one has nowhere to
	// go. enqueueHook must return immediately rather than wait for a slot.
	dropped := make(chan struct{})
	go func() {
		m.enqueueHook(func() { ran.Add(1) })
		close(dropped)
	}()
	select {
	case <-dropped:
	case <-time.After(5 * time.Second):
		t.Fatal("enqueueHook blocked when the queue was full, want an immediate drop")
	}

	close(release)

	want := int64(hookQueueDepth + 1) // the blocker plus a full buffer
	deadline := time.Now().Add(10 * time.Second)
	for ran.Load() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Give the dropped job a chance to run, so the assertion below fails
	// loudly if it was queued after all rather than dropped.
	time.Sleep(100 * time.Millisecond)
	if got := ran.Load(); got != want {
		t.Errorf("ran %d hooks, want %d (the overflow submission must be dropped)", got, want)
	}
}

// TestEnqueueHook_WorkerStopsOnContextCancel pins that the worker goroutine
// does not outlive the session: once the model's context is canceled it
// returns instead of lingering for the life of the process.
func TestEnqueueHook_WorkerStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{ctx: ctx}

	release := make(chan struct{})
	started := make(chan struct{})
	var ran atomic.Int64

	m.enqueueHook(func() {
		close(started)
		<-release
	})
	<-started

	// Cancel while the worker is busy, then let it finish. It loops back to a
	// select where the queue is empty and only ctx.Done() is ready, so the
	// exit branch is taken deterministically.
	cancel()
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Anything submitted after the worker has exited is never executed.
	time.Sleep(200 * time.Millisecond)
	m.enqueueHook(func() { ran.Add(1) })
	time.Sleep(200 * time.Millisecond)
	if got := ran.Load(); got != 0 {
		t.Errorf("worker ran %d hooks after context cancellation, want 0", got)
	}
}

// TestEnqueueHook_NilContextIsSafe covers the zero-value model path: hooks are
// reachable from tests and from a model built before ctx is assigned, and a
// nil context must not panic the worker.
func TestEnqueueHook_NilContextIsSafe(t *testing.T) {
	m := &model{}
	m.runLifecycleHooks("turn_complete", nil) // no hooks configured, no worker
	if m.hookQueue != nil {
		t.Error("hookQueue created with no hooks configured")
	}
}
