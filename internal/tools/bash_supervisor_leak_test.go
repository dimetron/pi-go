package tools

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// settleGoroutines waits for the goroutine count to stop falling, so a test
// does not mistake "the reaper has not been scheduled yet" for a leak. It
// returns the settled count.
func settleGoroutines(t *testing.T) int {
	t.Helper()

	last := runtime.NumGoroutine()
	stable := 0
	for range 200 {
		time.Sleep(10 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			stable++
			if stable >= 5 {
				return n
			}
			continue
		}
		stable = 0
		last = n
	}
	return last
}

// TestSupervisor_ForegroundRunsLeakNoGoroutines guards the reaping goroutine
// that start() spawns for every command. It is per-command and unbounded, so a
// path that fails to let it finish leaks one goroutine per shell call — which
// in a long session is thousands.
func TestSupervisor_ForegroundRunsLeakNoGoroutines(t *testing.T) {
	sup := fastSupervisor(t)

	// Warm up so one-time initialization is not counted as growth.
	run(t, sup, "echo warmup", 5*time.Second)
	baseline := settleGoroutines(t)

	for range 25 {
		out := run(t, sup, "echo hello", 5*time.Second)
		if out.Running {
			t.Fatalf("a fast command was backgrounded: %+v", out)
		}
	}

	after := settleGoroutines(t)
	if after > baseline+2 {
		t.Errorf("goroutines grew from %d to %d over 25 foreground commands", baseline, after)
	}
}

// TestSupervisor_BackgroundAndKillLeakNoGoroutines covers the handoff path,
// which spawns a second (reap) goroutine per command on top of the reaper.
func TestSupervisor_BackgroundAndKillLeakNoGoroutines(t *testing.T) {
	sup := fastSupervisor(t)

	out := run(t, sup, "sleep 30", 80*time.Millisecond)
	if _, err := sup.killHandle(out.Handle); err != nil {
		t.Fatalf("killHandle: %v", err)
	}
	baseline := settleGoroutines(t)

	for range 10 {
		out := run(t, sup, "sleep 30", 80*time.Millisecond)
		if !out.Running {
			t.Fatalf("a silent long command was not backgrounded: %+v", out)
		}
		if _, err := sup.killHandle(out.Handle); err != nil {
			t.Fatalf("killHandle: %v", err)
		}
	}

	after := settleGoroutines(t)
	if after > baseline+2 {
		t.Errorf("goroutines grew from %d to %d over 10 background+kill cycles", baseline, after)
	}
}

// TestSupervisor_CanceledForegroundRunsLeakNoGoroutines covers the ctx.Done
// branch of supervise, which kills the group and waits for the reaper.
func TestSupervisor_CanceledForegroundRunsLeakNoGoroutines(t *testing.T) {
	sup := fastSupervisor(t)

	cancelOnce := func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()
		defer cancel()
		_, err := sup.Run(ctx, runRequest{dir: t.TempDir(), command: "sleep 30", timeout: 10 * time.Second})
		if err == nil {
			t.Error("a canceled run returned no error")
		}
	}

	cancelOnce()
	baseline := settleGoroutines(t)

	for range 10 {
		cancelOnce()
	}

	after := settleGoroutines(t)
	if after > baseline+2 {
		t.Errorf("goroutines grew from %d to %d over 10 canceled commands", baseline, after)
	}
}

// TestSupervisor_KillAllReleasesEverything checks the session-teardown path
// leaves nothing behind: no tracked handles, no lingering goroutines.
func TestSupervisor_KillAllReleasesEverything(t *testing.T) {
	sup := NewBashSupervisor()
	sup.idleTimeout = 80 * time.Millisecond
	sup.heartbeat = 20 * time.Millisecond

	baseline := settleGoroutines(t)

	for range 5 {
		if out := run(t, sup, "sleep 30", 80*time.Millisecond); !out.Running {
			t.Fatal("command was not backgrounded")
		}
	}
	if got := len(sup.Handles()); got != 5 {
		t.Fatalf("Handles() has %d entries, want 5", got)
	}

	sup.KillAll()

	if got := sup.Handles(); len(got) != 0 {
		t.Errorf("Handles() = %v after KillAll, want empty", got)
	}
	after := settleGoroutines(t)
	if after > baseline+2 {
		t.Errorf("goroutines grew from %d to %d across KillAll", baseline, after)
	}
}

// TestSupervisor_StreamBufferStaysBounded checks the retained-tail invariant
// that keeps a noisy command from growing the heap without bound.
func TestSupervisor_StreamBufferStaysBounded(t *testing.T) {
	s := newStream(64)

	for range 100 {
		if _, err := s.Write([]byte("0123456789")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if got := len(s.String()); got > 64 {
		t.Errorf("retained %d bytes, want at most 64", got)
	}
	if got := s.Len(); got != 1000 {
		t.Errorf("Len() = %d, want 1000 (total ever written, not retained)", got)
	}

	// A reader asking from offset 0 must be told what it missed rather than
	// silently handed a later chunk as if it followed on.
	data, next, dropped := s.since(0)
	if dropped != 1000-int64(len(data)) {
		t.Errorf("dropped = %d, want %d", dropped, 1000-int64(len(data)))
	}
	if next != 1000 {
		t.Errorf("next offset = %d, want 1000", next)
	}
}

func TestStream_ZeroLimitFallsBackToDefault(t *testing.T) {
	s := newStream(0)
	if s.limit != streamCap {
		t.Errorf("limit = %d, want the default %d", s.limit, streamCap)
	}
}

func TestStream_EmptyWriteIsANoOp(t *testing.T) {
	s := newStream(64)
	n, err := s.Write(nil)
	if n != 0 || err != nil {
		t.Errorf("Write(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if s.Len() != 0 {
		t.Errorf("Len() = %d after an empty write, want 0", s.Len())
	}
}

func TestStream_SinceAtOrPastEnd(t *testing.T) {
	s := newStream(64)
	if _, err := s.Write([]byte("abc")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, next, dropped := s.since(3)
	if data != "" || next != 3 || dropped != 0 {
		t.Errorf("since(end) = (%q, %d, %d), want (\"\", 3, 0)", data, next, dropped)
	}

	data, next, dropped = s.since(99)
	if data != "" || next != 3 || dropped != 0 {
		t.Errorf("since(past end) = (%q, %d, %d), want (\"\", 3, 0)", data, next, dropped)
	}
}
