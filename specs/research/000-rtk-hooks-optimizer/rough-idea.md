# Rough Idea: RTK Hooks Optimizer for pi-go

## Source
Inspired by [pi-rtk-optimizer](https://github.com/MasuRii/pi-rtk-optimizer) — a TypeScript Pi extension that optimizes the coding agent through automated command rewriting and intelligent output compaction.

## What We Want
Implement an RTK-optimizer-style hooks/extension system in pi-go (Go), building on the existing `internal/extension/hooks.go` infrastructure. The system should:

1. **Command Rewriting** — Intercept bash tool calls and rewrite them to `rtk` equivalents when available (50+ commands across 10 categories: Git, filesystem, package managers, containers, etc.). Support "rewrite" and "suggest-only" modes.

2. **Output Compaction Pipeline** — Multi-stage processing to reduce context window usage:
   - ANSI code stripping
   - Test result aggregation
   - Build log filtering
   - Git output compaction
   - Linter output aggregation
   - Search result grouping
   - Smart truncation (configurable line/char limits)

3. **Additional Lifecycle Events** — Extend beyond `before_tool`/`after_tool`:
   - `session_start` / `session_switch` — reset tracking, reload config
   - `before_agent_start` — inject system prompt modifications

4. **TUI Configuration** — Interactive `/rtk` command with modal for:
   - Per-category rewrite toggles
   - Output compaction settings
   - Truncation parameters
   - Source code filtering levels (none/minimal/aggressive)

5. **Session Metrics** — Track compaction savings per tool type, queryable via `/rtk stats`.

## Existing Infrastructure
- `internal/extension/hooks.go` — basic `before_tool`/`after_tool` shell-command hooks
- `internal/extension/hooks_test.go` — tests for existing hooks
- `internal/tools/registry.go` — tool registration via ADK
- `internal/tui/tui.go` — Bubbletea TUI with slash commands
