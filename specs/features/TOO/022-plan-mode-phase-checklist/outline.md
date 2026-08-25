# Outline: Plan Mode Phase Checklist

## Overview

Add a "Plan" section to the right sidebar that shows the 7 PDD phases as a
checklist when in plan mode. The current phase is detected by scanning the spec
directory for artifact files. All changes are confined to `internal/tui/`.

## Phases / Slices

1. **Add `PlanPhase` type + `detectPlanPhases`** — add `PlanPhase{Name, Done}` and
   `detectPlanPhases(specDir)` with the 7-phase artifact mapping. Verify: package
   compiles.
2. **Add `sidebarPlanLines` renderer** — render the "Plan" heading and 7 phases
   with `▶`/`[x]`/`[ ]` markers; return `nil` when hidden. Verify: package
   compiles.
3. **Wire into `SidebarRenderInput` + `sidebarRenderInput`** — add `PlanPhases`
   field, populate it in `sidebarRenderInput` when in plan mode, and call
   `sidebarPlanLines` from `sidebarModeLines`. Verify: existing tests pass.
4. **Add tests** — `detectPlanPhases` unit tests, `sidebarPlanLines` section
   tests, and full sidebar render tests. Verify: new tests pass.

## Order of Changes & Testing

Slices 1 → 2 → 3 → 4, strictly sequential. Each slice compiles and passes tests
independently. Slices 2-4 depend on earlier slices.

## Key Type Signatures

```go
// New type:
type PlanPhase struct {
    Name string
    Done bool
}

// New function:
func detectPlanPhases(specDir string) []PlanPhase

// New section renderer:
func sidebarPlanLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string

// SidebarRenderInput gains one field:
type SidebarRenderInput struct {
    // ...existing fields...
    PlanPhases []PlanPhase // new: PDD phase checklist in plan mode; nil/empty = hidden
}
```

## Parallel-Safety

All slices touch `sidebar.go` and/or `tui.go` — none are parallel-safe; run
strictly in order.
