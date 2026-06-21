# Existing Protocol Adapter Patterns

## Scope

Objective findings about how pi-go integrates external protocol/tool ecosystems today.

## A2A pattern

`internal/tools/a2a.go` implements an A2A-backed tool.

Observed structure:

- dedicated input/output structs (`A2AInput`, `A2AOutput`)
- cache object owning protocol client instances (`ClientCache`)
- lazy client creation keyed by configured agent name
- configuration diff handling via cache eviction
- dynamic tool description listing configured remote agents
- tool handler returns structured result objects rather than raw protocol messages

Important functions:

```go
func NewClientCache(cfg *config.A2AConfig) *ClientCache
func (c *ClientCache) GetClient(ctx context.Context, agentName string) (*a2aclient.Client, error)
func (c *ClientCache) SendMessage(ctx context.Context, agentName string, prompt string, stream bool) A2AOutput
func NewA2ATool(cache *ClientCache) (tool.Tool, error)
```

A2A integration is tool-centric: one ADK tool named `a2a` calls a named external agent.

## MCP pattern

`internal/extension/mcp.go` integrates MCP servers as ADK toolsets.

Observed structure:

- config struct with transport details (`MCPServerConfig`)
- transport abstraction selected from command vs URL
- lazy connection and lazy tool resolution
- resilience wrapper so one broken server does not fail the whole agent
- timeout-based protection around initialization/tool listing
- status reporting based on cached loaded state (`pending`, `connected`, `failed`)

Important functions:

```go
func BuildMCPToolsets(servers []MCPServerConfig) ([]tool.Toolset, error)
func BuildMCPToolEntries(toolsets []tool.Toolset) []MCPToolEntry
func ToolsetStatuses(toolsets []tool.Toolset) []MCPServerStatus
```

The command-transport case uses a respawn wrapper because `exec.Cmd` is single-use.

## Shared adapter characteristics in repo

Across A2A, MCP, and subagents, the codebase currently shows these factual patterns:

- adapters usually sit behind a local Go facade that converts protocol-native events into pi-native structs
- external process/client initialization is usually lazy, cached, or both
- configuration is validated early and reported as user-readable errors
- individual endpoint/server failure is often isolated instead of crashing the whole session
- streamed protocol output is accumulated into a final string while also surfacing incremental updates
- descriptions of dynamic tools often enumerate available configured agents/resources

## No ACP implementation found

Repository search found no existing `acp` package integration and no dependency on `github.com/coder/acp-go-sdk` in
`go.mod`.
