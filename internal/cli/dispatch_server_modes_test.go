package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
)

// withStdinLines replaces os.Stdin with a pipe carrying the given NDJSON lines
// and closes it, so a server reading stdin sees the commands and then EOF.
// dispatchMode wires the stdio server to os.Stdin directly, so this is the only
// seam a test has.
func withStdinLines(t *testing.T, lines ...string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})

	go func() {
		defer func() { _ = w.Close() }()
		for _, line := range lines {
			if _, err := w.WriteString(line + "\n"); err != nil {
				return
			}
		}
	}()
}

// shortSocketPath returns a socket path short enough for the 104-byte sun_path
// limit, which t.TempDir() paths can exceed on macOS.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pisock")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// withSocketChanged sets the explicit-use flag and restores it, since
// resetGlobalFlags does not cover it.
func withSocketChanged(t *testing.T, changed bool) {
	t.Helper()
	orig := flagSocketChanged
	flagSocketChanged = changed
	t.Cleanup(func() { flagSocketChanged = orig })
}

// "socket" is pi-go's own JSON-RPC over a Unix socket. It has to bind the path
// from --socket, or an editor pointed at that path never connects.
func TestDispatchModeSocketServesTheConfiguredPath(t *testing.T) {
	resetGlobalFlags(t)
	withSocketChanged(t, false)
	flagSocket = shortSocketPath(t)

	ctx, cancel := context.WithCancel(context.Background())
	bound := make(chan struct{})
	go func() {
		// The listener exists as soon as Run gets past net.Listen; poll for the
		// socket file rather than guessing at a sleep.
		defer close(bound)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(flagSocket); err == nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = captureStderr(t, func() {
			err = dispatchMode(ctx, "socket", "", nil, "sid", nil, "test-model", config.Config{}, nil)
		})
	}()

	<-bound
	if _, statErr := os.Stat(flagSocket); statErr != nil {
		cancel()
		<-done
		t.Fatalf("socket %s was never created: %v", flagSocket, statErr)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("socket server did not shut down on context cancellation")
	}
	if err != nil {
		t.Errorf("dispatchMode(socket) = %v, want nil on shutdown", err)
	}
}

// `--mode rpc --socket` was the pre-rename spelling for the Unix socket
// server. Silently starting the stdio server instead would leave the caller's
// socket client hanging, so the deprecated spelling must still be honored —
// with a warning.
func TestDispatchModeRPCWithExplicitSocketFallsBackToSocketMode(t *testing.T) {
	resetGlobalFlags(t)
	withSocketChanged(t, true)
	flagSocket = shortSocketPath(t)

	ctx, cancel := context.WithCancel(context.Background())

	var err error
	var stderr string
	done := make(chan struct{})
	go func() {
		defer close(done)
		stderr = captureStderr(t, func() {
			err = dispatchMode(ctx, "rpc", "", nil, "sid", nil, "test-model", config.Config{}, nil)
		})
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(flagSocket); statErr == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	created := false
	if _, statErr := os.Stat(flagSocket); statErr == nil {
		created = true
	}

	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down on context cancellation")
	}

	if !created {
		t.Error("`--mode rpc --socket` did not start the socket server; a socket client would hang")
	}
	if err != nil {
		t.Errorf("dispatchMode = %v, want nil on shutdown", err)
	}
	if !strings.Contains(stderr, "deprecated") {
		t.Errorf("stderr = %q, want a deprecation warning for `--mode rpc --socket`", stderr)
	}
}

// Plain `--mode rpc` is the stdio NDJSON protocol pi-acp drives. Every command
// must be answered on stdout, and the server must exit when stdin closes.
func TestDispatchModeRPCServesTheStdioProtocol(t *testing.T) {
	resetGlobalFlags(t)
	withSocketChanged(t, false)

	withStdinLines(t,
		`{"type":"get_state","id":"s1"}`,
		`{"type":"get_available_models","id":"s2"}`,
	)

	var err error
	out := captureStdout(t, func() {
		err = dispatchMode(context.Background(), "rpc", "", nil, "sess-1", nil, "test-model", config.Config{}, nil)
	})
	if err != nil {
		t.Fatalf("dispatchMode(rpc) = %v, want nil when stdin closes", err)
	}

	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var resp map[string]any
		if jsonErr := json.Unmarshal([]byte(line), &resp); jsonErr != nil {
			t.Fatalf("stdout line is not JSON: %q: %v", line, jsonErr)
		}
		id, _ := resp["id"].(string)
		ids = append(ids, id)
	}

	if len(ids) != 2 || ids[0] != "s1" || ids[1] != "s2" {
		t.Errorf("response ids = %v, want [s1 s2]; pi-acp pairs replies by id", ids)
	}
}

