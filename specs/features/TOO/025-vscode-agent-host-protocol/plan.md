# Implementation Plan — `pi agent-host` (AHP server)

Module: `github.com/dimetron/pi-go`. New package `internal/ahp/server` + one CLI file.
All slices are vertical (implementation + tests together); each must compile and pass
tests independently. Verification command per slice. **No slice is parallel-safe** (all
share `internal/ahp/server` and are strictly layered) — run one worker at a time, in order.

Conventions: stdlib `testing` only, hand-rolled mocks, table-driven where shape varies,
`t.TempDir()` stores, `t.Chdir` when needed, `slog` for logs (never to stdout of the host).
Wire types always imported from `ahptypes` (generated; CI-parity with the TS spec).

Slice index (details in the sections below; /run ticks the detailed `- [ ] Step N`
checkboxes):
1. Wire plumbing — WebSocket + JSON-RPC + state manager
2. Auth & error codes
3. Session store bridge & root catalog
4. ACP bridge — session lifecycle
5. Chat turn flow
6. Tool-call state machine
7. Auth-required tool & provider auth
8. Reconnect & replay
9. CLI wiring
10. Conformance tests vs official Go client

---

- [ ] Step 1: Wire plumbing — WS server, JSON-RPC framing, state manager
  **Implement:**
  - `internal/ahp/server/host.go`: `Config` (Addr/Token/Model/BaseURL/Headers/Insecure/
    System/SessionService/LoadConfig/SandboxRootFunc/Logger), `NewHost`, `ListenAndServe`
    — HTTP server on `cfg.Addr`, `GET /ahp` upgrade route, one goroutine per connection
    (dispatch loop `readPump` + `writePump` like jsonrpc handleConn), graceful shutdown
    (close listener, per-session `cleanup`).
  - `internal/ahp/server/connection.go`: per-conn struct; routing by `(method, params.channel)`;
    methods in this slice: `initialize` (version negotiation via
    `ahptypes.SupportedProtocolVersions()`, returns `protocolVersion`, `serverSeq`,
    `serverInfo`, snapshots per `initialSubscriptions`, `defaultDirectory`),
    `ping` (respond even pre-initialize), `subscribe` (result `{snapshot?}`; stateless
    channels: no snapshot), `unsubscribe` (notification).
  - `internal/ahp/server/common.go`: `BaseParams` handling helpers — validate every
    command/notification `params.channel` (URI parse; `ahp-root://` only for
    connection-level); method-unknown → `-32601`; bad params → `-32602`.
  - `internal/ahp/server/state.go` — `StateManager`: per-channel current state
    (`RootState`/`SessionState`/`ChatState`/`TerminalState`), `Dispatch` applies pure
    reducer, stamps global monotonic `serverSeq`, appends to per-channel replay ring
    (cap 1024), emits `action` notifications to all subscribers of that channel;
    `Snapshot(uri)` materializes state; `Replay(since)` = replay list or fresh snapshots
    if gap > buffer; `origin{clientId, clientSeq}` on echoes; `rejectionReason` echo for
    invalid client actions (silent-ignore for unknown channels, per spec).
  - Files: `internal/ahp/server/{host.go, connection.go, common.go, state.go}`,
    `internal/ahp/server/{host_test.go, connection_test.go, state_test.go}`.
  - **Verify:** `go build ./... && go test ./internal/ahp/server/...`
  - Depends on: nothing. Parallel-safe: **no**.

- [ ] Step 2: Auth & error codes
  - `internal/ahp/server/auth.go`: `AuthManager` — handshake token check (`?tkn=` query or
    `Authorization: Bearer`), provider-token store (per `resource`), `authenticate`
    command handler (validates resource exists, stores token, no echo), `auth/required`
    notification path, `ProtectedResourceMetadata` advertisement in `RootState`.
  - `internal/ahp/server/errors.go`: AHP error-code helpers (`NewSessionNotFound`,
    `NewSessionAlreadyExists`, `NewTurnInProgress`, `NewUnsupportedProtocolVersion`,
    `NewAuthRequired`, `NewProviderNotFound`, `NewNotFound`, `NewPermissionDenied`) each
    embedding the standard `-326xx` shape with AHP `data` payloads.
  - Tests: token accept/reject (query + header), protected-resource gating, error-code data
    shapes (table-driven).
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Step 1. Parallel-safe: **no**.

