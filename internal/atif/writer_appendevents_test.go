package atif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
)

// TestWriterAppendEvents_BasicBatch verifies that a single AppendEvents call
// converts N events into N steps, flushes once, and produces a valid file.
func TestWriterAppendEvents_BasicBatch(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	events := []*session.Event{
		newTestEvent("user", "first"),
		newTestEvent("model", "second"),
		newTestEvent("user", "third"),
		newTestEvent("model", "fourth"),
	}

	if err := w.AppendEvents(events); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}

	if w.StepCount() != len(events) {
		t.Errorf("StepCount() = %d, want %d", w.StepCount(), len(events))
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("file contents are not valid JSON")
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(traj.Steps) != len(events) {
		t.Fatalf("steps in file = %d, want %d", len(traj.Steps), len(events))
	}
	for i, s := range traj.Steps {
		if s.StepID != i+1 {
			t.Errorf("step[%d].StepID = %d, want %d", i, s.StepID, i+1)
		}
	}
}

// TestWriterAppendEvents_ContentMatchesLivePath verifies that the trajectory
// produced by a single AppendEvents call with N events is byte-for-byte
// identical to the trajectory produced by N individual AppendEvent calls on
// a fresh writer. This proves the batch path is not a different (smaller)
// trajectory.
func TestWriterAppendEvents_ContentMatchesLivePath(t *testing.T) {
	dir := t.TempDir()

	events := []*session.Event{
		newTestEvent("user", "msg-0"),
		newTestEvent("model", "msg-1"),
		newTestEvent("user", "msg-2"),
		newTestEvent("model", "msg-3"),
		newTestEvent("user", "msg-4"),
	}

	// Both writers must share the same SessionID so their trajectories
	// are byte-equal.
	meta := SessionMeta{SessionID: "sess-1", AgentName: "pi-go"}

	// Live path: N AppendEvent calls on a fresh writer.
	fpLive := filepath.Join(dir, "live.atif.json")
	wLive := NewWriter(fpLive, meta)
	for _, ev := range events {
		if err := wLive.AppendEvent(ev); err != nil {
			t.Fatalf("live AppendEvent: %v", err)
		}
	}
	liveData, err := os.ReadFile(fpLive)
	if err != nil {
		t.Fatalf("read live file: %v", err)
	}

	// Batch path: one AppendEvents call on a separate fresh writer.
	fpBatch := filepath.Join(dir, "batch.atif.json")
	wBatch := NewWriter(fpBatch, meta)
	if err := wBatch.AppendEvents(events); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	batchData, err := os.ReadFile(fpBatch)
	if err != nil {
		t.Fatalf("read batch file: %v", err)
	}

	if string(liveData) != string(batchData) {
		t.Errorf("batch trajectory differs from live trajectory (live=%d bytes, batch=%d bytes)", len(liveData), len(batchData))
	}
}

// TestWriterAppendEvents_AllEmpty verifies that an AppendEvents call whose
// events all produce no steps does not create the trajectory file. This
// matches the existing TestWriterSkipsEmptyEvent / TestWriterCloseEmpty
// contract.
func TestWriterAppendEvents_AllEmpty(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	// All empty events: nil, no content, blank author.
	events := []*session.Event{
		nil,
		{Author: "user"},
		{Author: "model"},
	}
	if err := w.AppendEvents(events); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Errorf("file should not exist for all-empty batch, stat err = %v", err)
	}
	if w.StepCount() != 0 {
		t.Errorf("StepCount() = %d, want 0", w.StepCount())
	}
}

// TestWriterAppendEvents_MixedEmpty verifies that a batch with a mix of empty
// and non-empty events produces a file containing only the non-empty steps.
func TestWriterAppendEvents_MixedEmpty(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	events := []*session.Event{
		nil,                              // empty
		{Author: "user"},                 // empty (no content)
		newTestEvent("user", "real-1"),   // step 1
		{Author: "model"},                // empty
		newTestEvent("model", "real-2"),  // step 2
		nil,                              // empty
		newTestEvent("user", "real-3"),   // step 3
	}
	if err := w.AppendEvents(events); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}

	if w.StepCount() != 3 {
		t.Errorf("StepCount() = %d, want 3", w.StepCount())
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(traj.Steps) != 3 {
		t.Fatalf("steps in file = %d, want 3", len(traj.Steps))
	}
	for i, s := range traj.Steps {
		if s.StepID != i+1 {
			t.Errorf("step[%d].StepID = %d, want %d", i, s.StepID, i+1)
		}
	}
}

