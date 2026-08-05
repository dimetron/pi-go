# `/goal` Slash Command

## Objective

Add a `/goal` slash command to pi-go's interactive TUI, modeled on Codex CLI's
`/goal`. Users declare a persistent per-session objective that the agent
pursues across turns, with a status lifecycle (Active / Paused / Blocked /
Complete), a footer indicator, system-prompt injection, and round-trip
persistence through `meta.json` (so it survives `--continue`).

## Key Requirements

1. **Persistent per-session objective** — A single text field per session,
   persisted in `session.Meta.GoalContext`, parallel to `PlanContext`.
2. **Six sub-commands** — bare `/goal` (summary), `/goal <text>` (set),
   `/goal edit` (overlay editor), `/goal pause`, `/goal resume`,
   `/goal clear`.
3. **Status lifecycle** — `Active | Paused | Blocked | Complete`. Only
   `Active` injects into the agent prompt; `Paused | Blocked | Complete`
   leave the prompt unchanged.
4. **System-prompt injection** — A `# Current Goal` block appended after
   `Base + Rules + Skills` (parallel to `# Active Skill`).
5. **Footer indicator** — A short status line in the bottom bar showing
   the goal state and the (truncated) objective.
6. **Resume across `--continue` / `--session`** — A loaded session
   rehydrates the goal; if `Paused | Blocked`, a chat hint suggests
   `/goal resume`.
7. **Validation** — 4 000-byte cap on objective length; empty rejected.
8. **No silent overwrite protection** — `/goal <text>` replaces any
   existing goal silently.

## Acceptance Criteria

### Goal set

- Given no goal set, when `/goal keep tests green`, then chat shows
  `Goal set: keep tests green`, footer reads `Goal: keep tests green`,
  next turn sees the `# Current Goal` block, meta.json contains
  `GoalContext{objective: "keep tests green", status: active}`.

### Goal pause / resume

- Given a goal with `status: paused`, when `/goal resume`, then chat shows
  `Goal resumed`, footer flips from `Goal paused: <obj>` to `Goal: <obj>`,
  the `# Current Goal` block returns to the prompt.

### Goal edit

- Given an active goal with text `x`, when `/goal edit` then Enter then
  `y\nEnter`, then meta.json holds the new text and prompt rebuilds.

### Goal clear

- Given any goal, when `/goal clear`, then chat shows `Goal cleared.`,
  footer disappears, meta.json `GoalContext` is nil, no `# Current Goal`
  block.

### Validation cap

- Given no goal, when `/goal <4100-char string>`, then chat shows
  `goal objective must be at most 4000 characters (got 4100)`, state
  unchanged.

### Resume from paused session

- Given meta.json with `GoalContext{status: paused, objective: x}`, when
  `pi --continue`, then chat shows the resume hint
  (`Loaded … paused state …`), footer shows `Goal paused: x`, no
  `# Current Goal` block in prompt.

## Implementation Slices

1. **Slice 1 — Schema + persistence** — `GoalStatus`, `GoalContext`,
   `Meta.GoalContext` field, `UpdateGoalContext`, `GetGoalContext` in
   `internal/session/store.go`. Round-trip tests in `store_test.go`.
   verify: `go build ./internal/session/... && go test ./internal/session/...`

2. **Slice 2 — Agent validation + injection** — `MaxGoalObjectiveChars`,
   `ValidateGoalObjective`, `AppendActiveGoal` in `internal/agent/agent.go`.
   Tests for empty / 4 001 / 4 000 cases and exact block wording.
   verify: `go build ./internal/agent/... && go test ./internal/agent/...`

3. **Slice 3 — TUI dispatch + summary card** — `handleGoalCommand`,
   `printGoalSummary`, `setGoalFromInput`, `transitionGoal`, `clearGoal`,
   `persistGoal`, `truncate` in `internal/tui/goal.go` (new); model field
   `goal *session.GoalContext` in `internal/tui/types.go`; `case "/goal"`
   in `internal/tui/commands.go`. One test per sub-command in
   `commands_test.go`.
   verify: `go build ./internal/tui/... && go test -run TestHandleSlashCommandGoal ./internal/tui/...`

