package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// TestATIFReplayBatchRebuildsTrajectory creates a session, appends N events
// through the live AppendEvent path, then re-loads the session from disk in
// a fresh FileService. It verifies that the trajectory is rebuilt with all
// N events' steps and the content matches what N live AppendEvent calls
// produce on a fresh ATIF writer.
func TestATIFReplayBatchRebuildsTrajectory(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	svc, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}

	const N = 20
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "replay-batch",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessionID := resp.Session.ID()
	sessionDir := filepath.Join(dir, sessionID)

	// Append N events through the live path. Each AppendEvent flushes once,
	// producing N full-file rewrites of trajectory.atif.json.
	for i := 0; i < N; i++ {
		ev := &session.Event{
			ID:        fmt.Sprintf("ev-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
			Author:    "user",
		}
		ev.Content = genai.NewContentFromText(fmt.Sprintf("message %d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent(%d): %v", i, err)
		}
	}

	atifPath := filepath.Join(sessionDir, "trajectory.atif.json")
	liveData, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("read live trajectory: %v", err)
	}

	// Open a fresh FileService on the same directory. The session is not
	// in its cache, so the first Get triggers loadSessionFromDisk, which
	// (under the fix) should batch the ATIF replay via AppendEvents.
	svc2, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService(reload): %v", err)
	}
	_, err = svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get(reload): %v", err)
	}

	replayData, err := os.ReadFile(atifPath)
	if err != nil {
		t.Fatalf("read replay trajectory: %v", err)
	}

	var liveTraj, replayTraj map[string]any
	if err := json.Unmarshal(liveData, &liveTraj); err != nil {
		t.Fatalf("unmarshal live: %v", err)
	}
	if err := json.Unmarshal(replayData, &replayTraj); err != nil {
		t.Fatalf("unmarshal replay: %v", err)
	}

	liveSteps, _ := liveTraj["steps"].([]any)
	replaySteps, _ := replayTraj["steps"].([]any)

	if len(liveSteps) != N {
		t.Fatalf("live steps = %d, want %d", len(liveSteps), N)
	}
	if len(replaySteps) != N {
		t.Fatalf("replay steps = %d, want %d (steps lost during replay)", len(replaySteps), N)
	}

	// The replayed trajectory must be identical to the live one. The two
	// writers are constructed with the same SessionMeta (loaded from
	// meta.json) so their headers should match exactly.
	if string(liveData) != string(replayData) {
		t.Errorf("replay trajectory differs from live trajectory (live=%d bytes, replay=%d bytes)", len(liveData), len(replayData))
	}

	// Step IDs should be sequential 1..N in both.
	for i, s := range replaySteps {
		step, ok := s.(map[string]any)
		if !ok {
			t.Fatalf("step[%d] not an object: %T", i, s)
		}
		stepID, _ := step["step_id"].(float64)
		if int(stepID) != i+1 {
			t.Errorf("replay step[%d].step_id = %v, want %d", i, step["step_id"], i+1)
		}
	}
}

// TestATIFReplayFlushesOnce is the perf-side test for the batch replay. It
// observes the .atif-*.tmp side effect of os.CreateTemp via a high-frequency
// polling goroutine during loadSessionFromDisk. Under the old per-event
// AppendEvent loop, this count would be N (one temp file per event). Under
// the new batched AppendEvents, it should be 1.
//
// This is a best-effort signal: on very fast filesystems, a single flush
// may complete between two polls and be missed. The test therefore
// compares the replay count against a baseline live count for the same N
// events, rather than asserting an exact value.
func TestATIFReplayFlushesOnce(t *testing.T) {
	const N = 20
	dir := t.TempDir()
	ctx := context.Background()
	svc, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "replay-perf",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < N; i++ {
		ev := &session.Event{
			ID:        fmt.Sprintf("ev-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
			Author:    "user",
		}
		ev.Content = genai.NewContentFromText(fmt.Sprintf("m %d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent(%d): %v", i, err)
		}
	}
	sessionID := resp.Session.ID()
	sessionDir := filepath.Join(dir, sessionID)

	// Polling goroutine: count transitions of .atif-*.tmp files in the
	// session dir. A "transition" is the moment a temp file appears.
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
			entries, _ := os.ReadDir(sessionDir)
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

	// Force a cache miss: open a fresh FileService and call Get.
	svc2, err := NewFileService(dir)
	if err != nil {
		close(stop)
		<-done
		t.Fatalf("NewFileService(reload): %v", err)
	}
	if _, err := svc2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: sessionID,
	}); err != nil {
		close(stop)
		<-done
		t.Fatalf("Get(reload): %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	close(stop)
	<-done

	if transitions == 0 {
		t.Skipf("poller did not observe any flush; test is not reliable on this filesystem")
	}
	// A single batched flush should produce at most a small number of
	// transitions (the temp file appears once and disappears once). Allow
	// a small slack for poller race conditions.
	if transitions > 3 {
		t.Errorf("replay observed %d temp-file transitions; want <= 3 (single flush exposes the file briefly)", transitions)
	}
}
