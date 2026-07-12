//go:build e2e

package subagent

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestE2EDispatchCodex spawns a real `codex app-server` through the dispatch
// path the orchestrator uses, exercising the filtered environment and the
// codex.Event → subagent.Event translation against the real CLI.
func TestE2EDispatchCodex(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("skipping: codex not installed: %v", err)
	}

	proc, err := dispatchCodex(t.Context(), SpawnOpts{
		Prompt:  "Reply with exactly: PIGO_OK. Do not run any commands.",
		WorkDir: t.TempDir(),
	}, "codex")
	if err != nil {
		t.Fatalf("dispatchCodex: %v", err)
	}

	var sawStart, sawText, sawEnd bool
	deadline := time.After(3 * time.Minute)
drain:
	for {
		select {
		case ev, ok := <-proc.Events():
			if !ok {
				break drain
			}
			t.Logf("event: type=%s content=%q", ev.Type, ev.Content)
			switch ev.Type {
			case "message_start":
				sawStart = true
			case "text_delta":
				sawText = true
			case "message_end":
				sawEnd = true
			}
		case <-deadline:
			proc.Cancel()
			t.Fatal("timed out waiting for the codex subagent")
		}
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !sawStart || !sawText || !sawEnd {
		t.Errorf("events: start=%v text=%v end=%v; want all three", sawStart, sawText, sawEnd)
	}
	if !strings.Contains(result, "PIGO_OK") {
		t.Errorf("result = %q, want it to contain PIGO_OK", result)
	}
}
