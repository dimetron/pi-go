# Sidebar Artifacts Section — Requirements

## Functional

1. **R1.** When at least one artifact exists for the current session,
   the sidebar renders an `Artifacts [N]` section between the Model
   section and the Git section.
2. **R2.** When the artifact list is empty, the section is omitted
   entirely. No heading, no empty-state row, no `Artifacts [0]` line.
3. **R3.** Each artifact renders as one line containing:
   - an icon (`🖼` for `image/*` MIME types, `📎` otherwise),
   - the filename (truncated with `…` to fit the sidebar width),
   - the size in humanized form (`812 B`, `124 KB`, `2.1 MB`).
4. **R4.** The list updates on the same cadence as other sidebar
   state — i.e. it is read once per `View()` render. No new
   `tea.Tick`; no new goroutine.
5. **R5.** Artifacts with the same filename but different versions
   appear once (latest version wins). The renderer does not need to
   surface version numbers.

## Non-functional

- **N1.** Zero new runtime dependencies. Everything used already lives
  in `go.mod` (lipgloss, runewidth, genai, ADK artifact package).
- **N2.** Renderer remains pure. It must not call into ADK services
  directly. All data arrives via `SidebarRenderInput`.
- **N3.** The section is opt-in. Empty list = no render. Existing
  users see no change until something actually populates the list.
- **N4.** No regression in the existing 8 sidebar tests. New tests
  cover the empty case (hidden) and the populated case (renders the
  expected lines).

## Acceptance

- `go test ./internal/tui/...` passes, including two new test cases
  in `sidebar_test.go`.
- `go vet ./...` clean.
- Manual: rendering the sidebar with one image artifact shows the
  heading, one line, and matches the existing Catppuccin Mocha
  palette already used by Memory/MCP sections.
