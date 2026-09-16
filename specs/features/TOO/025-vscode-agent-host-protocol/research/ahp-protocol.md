# AHP Wire-Level Digest

Spec: https://microsoft.github.io/agent-host-protocol/ · Source: https://github.com/microsoft/agent-host-protocol (MIT; spec is DRAFT status). Framing: JSON-RPC 2.0. Current `protocolVersion`: **"0.9.0"** (`SupportedProtocolVersions()` = `["0.9.0","0.8.0","0.7.0","0.6.0","0.5.2","0.5.1"]`). Channel stability: Root/Session/Chat/Terminal/Telemetry/ResourceWatch = 2 (Stable); Changeset/MCP = 1.2; Automation = 1.0.

## 1. Connection-level methods (`channel: "ahp-root://"`)

### `initialize` (client→server request; MUST be first)
```jsonc
// params
{ "channel": "ahp-root://",
  "protocolVersions": ["0.9.0","0.8.0"],  // string[], SemVer, most-preferred first
  "clientId": "string",                    // opaque per-connection id, reused on reconnect
  "clientInfo": {"name": string, "version": string}?,  // Implementation
  "initialSubscriptions": ["ahp-session:/<uuid>"]?,    // URI[]
  "locale": "en-US"?,                      // BCP 47
  "capabilities": ClientCapabilities? }
// result
{ "protocolVersion": "0.9.0",              // MUST be one of the offered entries
  "serverSeq": number,
  "serverInfo": Implementation?,
  "snapshots": Snapshot[],                 // one per initialSubscriptions URI
  "defaultDirectory": URI?,
  "completionTriggerCharacters": string[]?,
  "terminalCommandPrefix": string?,        // convention "!"
  "telemetry": TelemetryCapabilities? }
```
No offered version supported ⇒ error `-32005` `UnsupportedProtocolVersion` with `data.supportedVersions`.

### `ping`
Params `{ channel: 'ahp-root://' }` → result `null`. Server MUST respond even pre-`initialize`.

### `reconnect`
```jsonc
// params:  { channel: "ahp-root://", clientId, lastSeenServerSeq: number, subscriptions: URI[] }
// result:  { type: "replay",   actions: ActionEnvelope[], missing: URI[] }   // replay from seq
//       or { type: "snapshot", snapshots: Snapshot[] }                      // gap > replay buffer
```
Protocol notifications are NOT replayed (client re-fetches session list via `listSessions`).

### `subscribe` / `unsubscribe`
- `subscribe` (request): params `{ channel: URI; delivery?: SubscriptionDeliveryOptions; view?: SubscribeView }` → result `{ snapshot?: Snapshot }` (omitted for stateless channels). `Snapshot = { resource: URI; state: RootState|SessionState|ChatState|…; fromSeq: number }`.
- `unsubscribe` (**notification**, no response): `{ channel: URI }`.

### `action` / `dispatchAction` envelopes
```jsonc
// client→server notification "dispatchAction": { channel: URI, clientSeq: number, action: StateAction }
// server→client notification "action":
{ channel: URI, action: StateAction, serverSeq: number,      // monotonic
  origin: { clientId: string, clientSeq: number }?,          // set on echoes for reconciliation
  rejectionReason: string? }                                  // set when server rejected a client action
```
Invalid client actions MUST be echoed with `rejectionReason`; actions referencing a non-existent channel are silently ignored (no echo).

### Error codes (verified in `clients/go/ahptypes/errors.generated.go`)
Standard: `-32700` ParseError, `-32600` InvalidRequest, `-32601` MethodNotFound, `-32602` InvalidParams, `-32603` InternalError.
AHP: `-32001` SessionNotFound · `-32002` ProviderNotFound · `-32003` SessionAlreadyExists · `-32004` TurnInProgress · `-32005` UnsupportedProtocolVersion · `-32006` reserved · `-32007` AuthRequired (`data.resources: ProtectedResourceMetadata[]`) · `-32008` NotFound · `-32009` PermissionDenied · `-32010` AlreadyExists.

