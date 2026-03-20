# Subagent Execution Modes: Single, Parallel, Chain

## Objective

Replace the current single-mode `agent` tool with a new `subagent` tool supporting three execution modes: single, parallel, and chain. Wire pipeline metadata through to the TUI for visualization.

## Background

Main currently has:
- `internal/tools/agent.go` — single-mode agent tool using `AgentEventCallback` and `SpawnWithInput`
- `internal/subagent/orchestrator.go` — `Spawn(ctx, SpawnInput)`, `SpawnWithInput(ctx, AgentInput)`, `LookupAgent(name)`, `AgentNames()`
- `internal/tui/tui.go` — basic `AgentSubEvent` with `AgentID`, `Kind`, `Content` (no pipeline metadata)
- Bundled agents already exist in `internal/subagent/bundled/`

## Key Requirements

### 1. New Subagent Tool (`internal/tools/subagent.go`)

Create `internal/tools/subagent.go` with:

**Types:**
- `SubagentInput` — supports three modes via field detection:
  - Single: `{agent: "<name>", task: "<prompt>"}`
  - Parallel: `{tasks: [{agent, task}, ...]}` (max 8)
  - Chain: `{chain: [{agent, task}, ...]}` (max 8)
- `SubagentOutput` — `{mode, results []AgentResult, summary}`
- `AgentResult` — `{agent, agent_id, status, result, error, duration}`
- `SubagentEvent` — extends events with `PipelineID`, `Mode`, `Step`, `Total`
- `SubagentEventCallback func(event SubagentEvent)`

**Mode Handlers:**
- `singleModeHandler` — spawn one agent, collect result
- `parallelModeHandler` — validate all agents upfront, spawn concurrently via `sync.WaitGroup`, collect all results
- `chainModeHandler` — validate all agents upfront, execute sequentially, support `{previous}` and `{previous_json}` template placeholders, stop on first failure

**Helper functions:**
- `detectMode(input)` — determines mode from populated fields
- `buildSubagentDescription(orch)` — dynamic description listing available agents from orchestrator registry
- `expandChainTemplate(task, previousResult)` — replaces `{previous}` and `{previous_json}` (JSON-escaped) placeholders
- `emitEvent(cb, ev)` — nil-safe callback invocation
- `resolveContext(ctx)` — extracts `context.Context` from `tool.Context`

**Error content in events:** When forwarding events to the callback, if `ev.Type == "error"` and `ev.Content` is empty, use `ev.Error` as the content so the TUI receives meaningful error messages.

**Spawn calls:** Use `orch.SpawnWithInput(ctx, subagent.AgentInput{Type: ..., Prompt: ...})` to match main's current API.

### 2. Legacy Wrapper (`internal/tools/agent.go`)

Keep `agent.go` but simplify it to delegate to the new subagent tool:
- `AgentTools(orch, onEvent)` should wrap the legacy `AgentEventCallback` into `SubagentEventCallback` and call `SubagentTools`
- The legacy wrapper converts `SubagentEvent` → `(agentID, kind, content)` for backward compatibility

### 3. TUI Pipeline Metadata (`internal/tui/tui.go`)

Extend the TUI to carry pipeline metadata:

**Struct changes:**
- `AgentSubEvent` — add `PipelineID`, `Mode`, `Step`, `Total` fields
- `agentSubEventMsg` — add matching fields
- `message` — add `pipelineID`, `pipelineMode`, `pipelineStep`, `pipelineTotal` fields

**Update handler changes:**
- `agentToolStartMsg` — recognize both `"agent"` and `"subagent"` tool names; extract type from `"type"` or `"agent"` key, prompt from `"prompt"` or `"task"` key
- `agentSubEventMsg` — on `"spawn"`, assign pipeline metadata to the matching message; on other events, match by `agentID`
- `waitForSubEvent` — forward all pipeline fields from `AgentSubEvent` to `agentSubEventMsg`

### 4. Remove Stale Code

- Remove `AgentTypeDef` struct and `AgentTypes` var from `internal/subagent/types.go` (keep `AgentInput`, `AgentOutput`, `AgentStatus`, `Event`)
- Remove `ValidateType` function from `internal/subagent/types.go`
- Remove `internal/subagent/types_test.go` (tests deleted code)
- Update `internal/agent/e2e_enhanced_test.go` `TestE2ESubagentTypes` to use `DiscoverAgents` + `bundled.LoadBundledAgents` instead of the old hardcoded map

