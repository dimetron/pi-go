# Rough Idea — `/goal` Slash Command

## One-line

Add a `/goal` slash command to pi-go's TUI for setting, viewing, editing, pausing,
resuming, and clearing a **persistent per-session objective** that the agent pursues
across turns — modeled on Codex CLI's `/goal` (see `tmp/codex/codex-rs/`).

## Why now

- pi-go already has `/plan` (planning) and `/run` (execution of a spec) but no
  concept of a long-lived per-session objective. Users hitting `/plan` repeatedly
  on related turns have no machine-readable way to say "stay on this".
- The existing `session.Meta` carries an `AGENTS.md`-style `Rules` block, but no
  per-session state owned by the user.
- Codex (the closest sibling tool) shipped this end-to-end and explicitly tracks the
  concept in `ThreadGoal { objective, status, token_budget, tokens_used,
  time_used_seconds }` plus a SQL table. pi-go can port the shape without copying
  the implementation; the store is local JSONL, not SQLite.

## What Codex's `/goal` looks like (reference)

`/goal` (bare) → opens a summary card:

```
Goal
Status: active
Objective: Keep improving the bare goal command until it feels calm and useful.
Time used: 1m
Tokens used: 12.5K
Token budget: 80K

Commands: /goal edit, /goal pause, /goal clear
```

Status footer when active with budget:

```
gpt-5.4                                            Pursuing goal (40K / 50K)
```

Statuses (from `tmp/codex/codex-rs/protocol/src/protocol.rs:4067`):
`Active | Paused | Blocked | UsageLimited | BudgetLimited | Complete`

Usage line (from `tmp/codex/codex-rs/tui/src/goal_display.rs:7`):
`Usage: /goal [<objective>|clear|edit|pause|resume]`

Objective validated to ≤ 4 000 chars (from `MAX_THREAD_GOAL_OBJECTIVE_CHARS`).

## Out of scope for v1 (deliberate cuts)

- **Token budget tracking** — drop the `token_budget` / `tokens_used` fields for v1.
  pi-go's token tracker is per-turn, not goal-scoped; matching codex's accounting
  would require plumbing the entire `SessionService` through the goal API. Tracked
  as a follow-up spec.
- **Goal-button keyboard shortcut** — codex has a button in its composer to open
  the goal menu. pi-go's TUI is a single-line `inputModel`, so the slash command
  is the only entry point; a button is not meaningful here.
- **Cross-session memory palace persistence** — out of scope. Goals are per-thread
  (per-session-id), not cross-session.
- **Multi-objective / sub-goals** — one objective per session, full stop.

## Status enum for pi-go v1

Drop `UsageLimited` (no quota concept in pi-go yet). Keep:
`Active | Paused | Blocked | Complete`.

`BudgetLimited` is folded into the deferred token-budget feature.

## Open questions for requirements

- Persistence: confirm we extend `session.Meta` with a new `*GoalContext` field
  alongside the existing `*PlanContext`.
- Inject objective into system prompt: prepend a `# Current Goal` block to the
  assembled instruction, the same way `# Active Skill` is appended.
- Resume across `--continue` and `--session`: goal must round-trip through the
  session store, not just live in TUI memory.
- Tests: each subcommand has its own table-driven test in
  `internal/tui/commands_test.go` (matches the `/plan`, `/skills` pattern).
