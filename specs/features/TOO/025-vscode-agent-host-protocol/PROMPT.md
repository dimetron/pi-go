# pi-go Agent Host — AHP server (`pi agent-host`)

## Objective
Implement a standalone Agent Host Protocol (AHP) server for pi-go (`pi agent-host`) in a new
package `internal/ahp/server`, exposing pi-go agent sessions over WebSocket to any AHP client
(JSON-RPC 2.0, spec v0.9.0, channel-addressed state + ordered ActionEnvelopes). The host
bridges internally to pi-go's existing ACP server composition (`internal/acp/server
RuntimeConfig`+`NewPromptHandler`, reused unchanged), persists sessions via
`internal/session.FileService`, and supports session list, create/prompt/cancel turns, the
tool-call state machine, AHP auth surfaces, and reconnect/replay. Extension-side changes are
out of scope. VS Code Agents-window attach is best-effort (undocumented upstream;
microsoft/vscode#325827/#325827) — generic AHP clients (official Go client) are the
contracted consumers and the primary conformance gate.

## Key Requirements
1. **AHP host over WebSocket** — JSON-RPC 2.0, one complete message per text frame, no
   subprotocol; token-gated handshake; `initialize` version negotiation (accept
   `ahptypes.SupportedProtocolVersions()`); URI-channel routing (`ahp-root://`,
   `ahp-session:/<id>`, `ahp-chat:/<id>`) via `(method, params.channel)`.
2. **State-first sync** — snapshots + ordered `ActionEnvelope{serverSeq, origin?,
   rejectionReason?}`; client actions echoed with `origin` or rejected with
   `rejectionReason`; `reconnect{lastSeenServerSeq}` replays from a 1024-envelope
   per-channel ring or sends fresh snapshots on overflow; `missing` URIs reported.
3. **Bridge, not duplicate** — reuse `acp/server.RuntimeConfig` + `NewPromptHandler`
   verbatim for agent composition (sandbox/tools/LLM/MCP/subagents/LSP/auto-compact);
   the AHP bridge drives the same `PromptHandler` interface; per-session `cleanup()`
   (bash kill-all, LSP/orchestrator shutdown, sandbox close) on `disposeSession`.
4. **Persistence & catalog** — sessions listed from `pisession.FileService.ListMeta`
   (past + live); past sessions re-attachable: subscribe yields a snapshot replayed from
   `events.jsonl` (skip `Partial`; FunctionCall→completed toolCall, FunctionResponse→
   paired by id else last-call-by-name); `fetchTurns` pages older turns.
5. **Turn & tool-call model** — client W-A `chat/turnStarted` (kind 'user') with
   optimistic echo `origin{clientId, clientSeq}` or `rejectionReason`; ADK events map to
   `chat/responsePart{markdown|reasoning,id}` + `chat/delta`, tool calls to
   `chat/toolCallStart→Delta→Ready(confirmed:'setting')→Complete`, cancellation to
   `chat/turnCancelled`, provider failure to `chat/error{resumable:false}`.
6. **Auth surfaces** — connection token at WS handshake (`?tkn=` or Bearer); AHP
   `authenticate` command + `auth/required` notification + `-32007` with
   `data.resources` for provider-protected resources; MCP tool auth via
   `chat/toolCallAuthRequired` + `session.inputNeeded{kind:toolAuthentication}` →
   `chat/toolCallAuthResolved`.
7. **CLI** — `pi agent-host` (cobra; flags `--addr 127.0.0.1:8931`, `--token` (generated,
   printed to stderr), `--model`, `--url`, `--header` repeatable, `--insecure`), err-log at
   `$HOME/.pi-go/sessions/agent-host.err.log`, store open non-fatal, graceful shutdown.

## Acceptance Criteria
### Connection & negotiation
- Given a running host with token T, when a client connects with the wrong token, then the
  WS upgrade is rejected (HTTP 401) before any JSON-RPC traffic.
- Given `initialize` offering `["0.9.0","0.8.0"]`, then the result is
  `protocolVersion:"0.9.0"`, `serverInfo.name:"pi-go"`, snapshots for each
  `initialSubscriptions` URI.
- Given `initialize` with only unsupported versions, then the server replies `-32005`
  with `data.supportedVersions`.
- Given `ping`, then a `null` result is returned (even pre-`initialize`).

### Catalog & persistence
- Given ≥1 past session in pi-go's store, when `listSessions` is called, then its summary
  (id, title, cwd, provider, updatedAt) appears; `root/sessionAdded|Removed|SummaryChanged`
  fire on create/dispose/updates.
