# nanocoder-tui — Implementation Plan

## Checklist

- [ ] Step 1: Theme system (ThemeManager + embedded themes.json + /theme command)
- [ ] Step 2: Responsive layout utility
- [ ] Step 3: Extract InputModel from tui.go
- [ ] Step 4: Extract ChatModel from tui.go
- [ ] Step 5: Extract StatusModel with context percentage and mode indicator
- [ ] Step 6: Extract ToolDisplayModel with formatter previews
- [ ] Step 7: Compose root model from sub-models
- [ ] Step 8: File @mentions in InputModel
- [ ] Step 9: Bash streaming progress
- [ ] Step 10: Compact/expanded tool toggle
- [ ] Step 11: Full-state prompt history

---

## Step 1: Theme System

**Objective:** Create ThemeManager with 39 embedded themes and `/theme` command.

**Implementation guidance:**
- Copy nanocoder's `themes.json` to `internal/tui/themes.json`
- Create `internal/tui/theme.go` with `ThemeManager` struct, `//go:embed themes.json`
- Define `Theme` and `ThemeColors` structs with JSON tags
- Add a hardcoded "pi-classic" theme matching current colors (blue 39, green 35, etc.) as fallback
- `NewThemeManager()` loads JSON, sets default to "tokyo-night"
- `SetTheme(name)` validates name, updates current
- `Colors()` returns current `ThemeColors` with `lipgloss.Color` values
- Add `/theme` slash command: no args lists themes, with arg switches theme
- Persist choice in `~/.pi-go/config.json` under `"theme"` key
- Wire `ThemeManager` into `Config` struct, pass pointer to model

**Test requirements:**
- `TestThemeManagerLoad` — embedded JSON parses, 39 themes available
- `TestThemeManagerSetTheme` — valid name works, invalid returns error
- `TestThemeManagerFallback` — if JSON corrupt, pi-classic fallback loads
- `TestThemeColors` — colors are valid lipgloss.Color values
- `TestThemeCommand` — `/theme dracula` changes theme, `/theme` lists all

**Integration notes:**
- Do NOT replace hardcoded colors in tui.go yet — that happens in Steps 3-6
- ThemeManager is created in `Run()` and passed into model
- Existing rendering continues to work unchanged

**Demo:** Run pi-go, type `/theme` to see 39 themes, type `/theme synthwave-84` to switch. Status bar and input still use old colors (not yet wired).

---

## Step 2: Responsive Layout Utility

**Objective:** Create layout helper that calculates breakpoints and provides path truncation.

**Implementation guidance:**
- Create `internal/tui/layout.go` with `Layout` struct and `Breakpoint` enum
- `NewLayout(w, h)` computes breakpoint from width: narrow < 60, normal 60-100, wide > 100
- `TruncatePath(path, max)` — ellipsis in middle: `src/...ng/file.go`
- `MaxPathLen()` — 30 narrow, 50 normal, 80 wide
- `StatusWidth()`, `ChatWidth()` — return `Width` (full width for now)
- Update model to store `Layout`, recalculate on `tea.WindowSizeMsg`

**Test requirements:**
- `TestBreakpoints` — verify thresholds at 59, 60, 100, 101
- `TestTruncatePath` — short paths unchanged, long paths truncated with ellipsis
- `TestMaxPathLen` — correct values per breakpoint

**Integration notes:**
- Layout stored in root model, passed to sub-models as they're extracted
- No visual changes yet — just infrastructure

**Demo:** Internal only — Layout struct created, tests pass.

---

## Step 3: Extract InputModel

**Objective:** Move input handling, cursor, completion, and history out of tui.go into `input.go`.

**Implementation guidance:**
- Create `internal/tui/input.go` with `InputModel` struct
- Move fields from model: `input`, `cursorPos`, `history`, `historyIdx`, `completion`, `completionResult`, `completionMode`, `selectedIndex`, `cyclingIdx`
- Move methods: key handling for input editing (backspace, delete, left, right, home, end, ctrl+a/e), typing, paste insertion
- Move completion logic from `handleKey()` (tab, shift+tab, enter in completion mode)
- Move history navigation (up/down arrows when not in completion)
- Move `renderInput()` → `InputModel.View()`
- Replace all hardcoded input colors with `m.theme.Colors()`
- `InputModel.Update()` handles `tea.KeyPressMsg` for input-related keys
- `InputModel.Value()` returns current text
- Root model calls `m.input.Update(msg)` and reads `m.input.Value()` on submit

