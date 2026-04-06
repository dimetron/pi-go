# Research: Existing Codebase Structure

## Project Overview
- **Module**: `github.com/dimetron/pi-go`
- **Go version**: 1.26.1
- **Architecture**: TUI-based coding agent using Google ADK

## Key Packages

### `internal/agent` (agent.go)
- Creates ADK Runner + LLM Agent
- Config: Model, Tools, Toolsets, Instruction, SessionService, Callbacks
- Methods:
  - `New(cfg Config)` → creates Agent
  - `CreateSession(ctx)` → creates ADK session, returns ID
  - `Run(ctx, sessionID, msg)` → returns iterator over `*session.Event`
  - `RebuildWithInstruction(instr)` → recreates runner with new instruction
- Session events are `iter.Seq2[*session.Event, error]`

### `internal/cli/interactive.go` (deferredInit)
- Full agent initialization flow
- Creates sandbox → core tools → parallel subsystems (LSP, memory, MCP, skills, git)
- Builds final Agent with all tools and callbacks
- Returns `tui.InitResult` with Agent, SessionID, etc.

### `internal/session` (store.go)
- `pisession.NewFileService(sessionsDir)` → session.Service

## Dependencies (from go.mod)
- `github.com/gorilla/websocket v1.5.3` (available as indirect)
- `golang.org/x/net v0.52.0` (includes websocket support)

## Web Server Requirements
- Standard library `net/http`
- WebSocket via `golang.org/x/net/websocket`

## Build/Test Commands
- Build: `go build ./...`
- Test: `go test ./...`
- Lint: `golangci-lint run`
