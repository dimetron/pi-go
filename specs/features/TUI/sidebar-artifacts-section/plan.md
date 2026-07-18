# Sidebar Artifacts Section — Plan

Vertical slice: type → helper → renderer → tests → call site. Each
step is verified before the next.

## Step 1. Add type + helper in `sidebar.go`

- Add `ArtifactEntry` struct.
- Add `Artifacts []ArtifactEntry` to `SidebarRenderInput`.
- Add `formatBytes(int64) string` (file-local).

Verify: `go build ./internal/tui/...` clean.

## Step 2. Add render block in `sidebar.go`

- Insert the new block between Model and Git, after the
  `lines = append(lines, "")` that closes Model.
- Reuse existing `dim` and `heading` styles where possible; define
  `artHeading` locally (Mocha peach + bold) to match Memory/MCP.

Verify: `go build ./internal/tui/...` clean. Empty list still
renders identically to baseline (no behavioral change yet).

## Step 3. Tests in `sidebar_test.go`

- `TestSidebar_Artifacts_EmptyHidden`: assert no `Artifacts` line in
  output when `Artifacts: nil` and when `Artifacts: []ArtifactEntry{}`.
- `TestSidebar_Artifacts_Populated`: three entries → heading
  `Artifacts [3]` plus three lines, in order, with the correct icons.

Verify: `go test ./internal/tui/ -run Sidebar -v` passes.

## Step 4. Wire model layer in `tui.go`

- Add `artifactList()` method on `model` that returns `nil` for now
  (no artifact service wired — the sidebar will simply not render
  the section until the paste work lands).
- Add the `Artifacts:` line in the `sidebarInput := SidebarRenderInput{...}`
  literal at tui.go:1186.

Verify: `go build ./...` and `go test ./internal/tui/...` clean.
Existing tests still pass.

## Step 5. Final verification

- `go vet ./...`
- `go test ./...` (full repo)
- `git diff --stat` to confirm only the four files in the design's
  file touch list are modified.
