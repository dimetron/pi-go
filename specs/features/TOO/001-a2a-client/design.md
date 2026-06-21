# Design: A2A Client Tool for pi-go

## Context

pi-go spawns subagents as local processes. The A2A (Agent-to-Agent) client tool extends this by enabling pi-go to call remote A2A-capable agents over HTTP, using the official `github.com/a2aproject/a2a-go/v2` SDK.

Users configure A2A agent endpoints in their `.pi-go/config.json`, and the A2A client is exposed as a callable tool (similar to the MCP extension).

## Config

```go
// In internal/config/config.go

// A2AAgentConfig holds a single A2A agent entry from config.json
type A2AAgentConfig struct {
    Name string `json:"name"` // tool-invocation name (e.g., "helper")
    URL  string `json:"url"`  // base URL of the A2A agent server
}

// Config struct gains:
A2A *A2AConfig `json:"a2a,omitempty"`

// A2AConfig holds the agents list
type A2AConfig struct {
    Agents []A2AAgentConfig `json:"agents,omitempty"`
}
```

Users add to their config:
```json
{
  "A2A": {
    "agents": [
      { "name": "helper", "url": "http://localhost:8080" },
      { "name": "coder", "url": "http://localhost:8081" }
    ]
  }
}
```

## Tool Definition

```go
// In internal/tools/a2a.go

// A2AInput defines the parameters for the a2a tool.
type A2AInput struct {
    AgentName string  `json:"agent_name"` // required: which configured agent to call
    Prompt    string  `json:"prompt"`     // required: message to send
    Stream    *bool   `json:"stream,omitempty"` // optional: force streaming (default: false)
}

// A2AOutput is the result from a completed A2A call.
type A2AOutput struct {
    Agent   string `json:"agent"`
    Status  string `json:"status"`  // "completed", "failed", "streaming"
    Result  string `json:"result"`
    Error   string `json:"error,omitempty"`
}
```

Tool description dynamically lists available configured agents from config.

## Client Lifecycle

A `ClientCache` lazily creates and caches A2A clients per agent URL, using a factory:

```go
type ClientCache struct {
    clients map[string]*a2aclient.Client
    mu      sync.Mutex
}
```

- Clients are created via `a2aclient.NewFromEndpoints(ctx, []string{url})`
- Thread-safe; lazy initialization
- No TTL or cleanup needed — pi-go processes are short-lived

## Event Handling

### Non-Streaming (default)

1. Call `client.SendMessage(ctx, req)` with `AcceptedOutputModes: ["text/plain", "application/json"]`
2. Collect result text from `Message` event (extract from Parts)
3. Handle task state transitions

### Streaming

1. Call `client.SendStreamingMessage(ctx, req)`
2. Iterate events, accumulate text from `Message` Parts
3. Optionally forward events to a callback (for TUI display)

```go
func (c *ClientCache) SendMessage(ctx context.Context, cfg *A2AAgentConfig, prompt string, stream bool) (A2AOutput, error)
```

## Implementation Architecture

```
internal/tools/a2a.go          — Tool implementation + input/output types
internal/config/config.go      — A2A config struct additions
internal/agent/agent.go       — Wire A2A tools into the agent toolsets
```

### Key Signatures

```go
// Tool constructor
func NewA2ATool(cache *ClientCache, agents []config.A2AAgentConfig) (tool.Tool, error)

// ClientCache
type ClientCache struct { /* ... */ }
func (cc *ClientCache) GetClient(ctx context.Context, agent config.A2AAgentConfig) (*a2aclient.Client, error)
func (cc *ClientCache) SendMessage(ctx context.Context, agent config.A2AAgentConfig, prompt string, stream bool) (A2AOutput, error)
```

## Patterns to Follow

1. **Config schema** — Follow existing `MCP`/`compactor` patterns: config structs with JSON tags, merged from global + project config
2. **Tool creation** — Follow `newTool[TArgs, TResults]` pattern from `registry.go`, using `functiontool.Func`
3. **Parameter aliases** — Remap `agent_name` ↔ `agent`, `message` ↔ `prompt` for LLM flexibility
4. **Error handling** — Return structured `Error` field in output, never panic. Log warnings for unreachable agents.
5. **Graceful degradation** — If agent URL is unreachable, tool returns error result (not tool failure)
6. **Fail-safe initialization** — `BuildA2ATools()` skips agents that fail to initialize

## Acceptance Criteria

### Configuration
- **Given** a user adds `{ "A2A": { "agents": [{ "name": "helper", "url": "http://localhost:8080" }] } }` to their config
- **When** pi-go starts
- **Then** the A2A tool is available and the description lists "helper"

### Non-Streaming Call
- **Given** an A2A agent "helper" is configured
- **When** the LLM calls `a2a({agent_name: "helper", prompt: "hello"})`
- **Then** the tool sends a message and returns `{agent: "helper", status: "completed", result: "..."}`

### Streaming Call
- **Given** an A2A agent "helper" with streaming capability is configured
- **When** the LLM calls `a2a({agent_name: "helper", prompt: "hello", stream: true})`
- **Then** the tool accumulates all Message events and returns `{status: "streaming", result: "..."}`

### Error Handling
- **Given** an A2A agent "helper" is unreachable
- **When** the LLM calls `a2a({agent_name: "helper", prompt: "hello"})`
- **Then** the tool returns `{status: "failed", error: "connection refused"}`

### Unknown Agent
- **Given** no A2A agent named "unknown" is configured
- **When** the LLM calls `a2a({agent_name: "unknown", prompt: "hello"})`
- **Then** the tool returns `{status: "failed", error: "unknown agent..."}`

## Testing Strategy

1. **Unit tests** — `internal/tools/a2a_test.go`: test client cache, message construction, event processing
2. **Mock tests** — Use `httptest` to mock an A2A server; test streaming and non-streaming paths
3. **Integration tests** (optional) — If a real A2A agent is available in the test environment
