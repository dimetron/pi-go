package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"

	pisession "github.com/dimetron/pi-go/internal/session"
)

// TestResolveSessionID covers the two flag-driven branches. Both run before
// any agent call, so they are reachable without a live model — and the
// --continue miss is the one a user hits on a fresh machine, where a wrong
// answer means silently starting a new session instead of saying so.
func TestResolveSessionID(t *testing.T) {
	t.Run("--session wins and is returned verbatim", func(t *testing.T) {
		resetGlobalFlags(t)
		flagSession = "explicit-session-id"

		// The agent and service are never touched on this path; nil proves it.
		got, err := resolveSessionID(t.Context(), nil, nil)
		if err != nil {
			t.Fatalf("resolveSessionID: %v", err)
		}
		if got != "explicit-session-id" {
			t.Fatalf("got %q, want %q", got, "explicit-session-id")
		}
	})

	t.Run("--continue with no prior session is an error, not a silent new one", func(t *testing.T) {
		resetGlobalFlags(t)
		flagContinue = true

		svc, err := pisession.NewFileService(t.TempDir())
		if err != nil {
			t.Fatalf("NewFileService: %v", err)
		}

		got, err := resolveSessionID(t.Context(), nil, svc)
		if err == nil {
			t.Fatalf("resolveSessionID returned %q, want an error", got)
		}
		if !strings.Contains(err.Error(), "no previous session") {
			t.Fatalf("err = %v, want it to name the missing previous session", err)
		}
		if got != "" {
			t.Fatalf("got %q alongside the error, want empty", got)
		}
	})

	t.Run("--continue takes precedence over --session", func(t *testing.T) {
		resetGlobalFlags(t)
		flagContinue = true
		flagSession = "ignored-because-continue-wins"

		svc, err := pisession.NewFileService(t.TempDir())
		if err != nil {
			t.Fatalf("NewFileService: %v", err)
		}

		// An empty store means --continue fails; if --session were checked
		// first this would succeed instead, which is the regression to catch.
		if _, err := resolveSessionID(t.Context(), nil, svc); err == nil {
			t.Fatal("--session shadowed --continue; want the --continue error")
		}
	})
}

// TestDispatchModeNoPrompt covers the empty-prompt guard. Every mode except
// rpc needs a prompt, and the guard has to report that rather than handing an
// empty turn to the model.
func TestDispatchModeNoPrompt(t *testing.T) {
	for _, mode := range []string{"print", "json", ""} {
		t.Run("mode="+mode, func(t *testing.T) {
			resetGlobalFlags(t)

			var err error
			out := captureStderr(t, func() {
				err = dispatchMode(context.Background(), mode, "", nil, "sid", nil, "test-model", config.Config{}, nil)
			})

			if err != nil {
				t.Fatalf("dispatchMode with no prompt = %v, want nil", err)
			}
			if !strings.Contains(out, "no prompt provided") {
				t.Fatalf("stderr = %q, want it to mention the missing prompt", out)
			}
			// The message exists so the user can tell which model/mode was
			// about to run; dropping either makes it useless.
			if !strings.Contains(out, "test-model") {
				t.Errorf("stderr %q omits the model name", out)
			}
		})
	}
}
