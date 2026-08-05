package atif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
		nil,                             // empty
		{Author: "user"},                // empty (no content)
		newTestEvent("user", "real-1"),  // step 1
		{Author: "model"},               // empty
		newTestEvent("model", "real-2"), // step 2
		nil,                             // empty
		newTestEvent("user", "real-3"),  // step 3
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

// runAndCountFlushes runs work() against a fresh Writer and returns the number
// of flushes the writer performed, sampled directly from Writer.FlushCount
// before and after the call. This is race-free — it does not depend on
// file-system polling — unlike the previous countFlushTransitions helper,
// which raced against os.Rename and missed flushes on fast filesystems.
func runAndCountFlushes(t *testing.T, fp string, meta SessionMeta, work func(w *Writer)) int {
	t.Helper()
	w := NewWriter(fp, meta)
	before := w.FlushCount()
	work(w)
	return w.FlushCount() - before
}

// TestWriterAppendEvents_FlushedExactlyOnce verifies that AppendEvents flushes
// the trajectory to disk exactly once, not once per event. The number of
// flushes is read directly from Writer.FlushCount (no filesystem polling),
// so the assertions are exact on every filesystem.
func TestWriterAppendEvents_FlushedExactlyOnce(t *testing.T) {
	const N = 30

	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	events := make([]*session.Event, N)
	for i := 0; i < N; i++ {
		events[i] = newTestEvent("user", "msg")
	}

	// Batch path: one AppendEvents call.
	batchCount := runAndCountFlushes(t, fp, SessionMeta{SessionID: "sess-batch", AgentName: "pi-go"}, func(w *Writer) {
		if err := w.AppendEvents(events); err != nil {
			t.Errorf("AppendEvents: %v", err)
		}
	})

	// Live path: N AppendEvent calls in a fresh dir.
	dirLive := t.TempDir()
	fpLive := filepath.Join(dirLive, "trajectory.atif.json")
	liveCount := runAndCountFlushes(t, fpLive, SessionMeta{SessionID: "sess-live", AgentName: "pi-go"}, func(w *Writer) {
		for _, ev := range events {
			if err := w.AppendEvent(ev); err != nil {
				t.Errorf("AppendEvent: %v", err)
			}
		}
	})

	if batchCount != 1 {
		t.Errorf("AppendEvents produced %d flushes; want exactly 1 (one batch flush at the end)", batchCount)
	}
	if liveCount != N {
		t.Errorf("AppendEvent x%d produced %d flushes; want exactly %d (one per event)", N, liveCount, N)
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
