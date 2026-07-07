# Research — `google.golang.org/adk` v1.4.0 Usage Surface in pi-go

> **Scope:** objective fact-finding. No design opinions, no migration plan.
> See companion files for the v2.0.0 API delta and the call-site inventory.

## 1. Inventory method

Two automated searches across the repo (excluding `tmp/`, `coverage/`, `*.out`):

```bash
# All import statements of the ADK module
rg 'google\.golang\.org/adk' --glob '**/*.go'
# All call sites that match the v1.4 → v2.0 surface changes we expect
rg 'session\.NewEvent\(|session\.NewEventWithContext\(|agent\.ToolContext|agent\.CallbackContext|agent\.ReadonlyContext|agent\.InvocationContext|agent\.Context\b|llmagent\.|runner\.|functiontool\.|mcptoolset\.|toolconfirmation\.' --glob '**/*.go'
```

**Results:**

- **160 import lines** across 50+ files importing `google.golang.org/adk/...`.
- **123 call-site matches** for the API surface that v2.0.0 changes or is at risk of changing.
- **0 direct call sites** of `session.NewEvent(...)` or `session.NewEventWithContext(...)` in our code.

## 2. ADK sub-packages used in this repo

Pulled from the import grep. Each row has the count of import lines, the
file(s) that use it, and a one-line note on usage shape.

| Sub-package                       | # imports | Files (representative)                                                                                  | Usage shape in pi-go                                                                                          |
|-----------------------------------|----------:|---------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| `agent`                           |        32 | `internal/agent/agent.go`, `internal/extension/hooks.go`, all `internal/tools/*`                          | `agent.Agent` interface, `agent.InvocationContext`, `agent.ReadonlyContext`, `agent.ToolContext`, `agent.CallbackContext` |
| `agent/llmagent`                  |         4 | `internal/agent/agent.go`, `internal/extension/hooks.go`, `internal/lsp/hooks.go`, `internal/tools/compactor.go` | `llmagent.New(Config)`, `llmagent.BeforeToolCallback`, `llmagent.AfterToolCallback`, `llmagent.BeforeModelCallback`, `llmagent.AfterModelCallback` |
| `model`                           |        33 | `internal/provider/*`, `internal/guardrail/model_wrapper.go`, `internal/tui/types.go`                    | `model.LLM`, `model.LLMRequest`, `model.LLMResponse`                                                          |
| `model/gemini`                    |         1 | `internal/provider/gemini.go`                                                                           | Gemini model wrapper (used directly, not the genai package)                                                    |
| `session`                         |        24 | `internal/session/store.go`, `internal/session/branch.go`, `internal/atif/convert.go`, `internal/acp/...` | `session.Service`, `session.Session`, `session.ReadonlyState`, `session.Event`, `session.EventActions`, `session.ReadonlyState` |
| `tool`                            |        38 | `internal/tools/*`, `internal/palace/*`, `internal/extension/mcp.go`, `internal/memory/worker.go`        | `tool.Tool`, `tool.Toolset`, `tool.Predicate`                                                                  |
| `tool/functiontool`               |         9 | `internal/tools/registry.go`, all `internal/palace/tool_*.go`                                            | `functiontool.New(Config)`, `functiontool.Func[TArgs, TResults]`                                               |
| `tool/mcptoolset`                 |         2 | `internal/extension/mcp.go`, `internal/extension/mcp_test.go`                                            | `mcptoolset.New(mcptoolset.Config{…})`                                                                          |
| `tool/toolconfirmation`           |         2 | `internal/tools/tool_invoke_test.go`, `internal/palace/tool_invoke_test.go`, `internal/extension/hooks_test.go` | `toolconfirmation.ToolConfirmation` (struct, no methods on it)                                                  |
| `memory`                          |         1 | `internal/palace/tool_invoke_test.go`                                                                   | `memory.SearchResponse` (in test only)                                                                         |
| `runner`                          |         1 | `internal/agent/agent.go`                                                                               | `runner.New(runner.Config{…})`                                                                                  |

**`session/database` and `session/vertexai`:** 0 imports.
**`agent/workflowagents` and `agent/remoteagent`:** 0 imports.
**`artifact`:** 0 imports.
**`server`:** 0 imports.

## 3. v1.4.0 reference types from the local module cache

Source: `/Users/dimetron/go/pkg/mod/google.golang.org/adk@v1.4.0/...`
(verified by `grep`/`cat` of the cached module).

### `agent` package (v1.4.0)

#### `agent.InvocationContext` interface

