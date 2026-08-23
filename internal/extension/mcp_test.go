package extension

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

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
	// Create proper tool instances (not nil slices)
	tools := []tool.Tool{
		&namedTool{nameVal: "tool1"},
		&namedTool{nameVal: "tool2"},
	}
	inner := &successToolset{tools: tools}
	rt := &resilientToolset{inner: inner, name: "working-mcp"}

	result, err := rt.Tools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}
	if rt.failed {
		t.Error("expected failed=false for successful toolset")
	}
}

// --- prefixedTool tests ---

// namedTool is a minimal tool.Tool implementation for testing.
type namedTool struct {
	nameVal string
}

func (t *namedTool) Name() string        { return t.nameVal }
func (t *namedTool) Description() string { return "test tool" }
func (t *namedTool) IsLongRunning() bool { return false }

func TestResilientToolset_ToolsKeepOriginalNames(t *testing.T) {
	inner := &successToolset{tools: []tool.Tool{
		&namedTool{nameVal: "search"},
		&namedTool{nameVal: "fetch"},
	}}
	rt := &resilientToolset{inner: inner, name: "docs-agent-client-protocol"}

	tools, err := rt.Tools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	// Verify tool names are NOT prefixed (keep original names)
	if tools[0].Name() != "search" {
		t.Errorf("expected original name 'search', got %q", tools[0].Name())
	}
	if tools[1].Name() != "fetch" {
		t.Errorf("expected original name 'fetch', got %q", tools[1].Name())
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
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"X-Api-Key": "abc-123"},
	}

	extCfg := MCPServerConfig{
		Name:    cfg.Name,
		Command: cfg.Command,
		Args:    cfg.Args,
		URL:     cfg.URL,
		Headers: cfg.Headers,
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
	if extCfg.Headers["X-Api-Key"] != "abc-123" {
		t.Errorf("expected header X-Api-Key=abc-123, got %q", extCfg.Headers["X-Api-Key"])
	}
}

// --- BuildMCPToolEntries tests ---

func TestBuildMCPToolEntries_EmptyToolsets(t *testing.T) {
	result := BuildMCPToolEntries(nil)
	if len(result) != 0 {
		t.Errorf("expected empty entries for nil toolsets, got %d", len(result))
	}
}

func TestBuildMCPToolEntries_FiltersFailed(t *testing.T) {
	// Build toolsets with one failed and one successful.
	inner := &successToolset{tools: []tool.Tool{&namedTool{nameVal: "test-tool"}}}
	rt := &resilientToolset{inner: inner, name: "test", failed: true}
	// Pre-load tools to simulate a previous Tools() call
	rt.tools = inner.tools

	result := BuildMCPToolEntries([]tool.Toolset{rt})
	if len(result) != 0 {
		t.Errorf("expected 0 entries for failed toolset, got %d", len(result))
	}
}

func TestBuildMCPToolEntries_SuccessCase(t *testing.T) {
	inner := &successToolset{tools: []tool.Tool{
		&namedTool{nameVal: "tool1"},
		&namedTool{nameVal: "tool2"},
	}}
	rt := &resilientToolset{inner: inner, name: "my-server"}
	// Pre-load tools to simulate a previous Tools() call
	rt.tools = inner.tools

	result := BuildMCPToolEntries([]tool.Toolset{rt})
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].Server != "my-server" || result[0].Tool != "tool1" {
		t.Errorf("unexpected entry: %+v", result[0])
	}
	if result[1].Server != "my-server" || result[1].Tool != "tool2" {
		t.Errorf("unexpected entry: %+v", result[1])
	}
}

// --- ToolsetStatuses tests ---

func TestToolsetStatuses_Empty(t *testing.T) {
	result := ToolsetStatuses(nil)
	if len(result) != 0 {
		t.Errorf("expected empty statuses, got %d", len(result))
	}
}

func TestToolsetStatuses_Pending(t *testing.T) {
	// A toolset that hasn't been queried yet is pending.
	// Use resilientToolset with empty tools slice (not loaded yet).
	inner := &successToolset{tools: nil}
	rt := &resilientToolset{inner: inner, name: "pending-server"}
	result := ToolsetStatuses([]tool.Toolset{rt})
	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	if result[0].Status != "pending" {
		t.Errorf("expected status 'pending', got %q", result[0].Status)
	}
}

func TestToolsetStatuses_Connected(t *testing.T) {
	// A resilientToolset with tools loaded is connected.
	inner := &successToolset{tools: []tool.Tool{&namedTool{nameVal: "tool1"}}}
	rt := &resilientToolset{inner: inner, name: "connected-server"}
	// Pre-load tools to simulate a successful Tools() call
	rt.tools = inner.tools
	result := ToolsetStatuses([]tool.Toolset{rt})
	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	if result[0].Status != "connected" {
		t.Errorf("expected status 'connected', got %q", result[0].Status)
	}
	if result[0].ToolCount != 1 {
		t.Errorf("expected ToolCount=1, got %d", result[0].ToolCount)
	}
}

func TestToolsetStatuses_Failed(t *testing.T) {
	// A resilientToolset with failed=true is failed.
	inner := &successToolset{tools: []tool.Tool{&namedTool{nameVal: "tool1"}}}
	rt := &resilientToolset{inner: inner, name: "failed-server", failed: true}
	// Even with tools, failed=true should show "failed" status
	result := ToolsetStatuses([]tool.Toolset{rt})
	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	if result[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %q", result[0].Status)
	}
}

