# Bubble Tea v2 Component Patterns

## Challenge: React Components → Bubble Tea Models

Nanocoder uses React components (Ink) with hooks for state. Bubble Tea uses the Elm architecture (Model → Update → View). The translation isn't 1:1 but follows clear patterns.

## Pattern: Composable Sub-Models

Each "component" becomes a sub-model with its own `Update()` and `View()` methods:

```go
// Sub-model interface
type Component interface {
    Update(msg tea.Msg) (Component, tea.Cmd)
    View() string
}

// Root model composes sub-models
type model struct {
    input   InputModel
    chat    ChatModel
    status  StatusModel
    theme   *ThemeManager
    // ... flow states
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    // Route messages to sub-models
    m.input, cmd = m.input.Update(msg)
    cmds = append(cmds, cmd)

    m.chat, cmd = m.chat.Update(msg)
    cmds = append(cmds, cmd)

    // ... etc
    return m, tea.Batch(cmds...)
}

func (m *model) View() tea.View {
    v := tea.NewView()
    v.SetContent(
        m.chat.View() + "\n" +
        m.status.View() + "\n" +
        m.input.View(),
    )
    return v
}
```

## Proposed Sub-Model Breakdown

### 1. ThemeManager (not a tea.Model — shared state)
```go
type ThemeManager struct {
    themes  map[string]Theme    // loaded from embedded JSON
    current string              // current theme slug
}

func (t *ThemeManager) Colors() ThemeColors
func (t *ThemeManager) SetTheme(name string) error
func (t *ThemeManager) List() []Theme
```

Passed by pointer to all sub-models that need styling.

### 2. InputModel
Handles: text input, cursor, @mentions, completion, history

**Replaces:** `input`, `cursorPos`, `history`, `historyIdx`, `completion`, `completionResult`, `completionMode`, `selectedIndex`, `cyclingIdx` fields

**New features:**
- @file mention detection and autocomplete
- Full InputState preservation in history
- Ghost text completion

### 3. ChatModel
Handles: message list, scroll, markdown rendering

**Replaces:** `messages`, `scroll`, `renderer`, `streaming`, `thinking`

**New features:**
- Tool formatter previews (rendered summaries)
- Compact/expanded tool output toggle

### 4. StatusModel
Handles: status bar rendering, context %, mode indicator

**Replaces:** status bar section of `renderStatusBar()`

**New features:**
- Context window percentage (always visible)
- Mode indicator (chat/plan)
- Responsive truncation on narrow terminals

### 5. ToolDisplayModel
Handles: tool call/result formatting and display

**Replaces:** `toolCallSummary()`, `toolResultSummary()`, highlight functions

**New features:**
- Formatter preview system (per-tool renderers)
- Compact/expanded toggle (Ctrl+O)
- Bash streaming progress

### 6. Flow Models (existing, minimal changes)
- CommitFlow, LoginFlow, PlanFlow, RunFlow
- Already semi-separated, just need cleaner interfaces

## Message Routing Strategy

```
tea.Msg arrives
  ├── tea.WindowSizeMsg → all models (update width/height)
  ├── tea.KeyPressMsg
  │   ├── Flow-specific keys → active flow model
  │   ├── Ctrl+O → ToolDisplayModel (toggle compact)
  │   ├── Shift+Tab → StatusModel (cycle mode) [if we add this]
  │   └── Default → InputModel
  ├── agentMsg variants → ChatModel + ToolDisplayModel
  └── flow-specific msgs → respective flow model
```

## Responsive Layout in Bubble Tea

```go
type LayoutBreakpoint int

const (
    LayoutNarrow LayoutBreakpoint = iota  // < 60
    LayoutNormal                           // 60-100
    LayoutWide                             // > 100
)

func (m *model) breakpoint() LayoutBreakpoint {
    switch {
    case m.width < 60:
        return LayoutNarrow
    case m.width > 100:
        return LayoutWide
    default:
        return LayoutNormal
    }
}
```

Used in View() methods to truncate paths, abbreviate labels, etc.

## Key Differences from Nanocoder's React Approach

| Concept | Nanocoder (React/Ink) | pi-go (Bubble Tea) |
|---------|----------------------|-------------------|
| State | useState hooks | Model struct fields |
| Side effects | useEffect | tea.Cmd functions |
| Context | React.Context | Pointer passing |
| Conditional render | JSX ternary | String concatenation in View() |
| Events | useInput callback | Update() pattern matching |
| Composition | Component nesting | Sub-model delegation |
| Static content | `<Static>` component | Viewport management |
| Memoization | useMemo | Manual caching if needed |
