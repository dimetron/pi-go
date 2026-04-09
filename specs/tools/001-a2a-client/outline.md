# Outline: A2A Client Tool

## Phases

1. **Add A2A config structs** to `internal/config/config.go`
   - `A2AAgentConfig`, `A2AConfig`, field on `Config`

2. **Create A2A tool package** `internal/tools/a2a.go`
   - Input/output types: `A2AInput`, `A2AOutput`
   - `ClientCache` struct with lazy client creation
   - `SendMessage` method handling both streaming/non-streaming
   - `NewA2ATool` function returning a `tool.Tool`
   - `A2ATools` function returning `[]tool.Tool`

3. **Wire A2A tools into agent** — modify `internal/agent/agent.go`
   - Build A2A toolsets from config
   - Pass to `llmagent.New()` via existing `Toolsets` field

4. **Add go.mod dependency** — `github.com/a2aproject/a2a-go/v2`

5. **Write unit tests** — `internal/tools/a2a_test.go`
   - Mock HTTP server tests for streaming/non-streaming
   - Client cache tests

## Key Type Signatures

```go
// Config additions
type A2AAgentConfig struct { Name, URL string }
type A2AConfig struct { Agents []A2AAgentConfig }
type Config struct { /* existing */ A2A *A2AConfig }

// Tool types
type A2AInput struct { AgentName string; Prompt string; Stream *bool }
type A2AOutput struct { Agent string; Status string; Result string; Error string }

// Client cache
type ClientCache struct { clients map[string]*a2aclient.Client; mu sync.Mutex }
func (cc *ClientCache) GetClient(ctx context.Context, cfg A2AAgentConfig) (*a2aclient.Client, error)
func (cc *ClientCache) SendMessage(ctx context.Context, cfg A2AAgentConfig, prompt string, stream bool) (A2AOutput, error)

// Tool constructor
func NewA2ATool(cache *ClientCache, agents []config.A2AAgentConfig) (tool.Tool, error)
func A2ATools(cfg *config.Config) ([]tool.Tool, error)
```
