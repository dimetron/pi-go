# Plan: Plan Mode Phase Checklist

## Context

Add a "Plan" section to the right sidebar that shows the 7 PDD phases as a
checklist when in plan mode. The current phase is detected by scanning the spec
directory for artifact files. All changes are confined to `internal/tui/`. See
`design.md` for full detail.

## Slices

### Slice 1: Add `PlanPhase` type + `detectPlanPhases`

**What to implement:**
- In `internal/tui/sidebar.go`:
  - Add `PlanPhase` type:
    ```go
    // PlanPhase is one PDD phase in the plan-mode sidebar checklist.
    type PlanPhase struct {
        Name string // short label, e.g. "Requirements"
        Done bool   // artifact file exists
    }
    ```
  - Add `phaseArtifacts` mapping (7 phases → artifact file/dir name):
    ```go
    var phaseArtifacts = []struct {
        Name     string
        Artifact string
    }{
        {"Idea", "rough-idea.md"},
        {"Requirements", "requirements.md"},
        {"Research", "research"},
        {"Design", "design.md"},
        {"Outline", "outline.md"},
        {"Plan", "plan.md"},
        {"Prompt", "PROMPT.md"},
    }
    ```
  - Add `detectPlanPhases(specDir string) []PlanPhase` that stats each artifact
    under `specDir` and returns the phases in order with `Done` set.
- Add `os` and `path/filepath` to imports if not already present.

**Verification checkpoint:** `go build ./internal/tui/...` compiles.

**Dependencies:** none.

**Parallel-safe:** no (touches `sidebar.go`).

### Slice 2: Add `sidebarPlanLines` renderer

**What to implement:**
- In `internal/tui/sidebar.go`, add `sidebarPlanLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string`:
  - Return `nil` when `len(in.PlanPhases) == 0` (hidden).
  - Render a "Plan" heading (e.g. `st.heading.Render("  Plan")`).
  - For each phase, determine its state: the current phase is the first with
    `Done == false`; render it with a `▶` marker (e.g. `"  ▶ "` in peach), done
    phases as `"  [x] "` in green, future phases as `"  [ ] "` in overlay.
  - Truncate phase names to fit `innerW` (reuse `truncateLabel`).
  - End with a blank line to match section spacing.

**Verification checkpoint:** `go build ./internal/tui/...` compiles.

**Dependencies:** Slice 1.

**Parallel-safe:** no (touches `sidebar.go`).

### Slice 3: Wire into `SidebarRenderInput` + `sidebarRenderInput`

**What to implement:**
- In `internal/tui/sidebar.go`, add `PlanPhases []PlanPhase` field to
  `SidebarRenderInput` with a doc comment (nil/empty = hidden).
- In `internal/tui/sidebar.go`, in `sidebarModeLines`, when `in.PlanPhases` is
  non-empty, render the Plan section (call `sidebarPlanLines`) in addition to the
  mode indicator.
- In `internal/tui/tui.go`, in `sidebarRenderInput`, populate `in.PlanPhases` when
  in plan mode:
  ```go
  if m.mode == "plan" && m.planWorktreePath != "" && m.planTaskName != "" {
      specDir := filepath.Join(m.planWorktreePath, "specs", m.planTaskName)
      in.PlanPhases = detectPlanPhases(specDir)
  }
  ```
- Ensure `path/filepath` is imported in `tui.go`.

**Verification checkpoint:** `go test ./internal/tui/...` passes (existing tests
unaffected).

**Dependencies:** Slices 1 and 2.

**Parallel-safe:** no (touches `sidebar.go` and `tui.go`).

### Slice 4: Add tests

**What to implement:**
- In `internal/tui/sidebar_sections_test.go`:
  - `TestDetectPlanPhases` — create a temp spec dir, write a subset of artifact
    files, assert `detectPlanPhases` returns correct `Done` flags and order.
  - `TestDetectPlanPhases_AllDone` — write all 7 artifacts, assert all `Done`.
  - `TestDetectPlanPhases_None` — empty dir, assert all `Done == false`.
  - `TestSidebarPlanLines` — call `sidebarPlanLines` directly with `PlanPhases`
    set, assert rendered lines contain phase labels, `[x]`/`[ ]` markers, and `▶`
    on the current phase.
  - `TestSidebarPlanLines_Hidden` — with empty `PlanPhases`, assert the section
    returns 0 lines.
- In `internal/tui/sidebar_test.go`:
  - `TestRenderSidebar_PlanChecklist` — render full sidebar with `Mode: "plan"`
    and `PlanPhases` set, assert the "Plan" section and phase markers appear;
    assert `[chat]` is absent.
  - `TestRenderSidebar_NoPlanSection` — render with `Mode: "chat"` and no
    `PlanPhases`, assert no "Plan" section appears.

**Verification checkpoint:** `go test ./internal/tui/...` passes; `go vet
./internal/tui/...` clean.

**Dependencies:** Slices 1, 2, and 3.

**Parallel-safe:** no (touches test files).

## Final Verification

After all slices:
- `go build ./...`
- `go test ./internal/tui/...`
- `go vet ./internal/tui/...`
- `golangci-lint run ./internal/tui/...`

## Progress

- [x] Step 1: Add `PlanPhase` type + `detectPlanPhases`
- [x] Step 2: Add `sidebarPlanLines` renderer
- [x] Step 3: Wire into `SidebarRenderInput` + `sidebarRenderInput`
- [x] Step 4: Add tests
