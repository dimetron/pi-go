//go:build e2e

package codex

import (
	"strings"
	"testing"
	"time"
)

// requireCodex skips the test unless a real codex CLI is installed. These tests
// spawn `codex app-server` for real and make model calls, so they run only
// under `make test-e2e`.
func requireCodex(t *testing.T) {
	t.Helper()
	if _, err := findBinary(DefaultBinaryPaths); err != nil {
		t.Skipf("skipping: %v", err)
	}
}

// TestE2ECodexTurn drives a full turn against the real app-server: spawn,
// initialize, thread/start, turn/start, stream items, turn/completed.
func TestE2ECodexTurn(t *testing.T) {
	requireCodex(t)

	sess, err := NewSession(t.Context(), SessionOpts{
		CWD:     t.TempDir(),
		Prompt:  "Reply with exactly: PIGO_OK. Do not run any commands.",
		Sandbox: SandboxReadOnly,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var sawMessage bool
	deadline := time.After(3 * time.Minute)
drain:
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				break drain
			}
			t.Logf("event: type=%s content=%q err=%q", ev.Type, ev.Content, ev.Error)
			if ev.Type == EventTypeMessage {
				sawMessage = true
			}
		case <-deadline:
			_ = sess.Cancel()
			t.Fatal("timed out waiting for the turn to complete")
		}
	}

	result := sess.Wait()
	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, want %q (error: %q, stderr: %q)", result.Status, StatusSuccess, result.Error, result.Stderr)
	}
	if !sawMessage {
		t.Error("no agent message streamed back")
	}
	if !strings.Contains(result.Result, "PIGO_OK") {
		t.Errorf("result = %q, want it to contain PIGO_OK", result.Result)
	}
	if result.StopReason != TurnCompleted {
		t.Errorf("stopReason = %q, want %q", result.StopReason, TurnCompleted)
	}
	if result.SessionID == "" {
		t.Error("no thread id on the result")
	}
}

// TestE2ECodexCancel verifies Cancel interrupts a real in-flight turn and
// terminates the subprocess.
func TestE2ECodexCancel(t *testing.T) {
	requireCodex(t)

	sess, err := NewSession(t.Context(), SessionOpts{
		CWD:     t.TempDir(),
		Prompt:  "Count slowly from 1 to 500, one number per line.",
		Sandbox: SandboxReadOnly,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Let the turn actually start so the interrupt has something to hit.
	waitFor(t, func() bool { return sess.TurnID() != "" }, "turn to start")

	if err := sess.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	select {
	case <-sess.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("Cancel did not terminate the session")
	}

	result := sess.Wait()
	if result.Status != StatusError {
		t.Errorf("status = %q, want %q for a canceled turn", result.Status, StatusError)
	}
}
