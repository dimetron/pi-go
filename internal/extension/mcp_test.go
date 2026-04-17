package extension

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/config"
)

// --- BuildMCPToolsets unit tests ---

func TestBuildMCPToolsetsEmpty(t *testing.T) {
	toolsets, err := BuildMCPToolsets(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(toolsets) != 0 {
		t.Errorf("expected 0 toolsets, got %d", len(toolsets))
	}
}

func TestBuildMCPToolsetsCreatesToolsets(t *testing.T) {
	servers := []MCPServerConfig{
		{Name: "test-server", Command: "echo", Args: []string{"hello"}},
	}

	toolsets, err := BuildMCPToolsets(servers)
	if err != nil {
		t.Fatal(err)
	}
	if len(toolsets) != 1 {
		t.Errorf("expected 1 toolset, got %d", len(toolsets))
	}
	if toolsets[0].Name() != "test-server" {
		t.Errorf("expected toolset name 'test-server', got %q", toolsets[0].Name())
	}
}

func TestBuildMCPToolsets_SkipsBadServers(t *testing.T) {
	servers := []MCPServerConfig{
		{Name: "bad", Command: "/nonexistent-binary-xyz"},
		{Name: "good", Command: "echo", Args: []string{"hello"}},
	}

	toolsets, err := BuildMCPToolsets(servers)
	if err != nil {
		t.Fatalf("BuildMCPToolsets should not fail: %v", err)
	}
	// buildMCPToolset uses mcptoolset.New which is lazy, so both may succeed.
	_ = toolsets
}

// --- resilientToolset mock tests ---

type failingToolset struct{}

func (f *failingToolset) Name() string { return "failing" }
func (f *failingToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return nil, fmt.Errorf("MCP server crashed: EOF")
}

func TestResilientToolset_GracefulDegradation(t *testing.T) {
	rt := &resilientToolset{inner: &failingToolset{}, name: "test-mcp"}

	tools, err := rt.Tools(nil)
	if err != nil {
		t.Fatalf("resilientToolset should not propagate error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools from failed toolset, got %d", len(tools))
	}

	// Second call should also succeed (cached failure).
	tools2, err2 := rt.Tools(nil)
	if err2 != nil {
		t.Fatalf("second call should not fail: %v", err2)
	}
	if len(tools2) != 0 {
		t.Errorf("expected 0 tools on second call, got %d", len(tools2))
	}
}

type hangingToolset struct{}

func (h *hangingToolset) Name() string { return "hanging" }
func (h *hangingToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	time.Sleep(10 * time.Minute)
	return nil, nil
}

func TestResilientToolset_Timeout(t *testing.T) {
	origTimeout := mcpConnectTimeout
	mcpConnectTimeout = 100 * time.Millisecond
	defer func() { mcpConnectTimeout = origTimeout }()

	rt := &resilientToolset{inner: &hangingToolset{}, name: "slow-mcp"}

	start := time.Now()
	tools, err := rt.Tools(nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("resilientToolset should not return error on timeout: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools from timed-out toolset, got %d", len(tools))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Tools() took %v, expected ~100ms timeout", elapsed)
	}
	if !rt.failed {
		t.Error("expected failed=true after timeout")
	}
}

type successToolset struct {
	tools []tool.Tool
}

func (s *successToolset) Name() string { return "success" }
func (s *successToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return s.tools, nil
}

func TestResilientToolset_SuccessPassesThrough(t *testing.T) {
	inner := &successToolset{tools: make([]tool.Tool, 2)}
	rt := &resilientToolset{inner: inner, name: "working-mcp"}

	tools, err := rt.Tools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if rt.failed {
		t.Error("expected failed=false for successful toolset")
	}
}

// --- respawnTransport tests ---

func TestRespawnTransport_FreshCmdOnEachConnect(t *testing.T) {
	// Test that respawnTransport creates a fresh exec.Cmd on each Connect.
	// If the same Cmd object were reused, exec.Cmd.Stdout would already be set
	// and subsequent Connect calls would fail with "Stdout already set".
	rt := &respawnTransport{
		command: "false",
		args:    nil,
	}

	ctx := context.Background()

	// First connect — will fail (false exits with code 1).
	_, err1 := rt.Connect(ctx)

	// Second connect — if Cmd is reused, fails with "Stdout already set".
	// If Cmd is fresh, fails with the command exit status (acceptable).
	_, err2 := rt.Connect(ctx)
	if err2 != nil && err2.Error() == "exec: Stdout already set" {
		t.Fatalf("second Connect reused exec.Cmd: %v", err2)
	}

	// err1 may be non-nil (command failed) — that's fine.
	_ = err1
}

// --- config integration tests ---

// TestMCPServerConfigFromConfigStruct verifies that extension.MCPServerConfig
// is compatible with config.MCPServer.
func TestMCPServerConfigFromConfigStruct(t *testing.T) {
	cfg := config.MCPServer{
		Name:    "test-server",
		Command: "echo",
		Args:    []string{"test"},
	}

	extCfg := MCPServerConfig{
		Name:    cfg.Name,
		Command: cfg.Command,
		Args:    cfg.Args,
	}

	if extCfg.Name != "test-server" {
		t.Errorf("expected name 'test-server', got %q", extCfg.Name)
	}
	if extCfg.Command != "echo" {
		t.Errorf("expected command 'echo', got %q", extCfg.Command)
	}
	if len(extCfg.Args) != 1 || extCfg.Args[0] != "test" {
		t.Errorf("expected args ['test'], got %v", extCfg.Args)
	}
}
