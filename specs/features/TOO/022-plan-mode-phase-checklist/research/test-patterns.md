# Research: Test patterns for sidebar and plan mode

## Sidebar rendering tests

### `internal/tui/sidebar_test.go` (620 lines)
- Tests call `RenderSidebar(SidebarRenderInput{...})` directly with a struct literal.
- Assertions via `strings.Contains(result, "...")` on the full rendered string, with `t.Error`/`t.Errorf`. Both positive (`if !strings.Contains(...)`) and negative (`if strings.Contains(...)`).
- For ANSI-styled output, `ansi.Strip(result)` is used before substring checks.
- `TestRenderSidebar_RunChecklist` (line 266) sets `RunChecklist`, `RunPhase`, `RunSpec`, `RunCycle`, `RunMaxCycle`, `Running`, `ActiveTool`.
- `TestRenderSidebar_RunChecklist` (lines 299-301) asserts `"[chat]"` is **absent** when a run checklist is active.

### `internal/tui/sidebar_sections_test.go` (352 lines)
- Tests individual section renderers directly (not whole `RenderSidebar`).
- Helpers: `plain(lines []string) string` (lines 18-20) strips ANSI; `testSidebarStyles() sidebarStyles` (lines 24-26) returns `newSidebarStyles(darkPalette)`.
- Section renderers called directly, e.g. `sidebarModeLines(tt.in, 27, testSidebarStyles())` (line 184).
- Table-driven tests with `t.Run` subtests and `t.Parallel()`. Each case has `in SidebarRenderInput`, `wantParts []string`, optional `absent string`.
- `TestSidebarHiddenSections` (lines 98-123) — the canonical "section hidden" test: calls each section renderer with empty `SidebarRenderInput{}` and asserts it returns **0 lines**.
- `TestSidebarModeLinesRunChecklist` (lines 192-214) — asserts `"[chat]"` is **absent** when a run checklist is present.

## Plan-mode tests

### `internal/tui/plan_test.go` (604 lines)
- Plain `func TestXxx(t *testing.T)` functions, no shared harness.
- Test `copyDir`/`copyOverwrite`, `toKebabCase`, `createSpecSkeleton`, `TestBuildPlanInstruction_*`, `TestFinishPlanWorktree_*`, `TestShortTaskName`.
- `TestFinishPlanWorktree_*` use `initRunTestRepo(t)` and `subagent.NewOrchestrator` to create a real git worktree, then construct a `&model{...}` with plan fields and call `m.finishPlanWorktree()`.

### `internal/tui/plan_run_e2e_test.go` (625 lines)
- E2E tests. Create spec skeletons in `t.TempDir()`, assert dir/file existence with `os.Stat`.
- Construct `&model{cfg: Config{...}, chatModel: ChatModel{...}, run: &runState{...}}` and call handlers.

## `ChecklistStep` type

Defined in `internal/tui/run.go:20-24`:
```go
type ChecklistStep struct {
    Title string
    Done  bool
}
```
Consumed by the sidebar via `SidebarRenderInput.RunChecklist []ChecklistStep` (sidebar.go:47) and rendered in `sidebarRunLines` (sidebar.go:226-250).

## Presence/absence test pattern

- `TestRenderSidebar_Minimal` (sidebar_test.go:15-34) asserts `"Context"` absent, `"Model"`/`"Mode"` present.
- `TestRenderSidebar_MCPTools_Empty` (lines 458-467) asserts `"MCP Tools"` absent when `MCPTools` nil.
- `TestRenderSidebar_MemoryStatus_Nil` (lines 538-547) asserts `"Memory ["` absent when `MemoryStatus` nil.
- `TestRenderSidebar_Artifacts_EmptyHidden` (lines 549-573) asserts `"Artifacts ["` absent for empty lists.
