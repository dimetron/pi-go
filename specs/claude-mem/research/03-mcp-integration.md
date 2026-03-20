# Research: pi-go MCP Integration

## Current Architecture: MCP Client Only

pi-go does NOT implement an MCP server. It is purely an MCP client.

### MCP Config (`internal/config/config.go`)
```go
type MCPConfig struct {
    Servers []MCPServer `json:"servers"`
}
type MCPServer struct {
    Name    string   `json:"name"`
    Command string   `json:"command"`
    Args    []string `json:"args"`
}
```

### Bridge Layer (`internal/extension/mcp.go`)
- `BuildMCPToolsets()` launches each server as a subprocess with stdio transport
- Uses `google.golang.org/adk/tool/mcptoolset` for discovery
- MCP SDK: `github.com/modelcontextprotocol/go-sdk v1.4.1`

### Tool Registration Pattern (`internal/tools/registry.go`)
```go
func newTool[TArgs, TResults any](name, description string, handler func(tool.Context, TArgs) (TResults, error)) (tool.Tool, error)
```
- Uses `github.com/google/jsonschema-go` for schema generation from Go structs
- `coercingTool` wrapper handles LLM type quirks (string → int/bool)
- `lenientSchema` sets `additionalProperties: true`

### Adding New Native Tools

Register in `CoreTools()` at `internal/tools/registry.go`:
```go
builders := []func(*Sandbox) (tool.Tool, error){
    newReadTool,
    // ...
    newMemSearchTool,
    newMemTimelineTool,
    newMemGetObservationsTool,
}
```

## Decision: Native Tools vs MCP Server

**Option A: Native tools** — Register search/timeline/get_observations directly in CoreTools(). Simpler, no subprocess overhead.

**Option B: Embedded MCP server** — Implement an MCP server in Go, run in-process. More aligned with claude-mem's architecture but adds complexity.

**Recommendation**: Native tools for pi-go's own use, with optional MCP server for external clients (web viewer, Claude Desktop).
