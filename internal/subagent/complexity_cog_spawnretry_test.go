package subagent

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
)

// These tests pin the retry behavior of (*Orchestrator).SpawnWithRetry before
// it was flattened for cognitive complexity: how many attempts run, which
// agent statuses count as retryable, that retries carry no backoff, what a
// canceled context does mid-sequence, and what is left on the returned event
// stream. They were written and run against the unrefactored function first,
// so the literals below are recordings of the original behavior rather than
// agreement with the new code.

// acpAttemptRecorder counts how many times the ACP session factory was asked
// for a session, which is one call per SpawnWithRetry attempt that reaches
// dispatchACP.
type acpAttemptRecorder struct {
	mu    sync.Mutex
	count int
}

func (r *acpAttemptRecorder) next() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.count
	r.count++
	return n
}

func (r *acpAttemptRecorder) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// installACPAttempts routes every ACP spawn through start, handing it the
// zero-based attempt number so a test can script a different outcome per
// attempt.
func installACPAttempts(t *testing.T, rec *acpAttemptRecorder, start func(attempt int) (acpSession, error)) {
	t.Helper()
	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, _, _ string, _ SpawnOpts) (acpSession, error) {
		return start(rec.next())
	}
	t.Cleanup(func() { startACPSessionFn = prev })
}

// forceRunningAgentStatus waits for the orchestrator to register a running
// agent and overwrites its status, returning the agent id.
//
// SpawnWithRetry samples the tracked status the moment it sees a terminal
// event, while forwardAgentEvents only writes that status after the process
// stream closes. Setting the status before the terminal event is emitted is
// what makes the crash branch deterministic instead of a scheduling race.
func forceRunningAgentStatus(t *testing.T, o *Orchestrator, status string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		for id, st := range o.agents {
			if st.Status == "running" {
				st.Status = status
				o.mu.Unlock()
				return id
			}
		}
		o.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Errorf("no running agent registered within 3s")
	return ""
}

// drainEventTypes reads an event stream to close and returns the types seen.
func drainEventTypes(events <-chan Event) []string {
	var types []string
	for ev := range events {
		types = append(types, ev.Type)
	}
	return types
}

func containsType(types []string, want string) bool {
	for _, ty := range types {
		if ty == want {
			return true
		}
	}
	return false
}

// TestSpawnWithRetry_AttemptCountPinned pins the attempt arithmetic when every
// spawn fails: MaxRetries is clamped to [0,3], the first try is not counted as
// a retry, and the reported count is retries+1.
func TestSpawnWithRetry_AttemptCountPinned(t *testing.T) {
	cases := []struct {
		name       string
		maxRetries int
		wantSpawns int
		wantErr    string
	}{
		{"negative clamps to zero", -1, 1, "spawn failed after 1 attempts"},
		{"zero is a single attempt", 0, 1, "spawn failed after 1 attempts"},
		{"one retry is two attempts", 1, 2, "spawn failed after 2 attempts"},
		{"three retries is four attempts", 3, 4, "spawn failed after 4 attempts"},
		{"above max clamps to three", 10, 4, "spawn failed after 4 attempts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &acpAttemptRecorder{}
			installACPAttempts(t, rec, func(int) (acpSession, error) {
				return nil, stubError("start refused")
			})

			orch := NewOrchestrator(testConfig(), "", nil)
			defer orch.Shutdown()

			events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
				Agent:      AgentConfig{Name: "claude", Role: "smol"},
				Prompt:     "hi",
				MaxRetries: tc.maxRetries,
			})
			if err == nil {
				t.Fatal("expected a spawn failure")
			}
			if got := err.Error(); !strings.Contains(got, tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", got, tc.wantErr)
			}
			if !errors.Is(err, stubError("start refused")) {
				t.Errorf("error %v does not wrap the underlying spawn error", err)
			}
			if events != nil {
				t.Error("expected a nil event stream on total spawn failure")
			}
			if agentID != "" {
				t.Errorf("agentID = %q, want empty on total spawn failure", agentID)
			}
			if got := rec.total(); got != tc.wantSpawns {
				t.Errorf("spawn attempts = %d, want %d", got, tc.wantSpawns)
			}
		})
	}
}

// TestSpawnWithRetry_NoBackoffBetweenAttempts pins that the retry loop
// re-spawns immediately. There is no sleep and no backoff schedule, so four
// failing attempts finish essentially instantly; a flattening that introduced
// a delay would show up here.
func TestSpawnWithRetry_NoBackoffBetweenAttempts(t *testing.T) {
	rec := &acpAttemptRecorder{}
	installACPAttempts(t, rec, func(int) (acpSession, error) {
		return nil, stubError("start refused")
	})

	orch := NewOrchestrator(testConfig(), "", nil)
	defer orch.Shutdown()

	start := time.Now()
	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 3,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a spawn failure")
	}
	if rec.total() != 4 {
		t.Fatalf("spawn attempts = %d, want 4", rec.total())
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("four attempts took %s; the loop retries with no backoff and should be near-instant", elapsed)
	}
}

