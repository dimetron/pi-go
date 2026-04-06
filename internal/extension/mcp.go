package extension

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
)

var mcpConnectTimeout = 15 * time.Second

// MCPServerConfig matches the config.MCPServer structure.
type MCPServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// BuildMCPToolsets creates ADK Toolsets from MCP server configurations.
// Each server is launched as a subprocess using CommandTransport.
// Servers that fail to initialize are logged and skipped rather than
// failing the entire batch.
func BuildMCPToolsets(servers []MCPServerConfig) ([]tool.Toolset, error) {
	var toolsets []tool.Toolset
	for _, srv := range servers {
		ts, err := buildMCPToolset(srv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: MCP server %q skipped: %v\n", srv.Name, err)
			continue
		}
		toolsets = append(toolsets, ts)
	}
	return toolsets, nil
}

// respawnTransport wraps CommandTransport to create a fresh exec.Cmd on each
// Connect call. Go's exec.Cmd is single-use — once StdoutPipe/StdinPipe are
// called the Cmd cannot be reused. Without this wrapper, a failed MCP server
// startup poisons the transport permanently with "Stdout already set" errors.
type respawnTransport struct {
	command string
	args    []string
}

func (t *respawnTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	ct := &mcp.CommandTransport{
		Command: exec.Command(t.command, t.args...),
	}
	return ct.Connect(ctx)
}

// resilientToolset wraps an MCP Toolset so that connection failures at
// tool-listing time are logged instead of killing the agent. The ADK
// mcptoolset connects lazily — errors surface only when Tools() is first
// called during an LLM request. Without this wrapper, a single misconfigured
// or unreachable MCP server crashes the entire agent.
//
// A timeout guards against hanging MCP servers that start but never respond
// to the JSON-RPC "initialize" handshake.
type resilientToolset struct {
	inner tool.Toolset
	name  string

	once   sync.Once
	tools  []tool.Tool
	failed bool
}

func (r *resilientToolset) Name() string { return r.name }

func (r *resilientToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	r.once.Do(func() {
		type result struct {
			tools []tool.Tool
			err   error
		}
		ch := make(chan result, 1)
		go func() {
			tools, err := r.inner.Tools(ctx)
			ch <- result{tools, err}
		}()

		select {
		case res := <-ch:
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "pi-go: warning: MCP server %q unavailable: %v\n", r.name, res.err)
				r.failed = true
				return
			}
			r.tools = res.tools
		case <-time.After(mcpConnectTimeout):
			fmt.Fprintf(os.Stderr, "pi-go: warning: MCP server %q timed out after %v, skipping\n", r.name, mcpConnectTimeout)
			r.failed = true
		}
	})
	if r.failed {
		return nil, nil
	}
	return r.tools, nil
}

func buildMCPToolset(srv MCPServerConfig) (tool.Toolset, error) {
	transport := &respawnTransport{
		command: srv.Command,
		args:    srv.Args,
	}

	ts, err := mcptoolset.New(mcptoolset.Config{
		Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("creating MCP toolset: %w", err)
	}
	return &resilientToolset{inner: ts, name: srv.Name}, nil
}