- Given `subscribe` to a past session URI (`ahp-session://pi-go/<id>`), then a valid
  `SessionState` snapshot arrives, replayed from stored events; `fetchTurns` pages older
  turns into `ChatState.turns`.
- Given `createSession` on an in-use URI, then `-32003 SessionAlreadyExists`.

### Turns & tool calls
- Given a ready session's default chat, when the client dispatches `chat/turnStarted`
  (user message) and the turn runs, then sequenced `chat/responsePart` + `chat/delta`
  (text and reasoning) arrive, then `chat/turnComplete` (or `turnCancelled` on cancel;
  `chat/error{resumable:false}` on provider failure).
- Given a tool call during a turn, then `chat/toolCallStart → toolCallDelta →
  toolCallReady(confirmed:'setting') → toolCallComplete{result}` arrive in order.
- Given an in-flight turn, when `chat/turnCancelled` is dispatched, then the turn ends
  `cancelled` and pi-go's cancel path (`StopReasonCancelled`) runs.
- Given a client-dispatched invalid action (e.g. `toolCallConfirmed` on a
  non-pending-confirmation call), then it is echoed with `rejectionReason`; unknown-channel
  actions produce no echo.

### Auth
- Given a provider with a protected resource and no token, when a session is created/prompted,
  then `auth/required` (or `-32007` with `data.resources`) is sent, and after the client's
  `authenticate` the flow proceeds.
- Given an MCP tool requiring auth, then `chat/toolCallAuthRequired` + a
  `session.inputNeeded{kind:toolAuthentication}` entry appear and clear via
  `chat/toolCallAuthResolved` after `authenticate`.

### Sync
- Given any `action` notification, then `serverSeq` is strictly increasing, and a client's
  own dispatched action is echoed with matching `origin{clientId, clientSeq}`.
- Given a dropped connection, when `reconnect{lastSeenServerSeq}` is sent, then missed
  envelopes arrive in order, or fresh snapshots when the gap exceeds the buffer, with
  `missing` for disposed sessions.
- Given a host restart, when a client re-subscribes to a prior session URI, then a valid
  snapshot from the store arrives and prompting continues (re-attach).

## Implementation Slices
1. **Wire plumbing** — WS server (gorilla), JSON-RPC 2.0 framing (1 msg/frame), token
   handshake, `initialize`/`ping`/`subscribe`/`unsubscribe`/`reconnect` entry points,
   `StateManager` (reducers, global monotonic `serverSeq`, per-channel 1024 ring,
   echo/rejection semantics), `action` broadcast; **adds module dep**
   `github.com/microsoft/agent-host-protocol/clients/go` v0.6.0 (`ahptypes` used by
   all later slices). files:
   `internal/ahp/server/host.go`, `internal/ahp/server/connection.go`,
   `internal/ahp/server/common.go`, `internal/ahp/server/state.go`,
   `go.mod`, `go.sum`,
   `internal/ahp/server/host_test.go`, `internal/ahp/server/connection_test.go`,
   `internal/ahp/server/state_test.go`, verify:
   `go test ./internal/ahp/server/...`, parallel-safe: no
2. **Auth & error codes** — `AuthManager` (handshake token, `authenticate`, protected
   resources, `auth/required`), AHP error constructors with `data` payloads
   (-32001/-32002/-32003/-32004/-32005/-32007/-32008/-32009/-32010). files:
   `internal/ahp/server/auth.go`, `internal/ahp/server/errors.go`,
   `internal/ahp/server/auth_test.go`, `internal/ahp/server/errors_test.go`, verify:
   `go test ./internal/ahp/server/...`, parallel-safe: no
3. **Store bridge & root catalog** — `SessionStore` over `pisession.FileService`,
   `listSessions`, `root/session*` notifications, `fetchTurns`, JSONL→`Turn[]`
   replay-walk (port of `internal/acp/server/replay.go`). files:
   `internal/ahp/server/store.go`, `internal/ahp/server/root.go`,
   `internal/ahp/server/replay.go`, `internal/ahp/server/store_test.go`,
   `internal/ahp/server/root_test.go`, `internal/ahp/server/replay_test.go`, verify:
   `go test ./internal/ahp/server/...`, parallel-safe: no
4. **ACP bridge — session lifecycle** — reuse `acp/server.RuntimeConfig`+`NewPromptHandler`;
   per-URI `piSessionState` cache (lazy first-prompt init); `createSession`/`disposeSession`
   (with full `cleanup()`), `session/ready|creationFailed`, `SessionState` snapshot,
   hostile-id hashing. files: `internal/ahp/server/bridge.go`,
   `internal/ahp/server/session.go`, `internal/ahp/server/bridge_test.go`,
   `internal/ahp/server/session_test.go`, verify: `go test ./internal/ahp/server/...`,
   parallel-safe: no
