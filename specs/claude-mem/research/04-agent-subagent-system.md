# Research: pi-go Agent/Subagent System

## Agent System (`internal/agent/`)

Built on Google ADK Go (`google.golang.org/adk`):

```
agent.Config -> agent.New() -> llmagent.New() + runner.New() -> agent.Agent
```

- `Agent.Run()` / `Agent.RunStreaming()` returns `iter.Seq2[*session.Event, error]`
- Providers: Anthropic (official SDK), OpenAI, Gemini, Ollama
- `RebuildWithInstruction()` can change system prompt mid-session
- `WithRetry()` wraps runs with exponential backoff

## Subagent System (`internal/subagent/`)

### Agent Definitions
Markdown files with YAML frontmatter:
- Bundled (embedded): `internal/subagent/bundled/*.md` — explore, plan, designer, task, quick-task, worker, code-reviewer, spec-reviewer
- User: `~/.pi-go/agents/*.md`
- Project: `.pi-go/agents/*.md`

### Spawner (`spawner.go`)
Starts child `pi` processes: `pi --mode json [--model <model>] [--system <instruction>] <prompt>`
Child emits JSONL events on stdout, streamed via `chan Event`.

### Pool (`pool.go`)
Buffered-channel semaphore: `DefaultPoolSize = 5`

### Orchestrator (`orchestrator.go`)
Composes Pool + Spawner + WorktreeManager:
- `Orchestrator.Spawn(ctx, SpawnInput)` — acquire slot, optionally create worktree, spawn process
- Tracks agents in `map[string]*agentState` with status: running/completed/failed/cancelled

## AI Compression Integration Approaches

### Approach 1: Synchronous AfterToolCallback (too slow)
Block in callback, spawn compression agent. 2-5s overhead per tool call — unacceptable.

### Approach 2: Async Queue + Background Goroutine (recommended)
1. AfterToolCallback pushes `(toolName, args, result)` to buffered channel
2. Background goroutine drains channel, spawns compression agents
3. Compressed observations written to SQLite
4. Context injected at session start via system instruction

### Approach 3: Post-Turn Compression
Collect all tool results after each turn, compress in bulk. Simpler but less granular.

## Recommended Integration

1. Create `internal/subagent/bundled/memory-compressor.md` with `role: smol`
2. Add AfterToolCallback that enqueues to buffered channel (non-blocking)
3. Background goroutine uses Orchestrator.Spawn with `Background: true`
4. Results stored in SQLite observations table
5. SessionStart context built from recent observations via system instruction
