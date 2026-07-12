# Built-in Slash Command Pattern

**Source:** `src/acp/agent.ts`, lines 62–101 (advertising) and lines 435–890 (handling)

## Advertising: `builtinAvailableCommands()`

Lines 62–101 — returns a static array of `AvailableCommand[]`:

```typescript
function builtinAvailableCommands(): AvailableCommand[] {
  return [
    { name: 'compact',       description: 'Manually compact the session context', input: { hint: 'optional custom instructions' } },
    { name: 'autocompact',   description: 'Toggle automatic context compaction',  input: { hint: 'on|off|toggle' } },
    { name: 'export',        description: 'Export session to an HTML file...' },
    { name: 'session',       description: 'Show session stats...' },
    { name: 'name',          description: 'Set session display name',             input: { hint: '<name>' } },
    { name: 'steering',      description: 'Get/set pi steering message delivery mode', input: { hint: '(no args to show) all | one-at-a-time' } },
    { name: 'follow-up',     description: 'Get/set pi follow-up message delivery mode', input: { hint: '(no args to show) all | one-at-a-time' } },
    { name: 'changelog',     description: 'Show pi changelog' }
  ]
}
```

These are merged with pi's own commands (from `proc.getCommands()` or file-based slash commands) via `mergeCommands()` (
lines 104–116), which preserves order and de-dupes by name (first wins). The merged list is sent as
`sessionUpdate: "available_commands_update"` after `session/new` (line 404) and `session/load` (line 1083).

## Handling Pattern in `prompt()`

Lines 435–890. The pattern is:

### 1. Parse command name and args

```typescript
const { message, images } = promptToPiMessage(params.prompt)

if (images.length === 0 && message.trimStart().startsWith('/')) {
  const trimmed = message.trim()
  const space = trimmed.indexOf(' ')
  const cmd = space === -1 ? trimmed.slice(1) : trimmed.slice(1, space)
  const argsString = space === -1 ? '' : trimmed.slice(space + 1)
  const args = parseCommandArgs(argsString)
```

### 2. Handle each command with if-blocks

Each built-in command is an `if (cmd === '...')` block that:

1. **Calls adapter-side logic** — invokes `session.proc.*` methods or reads files
2. **Emits session updates** — uses `this.conn.sessionUpdate()` to send `agent_message_chunk` (and sometimes
   `session_info_update`)
3. **Returns `{ stopReason: 'end_turn' }`** — short-circuits, does NOT call `session.prompt()`

### 3. Example: `/steering` (lines 561–606)

```typescript
if (cmd === 'steering') {
  const modeRaw = String(args[0] ?? '').toLowerCase()
  const state = (await session.proc.getState()) as any
  const current = String(state?.steeringMode ?? '')

  if (!modeRaw) {
    // No arg: report current value
    await this.conn.sessionUpdate({
      sessionId: session.sessionId,
      update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: `Steering mode: ${current}` } }
    })
    return { stopReason: 'end_turn' }
  }

  if (modeRaw !== 'all' && modeRaw !== 'one-at-a-time') {
    // Invalid: show usage
    await this.conn.sessionUpdate({ ... content: { type: 'text', text: 'Usage: /steering all | /steering one-at-a-time' } })
    return { stopReason: 'end_turn' }
  }

  // Valid: set and confirm
  await session.proc.setSteeringMode(modeRaw as 'all' | 'one-at-a-time')
  await this.conn.sessionUpdate({ ... content: { type: 'text', text: `Steering mode set to: ${modeRaw}` } })
  return { stopReason: 'end_turn' }
}
```

### 4. Fall-through to normal prompt

If no built-in command matches (line 882):

```typescript
const result = await session.prompt(message, images)
```

## Complete list of built-in commands handled in `prompt()`

| Command        | Lines   | Behavior                                                                  |
|----------------|---------|---------------------------------------------------------------------------|
| `/compact`     | 449–473 | Calls `proc.compact()`, emits compaction result                           |
| `/session`     | 475–508 | Calls `proc.getSessionStats()`, emits stats text                          |
| `/name`        | 510–559 | Calls `proc.setSessionName()`, emits `session_info_update` + confirmation |
| `/steering`    | 561–606 | Gets/sets `proc.setSteeringMode()`                                        |
| `/follow-up`   | 608–653 | Gets/sets `proc.setFollowUpMode()`                                        |
| `/changelog`   | 655–731 | Reads `CHANGELOG.md` from pi installation                                 |
| `/export`      | 733–850 | Calls `proc.exportHtml()`, emits resource_link                            |
| `/autocompact` | 852–879 | Toggles `proc.setAutoCompaction()`                                        |

## Key Observations

- **No model invocation:** All built-in commands are handled entirely adapter-side — `proc.prompt()` is never called.
- **Always return `end_turn`:** Every built-in command returns `{ stopReason: 'end_turn' }`.
- **Image guard:** Built-in commands only work when no images are attached (`images.length === 0`).
- **File-based slash commands** are different — they are expanded inside `session.prompt()` via `expandSlashCommand()`
  and sent to the model.
- `parseCommandArgs` is imported from `./slash-commands.js` (line 42).