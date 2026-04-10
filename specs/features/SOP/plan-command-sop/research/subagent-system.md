# Research: Subagent/Orchestrator System

## Package: `internal/subagent/`

### Components

| File | Purpose |
|------|---------|
| `types.go` | Agent type definitions, input/output structs, event types |
| `pool.go` | Concurrency limiter (buffered channel semaphore, default 5) |
| `spawner.go` | Process spawning (`pi --mode json --model X --system Y prompt`) |
| `worktree.go` | Git worktree creation/cleanup for isolated execution |
| `orchestrator.go` | Composes pool + spawner + worktree, manages lifecycle |

### Agent Types

| Type | Role | Worktree | Purpose |
|------|------|----------|---------|
| explore | smol | no | Fast read-only codebase exploration |
| plan | plan | no | Analyze code, create implementation plans |
| designer | slow | yes | Create/modify code with full tools |
| reviewer | slow | no | Code review with git inspection |
| task | default | yes | Complete end-to-end coding tasks |
| quick_task | smol | no | Small focused tasks |

### Spawn Flow

```
Orchestrator.Spawn(ctx, AgentInput)
  → Validate type
  → Resolve model from role
  → Acquire pool slot
  → Create worktree (if needed)
  → Spawner.Spawn() → Process
  → Forward events channel
  → On completion: cleanup worktree, release pool
```

### Event Types
- `text_delta` — streaming LLM text
- `tool_call` — tool invocation
- `tool_result` — tool result
- `message_end` — completion marker
- `error` — error event

### Key Insight for /run

The existing subagent system already supports spawning a "task" agent with worktree isolation. `/run` could:
1. Read `PROMPT.md` from a specs directory
2. Spawn a "task" type agent with the PROMPT.md content as the prompt
3. Stream events back to the TUI
4. The agent type's instruction already includes implementation guidance

The main gap: no built-in way to pass a file's content as a prompt (currently takes inline prompt string).
