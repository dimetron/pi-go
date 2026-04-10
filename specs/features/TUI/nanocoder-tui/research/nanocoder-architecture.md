# Nanocoder TUI Architecture Analysis

**Source:** https://github.com/Nano-Collective/nanocoder

## Overview
Nanocoder is a local-first CLI coding agent built with TypeScript and Ink (React for terminals). It uses a component-based architecture with React hooks for state management.

## Tech Stack
- **Framework:** Ink (React for CLI) + TypeScript
- **Rendering:** React component tree with `ink`'s `Box`, `Text`, `Static` components
- **State:** React hooks (`useState`, `useCallback`, `useMemo`, custom hooks)
- **Styling:** Theme-based color system with 39 presets loaded from JSON
- **Markdown:** Custom markdown parser with syntax highlighting
- **Input:** Custom `TextInput` component with multiline support

## Component Architecture

### App Structure
```
App.tsx (root)
├── ThemeContext.Provider
│   ├── TitleShapeContext.Provider
│   │   ├── UIStateProvider
│   │   │   ├── ChatHistory (Static — never unmounted)
│   │   │   │   ├── WelcomeMessage
│   │   │   │   ├── Status
│   │   │   │   ├── AssistantMessage (markdown rendered)
│   │   │   │   ├── ToolMessage
│   │   │   │   └── BashProgress (live streaming)
│   │   │   ├── FileExplorer (conditional)
│   │   │   ├── ModalSelectors (conditional)
│   │   │   │   ├── ModelSelector
│   │   │   │   ├── ProviderSelector
│   │   │   │   ├── SessionSelector
│   │   │   │   └── CheckpointSelector
│   │   │   ├── SchedulerView (conditional)
│   │   │   └── ChatInput (conditional)
│   │   │       ├── ToolConfirmation
│   │   │       ├── ToolExecutionIndicator
│   │   │       ├── QuestionPrompt
│   │   │       └── UserInput
```

### Key Hooks
- `useAppState` — centralized state for the entire app
- `useChatHandler` — manages AI conversation flow
- `useToolHandler` — tool confirmation/execution pipeline
- `useAppHandlers` — slash commands, cancel, mode toggle
- `useContextPercentage` — tracks token usage
- `useResponsiveTerminal` — adaptive layout breakpoints
- `useTheme` — theme colors via React context
- `useInputState` — input with placeholder system for @file mentions

## Notable Design Patterns

### 1. Theme System
- 39 themes defined in `themes.json`, loaded at startup
- Each theme provides: `primary`, `secondary`, `success`, `error`, `warning`, `info`, `tool`, `text`, `base` colors
- Runtime switching via `/theme` command
- Responsive: narrow terminals get simplified layouts

### 2. Responsive Terminal
Three breakpoints based on terminal width:
- **Narrow** (< ~60 cols): Simplified boxes, shorter labels
- **Normal** (~60-100 cols): Standard layout
- **Wide** (> 100 cols): Full details, longer descriptions

### 3. Tool Confirmation Flow
```
Tool call received
  → Validate with registered validator (optional)
  → Run formatter to generate preview (optional)
  → Show preview + confirm/cancel select
  → Auto-cancel on formatter error
  → Auto-proceed on validation error (lets model self-correct)
```

### 4. File @Mentions
- Typing `@` triggers file autocomplete
- Fuzzy file matching with scoring
- Tab to select, arrow keys to navigate
- Selected file content injected as placeholder in prompt
- Full content sent to LLM, display shows `[@filename]`

### 5. Bash Streaming Progress
- Real-time output streaming via EventEmitter
- Output preview during execution
- Token count shown on completion
- Truncation to 4k chars for context savings
- Escape to cancel running command

### 6. Context Window Tracking
- Tracks token usage per message
- Shows percentage in status bar
- Warning at 60%+ usage
- Suggests `/compact` command when high

### 7. Compact Tool Display
- Ctrl+O toggles between expanded and compact tool results
- Compact mode: shows running tally of tool counts
- Expanded mode: shows full tool output
- Accumulated counts flushed to static display on toggle

### 8. Development Mode
- Three modes: `normal`, `plan`, `development`
- Shift+Tab to cycle
- Visual indicator in input area
- Affects tool auto-approval behavior

### 9. Slash Command System
- `CommandRegistry` class with registration, lookup, fuzzy completion
- Tab completion with fuzzy scoring
- Commands: /help, /clear, /model, /provider, /checkpoint, /export, /compact, /status, /tasks, /schedule, /ide, /init, /lsp, /mcp, /setup, /update, /usage, /exit
- Custom commands loaded from user config

### 10. Chat History (Static)
- Uses Ink's `<Static>` component — content never re-renders
- Messages appended via queue system (`addToChatQueue`)
- Ensures scroll stability — old messages don't flicker
- Live component (streaming) separate from static history
