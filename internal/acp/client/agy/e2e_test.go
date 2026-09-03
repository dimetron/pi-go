//go:build e2e

package agy

import (
	"os"
	"strings"
	"testing"
	"time"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// envE2E gates the credentialed run. The ACP server refuses session/new until
// an auth method is chosen in ~/.gemini/antigravity-acp/settings.json, and
// oauth-personal then blocks on an interactive browser login — which would
// hang an unattended `make test-e2e` for the full deadline rather than fail.
const envE2E = "PI_ACP_AGY_E2E"

// requireAgyServer skips the test unless the Antigravity ACP server is
// installed (scripts/install-agy-acp.sh) and the caller has opted in. These
// tests spawn the real server and make model calls, so they run only under
// `make test-e2e` with PI_ACP_AGY_E2E set.
func requireAgyServer(t *testing.T) {
	t.Helper()
	if _, err := findBinary(DefaultBinaryPaths); err != nil {
		t.Skipf("skipping: %v", err)
	}
	if os.Getenv(envE2E) == "" {
		t.Skipf("skipping: set %s=1 once Antigravity is authenticated", envE2E)
	}
}

// TestE2EAgyTurn drives a full turn against the real ACP server: spawn,
// initialize, session/new, prompt, streamed events, result.
func TestE2EAgyTurn(t *testing.T) {
	requireAgyServer(t)

	r := Runner{}
	sess, err := r.Start(t.Context(), RunRequest{
		Prompt: "Reply with exactly: PIGO_OK. Do not run any commands or edit any files.",
		CWD:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var text strings.Builder
	deadline := time.After(3 * time.Minute)
drain:
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				break drain
			}
			t.Logf("event: type=%s content=%q err=%q", ev.Type, ev.Content, ev.Error)
			if ev.Type == shared.EventTypeMessage {
				text.WriteString(ev.Content)
			}
		case <-deadline:
			_ = sess.Cancel()
			t.Fatal("timed out waiting for the turn to complete")
		}
	}

	result := sess.Wait()
	if strings.Contains(result.Error, "Authentication required") {
		t.Skipf("skipping: Antigravity ACP server is not authenticated: %s", result.Error)
	}
	if result.Status != shared.StatusSuccess {
		t.Fatalf("status = %q, want %q (error: %q, stderr: %q)",
			result.Status, shared.StatusSuccess, result.Error, result.Stderr)
	}
	if strings.TrimSpace(text.String()) == "" && strings.TrimSpace(result.Result) == "" {
		t.Error("no agent message streamed back")
	}
}
