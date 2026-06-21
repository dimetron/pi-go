# A2A Client Tool

## Objective

Implement an A2A (Agent-to-Agent) client tool that allows pi-go to call remote A2A-capable agents over HTTP using the official `github.com/a2aproject/a2a-go/v2` SDK. Users configure agent endpoints in `.pi-go/config.json`, and the A2A client is exposed as a callable tool.

## Key Requirements

1. **SDK Integration** — Use `github.com/a2aproject/a2a-go/v2` for A2A protocol communication
2. **Config-driven Agents** — Read agent configurations from `~/.pi-go/config.json` and `.pi-go/config.json`
3. **Tool Interface** — Expose A2A agents as callable ADK tools with `agent_name`, `prompt`, and optional `stream` parameters
4. **Streaming Support** — Both streaming and non-streaming response modes
5. **Error Handling** — Graceful degradation with structured error results

## Acceptance Criteria

### Configuration
- Given a user adds `{ "A2A": { "agents": [{ "name": "helper", "url": "http://localhost:8080" }] } }` to their config
- When pi-go starts
- Then the A2A tool is available and the description lists "helper"

### Non-Streaming Call
- Given an A2A agent "helper" is configured
- When the LLM calls `a2a({agent_name: "helper", prompt: "hello"})`
- Then the tool sends a message and returns `{agent: "helper", status: "completed", result: "..."}`

### Streaming Call
- Given an A2A agent "helper" with streaming capability is configured
- When the LLM calls `a2a({agent_name: "helper", prompt: "hello", stream: true})`
- Then the tool accumulates all Message events and returns `{status: "streaming", result: "..."}`

### Error Handling
- Given an A2A agent "helper" is unreachable
- When the LLM calls `a2a({agent_name: "helper", prompt: "hello"})`
- Then the tool returns `{status: "failed", error: "connection refused"}`

### Unknown Agent
- Given no A2A agent named "unknown" is configured
- When the LLM calls `a2a({agent_name: "unknown", prompt: "hello"})`
- Then the tool returns `{status: "failed", error: "unknown agent..."}`

## Implementation Slices

1. **A2A Config Structs** — Add `A2AAgentConfig`, `A2AConfig`, and field to `Config` in `internal/config/config.go`, verify: `go build ./internal/config/...`
2. **A2A ClientCache + Tool Types** — Create `internal/tools/a2a.go` with input/output types, `ClientCache`, `SendMessage`, `NewA2ATool`, verify: `go build ./internal/tools/...`
3. **Wire A2A Tools into Agent** — Modify `internal/agent/agent.go` to build and pass A2A toolsets, verify: `go build ./internal/agent/...`
4. **Add Dependency** — Add `github.com/a2aproject/a2a-go/v2` to `go.mod` and run `go mod tidy`, verify: `go build ./...`
5. **Unit Tests** — Create `internal/tools/a2a_test.go` with mock server tests, verify: `go test ./internal/tools/... -run A2A -v`

## Gates

- **build**: `go build ./...`
- **test**: `go test ./...`
- **vet**: `go vet ./...`

## Reference

- Design: `specs/features/TOO/001-a2a-client/design.md`
- Outline: `specs/features/TOO/001-a2a-client/outline.md`
- Plan: `specs/features/TOO/001-a2a-client/plan.md`
- Requirements: `specs/features/TOO/001-a2a-client/requirements.md`
- Research: `specs/features/TOO/001-a2a-client/research/`

## Constraints

- Follow existing config merging pattern from `internal/config/config.go`
- Follow `newTool[TArgs, TResults]` pattern from `internal/tools/registry.go`
- Use `a2aclient.NewFromEndpoints()` for client creation
- Implement `iter.Seq2` streaming loop for streaming responses
- Extract text from `Message` event Parts using `part.Text()` helper
