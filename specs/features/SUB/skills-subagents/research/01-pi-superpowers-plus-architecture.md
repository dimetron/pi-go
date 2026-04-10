# pi-superpowers-plus Architecture

Source: https://github.com/coctostan/pi-superpowers-plus

## Agent Definition Format

Markdown files with YAML frontmatter in `agents/` directory:

```yaml
---
name: implementer
description: Implement tasks via TDD and commit small changes
tools: read, write, edit, bash, lsp
model: claude-sonnet-4-5
---

System prompt body here...
```

### 4 Bundled Agents

| Agent | Tools | Model | Description |
|-------|-------|-------|-------------|
| implementer | read, write, edit, bash, lsp | sonnet | Implement tasks via TDD |
| worker | read, write, edit, bash, lsp | sonnet | General-purpose isolated tasks |
| code-reviewer | read, bash, find, grep, ls | sonnet | Quality, security, testing (read-only) |
| spec-reviewer | read, bash, find, grep, ls | sonnet | Verify implementation matches spec (read-only) |

## Agent Discovery (agents.ts)

Three-tier priority system:
1. **Bundled** (package `agents/` dir) — lowest priority
2. **User** (`~/.pi/agent/agents/`) — medium priority
3. **Project** (`.pi/agents/`) — highest priority, override by name

Scope parameter: `"user"`, `"project"`, or `"both"`

## Subagent Tool (index.ts, 1124 lines)

### Three Execution Modes

1. **Single**: `{agent: "name", task: "..."}`
2. **Parallel**: `{tasks: [{agent, task}, ...]}` — up to 8 concurrent
3. **Chain**: `{chain: [{agent, task}, ...]}` — sequential, `{previous}` placeholder

### Process Isolation

- Each subagent = fresh `pi` subprocess in JSON mode
- Command: `pi --mode json -p --no-session [--model MODEL] [--tools TOOLS] [--append-system-prompt FILE] "Task: ..."`
- Filtered environment (allowlist of prefixes + explicit vars)
- Inactivity timeout: 120s (kills on no output)
- Absolute timeout: 10min default (configurable per agent)

### Concurrency Control

- Semaphore-based (not channel-based)
- Default: 6 concurrent subagents
- Configurable via `PI_SUBAGENT_CONCURRENCY` env var

### Process Lifecycle

- ProcessTracker tracks all child processes
- `process.on("exit", () => processTracker.killAll())` for cleanup
- SIGTERM first, SIGKILL after 5s fallback

### Event Streaming

- Reads JSONL from subprocess stdout
- Parses `message_end` and `tool_result_end` events
- Accumulates usage stats (tokens, cost, turns)
- Streams partial results via `onUpdate` callback

### Result Format

- Single: final assistant text output
- Parallel: `"N/M succeeded\n\n[agent] status: preview"`
- Chain: final step's output, stops on first error

### TUI Rendering

- Expanded/collapsed views for each mode
- Tool call formatting (bash, read, write, edit, find, grep)
- Usage stats display (tokens, cost, model)
- Progress indicators for running agents