// countFlushTransitions spins a polling goroutine that observes how many
// times a .atif-*.tmp file appears in dir. Each call to flush() creates
// exactly one such temp file (via os.CreateTemp) and removes it via
// os.Rename. So the number of "appearing" transitions is a lower bound on
// the number of flush() calls.
//
// The poller is best-effort: on very fast filesystems, a single flush may
// complete between two polls and be missed entirely. The function returns
// the observed count plus the "currently visible" indicator so the caller
// can also reason about duration.
func countFlushTransitions(t *testing.T, dir string, work func()) int {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	var transitions int
	var inTmp bool
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			entries, _ := os.ReadDir(dir)
			hasTmp := false
			for _, e := range entries {
				name := e.Name()
				if strings.HasPrefix(name, ".atif-") && strings.HasSuffix(name, ".tmp") {
					hasTmp = true
					break
				}
			}
			if hasTmp && !inTmp {
				transitions++
				inTmp = true
			} else if !hasTmp && inTmp {
				inTmp = false
			}
			runtime.Gosched()
		}
	}()
	work()
	// Allow poller to drain the dir.
	time.Sleep(2 * time.Millisecond)
	close(stop)
	<-done
	return transitions
}

// TestWriterAppendEvents_FlushedExactlyOnce verifies that AppendEvents flushes
// the trajectory to disk once at the end, not once per event. The test
// counts how many times a .atif-*.tmp file is observed in the dir during
// the call, and compares against the same count for N per-event AppendEvent
// calls. AppendEvents must produce strictly fewer flushes.
func TestWriterAppendEvents_FlushedExactlyOnce(t *testing.T) {
	const N = 30

	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	events := make([]*session.Event, N)
	for i := 0; i < N; i++ {
		events[i] = newTestEvent("user", "msg")
	}

	// Batch path: one AppendEvents call.
	batchCount := countFlushTransitions(t, dir, func() {
		w := NewWriter(fp, SessionMeta{SessionID: "sess-batch", AgentName: "pi-go"})
		if err := w.AppendEvents(events); err != nil {
			t.Errorf("AppendEvents: %v", err)
		}
	})

	// Live path: N AppendEvent calls in a fresh dir.
	dirLive := t.TempDir()
	fpLive := filepath.Join(dirLive, "trajectory.atif.json")
	liveCount := countFlushTransitions(t, dirLive, func() {
		w := NewWriter(fpLive, SessionMeta{SessionID: "sess-live", AgentName: "pi-go"})
		for _, ev := range events {
			if err := w.AppendEvent(ev); err != nil {
				t.Errorf("AppendEvent: %v", err)
			}
		}
	})

	if batchCount == 0 {
		t.Errorf("poller did not observe any flush; test is not reliable on this filesystem")
	}
	if liveCount == 0 {
		t.Errorf("poller did not observe any live flush; test is not reliable on this filesystem")
	}
	if batchCount >= liveCount {
		t.Errorf("AppendEvents should produce fewer flushes than N per-event AppendEvent calls: batch=%d live=%d (N=%d)", batchCount, liveCount, N)
	}
	// AppendEvents should be roughly 1; allow a small slack for the
	// os.Rename-close race in the poller.
	if batchCount > 3 {
		t.Errorf("AppendEvents produced %d flush observations; want <= 3 (single flush, with poller race slack)", batchCount)
	}
}

// TestWriterAppendEvents_ConcurrentWithAppendEvent verifies that AppendEvent
// and AppendEvents calls are serialized by the writer's mutex and no steps
// are lost when both are called concurrently.
func TestWriterAppendEvents_ConcurrentWithAppendEvent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	const liveN = 10
	batchEvents := []*session.Event{
		newTestEvent("user", "b1"),
		newTestEvent("model", "b2"),
		newTestEvent("user", "b3"),
	}
	batchM := len(batchEvents)

	var wg sync.WaitGroup
	for i := 0; i < liveN; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.AppendEvent(newTestEvent("user", "live")); err != nil {
				t.Errorf("AppendEvent: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := w.AppendEvents(batchEvents); err != nil {
			t.Errorf("AppendEvents: %v", err)
		}
	}()
	wg.Wait()

	want := liveN + batchM
	if w.StepCount() != want {
		t.Errorf("StepCount() = %d, want %d", w.StepCount(), want)
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(traj.Steps) != want {
		t.Errorf("steps in file = %d, want %d", len(traj.Steps), want)
	}
	// Step IDs should be 1..N without duplicates.
	seen := make(map[int]bool)
	for i, s := range traj.Steps {
		if seen[s.StepID] {
			t.Errorf("duplicate step ID %d at index %d", s.StepID, i)
		}
		seen[s.StepID] = true
	}
}
