# Sidebar Artifacts Section — Summary

## What landed

A new **Artifacts** section in the right sidebar that lists every
ADK artifact attached to the current session. Hidden when empty.

## Files touched

| File | Lines | Purpose |
|---|---|---|
| `internal/tui/sidebar.go` | +55 | `ArtifactEntry` type, `Artifacts` field on `SidebarRenderInput`, `formatBytes` helper, render block between Model and Git. |
| `internal/tui/sidebar_test.go` | +73 | Two new tests: `TestRenderSidebar_Artifacts_EmptyHidden` and `TestRenderSidebar_Artifacts_Populated`. |
| `internal/tui/tui.go` | +16 | One line in the `sidebarInput := SidebarRenderInput{...}` literal passing `m.artifactList()`. Plus a stub `artifactList()` method that returns `nil` until the artifact service is wired. |
| `specs/features/TUI/sidebar-artifacts-section/` | new | Idea, requirements, design, plan. |

## What the renderer does

For each `ArtifactEntry`:
- `🖼` icon for `image/*` MIME types, `📎` otherwise.
- Filename truncated with `…` to fit the sidebar's inner width
  (mirroring the Git branch pattern).
- Size in humanized form: `812 B` / `124 KB` / `2.1 MB`.

Heading: `Artifacts [N]` in Mocha peach (`#fab387`), bold, matching
the palette already used by Memory and MCP Tools.

## What the model layer does (today)

Nothing. `m.artifactList()` returns `nil`. The sidebar renders
nothing. This is intentional: the artifact service is plumbed
through `agent.Config` in the image-paste feature, and the call site
is now in place. Wiring the body is a follow-up — the
spec's `design.md` has the full data-flow shape ready to fill in.

## Verification

- `go vet ./...` — clean.
- `go test ./...` — all packages pass, including the two new
  sidebar tests.
- Manual: dump-test confirmed the section appears between Model and
  Mode with the correct icons, sizes, and truncation.

## Lessons / follow-ups

- The `m.artifactService()` accessor is the next thing to add.
  The natural place is on the `agent.Agent` struct (line 397 in
  `internal/agent/agent.go`), with a getter exposed to the TUI.
- Once wired, the sidebar will reflect artifacts created by the
  paste/drop UX in real time — no extra `tea.Tick` needed if the
  paste flow triggers a redraw (it does, via `submitPrompt`).
- Click-to-view and click-to-remove are deliberately deferred.
  They need selection-mode plumbing analogous to what the Git
  branch picker uses; the data is ready but the interaction
  surface is its own spec.
- The two-space gap between filename and size (`"  "` literal)
  was a deliberate choice for alignment. If a future section
  needs a tighter layout, the `formatBytes` width assumption
  (`maxName := innerW - 2 - 2 - 10`) is the one knob to revisit.
