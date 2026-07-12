# Implementation Plan: Codex Direct-Mode App-Server Subagent

## Vertical Slices

- [ ] **Slice 1: Protocol types** — Create `internal/codex/protocol.go` with all JSON-RPC and codex message types (
  JSONRPCRequest/Notification/Response, RPCError, InitializeParams/Response, ThreadStartParams/Response,
  TurnStartParams/Response, ReviewStartParams/Response, TurnInterruptParams, notification param types, Item struct). Add
  `// TODO: future` comments for thread/resume, thread/list, externalAgentConfig/import, config/read, account/read.
  Verify: `go build ./internal/codex/`

- [ ] **Slice 2: JSON-RPC client + binary resolution** — Create `internal/codex/client.go` with `Client` struct that
  spawns `codex app-server` as a subprocess, pipes stdin/stdout, runs a reader goroutine that splits JSONL and routes
  responses (by ID) to pending channels and notifications to a notification channel. Implement `NewClient()` (spawn +
  initialize handshake + `initialized` notify), `request()`, `notify()`, `notifications()`, `close()`. Implement
  `findBinary()` with PATH lookup + default paths, `PI_CODEX_CMD` env var override, `rpcTimeout = 60s`. Verify:
  `go build ./internal/codex/`

- [ ] **Slice 3: Session + event translation** — Create `internal/codex/session.go` with `Event` and `RunResult` types (
  mirroring `sharedacp.Event`/`RunResult` shapes). Implement `Session` struct, `SessionOpts` (CWD, Prompt, Sandbox, Env,
  Review), `NewSession()` (creates Client → initialize → thread/start → starts turn/start or review/start → launches
  notification handler goroutine). The notification handler translates codex notifications into `Event`s on the events
  channel and accumulates agentMessage text into the result. Completion on `turn/completed` (normal), `turn/completed`
  with `interrupted`/`failed` status (from Cancel). Implement `Events()`, `Done()`, `Wait()`, `Cancel()` (sends
  `turn/interrupt` then kills subprocess). Verify: `go build ./internal/codex/`

- [ ] **Slice 4: Dispatch integration** — Create `internal/subagent/spawner_codex.go` with `codexAgentNames` map (
  `"codex"`, `"codex-review"`), `isCodexAgent()`, `codexSession` interface (Events/Done/Cancel/Wait using `codex.Event`/
  `codex.RunResult`), `startCodexSessionFn` var (overridable in tests), `startCodexSession()` (determines sandbox mode +
  review flag from agent name, constructs `codex.SessionOpts`, calls `codex.NewSession()`), `dispatchCodex()` (wraps
  prompt, resolves timeout, calls `startCodexSessionFn`, constructs `*Process` with buffer 256 + cancel closure,
  launches `pumpCodexSession` goroutine), `pumpCodexSession()` (translates `codex.Event` → `subagent.Event`:
  message→text_delta, progress/tool→tool_call, stderr→stderr, error→error; emits synthetic message_start; emits terminal
  message_end with StopReason; no sentinel detection needed). Verify: `go build ./internal/subagent/`

- [ ] **Slice 5: Orchestrator wiring + env allowlist** — Edit `internal/subagent/orchestrator.go`: add `isCodexAgent`
  branch in `Spawn()` between the `isACPAgent` check and the default `Spawner.Spawn`. Change ACP event logging from
  `logACP := isACPAgent(agent.Name)` to `logACP := isACPAgent(agent.Name) || isCodexAgent(agent.Name)` — **never add
  codex to `acpAgentNames`**. Edit `internal/subagent/environ.go`: add `"CODEX_HOME"` to `DefaultEnvAllowlist` (needed
  by codex for config/state). Verify: `go build ./...` and `go test ./internal/subagent/ -run TestSpawn -count=1`

- [ ] **Slice 6: Bundled agent definitions** — Create `internal/subagent/bundled/codex.md` (name: codex, role: default,
  worktree: false, workspace-write tools) and `internal/subagent/bundled/codex-review.md` (name: codex-review, role:
  default, worktree: false, read-only tools). Verify: `go test ./internal/subagent/ -run TestLoadBundled -count=1`

- [ ] **Slice 7: Tests** — Create `internal/codex/client_test.go` (JSON-RPC message marshaling/unmarshaling, response
  routing, notification parsing, binary resolution + env override + not-found error). Create
  `internal/codex/session_test.go` (mock client, test turn lifecycle: events for each notification type, completion on
  turn/completed with matching threadID, **thread-ID filtering**: turn/completed from wrong threadID is NOT terminal,
  Cancel sends turn/interrupt, error notification produces error event, **subprocess crash before turn/completed**
  produces error result). Create `internal/subagent/spawner_codex_test.go` (override `startCodexSessionFn` with
  `fakeCodexSession`, verify `dispatchCodex` constructs `*Process`, `pumpCodexSession` translates events, no sentinel
  used, error results produce error events, requires prompt, unknown agent rejected). Verify:
  `go test ./internal/codex/ ./internal/subagent/ -count=1`

## Verification Commands

Each slice should be verified with:

- **build**: `go build ./...`
- **test**: `go test ./internal/codex/ ./internal/subagent/ -count=1`
- **vet**: `go vet ./internal/codex/ ./internal/subagent/`