```go
type InvocationContext interface {
    context.Context
    Agent() Agent
    Artifacts() Artifacts
    Memory() Memory
    Session() session.Session
    InvocationID() string
    Branch() string
    UserContent() *genai.Content
    RunConfig() *RunConfig
    EndInvocation()
    Ended() bool
    WithContext(ctx context.Context) InvocationContext
}
```

#### `agent.ReadonlyContext` interface

```go
type ReadonlyContext interface {
    context.Context
    UserContent() *genai.Content
    InvocationID() string
    AgentName() string
    ReadonlyState() session.ReadonlyState
    UserID() string
    AppName() string
    SessionID() string
    Branch() string
}
```

#### `agent.CallbackContext` interface

```go
type CallbackContext interface {
    ReadonlyContext
    Artifacts() Artifacts
    State() session.State
}
```

#### `agent.ToolContext` interface

```go
type ToolContext interface {
    CallbackContext
    FunctionCallID() string
    Actions() *session.EventActions
    SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error)
    ToolConfirmation() *toolconfirmation.ToolConfirmation
    RequestConfirmation(hint string, payload any) error
}
```

### `agent/llmagent` package (v1.4.0) — callback types

```go
type BeforeModelCallback func(ctx agent.CallbackContext, llmRequest *model.LLMRequest) (*model.LLMResponse, error)
type AfterModelCallback  func(ctx agent.CallbackContext, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error)
type BeforeToolCallback   func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error)
type AfterToolCallback    func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error)
```

Note: `tool.Context` is a **type alias** for `agent.ToolContext` in v1.4.0
(`type Context = agent.ToolContext` in `tool/tool.go`, marked `Deprecated`).

### `session.NewEvent` (v1.4.0)

```go
// NewEvent creates a new event defining now as the timestamp.
func NewEvent(invocationID string) *Event {
    return &Event{
        ID:           uuid.NewString(),
        InvocationID: invocationID,
        Timestamp:    time.Now(),
        Actions:      EventActions{StateDelta: make(map[string]any), ArtifactDelta: make(map[string]int64)},
    }
}
```

`NewEventWithContext` does **not exist** in v1.4.0. The README-v2.md mention
of "the temporary `NewEventWithContext` helper" refers to the v2.0 dev cycle
where it was briefly introduced and then removed.

### `tool/functiontool` (v1.4.0)

```go
type Func[TArgs, TResults any] func(tool.Context, TArgs) (TResults, error)
type Config struct { Name, Description string; … }
func New(cfg Config) (tool.Tool, error)
```

### `tool/mcptoolset` (v1.4.0)

```go
type Config struct { … }
func New(cfg Config) (tool.Toolset, error)
```

### `tool/toolconfirmation` (v1.4.0)

```go
type ToolConfirmation struct { … }      // struct only
func OriginalCallFrom(*genai.FunctionCall) (*genai.FunctionCall, error)
```

### `runner` (v1.4.0)

```go
type Config struct { … }
type PluginConfig struct { … }
type RunOption func(*runOptions)
func WithStateDelta(delta map[string]any) RunOption
func New(cfg Config) (*Runner, error)
```

### `model/llm` (v1.4.0)

The diff between v1.4.0 and v2.0.0 of this file is empty at the
`^(type|func [A-Z])` grep level. The `model` package is **unchanged** in
v2.0.0 (it carries over as `google.golang.org/adk/v2/model`).

### `model/gemini` (v1.4.0)

Same package layout (one file plus tests) in v2.0.0. **No public API
changes** observed.

## 4. Hand-rolled mock context fakes in this repo

These are the in-test fakes that implement `agent.ToolContext` or
`agent.CallbackContext` and will need to be expanded for the v2 `Context`
interface.

| File                                          | Type / var                  | Interface asserted    | Methods implemented (v1.4.0)                                                                                     |
|-----------------------------------------------|-----------------------------|------------------------|-----------------------------------------------------------------------------------------------------------------|
| `internal/extension/hooks_test.go`            | `mockToolCtx`               | `agent.ToolContext`    | `ToolConfirmation` returns nil. Inherits `mockReadonlyContext` for the rest.                                     |
| `internal/extension/hooks_test.go`            | `mockReadonlyContext`       | `agent.CallbackContext` (and `agent.ReadonlyContext` via embedding) | `UserContent`, `InvocationID`, `AgentName`, `ReadonlyState`, `UserID`, `AppName`, `SessionID`, `Branch`, `Artifacts`, `State`, `context.Context` (via `ctx` field) |
| `internal/tools/tool_invoke_test.go`           | `mockToolCtx` (struct)      | `agent.ToolContext`    | `context.Context` (via `ctx` field), `Artifacts`, `State`, `InvocationID`, `Branch`, `UserContent`, `ReadonlyState`, `UserID`, `AppName`, `SessionID`, `AgentName`, `FunctionCallID`, `Actions`, `SearchMemory`, `RequestConfirmation`, `ToolConfirmation` |
| `internal/palace/tool_invoke_test.go`         | `mockToolCtx` (struct)      | `agent.ToolContext`    | Same shape as above                                                                                              |
| `internal/extension/mcp_test.go`              | `failingToolset`, `hangingToolset`, `successToolset`, `resilientToolset` | `tool.Toolset` (via `Tools(ctx agent.ReadonlyContext)`) | `Name`, `Tools`. Do not implement context interfaces.                                                              |
| `internal/agent/agent_test.go` etc.            | various test fakes          | mix of `agent.InvocationContext`, `agent.ReadonlyContext` | not enumerated individually; see next research file.                                                              |

