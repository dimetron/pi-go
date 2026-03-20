# Nanocoder TUI — Refactor & Enhance pi-go's Terminal Interface

## Objective

Refactor pi-go's monolithic TUI (`internal/tui/tui.go`, ~2600 lines) into composable Bubble Tea v2 sub-models and add 9 features adapted from nanocoder's TUI design. The root `tui.go` must shrink to < 500 lines (thin message router).

## Key Requirements

1. **ThemeManager** (`internal/tui/theme.go`) — Embed nanocoder's 39-theme JSON via `//go:embed`, expose `ThemeColors` with 13 color roles (text, base, primary, tool, success, error, secondary, info, warning, diffAdded, diffRemoved, diffAddedText, diffRemovedText). Add `/theme` command. Default: tokyo-night. Persist in `~/.pi-go/config.json`.

2. **ResponsiveLayout** (`internal/tui/layout.go`) — Breakpoints (narrow < 60, normal 60-100, wide > 100). Truncate paths on narrow terminals.

3. **InputModel** (`internal/tui/input.go`) — Extract input, cursor, completion, history from model. Add `@file` mention autocomplete (fuzzy glob, Tab select, path-only reference sent to agent as `[Referenced file: path]`). Full InputState history (text + mentions).

4. **ChatModel** (`internal/tui/chat.go`) — Extract messages, scroll, streaming, markdown rendering. Use theme colors. Delegate tool rendering to ToolDisplayModel.

5. **StatusModel** (`internal/tui/status.go`) — Extract status bar. Add context window % bar (green < 60%, orange 60-80%, red > 80%). Add mode indicator (`[chat]`/`[plan]`).

6. **ToolDisplayModel** (`internal/tui/tool_display.go`) — Per-tool formatters (diff view for edits using diffAdded/diffRemoved colors, syntax highlight for reads, exit code + tokens for bash). Compact toggle (Ctrl+O) switches between full output and running tally. Bash streaming with live output preview.

7. **Root model** (`internal/tui/tui.go`) — Thin router composing sub-models. Routes tea.Msg to appropriate sub-model. < 500 lines.

8. **History upgrade** (`internal/tui/history_v2.go`) — JSON format preserving InputState with @mentions. Migrate from plain text on first run.

## Acceptance Criteria (Given-When-Then)

- Given pi-go starts, when no theme configured, then tokyo-night is default and all colors use theme roles
- Given `/theme dracula`, when theme exists, then all UI colors change immediately and persist across restarts
- Given terminal width < 60, when rendering, then paths truncated and labels abbreviated
- Given user types `@src/`, when in input, then fuzzy file completions shown; Tab selects; prompt includes `[Referenced file: path]`
- Given agent running, when tokens accumulate, then status bar shows color-coded context % bar
- Given `edit` tool result, when rendering, then diff shown with green/red theme diff colors
- Given Ctrl+O pressed, when in expanded mode, then tool output collapses to running tally; press again to expand
- Given bash tool executing, when output arrives, then live preview shown; Escape cancels; completion shows exit code + token estimate
- Given `/plan` active, when rendering, then status bar shows `[plan]` in warning color
- Given input with @mentions submitted, when pressing Up arrow later, then full InputState restored
- Given all changes applied, when running test suite, then all existing tests pass
- Given refactored code, when counting tui.go lines, then < 500

## Reference

Full specs at `specs/nanocoder-tui/` — design.md for architecture details, plan.md for 11-step implementation order, research/ for nanocoder analysis and Bubble Tea patterns.
