# Research: Mouse handling & coordinate spaces (tui.go, selection.go)

## Dispatcher

`handleMouse(msg tea.MouseMsg)` (tui.go:787), reached from Update at tui.go:670-671. Dispatches to:
- `handleMouseClick(msg tea.MouseClickMsg)` — tui.go:921
- `handleMouseMotion(msg tea.MouseMotionMsg)` — tui.go:980
- `handleMouseRelease(msg tea.MouseReleaseMsg)` — tui.go:995
- `handleMouseWheel(msg tea.MouseWheelMsg)` — tui.go:1049

All use `msg.Mouse()` → bubbletea v2 `tea.Mouse{X, Y int; Button MouseButton; Mod KeyMod}`. **X/Y are terminal cell coordinates.**

## Coordinate mapping

Y is absolute terminal row (no alt-screen). `clampToChat` (tui.go:1023-1036): frame row = `y - m.frameTop()` where `frameTop() = max(0, m.height - m.frameRows)` (tui.go:1039-1046); clamps to `[msgTop, msgBottom-1]`. X clamped to `[0, chatWidth()-1]`.

## Drag selection (`selection` type, selection.go:19-29)

Fields: `dragging bool`, `anchorX/anchorY`, `cursorX/cursorY`, `present bool` (set only by Motion).

Flow:
- Click (tui.go:921): non-left ignored; hitContextClear check; click right of panel deselects; click inside existing highlight re-copies (tui.go:945-947); otherwise starts `selection{dragging: true, anchor=cursor=(x,y)}`.
- Motion: updates cursor, sets `present = true`.
- Release (tui.go:995): sets `dragging = false`; if `m.sel.empty()` (selection.go:71-73 — `!present || anchor == cursor`) resets and does nothing; else copies via `copyAndFlash` (OSC 52 + clipboard).

**"Click without drag" notion already exists**: `selection.empty()`.

## Relevant model state

- `sel selection` (tui.go:85), `lastFrame string` (:86, saved in View :1597), `frameRows int` (:87).
- `msgTop, msgBottom int` (:103) — half-open selectable rows, set in View at :1600.
- `chatModel.ChatModel` — custom model: `Scroll int // offset from bottom` (chat.go:267), `Width int`; `ScrollUp/ScrollDown/MaxScroll(height)`.
- Dimensions: `m.width/m.height`, `chatWidth()`, `messageViewportHeight()` (tui.go:1894).

## MouseMode

tui.go:1634: `v.MouseMode = tea.MouseModeCellMotion`, `v.AltScreen = false` (:1633). Asserted in history_key_test.go:201-202.

## Browser helper

`internal/browser/browser.go`: `browser.Open(url string) error` returning `ErrNoHandler`. Tries `$BROWSER`, then platform commands. Already used by login flows.
