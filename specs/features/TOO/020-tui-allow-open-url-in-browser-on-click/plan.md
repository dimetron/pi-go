# Plan: TUI click-to-open URL in browser

Feature: hovering an http(s) URL in the TUI chat message viewport underlines it; a left click without drag on it opens it via `internal/browser.Open`. Errors silently ignored. Scope: chat output only.

Read `design.md` for full context before starting. All work in `/Users/dimetron/p6s/pi-dev/pi-go`.

- [ ] Slice 1: Frame link resolution (`internal/tui/framelinks.go` + tests)
  - Create `internal/tui/framelinks.go`:
    - `type LinkSpan struct { URL string; Start, End int }` — display columns, half-open.
    - `func frameLinks(line string) []LinkSpan` — parse OSC 8 hyperlink sequences (form `\x1b]8;params;URI\x07text\x1b]8;;\x07`, and ST (`\x1b\\`) terminators) out of one frame line. Compute Start/End as display columns of the inner text using existing conventions in chat.go's `hyperlinkRenderedLine` (see how it converts byte offsets to columns with `lipgloss.Width`/`ansi.Cut`). Return nil when no links.
    - `func frameLinkAt(frame string, row, col int) *LinkSpan` — split frame on `\n`, index row (return nil if out of range), strip nothing; use `frameLinks`; match col within `[Start, End)`; nil otherwise.
  - Create `internal/tui/framelinks_test.go`: table-driven tests covering: BEL-terminated and ST-terminated OSC 8; plain line → nil; multiple links per line; styled inner text (SGR bytes inside link text); hit inside/outside span; out-of-range row/col; multi-line frame. Model test fixtures on real output from `hyperlinkRenderedURLs` in chat.go (see `terminal_hyperlink_test.go`).
  - Verify: `go build ./... && go test ./internal/tui/ -run 'FrameLink' -v`

- [ ] Slice 2: Hover underline rendering + motion wiring
  - In framelinks.go add `func applyHoverUnderline(frame string, row, startCol, endCol int) string` — returns frame with SGR underline (`\x1b[4m` … `\x1b[24m`) inserted around exactly those display columns of that line; must not disturb other escape bytes; no-op if row/cols out of range or span already underlined by style bytes (check for existing SGR at boundaries; simplest safe rule: only apply when the target cells carry no SGR styling of their own — document this limitation).
  - Add `applyHoverUnderline` tests to framelinks_test.go (exact span coverage, adjacent escapes preserved, out-of-range no-op).
  - In tui.go model struct add fields: `hoverX, hoverY int` (frame coords), `hoverLink string`.
  - Extend `handleMouseMotion` (tui.go:980): after existing drag logic, map msg coords via `clampToChat` to `(x, y)`; resolve `frameLinkAt(m.lastFrame, y, x)`; set/clear `hoverX/hoverY/hoverLink` accordingly. Motion during active drag should clear hoverLink (drag = selecting, not hovering).
  - In View (around where lastFrame is saved, tui.go:1597): if `hoverLink != ""` re-resolve its span at `(hoverX, hoverY)`; if found, apply `applyHoverUnderline` to the composed message rows before saving lastFrame ordering per current code (apply after composition, before assignment — keep `frameRows` consistent since underline adds no lines). If re-resolution fails, clear hoverLink.
  - Verify: `go build ./... && go test ./internal/tui/ -v`

- [ ] Slice 3: Click-to-open on release
  - In tui.go add injectable hook near login.go's pattern: `var openChatLink = browser.Open` (import `internal/browser`).
  - Add model field `pressLink string`.
  - In `handleMouseClick` (tui.go:921): for left-button presses inside the message viewport, before existing selection-start logic, record `m.pressLink = frameLinkAt(m.lastFrame, y, x)` result (nil if none). Do NOT alter existing selection behavior.
  - In `handleMouseRelease` (tui.go:995): capture press-link first; after the existing empty-selection reset branch (the "click without drag" path), if selection ended empty AND release cell resolves via `frameLinkAt` to a LinkSpan whose URL equals `pressLink.URL`, call `_ = openChatLink(url)`. Clear `pressLink` and `hoverLink` on any release. Drags produce non-empty selection → suppressed automatically.
  - Tests (new file or extend existing mouse handler tests): craft a model with a lastFrame containing an OSC 8 link (use `hyperlinkRenderedURLs` to build it); stub `openChatLink` with a recorder; cases:
    1. press+release same cell on link, no motion → called once with URL;
    2. press on link, motion to another cell, release → not called, selection non-empty;
    3. press+release on non-link cell → not called;
    4. stub returns error → no panic, no visible state change;
    5. press on link A, release over link B (different URL) → not called.
  - Verify: `go build ./... && go test ./internal/tui/... -v`, then full gate `go test ./...`

## Gates

- build: `make build`
- test: `go test ./...`
- vet/lint: `make vet lint`

## Constraints

- Do not modify chat.go rendering pipeline or selection.go semantics.
- No new dependencies; reuse `charmbracelet/x/ansi`, `lipgloss`, `internal/browser`.
- Keep all changes within `internal/tui/`.
