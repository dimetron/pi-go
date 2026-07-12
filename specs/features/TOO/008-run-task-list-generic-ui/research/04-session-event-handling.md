# Session Event Handling

**Source:** `src/acp/session.ts`, lines 515–861

## How `handlePiEvent()` Works

`PiAcpSession` registers as an event listener in its constructor (line 313):

```typescript
this.proc.onEvent(ev => this.handlePiEvent(ev))
```

`handlePiEvent()` (lines 515–861) is a switch on `ev.type`:

| Event type              | Lines   | Behavior                                        |
|-------------------------|---------|-------------------------------------------------|
| `message_update`        | 519–610 | Stream text/thinking deltas, surface tool calls |
| `tool_execution_start`  | 612–685 | Mark tool in_progress, capture file snapshots   |
| `tool_execution_update` | 687–709 | Emit partial results                            |
| `tool_execution_end`    | 711–768 | Emit final results, diffs, cleanup              |
| `extension_ui_request`  | 770–780 | Handle extension UI (select/confirm)            |
| `auto_retry_start`      | 782–788 | Emit retry message                              |
| `auto_retry_end`        | 790–796 | Emit retry finished message                     |
| `auto_compaction_start` | 798–808 | Emit compaction message                         |
| `auto_compaction_end`   | 809–819 | Emit compaction done message                    |
| `agent_start`           | 820–823 | Set `inAgentLoop = true`                        |
| `turn_end`              | 825–829 | No-op (wait for `agent_end`)                    |
| `agent_end`             | 831–856 | Resolve pending turn, start next queued prompt  |

## `message_update` with `text_delta` — How `agent_message_chunk` Is Emitted

Lines 519–529:

```typescript
case 'message_update': {
  const ame = (ev as any).assistantMessageEvent

  // Stream assistant text.
  if (ame?.type === 'text_delta' && typeof ame.delta === 'string') {
    this.emit({
      sessionUpdate: 'agent_message_chunk',
      content: { type: 'text', text: ame.delta } satisfies ContentBlock
    })
    break
  }

  if (ame?.type === 'thinking_delta' && typeof ame.delta === 'string') {
    this.emit({
      sessionUpdate: 'agent_thought_chunk',
      content: { type: 'text', text: ame.delta } satisfies ContentBlock
    })
    break
  }
  // ... tool call handling follows
```

### The `emit()` method (lines 400–413)

All session updates go through `this.emit()`:

```typescript
private emit(update: SessionUpdate): void {
  this.lastEmit = this.lastEmit
    .then(() => this.conn.sessionUpdate({ sessionId: this.sessionId, update }))
    .catch(() => {})
}
```

Updates are serialized via a promise chain (`lastEmit`) to ensure ordered delivery.

## Where Marker Parsing Would Be Inserted

Marker parsing (e.g., parsing plan entries from assistant text deltas) would be inserted in the `text_delta` branch of
`message_update`, **before or after** the `this.emit()` call:

```typescript
if (ame?.type === 'text_delta' && typeof ame.delta === 'string') {
  // >>> INSERT MARKER PARSING HERE <<<
  // Accumulate text, detect markers, emit plan updates

  this.emit({
    sessionUpdate: 'agent_message_chunk',
    content: { type: 'text', text: ame.delta } satisfies ContentBlock
  })
  break
}
```

### Considerations for marker parsing:

1. **Text accumulation:** `text_delta` provides only a fragment. A buffer is needed to accumulate text and detect
   markers that may span chunk boundaries.
2. **Marker detection:** Check the accumulated buffer for plan markers (e.g., `[PLAN]` / `[/PLAN]` or similar). When
   detected, emit `sessionUpdate: "plan"` with `PlanEntry[]`.
3. **Client capability check:** Before emitting plan updates, check `clientCapabilities.plan` (currently not stored —
   would need to be passed from `initialize()`).
4. **Text filtering:** If markers should not appear in the chat, the text delta passed to `agent_message_chunk` would
   need to be filtered to exclude marker content.
5. **State management:** The session would need to track plan state (current entries) to send complete `entries` arrays
   on each update (the ACP spec says "the client replaces the entire plan with each update").