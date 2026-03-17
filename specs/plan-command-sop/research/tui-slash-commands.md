# Research: TUI Slash Command Architecture

## File Locations
- Main TUI: `internal/tui/tui.go` (~1310 lines)
- Commit command: `internal/tui/commit.go` (~280 lines)
- History command: `internal/tui/history.go` (~113 lines)

## Dispatch Flow

1. User types input → `submit()` (line 465)
2. If starts with `/` → `handleSlashCommand()` (line 511)
3. Switch on command name → handler function

## Existing Commands

| Command | Handler | Type |
|---------|---------|------|
| `/help` | inline | Sync (immediate display) |
| `/clear` | inline | Sync |
| `/model` | inline | Sync |
| `/session` | inline | Sync |
| `/branch` | `handleBranchCommand()` | Sync |
| `/compact` | `handleCompactCommand()` | Sync |
| `/agents` | `handleAgentsCommand()` | Sync |
| `/history` | `handleHistoryCommand()` | Sync |
| `/commit` | `handleCommitCommand()` | Async (LLM call + confirmation) |
| `/exit`, `/quit` | inline | Sync |

## Autocomplete System

`slashCommands` array (line 1051) feeds `completeSlashCommand()` for Tab completion.

## Async Pattern (/commit)

The `/commit` command is the best reference for building `/plan` and `/run`:

1. **Initiation**: `handleCommitCommand()` checks preconditions, collects data
2. **LLM Call**: Returns a `tea.Cmd` that calls `GenerateCommitMsg(ctx, diffs)`
3. **Result Handling**: Result arrives as `commitGeneratedMsg` in `Update()`
4. **Confirmation**: Sets `commit.phase = "confirming"`, waits for Enter/Esc
5. **Execution**: Runs the action, sends `commitDoneMsg`

## TUI Config

```go
type Config struct {
    Agent          *agent.Agent
    Orchestrator   *subagent.Orchestrator
    GenerateCommitMsg func(ctx, diffs) (string, error)
    // ... other fields
}
```

New commands can follow the same pattern: add a callback to Config, handle the async flow via message types.

## Key Insight for /plan and /run

- `/plan` could be a **multi-turn async flow** (longer than /commit) — needs state tracking
- `/run` could leverage the existing **Orchestrator.Spawn()** with a "task" agent type
- Both need new entries in `slashCommands` array for autocomplete
- Both need new message types for TUI updates