5. **Chat turn flow** — `createChat` (default chat), `chat/turnStarted` W-A handling with
   optimistic echo/rejection, prompt execution via bridge `PromptHandler`, ADK→AHP
   adapter (markdown/reasoning parts + `chat/delta`, `turnComplete|turnCancelled|error`),
   cancel → in-flight ctx-cancel. files: `internal/ahp/server/chat.go`,
   `internal/ahp/server/adapter/stream.go`, `internal/ahp/server/chat_test.go`,
   `internal/ahp/server/adapter/stream_test.go`, verify:
   `go test ./internal/ahp/server/...`, parallel-safe: no
6. **Tool-call state machine** — map `extension.BuildToolCallCallbacks` to
   `chat/toolCallStart→Delta→Ready(confirmed:'setting')→Complete` with result content
   shapes and failure/error shapes; nested subagent flattening; `ToolCallStatus`
   transitions in `ChatState.activeTurn`. files:
   `internal/ahp/server/adapter/toolcall.go`, `internal/ahp/server/adapter/toolcall_test.go`,
   verify: `go test ./internal/ahp/server/...`, parallel-safe: no
7. **Auth-required tool & provider auth** — `chat/toolCallAuthRequired` (MCP contributor),
   `session/inputNeeded{toolAuthentication}` set/remove, `chat/toolCallAuthResolved`;
   provider `-32007`/`auth/required` gating on session create/prompt. files:
   `internal/ahp/server/chat_auth.go`, `internal/ahp/server/chat_auth_test.go`, verify:
   `go test ./internal/ahp/server/...`, parallel-safe: no
8. **Reconnect & replay** — `reconnect{lastSeenServerSeq}` → per-channel replay from ring
   or fresh snapshots on overflow, `missing` URIs, dispose-drop, re-attach to past
   sessions after restart. files: `internal/ahp/server/reconnect.go`,
   `internal/ahp/server/reconnect_test.go`, verify:
   `go test ./internal/ahp/server/...`, parallel-safe: no
9. **CLI wiring** — `pi agent-host` cobra command (flags: `--addr 127.0.0.1:8931`,
   `--token` generated+printed to stderr, `--model`, `--url`, `--header` repeatable,
   `--insecure`), registration in cli.go, `runAgentHost` (signals, err-log, non-fatal
   store open, graceful shutdown). files: `internal/cli/agent_host.go`,
   `internal/cli/agent_host_test.go`, `internal/cli/cli.go`, verify:
   `go build ./... && go test ./internal/cli/...`, parallel-safe: no
10. **Conformance vs official Go client** — integration tests (tag `integration`) driving
    the in-process host with `ahp`/`ahpws` from
    `github.com/microsoft/agent-host-protocol/clients/go` v0.6.0: handshake+negotiation,
    subscribe→snapshot, createSession→prompt→ordered parts/tool-call→turnComplete,
    cancellation, reconnect replay + snapshot fallback, `fetchTurns`, `listSessions`+
    root notifications, authenticate flow, envelope ordering/origin/seq monotonicity.
    dependency already added in slice 1. files:
    `internal/ahp/server/conformance_test.go`, verify:
    `go test -tags integration ./internal/ahp/server/...`, parallel-safe: no

## Execution Model
Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the
**Coordinator**; it delegates rather than implements.

- **Workers**: one `worker` subagent per slice (`quick-task` for a single-file
  mechanical change). No slice is parallel-safe — run strictly one at a time, in order
  (Steps 1–10).
- **Verifier**: after the last slice, a `code-reviewer` subagent checks the Done
  Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to
  10 cycles total.

## Done Criteria
The Verifier checks these against the diff, not against the checklist. Each must
be objectively checkable by reading code or running a command.
- [ ] `internal/ahp/server` exists with `ListenAndServe` serving AHP over WebSocket
      (gorilla/websocket, one JSON-RPC message per text frame) and a token-gated
      HTTP upgrade (401 on bad/missing token) — see `host.go`/`host_test.go`.
- [ ] `initialize` negotiates `protocolVersion` from
      `ahptypes.SupportedProtocolVersions()` (returns `-32005` + `data.supportedVersions`
      when none match) and returns snapshots per `initialSubscriptions` — see
      `connection_test.go`.
- [ ] `listSessions` returns summaries sourced from `pisession.FileService.ListMeta`
      (past sessions included); `subscribe` to `ahp-session://pi-go/<stored-id>` yields a
      `SessionState` snapshot replayed from `events.jsonl` — see `store_test.go`/
      `root_test.go`/`replay_test.go`.