- [ ] Step 3: Session store bridge — catalog + history replay
  - `internal/ahp/server/store.go`: `SessionStore` interface + `FileSessionStore` over
    `pisession.FileService` (constructor `NewFileService`); `SessionSummary` mapping
    (id/cwd/title/provider/model/updatedAt/live); hostile-id handling via
    `acpserver.StoreSessionID`-equivalent hashing (local impl to avoid import cycle —
    `storeID(ahpSessionURI) string`).
  - `internal/ahp/server/root.go`: `listSessions` request handler (reads
    `SessionStore.List`, newest-first; optional `cwd` filter; returns `SessionSummary[]`);
    `root/sessionAdded|Removed|sessionSummaryChanged` notifications on create/dispose;
    `fetchTurns` command (pages history into `ChatState.turns` before cursor).
  - `internal/ahp/server/replay.go`: `replayEvents(ctx, iter.Seq[*adksession.Event],
    emitFn)` — ports `internal/acp/server/replay.go` walk to AHP part shapes (user→
    `chat/responsePart`? no — replay emits *messages* into `Turn` state, not live actions;
    skip `Partial`; FunctionCall→completed tool call; FunctionResponse→paired Complete by
    id else last-call-by-name; text→markdown part; thought→reasoning part).
  - Tests: seeded `t.TempDir()` store with fixture events (message, thought, tool call+
    response pairs, compaction event, partial event) asserting exact replay sequence.
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Step 2. Parallel-safe: **no**.

- [ ] Step 4: ACP bridge — session lifecycle
  - `internal/ahp/server/bridge.go`: `Bridge` interface + `acpBridge` impl wrapping
    `acpserver.RuntimeConfig` (reuse as-is; constructor shared with acp_server.go
    wiring): per-URI `piSessionState` cache (mutex, first-prompt-lazy init copying
    `runtime.go initPiSessionState` behavior: LoadFrom(cwd), LLM build, sandbox, tools,
    MCP toolsets, subagent orchestrator, LSP manager, auto-compact hook, `OpenSession`
    under `StoreSessionID`-mapped id); `Dispose` runs full `cleanup()` (bash kill-all,
    LSP/orchestrator shutdown, sandbox close) — fixing the ACP-server disposal gap.
  - `internal/ahp/server/session.go`: `createSession` (validate URI unused → else
    `-32003`; provider default `"pi-go"`; workingDirectories validation — >1 without
    `multipleWorkingDirectories` capability ⇒ use first, ignore rest per spec; `activeClient`
    eager claim), `session/ready` / `session/creationFailed` async init, `disposeSession`
    cascade; `SessionState` reducer + snapshot.
  - Tests: create/dup-error/ready lifecycle; dispose runs cleanup (spy on injected cleanup).
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Step 3. Parallel-safe: **no**.

