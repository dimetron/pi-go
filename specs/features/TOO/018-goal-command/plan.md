# Plan — `/goal` Slash Command

Eight vertical slices. Each compiles and tests independently before the
next begins. Gate commands at the bottom apply at every slice end.

---

## Slice 1 — Session schema + persistence

**Goal:** The session metadata layer can store and retrieve a goal,
round-tripping through `meta.json`.

Files:

- `internal/session/store.go` — add `GoalStatus` type and constants,
  `GoalContext` struct, `Meta.GoalContext *GoalContext` field after
  `PlanContext`, `UpdateGoalContext`, `GetGoalContext` mirroring the
  `PlanContext` pair (store.go:620, 638) byte-for-byte.
- `internal/session/store_test.go` — add `TestGoalContext_RoundTrip`:
  marshal an instance with each of the four statuses, unmarshal, assert
  equality. Add a 4 000-byte exact-cap case and a 4 001-byte rejection
  (rejection happens at validation, not here — this is just round-trip).
- `internal/session/store_test.go` — add `TestGoalContext_NilClear`:
  `UpdateGoalContext(id, nil)` drops the field on disk, subsequent
  `GetGoalContext` returns `(nil, nil)`.

Verify:

```bash
go build ./internal/session/... && go test ./internal/session/...
```

Dependencies: none. This slice stands alone.

---

## Slice 2 — Agent validation + injection

**Goal:** The agent package owns the 4 000-char validator and the
`# Current Goal` block builder.

Files:

- `internal/agent/agent.go` — add `MaxGoalObjectiveChars = 4_000`,
  `ValidateGoalObjective(string) error` (empty + over-cap rejection),
  `AppendActiveGoal(prompt, objective string) string` returning exactly
  the Q8 block. Place immediately after `AppendActiveSkill` (line 727).
- `internal/agent/agent.go` — add a docstring comment on
  `AppendActiveGoal` noting the byte-vs-rune choice (`len`, not rune count).
- `internal/agent/agent_test.go` — add `TestValidateGoalObjective`
  table-driven for empty / 4001 / 4000 cases.
- `internal/agent/agent_test.go` — add `TestAppendActiveGoal` asserting
  exact block wording (snapshot-style string equality) and that
  empty/over-cap inputs return the prompt unchanged.

Verify:

```bash
go build ./internal/agent/... && go test ./internal/agent/...
```

Dependencies: none.

---

## Slice 3 — TUI dispatch + summary card

**Goal:** All six sub-commands route through `handleGoalCommand`. State
persists. Prompt rebuild is stubbed.

Files:

- `internal/tui/commands.go` — add `case "/goal": return m.handleGoalCommand(parts[1:])`
  between `/run` (line 75) and `/login` (line 77).
- `internal/tui/goal.go` (new) — define `handleGoalCommand(args)`,
  `printGoalSummary()`, `setGoalFromInput(text)`,
  `transitionGoal(to session.GoalStatus, notice string)`,
  `clearGoal()`, `persistGoal() error`, `truncate(s string, n int) string`.
  Stub `applyGoalToPrompt()` to a no-op (logged note that it lands in
  slice 4).
- `internal/tui/types.go` — extend `model` with `goal *session.GoalContext`,
  `goalEditing bool`, `goalDraft []rune`. Initialize `goal: nil` in
  `newM()` and any other constructors (verify with `grep "newM\|&model{"`).
- `internal/tui/commands_test.go` — add a `TestHandleSlashCommandGoal_*`
  test per sub-command:
    - bare on no goal → `No goal is currently set` chat message
    - bare on active goal → `Goal: <obj>   [/goal edit | /goal pause | /goal clear]` line
    - `/goal <text>` → `Goal set: <text>` chat message, `m.goal != nil`
    - `/goal pause` → status flips, footer text shape matches
    - `/goal resume` → status flips, no-op message when already active
    - `/goal clear` → `m.goal == nil`
    - over-cap input (4100 chars) → validation error message, no state change

