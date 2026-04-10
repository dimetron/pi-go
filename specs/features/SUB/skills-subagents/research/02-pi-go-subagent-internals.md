# pi-go Subagent System Internals

## Package: internal/subagent/

### Files

| File | Purpose |
|------|---------|
| types.go | Type definitions, agent type map, validation |
| orchestrator.go | Lifecycle management, state tracking |
| spawner.go | Process spawning, JSONL parsing, event streaming |
| pool.go | Concurrency control (buffered channel semaphore) |
| worktree.go | Git worktree isolation |

### Hardcoded Agent Types (types.go)

```go
var AgentTypes = map[string]AgentTypeDef{
    "explore":    {Role: "smol",    Worktree: false, Tools: [read, grep, find, tree, ls]},
    "plan":       {Role: "plan",    Worktree: false, Tools: [read, grep, find, tree, ls, git-overview]},
    "designer":   {Role: "slow",    Worktree: true,  Tools: [read, write, edit, grep, find, tree, ls, bash]},
    "reviewer":   {Role: "slow",    Worktree: false, Tools: [read, grep, find, git-overview, git-file-diff, git-hunk]},
    "task":       {Role: "default", Worktree: true,  Tools: [read, write, edit, bash, grep, find, tree, ls, git-overview]},
    "quick_task": {Role: "smol",    Worktree: false, Tools: [read, write, edit, bash, grep, find]},
}
```

### Orchestrator

```go
type Orchestrator struct {
    pool     *Pool
    spawner  *Spawner
    worktree *WorktreeManager
    cfg      *config.Config
    agents   map[string]*agentState
    mu       sync.Mutex
    closed   bool
}
```

- `NewOrchestrator(cfg, repoRoot)` — creates with pool size 5
- `Spawn(ctx, input) → (events chan, agentID, error)`
- `List() → []AgentStatus`
- `Cancel(agentID) → error`
- `Shutdown()` — cancels all, cleans up worktrees

### Spawn Flow

1. Validate type → 2. Resolve role→model → 3. Acquire pool slot →
4. Generate agentID → 5. Create worktree (if needed) → 6. Spawn process →
7. Register state → 8. Launch event forwarding goroutine → 9. Return channel + ID

### Spawner

- Command: `pi --mode json [--model M] [--system INSTR] PROMPT`
- JSONL parsing from stdout (256KB buffer, 1MB max line)
- Events buffered channel (capacity 64, non-blocking send)
- Process group kill on cancellation (SIGKILL)
- 3s wait delay before forced kill

### Pool

- Buffered channel semaphore (capacity = max concurrent)
- `Acquire(ctx)` blocks until slot available or context cancelled
- `Release()` frees a slot
- Default size: 5, minimum: 1

### WorktreeManager

- Creates worktrees at `repoRoot/.pi-go/worktrees/<shortID>/`
- Branch naming: `pi-agent-<shortID>`
- Cleanup with retry, falls back to `os.RemoveAll`
- `CleanupAll()` for shutdown (3 retry passes)
- `MergeBack(agentID)` to merge worktree branch

### Event Type

```go
type Event struct {
    Type    string // "text_delta", "tool_call", "tool_result", "message_end", "error"
    Content string
    Error   string
}
```

## Package: internal/tools/agent.go

### Tool Schema

```go
type AgentToolInput struct {
    Type   string // explore, plan, designer, reviewer, task, quick_task
    Prompt string
}

type AgentToolOutput struct {
    AgentID  string
    Type     string
    Result   string // accumulated text_delta (truncated to 100KB)
    Error    string
    Duration string
}
```

### AgentEventCallback

```go
type AgentEventCallback func(agentID, eventType, content string)
```

Called for each event: "spawn", "text_delta", "tool_call", "tool_result", "error"

### Wiring (cli.go)

```go
agentEventCh := make(chan tui.AgentSubEvent, 128)
agentEventCB := func(agentID, eventType, content string) {
    select {
    case agentEventCh <- tui.AgentSubEvent{...}:
    default: // drop if full
    }
}
agentTools, _ := tools.AgentTools(orch, agentEventCB)
```

## TUI Event Streaming (internal/tui/)

### Types

```go
type AgentSubEvent struct {
    AgentID string
    Kind    string // "spawn", "tool_call", "tool_result", "text_delta", "error"
    Content string
}

type agentEv struct {
    kind    string
    content string
}
```

### Event Flow

1. Agent tool → callback → agentEventCh (buffer 128)
2. TUI's waitForSubEvent() → wraps as agentSubEventMsg
3. Update() routes: "spawn" → assign ID; others → append to agentEvents
4. Rendering: last 8 events shown, tool_call=⚙, tool_result=✓, text=→

### Key Insight

The TUI already supports multiple concurrent agent tool calls — each gets independent message and event stream. The activeTools map tracks parallel tool execution with timing.

## Gap Summary for New Features

| What Exists | What's Missing |
|-------------|----------------|
| Single spawn mode | Parallel + Chain modes |
| Hardcoded agent types | Markdown-defined discoverable agents |
| Pool concurrency (5) | Configurable concurrency, env var control |
| Basic timeouts (WaitDelay 3s) | Inactivity timeout, absolute timeout |
| Inherited env | Filtered env for subprocess isolation |
| Simple event forwarding | Rich event rendering (usage stats, tool formatting) |
| agentID-based routing | Chain metadata, parallel status tracking |