- [ ] A scripted turn produces exactly: `chat/turnStarted` echo (with
      `origin{clientId, clientSeq}`), `chat/responsePart{kind:markdown}` + `chat/delta`
      per text part, `reasoning` part per thought, `chat/toolCallStart→Delta→Ready
      (confirmed:'setting')→Complete` per tool call, then `chat/turnComplete` — see
      `chat_test.go`/`adapter/stream_test.go`/`adapter/toolcall_test.go`.
- [ ] `reconnect{lastSeenServerSeq}` returns missed envelopes in strict `serverSeq`
      order, or fresh snapshots when the gap exceeds the 1024-envelope ring, with
      `missing` for disposed subscriptions — see `reconnect_test.go`.
- [ ] `authenticate` stores a token for a declared protected resource; session
      create/prompt without it returns `-32007` with `data.resources`; MCP tool auth
      surfaces `chat/toolCallAuthRequired` + `session.inputNeeded` and resolves via
      `chat/toolCallAuthResolved` — see `auth_test.go`/`chat_auth_test.go`.
- [ ] `pi agent-host` is a registered cobra command (`internal/cli/cli.go`) with flags
      `--addr/--token/--model/--url/--header/--insecure`, graceful shutdown on
      SIGINT/SIGTERM, err-log file at `$HOME/.pi-go/sessions/agent-host.err.log` — see
      `agent_host_test.go`.
- [ ] Conformance suite (tag `integration`) passes using the official
      `github.com/microsoft/agent-host-protocol/clients/go` client against the
      in-process host — see `conformance_test.go`.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass; `golangci-lint run ./...`
      reports no new issues in `internal/ahp/server`.
- [ ] No slice is left as a stub, TODO, or panic("not implemented").

## Gates
- **build**: `go build ./...`
- **test**: `go test ./...`
- **integration**: `go test -tags integration ./internal/ahp/server/...`
- **vet**: `go vet ./...`
- **lint**: `golangci-lint run ./...`

## Reference
- Design: `.pi-go/tasks/025-vscode-agent-host-protocol/specs/features/TOO/025-vscode-agent-host-protocol/design.md`
- Outline: `.pi-go/tasks/025-vscode-agent-host-protocol/specs/features/TOO/025-vscode-agent-host-protocol/outline.md`
- Plan: `.pi-go/tasks/025-vscode-agent-host-protocol/specs/features/TOO/025-vscode-agent-host-protocol/plan.md`
- Requirements: `.pi-go/tasks/025-vscode-agent-host-protocol/specs/features/TOO/025-vscode-agent-host-protocol/requirements.md`
- Research: `.pi-go/tasks/025-vscode-agent-host-protocol/specs/features/TOO/025-vscode-agent-host-protocol/research/`

## Constraints
- **Never hand-copy AHP wire types** — import `ahptypes` from
  `github.com/microsoft/agent-host-protocol/clients/go` (generated, CI-parity with the TS
  spec). The Go lib is client-only; the server side is implemented here from the spec.
- **Reuse `internal/acp/server` composition as-is** (`RuntimeConfig`, `NewPromptHandler`,
  `Stream`-adapter pattern, `StoreSessionID`, replay walk) — do not create a third
  composition root (repo TODO items 8/14/37 warn about duplicate agent wiring). New
  transport code is required; agent composition is not.
- stdlib `testing` only (no testify — exactly 1 file in the repo uses it); hand-rolled
  mocks; table-driven where shape varies; `t.TempDir()` for stores.
- AHP spec is v0.9.0 DRAFT; tolerate unknown actions/fields (spec: ignore additive
  changes). Every command/notification `params` carries top-level `channel: URI`;
  connection-level commands pin it to literal `ahp-root://`.
- LLM/diagnostic logs go to slog only — stdout/stderr of the host: stderr carries the
  token banner and errors; never emit protocol traffic to logs at info level.
- `go test -race` must stay clean on `internal/ahp/server` (CI excludes
  `internal/acp/server` from race; do not need the same exclusion here).
- Concurrency: serialize turns per chat (mutex precedent `piSessionState.mu`); host
  owns sessions — a dropped client connection must NOT cancel in-flight turns.
- Extension (`vscode/`) is out of scope; do not modify it.
- pi-go module: `github.com/dimetron/pi-go`, Go 1.27; deps available:
  `github.com/gorilla/websocket v1.5.3`, `github.com/coder/websocket v1.8.13` (transitive
  of the Go AHP client), `github.com/coder/acp-go-sdk v0.13.5`,
  `google.golang.org/adk/v2`.