**Test requirements:**
- `TestInputTyping` — characters insert at cursor position
- `TestInputCursorNavigation` — left, right, home, end, ctrl+a/e
- `TestInputBackspace` — delete at cursor
- `TestInputCompletion` — tab triggers, enter applies
- `TestInputHistory` — up/down cycles through history
- `TestInputView` — renders with theme colors

**Integration notes:**
- Root model's `handleKey()` delegates input keys to `InputModel.Update()`
- Root model still handles submit (Enter when not in completion), slash commands, escape
- `renderInput()` removed from tui.go, replaced with `m.input.View()`

**Demo:** Input field works identically to before but uses theme colors. Completion and history work.

---

## Step 4: Extract ChatModel

**Objective:** Move message list, scrolling, streaming accumulation, and message rendering into `chat.go`.

**Implementation guidance:**
- Create `internal/tui/chat.go` with `ChatModel` struct
- Move fields: `messages`, `scroll`, `renderer`, `streaming`, `thinking`
- Move methods: `renderMessages()` → `ChatModel.View()`
- Move `renderMarkdown()` into ChatModel
- Move syntax highlighting helpers: `highlightReadOutput()`, `highlightGrepOutput()`, `highlightFindOutput()`, `highlightCode()`
- Replace all hardcoded colors with `m.theme.Colors()`
- `ChatModel.Update()` handles: `agentTextMsg` (accumulate streaming), `agentThinkingMsg` (accumulate thinking), scroll keys (if we add them)
- `AddMessage(msg)` — append to messages list
- `UpdateStreaming(text)` — update last assistant message
- `Clear()` — reset for `/clear` command
- Glamour renderer style: use `glamour.WithAutoStyle()` but override based on `theme.IsDark()` → dark style or light style

**Test requirements:**
- `TestChatAddMessage` — messages append correctly
- `TestChatStreaming` — streaming text accumulates, replaces last message
- `TestChatThinking` — thinking accumulates, shows last 6 lines
- `TestChatClear` — clears all messages
- `TestChatRenderUserMessage` — user message rendered with theme.Primary
- `TestChatRenderToolMessage` — tool message rendered with theme.Tool
- `TestHighlightReadOutput` — syntax highlighting works with theme

**Integration notes:**
- Root model routes `agentTextMsg`/`agentThinkingMsg` to ChatModel
- Root model calls `m.chat.AddMessage()` when creating user/tool/assistant messages
- `renderMessages()` removed from tui.go

**Demo:** Conversation renders identically but with theme colors. Markdown and code highlighting still work.

---

## Step 5: Extract StatusModel with Context % and Mode Indicator

**Objective:** Move status bar rendering into `status.go`, add context percentage display and mode indicator.

**Implementation guidance:**
- Create `internal/tui/status.go` with `StatusModel` struct
- Move fields: `gitBranch`, tool tracking (`activeTool`, `activeTools`, `toolStart`), trace count
- Move `renderStatusBar()` → `StatusModel.View()`
- Replace all hardcoded status colors with theme colors
- **New: Context percentage**
  - Add `contextPercent`, `contextUsed`, `contextLimit` fields
  - `SetContextUsage(used, limit)` calculates percentage
  - Render: `ctx: 45% [████░░░░]` using block chars, colored by threshold
  - Color: `theme.Success` < 60%, `theme.Warning` 60-80%, `theme.Error` > 80%
  - Narrow: just `ctx: 45%` without bar
- **New: Mode indicator**
  - Add `mode` field ("chat" or "plan")
  - `SetMode(mode)` updates display
  - Render: `[chat]` in `theme.Primary` or `[plan]` in `theme.Warning`
  - Placed at start of status bar
- Feed context data from `TokenTracker` (already in Config)

**Test requirements:**
- `TestStatusView` — renders provider, model, git branch with theme colors
- `TestContextPercentage` — correct percentage calculation and color thresholds
- `TestContextBar` — bar rendering at various percentages
- `TestModeIndicator` — [chat] and [plan] render with correct colors
- `TestStatusResponsive` — narrow terminal truncates paths, hides bar

**Integration notes:**
- Root model updates `StatusModel` fields when agent starts/stops, tools execute, mode changes
- `/plan` sets mode to "plan", completion resets to "chat"
- Context updated on each `agentTextMsg` or `agentToolResultMsg`

**Demo:** Status bar shows `[chat] anthropic | claude-4 | ctx: 23% [████░░░░░░] | ⌂ main | tool: read (0.3s)` in theme colors.

---

## Step 6: Extract ToolDisplayModel with Formatter Previews

**Objective:** Move tool rendering into `tool_display.go`, add rich formatter previews.

