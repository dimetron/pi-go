# Codex App-Server Protocol Research

## Source: tmp/codex-plugin-cc/

The codex-plugin-cc is a Claude Code plugin that wraps the Codex CLI's app-server
mode. It speaks a **JSON-RPC 2.0 over newline-delimited JSON (JSONL)** protocol —
NOT the ACP (Agent Communication Protocol) used by claude/gemini/cursor/copilot.

## Transport

Two modes:

1. **Direct (spawned)** — `SpawnedCodexAppServerClient` spawns `codex app-server`
   as a child process, communicating over stdin/stdout pipes. This is "direct mode."
2. **Broker (shared)** — `BrokerCodexAppServerClient` connects to a Unix domain
   socket managed by `app-server-broker.mjs`, which itself spawns one `codex app-server`
   and multiplexes multiple clients.

**Direct mode = no broker, no shim.** The Go implementation should spawn `codex app-server`
directly and speak JSON-RPC over its stdin/stdout.

## JSON-RPC Message Routing

Incoming JSONL lines are parsed and routed:

- Has `id` + `method` → server-initiated request → client responds with error
- Has `id`, no `method` → response to a pending request. Matched by `id` in pending map
- Has `method`, no `id` → notification → dispatched to notification handler

## Protocol Flow (initialize → thread/start → turn/start)

### 1. initialize (client→server, request)

```json
{
  "id": 1, "method": "initialize",
  "params": {
    "clientInfo": { "title": "pi-go", "name": "pi-go", "version": "dev" },
    "capabilities": {
      "experimentalApi": false,
      "requestAttestation": false,
      "optOutNotificationMethods": [
        "item/agentMessage/delta",
        "item/reasoning/summaryTextDelta",
        "item/reasoning/summaryPartAdded",
        "item/reasoning/textDelta"
      ]
    }
  }
}
```

Response: `{ "userAgent": "codex-app-server-..." }`

### 2. initialized (client→server, notification)

```json
{ "method": "initialized", "params": {} }
```

### 3. thread/start (client→server, request)

```json
{
  "id": 2, "method": "thread/start",
  "params": {
    "cwd": "/path/to/project",
    "model": null,
    "approvalPolicy": "never",
    "sandbox": "read-only",
    "serviceName": "pi-go-codex-subagent",
    "ephemeral": true
  }
}
```

Response: `{ "thread": { "id": "thr_1", ... }, "model": "...", ... }`

### 4. turn/start (client→server, request)

```json
{
  "id": 3, "method": "turn/start",
  "params": {
    "threadId": "thr_1",
    "input": [{ "type": "text", "text": "Fix the bug", "text_elements": [] }],
    "model": null,
    "effort": null,
    "outputSchema": null
  }
}
```

Response: `{ "turn": { "id": "turn_1", "status": "inProgress", "items": [], "error": null } }`

### 5. turn/interrupt (client→server, request)

```json
{ "id": 4, "method": "turn/interrupt", "params": { "threadId": "thr_1", "turnId": "turn_1" } }
```

## Server Notifications

| Method           | Params                                    | When                   |
|------------------|-------------------------------------------|------------------------|
| `thread/started` | `{ thread: { id, name, ... } }`           | After thread/start     |
| `turn/started`   | `{ threadId, turn: { id, status } }`      | Turn begins processing |
| `item/started`   | `{ threadId, turnId, item: ThreadItem }`  | Item begins            |
| `item/completed` | `{ threadId, turnId, item: ThreadItem }`  | Item finishes          |
| `turn/completed` | `{ threadId, turn: { id, status, ... } }` | Turn finishes          |
| `error`          | `{ error: { message, code? } }`           | Server-side error      |

## Item Types (ThreadItem)

Discriminated union by `type`:

- **agentMessage**: `{ text, phase: "final_answer" | "analysis" | null }` — the agent's text output
- **commandExecution**: `{ command, status, exitCode }` — shell command ran by codex
- **fileChange**: `{ changes: [{ path, ... }] }` — file modifications
- **reasoning**: `{ summary: [...] }` — reasoning summary text
- **mcpToolCall**: `{ server, tool, status }` — MCP tool invocation
- **dynamicToolCall**: `{ tool, status }` — dynamic tool
- **webSearch**: `{ query }` — web search
- **enteredReviewMode / exitedReviewMode**: review mode transitions

## Turn Completion Detection

Three paths:

1. **Explicit `turn/completed`** on the main thread → immediate completion
2. **Inferred completion** — when `agentMessage` with `phase: "final_answer"` completes
   but no `turn/completed` follows within 250ms → synthetic completion
3. **Subagent turn completion** — `turn/completed` for subagent thread removes from
   activeSubagentTurns, triggers inferred completion check

## Key Difference from ACP

ACP uses the `coder/acp-go-sdk` with `Initialize → NewSession → Prompt` lifecycle.
The SDK handles notification routing and provides `ClientSideConnection`.

Codex uses its own JSON-RPC protocol with `initialize → thread/start → turn/start`.
There is no Go SDK for this — we must implement the JSON-RPC client ourselves.

## Sandbox Modes

- `read-only` — no file modifications (default for reviews)
- `workspace-write` — can modify files in the workspace
- `danger-full-access` — full access

## Approval Policy

- `never` — never ask for approval (autonomous)
- `on-failure` — ask on failure
- `on-request` — ask on request
- `always` — always ask

The plugin always uses `never` for autonomous subagent operation.