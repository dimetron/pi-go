# Research: Sidebar rendering pipeline

## Where `SidebarRenderInput` is built and rendered

- **Call site:** `internal/tui/tui.go:1565` — `sidebar := RenderSidebar(m.sidebarRenderInput(sidebarWidth, panelRows))`, gated by `if showSidebar {` at line 1564.
- **Constructor:** `internal/tui/tui.go:1693` — `func (m *model) sidebarRenderInput(sidebarWidth, panelRows int) SidebarRenderInput`. Builds the struct literal at lines 1694–1723, then conditionally fills run fields at lines 1724–1730:
  ```go
  if m.run != nil && m.run.phase != "" {
      in.RunChecklist = m.run.checklist
      in.RunPhase = m.run.phase
      in.RunSpec = m.run.specName
      in.RunCycle = m.run.retries + 1
      in.RunMaxCycle = m.run.maxRetries
  }
  ```
- `Mode: m.mode` is set at `tui.go:1702`.

## How `m.mode` flows in

- `m.mode` is a `string` field on the model, declared at `tui.go:65` (`// "chat" or "plan" — shown in status bar`).
- Never initialized in `newModel` — stays zero value `""`.
- Set to `"plan"` in exactly one production location: `plan.go:470` (`m.mode = "plan"`).
- Passed verbatim into `SidebarRenderInput.Mode` at `tui.go:1702`.
- Sidebar defaults empty to `"chat"` at render time: `sidebar.go:214` — `mode := cmp.Or(in.Mode, "chat")`. `"plan"` renders as `[plan]` in peach (`sidebar.go:215-216`).
- `m.mode` is **never reset** in production code.

## How the `/run` checklist is populated

- `ChecklistStep` type: `run.go:20-24` — `{Title string; Done bool}`.
- Initial parse: `run.go:357` — `checklist := parsePlanChecklist(m.cfg.WorkDir, specName)`.
- `parsePlanChecklist` (`run.go:1732-1739`) reads `filepath.Join(workDir, "specs", specName, "plan.md")` and calls `extractChecklist`.
- `extractChecklist` (`run.go:1760-1798`) parses checkbox lines (`^-\\s+\\[([ xX])\\]\\s+(.+)`) or falls back to `### Slice N: Title` headings.
- Stored on `m.run.checklist` in `startRunAgent` (`run.go:469`) and `handleRunParallel` (`run.go:561`).
- Live refresh: `refreshRunChecklist` (`run.go:1489-1500`) re-reads plan.md after write/edit ops.
- On successful merge, all steps marked done: `run.go:1328-1330`.

## Rendering side

- `RenderSidebar` (`sidebar.go:111`) calls `sidebarModeLines` (`sidebar.go:208`), which at line 209 checks `if len(in.RunChecklist) > 0 && in.RunPhase != ""` and delegates to `sidebarRunLines` (`sidebar.go:226-250`).
- `sidebarRunLines` renders the spec name, `cycle %d/%d ∙ %s`, and the `[x]`/`[ ]` step list. Done steps render `"  [x] "` in green, pending `"  [ ] "` in overlay color.
- `sidebarModeLines` otherwise renders a `Mode` heading and `[plan]` in peach when `in.Mode == "plan"`.
