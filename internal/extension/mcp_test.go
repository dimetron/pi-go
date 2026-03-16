package extension

import (
	"testing"
)

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
	// We can't easily test with a real MCP server, but we can verify
	// that BuildMCPToolsets creates the right number of toolsets.
	// The actual connection happens lazily when tools are listed.
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
}
