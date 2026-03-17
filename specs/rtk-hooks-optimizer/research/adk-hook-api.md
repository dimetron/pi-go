# ADK Hook API Research

## Available Callback Types (ADK v0.6.0)

### Tool-Level Callbacks (LLMAgent)

| Callback | Signature | Can Modify | Short-Circuit |
|----------|-----------|-----------|---------------|
| **BeforeToolCallback** | `func(tool.Context, tool.Tool, args map[string]any) (map[string]any, error)` | args ✅ | Return non-nil result to skip tool |
| **AfterToolCallback** | `func(tool.Context, tool.Tool, args, result map[string]any, err error) (map[string]any, error)` | result ✅ | Return modified result |
| **OnToolErrorCallback** | `func(tool.Context, tool.Tool, args map[string]any, err error) (map[string]any, error)` | error ✅ | Return synthetic result to recover |

### Model-Level Callbacks (LLMAgent)

| Callback | Signature | Can Modify | Short-Circuit |
|----------|-----------|-----------|---------------|
| **BeforeModelCallback** | `func(agent.CallbackContext, *model.LLMRequest) (*model.LLMResponse, error)` | request ❌ (read-only) | Return cached response |
| **AfterModelCallback** | `func(agent.CallbackContext, *model.LLMResponse, error) (*model.LLMResponse, error)` | response ✅ | Replace response |
| **OnModelErrorCallback** | `func(agent.CallbackContext, *model.LLMRequest, error) (*model.LLMResponse, error)` | error ✅ | Provide recovery response |

### Agent-Level Callbacks

| Callback | Signature | Can Modify | Short-Circuit |
|----------|-----------|-----------|---------------|
| **BeforeAgentCallback** | `func(agent.CallbackContext) (*genai.Content, error)` | — | Return content to skip agent |
| **AfterAgentCallback** | `func(agent.CallbackContext) (*genai.Content, error)` | — | Override agent output |

### Plugin/Runner-Level Callbacks

| Callback | Signature | Notes |
|----------|-----------|-------|
| **OnUserMessageCallback** | `func(agent.InvocationContext, *genai.Content) (*genai.Content, error)` | Intercept user messages |
| **BeforeRunCallback** | `func(agent.InvocationContext) (*genai.Content, error)` | Pre-invocation setup |
| **AfterRunCallback** | `func(agent.InvocationContext)` | Cleanup only (void) |
| **OnEventCallback** | `func(agent.InvocationContext, *session.Event) (*session.Event, error)` | Observe/modify every event |

## tool.Context Capabilities

```go
type Context interface {
    agent.CallbackContext
    FunctionCallID() string
    Actions() *session.EventActions
    SearchMemory(context.Context, string) (*memory.SearchResponse, error)
    ToolConfirmation() *toolconfirmation.ToolConfirmation
    RequestConfirmation(hint string, payload any) error
}
```

## Current pi-go Usage

- **hooks.go**: Only `BeforeToolCallback` and `AfterToolCallback` via shell commands
- **lsp/hooks.go**: `AfterToolCallback` for LSP formatting/diagnostics (modifies result map)
- **Not used**: BeforeAgentCallback, AfterAgentCallback, BeforeModelCallback, AfterModelCallback, OnToolErrorCallback, OnEventCallback, Plugin callbacks

## What ADK Does NOT Provide (Must Build)

1. **System prompt modification hooks** — instruction is built before agent creation
2. **LLM request modification** — BeforeModelCallback is read-only
3. **Session lifecycle hooks** — no session create/destroy/switch events
4. **Tool definition modification** — no dynamic tool schema changes at runtime
5. **Streaming response hooks** — no per-chunk callbacks
6. **Tool discovery hooks** — partial support via Toolset interface

## Key Insight for RTK Optimizer

- **Command rewriting** → Use `BeforeToolCallback` — can modify args and return modified args
- **Output compaction** → Use `AfterToolCallback` — can modify result map
- **Session events** → Must build ourselves (no ADK support)
- **System prompt injection** → Must modify instruction before agent creation (not via hooks)
- **Metrics/logging** → Use `AfterModelCallback` for token tracking, `OnEventCallback` for event-level metrics
