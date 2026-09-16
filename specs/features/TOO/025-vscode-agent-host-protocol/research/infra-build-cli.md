# pi-go Infrastructure — JSON-RPC, Web Server, CLI, Build/Test

## 1. JSON-RPC infrastructure: `internal/jsonrpc` and `internal/pirpc`
Two **independent** RPC servers exist; neither shares framing/dispatch code with the other, and **neither is reusable for a WebSocket server without writing a new transport layer** — each is hard-coupled to its transport.

### `internal/jsonrpc` — Unix-socket JSON-RPC 2.0 server
- **Framing**: newline-delimited JSON over `net.Conn`, shared `json.Decoder` per connection (`internal/jsonrpc/rpc.go:143-175`, `handleConn` loops `dec.Decode(&req)`).
- **Types**: `Request{JSONRPC, Method, Params, ID}` (:22-27), `Response{JSONRPC, Result, Error, ID}` (:30-35), `Error{Code, Message}` (:38-41), `Event{Type, Agent, Role, Delta, Content, ToolName, ToolInput}` streaming JSONL event (:44-52).
- **Routing**: hard `switch req.Method` in `handleConn` — `"prompt"`, `"session.create"`, `"session.list"`, default → `-32601 method not found` (:160-173). No method-registry abstraction.
- **Transport**: **Unix domain socket only.** `Server.Run` does `net.Listen("unix", s.socketPath)`, stale-socket removal, SIGTERM/SIGINT graceful shutdown (:97-139). `Config{Agent *agent.Agent; SocketPath string}` (:67-70).
- **Concurrency**: `runMu sync.Mutex` serializes `agent.Run` (ADK runner not concurrent-safe; :78-83, locked for whole stream in `streamRun`, :224-234).
- **Coupling**: `handleConn` takes `net.Conn` but framing + streaming `Event` schema are baked into the server struct — no pluggable transport/handler interface. A WS server reuses only the wire shapes.

### `internal/pirpc` — stdio NDJSON compatibility facade
- Purpose: upstream pi's `--mode rpc` protocol for the external `pi-acp` adapter (Zed/GoLand). Package doc: "a compatibility facade, not pi-go's preferred integration path" (rpc.go:1-8).
- **Framing**: NDJSON on stdin via `bufio.Scanner` (32 MiB line cap `maxCommandBytes = 32 << 20`, :149,157-159) — Scanner chosen so a malformed line is recoverable.
- **Envelope**: NOT JSON-RPC 2.0. Commands are `{"type","id",...}` union struct `command` (:121-135); exactly one `response{Type:"response", ID, Command, Success, Data, Error}` per command (:138-145). Other stdout lines are async events (`agent_start`, `message_update`, `tool_execution_start/end`, `agent_end`, `agent_settled`).
- **Dispatch**: `switch cmd.Type` (:188-257) — `prompt` (goroutine so `abort` stays readable), `abort`, `get_state`, `get_available_models`, `get_session_stats`, `get_commands`, `get_messages`, `set_model`, plus accepted-noop `set_thinking_level`/`set_follow_up_mode`/`set_steering_mode`/`set_auto_compaction`/`set_session_name`/`switch_session`/`compact`; `export_html` rejected; default unknown-command error.
- **Transport**: `Config{Agent, SessionID, In io.Reader, Out io.Writer, Log, Model, ModelSwitcher}` (:61-79) — stdio-coupled by design.
- **Wiring**: both selected in `internal/cli/cli.go:1145` (`--mode socket` → `jsonrpc.NewServer`) and `:1151` (`--mode rpc` → `pirpc.NewServer`).

## 2. `internal/webserver` today
HTTP + WebSocket web terminal (xterm.js, pairing-code auth).
- `ServerV2` (server.go:22-40) wraps `*http.Server` with **`gorilla/websocket` v1.5.3** (go.mod). `NewServerV2(cfg Config)`; `Start()` does `net.Listen("tcp", cfg.Addr)` + serve goroutine (:106-121). `Shutdown` closes PTY pool, sessions, http server (:132-136).
- **Routes** (`setupRoutes`, :79-103): `GET /pair`, `POST|GET /api/pair`, `POST /api/pair/submit`, `GET /api/status`, `GET /` (index), **`GET /ws/`** (WS terminal), voice endpoints (`/api/voice/config`, `/api/voice/sessions[/{id}]`, `/api/voice/gemini/ws`), `GET /static/` (embedded, overridable `cfg.StaticDir`).
- **Port**: `DefaultAddr = "127.0.0.1:8765"` (handlers.go:43); `Config.Addr`/`pi serve --addr`; port 0 supported — bound address recorded in `listenAddr` (:29,112). HTTP timeouts Read 30s/Idle 120s (:71-72).
- **Auth/token**: 6-digit pairing flow (pairing.go). `PairingManager`: `pending` (code→PendingPair) + `approved` (token→ApprovedPair) (:66-82). `POST /api/pair` returns only the code (:193-196); browser posts to `/api/pair/submit` → `pi_token` HttpOnly SameSite=Strict cookie (24h). WS auth (`handleWebSocket`, :313-341): session ID in path + `pairingToken(r)` (cookie **or** `?token=` query, :358-365), then `websocket.Upgrader{CheckOrigin: checkSameOrigin}` + PTY attach. Anti-brute-force: 3 wrong codes → **server shutdown** via `LockedOut()` channel (pairing.go:45-63); `pi serve` selects on it and exits.
- **CLI**: `runServe` (internal/cli/serve.go:57-120) — `webserver.Config`, `server.Start()`, `server.BootstrapPair(project)` banner; SIGINT/SIGTERM or lockout ends it.

