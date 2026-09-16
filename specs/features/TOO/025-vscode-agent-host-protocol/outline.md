# Outline — `pi agent-host` (AHP server)

New package `internal/ahp/server` + one CLI file. Slices are vertical; each compiles + tests green.

## Key types introduced (header view)

```go
// wiretypes (via ahptypes import — never hand-copy)
// internal/ahp/server/host.go
type Config struct{ Addr, Token, Model, BaseURL string; Headers []string; Insecure bool;
    System string; SessionService adksession.Service; LoadConfig func() (config.Config, error);
    SandboxRootFunc func(string) string; Logger *slog.Logger }
type Host struct{...}; func NewHost(cfg Config) *Host
func (h *Host) ListenAndServe(ctx context.Context) error   // WS + per-conn loops

// state.go
type StateManager struct{...}
func (m *StateManager) Snapshot(uri ahp.URI) (ahptypes.Snapshot, bool)
func (m *StateManager) Dispatch(ctx context.Context, chans []ahp.URI, a ahptypes.StateAction)
func (m *StateManager) Replay(since map[ahp.URI]int64) ([]ahptypes.ActionEnvelope, []ahp.URI)

// bridge.go
type Bridge interface {
    CreateSession(ctx context.Context, uri ahp.URI, p CreateParams) (*sessionHandle, error)
    RestoreSession(ctx context.Context, uri ahp.URI, storeID string) (*sessionHandle, error)
    Dispose(uri ahp.URI) error
}
type sessionHandle struct{ ACPID string; DefaultChat ahp.URI; Chats map[ahp.URI]*chatHandle }
type chatHandle struct{ Cancel(context.Context) bool; /* prompt via acp PromptHandler */ }

// adapter/stream.go — ADK event → AHP chat actions
type SessionUpdater struct{...}
func (s *SessionUpdater) OnEvent(ctx context.Context, ev *adksession.Event) error
func (s *SessionUpdater) OnToolStart(ctx context.Context, name, args string) (string, error)
func (s *SessionUpdater) OnToolEnd(ctx context.Context, id, args, result string, runErr error) error
func (s *SessionUpdater) OnBashOutput(ctx context.Context, out string) error

// store.go — over pisession.FileService
type SessionStore interface{ List(ctx) ([]pisession.Meta, error);
    Load(ctx, id string) (iter.Seq[*adksession.Event], func() (int64, error), error) }

// auth.go
type AuthManager struct{...}; func (a *AuthManager) Handshake(r *http.Request) error
func (a *AuthManager) OnAuthenticate(ctx context.Context, p AuthenticateParams) error
```

## Slices (order = build order)

1. **Wire plumbing** — WS server (gorilla), JSON-RPC 2.0 framing (1 msg/frame), token
   handshake, `ping`/`initialize`/`subscribe`/`unsubscribe`/`reconnect` + envelope
   broadcast machinery; `StateManager` (reducers for in-scope actions, seq, 1024 ring).
   files: `internal/ahp/server/{host.go,connection.go,common.go,state.go}` +
   `host_test.go`,`state_test.go`. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
2. **Auth & errors** — `authenticate` cmd, `auth/required` notif, `-32005/-32007/-32001/-32003`
   codes w/ data, protectedResources advertisement, rejection-echo semantics.
   files: `auth.go`, `errors.go` + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
3. **Session store bridge** — `SessionStore` over `FileService` (List/Load + meta), session
   catalog RPCs `listSessions`, `root/session*` notifications, `fetchTurns`, replay-walk of
   JSONL → `Turn[]` for restored sessions (port of `acp/server/replay.go` logic).
   files: `store.go`,`root.go` + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
4. **ACP bridge: session lifecycle** — `createSession`/`disposeSession` wired to
   `acp/server.RuntimeConfig`+`NewPromptHandler` (reused); `piSessionState` cache w/ `cleanup()`
   on dispose; `session/ready|creationFailed`; `ChatState` snapshot; `SessionStore` mapping
   (hostile-id hashing precedent).
   files: `bridge.go`,`session.go` + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
5. **Chat turn flow** — dispatch `chat/turnStarted` (client W-A) w/ optimistic echo/origin;
   turn exec via bridge PromptHandler; adapter `SessionUpdater` (text/thought parts, deltas,
   `turnComplete/Cancelled/Error`); cancellation → in-flight cancel precedent.
   files: `chat.go`,`adapter/stream.go` + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
6. **Tool-call state machine** — map `extension.BuildToolCallCallbacks` → `toolCallStart/
   Delta/Ready(confirmed:'setting')/Complete`; result content shapes; `session.inputNeeded`
   roll-up for `pending-result-confirmation` passthrough.
   files: `adapter/toolcall.go` + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
7. **Auth-required tool + provider auth** — `chat/toolCallAuthRequired` (MCP contributor),
   `inputNeeded{kind:toolAuthentication}` → `authenticate` → `chat/toolCallAuthResolved`;
   `auth/required` + `-32007` w/ `data.resources` when provider OAuth needed.
   files: `chat_auth.go`, `auth.go` edits + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
8. **Reconnect & replay** — `reconnect{lastSeenServerSeq}` → per-channel replay from ring, or
   fresh snapshots on overflow; `missing` URIs; re-attach to past sessions from store;
   re-subscribe flow; connection-drop keeps sessions alive.
   files: `reconnect.go`, `state.go` edits + tests. verify: `go test ./internal/ahp/server/...`. parallel-safe: no
9. **CLI wiring** — `internal/cli/agent_host.go` (`newAgentHostCmd`: `--addr/--token/--model/
   --url/--header/--insecure`), registration in `cli.go`, run func (signals, err-log file,
   store open non-fatal, generated token banner to stderr).
   files: `internal/cli/agent_host.go` + test. verify: `go build ./... && go test ./internal/cli/...`. parallel-safe: no
10. **Conformance vs official Go client** — integration tests (tag `integration`) using
    `ahp`/`ahpws` clients against in-process host: handshake, subscribe→snapshot,
    createSession→prompt→parts→toolCall sequence, cancel, reconnect replay, authenticate,
    fetchTurns, listSessions. Fix wire drift found here.
    files: `conformance_test.go` (+ go.mod dep). verify: `go test -tags integration ./internal/ahp/server/...`. parallel-safe: no

Sequencing notes: 1→8 strictly sequential (same package, layered). 9 needs 4 (composition)
but not 5–8. 10 needs all. Single worker throughout is fine; no parallelizable slice pairs
share no files.

Est. ~10 slices · ~14 files · fits one spec; each slice ≤ ~400 changed lines.