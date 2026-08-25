# TUI: open URL in browser on click

## Objective
Add click-to-open-URL support to the pi-go TUI chat output. Hovering an http(s) URL underlines it; a left click without drag opens it in the system browser via `internal/browser.Open`. Errors are silently ignored; scope is chat output only.

## Key Requirements
1. **Frame link resolution** — parse OSC 8 hyperlink spans out of the composed frame string to resolve which URL (if any) sits under a given cell; derived on demand from `lastFrame`, no cached index.
2. **Hover underline** — mouse motion over a link sets hover state; View renders SGR underline over exactly that span.
3. **Click-to-open with drag suppression** — release opens the URL only when selection ended empty ("click without drag") and press/release resolve to the same URL.
4. **Silent failure** — `browser.Open` errors ignored (`_ = openChatLink(url)`); injectable var for tests.

## Acceptance Criteria
### Link resolution
- Given a frame line containing an OSC 8 hyperlink, when `frameLinks` runs, then it returns the URL with correct display-column Start/End (BEL and ST terminator forms, styled inner text, multiple links per line).
- Given out-of-range row/col or non-link cells, when `frameLinkAt` runs, then it returns nil.

### Hover
- Given chat output containing `https://example.com`, when the mouse moves onto it, then the next frame underlines that span only; moving off clears it.

### Click-to-open
- Given press and release on the same link cell without motion between, when handled, then `openChatLink` is called once with the URL.
- Given press on a link followed by motion/drag, then no open occurs and text selection works as before.
- Given click on non-link text, then `openChatLink` is never called.
- Given `openChatLink` returns an error, then nothing visible changes.

## Implementation Slices
1. **Frame link resolution** — pure functions + unit tests, files: `internal/tui/framelinks.go`, `internal/tui/framelinks_test.go`, verify: `go build ./... && go test ./internal/tui/ -run 'FrameLink' -v`, parallel-safe: yes
2. **Hover underline rendering** — `applyHoverUnderline`, model hover fields, motion handler + View wiring, files: `internal/tui/framelinks.go`, `internal/tui/framelinks_test.go`, `internal/tui/tui.go`, verify: `go test ./internal/tui/ -v`, parallel-safe: no
3. **Click-to-open on release** — `openChatLink` hook, press/release handling, handler tests, files: `internal/tui/tui.go`, verify: `go build ./... && go test ./internal/tui/... -v && go test ./...`, parallel-safe: no

## Execution Model
Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the **Coordinator**; it delegates rather than implements.

- **Workers**: one `worker` subagent per slice. Slices are sequential (2 and 3 share tui.go with each other's context); run one at a time in order.
- **Verifier**: after the last slice, a `code-reviewer` subagent checks the Done Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to 10 cycles total.

## Done Criteria
The Verifier checks these against the diff, not against the checklist:
- [ ] `internal/tui/framelinks.go` exports `frameLinks`, `frameLinkAt`, `applyHoverUnderline` with working OSC 8 parsing (both BEL and ST terminators), covered by tests in `framelinks_test.go`
- [ ] Motion over a link in the message viewport produces an underlined span in the composed frame (hover wiring present in handleMouseMotion and View)
- [ ] Release on a link after dragless click calls the injected browser opener exactly once; drags suppress opening; non-link clicks never call it — see handler tests
- [ ] `browser.Open` errors are silently discarded (no user-facing error path added)
- [ ] No slice is left as a stub, TODO, or panic("not implemented")
- [ ] `go build ./...` and `go test ./...` pass; existing mouse/selection behavior unchanged (selection.go unmodified)

## Gates
- **build**: `make build`
- **test**: `go test ./...`
- **vet/lint**: `make vet lint`

## Reference
- Design: `specs/features/TOO/020-tui-allow-open-url-in-browser-on-click/design.md`
- Outline: `specs/features/TOO/020-tui-allow-open-url-in-browser-on-click/outline.md`
- Plan: `specs/features/TOO/020-tui-allow-open-url-in-browser-on-click/plan.md`
- Requirements: `specs/features/TOO/020-tui-allow-open-url-in-browser-on-click/requirements.md`
- Research: `specs/features/TOO/020-tui-allow-open-url-in-browser-on-click/research/`

## Constraints
- Do not modify `chat.go` rendering pipeline or `selection.go` semantics
- No new dependencies; reuse `charmbracelet/x/ansi`, `lipgloss`, `internal/browser`
- Keep all source changes within `internal/tui/`