4. **Slice 4 — Prompt rebuild + edit overlay** — implement
   `applyGoalToPrompt` in `goal.go` (calls
   `m.cfg.Agent.RebuildWithInstruction`); add `beginGoalEdit` /
   `commitGoalEdit` reusing the `create_skill.go` overlay pattern. Test
   that `RebuildWithInstruction` is called on state change.
   verify: `go build ./internal/tui/... && go test ./internal/tui/...`

5. **Slice 5 — Footer indicator** — extend `StatusRenderInput` with
   `GoalIndicator string` in `internal/tui/status.go`; implement
   `goalIndicatorText()` in `internal/tui/tui.go`; populate in
   `statusRenderInput()`. Five-case table test.
   verify: `go test -run TestGoalIndicatorText ./internal/tui/...`

6. **Slice 6 — Resume across `--continue` / `--session`** — in
   `internal/cli/interactive.go:~298`, call `AppendActiveGoal` when the
   loaded goal is Active; prepend the resume-hint message when Paused or
   Blocked. Tests in `cli_test.go` / new `interactive_test.go`.
   verify: `go test -run TestResumeGoal ./internal/cli/...`

7. **Slice 7 — Autocomplete + help table** — append `"/goal"` to
   `slashCommands`; add `slashCommandDesc` case in
   `internal/tui/input.go`; add `**Long-running tasks:**` section to
   `formatHelp` in `internal/tui/commands.go`. Tests for both.
   verify: `go test -run "TestSlashCommands|TestFormatHelp" ./internal/tui/...`

8. **Slice 8 — Compact / reload resilience** — `TestGoalContext_AfterCompactReload`
   and `TestGoalContext_SurvivesUpdateTwice` in
   `internal/session/store_test.go`. Confirms `meta.json` round-trip
   survives `Compact()` and that `UpdatedAt` advances.
   verify:
   `go test -run "TestGoalContext_AfterCompactReload|TestGoalContext_SurvivesUpdateTwice" ./internal/session/...`

## Gates

- **build**: `go build ./...`
- **test**: `go test ./internal/session/... ./internal/agent/... ./internal/tui/... ./internal/cli/...`
- **vet**: `go vet ./...`
- **race (manual, optional)**:
  `go test -race ./internal/tui/... ./internal/session/... ./internal/agent/... ./internal/cli/...`

Run the slice-specific verify command at the end of each slice.
Run all gates plus `go test ./...` at the end of Slice 8 (the final
slice). No `go.mod` change; no new module dependency.

## Reference

- Design: `specs/features/TOO/018-goal-command/design.md`
- Outline: `specs/features/TOO/018-goal-command/outline.md`
- Plan: `specs/features/TOO/018-goal-command/plan.md`
- Requirements: `specs/features/TOO/018-goal-command/requirements.md`
- Research: `specs/features/TOO/018-goal-command/research/`
    - `codex-goal-reference.md` — codex port anatomy
    - `pi-go-integration-points.md` — where the implementation lands

## Constraints

- **No new module dependency.** Stick to the existing dependencies; no
  `go.mod` change.
- **One objective per session.** No list of goals, no sub-goals.
- **Interactive TUI mode only.** `print`, `json`, `rpc` modes honor a
  loaded goal in the prompt but do not expose a `/goal` command
  surface. CLI/RPC parity is a follow-up spec.
- **`len` is byte length** for the 4 000-char cap, not rune count. The
  purpose is to bound prompt cost; bytes are the right unit.
- **`/goal <text>` replaces any existing goal silently.** No confirmation
  step (Q4 decision).
- **Byte cap = 4 000.** Matches codex's `MAX_THREAD_GOAL_OBJECTIVE_CHARS`
  (Q3).
- **Status enum is `Active | Paused | Blocked | Complete`.** No
  `UsageLimited` / `BudgetLimited` in v1 — token-budget accounting is a
  follow-up spec.
- **Slice ordering is fixed.** Slices 1 and 2 are independent; Slice 3
  depends on both; Slices 4–8 depend on Slice 3. Do not reorder.
- **Do not edit `internal/agent/agent.go:46` (`SystemInstruction` constant).**
  The `# Current Goal` block goes through `AppendActiveGoal`, not by
  editing the constant.
- **Do not change `session.Meta` keys other than adding `goalContext`.**
  Existing keys (`planContext`, `title`, `model`, etc.) must remain stable.
- **Existing `AppendActiveSkill` shape is the precedent for `AppendActiveGoal`.**
  Match the docstring style, the `\n\n` separators, and the placement
  after `Base + Rules + Skills`.