Verify:

```bash
go build ./internal/tui/... && go test -run TestHandleSlashCommandGoal ./internal/tui/...
```

Dependencies: Slices 1, 2 (calls `ValidateGoalObjective`, calls
`UpdateGoalContext`).

---

## Slice 4 — Prompt rebuild on edit / pause / resume

**Goal:** A status change re-applies the `# Current Goal` block to the
agent prompt; `/goal edit` has an editor.

Files:

- `internal/tui/goal.go` — implement `applyGoalToPrompt()` per design
  §"Injection points"`. Call from `transitionGoal`, `setGoalFromInput`,
  `commitGoalEdit`. Skip when `m.goal == nil` or `Status != Active`.
- `internal/tui/goal.go` — implement `beginGoalEdit` and
  `commitGoalEdit`. Pre-fill `m.goalDraft` from current objective when
  entering; on Enter commit, on Esc drop. Reuse the overlay pattern from
  `internal/tui/create_skill.go:50-90` (no new package).
- `internal/tui/goal_test.go` (new) — `TestGoalEdit_Overlay_*`:
    - entering edit pre-fills the draft from current objective
    - Enter replaces goal and rebuilds prompt
    - Esc drops overlay and leaves goal unchanged
- `internal/tui/commands_test.go` — extend `TestHandleSlashCommandGoal`
  to assert `RebuildWithInstruction` was called when state changed from
  Paused to Active (mock the agent if necessary; if the test harness
  already constructs an `Agent`, capture its last instruction).

Verify:

```bash
go build ./internal/tui/... && go test ./internal/tui/...
```

Dependencies: Slice 3.

---

## Slice 5 — Footer indicator

**Goal:** The status bar shows a one-line goal indicator.

Files:

- `internal/tui/status.go` — extend `StatusRenderInput` with
  `GoalIndicator string`. Hook into the existing render loop so that
  when `GoalIndicator != ""` it renders, else nothing. Match the
  placement rules of the existing token counter (right-aligned side of
  the bar; left of token usage).
- `internal/tui/tui.go` — extend `statusRenderInput()` (line 1836) to
  populate `GoalIndicator: m.goalIndicatorText()`.
- `internal/tui/tui_test.go` — `TestGoalIndicatorText` table-driven
  over five inputs: `nil` goal, Active, Paused, Blocked, Complete.
  Assert exact strings.

Verify:

```bash
go build ./internal/tui/... && go test -run TestGoalIndicatorText ./internal/tui/...
```

Dependencies: Slice 3 (model field), Slice 4 (`applyGoalToPrompt` not
strictly required, but a coherent story).

---

## Slice 6 — Resume across `--continue` / `--session`

**Goal:** A loaded session rehydrates the goal into the prompt and a
one-line hint when the loaded goal is Paused/Blocked.

Files:

- `internal/cli/interactive.go` — after `instruction = instructionParts.String()`
  (line 297), look up `sessionSvc.GetGoalContext(loadedSessionID)`. If
  non-nil and `Status == Active`, call `AppendActiveGoal` on `instruction`.
- `internal/cli/interactive.go` — find the model-construction site
  (search for `&model{` / `tea.NewProgram`). If a goal is loaded with
  `Status ∈ {Paused, Blocked}`, set `m.goal` and prepend one assistant
  message: `Loaded a previously-set goal in '<status>' state. Type
  '/goal resume' to keep pursuing it.`
- `internal/cli/cli_test.go` or new `interactive_test.go` — add
  `TestResumeGoal_HintShown`:
    - Given `meta.json` with a paused goal,
    - When `InteractiveCLI(loadedSessionID)` is built,
    - Then the model's chat messages start with the resume hint and the
      model field `goal` is non-nil.
- Same file — add `TestResumeGoal_ActiveInjected`:
    - Given `meta.json` with an active goal,
    - When the instruction is assembled,
    - Then `strings.Contains(instruction, "# Current Goal")` is true.

Verify:

```bash
go build ./internal/cli/... && go test -run TestResumeGoal ./internal/cli/...
```

Dependencies: Slices 1, 2, 3.

---

## Slice 7 — Autocomplete + help table

**Goal:** `/goal` is in autocomplete and `/help`.

Files:

- `internal/tui/input.go` — append `"/goal"` to `slashCommands` (line 272-295).
  Add `case "/goal": return "Set or view the current session goal"`
  to `slashCommandDesc` (line 297-351).
- `internal/tui/commands.go` — extend `formatHelp()` (line 794-820) with
  the `**Long-running tasks:**` section between `**Git & Planning:**`
  and `**Display:**` per design.
- `internal/tui/input_test.go` (if exists) or `commands_test.go` — add
  `TestSlashCommands_IncludesGoal` asserting `"/goal"` is in the list.
  Add `TestFormatHelp_GoalSection` asserting the new section appears in
  the rendered help output.

Verify:

```bash
go test -run "TestSlashCommands|TestFormatHelp" ./internal/tui/...
```

Dependencies: Slice 3 (handler exists).

---

## Slice 8 — Session-resume rebuild across compact/reload

**Goal:** A `compact` mid-session followed by `--continue` lands the
prompt in the right state. Catches double-prefix / stale-cache bugs.

Files:

- `internal/session/store_test.go` — add `TestGoalContext_AfterCompactReload`:
    - Create session, `UpdateGoalContext(id, &GoalContext{…, Status: Paused, …})`,
    - `Compact`, close, reopen,
    - Assert `GetGoalContext` returns the same `Status: Paused` value.
    - Assert `instruction = AppendActiveGoal(base, "")` returns `base` unchanged
      on the empty objective path (no leaked block).
- `internal/session/store_test.go` — add `TestGoalContext_SurvivesUpdateTwice`:
    - Set Active, update to Paused, reload, flip to Active again, reload.
    - No fields are dropped; `UpdatedAt` advances each time.

Verify:

```bash
go test -run "TestGoalContext_AfterCompactReload|TestGoalContext_SurvivesUpdateTwice" ./internal/session/...
```

Dependencies: Slice 1.

---

## Order of execution

1. Slice 1 — schema + persistence
2. Slice 2 — agent validation + injection
3. Slice 3 — TUI dispatch + summary card
4. Slice 4 — prompt rebuild + edit overlay
5. Slice 5 — footer indicator
6. Slice 6 — `--continue` / `--session` rehydration
7. Slice 7 — autocomplete + help table
8. Slice 8 — compact/reload resilience

Slices 1 and 2 are independent. Slice 3 depends on both. Slices 4–8
depend on Slice 3.

## Gates (run after every slice)

```bash
# Always
go build ./...
go vet ./...

# Touched-by-slice subset (faster feedback than full test)
go test ./internal/session/...
go test ./internal/agent/...
go test ./internal/tui/...
go test ./internal/cli/...
```

Full test (run after plan completion, optional per-slice):

```bash
go test ./...
```

No race detector is enforced by CI today, but the work must remain safe
under:

```bash
go test -race ./internal/tui/... ./internal/session/... ./internal/agent/... ./internal/cli/...
```

(no new goroutines, no shared mutable state across the slash-command
paths).

## Constraints

- No new module dependency, no `go.mod` change.
- `len(byte)` based cap on objective length (not rune count).
- One objective per session, full stop.
- `/goal <text>` overwrites any existing goal silently (Q4).
- Footer indicator slot: same placement as token usage, model name (Q5).
- Interactive TUI mode only — `print`/`json`/`rpc` honor the loaded goal
  but expose no `/goal` command (Q6).
- Status enum: `Active | Paused | Blocked | Complete` (no
  `UsageLimited` / `BudgetLimited` in v1 — defer to token-budget spec).
- 4 000-byte cap (Q3).
- Q8 wording for the prompt block.
- Q7 wording for the summary card per status.