- [ ] Step 5: Chat turn flow
  - `internal/ahp/server/chat.go`: `createChat` (session-scoped; default chat exists —
    v1 single-chat-per-session), `ChatState` snapshot/reducers, turn dispatch validation
    (only `Message.kind:'user'`), `chat/turnStarted` client W-A handling with optimistic
    echo (`origin{clientId, clientSeq}`) or `rejectionReason` (invalid kind/nonexistent
    chat), prompt execution via bridge `chatHandle` calling the acp `PromptHandler`
    with AHP-`SessionUpdater` (this slice's adapter) as the `SessionUpdater` arg.
  - `internal/ahp/server/adapter/stream.go`: `Stream.OnEvent` — ADK event walk mapping:
    text part → `chat/responsePart{kind:markdown,id}` then `chat/delta{partId,content}`
    (accumulate finalText; `agent.StreamDedup` precedent for delta-vs-aggregate dedup);
    thought part → `reasoning` part + deltas; role user ignored; final `turnComplete
    {duration?}` on nil error; ctx-cancel → `turnCancelled{duration}`; provider error →
    `chat/error{resumable:false}` + `TurnState.Error` end.
  - Tests: scripted ADK event streams → exact ordered action sequence (table-driven);
    cancel mapping; error mapping; W-A echo/reject.
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Step 4. Parallel-safe: **no**.

- [ ] Step 6: Tool-call state machine
  - `internal/ahp/server/adapter/toolcall.go`: `OnToolStart` → `chat/toolCallStart
    {toolCallId, toolName, displayName}` + `toolCallDelta` (partial args streaming) +
    `toolCallReady{invocationMessage, toolInput, confirmed:'setting'}` (auto-approve
    posture; contributor nil = server-origin); `OnToolEnd` → `chat/toolCallComplete
    {result{success, pastTenseMessage, content:[text], error?}}` (Failed status carries
    `error{message}`); status transitions in `ChatState.activeTurn`.
  - Tests: table-driven over tool kinds (read/grep/edit/bash/subagent mapping from
    `ToolKind` precedent); ordering incl. nested subagent flattening (parent-card text
    lines like ACP adapter); failure shape.
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Step 5. Parallel-safe: **no**.

- [ ] Step 7: Auth-required tool & provider auth
  - `internal/ahp/server/chat_auth.go`: `chat/toolCallAuthRequired` action (from
    `running`, MCP-contributed tools), `session/inputNeeded` set/remove actions,
    `chat/toolCallAuthResolved` on successful `authenticate`; route to chat channel.
  - `auth.go` extension: provider-level `AuthRequired` — session create/prompt with
    unauthenticated provider returns `-32007` with `data.resources`; `auth/required`
    notification dispatch; token storage per `resource`; revalidation on next turn.
  - Tests: auth flow with mock protected resource; rejection pre-auth.
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Steps 2, 6. Parallel-safe: **no**.

- [ ] Step 8: Reconnect & replay
  - `internal/ahp/server/reconnect.go`: `reconnect` handler (rebind `clientId`,
    `lastSeenServerSeq`, per-subscription replay vs snapshot decision); `missing` list;
    post-reconnect continuation; drop of disposed-session subscriptions.
  - Tests: replay-in-order, gap-overflow→snapshot, missing disposed session, re-attach to
    stored past session after host "restart" (new StateManager, same store).
  - **Verify:** `go test ./internal/ahp/server/...`
  - Depends on: Steps 1, 3, 5. Parallel-safe: **no**.

- [ ] Step 9: CLI wiring — `pi agent-host`
  - `internal/cli/agent_host.go`: `newAgentHostCmd()` (cobra; `Use: "agent-host"`,
    `Args: cobra.NoArgs`; flags `--addr` default `127.0.0.1:8931`, `--token` default
    generated, `--model`, `--url`, `--header` repeatable, `--insecure` — mirror
    acp_server.go flag pattern); `runAgentHost`: `signal.NotifyContext(os.Interrupt)`,
    err-log at `$HOME/.pi-go/sessions/agent-host.err.log` (same logHandler),
    `openServerSessionStore` reuse, generated-token banner to stderr, `ahpserver.NewHost`
    + `ListenAndServe`; error wrap `"agent host: %w"`.
  - `internal/cli/cli.go:265-275`: add `cmd.AddCommand(newAgentHostCmd())`.
  - Tests: flag defaults; token generation; graceful shutdown (signals).
  - **Verify:** `go build ./... && go test ./internal/cli/...`
  - Depends on: Step 4 (composition), independent of 5–8. Parallel-safe: **no**.

- [ ] Step 10: Conformance vs official Go client
  - `internal/ahp/server/conformance_test.go` (build tag `integration`): spin host in
    process on ephemeral port; drive with `ahp.Connect` + `ahpws.Connect` (dep:
    `github.com/microsoft/agent-host-protocol/clients/go` v0.6.0); scenarios:
    (a) initialize negotiation + snapshots, (b) subscribe→snapshot of stored session,
    (c) createSession → `chat/turnStarted` dispatch → prompt → ordered
    responsePart/delta/toolCall actions → turnComplete, (d) toolCallConfirmed rejection
    semantics, (e) cancel, (f) reconnect replay + snapshot fallback, (g) fetchTurns
    pagination, (h) listSessions + root notifications, (i) authenticate w/ protected
    resource, (j) `ping`. Assert: envelope ordering, `origin` echoes, seq monotonicity.
  - **Verify:** `go test -tags integration ./internal/ahp/server/...`
  - Depends on: Steps 1–9. Parallel-safe: **no**.

## Verification commands (project gates)

- build: `go build ./...` (Makefile `make build` builds `./cmd/pi` + `./cmd/pi-sandbox`)
- test: `go test ./...` (make test); targeted: `go test ./internal/ahp/server/...`
- integration: `go test -tags integration ./internal/ahp/server/...`
- vet: `go vet ./...`
- lint: `golangci-lint run ./...`

## Dependencies & ordering

Strictly sequential: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10.
Step 9 depends on 4 only (could run after 4), but shares `internal/cli` conventions —
keep sequential for simplicity. Step 10 requires all prior.

## Non-goals (deferred, per requirements.md Q3/Q4)

Terminal channel, automation channels, changesets, client-contributed tools routing
(`session/activeClientSet` tool routing), plugins/customizations, `completions`,
multi-chat per session (default chat only), multi-client concurrent turn arbitration
(single driving client; read-only second clients get sync via same machinery), stdio/
Unix-socket AHP transports.