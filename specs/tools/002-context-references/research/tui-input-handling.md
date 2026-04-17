# TUI Input Handling Research

## Main TUI Entry Point and Input Handling

**Main TUI file:** `internal/tui/tui.go`

- **Entry point:** `Run(ctx, cfg)` at line 165 creates a Bubble Tea `model` and runs it via `p.Run()`
- **Main model struct** (`model`) at line 21 contains:
    - `inputModel InputModel` — handles text input
    - `chatModel ChatModel` — displays conversation
    - `statusModel StatusModel` — status bar
    - `agentCh chan agentMsg` — channel for agent communication

**Input routing:**

- `Update()` at line 246 routes `tea.KeyPressMsg` to `handleKey()` (line 387)
- `handleKey()` delegates most keys to `inputModel.HandleKey()` (line 538)
- When Enter is pressed → `InputSubmitMsg` is emitted → `submitPrompt()` is called (line 283-287)

## How User Messages Are Processed Before Being Sent

**`InputModel.HandleKey()` in `input.go:78-345`:**

- Tracks cursor position, text, history, and completion state
- When **Enter** is pressed (line 82-123):
    1. Applies file @mention selection if in mention mode
    2. Applies completion selection if in completion mode
    3. Trims whitespace
    4. Calls `extractMentions(text)` to find `@path` references (line 114)
    5. Saves to history (line 116-119)
    6. Returns `InputSubmitMsg{Text: text, Mentions: mentions}`

**`submitPrompt()` in `agent_loop.go:190-224`:**

- Takes the text and mentions
- **Attaches file annotations** (lines 191-203): Appends `[Referenced file: path]` for each @mention
- Logs the message via `m.cfg.Logger.UserMessage(promptText)`
- Adds user message to `chatModel.Messages`
- Starts agent loop via `go m.runAgentLoop(promptText)` (line 221)

## Existing Message Preprocessing / Expansion Patterns

### @ mention system (`input.go`, `completion.go`):

1. **`extractMentions()` in `completion.go:364-381`:**
    - Scans for @ followed by path (no space/tab/newline/@)
    - Returns raw path strings without the @

2. **`CompleteMention()` in `completion.go:273-280`:**
    - Returns file completion candidates for `@` prefix
    - Uses `matchingFiles()` which walks the workDir and matches relative paths
    - Supports fuzzy matching via `fuzzyMatchPath()`

3. **`findMentionAtCursor()` in `completion.go:351-361`:**
    - Finds the `@` at cursor position while typing
    - Used to update completion results as user types after `@`

4. **In `submitPrompt()` (agent_loop.go:191-203):**
   ```go
   if len(mentions) > 0 {
       var refs strings.Builder
       refs.WriteString(text)
       refs.WriteString("\n")
       for _, path := range mentions {
           refs.WriteString("\n[Referenced file: ")
           refs.WriteString(path)
           refs.WriteString("]")
       }
       promptText = refs.String()
   }
   ```

### Slash commands (`commands.go`):

- Commands like `/help`, `/plan`, `/run`, `/skills` are handled in `handleSlashCommand()` (line 17)
- Commands starting with `/` are intercepted before reaching the agent (tui.go:284-286)

## TUI Component Structure

```
model (tui.go:21)
├── inputModel InputModel (input.go)
│   ├── Text, CursorPos, History
│   ├── Completion (ghost autocomplete)
│   ├── MentionMode + MentionResult (@ file completion)
│   ├── CyclingIdx (command cycling)
│   └── Skills, SkillDirs, WorkDir
├── chatModel ChatModel (chat.go)
│   ├── Messages []message
│   ├── Scroll offset
│   ├── Streaming, Thinking (live updates)
│   └── ToolDisplay, TraceLog
├── statusModel StatusModel (status.go)
│   ├── ActiveTool, ActiveTools
│   ├── GitBranch
│   └── TokenTracker
├── themeManager *ThemeManager (theme.go)
├── face *FaceRenderer (face.go)
├── matrix matrixState (matrix.go)
└── agentCh chan agentMsg (agent_loop.go)
```

## Key Files

| File                         | Purpose                                             |
|------------------------------|-----------------------------------------------------|
| `internal/tui/tui.go`        | Bubble Tea model, layout, routing                   |
| `internal/tui/input.go`      | Text input, key handling, @ mentions, completions   |
| `internal/tui/completion.go` | Completion logic, file matching, mention extraction |
| `internal/tui/agent_loop.go` | Message flow to agent, streaming                    |
| `internal/tui/commands.go`   | Slash command handlers                              |
| `internal/tui/chat.go`       | Message rendering with glamour markdown             |
