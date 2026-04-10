# nanocoder-tui — Summary

## Artifacts

| File | Description |
|------|-------------|
| `specs/nanocoder-tui/rough-idea.md` | Initial concept and key areas of interest |
| `specs/nanocoder-tui/requirements.md` | 10 Q&A pairs covering all design decisions |
| `specs/nanocoder-tui/research/nanocoder-architecture.md` | Full nanocoder TUI architecture analysis |
| `specs/nanocoder-tui/research/nanocoder-theme-format.md` | 39-theme JSON format, color roles, Go integration plan |
| `specs/nanocoder-tui/research/pi-go-tui-current-state.md` | pi-go TUI structural map and refactoring opportunities |
| `specs/nanocoder-tui/research/bubbletea-component-patterns.md` | React→Bubble Tea translation patterns |
| `specs/nanocoder-tui/design.md` | Detailed design: architecture, components, interfaces, acceptance criteria |
| `specs/nanocoder-tui/plan.md` | 11-step incremental implementation plan with TDD |

## Overview

Refactor pi-go's monolithic TUI (~2600 lines) into 5 composable Bubble Tea sub-models (InputModel, ChatModel, StatusModel, ToolDisplayModel + ThemeManager) and add 9 features adapted from nanocoder's TypeScript/Ink TUI:

1. **39-theme system** — embedded JSON, `/theme` command, runtime switching
2. **Responsive layout** — path truncation on narrow terminals
3. **Tool formatter previews** — diff view for edits, syntax highlighting for reads
4. **File @mentions** — fuzzy autocomplete, path reference for agent
5. **Context window %** — always-visible bar with color thresholds
6. **Bash streaming** — live output, cancel, token count
7. **Compact tool toggle** — Ctrl+O switches between full output and running tally
8. **Mode indicator** — [chat] / [plan] visual in status bar
9. **Full-state history** — JSON history preserving @mentions

## Next Steps

- **Implement via Ralph:** `ralph run --config presets/spec-driven.yml`
- **Or step-by-step:** Follow the 11 steps in `plan.md`, each producing working functionality
- **Theme JSON:** Copy from `/tmp/nanocoder-themes.json` (saved during research) to `internal/tui/themes.json`
