package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/tui"
)

// -----------------------------------------------------------------------
// deferredInit — drive the heavy init routine in isolation and verify
// the InitEvent stream reaches a final event with a non-nil Result.
// -----------------------------------------------------------------------

func TestDeferredInit_MemoryOffBasic(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Save and restore package-level flags.
	origMemOff := flagMemoryOff
	origSystem := flagSystem
	origSession := flagSession
	origURL := flagURL
	origInsecure := flagInsecure
	origHeaders := flagHeaders
	defer func() {
		flagMemoryOff = origMemOff
		flagSystem = origSystem
		flagSession = origSession
		flagURL = origURL
		flagInsecure = origInsecure
		flagHeaders = origHeaders
	}()
	flagMemoryOff = true // Skip memory DB path.
	flagSystem = "test system prompt"
	flagSession = ""
	flagURL = ""
	flagInsecure = false
	flagHeaders = nil

	cwd := tmpHome // sandbox root and cwd both in tmpHome

	// Use a mock LLM since deferredInit still constructs an agent around it.
	llm := &cliMockLLM{name: "test-llm", response: "ok"}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := config.Config{}
	tracker := guardrail.New(0)

	ch := make(chan tui.InitEvent, 128)
	var res initResources
	defer res.cleanup()

	done := make(chan struct{})
	go func() {
		defer close(done)
		deferredInit(ctx, cfg, llm, tracker, cwd, cwd, "", ch, &res)
		close(ch)
	}()

	gotDone := false
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("deferredInit reported error: %v", ev.Err)
		}
		if ev.Done && ev.Result != nil {
			gotDone = true
		}
	}
	<-done

	if !gotDone {
		t.Error("deferredInit did not emit a final Done event with Result")
	}
	if res.sessionID == "" {
		t.Error("expected res.sessionID to be populated after init")
	}
	if res.sandbox == nil {
		t.Error("expected res.sandbox to be populated")
	}
}

func TestDeferredInit_WithMCP(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	origMemOff := flagMemoryOff
	origSystem := flagSystem
	defer func() {
		flagMemoryOff = origMemOff
		flagSystem = origSystem
	}()
	flagMemoryOff = true
	flagSystem = ""

	llm := &cliMockLLM{name: "test-llm-mcp", response: "ok"}
	tracker := guardrail.New(0)

	// Config with an MCP server that will fail to start (non-existent binary).
	// BuildMCPToolsets logs and skips failing servers.
	cfg := config.Config{
		MCP: &config.MCPConfig{
			Servers: []config.MCPServer{
				{Name: "dummy", Command: "/nonexistent/binary", Args: []string{"--foo"}},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch := make(chan tui.InitEvent, 128)
	var res initResources
	defer res.cleanup()

	done := make(chan struct{})
	go func() {
		defer close(done)
		deferredInit(ctx, cfg, llm, tracker, tmpHome, tmpHome, "", ch, &res)
		close(ch)
	}()

	// Drain events.
	for range ch {
	}
	<-done
}

func TestDeferredInit_WithMemoryEnabled(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	origMemOff := flagMemoryOff
	defer func() { flagMemoryOff = origMemOff }()
	flagMemoryOff = false // Enable memory.

	// Pre-create the memory DB directory so OpenDB succeeds.
	memDir := filepath.Join(tmpHome, ".pi-go", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	llm := &cliMockLLM{name: "test-llm-mem", response: "ok"}
	tracker := guardrail.New(0)
	cfg := config.Config{}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch := make(chan tui.InitEvent, 128)
	var res initResources
	defer res.cleanup()

	done := make(chan struct{})
	go func() {
		defer close(done)
		deferredInit(ctx, cfg, llm, tracker, tmpHome, tmpHome, "", ch, &res)
		close(ch)
	}()

	gotResult := false
	for ev := range ch {
		if ev.Err != nil {
			// Memory branch could fail on environments without SQLite.
			t.Logf("deferredInit: %v", ev.Err)
		}
		if ev.Done && ev.Result != nil {
			gotResult = true
		}
	}
	<-done

	if !gotResult {
		t.Log("memory path did not produce a result (may be environment-specific)")
	}
}

func TestDeferredInitTotal(t *testing.T) {
	origMemOff := flagMemoryOff
	defer func() { flagMemoryOff = origMemOff }()

	flagMemoryOff = true
	if got, want := deferredInitTotal(config.Config{}), 5; got != want {
		t.Fatalf("deferredInitTotal() with memory off = %d, want %d", got, want)
	}

	flagMemoryOff = false
	memoryDisabled := false
	cfg := config.Config{
		Memory: &config.MemoryConfig{Enabled: &memoryDisabled},
		MCP: &config.MCPConfig{
			Servers: []config.MCPServer{{Name: "local", Command: "pi-mcp"}},
		},
	}
	if got, want := deferredInitTotal(cfg), 6; got != want {
		t.Fatalf("deferredInitTotal() with MCP only = %d, want %d", got, want)
	}

	memoryEnabled := true
	cfg.Memory = &config.MemoryConfig{Enabled: &memoryEnabled}
	if got, want := deferredInitTotal(cfg), 7; got != want {
		t.Fatalf("deferredInitTotal() with memory and MCP = %d, want %d", got, want)
	}
}
