# Research: Sidebar Rendering & Test Patterns

## SidebarRenderInput — internal/tui/sidebar.go:29-62 (plan/run-relevant fields)

- `RunChecklist []ChecklistStep` (48) — steps from plan.md during /run
- `RunPhase string` (49) — current /run phase (empty if not running)
- `RunSpec string` (50) — spec name during /run
- `RunCycle int` (51) — current retry cycle
- `RunMaxCycle int` (52) — max retries
- `PlanPhases []PlanPhase` (53) — PDD phase checklist in plan mode; nil/empty = hidden

`PlanPhase` (76-79): `{Name string; Done bool}`. `ChecklistStep` (run.go:24-27):
`{Title string; Done bool}`.

`phaseArtifacts` (82-93): ordered 7 PDD phases → artifacts: Idea→rough-idea.md,
Requirements→requirements.md, Research→research (dir), Design→design.md, Outline→outline.md,
Plan→plan.md, Prompt→PROMPT.md.

`detectPlanPhases(specDir)` (98-105): stats each artifact; `Done = (os.Stat err == nil)`.
Degrades gracefully to all-incomplete on missing/unreadable dir.

## Rendering

`RenderSidebar(in)` (145-168): `w := max(in.Width, 10)`, `innerW := w - 3` (146-147), builds
`sidebarStyles` from palette, concatenates section line-slices in order (152-165): mood, model,
artifact, git, **mode**, agent, skill, memory, MCP, loading. Returns `sidebarFrame(...)`.

`sidebarModeLines` (242-259) — the dispatcher:
- If `len(in.RunChecklist) > 0 && in.RunPhase != ""` → `sidebarRunLines(...)` + `""` (243-245).
- Else renders `Mode` heading + `[mode]`, appends `sidebarActivityLines`, and if
  `len(in.PlanPhases) > 0` appends `sidebarPlanLines(...)` (255-257).

`sidebarRunLines` (263-287): `Run: <spec>` heading (truncated to `innerW+2`), `cycle <RunCycle>/<RunMaxCycle> ∙ <RunPhase>` in peach, then each step: done → green `"  [x] "`, else overlay `"  [ ] "` (title truncated to `max(innerW-5, 10)`). If `in.Running`, appends `sidebarActivityLines`.

`sidebarPlanLines` (293-312): nil if no PlanPhases. `Plan` heading, then per phase: done →
green `[x]`, first not-done → peach `▶` (current), rest → overlay `[ ]`. Title truncated to
`max(innerW-5, 10)`.

`sidebarFrame` (490-548): pads/truncates content to `targetH = max(0, in.Height-matrixH-statusH-ruleH)` (509), fills with dim `"  ∙∙∙"`, appends matrix/status/rule, boxes with `Width(w).MaxWidth(w)` and `Height(in.Height).MaxHeight(in.Height)` (539-545).

## Where inputs are assembled — tui.go

`sidebarRenderInput(sidebarWidth, panelRows int) SidebarRenderInput` (tui.go:1694-1737).
Base struct at 1695-1724.

Run fields (1725-1731), gated on `m.run != nil && m.run.phase != ""`:
- `in.RunChecklist = m.run.checklist`
- `in.RunPhase = m.run.phase`
- `in.RunSpec = m.run.specName`
- `in.RunCycle = m.run.retries + 1` (one-based)
- `in.RunMaxCycle = m.run.maxRetries`

Plan fields (1732-1735), gated on `m.mode == "plan" && m.planWorktreePath != "" && m.planTaskName != ""`:
- `specDir := filepath.Join(m.planWorktreePath, "specs", m.planTaskName)`
- `in.PlanPhases = detectPlanPhases(specDir)`

Call site (tui.go:1566): `sidebar := RenderSidebar(m.sidebarRenderInput(sidebarWidth, panelRows))`
inside the `if showSidebar` branch of `View()`, then `joinPanelSidebar(...)` (1567).

`runState` source fields (run.go:39-62): `specName`, `phase` (54: "running","gating","verifying",
"retrying","merging","done","failed"), `retries`, `maxRetries`, `checklist []ChecklistStep` (61).

## Test patterns

### Test helpers
- `plain(lines []string) string` — sidebar_sections_test.go:20-22: `ansi.Strip(strings.Join(lines, "\n"))`.
- `testSidebarStyles() sidebarStyles` — sidebar_sections_test.go:26-28: `newSidebarStyles(darkPalette)`.
- `sidebarMockTokenTracker` — sidebar_test.go:356-376.
- `newTestModel(t) *model` — teatest_test.go:19-36: minimal model (cfg ModelName/ProviderName, ctx/cancel, inputModel, chatModel, face, width 80, height 24).
- `cplxModel(t) *model` — complexity_commands_test.go:22-42: like newTestModel but with SessionID, WorkDir: t.TempDir(), palette: darkPalette, width 100, height 30. Used for `sidebarRenderInput` tests.
- `historyModel(t, entries...) *model` — history_key_test.go:9-22: model with history, statusModel, chatModel, width 100, height 40. Used by render-integrity tests.