## 3. CLI command registration (cobra)
Root `newRootCmd()` at `internal/cli/cli.go:110` (`Use: "pi [prompt]"`). Subcommands wired at **cli.go:265-275**:
```go
cmd.AddCommand(newPingCmd())
cmd.AddCommand(newAuditCmd())
cmd.AddCommand(newServeCmd())
cmd.AddCommand(newMemoryCmd())
cmd.AddCommand(newModelCmd())
cmd.AddCommand(newLoginCmd())
cmd.AddCommand(newACPServerCmd())
cmd.AddCommand(newA2AServerCmd())
cmd.AddCommand(newUpgradeCmd())
cmd.AddCommand(newSessionStatsCmd())
cmd.AddCommand(newVerifyCmd())
```
Pattern (from `internal/cli/acp_server.go:15-33`): one file per command in `internal/cli/`, exporting an unexported `newXCmd() *cobra.Command` + package-level `flagX...` vars + `runX(cmd *cobra.Command, args []string) error` `RunE`. New subcommand = new file with `newFooCmd()` + one `cmd.AddCommand(newFooCmd())` line.

## 4. Build / test / vet commands (project's real commands)
**Makefile**:
- Build: `make build` → `go build -v -ldflags "-X github.com/dimetron/pi-go/internal/cli.BuildTag=$(git rev-parse --short HEAD)" ./cmd/pi` + `go build -v ./cmd/pi-sandbox` (after `cache-clean`: `golangci-lint cache clean`).
- Test: `make test` = `make test-unit` = **`go test ./...`**; `test-integration` = `go test -tags integration ./...`; `test-e2e` = `go test -tags e2e ./...`; coverage = `go test -coverprofile=coverage.out -coverpkg=./internal/... ./internal/...`.
- Lint: `make lint` = **`golangci-lint run ./...`**; vet: `make vet` = **`go vet ./...`**.
- Vuln gate: `govulncheck -format json ./... | go run ./hack/vulngate`.

**CI** (`.github/workflows/ci.yml`): lint (golangci-lint-action v9.3.0, v2.13), vulncheck, `test` → `go test -count=1 ./...` then `go test -race -count=1 $(go list ./... | grep -v '/internal/acp/server$')`; `test-windows` → `go test -count=1 ./...` (no race/CGO); coverage → Codecov; build matrix (linux/windows/darwin × amd64/arm64, excl. windows/arm64) → `go build -o pi ./cmd/pi`.

**`.golangci.yml`**: v2; errcheck, govet (enable-all minus fieldalignment/shadow), ineffassign, staticcheck, unused, bodyclose, copyloopvar, durationcheck, errname, errorlint, fatcontext, misspell, nilerr, revive, unconvert, wastedassign; gofmt+goimports (local-prefix `github.com/dimetron/pi-go`); complexity linters disabled; relaxed for `_test.go`.

## 5. Test conventions
- **stdlib `testing` only; no testify** — 542 `_test.go` files, exactly **1** imports testify (`internal/acp/server/commands_test.go`, `assert.Equal`). Elsewhere `t.Fatalf`/`t.Errorf`, `t.Helper()`, `t.TempDir()`, `t.Chdir`.
- **Table-driven** where input/output varies (e.g. `internal/config/config_test.go:93-99`, `:1151-1157`); RPC servers use scenario-style one-func-per-behavior.
- **Hand-rolled mocks**: `internal/jsonrpc/rpc_test.go:17-53` `mockLLM`/`errLLM` implementing `model.LLM`, `newTestAgent(t, response) *agent.Agent`.
- Example styles: `internal/jsonrpc/rpc_test.go` (real Unix socket in t.TempDir); `internal/pirpc/rpc_test.go` (drives `Server.dispatch` directly); `internal/config/config_test.go` (table-driven).
- CI excludes `internal/acp/server` from `-race`.

## Consequence for the AHP host
A WebSocket AHP host needs **new** transport/framing code (none of the three existing servers is reusable as-is), but can copy patterns: `jsonrpc` (JSON-RPC 2.0 shapes), `webserver` (gorilla/websocket upgrader + token auth + graceful shutdown), `acp_server.go` (cobra subcommand pattern), `acp/server/runtime.go` (agent composition — likely reusable via `NewPromptHandler`/`RuntimeConfig` as-is, since the AHP bridge can drive the same `PromptHandler` interface the ACP server drives).