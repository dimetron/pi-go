# Outline: Codex Direct-Mode App-Server Subagent

## Slices (ordered)

1. **Protocol types** — `internal/codex/protocol.go`: JSON-RPC request/response/notification types,
   initialize/thread/turn/review/interrupt params and responses, item types, notification params
2. **JSON-RPC client** — `internal/codex/client.go`: `Client` struct spawning `codex app-server`, reader goroutine
   routing responses by ID + notifications to channel, `request()`, `notify()`, `close()`, `findBinary()`,
   `PI_CODEX_CMD` env var
3. **Session + event translation** — `internal/codex/session.go`: `Session` struct, `NewSession()` (initialize →
   thread/start → start turn/review), notification handler translating to `codex.Event`, `Events()`, `Done()`, `Wait()`,
   `Cancel()`
4. **Dispatch integration** — `internal/subagent/spawner_codex.go`: `codexAgentNames`, `isCodexAgent()`, `codexSession`
   interface, `dispatchCodex()`, `startCodexSession()`, `startCodexSessionFn`, `pumpCodexSession()`
5. **Orchestrator wiring** — `internal/subagent/orchestrator.go`: third branch in `Spawn()`, ACP event logging for codex
   agents
6. **Bundled agents** — `internal/subagent/bundled/codex.md` + `codex-review.md`
7. **Tests** — `internal/codex/client_test.go`, `internal/codex/session_test.go`,
   `internal/subagent/spawner_codex_test.go`

## Key Type Signatures (C-header style)

```go
// internal/codex/protocol.go
type JSONRPCRequest struct { JSONRPC string; ID int; Method string; Params interface{} }
type JSONRPCNotification struct { JSONRPC string; Method string; Params json.RawMessage }
type JSONRPCResponse struct { JSONRPC string; ID int; Result json.RawMessage; Error *RPCError }
type RPCError struct { Code int; Message string }

// internal/codex/client.go
type Client struct { ... }
func NewClient(ctx context.Context, cwd string, env []string) (*Client, error)
func (c *Client) request(method string, params interface{}) (json.RawMessage, error)
func (c *Client) notify(method string, params interface{}) error
func (c *Client) notifications() <-chan JSONRPCNotification
func (c *Client) close() error
func findBinary(paths []string) (string, error)

// internal/codex/session.go
type Event struct { Type string; Content string; Error string; SessionID string }
type RunResult struct { Status string; Result string; Error string; SessionID string; Stderr string; StopReason string }
type Session struct { ... }
type SessionOpts struct { CWD string; Prompt string; Sandbox string; Env []string; Review bool }
func NewSession(ctx context.Context, opts SessionOpts) (*Session, error)
func (s *Session) Events() <-chan Event
func (s *Session) Done() <-chan struct{}
func (s *Session) Wait() RunResult
func (s *Session) Cancel() error

// internal/subagent/spawner_codex.go
var codexAgentNames = map[string]struct{}{"codex": {}, "codex-review": {}}
func isCodexAgent(name string) bool
type codexSession interface { Events() <-chan codex.Event; Done() <-chan struct{}; Cancel() error; Wait() codex.RunResult }
var startCodexSessionFn = startCodexSession
func dispatchCodex(ctx context.Context, opts SpawnOpts, agentName string) (*Process, error)
func startCodexSession(ctx context.Context, agentName string, prompt string, opts SpawnOpts) (codexSession, error)
func pumpCodexSession(sess codexSession, proc *Process, agentName string)
```