The total number of `var _ agent.X = (*Y)(nil)` type-assertion lines that
will need to be updated to point at the new `agent.Context` interface (or
left as-is if pointing at `agent.InvocationContext`/`agent.ReadonlyContext`/
`tool.Toolset` which are not changing) is: see `call-sites.md`.

## 5. Existing build / test / lint infrastructure

`/Users/dimetron/p6s/pi-dev/pi-go/Makefile` provides:

- `make build` → `go build -ldflags … ./cmd/pi` and `go build ./cmd/pi-sandbox`
- `make test` / `make test-unit` → `go test ./...`
- `make test-integration` → `go test -tags integration ./...`
- `make test-e2e` → `go test -tags e2e ./...`
- `make test-coverage` → `go test -coverprofile=coverage.out -coverpkg=./internal/... ./internal/...`
- `make lint` → `golangci-lint run ./...`
- `make vet` → `go vet ./...`
- `make check-cve` → `go mod tidy -v && grype db update && govulncheck ./... && grype .`

### Pre-existing test failure (not introduced by this migration)

Running `go test ./internal/...` on the current trunk (v1.4.0) shows one
unrelated failure:

```
--- FAIL: TestCommitCommand_ConfirmCommits (0.32s)
    commit_test.go:150: git commit -m initial commit failed: exit status 128
        error: 1Password: agent returned an error
        fatal: failed to write commit object
FAIL    github.com/dimetron/pi-go/internal/tui    9.855s
```

This failure depends on a 1Password CLI agent being installed and unlocked in
the dev environment. It is not ADK-related. The migration plan must either
work around it (e.g. skip the test, fix the test, or fix the 1Password
environment) or document it as a known pre-existing failure and exclude it
from the gate. **Decision deferred to design phase.**

## 6. `pi audit` security-audit command

Path: `internal/cli/audit.go` (cobra subcommand `audit`).

- Default: scans default skill directories for hidden Unicode / BiDi / tag-character prompt-injection attacks.
- Flags: `--dir`, `--file`, `--strip`, `--dry-run`, `--force`, `--verbose`, `--format` (text|json|markdown), `--output`.
- Exit codes: 0 = clean/info, 1 = critical, 2 = warning.

For the post-migration security audit step (Q7/A7), the natural extension is
to also run `pi audit` over the ADK v2 module cache (e.g. `go env GOMODCACHE/google.golang.org/adk/v2@v2.0.0/**.md`) to surface any hidden-Unicode findings in vendor-sourced docs. This is **not** what the command was designed for — `pi audit` scans `SKILL.md` files. The realistic options are:

- (i) Run `pi audit` against our own `skills/` and `*.SKILL.md` files (the default behavior) to confirm we didn't introduce anything.
- (ii) Add a `--path` flag if needed and run it against the ADK v2 module cache.
- (iii) Use a separate static analysis tool (e.g. `govulncheck`, `grype` from `make check-cve`) for the dependency closure.

**Decision deferred to design phase** (or this is a small enough decision to leave to the implementer at execution time).

## 7. Open questions deferred to design

1. Working directory of the migration: confirm we work on branch
   `feature/adk-20-migration` off trunk.
2. The pre-existing `TestCommitCommand_ConfirmCommits` failure: handle during design.
3. The post-migration `pi audit` scope: choose (i) / (ii) / (iii) above.
4. Coverage floor number: needs to be measured on trunk right before
   starting the migration; current `make test-coverage` shows 87.2% total
   statements (without `-tags e2e` filter on `internal/...`), 25.7% with
   `coverpkg=./internal/...` (the lower number includes un-tested code in
   the same package set as the tests run). The migration is responsible for
   not regressing whatever baseline is captured at branch-point.