## 2. Root channel (`ahp-root://`)
- `RootState`: global lightweight data (agents catalog, terminals catalog, host config) — does **not** contain the session list.
- Session catalog fetched imperatively via `listSessions` → `SessionSummary[]`, kept in sync via notifications `root/sessionAdded`, `root/sessionRemoved`, `root/sessionSummaryChanged`. Generic `root/progress` notification for long ops.
- `SessionSummary`: identity (`resource`, `provider`, `createdAt`, `workingDirectories`) + aggregates (`title`, `status` bits incl. `InputNeeded`, activity, `isRead`/`isArchived` flags, chats catalog mirrors).
- `resolveSessionConfig` — iterative resolution of the session-config property schema; `sessionConfigCompletions` for dynamic values.

## 3. Session channel (`ahp-session:/<uuid>`)
`SessionState` fields: `lifecycle` (`SessionLifecycle` init states: `'creating'` → ready via `session/ready` or `session/creationFailed`), `creationError?`, `chats: ChatSummary[]`, `defaultChat: URI`, `activeClients[]` (host-managed via `session/activeClientSet`/`Removed`), `customizations[]`, `inputNeeded: SessionInputRequest[]` (aggregated across chats; kinds `chatInput` | `toolConfirmation` | `toolClientExecution` | `toolAuthentication`), `workingDirectories: URI[]`, plus title/isRead/isArchived/activity/changesets/serverTools.

Key commands: `createSession` (client picks URI; params `provider?`, `workingDirectories[]`, `activeClient?`; result carries snapshot), `disposeSession`, `fetchTurns` (pagination into history), `completions`. Notifications: `action` (server push), `dispatchAction` (client), `unsubscribe`.

Session lifecycle on create: client picks URI → `createSession` → `subscribe` (MAY batch) → snapshot `lifecycle:'creating'` → async backend init → `session/ready` or `session/creationFailed` → broadcast `root/sessionAdded` to `ahp-root://` subscribers. Duplicate URI ⇒ `-32003 SessionAlreadyExists`.

## 4. Chat channel (`ahp-chat:/<uuid>`)
`ChatState`: `{ resource, title, status, activity?, modifiedAt, origin?, interactivity?: 'full'|'read-only'|'hidden', workingDirectories?, turns: Turn[], turnsNextCursor?, activeTurn?, steeringMessage?: PendingMessage, queuedMessages?: PendingMessage[], draft?, _meta? }`.

`PendingMessage = { id, message }`, kind `'steering'` (injected into current turn) or `'queued'` (auto-starts next turn).

### Turn lifecycle
- `chat/turnStarted` (client-dispatchable; only `Message.kind:'user'`): `{ type, turnId, startedAt, message, queuedMessageId?, _meta? }` — write-ahead; client applies optimistically, server echoes with `origin` or rejects with `rejectionReason`.
- `chat/responsePart`: `{ type, turnId, part: ResponsePart }`. `ResponsePart.kind`: `markdown` (`{kind,id,content}`), `contentRef`, `toolCall` (`{kind, toolCall: ToolCallState}`), `reasoning`, `systemNotification`, `inputRequest`, `error`.
- `chat/delta` (streaming): `{ type, turnId, partId, content }` — appends to an existing markdown/reasoning part.
- `chat/turnComplete`: `{ type, turnId, duration? }` (ms, opaque) · `chat/turnCancelled`: `{ type, turnId, duration }` (rejected if no active turn) · `chat/turnResume` reopens an errored turn when its `ErrorResponsePart.resumable === true`.
- `chat/error`: `{ type, turnId?, error: ErrorInfo, resumable? }` — the only path for error parts; ends turn with `TurnState.Error` (`'complete'|'cancelled'|'error'`).