**Implementation guidance:**
- Create `internal/tui/tool_display.go` with `ToolDisplayModel` struct
- Move: `toolCallSummary()`, `toolResultSummary()` → formatters
- Create `ToolFormatter` function type: `func(name string, args map[string]any, result string, colors ThemeColors) string`
- Register built-in formatters:
  - `read`: syntax-highlighted code with line numbers (move `highlightReadOutput` here)
  - `write`: `✓ path (N bytes)` in success color
  - `edit`: diff view with `theme.DiffAdded`/`theme.DiffRemoved` backgrounds and text colors
  - `bash`: command + exit code + token estimate
  - `grep`: highlighted matches (move `highlightGrepOutput` here)
  - `find`/`ls`/`tree`: colored paths (move `highlightFindOutput` here)
  - `agent`: type + title + event stream (move subagent rendering here)
- `FormatToolCall(name, args)` → one-line summary (existing `toolCallSummary` logic)
- `FormatToolResult(name, result)` → rich formatted output
- ChatModel calls ToolDisplayModel for tool message rendering

**Test requirements:**
- `TestFormatRead` — syntax highlighting applied, line numbers present
- `TestFormatEdit` — diff colors applied for add/remove lines
- `TestFormatBash` — shows command, exit code, token estimate
- `TestFormatGrep` — file:line matches highlighted
- `TestFormatWrite` — success message with path and size
- `TestFormatAgent` — agent type, title, event stream rendered

**Integration notes:**
- ChatModel delegates tool rendering: `m.tools.FormatToolResult(name, result)`
- Highlight functions move from chat.go to tool_display.go
- All formatters use theme colors

**Demo:** Tool results render with rich formatting — edit shows green/red diff, bash shows exit code dot, read shows highlighted code. All in theme colors.

---

## Step 7: Compose Root Model from Sub-Models

**Objective:** Rewrite tui.go as a thin router composing all sub-models.

**Implementation guidance:**
- Strip tui.go down to:
  - `model` struct with sub-models + flow states + agent state
  - `Run()` — creates ThemeManager, sub-models, tea.Program
  - `Init()` — delegates to sub-models
  - `Update()` — routes messages to appropriate sub-model
  - `View()` — composes sub-model views + viewport clipping
  - `submit()` — reads InputModel value, creates agent loop
  - `handleSlashCommand()` — dispatch (keep in root, delegates rendering)
  - Flow handlers (commit, login, plan, run — remain as they are)
- Message routing table:
  - `tea.WindowSizeMsg` → all sub-models
  - `tea.KeyPressMsg` → active flow OR InputModel
  - `agentTextMsg`/`agentThinkingMsg` → ChatModel
  - `agentToolCallMsg`/`agentToolResultMsg` → ChatModel + StatusModel
  - `agentDoneMsg` → root (clear state) + StatusModel
  - Flow msgs → respective flow
- Verify tui.go is < 500 lines

**Test requirements:**
- `TestRootModelUpdate` — messages route to correct sub-model
- `TestRootModelView` — composed view contains all sub-model output
- `TestSubmitFlow` — input → submit → agent loop starts
- All existing TUI tests still pass
- teatest integration: full conversation flow works end-to-end

**Integration notes:**
- This is the critical step — must not break any functionality
- Run full test suite after each method migration
- Keep flow states in root model (they interact with multiple sub-models)

**Demo:** pi-go works exactly as before, all 39 themes active, all colors themed, tui.go is < 500 lines. Every slash command works.

---

## Step 8: File @Mentions in InputModel

**Objective:** Add @file autocomplete to InputModel.

**Implementation guidance:**
- Detect `@` character in input followed by non-space chars
- On `@` detection: enter mention mode, start fuzzy file search
- File search: `filepath.Glob` on working directory with typed prefix
- Show up to 5 candidates below input with arrow navigation
- Tab selects candidate, inserts full relative path: `@src/auth/login.go`
- Space or Enter exits mention mode
- Track mentions in `InputState.Mentions` with path and position
- On submit: convert `@path` references to `[Referenced file: path]` in prompt
- `MentionPaths()` returns list of referenced paths
- `InputState` with mentions stored in history for full restoration

**Test requirements:**
- `TestMentionDetection` — `@` triggers mention mode
- `TestMentionFuzzySearch` — partial paths match files
- `TestMentionSelect` — Tab inserts selected path
- `TestMentionInPrompt` — submitted prompt contains `[Referenced file: ...]`
- `TestMentionHistory` — up arrow restores mentions
- `TestMentionView` — candidates render below input with theme colors

