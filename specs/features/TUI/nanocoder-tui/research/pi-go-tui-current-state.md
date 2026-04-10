# pi-go TUI Current State Analysis

## Architecture Overview

pi-go's TUI is a **monolithic Bubble Tea v2 application** in `internal/tui/tui.go` (~2600 lines) with supporting files for specific flows.

### File Map
| File | Lines | Purpose |
|------|-------|---------|
| `tui.go` | ~2600 | Core model, Update(), View(), all rendering |
| `completion.go` | ~200 | Slash command / spec completion engine |
| `commit.go` | ~280 | `/commit` flow (git staging, LLM msg gen) |
| `login.go` | ~300 | `/login` flow (OAuth, device code, manual key) |
| `plan.go` | ~270 | `/plan` flow (PDD session) |
| `run.go` | ~400 | `/run` flow (spec execution with gating) |
| `create_skill.go` | ~200 | `/skill-create` flow |
| `history.go` | ~113 | Persistent command history (~/.pi-go/history) |
| `ping.go` | ~100 | `/ping` LLM connectivity test |
| `restart.go` | ~20 | Process re-exec |
| `drain_unix.go` | ~50 | Terminal response cleanup |

### Model Struct — Key Field Groups

**UI state:** `width`, `height`, `scroll`
**Input:** `input`, `cursorPos`, `history`, `historyIdx`, `completion`, `completionResult`, `completionMode`, `selectedIndex`, `cyclingIdx`
**Messages:** `messages []message` (user/assistant/tool/thinking roles)
**Agent:** `running`, `streaming`, `thinking`, `activeTool`, `activeTools`, `agentCh`
**Flow states:** `commit *commitState`, `login *loginState`, `plan *planState`, `run *runState`, `pendingSkillCreate`
**Rendering:** `renderer *glamour.TermRenderer`
**Debug:** `traceLog []traceEntry`

### Current Styling (Hardcoded Colors)

| Color Code | Usage |
|-----------|-------|
| 39 (blue) | User input `>`, file paths, selection |
| 35 (green) | Tool bullets, tool names |
| 213 (purple) | Agent bullets, agent types |
| 214 (orange) | Active tools, warnings |
| 196 (red) | Errors, token limit |
| 240-252 (grays) | Separators, hints, dim text |
| 236 bg | Status bar background |

**Problem:** All colors are hardcoded as lipgloss.Color("NN") inline — no theme abstraction.

### Rendering Pipeline

```
View()
├── renderMessages() — iterates m.messages
│   ├── User: ">" prefix + plain text
│   ├── Assistant: glamour markdown rendering
│   ├── Tool: bullet + name + highlighted output
│   │   ├── highlightReadOutput() — line numbers + syntax
│   │   ├── highlightGrepOutput() — file:line: + syntax
│   │   ├── highlightFindOutput() — path coloring
│   │   └── highlightCode() — chroma lexer + monokai
│   ├── Thinking: 💭 + italic + last 6 lines
│   └── Agent/Subagent: type + title + event stream
├── renderStatusBar() — provider, model, tokens, git, tools, agents
└── renderInput() — prompt + cursor + ghost + completion menu
```

### What's Missing (vs nanocoder)

1. **No theme system** — all colors hardcoded
2. **No responsive layout** — fixed rendering, no width adaptation
3. **No tool previews/formatters** — tools show raw result summaries
4. **No @file mentions** — input is plain text only
5. **No context % display** — rough token estimate only in status bar
6. **No compact/expanded toggle** — tool output always shown
7. **No mode indicator** — no visual for plan vs chat mode
8. **No full-state history** — text-only, no metadata preservation
9. **No bash streaming** — shows result after completion

### Refactoring Opportunities

The monolithic model can be decomposed into:

1. **InputModel** — input text, cursor, history, completion, @mentions
2. **ChatModel** — message list, scroll, rendering
3. **StatusModel** — provider, model, tokens, git, tools, mode indicator
4. **ThemeModel** — color definitions, current theme, responsive breakpoints
5. **ToolDisplayModel** — tool previews, compact/expanded toggle
6. **FlowModels** — commit, login, plan, run (already semi-separated)

Each sub-model would implement its own Update/View and be composed in the root model.