// TestSpawnWithRetry_StatusClassification pins which tracked statuses count as
// a crash worth re-spawning. Only "failed" and "killed" retry; every other
// terminal status is handed back to the caller as a success.
func TestSpawnWithRetry_StatusClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		wantSpawns int
	}{
		{"failed retries", "failed", 2},
		{"killed retries", "killed", 2},
		{"canceled does not retry", "canceled", 1},
		{"completed does not retry", "completed", 1},
		{"timeout does not retry", "timeout", 1},
		{"unrecognized status does not retry", "running-observed", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orch := NewOrchestrator(testConfig(), "", nil)
			defer orch.Shutdown()

			var (
				idMu    sync.Mutex
				firstID string
			)
			rec := &acpAttemptRecorder{}
			installACPAttempts(t, rec, func(attempt int) (acpSession, error) {
				sess := newFakeACPSession()
				go func() {
					if attempt == 0 {
						id := forceRunningAgentStatus(t, orch, tc.status)
						idMu.Lock()
						firstID = id
						idMu.Unlock()
						sess.events <- sharedacp.Event{Type: sharedacp.EventTypeError, Error: "boom", SessionID: "s"}
						sess.finish(sharedacp.RunResult{Status: sharedacp.StatusError, Error: "boom", SessionID: "s"})
						return
					}
					sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "ok", SessionID: "s"}
					sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "ok", SessionID: "s"})
				}()
				return sess, nil
			})

			events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
				Agent:      AgentConfig{Name: "claude", Role: "smol"},
				Prompt:     "hi",
				MaxRetries: 1,
			})
			if err != nil {
				t.Fatalf("SpawnWithRetry: %v", err)
			}
			if events == nil {
				t.Fatal("expected a non-nil event stream")
			}
			if got := rec.total(); got != tc.wantSpawns {
				t.Errorf("spawn attempts = %d, want %d", got, tc.wantSpawns)
			}

			idMu.Lock()
			first := firstID
			idMu.Unlock()
			if tc.wantSpawns == 1 && agentID != first {
				t.Errorf("agentID = %q, want the first agent %q", agentID, first)
			}
			if tc.wantSpawns == 2 && agentID == first {
				t.Errorf("agentID = %q, want a different agent than the crashed %q", agentID, first)
			}

			drainEventTypes(events)
		})
	}
}

// TestSpawnWithRetry_CrashOnEveryAttempt pins the exhausted-retry failure: the
// error names the agent and the attempt count, the event stream comes back
// nil, and the agent id of the last attempt is still returned alongside it.
func TestSpawnWithRetry_CrashOnEveryAttempt(t *testing.T) {
	orch := NewOrchestrator(testConfig(), "", nil)
	defer orch.Shutdown()

	var (
		idMu sync.Mutex
		ids  []string
	)
	rec := &acpAttemptRecorder{}
	installACPAttempts(t, rec, func(int) (acpSession, error) {
		sess := newFakeACPSession()
		go func() {
			id := forceRunningAgentStatus(t, orch, "failed")
			idMu.Lock()
			ids = append(ids, id)
			idMu.Unlock()
			sess.events <- sharedacp.Event{Type: sharedacp.EventTypeError, Error: "boom", SessionID: "s"}
			sess.finish(sharedacp.RunResult{Status: sharedacp.StatusError, Error: "boom", SessionID: "s"})
		}()
		return sess, nil
	})

	events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 2,
	})
	if err == nil {
		t.Fatal("expected a crash error after every attempt failed")
	}
	if events != nil {
		t.Error("expected a nil event stream when the retries are exhausted")
	}
	if rec.total() != 3 {
		t.Errorf("spawn attempts = %d, want 3", rec.total())
	}

	idMu.Lock()
	got := append([]string(nil), ids...)
	idMu.Unlock()
	if len(got) != 3 {
		t.Fatalf("tracked %d agents, want 3", len(got))
	}
	if agentID != got[2] {
		t.Errorf("agentID = %q, want the last attempt's agent %q", agentID, got[2])
	}
	want := "subagent " + got[2] + " crashed after 3 attempts"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestSpawnWithRetry_SilentStreamRetriesThenSucceeds pins the third outcome of
