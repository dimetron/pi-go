# Existing Subagent System

## Scope

Objective findings about how pi-go implements subagents today.

## Relevant packages

- `internal/subagent` owns subagent lifecycle, registry, spawning, worktrees, timeouts, and status tracking.
- `internal/tools/subagent.go` exposes the subagent capability as an ADK tool named `subagent`.
- `internal/atif` links subagent runs into trajectory output via `session_id` references.
- `internal/tui/commands.go` exposes `/subagents` status UI.

## Current tool surface

The current `subagent` tool supports three modes in `internal/tools/subagent.go`:

- Single: `{agent, task}`
- Parallel: `{tasks: [...]}` with max 8 tasks
- Chain: `{chain: [...]}` with max 8 steps

`SubagentInput` fields:

```go
type SubagentInput struct {
    Agent string `json:"agent,omitempty"`
    Task  string `json:"task,omitempty"`
    Tasks []TaskItem `json:"tasks,omitempty"`
    Chain []ChainItem `json:"chain,omitempty"`
}
```

The tool returns structured `SubagentOutput`:

```go
type SubagentOutput struct {
    Mode    string        `json:"mode"`
    Results []AgentResult `json:"results"`
    Summary string        `json:"summary"`
}
```

Each result includes:

```go
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

## Event model

The TUI-facing event callback in `internal/tools/subagent.go` uses:

```go
type SubagentEvent struct {
    AgentID    string `json:"agent_id"`
    Kind       string `json:"kind"`
    Content    string `json:"content"`
    PipelineID string `json:"pipeline_id"`
    Mode       string `json:"mode"`
    Step       int    `json:"step"`
    Total      int    `json:"total"`
}
```

Underlying spawned process events come from `internal/subagent/types.go`:

```go
type Event struct {
    Type      string `json:"type"`
    Content   string `json:"content,omitempty"`
    Error     string `json:"error,omitempty"`
    SessionID string `json:"session_id,omitempty"`
}
```

Documented event kinds there are `text_delta`, `tool_call`, `tool_result`, `message_end`, and `error`; the spawner also
emits `message_start` when a subprocess session ID is observed.

## Lifecycle and orchestration

`internal/subagent/orchestrator.go` composes:

- `Pool` for concurrency limiting
- `Spawner` for launching subprocesses
- `WorktreeManager` for optional isolated git worktrees

Primary APIs:

```go
func NewOrchestrator(cfg *config.Config, repoRoot string, agentConfigs []AgentConfig) *Orchestrator
func (o *Orchestrator) Spawn(ctx context.Context, input SpawnInput) (<-chan Event, string, error)
func (o *Orchestrator) SpawnWithInput(ctx context.Context, input AgentInput) (<-chan Event, string, error)
func (o *Orchestrator) Cancel(agentID string) error
func (o *Orchestrator) List() []AgentStatus
func (o *Orchestrator) LookupAgent(name string) (AgentConfig, error)
func (o *Orchestrator) AgentNames() []string
```

The orchestrator:

- validates the named agent against a registry
- resolves the model from `config.Config.ResolveRole`
- acquires a pool slot before spawn
- optionally creates a worktree
- forwards provider settings to children (`BaseURL`, `Insecure`, `Headers`)
- tracks runtime state in memory
- updates final status after process exit
- supports cancellation of running agents by ID
- shuts down by canceling running agents and cleaning worktrees

## Spawn strategy today

`internal/subagent/spawner.go` launches the current pi binary as a subprocess using `os.Executable()` by default.

Command shape:

- executable: current `pi` binary
- args begin with `--mode json`
- optional flags: `--model`, `--url`, `--insecure`, repeated `--header`, `--system`
- final argument is the prompt text

The spawner reads JSONL from stdout, parses event objects, and forwards them as subagent events.
If stdout lines are not valid JSON, they are emitted as `text_delta`.
Stderr is buffered and attached to process failure errors.

## Environment handling

Subagents do not inherit the full parent environment. `internal/subagent/environ.go` filters env vars and only passes
approved prefixes and provider-related settings.
The orchestrator may additionally append:

- `PI_SANDBOX_ROOT=<repo root>`
- `PI_WORKTREE_ROOT=<worktree path>`

## Output and trace linkage

The subagent tool preserves `SessionID` from subprocess `message_start` events.
`internal/atif/link.go` inspects `subagent` tool responses for `session_id` fields and links parent and child
trajectories using `subagent_trajectory_ref`.

## Failure handling patterns

Observed patterns in current implementation:

- validate all requested agents before starting parallel or chain executions
- return structured failure results instead of raising tool errors for many user-facing failures
- enforce hard limits on fan-out (`maxParallelTasks`, `maxChainSteps`)
- drop overflowing event channel writes in the spawner instead of blocking
- use timeouts and cancellation through `context.Context`

## Implications for ACP research

Facts only:

- pi already has a local-subprocess adapter pattern for one protocolized agent path (`subagent` via subprocess pi
  binary).
- pi already has a remote/local adapter precedent for other protocol families (`a2a`, `mcp`).
- existing subagent UX is centered on a named agent registry, structured result objects, and streamed event forwarding
  to the TUI.
