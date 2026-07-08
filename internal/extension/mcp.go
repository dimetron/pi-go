package extension

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
)

var mcpConnectTimeout = 15 * time.Second

// MCPServerConfig matches the config.MCPServer structure.
type MCPServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`     // HTTP transport (Streamable HTTP)
	Headers map[string]string `json:"headers,omitempty"` // Custom HTTP headers for the URL transport
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
//
// Duplicate tool names across MCP servers are deduplicated automatically.
// When multiple servers expose a tool with the same name, only the first
// occurrence (from the server that loads first) is kept to prevent the
// "duplicate tool" error from the ADK runner.
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
			r.tools = r.deduplicateTools(res.tools)
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

// deduplicateTools returns tools with their original names. When multiple
// MCP servers expose tools with the same name, the LLM will see them all
// as-is. The ADK runner handles tool call routing by matching the tool name
// in the function call response against available tools.
func (r *resilientToolset) deduplicateTools(tools []tool.Tool) []tool.Tool {
	return tools
}

// MCPToolEntry is a single resolved tool from a named MCP server.
type MCPToolEntry struct {
	Server string
	Tool   string
}

// BuildMCPToolEntries returns a flat list of {Server, Tool} pairs for all
// connected MCP toolsets. Only toolsets whose tools have already been loaded
// (via a previous Tools() call) contribute entries — pending/failed servers
// are silently skipped.
func BuildMCPToolEntries(toolsets []tool.Toolset) []MCPToolEntry {
	var entries []MCPToolEntry
	for _, ts := range toolsets {
		rt, ok := ts.(*resilientToolset)
		if !ok || rt.failed || len(rt.tools) == 0 {
			continue
		}
		for _, t := range rt.tools {
			entries = append(entries, MCPToolEntry{
				Server: rt.name,
				Tool:   t.Name(),
			})
		}
	}
	return entries
}

type MCPServerStatus struct {
	Name      string
	Connected bool   // true if Tools() succeeded at least once
	Failed    bool   // true if Tools() timed out or returned an error
	ToolCount int    // number of tools reported; 0 if not yet loaded
	Status    string // "connected", "failed", "pending"
}

// ToolsetStatuses returns the current status for each toolset that was created
// by BuildMCPToolsets. Toolsets that haven't been queried yet are reported as "pending".
func ToolsetStatuses(toolsets []tool.Toolset) []MCPServerStatus {
	statuses := make([]MCPServerStatus, 0, len(toolsets))
	for _, ts := range toolsets {
		rt, ok := ts.(*resilientToolset)
		if !ok {
			statuses = append(statuses, MCPServerStatus{
				Name:   ts.Name(),
				Status: "pending",
			})
			continue
		}
		s := MCPServerStatus{
			Name:      rt.name,
			Connected: len(rt.tools) > 0,
			Failed:    rt.failed,
			ToolCount: len(rt.tools),
		}
		switch {
		case rt.failed:
			s.Status = "failed"
		case len(rt.tools) > 0:
			s.Status = "connected"
		default:
			s.Status = "pending"
		}
		statuses = append(statuses, s)
	}
	return statuses
}

func buildMCPToolset(srv MCPServerConfig) (tool.Toolset, error) {
	var transport mcp.Transport
	switch {
	case srv.URL != "":
		t := &mcp.StreamableClientTransport{Endpoint: srv.URL}
		if len(srv.Headers) > 0 {
			t.HTTPClient = &http.Client{
				Transport: &headerRoundTripper{headers: srv.Headers},
			}
		}
		transport = t
	case srv.Command != "":
		transport = &respawnTransport{
			command: srv.Command,
			args:    srv.Args,
		}
	default:
		return nil, fmt.Errorf("MCP server %q has neither command nor URL", srv.Name)
	}

	ts, err := mcptoolset.New(mcptoolset.Config{
		Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("creating MCP toolset: %w", err)
	}
	return &resilientToolset{inner: ts, name: srv.Name}, nil
}

// headerRoundTripper injects static HTTP headers (e.g., API keys, usernames)
// into every request sent by the Streamable HTTP MCP transport. The base
// RoundTripper defaults to http.DefaultTransport when nil.
type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we never mutate the caller's headers, which the
	// transport may reuse across retries.
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}