**Integration notes:**
- InputModel handles all mention logic internally
- Root model reads `m.input.MentionPaths()` when building agent prompt
- Agent sees file references as hints, decides whether to read

**Demo:** Type `@tui` in input, see `tui.go`, `tui_test.go` etc. as candidates. Tab selects. Submit sends prompt with file reference. Up arrow restores exact state.

---

## Step 9: Bash Streaming Progress

**Objective:** Show live bash output during execution.

**Implementation guidance:**
- Add `BashStream` struct to ToolDisplayModel with command, output buffer, state
- New message type: `bashOutputMsg{id, chunk}` sent from agent goroutine during bash execution
- Agent's bash tool handler sends incremental output chunks via channel
- `ToolDisplayModel.StartBashStream(id, command)` — initialize stream
- `ToolDisplayModel.UpdateBashStream(id, output)` — append output, keep last N lines as preview
- `ToolDisplayModel.CompleteBashStream(id, exitCode)` — mark complete
- `BashStreamView(id)` renders:
  - Running: `⚒ bash: command` + last 5 lines of output + spinner
  - Complete: `⚒ bash: command` + `● exit N (~M tokens)` (green/red dot)
- Token estimate: `len(output) / 4`
- Escape key sends cancel signal to running bash

**Test requirements:**
- `TestBashStreamStart` — stream created with command
- `TestBashStreamUpdate` — output appends, preview shows last 5 lines
- `TestBashStreamComplete` — exit code displayed, token count calculated
- `TestBashStreamView` — running shows spinner, complete shows dot

**Integration notes:**
- Requires modification to agent's bash tool to send incremental output
- If agent doesn't support streaming, fall back to current behavior (result after completion)
- ChatModel shows BashStreamView for active bash tools

**Demo:** Run a prompt that triggers `npm test` or `go test`. See live output scrolling. Completion shows green dot and token count.

---

## Step 10: Compact/Expanded Tool Toggle

**Objective:** Ctrl+O toggles between full tool output and running tally.

**Implementation guidance:**
- `ToolDisplayModel.compact` bool field
- `ToggleCompact()` — flip compact flag
- When compact:
  - `compactCounts` map increments on each tool result
  - `CompactView()` renders: `tools: read(3) edit(2) bash(1)`
  - ChatModel skips full tool rendering, shows compact line instead
- When expanding (compact → expanded):
  - Flush accumulated counts as a summary message in chat
  - Reset counts
- Root model catches `ctrl+o` key, calls `m.tools.ToggleCompact()`
- Status bar shows compact indicator: `[compact]` or nothing

**Test requirements:**
- `TestCompactToggle` — toggle flips state
- `TestCompactCounts` — counts increment per tool
- `TestCompactView` — renders formatted tally
- `TestCompactExpand` — expanding flushes counts

**Integration notes:**
- ChatModel checks `m.tools.IsCompact()` when rendering tool messages
- If compact, skip tool rendering and increment count instead
- Ctrl+O handled in root model Update before delegating to InputModel

**Demo:** Start a session, run a complex prompt. Press Ctrl+O — tool output collapses to `tools: read(5) grep(3)`. Press Ctrl+O again — summary appears, full output resumes.

---

## Step 11: Full-State Prompt History

**Objective:** Upgrade history from plain text to structured InputState with @mentions.

**Implementation guidance:**
- Create `internal/tui/history_v2.go` (keep `history.go` for migration)
- `HistoryEntry` struct: `Text string`, `Mentions []FileMention`
- `SaveHistory(entries []InputState)` → write `~/.pi-go/history.json`
- `LoadHistory() []InputState` → read JSON, fallback to plain text migration
- Migration: if `history.json` doesn't exist but `history` does, read plain text, convert to InputState (no mentions), write JSON, log migration
- Max 1000 entries (same as current)
- `InputModel` uses `[]InputState` for history instead of `[]string`
- Up/Down arrows restore full `InputState` including `Mentions`
- On submit: append current `InputState` to history, persist

**Test requirements:**
- `TestHistorySaveLoad` — round-trip JSON with mentions
- `TestHistoryMigration` — plain text → JSON conversion
- `TestHistoryMaxEntries` — oldest dropped when > 1000
- `TestHistoryWithMentions` — mentions preserved through save/load
- `TestHistoryCycling` — up/down restores full state

**Integration notes:**
- Replace `loadHistory()`/`appendHistory()` calls with new v2 functions
- InputModel uses new history format internally
- Old `history.go` can be removed after migration is verified

**Demo:** Type `fix bug in @src/auth.go`, submit. Later, press Up arrow — see exact input with `@src/auth.go` mention restored and highlighted.
