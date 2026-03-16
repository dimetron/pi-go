package extension

import (
	"fmt"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
)

// MCPServerConfig matches the config.MCPServer structure.
type MCPServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// BuildMCPToolsets creates ADK Toolsets from MCP server configurations.
// Each server is launched as a subprocess using CommandTransport.
func BuildMCPToolsets(servers []MCPServerConfig) ([]tool.Toolset, error) {
	var toolsets []tool.Toolset
	for _, srv := range servers {
		ts, err := buildMCPToolset(srv)
		if err != nil {
			return nil, fmt.Errorf("MCP server %q: %w", srv.Name, err)
		}
		toolsets = append(toolsets, ts)
	}
	return toolsets, nil
}

func buildMCPToolset(srv MCPServerConfig) (tool.Toolset, error) {
	transport := &mcp.CommandTransport{
		Command: exec.Command(srv.Command, srv.Args...),
	}

	ts, err := mcptoolset.New(mcptoolset.Config{
		Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("creating MCP toolset: %w", err)
	}
	return ts, nil
}