### Tool-call state machine
`ToolCallStatus`: `streaming → pending-confirmation → running → (auth-required → running) → pending-result-confirmation → completed | cancelled` (cancelled also pre-execution; `ToolCallCancellationReason: 'denied'|'skipped'|'result-denied'`).
- `chat/toolCallStart`: `{ type, turnId, toolCallId, toolName, displayName?, intention?, contributor? }`; `ToolCallContributor = {kind:'client', clientId} | {kind:'mcp', customizationId}`.
- `chat/toolCallDelta`: `{ type, turnId, toolCallId, content? (partial params), invocationMessage? }`.
- `chat/toolCallReady`: `{ type, turnId, toolCallId, invocationMessage, toolInput?, confirmationTitle?, riskAssessment?, edits?{items: FileEdit[]}, editable?, confirmed?: ToolCallConfirmationReason ('not-needed'|'user-action'|'setting'), options?: ConfirmationOption[]{id,label,kind:'approve'|'deny',group?} }` — `confirmed` set ⇒ straight to `running`; else `pending-confirmation`.
- `chat/toolCallConfirmed` (client): `{ type, turnId, toolCallId, approved, confirmed?, reason?, editedToolInput?, userSuggestion?, reasonMessage?, selectedOptionId? }` — MUST be rejected if call not in `pending-confirmation`.
- `chat/toolCallComplete`: `{ type, turnId, toolCallId, result: ToolCallResult, requiresResultConfirmation? }`; `ToolCallResult = { success: boolean; pastTenseMessage: StringOrMarkdown; content?: ToolResultContent[]; structuredContent?; error?: {message, code?} }`; `ToolResultContent.type: 'text'|'embeddedResource'|'resource'|'fileEdit'|'terminal'|'subagent'`.
- `chat/toolCallResultConfirmed` (client): `{ type, turnId, toolCallId, approved }`.
- `chat/toolCallAuthRequired` (only from `running`; MCP contributors) → resolved by connection-level `authenticate`, then `chat/toolCallAuthResolved`.
- Also `chat/toolCallContentChanged`.

### Auth flow
- Hosts/agents MAY declare `protectedResources` in `AgentInfo`; client SHOULD obtain a Bearer token and push via `authenticate` command (connection-level, `ahp-root://`).
- Using a session backed by an unauthenticated agent ⇒ server SHOULD return `-32007 AuthRequired` with resource metadata in `data`; server dispatches `auth/required` notification.
- `toolAuthentication` `inputNeeded` entries are the one kind resolved via `authenticate` rather than a `chat/*` action; host removes the aggregate entry on `chat/toolCallAuthResolved`.

## 5. Transport
- No transport prescribed (LSP/DAP style); requirements: ordered, reliable, bidirectional, complete-message delivery. Not negotiated in-protocol.
- **WebSocket**: server acts as WS server; **each text frame = exactly one complete JSON-RPC message**; **no subprotocol, no mandated URL path, no connection-token header/query format defined by the spec** — endpoint gating is a transport-layer concern settled during the HTTP upgrade ("via query parameters, headers, or the HTTP upgrade request") before `initialize`.
- Keep-alive: protocol `ping`; transport WS ping/pong MAY also be used; intervals implementation-specific.
- stdio/MessagePort framing: **not defined by the spec** (only generically allowed).

## 6. Go client library
Module `github.com/microsoft/agent-host-protocol/clients/go` (latest tag v0.6.0; Go ≥1.22; sole dep `github.com/coder/websocket v1.8.13`).
- `ahptypes` — generated wire types only (`state/actions/commands/notifications/messages/errors/version.generated.go`), generated from canonical TS `types/`; CI enforces no drift.
- `ahp` — async `Client` over pluggable `Transport` (`transport.go`), pure reducers (`reducers.go`), `ahp/hosts.MultiHostClient` (2+ hosts), `Subscription.Events() <-chan SubscriptionEvent` (variants `SubscriptionEventAction{Envelope}`, `SessionAdded`, `SessionRemoved`, `SessionSummaryChanged`, `AuthRequired`), `ServerRequestHandler`, `ResourceRequestHandlers`. `ahp.Connect(ctx, transport, ahp.DefaultConfig())` → `client.Initialize(ctx, "my-client", ahptypes.SupportedProtocolVersions(), nil)` → `client.Subscribe(ctx, uri)`.
- `ahpws` — WS transport: `ahpws.Connect(ctx, "ws://localhost:12345", DialOptions{HTTPHeader, Subprotocols, TLS})`; accepts `ws://`/`wss://`, any path, no subprotocol set.
- **Server support: none — client-only.** The reference AHP server is VS Code's (`src/vs/platform/agentHost/node/`, TS, standalone entry `agentHostServerMain.ts`). Implementations page lists exactly one server.