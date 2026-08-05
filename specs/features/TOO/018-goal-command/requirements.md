# Requirements — `/goal` Slash Command

## Task Scope

Add a `/goal` slash command to pi-go's TUI, modeled on Codex CLI's `/goal`,
that lets the user set, view, edit, pause, resume, and clear a **persistent
per-session objective** the agent pursues across turns. The objective is
persisted in `session.Meta` (parallel to `PlanContext`), surfaced in the
status footer (e.g. `Goal: keep tests green`), injected into the agent
system prompt (parallel to `# Active Skill`), and survives `--continue` /
`--session` resume.

Non-goals (deliberately cut from v1; see
`research/codex-goal-reference.md` for what codex does, deferred):

- Token-budget tracking (`token_budget`, `tokens_used`) — pi-go's token
  tracker is per-turn, not per-thread goal; matching codex would require
  plumbing the entire `SessionService` through the goal API. Tracked as a
  follow-up spec.
- `UsageLimited` and `BudgetLimited` statuses — folded away in v1; the
  only statuses are `Active | Paused | Blocked | Complete`.
- Composer-button shortcut — pi-go's TUI is a single-line `inputModel`;
  the slash command is the only entry point.
- Cross-session memory palace persistence. Goals are per-thread.
- Multi-objective / sub-goals. One objective per session.

## Acceptance Criteria

### Functional

- [ ] `/goal` with no args: prints the summary card if a goal exists,
  otherwise prints the usage line and a hint ("No goal is currently set").
- [ ] `/goal <objective text>`: creates a goal with `Status=Active`; if a
  goal already exists (any status), replaces it silently and appends
  a chat-log entry `Goal set: <new objective>`. No confirmation step.
- [ ] `/goal edit`: opens the editor over the current objective (text-area
  pre-filled with current text); on commit, replaces the objective and
  sets `UpdatedAt`.
- [ ] `/goal pause`: sets `Status=Paused`. Bare `/goal` shows the
  `Commands: /goal resume, /goal edit, /goal clear` footer.
- [ ] `/goal resume`: sets `Status=Active` if currently `Paused`, or
  `Blocked`. No-op if already `Active`.
- [ ] `/goal clear`: deletes the goal (NULL the `*GoalContext` slot);
  restores the no-indicator state.
- [ ] When `Status=Active`, the agent's system prompt includes a
  `# Current Goal` block with the objective text. The objective is
  capped at 4 000 characters (Q3).
- [ ] When `Status != Active`, no `# Current Goal` block is injected.
- [ ] `--continue` and `--session <id>` rehydrate the goal from disk;
  if the loaded goal is `Paused` or `Blocked`, a one-line info message in
  the chat suggests `/goal resume`.

### Non-functional

- [ ] No new module dependency, no `go.mod` change.
- [ ] Goal serializes cleanly through `session.Meta` JSON round-trip
  (`UpdateGoalContext` / `GetGoalContext` mirror `PlanContext`).
- [ ] The TUI status footer reads `Goal: <truncated>` when `Status=Active`,
  `Goal paused` when `Paused`, `Goal blocked` when `Blocked`,
  `Goal complete` (dim) when `Complete`, and nothing when no goal set.
- [ ] No goroutines, no shared mutable state across the slash-command paths
  (passes `go test -race`).
- [ ] Each sub-command has a unit test in
  `internal/tui/commands_test.go`; the round-trip test in
  `internal/session/store_test.go` covers `*GoalContext` JSON
  serialization.

### Commands / Help / Autocomplete

- [ ] `/goal` is in the `slashCommands` autocomplete list
  (`internal/tui/input.go`).
- [ ] `slashCommandDesc("/goal")` returns "Set or view the current session
  goal".
- [ ] `formatHelp()` lists `/goal` under a new **Long-running tasks**
  group, sibling to `/plan` and `/run`.

## Questions & Answers

**Q1 — What does "goal-command" actually mean here?**
A: Full Codex-style `/goal` slash command — persistent per-session
objective with set / view / edit / pause / resume / clear sub-commands,
persisted in `session.Meta`, surfaced in the footer.

**Q2 — Where in the system prompt is the objective injected?**
A: Appended *after* `Base + Rules + Skills` (parallel to how `# Active
Skill` is appended via `AppendActiveSkill` in `agent.go:758`).
Implemented by a sibling `AppendActiveGoal(prompt, objective) string`
called once at session start if a goal is loaded with `Status=Active`.
Re-applied on `/goal edit`, `/goal pause → /goal resume` only if the
prompt assembly is rebuilt for that turn. The agent prompt's pre-existing
"keep working context focused: … restate the current goal" line is left
untouched as a general principle.

**Q3 — Validation/size cap on the objective text?**
A: **4 000 characters**, matching codex's
`MAX_THREAD_GOAL_OBJECTIVE_CHARS`. Empty string is rejected. The check
lives next to `AppendActiveGoal` so callers cannot bypass it.

**Q4 — When `/goal <text>` is invoked and an active goal already exists?**
A: Always replace silently. No confirmation step. The next bare `/goal`
shows the new objective. The TUI message log records the change as
`Goal set: <new objective>`.

**Q5 — Where does the goal indicator live in the TUI layout?**
A: Footer line only, alongside the model name and token usage (same
placement codex uses for "Pursuing goal"). Single-line `inputModel`
composer means there is no in-composer button; the slash command is
the only entry point.

**Q6 — Which modes support `/goal`?**
A: Interactive TUI only. `print`, `json`, and `rpc` modes load the goal
from the session if present and inject it into the system prompt, but
provide no `/goal` interface. CLI/RPC parity (e.g. `pi --goal <text>`,
`pi-go-rpc.SetGoal`) is a follow-up spec.

**Q7 — What does the bare `/goal` summary card show per status?**
A: One-line summary card with objective + status-aware action hint:

- `Active`:    `Goal: <objective>   [/goal edit | /goal pause | /goal clear]`
- `Paused`:    `Goal paused: <objective>   [/goal resume | /goal edit | /goal clear]`
- `Blocked`:   `Goal blocked: <objective>   [/goal resume | /goal edit | /goal clear]`
- `Complete`:  dim `Goal complete: <objective>   [/goal clear]`
- none:        `No goal set.   /goal <objective> to set.`

**Q8 — How is the goal worded in the system prompt?**
A: A full block (matching codex's tone) appended after `Base + Rules +
Skills`:

```
# Current Goal

The user's active session goal is:

> <objective text>

Stay focused on this objective across turns. If it is no longer the user's
intent, surface that and suggest /goal edit before pursuing other work.
```

Only present when `Status=Active`. `Paused` / `Blocked` / `Complete`
goals inject nothing.
