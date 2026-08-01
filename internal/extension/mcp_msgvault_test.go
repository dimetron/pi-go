package extension

import (
	"context"
	"os/exec"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// readonlyCtx is a minimal agent.ReadonlyContext for tests that need a
// non-nil context (e.g., when calling into the real ADK mcptoolset, which
// dereferences the context during MCP Connect).
type readonlyCtx struct {
	context.Context
}

func (readonlyCtx) UserContent() *genai.Content          { return nil }
func (readonlyCtx) InvocationID() string                 { return "" }
func (readonlyCtx) AgentName() string                    { return "test" }
func (readonlyCtx) ReadonlyState() session.ReadonlyState { return nil }
func (readonlyCtx) UserID() string                       { return "" }
func (readonlyCtx) AppName() string                      { return "" }
func (readonlyCtx) SessionID() string                    { return "" }
func (readonlyCtx) Branch() string                       { return "" }

var _ agent.ReadonlyContext = readonlyCtx{}

// TestBuildMCPToolsets_MsgvaultSmoke runs a real MCP server (msgvault) end-to-end
// against BuildMCPToolsets and verifies the connection succeeds, the toolset is
// reported as "connected", and at least one tool is exposed.
//
// Skipped when the `msgvault` binary is not on PATH.
func TestBuildMCPToolsets_MsgvaultSmoke(t *testing.T) {
	if _, err := exec.LookPath("msgvault"); err != nil {
		t.Skipf("msgvault not on PATH: %v", err)
	}

	// Some machines have msgvault but no local DB; msgvault mcp will start
	// anyway and just report no messages on queries. Connection / tool-list
	// is what we're verifying here.
	toolsets, err := BuildMCPToolsets([]MCPServerConfig{
		{Name: "msgvault", Command: "msgvault", Args: []string{"mcp"}},
	})
	if err != nil {
		t.Fatalf("BuildMCPToolsets: %v", err)
	}
	if len(toolsets) != 1 {
		t.Fatalf("expected 1 toolset, got %d", len(toolsets))
	}
	if toolsets[0].Name() != "msgvault" {
		t.Errorf("expected toolset name 'msgvault', got %q", toolsets[0].Name())
	}

	// Drive the toolset through a real Tools() call to verify the MCP
	// initialize handshake + tools/list both succeed. The resilientToolset
	// enforces a 15s connection timeout internally. We pass a real (non-nil)
	// ReadonlyContext because the ADK mcptoolset dereferences the embedded
	// context.Context during MCP Connect; passing nil would crash the mcp
	// SDK with a nil-pointer panic in ioConn.Write.
	tools, err := toolsets[0].Tools(readonlyCtx{Context: context.Background()})
	if err != nil {
		t.Fatalf("Tools() returned error: %v", err)
	}
	if len(tools) == 0 {
		// Diagnose: is the toolset marked failed?
		if rt, ok := toolsets[0].(*resilientToolset); ok && rt.failed {
			t.Fatal("msgvault toolset reported failed during Tools()")
		}
		t.Fatal("msgvault MCP server connected but exposed 0 tools")
	}

	// Sanity-check the tool names match what msgvault advertises in its
	// --help output (search_messages, get_message, list_messages, get_stats,
	// aggregate, stage_deletion). We only assert on search_messages /
	// list_messages since the surface is stable, and a mismatch here means
	// msgvault changed its public API.
	got := map[string]bool{}
	for _, tl := range tools {
		got[tl.Name()] = true
	}
	for _, want := range []string{"search_messages", "list_messages"} {
		if !got[want] {
			t.Errorf("expected msgvault to expose %q, got: %v", want, got)
		}
	}

	statuses := ToolsetStatuses(toolsets)
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != "connected" {
		t.Errorf("expected status 'connected', got %q (ToolCount=%d)", statuses[0].Status, statuses[0].ToolCount)
	}
	t.Logf("msgvault MCP connected: %d tools exposed (status=%q)", statuses[0].ToolCount, statuses[0].Status)
}