### sidebar_test.go (whole-sidebar RenderSidebar tests)
- `TestRenderSidebar_RunChecklist` (266): builds `SidebarRenderInput{Width:30, Height:30, RunChecklist:[...], RunPhase:"running", RunSpec:"my-spec", RunCycle:2, RunMaxCycle:10, Running:true, ActiveTool:"edit"}` and asserts substrings `"Run: my-spec"`, `"cycle 2/10"`, `"[x] Setup project"`, `"[ ] Implement feature"`, `"edit"`, absence of `"[chat]"`.
- `TestRenderSidebar_RunChecklistTruncatesLongTitles` (304): asserts `"…"`.
- `TestRenderSidebar_RunChecklistThinkingNoTool` (322): asserts `"thinking"`.
- `TestRenderSidebar_EmptyChecklistShowsNormalMode` (341): empty checklist falls through to `Mode`.
- `TestRenderSidebar_PlanChecklist` (622): `Mode:"plan"`, `PlanPhases:[7 phases, Idea done]`; strips ANSI, asserts `"Plan"`, `"[x] Idea"`, `"▶ Requirements"`, `"[ ] Research"`, absence of `"[chat]"`.
- `TestRenderSidebar_NoPlanSection` (648): no `"▶ "` / `"[x] Idea"` when no PlanPhases.

### sidebar_sections_test.go (direct section-renderer tests)
- `TestSidebarModeLines` (169): table of `SidebarRenderInput` → expected substring via `plain(sidebarModeLines(tt.in, 27, testSidebarStyles()))`.
- `TestSidebarModeLinesRunChecklist` (194): asserts `"Run: my-spec"`, `"cycle 2/5 ∙ implement"`, `"[x] write tests"`, `"[ ] make them pass"`, absence of `"[chat]"`.
- `TestSidebarPlanLines` (424): `plain(sidebarPlanLines(in, 27, testSidebarStyles()))` asserts `"Plan"`, `"[x] Idea"`, `"▶ Requirements"`, `"[ ] Research"`. `TestSidebarPlanLines_Hidden` (439): 0 lines for empty input.
- `TestSidebarHiddenSections` (100): each section renderer returns 0 lines for empty input.
- `TestDetectPlanPhases` (356), `_AllDone` (386), `_None` (409): write artifacts into `t.TempDir()` and assert the 7 phases' names and Done flags.

### sidebarRenderInput assembly test
- `TestCplxSidebarRenderInput` — complexity_commands_test.go:1666-1707: uses `cplxModel(t)`;
  asserts size passthrough, empty run fields when no run, and with
  `m.run = &runState{phase:"running", specName:"spec-1", retries:2, maxRetries:10, checklist:[...]}`
  asserts `RunPhase`, `RunSpec`, `RunCycle==3` (one-based), `RunMaxCycle==10`, `len(RunChecklist)==1`.

### Render-integrity tests (render_integrity_test.go)
- `TestFrameHeightFitsTerminal` (83), `TestSidebarIsFixedSizeAndRightAligned` (120),
  `TestChatOutputCannotTakeTheSidebarsColumns` (157), `TestFrameIntegrityOnRealContent` (199).
  Build a `historyModel`, set `m.width`/`m.height`, call `m.applyResize()`, append
  `realisticChat()` messages, assert every row is exactly terminal width, the rail holds its
  column (`railCol := m.mainWidth() - railWidth`), and the sidebar occupies exactly
  `SidebarWidth` columns (`width - sidebarStart == SidebarWidth`, 140-143).

## SidebarWidth and inner-width math

- `SidebarWidth = 23` — sidebar.go:26. Every consumer reads it (mainWidth, render-integrity column check).
- Inner width: `w := max(in.Width, 10)`, `innerW := w - 3` (padding + border) — sidebar.go:146-147.
- Checklist titles truncate to `max(innerW-5, 10)` to leave room for the `"  [x] "` prefix (274, 300).
- Run heading truncates to `innerW+2` (265).

**Noteworthy:** `sidebarRunLines` has no direct unit test — covered indirectly through
`sidebarModeLines` (`TestSidebarModeLinesRunChecklist`) and whole-sidebar `RenderSidebar`
tests. The run-checklist branch requires **both** `len(RunChecklist) > 0` **and**
`RunPhase != ""`; an empty checklist falls through to the normal `Mode` display
(pinned by `TestRenderSidebar_EmptyChecklistShowsNormalMode`).