// set_model reaches buildSwitchedLLM through the switcher dispatchMode wires
// in. An unknown model must come back as an error response rather than a
// cosmetic success, or the client renders a switch that never happened.
//
// A real agent is required: pirpc refuses to call the switcher at all when it
// has no agent to rebuild, which would bypass the wiring under test.
func TestDispatchModeRPCSetModelRejectsUnknownModels(t *testing.T) {
	resetGlobalFlags(t)
	withSocketChanged(t, false)

	ag, sessionID := newTestAgent(t, &cliMockLLM{name: "test-model", response: "hi"})
	// buildSwitchedLLM records the new model's context window on the tracker,
	// so the switcher needs the real one runNonInteractive always supplies.
	tracker := guardrail.New(0)
	withStdinLines(t, `{"type":"set_model","id":"m1","modelId":"not-a-real-model-xyz","provider":"openai"}`)

	var err error
	out := captureStdout(t, func() {
		err = dispatchMode(context.Background(), "rpc", "", ag, sessionID, nil, "test-model", config.Config{}, tracker)
	})
	if err != nil {
		t.Fatalf("dispatchMode(rpc) = %v, want nil when stdin closes", err)
	}

	line := strings.TrimSpace(out)
	if line == "" {
		t.Fatal("set_model produced no response; the client would wait forever")
	}
	var resp map[string]any
	if jsonErr := json.Unmarshal([]byte(line), &resp); jsonErr != nil {
		t.Fatalf("stdout line is not JSON: %q: %v", line, jsonErr)
	}
	if resp["success"] != false {
		t.Errorf("success = %v, want false for an unknown model", resp["success"])
	}
	if resp["error"] == nil {
		t.Error("error field missing; the client cannot tell the switch failed")
	}
}

// The provider the ACP client names has to override the default role's
// provider. Without that substitution pi-go infers the provider from the
// configured default, which routes e.g. an OpenAI model through Ollama.
func TestDispatchModeRPCSetModelHonorsTheNamedProvider(t *testing.T) {
	resetGlobalFlags(t)
	withSocketChanged(t, false)

	ag, sessionID := newTestAgent(t, &cliMockLLM{name: "test-model", response: "hi"})
	// buildSwitchedLLM records the new model's context window on the tracker,
	// so the switcher needs the real one runNonInteractive always supplies.
	tracker := guardrail.New(0)

	// A default role pinned to a different provider is the stale pin the
	// substitution exists to defeat.
	cfg := config.Config{Roles: map[string]config.RoleConfig{
		"default": {Model: "some-ollama-model", Provider: "ollama"},
	}}

	withStdinLines(t, `{"type":"set_model","id":"m1","modelId":"gpt-5.5","provider":"openai"}`)

	var err error
	out := captureStdout(t, func() {
		err = dispatchMode(context.Background(), "rpc", "", ag, sessionID, nil, "test-model", cfg, tracker)
	})
	if err != nil {
		t.Fatalf("dispatchMode(rpc) = %v, want nil when stdin closes", err)
	}

	var resp map[string]any
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); jsonErr != nil {
		t.Fatalf("stdout line is not JSON: %q: %v", out, jsonErr)
	}

	// The switch itself needs credentials this test does not have, so either
	// outcome is acceptable — what must not happen is the request being routed
	// to ollama, which would name that provider in the error.
	if resp["success"] == true {
		data, ok := resp["data"].(map[string]any)
		if !ok {
			t.Fatalf("data has type %T, want the state map", resp["data"])
		}
		if data["provider"] == "ollama" {
			t.Errorf("provider = ollama; the client's choice of openai was ignored")
		}
		return
	}
	if msg, _ := resp["error"].(string); strings.Contains(strings.ToLower(msg), "ollama") {
		t.Errorf("error = %q; the request was routed to the default role's provider, not the named one", msg)
	}

	// The caller's config must not be mutated: the switcher clones Roles
	// precisely so a switch cannot rewrite the session's default.
	if cfg.Roles["default"].Provider != "ollama" {
		t.Errorf("caller config was mutated: default provider = %q, want ollama", cfg.Roles["default"].Provider)
	}
}
