# Outline — `/goal` Slash Command

Vertical slices. Each builds, tests, and is a checkpoint on its own.

## Slice 1 — Session schema + persistence

Add `GoalContext`, `GoalStatus`, `Meta.GoalContext *GoalContext`,
`UpdateGoalContext`, `GetGoalContext` to `internal/session/store.go`. Round-trip
test in `store_test.go`. (No TUI, no agent changes.)

## Slice 2 — Agent validation + injection

Add `MaxGoalObjectiveChars`, `ValidateGoalObjective`, `AppendActiveGoal` to
`internal/agent/agent.go`. Tests for empty / 4 001 / 4 000 cases and for the
exact wording of the injected block. (No TUI, no session-store wiring.)

## Slice 3 — TUI dispatch + summary card

In `internal/tui/goal.go` (new): `handleGoalCommand(args)` wired from
`commands.go`. Implement: bare `/goal` (summary), `/goal <text>` (set), `/goal
pause`, `/goal resume`, `/goal clear`. Persist each via Slice 1. Drop
validation through Slice 2. Stub `applyGoalToPrompt` so it builds but doesn't
rebuild yet. Tests in `commands_test.go` per sub-command.

## Slice 4 — Prompt rebuild on edit / pause / resume

Wire `applyGoalToPrompt` to actually call `m.cfg.Agent.RebuildWithInstruction`.
Add `m.goal *session.GoalContext` and `goalEditing/goalDraft` overlay state.
Add `/goal edit` using the `create_skill.go` overlay pattern. Tests: prompt
contains block when active, doesn't when paused.

## Slice 5 — Footer indicator

Add `GoalIndicator string` to `StatusRenderInput`. Implement
`goalIndicatorText()`. Render in `status.go`. `statusRenderInput` wires it.
Five-case test in `tui_test.go`.

## Slice 6 — Resume across `--continue` / `--session`

In `internal/cli/interactive.go:~298`, after `instructionParts.String()`,
call `AppendActiveGoal` when `GetGoalContext` returns a goal with
`Status=Active`. Prepend the resume-hint message when the loaded goal is
`Paused` or `Blocked`. Test in `interactive_test.go` (or extend
`cli_test.go`).

## Slice 7 — Autocomplete + help table

Append `/goal` to `slashCommands` (`input.go:272`). Add case to
`slashCommandDesc`. Add the `**Long-running tasks:**` section to `formatHelp`
between `**Git & Planning:**` and `**Display:**`. Test in `input_test.go`.

## Slice 8 — Session-resume rebuild via RebuildWithInstruction

Cover the case where a prompt was rebuilt by `/goal` mid-session and the
session is later resumed across a process restart. Confirms
`UpdateGoalContext` survives a `compact` + re-load cycle. Test asserts no
double-prefix when `Status=Paused`.

## Order of changes

1 → 2 → 3 → 4 → 5 → 6 → 7 → 8. Each slice compiles and tests in isolation.
Slices 1–3 are forced; 4–8 are follow-ups that depend on 3.

## Key new symbols

```go
// session/store.go
type GoalStatus string
type GoalContext struct { /* … see design */ }
func (s *FileService) UpdateGoalContext(id string, ctx *GoalContext) error
func (s *FileService) GetGoalContext(id string) (*GoalContext, error)

// agent/agent.go
const MaxGoalObjectiveChars = 4_000
func ValidateGoalObjective(string) error
func AppendActiveGoal(prompt, objective string) string

// tui/goal.go  (new)
func (m *model) handleGoalCommand(args []string) (tea.Model, tea.Cmd)
func (m *model) printGoalSummary()
func (m *model) setGoalFromInput(text string)
func (m *model) beginGoalEdit()
func (m *model) commitGoalEdit()
func (m *model) transitionGoal(to GoalStatus, notice string)
func (m *model) clearGoal()
func (m *model) applyGoalToPrompt()
func (m *model) persistGoal() error
func (m *model) goalIndicatorText() string
func truncate(s string, n int) string

// tui/types.go — extend model
goal        *session.GoalContext
goalEditing bool
goalDraft   []rune

// tui/status.go — extend StatusRenderInput
GoalIndicator string

// tui/input.go — append "/goal" + desc case
```

## Gates (per-slice verification)

```bash
go build ./...
go vet ./...
go test ./internal/session/...
go test ./internal/agent/...
go test ./internal/tui/...
go test ./internal/cli/...
```