## Acceptance Criteria

### Single Mode
- Given `{agent: "explore", task: "find main.go"}`, spawn one agent and return result with mode="single"
- Given an unknown agent name, return error result without spawning

### Parallel Mode
- Given `{tasks: [{agent: "explore", task: "..."}, {agent: "explore", task: "..."}]}`, spawn both concurrently and return all results
- Given one invalid agent name in tasks list, fail upfront before spawning any
- Given more than 8 tasks, return error

### Chain Mode
- Given `{chain: [{agent: "explore", task: "find X"}, {agent: "task", task: "fix {previous}"}]}`, run sequentially, passing previous result via `{previous}` placeholder
- Given `{previous_json}` placeholder, JSON-escape the previous result (backslash, quote, newline, carriage return, tab)
- Given a step fails, stop the chain and return partial results

### TUI Integration
- Given a parallel spawn, the TUI receives events with correct `pipelineID`, `mode="parallel"`, `step`, and `total` for each agent
- Given a chain spawn, events carry `mode="chain"` with correct step progression

### Error Propagation
- Given an error event with empty content, the SubagentEvent carries the error message in Content

## Implementation Slices

1. **Slice 1: SubagentEvent types and helpers** — Add `SubagentEvent`, `SubagentEventCallback`, `SubagentInput`, `SubagentOutput`, `AgentResult`, `TaskItem`, `ChainItem` types and helper functions (`detectMode`, `emitEvent`, `resolveContext`, `expandChainTemplate`) to `internal/tools/subagent.go`. Verify: `go build ./internal/tools/...`

2. **Slice 2: Single mode handler** — Implement `singleModeHandler`, `NewSubagentTool`, `SubagentTools`, and `buildSubagentDescription`. Verify: `go test ./internal/tools/... -run TestSubagent`

3. **Slice 3: Parallel mode handler** — Implement `parallelModeHandler` with `sync.WaitGroup`, upfront validation, max-8 limit. Verify: `go test ./internal/tools/... -run TestSubagentParallel`

4. **Slice 4: Chain mode handler** — Implement `chainModeHandler` with `expandChainTemplate`, upfront validation, stop-on-failure. Verify: `go test ./internal/tools/... -run TestSubagentChain`

5. **Slice 5: Legacy wrapper** — Simplify `internal/tools/agent.go` to delegate `AgentTools` → `SubagentTools` via callback wrapping. Verify: `go test ./internal/tools/... -run TestAgent`

6. **Slice 6: TUI pipeline metadata** — Extend `AgentSubEvent`, `agentSubEventMsg`, `message` structs; update `Update` handler to recognize `"subagent"` tool and propagate pipeline fields; update `waitForSubEvent`. Verify: `go test ./internal/tui/... -run TestAgent`

7. **Slice 7: Remove stale types.go code** — Remove `AgentTypeDef`, `AgentTypes`, `ValidateType` from `types.go`; delete `types_test.go`; update `e2e_enhanced_test.go` to use discovery system. Verify: `go build ./... && go vet ./...`

8. **Slice 8: Tests** — Add comprehensive tests for all three modes, edge cases (empty input, max limits, unknown agents, error propagation), and `expandChainTemplate`. Verify: `go test -race ./internal/tools/... ./internal/tui/...`

## Gates

- **build**: `go build ./...`
- **vet**: `go vet ./...`
- **test**: `go test ./internal/tools/... ./internal/tui/... ./internal/subagent/...`
- **race**: `go test -race ./internal/tools/... ./internal/tui/... ./internal/subagent/...`

## Constraints

- All slices must compile independently
- Parallel mode uses `sync.WaitGroup` with pre-allocated results slice (index-safe, no mutex needed)
- Chain mode stops on first failure
- Both parallel and chain validate all agent names before spawning any
- `expandChainTemplate` uses manual string replacement (not `json.Marshal`) for `{previous_json}`
- Maximum 8 tasks in parallel mode, 8 steps in chain mode
- Error events must propagate `ev.Error` into `SubagentEvent.Content` when `ev.Content` is empty