// an attempt: the stream closes without ever emitting message_end or error.
// That is treated as retryable, and once the attempts run out the last stream
// is handed back with a nil error rather than a crash error.
//
// `true` exits immediately with no JSONL output, so the only event the
// forwarder produces is the synthesized run_done — which is neither of the two
// types the retry loop waits for.
func TestSpawnWithRetry_SilentStreamRetriesThenSucceeds(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` binary on PATH: %v", err)
	}

	orch := NewOrchestrator(testConfig(), "", nil)
	defer orch.Shutdown()
	orch.SetPiBinary(truePath)

	events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "explore", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("SpawnWithRetry: %v", err)
	}
	if agentID == "" {
		t.Fatal("expected a non-empty agent id")
	}
	if events == nil {
		t.Fatal("expected a non-nil event stream")
	}

	orch.mu.Lock()
	spawned := len(orch.agents)
	orch.mu.Unlock()
	if spawned != 3 {
		t.Errorf("registered agents = %d, want 3 (a silent stream is retried)", spawned)
	}

	drainEventTypes(events)
}

// TestSpawnWithRetry_ZeroRetriesLeavesStreamUndrained pins the difference the
// maxRetries==0 shortcut makes to the caller: with no retries configured the
// stream is returned untouched, so the caller still sees message_end. With
// retries configured the loop consumes events up to and including the terminal
// one, and the caller never sees it.
func TestSpawnWithRetry_ZeroRetriesLeavesStreamUndrained(t *testing.T) {
	cases := []struct {
		name            string
		maxRetries      int
		wantMessageEnd  bool
		wantSpawnCalls  int
		wantRunDoneLast bool
	}{
		{"zero retries hands back an undrained stream", 0, true, 1, true},
		{"retries consume the terminal event", 1, false, 1, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &acpAttemptRecorder{}
			installACPAttempts(t, rec, func(int) (acpSession, error) {
				sess := newFakeACPSession()
				go func() {
					sess.events <- sharedacp.Event{Type: sharedacp.EventTypeMessage, Content: "ok", SessionID: "s"}
					sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "ok", SessionID: "s"})
				}()
				return sess, nil
			})

			orch := NewOrchestrator(testConfig(), "", nil)
			defer orch.Shutdown()

			events, agentID, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
				Agent:      AgentConfig{Name: "claude", Role: "smol"},
				Prompt:     "hi",
				MaxRetries: tc.maxRetries,
			})
			if err != nil {
				t.Fatalf("SpawnWithRetry: %v", err)
			}
			if agentID == "" {
				t.Fatal("expected a non-empty agent id")
			}
			if got := rec.total(); got != tc.wantSpawnCalls {
				t.Errorf("spawn attempts = %d, want %d", got, tc.wantSpawnCalls)
			}

			types := drainEventTypes(events)
			if got := containsType(types, "message_end"); got != tc.wantMessageEnd {
				t.Errorf("caller saw message_end = %v, want %v (events: %v)", got, tc.wantMessageEnd, types)
			}
			if tc.wantRunDoneLast {
				if len(types) == 0 || types[len(types)-1] != "run_done" {
					t.Errorf("last event = %v, want run_done", types)
				}
			}
		})
	}
}

// TestSpawnWithRetry_CanceledContextDoesNotShortCircuit pins what a context
// canceled partway through the retry sequence does: nothing special. The loop
// keeps re-spawning until the attempts run out, and each remaining attempt
// fails at spawn, so the caller gets the spawn-failure error naming the full
// attempt count rather than an early context error.
func TestSpawnWithRetry_CanceledContextDoesNotShortCircuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := NewOrchestrator(testConfig(), "", nil)
	defer orch.Shutdown()

	rec := &acpAttemptRecorder{}
	installACPAttempts(t, rec, func(attempt int) (acpSession, error) {
		if attempt > 0 {
			return nil, stubError("session start refused after cancel")
		}
		sess := newFakeACPSession()
		go func() {
			forceRunningAgentStatus(t, orch, "failed")
			cancel() // cancel between the first crash and the first retry
			sess.events <- sharedacp.Event{Type: sharedacp.EventTypeError, Error: "boom", SessionID: "s"}
			sess.finish(sharedacp.RunResult{Status: sharedacp.StatusError, Error: "boom", SessionID: "s"})
		}()
		return sess, nil
	})

	events, agentID, err := orch.SpawnWithRetry(ctx, SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 2,
	})
	if err == nil {
		t.Fatal("expected an error once the context was canceled")
	}
	if !strings.Contains(err.Error(), "spawn failed after 3 attempts") {
		t.Errorf("error = %q, want it to report all 3 attempts", err.Error())
	}
	if events != nil || agentID != "" {
		t.Errorf("events = %v, agentID = %q; want nil and empty", events, agentID)
	}
}

// TestSpawnWithRetry_ShutdownOrchestratorFailsEveryAttempt pins that a
// shut-down orchestrator is not a special case either: Spawn rejects every
// attempt and the loop still burns through the full budget.
func TestSpawnWithRetry_ShutdownOrchestratorFailsEveryAttempt(t *testing.T) {
	orch := NewOrchestrator(testConfig(), "", nil)
	orch.Shutdown()

	_, _, err := orch.SpawnWithRetry(context.Background(), SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 3,
	})
	if err == nil {
		t.Fatal("expected an error from a shut-down orchestrator")
	}
	if !strings.Contains(err.Error(), "spawn failed after 4 attempts") {
		t.Errorf("error = %q, want it to report all 4 attempts", err.Error())
	}
	if !strings.Contains(err.Error(), "orchestrator is shut down") {
		t.Errorf("error = %q, want the underlying shutdown error", err.Error())
	}
}
