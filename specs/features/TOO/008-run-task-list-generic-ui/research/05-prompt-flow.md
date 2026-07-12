# Prompt Flow

**Source:** `src/acp/agent.ts` (lines 435–890) and `src/acp/session.ts` (lines 336–372)

## Agent-Level `prompt()` — `src/acp/agent.ts`

### Step 1: Restore session

```typescript
async prompt(params: PromptRequest): Promise<PromptResponse> {
  const session = await this.restoreSession(params.sessionId)
  const { message, images } = promptToPiMessage(params.prompt)
```

### Step 2: Built-in slash command handling (adapter-side)

Lines 442–880 — if the message starts with `/` and no images, parse command name + args and handle built-in commands (
compact, session, name, steering, follow-up, changelog, export, autocompact). Each returns `{ stopReason: 'end_turn' }`
immediately.

### Step 3: Delegate to session

```typescript
const result = await session.prompt(message, images)

const stopReason: StopReason =
  result === 'error' ? (session.wasCancelRequested() ? 'cancelled' : 'end_turn') : result

return { stopReason }
```

## Session-Level `prompt()` — `src/acp/session.ts`

### How `expandSlashCommand` Factors In

Line 338:

```typescript
async prompt(message: string, images: unknown[] = []): Promise<StopReason> {
  // pi RPC mode disables slash command expansion, so we do it here.
  const expandedMessage = expandSlashCommand(message, this.fileCommands)
```

`expandSlashCommand()` is from `./slash-commands.js`. It expands **file-based** slash commands (prompt templates from
`.pi/prompts/*.md` or similar). If the message starts with `/commandname` and a matching file command exists, it
replaces the command with the file's content (template).

**Important distinction:**

- **Built-in slash commands** (compact, export, etc.) are handled in `agent.ts` *before* reaching `session.prompt()` —
  they never call the model.
- **File-based slash commands** are expanded in `session.prompt()` — the expanded text IS sent to the model.

### Turn Queueing

Lines 340–372:

```typescript
const turnPromise = new Promise<StopReason>((resolve, reject) => {
  const queued: QueuedTurn = { message: expandedMessage, images, resolve, reject }

  if (this.pendingTurn) {
    this.turnQueue.push(queued)
    this.emit({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: `Queued message (position ${this.turnQueue.length}).` } })
    this.emit({ sessionUpdate: 'session_info_update', _meta: { piAcp: { queueDepth: this.turnQueue.length, running: true } } })
    return
  }

  this.startTurn(queued)
})

return turnPromise
```

### `startTurn()` (lines 473–513)

Sets `cancelRequested = false`, `inAgentLoop = false`, stores `pendingTurn`, emits queue depth, then calls:

```typescript
this.proc.prompt(t.message, t.images).catch(err => { ... })
```

Completion is driven by pi events (`agent_end`), not the RPC response.

## Where Prompt Injection (Adding Marker Instructions) Would Be Added

### Option A: In `agent.ts` before calling `session.prompt()`

After the built-in slash command block, before line 882:

```typescript
// Inject marker instructions into the prompt
const enhancedMessage = injectPlanMarkerInstructions(message)

const result = await session.prompt(enhancedMessage, images)
```

This would prepend/append system instructions telling the model to output plan markers in its response.

### Option B: In `session.ts` inside `prompt()`, after `expandSlashCommand`

After line 338:

```typescript
const expandedMessage = expandSlashCommand(message, this.fileCommands)
// Inject marker instructions
const enhancedMessage = injectPlanMarkerInstructions(expandedMessage)
```

### Option C: In `startTurn()` before calling `proc.prompt()`

Line 488:

```typescript
this.proc.prompt(t.message, t.images).catch(...)
```

Could modify `t.message` here, but this is after queueing so queued messages would also get injected.

### Recommendation

**Option A** is cleanest — it's at the agent level, after built-in commands are filtered out, and before the session
layer. It ensures only messages that actually go to the model get the injection.