// --- headerRoundTripper tests ---

func TestHeaderRoundTripper_InjectsHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &headerRoundTripper{
			headers: map[string]string{"X-Username": "dmitriyr", "X-Api-Key": "abc-123"},
		},
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if v := got.Get("X-Username"); v != "dmitriyr" {
		t.Errorf("expected X-Username=dmitriyr, got %q", v)
	}
	if v := got.Get("X-Api-Key"); v != "abc-123" {
		t.Errorf("expected X-Api-Key=abc-123, got %q", v)
	}
}

// TestHeaderRoundTripper_DoesNotMutateOriginalRequest verifies the round
// tripper clones the request before adding headers.
func TestHeaderRoundTripper_DoesNotMutateOriginalRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &headerRoundTripper{headers: map[string]string{"X-Api-Key": "abc-123"}}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if req.Header.Get("X-Api-Key") != "" {
		t.Errorf("original request was mutated: %q", req.Header.Get("X-Api-Key"))
	}
}

// --- Hook callback tests ---

func TestBuildBeforeToolCallbacks_SkipsNonBeforeToolEvents(t *testing.T) {
	hooks := []HookConfig{
		{Event: "after_tool", Command: "echo test"},
	}
	cbs := BuildBeforeToolCallbacks(hooks)
	if len(cbs) != 0 {
		t.Errorf("expected 0 before callbacks for after_tool event, got %d", len(cbs))
	}
}

func TestBuildAfterToolCallbacks_SkipsNonAfterToolEvents(t *testing.T) {
	hooks := []HookConfig{
		{Event: "before_tool", Command: "echo test"},
	}
	cbs := BuildAfterToolCallbacks(hooks)
	if len(cbs) != 0 {
		t.Errorf("expected 0 after callbacks for before_tool event, got %d", len(cbs))
	}
}

func TestBuildBeforeToolCallbacks_WithMatchingTool(t *testing.T) {
	hooks := []HookConfig{
		{
			Event:   "before_tool",
			Command: "echo before",
			Tools:   []string{"read"},
		},
	}
	cbs := BuildBeforeToolCallbacks(hooks)
	if len(cbs) != 1 {
		t.Fatalf("expected 1 before callback, got %d", len(cbs))
	}
	// The callback runs successfully with a non-matching tool name (skipped).
}

func TestBuildAfterToolCallbacks_WithMatchingTool(t *testing.T) {
	hooks := []HookConfig{
		{
			Event:   "after_tool",
			Command: "echo after",
			Tools:   []string{"write"},
		},
	}
	cbs := BuildAfterToolCallbacks(hooks)
	if len(cbs) != 1 {
		t.Fatalf("expected 1 after callback, got %d", len(cbs))
	}
}

// TestBuildMCPToolset_OAuthHandler verifies that a remote server with the
// oauth flag gets an OAuth authorization-code handler attached to its
// transport, and that the toolset uses the longer OAuth connect timeout.
func TestBuildMCPToolset_OAuthHandler(t *testing.T) {
	// The browser budget applies only where a browser can open.
	allowInteractiveOAuth(t)

	ts, err := buildMCPToolset(MCPServerConfig{
		Name:  "oauth-server",
		URL:   "https://mcp.example.com/mcp",
		OAuth: true,
	})
	if err != nil {
		t.Fatalf("buildMCPToolset: %v", err)
	}
	rt, ok := ts.(*resilientToolset)
	if !ok {
		t.Fatalf("expected *resilientToolset, got %T", ts)
	}
	if rt.timeout != mcpOAuthConnectTimeout {
		t.Errorf("expected OAuth connect timeout %v, got %v", mcpOAuthConnectTimeout, rt.timeout)
	}
	// The tracked transport wraps a StreamableClientTransport with an
	// OAuthHandler set.
	ct, ok := rt.transport.inner.(*mcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("expected *mcp.StreamableClientTransport, got %T", rt.transport.inner)
	}
	if ct.OAuthHandler == nil {
		t.Error("expected OAuthHandler to be set on the transport")
	}
}

// TestBuildMCPToolset_NoOAuthHandler verifies that a remote server without the
// oauth flag gets no OAuth handler and the default connect timeout.
func TestBuildMCPToolset_NoOAuthHandler(t *testing.T) {
	ts, err := buildMCPToolset(MCPServerConfig{
		Name: "plain-server",
		URL:  "https://mcp.example.com/mcp",
	})
	if err != nil {
		t.Fatalf("buildMCPToolset: %v", err)
	}
	rt, ok := ts.(*resilientToolset)
	if !ok {
		t.Fatalf("expected *resilientToolset, got %T", ts)
	}
	if rt.timeout != mcpConnectTimeout {
		t.Errorf("expected default connect timeout %v, got %v", mcpConnectTimeout, rt.timeout)
	}
	ct, ok := rt.transport.inner.(*mcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("expected *mcp.StreamableClientTransport, got %T", rt.transport.inner)
	}
	if ct.OAuthHandler != nil {
		t.Error("expected no OAuthHandler on the transport")
	}
}
