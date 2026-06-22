# pi-go Tool Integration Patterns — Research Findings

## Tool Registration Pattern

pi-go uses the ADK `tool.Tool` interface. Tools are registered via builders:

```go
func CoreTools(sandbox *Sandbox) ([]tool.Tool, error) {
    builders := []func(*Sandbox) (tool.Tool, error){
        newReadTool,
        newWriteTool,
        // ...
    }
}
```

## Tool Creation Helper

`internal/tools/registry.go` provides `newTool[TArgs, TResults]` for creating function tools:

```go
func newTool[TArgs, TResults any](
    name, description string,
    handler functiontool.Func[TArgs, TResults],
    aliases ...map[string]string,
) (tool.Tool, error)
```

This:
1. Generates a lenient JSON schema (all properties optional, additional props allowed)
2. Handles type coercion (string → int/bool for fields that LLMs may send as strings)
3. Handles parameter name alias resolution

## Existing Tool Input/Output Types

```go
// Subagent tool
type SubagentInput struct {
    Agent string         `json:"agent,omitempty"`
    Task  string         `json:"task,omitempty"`
    Tasks []TaskItem     `json:"tasks,omitempty"`
    Chain []ChainItem    `json:"chain,omitempty"`
}
type SubagentOutput struct {
    Mode    string        `json:"mode"`
    Results []AgentResult `json:"results"`
    Summary string        `json:"summary"`
}
type AgentResult struct {
    Agent     string `json:"agent"`
    AgentID   string `json:"agent_id"`
    Status    string `json:"status"`
    Result    string `json:"result"`
    Error     string `json:"error,omitempty"`
    Duration  string `json:"duration"`
    SessionID string `json:"session_id,omitempty"`
}
```

## Config Loading Pattern

`internal/config/config.go`:
- `Load()` reads from `~/.pi-go/config.json` (global) then `.pi-go/config.json` (project)
- Project config merges on top of global
- Config struct has JSON tags for unmarshaling

## Agent Integration

`internal/agent/agent.go`:
- `Config` struct has `Toolsets []tool.Toolset` field
- Toolsets are passed to `llmagent.New()`
- Toolsets implement `tool.Toolset` interface: `Name()`, `Tools(ctx) ([]tool.Tool, error)`
