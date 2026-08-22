// Tests for run-mode selection and the interactive / non-interactive paths.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/testenv"
)

func TestResolveMode_ExplicitFlag(t *testing.T) {
	resetGlobalFlags(t)
	flagMode = "rpc"
	if got := resolveMode(); got != "rpc" {
		t.Errorf("resolveMode() = %q, want %q", got, "rpc")
	}
}

func TestResolveMode_EmptyFallsBackToDetect(t *testing.T) {
	resetGlobalFlags(t)
	flagMode = ""
	got := resolveMode()
	// Under go test stdin is a pipe so it should be print, but allow both.
	if got != "print" && got != "interactive" {
		t.Errorf("resolveMode() = %q, want 'print' or 'interactive'", got)
	}
}

func TestDetectMode_PipedStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()

	mode := detectMode()
	if mode != "print" {
		t.Errorf("detectMode with piped stdin = %q, want 'print'", mode)
	}
}

func TestRunInteractive_CancelContext(t *testing.T) {
	if raceEnabled {
		t.Skip("Bubble Tea/cancelreader shutdown races under -race in this TTY simulation")
	}
	resetGlobalFlags(t)
	flagMemoryOff = true
	tmpHome := t.TempDir()
	testenv.SetHome(t, tmpHome)

	llm := &cliMockLLM{name: "test-interactive", response: "ok"}
	cfg := config.Config{}
	tracker := guardrail.New(0)

	// info value needs Provider set.
	info := provInfo("openai", "gpt-5.4")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- runInteractive(ctx, cfg, llm, info, tracker, "default", tmpHome, tmpHome, "")
	}()

	// Give it a moment to start, then cancel. tui.Run requires a real TTY
	// which will fail quickly in test env.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Either nil or an error from TUI is acceptable.
	case <-time.After(30 * time.Second):
		t.Fatal("runInteractive did not exit in time")
	}
}

// provInfo builds a provider.Info for tests.
//
// This used to be a structurally-identical shim, on the reasoning that
// importing provider.Info "just duplicates setup". The shim silently became a
// compile error the first time a field was added to the real struct, which is
// the failure mode a mirror type always has. Use the real one.
func provInfo(providerName, model string) provider.Info {
	return provider.Info{Provider: providerName, Model: model}
}

func TestRunNonInteractive_JSONEmptyPromptEarlyExit(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagMemoryOff = true

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "json", "--memory-off"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunNonInteractive_PrintEmptyPromptEarlyExit(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagMemoryOff = true

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "print", "--memory-off"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunNonInteractive_WithSystemAndHooks(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{
		"roles": {"default": {"model":"gpt-5.4","provider":"openai"}},
		"hooks": [{"event":"before_tool","command":"echo x","tools":["read"]}],
		"memory": {"enabled": false}
	}`), 0o644)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--system", "custom system prompt", "--mode", "print"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDetectMode_AllPaths(t *testing.T) {
	mode := detectMode()
	if mode != "print" && mode != "interactive" {
		t.Errorf("detectMode() = %q, want 'print' or 'interactive'", mode)
	}
}
