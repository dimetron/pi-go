# Plan Mode Phase Checklist

## Objective
Add a "Plan" section to the right sidebar that shows the 7 PDD phases as a
checklist when in plan mode, so the user can see at a glance how far a `/plan`
session has progressed. The current phase is detected by scanning the spec
directory for artifact files. All changes are confined to `internal/tui/`.

## Key Requirements
1. **7-phase checklist** — Idea, Requirements, Research, Design, Outline, Plan,
   Prompt, each mapped to a spec artifact file/dir that marks it complete.
2. **Current phase detection** — the current phase is the first whose artifact is
   missing; if all exist, the plan is complete (all checked).
3. **Plan-mode only** — the section renders only when in plan mode; hidden
   otherwise.
4. **Reuse `/run` checklist style** — `[x]` (green) for done, `[ ]` (overlay) for
   future, plus a `▶` marker on the current phase.

## Acceptance Criteria
### Plan section rendering
- Given the user is in plan mode, when the sidebar renders, then a "Plan" section
  shows all 7 phases with the current phase marked `▶` and completed phases `[x]`.
- Given a phase's artifact file exists, when the sidebar renders, then that phase
  is shown as completed (`[x]`).
- Given the current phase's artifact is missing, when the sidebar renders, then
  that phase is shown as current (`▶`) and later phases as `[ ]`.
- Given all 7 artifacts exist, when the sidebar renders, then all phases are shown
  as completed.
- Given the user is NOT in plan mode, when the sidebar renders, then no "Plan"
  section is shown.
- Given the spec directory cannot be read, when the sidebar renders, then the
  checklist degrades gracefully (no crash; phases shown as incomplete).

## Implementation Slices
1. **Add `PlanPhase` type + `detectPlanPhases`** — add `PlanPhase{Name, Done}` and
   `detectPlanPhases(specDir string) []PlanPhase` with the 7-phase artifact mapping
   (`rough-idea.md`, `requirements.md`, `research` dir, `design.md`, `outline.md`,
   `plan.md`, `PROMPT.md`). Files: `internal/tui/sidebar.go`. Verify:
   `go build ./internal/tui/...`. Parallel-safe: no.
2. **Add `sidebarPlanLines` renderer** — render the "Plan" heading and 7 phases
   with `▶`/`[x]`/`[ ]` markers; return `nil` when `len(in.PlanPhases) == 0`.
   Files: `internal/tui/sidebar.go`. Verify: `go build ./internal/tui/...`.
   Parallel-safe: no.
3. **Wire into `SidebarRenderInput` + `sidebarRenderInput`** — add `PlanPhases
   []PlanPhase` field to `SidebarRenderInput`; in `sidebarModeLines` render the
   Plan section when `PlanPhases` is non-empty; in `sidebarRenderInput` (tui.go)
   populate `in.PlanPhases` when `m.mode == "plan" && m.planWorktreePath != "" &&
   m.planTaskName != ""` via `detectPlanPhases(filepath.Join(m.planWorktreePath,
   "specs", m.planTaskName))`. Files: `internal/tui/sidebar.go`,
   `internal/tui/tui.go`. Verify: `go test ./internal/tui/...`. Parallel-safe: no.
4. **Add tests** — `TestDetectPlanPhases`, `TestDetectPlanPhases_AllDone`,
   `TestDetectPlanPhases_None`, `TestSidebarPlanLines`, `TestSidebarPlanLines_Hidden`
   in `internal/tui/sidebar_sections_test.go`; `TestRenderSidebar_PlanChecklist`,
   `TestRenderSidebar_NoPlanSection` in `internal/tui/sidebar_test.go`. Files:
   `internal/tui/sidebar_sections_test.go`, `internal/tui/sidebar_test.go`.
   Verify: `go test ./internal/tui/...` and `go vet ./internal/tui/...`.
   Parallel-safe: no.

## Execution Model
Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the
**Coordinator**; it delegates rather than implements.

- **Workers**: one `worker` subagent per slice. No slices are parallel-safe, so run
  them one at a time, in order.
- **Verifier**: after the last slice, a `code-reviewer` subagent checks the Done
  Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to
  10 cycles total.

## Done Criteria
The Verifier checks these against the diff, not against the checklist. Each must
be objectively checkable by reading code or running a command.
- [ ] `PlanPhase{Name, Done}` type and `detectPlanPhases(specDir)` exist and map
      the 7 PDD phases to their artifact files/dirs — see `TestDetectPlanPhases`.
- [ ] `sidebarPlanLines` renders a "Plan" heading and the 7 phases with `▶` on the
      current phase, `[x]` on done phases, `[ ]` on future phases — see
      `TestSidebarPlanLines`.
- [ ] `sidebarPlanLines` returns nil (section hidden) when `PlanPhases` is empty —
      see `TestSidebarPlanLines_Hidden`.
- [ ] `SidebarRenderInput` has a `PlanPhases []PlanPhase` field, populated in
      `sidebarRenderInput` only when `m.mode == "plan"` and plan fields are set.
- [ ] `sidebarModeLines` renders the Plan section when `PlanPhases` is non-empty.
- [ ] `TestRenderSidebar_PlanChecklist` asserts the "Plan" section and phase markers
      appear and `[chat]` is absent.
- [ ] `TestRenderSidebar_NoPlanSection` asserts no "Plan" section appears when not
      in plan mode.
- [ ] `go test ./internal/tui/...` passes.
- [ ] No slice is left as a stub, TODO, or panic("not implemented").

## Gates
- **build**: `go build ./internal/tui/...`
- **test**: `go test ./internal/tui/...`
- **vet**: `go vet ./internal/tui/...`
- **lint**: `golangci-lint run ./internal/tui/...`

## Reference
- Design: `specs/features/TOO/022-plan-mode-phase-checklist/design.md`
- Outline: `specs/features/TOO/022-plan-mode-phase-checklist/outline.md`
- Plan: `specs/features/TOO/022-plan-mode-phase-checklist/plan.md`
- Requirements: `specs/features/TOO/022-plan-mode-phase-checklist/requirements.md`
- Research: `specs/features/TOO/022-plan-mode-phase-checklist/research/`

## Constraints
- The checklist must only render in plan mode (`m.mode == "plan"`).
- The current phase is the first phase whose artifact is missing; if all artifacts
  exist, all phases are checked.
- The sidebar width (23) must not change; labels must fit (use `truncateLabel`).
- Re-scan the spec directory on every render (cheap `os.Stat` calls).
- Must not break the existing `/run` checklist rendering.
- No changes to the `/plan` command flow or the PDD SOP itself.
