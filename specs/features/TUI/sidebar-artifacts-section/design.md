# Sidebar Artifacts Section — Design

## Data flow

```
   ┌──────────────────┐    once per render    ┌────────────────────────┐
   │ model.artifactList()│ ────────────────► │ SidebarRenderInput     │
   │  (reads ADK       │  []ArtifactEntry    │   .Artifacts           │
   │   artifact.List)  │                     └─────────┬──────────────┘
   └──────────────────┘                               │
                                                     ▼
                                          ┌────────────────────────┐
                                          │ RenderSidebar(...)     │
                                          │  new "Artifacts" block │
                                          └────────────────────────┘
```

`RenderSidebar` stays a pure function. The model layer is responsible
for the one ADK call (`artifact.Service.List`) per `View()` render
and for translating the result into `[]ArtifactEntry`.

## Data shape

```go
// ArtifactEntry is one row in the Artifacts sidebar section.
type ArtifactEntry struct {
    Filename string // ADK artifact key, e.g. "screenshot.png"
    Size     int64  // bytes stored in the artifact's genai.Part
    Mime     string // optional; "image/png" gets the 🖼 icon
}
```

Added to `SidebarRenderInput` (sidebar.go):

```go
Artifacts []ArtifactEntry
```

## Model layer

New method on `model`:

```go
// artifactList returns a snapshot of the artifacts attached to the
// current session. Returns nil if no artifact service is wired
// (current state of the codebase) — the sidebar treats nil/empty
// identically.
func (m *model) artifactList() []ArtifactEntry {
    svc := m.artifactService()  // may be nil; check first
    if svc == nil {
        return nil
    }
    resp, err := svc.List(ctx, &artifact.ListRequest{
        AppName: agent.AppName,
        UserID:  agent.DefaultUserID,
        SessionID: m.sessionID,
    })
    if err != nil { return nil }
    out := make([]ArtifactEntry, 0, len(resp.FileNames))
    for _, name := range resp.FileNames {
        // Load to get size + mime. Skip entries that fail to load
        // (deleted between List and Load race).
        lr, err := svc.Load(ctx, &artifact.LoadRequest{...})
        if err != nil { continue }
        var size int64
        var mime string
        if lr.Part != nil && lr.Part.InlineData != nil {
            size = int64(len(lr.Part.InlineData.Data))
            mime = lr.Part.InlineData.MIMEType
        }
        out = append(out, ArtifactEntry{
            Filename: name, Size: size, Mime: mime,
        })
    }
    return out
}
```

Two questions intentionally deferred to follow-ups:

1. Where does `m.artifactService()` come from? The runner exposes it
   via `runner.Config.ArtifactService` but pi-go doesn't currently
   set one. For this slice, the helper returns nil — the sidebar
   simply renders nothing. Wiring it through `agent.Config` is a
   one-liner when the paste work lands.
2. Caching: we re-list on every render. Each `List` is a single
   `omap` scan (`O(N)` in artifact count) which is fine for the
   expected scale (handful of items). If profiling later shows it,
   cache behind a `tea.Tick`.

## Renderer

New block in `RenderSidebar`, immediately after the Model section
(after sidebar.go:137) and before the Git section:

```go
// --- Artifacts section ---
if len(in.Artifacts) > 0 {
    artHeading := lipgloss.NewStyle().
        Foreground(lipgloss.Color("#fab387")).Bold(true) // Mocha peach
    lines = append(lines,
        artHeading.Render(fmt.Sprintf("  Artifacts [%d]", len(in.Artifacts))))

    for _, a := range in.Artifacts {
        icon := "📎"
        if strings.HasPrefix(a.Mime, "image/") {
            icon = "🖼"
        }
        size := formatBytes(a.Size)
        // "  📎 filename.txt  (812 B)" — 2 indent + icon + space +
        // name + 2-space gap + size. Budget for size: 10 chars max.
        maxName := innerW - 2 - 2 - 10
        if maxName < 6 {
            maxName = 6
        }
        name := a.Filename
        if runewidth.StringWidth(name) > maxName {
            name = runewidth.Truncate(name, maxName-1, "…")
        }
        lines = append(lines, dim.Render(
            fmt.Sprintf("  %s %s  %s", icon, name, size)))
    }
    lines = append(lines, "")
}
```

### `formatBytes`

```go
// formatBytes renders a byte count as "812 B" / "124 KB" / "2.1 MB".
// No GB tier — session artifacts won't hit that.
func formatBytes(n int64) string {
    switch {
    case n < 1024:
        return fmt.Sprintf("%d B", n)
    case n < 1024*1024:
        return fmt.Sprintf("%d KB", n/1024)
    default:
        return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
    }
}
```

### Icon choice

`🖼` (U+1F5BC) for images, `📎` (U+1F4CE) otherwise. Both are in the
emoji block; they will render as monochrome glyphs in most terminals
and color glyphs in iTerm2/Kitty. No new font dependency.

## Test plan

Two new cases in `sidebar_test.go`, following the existing
`RenderSidebar(SidebarRenderInput{...})` pattern:

1. **Empty list** — assert no `Artifacts` substring in the rendered
   output, and line count is unchanged from a baseline with no
   `Artifacts` field.
2. **Three entries** — assert the heading `Artifacts [3]` appears,
   and three lines with the expected icon/filename/size are present
   in the right order.

Existing tests in the file are not modified.

## File touch list

| File | Change |
|---|---|
| `internal/tui/sidebar.go` | Add `ArtifactEntry` type, `Artifacts` field on `SidebarRenderInput`, `formatBytes` helper, new section in `RenderSidebar`. |
| `internal/tui/sidebar_test.go` | Add empty-list and populated-list test cases. |
| `internal/tui/tui.go` | Pass `Artifacts: m.artifactList()` into the `sidebarInput` literal. |
| `internal/tui/tui.go` (model) | Add `artifactList()` method and the `artifactService()` accessor that returns nil for now. |
