# Outline: Click-to-open URL in TUI chat

## Slices (vertical, each independently verifiable)

1. **Slice 1: Frame link resolution** — `internal/tui/framelinks.go` with `LinkSpan`, `frameLinks(line)`, `frameLinkAt(frame, row, col)` + `framelinks_test.go` unit tests. Pure functions, no wiring. Depends on: none. Parallel-safe: yes.
2. **Slice 2: Hover underline rendering** — `applyHoverUnderline` in framelinks.go + tests; model fields `hoverX/hoverY/hoverLink`; motion handler sets/clears hover; View applies underline to message rows. Verify: underline tests + existing tui tests green. Depends on Slice 1. Parallel-safe: no.
3. **Slice 3: Click-to-open on release** — injectable `var openChatLink = browser.Open`; release handler opens when selection empty and press/release resolve to same link; click handler records press-point link; handler tests (open, drag-suppressed, error-silent). Verify: `go test ./internal/tui/...`. Depends on Slices 1–2 (hover state exists; but logic only needs Slice 1 — order after 2 for file-conflict avoidance). Parallel-safe: no.

## Key signatures

```go
// framelinks.go
type LinkSpan struct { URL string; Start, End int }
func frameLinks(line string) []LinkSpan
func frameLinkAt(frame string, row, col int) *LinkSpan
func applyHoverUnderline(frame string, row, startCol, endCol int) string

// tui.go
var openChatLink = browser.Open // injected for tests
// model: hoverX, hoverY int; hoverLink string
```

## Order & testing

Slices strictly sequential (1 → 2 → 3); each ends with `go build ./... && go test ./internal/tui/...`. Final gate: `go test ./...`.
