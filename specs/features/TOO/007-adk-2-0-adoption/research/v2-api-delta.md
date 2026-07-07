# Research — `google.golang.org/adk` v2.0.0 API Delta

> **Scope:** objective comparison of v1.4.0 (current) and v2.0.0 (target).
> Sources: `v2.0.0` GitHub release notes, `README-v2.md`, v2.0.0 source
> downloaded from `proxy.golang.org/google.golang.org/adk/v2/@v/v2.0.0.zip`,
> v1.4.0 source from the local module cache.
>
> See companion files for the v1.4.0 usage surface and the per-call-site inventory.

## 1. Module path change

- **v1.4.0:** `google.golang.org/adk`
- **v2.0.0:** `google.golang.org/adk/v2` (Go semantic-import versioning rule for major-version bumps)

Every import line in pi-go (`google.golang.org/adk/...`) becomes
`google.golang.org/adk/v2/...`. This is the single largest mechanical change.

## 2. Go version

- **v1.4.0 `go.mod`:** `go 1.25.0`
- **v2.0.0 `go.mod`:** `go 1.25.0` (no change to the minimum Go version)
- **pi-go `go.mod`:** `go 1.26.4` (above the floor — no change required)

## 3. Package layout delta

Top-level packages that appear in v2.0.0 but **did not exist** in v1.4.0:
none observed at the top level — v2.0.0 is structurally a superset.

Top-level packages that **did not change** (verified by listing both
module cache trees):

```
adk/agent      → adk/v2/agent
adk/artifact   → adk/v2/artifact
adk/cmd        → adk/v2/cmd
adk/internal   → adk/v2/internal
adk/memory     → adk/v2/memory
adk/model      → adk/v2/model
adk/runner     → adk/v2/runner
adk/scripts    → adk/v2/scripts
adk/server     → adk/v2/server
adk/session    → adk/v2/session
adk/telemetry  → adk/v2/telemetry
adk/tool       → adk/v2/tool
adk/util       → adk/v2/util
```

New packages in v2.0.0: **`platform`**, **`plugin`**, **`workflow`**.

Internal additions: `agent/common_context.go`, `agent/common_context_delta.go`,
`agent/common_context_mock.go`, `agent/callback_context_wrapper.go`,
`agent/tool_context_wrapper.go`, `agent/context_mock.go`,
`agent/dynamic_scheduler.go`, `runner/run_node.go` (and many test files).

## 4. Sub-package renames / removals

**No renames, no removals observed at the public-API level.** All
sub-packages used by pi-go continue to exist at the same import path
(only the `/v2` prefix changes):

- `adk/agent` → `adk/v2/agent`
- `adk/agent/llmagent` → `adk/v2/agent/llmagent`
- `adk/model` → `adk/v2/model`
- `adk/model/gemini` → `adk/v2/model/gemini`
- `adk/session` → `adk/v2/session`
- `adk/tool` → `adk/v2/tool`
- `adk/tool/functiontool` → `adk/v2/tool/functiontool`
- `adk/tool/mcptoolset` → `adk/v2/tool/mcptoolset`
- `adk/tool/toolconfirmation` → `adk/v2/tool/toolconfirmation`
- `adk/memory` → `adk/v2/memory`
- `adk/runner` → `adk/v2/runner`

## 5. Type and function changes (only the ones pi-go touches)

### 5.1 `agent.Context` — the unified context

v1.4.0 had three separate interfaces:
- `agent.InvocationContext`
- `agent.ReadonlyContext`
- `agent.CallbackContext`
- `agent.ToolContext` (extends `CallbackContext`)

v2.0.0 collapses the last two into one `agent.Context` interface that
embeds `ReadonlyContext` + `InvocationContext` and adds the new method
surface. **The v1.4 interfaces still exist as separate types in v2.0.0.**

#### v2.0.0 `agent.Context` — full method set (excludes embedded interfaces)

```go
type Context interface {
    ReadonlyContext
    InvocationContext

    // callback-context surface (was CallbackContext in v1.4.0)
    Artifacts() Artifacts
    State() session.State

    // tool-context surface (was ToolContext in v1.4.0)
    FunctionCallID() string
    Actions() *session.EventActions
    SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error)
    ToolConfirmation() *toolconfirmation.ToolConfirmation
    RequestConfirmation(hint string, payload any) error

    // new in v2.0.0 — workflow / dynamic-orchestration surface
    ResumedInput(interruptID string) (any, bool)     // also on InvocationContext
    Path() string
    RunID() string
    SubScheduler() DynamicSubScheduler
    WithAgentContext(ctx context.Context) Context
    WithAgentTimeout(timeout time.Duration) (Context, context.CancelFunc)
    WithAgentCancel() (Context, context.CancelFunc)
    OutputForAncestors() []string
    WithDelta(d *CommonContextDelta) Context
}
```

#### v2.0.0 `agent.InvocationContext` — added members

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

    // NEW in v2.0.0
    IsolationScope() string
    ResumedInput(interruptID string) (any, bool)
    WithICDelta(d *InvocationContextDelta) InvocationContext
}
```

#### Summary of net new methods on `agent.Context` (vs. v1.4 `ToolContext`)

| Method                                  | New on | Notes                                                                |
|-----------------------------------------|--------|----------------------------------------------------------------------|
| `IsolationScope() string`               | `InvocationContext` | Used by collaboration agents for per-sub-agent history filtering. |
| `ResumedInput(string) (any, bool)`      | `InvocationContext` and `Context` | Workflow / HITL resume payload lookup.                            |
| `WithICDelta(*InvocationContextDelta) InvocationContext` | `InvocationContext` | Delta wrapper for invocation context. |
| `Path() string`                         | `Context` | Workflow node path.                                                  |
| `RunID() string`                        | `Context` | Workflow run id.                                                     |
| `SubScheduler() DynamicSubScheduler`    | `Context` | Dynamic-node sub-scheduler.                                          |
| `WithAgentContext(ctx) Context`         | `Context` | Wrap with a new context.                                             |
| `WithAgentTimeout(d) (Context, CancelFunc)` | `Context` | Add timeout.                                                     |
| `WithAgentCancel() (Context, CancelFunc)` | `Context` | Add cancellation.                                                   |
| `OutputForAncestors() []string`         | `Context` | Workflow ancestors list.                                             |
| `WithDelta(*CommonContextDelta) Context` | `Context` | Apply common-context delta.                                          |

All existing methods on `agent.InvocationContext`, `agent.ReadonlyContext`,
`agent.CallbackContext`, and `agent.ToolContext` in v1.4.0 are preserved
unchanged in v2.0.0 (with the noted additions to `InvocationContext`).

### 5.2 Callback signatures — `agent/llmagent`

v1.4.0:

```go
type BeforeModelCallback func(ctx agent.CallbackContext, llmRequest *model.LLMRequest) (*model.LLMResponse, error)
type AfterModelCallback  func(ctx agent.CallbackContext, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error)
type BeforeToolCallback   func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error)
type AfterToolCallback    func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error)
```

v2.0.0:

```go
type BeforeModelCallback func(ctx agent.Context, llmRequest *model.LLMRequest) (*model.LLMResponse, error)
type AfterModelCallback  func(ctx agent.Context, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error)
type BeforeToolCallback   func(ctx agent.Context, tool tool.Tool, args map[string]any) (map[string]any, error)
type AfterToolCallback    func(ctx agent.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error)
```

**Change:** every callback type's first parameter is widened from
`agent.CallbackContext` (model) / `tool.Context` (tool) to the unified
`agent.Context`. This is a **drop-in widening** — the new `agent.Context`
is a super-interface of the old types.

In our code, every user-defined callback that takes `ctx agent.ToolContext`
(about 40+ sites in `internal/extension/hooks.go`, `internal/lsp/hooks.go`,
`internal/tools/compactor.go`, `internal/memory/worker.go`, etc.) needs to
switch the parameter type to `ctx agent.Context`. The function body can
generally stay the same (it uses methods that exist on both types), but
any v2-only method access (e.g. `ctx.IsolationScope()`) is a new option
(not required for the migration).

### 5.3 `functiontool.Func` signature

v1.4.0:

```go
type Func[TArgs, TResults any] func(tool.Context, TArgs) (TResults, error)
```

v2.0.0:

```go
type Func[TArgs, TResults any] func(agent.Context, TArgs) (TResults, error)
```

**Change:** same as callback signatures. Every functiontool handler in
pi-go (about 25+ call sites in `internal/tools/*.go` and
`internal/palace/tool_*.go`) that takes `ctx agent.ToolContext` needs
to be retyped to `ctx agent.Context`. The handler body is unaffected
for v1-method usage.

### 5.4 `session.NewEvent`

v1.4.0:

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

v2.0.0:

```go
// NewEvent creates a new event defining now as the timestamp.
//
// The event ID and timestamp are obtained through the platform package, so a
// time or UUID provider installed on ctx (see [platform.WithTimeProvider] and
// [platform.WithUUIDProvider]) controls them.
func NewEvent(ctx context.Context, invocationID string) *Event {
    return &Event{
        ID:           platform.NewUUID(ctx),
        InvocationID: invocationID,
        Timestamp:    platform.Now(ctx),
        Actions:      EventActions{StateDelta: make(map[string]any), ArtifactDelta: make(map[string]int64)},
    }
}
```

**Change:** `NewEvent` now takes a `context.Context` as its first parameter.
`NewEventWithContext` is gone.

**Impact on pi-go:** **zero direct call sites** — we never call
`session.NewEvent` directly. Event creation goes through the ADK runner
and the agent loop, not us. So this change has no source-level impact on
our repo.

### 5.5 `tool.Context` (alias) — removed

v1.4.0 (already `Deprecated`):

```go
// Context is an alias for agent.ToolContext.
//
// Deprecated: use agent.Context directly. This alias exists only to
// minimize churn during the migration and will be removed in a future
// release.
type Context = agent.ToolContext
```

v2.0.0: **removed.** The `tool` package still has `Tool`, `Toolset`,
`Predicate`, `AllowedToolsPredicate`, but no `Context` alias.

**Impact on pi-go:** if any of our code references `tool.Context`
directly (not as a callback parameter, which is the common case), the
identifier is gone. A direct grep shows **zero** references to
`tool.Context` as a type in our code (the v1.4.0 code uses
`agent.ToolContext` directly, and the callback parameters are
`tool.Context` only inside function-type literals that the v2.0.0
compiler will silently accept as `agent.Context`-typed callbacks).

### 5.6 `agent.NewToolContext` and `tool.NewToolContext`

v1.4.0 had `tool.NewToolContext` (wraps `agent.NewToolContext`).
Both exist in v2.0.0; the `tool.NewToolContext` wrapper is **removed**
(verified by grep on the v2 source).

**Impact on pi-go:** zero — we do not call `NewToolContext` in any of our
files (events are produced by the ADK runner).

### 5.7 `agent.Agent`, `agent.Artifacts`, `agent.Memory`

Byte-identical in v1.4.0 and v2.0.0 (verified by `diff` of the public
`type` declarations).

### 5.8 `agent.Loader`, `agent.LiveSession`

Identical in shape. (No new methods observed.)

### 5.9 `model.LLM`, `model.LLMRequest`, `model.LLMResponse`

Byte-identical (verified by `diff` of the `^type|^func` grep between
v1.4.0 and v2.0.0 of `model/llm.go`).

### 5.10 `model/gemini` package

Identical layout (single file + tests). No public API change.

### 5.11 `session` package — public types

`Event`, `EventActions`, `Events`, `Session`, `ReadonlyState`, `Service`,
`State` — all carry over with the same public surface. The only change to
v2.0.0 is the `NewEvent` signature (see §5.4).

### 5.12 `runner` package

Public types: `Config`, `PluginConfig`, `RunOption`, `Runner`, `WithStateDelta` —
identical. v2.0.0 adds many test files (`run_node_test.go`,
`hitl_integration_test.go`, `find_active_task_isolation_scope_test.go`)
but no public API changes for the things pi-go uses.

### 5.13 `tool` package — public types

`Tool`, `Toolset`, `Predicate`, `AllowedToolsPredicate`, `StringPredicate`
— all carry over. `StringPredicate` was already `Deprecated` in v1.4.0 and
remains so in v2.0.0.

### 5.14 `tool/toolconfirmation` package

`ToolConfirmation` struct and `OriginalCallFrom` function — byte-identical
to v1.4.0. No changes.

### 5.15 `memory` package

`SearchResponse` and `Service` interface — identical.

### 5.16 `mcptoolset` package

`Config` struct, `New` function — identical.

## 6. `agent.StrictContextMock` (new in v2.0.0)

Per the migration guide and the v2 source, v2.0.0 ships a
`agent.StrictContextMock` that **fully implements the entire v2
`agent.Context` interface** by panicking for every un-overridden method
and serving the standard `context.Context` methods from a `Ctx` field.

Source: `agent/context_mock.go` (v2.0.0).

Per **A1/A5** of our requirements, we do **not** switch our existing
hand-rolled mocks to `StrictContextMock` — we add the missing methods
in place so the mocks continue to satisfy `agent.Context` (or
`agent.ToolContext` if we want to be conservative) under the v2.0.0
type definitions.

The full method set that our mocks must grow to satisfy (to assert
`var _ agent.Context = (*Y)(nil)`) is the union of:

- All methods on `agent.InvocationContext` (including new `IsolationScope`, `ResumedInput`, `WithICDelta`)
- All methods on `agent.ReadonlyContext`
- All methods in §5.1 (the additional `Context` surface beyond the embedded ones)
- The 4 `context.Context` standard methods (`Deadline`, `Done`, `Err`, `Value`) — these are typically served by an embedded `context.Context` field on the mock struct.

We do **not** need to add the following methods that appear on
`StrictContextMock` but are **not** on the public `agent.Context`
interface (they are on private wrapper types `callbackContextWrapper` and
`toolContextWrapper`):

- `WithBranch(branch string) Context`
- `InvocationContext() InvocationContext`
- `SetInvocationContext(InvocationContext)`

These three are **excluded** from our hand-rolled mock upgrades because
they are not part of the public `agent.Context` interface and are
therefore not required for the `var _ agent.Context = …` assertion.

## 7. Transitive dependency changes (v1.4.0 → v2.0.0 ADK go.mod)

Relevant to pi-go's `go.mod` after we bump:

| Dependency                                       | v1.4.0 ADK requires | v2.0.0 ADK requires | pi-go's current pin | Impact on pi-go                                                                                |
|--------------------------------------------------|---------------------|---------------------|---------------------|------------------------------------------------------------------------------------------------|
| `github.com/a2aproject/a2a-go/v2`                | `v2.3.1`            | `v2.3.1`            | `v2.3.1`            | **none** — already in sync.                                                                    |
| `github.com/modelcontextprotocol/go-sdk`         | `v1.4.1`            | `v1.4.1`            | `v1.6.1`            | **none** — our pin is already newer; `go mod tidy` will keep `v1.6.1`.                         |
| `google.golang.org/genai`                        | `v1.57.0`           | `v1.57.0`           | `v1.61.0`           | **none** — our pin is already newer; `go mod tidy` will keep `v1.61.0`.                        |
| `golang.org/x/sync`                              | `v0.20.0`           | `v0.20.0`           | `v0.21.0`           | **none** — our pin is already newer.                                                           |
| `go.opentelemetry.io/otel`                       | `v1.43.0`           | `v1.43.0`           | `v1.44.0`           | **none** — our pin is already newer.                                                           |
| `go.opentelemetry.io/otel/trace`                | `v1.43.0`           | `v1.43.0`           | `v1.44.0`           | **none** — our pin is already newer.                                                           |
| `gorm.io/gorm`                                   | (not used by ADK)   | `v1.31.0` (added in v2) | (not used by pi-go) | v2.0.0 ADK adds `gorm.io/gorm` as a transitive dep (for the in-memory session service). This will appear in pi-go's `go.mod` indirect block. **Acceptable** per A6. |
| `github.com/glebarez/sqlite`                     | (not used by ADK)   | `v1.8.0` (added in v2) | (not used by pi-go) | New transitive for the in-memory session service's SQLite fallback. We use `modernc.org/sqlite` already; **no conflict**. |
| `github.com/mitchellh/mapstructure`              | (not used by ADK)   | `v1.5.0` (added in v2) | (not used by pi-go) | New transitive. **Acceptable** per A6. |

**Net summary:** the v2.0.0 ADK's transitive deltas relative to v1.4.0 are
**all compatible with our existing pins**. The only new indirect deps that
`go mod tidy` will pull into our `go.mod` are `gorm.io/gorm`,
`glebarez/sqlite`, and `mitchellh/mapstructure`, all of which are purely
additive (we don't directly use them, and they don't conflict with
`modernc.org/sqlite` or our OTel/x/sync/genai pins).

## 8. v2.0.0 release notes summary

From the GitHub release body (tag `v2.0.0`, commit
`78f9c2411a62df736bf84425c6fa2a2c598e35cb`):

> ## New major features
> In ADK Go 2.0 we are adding our new graph-based workflow engine, built-in
> human-in-the-loop, dynamic orchestration and more:
> - **Agent workflows** — a new graph-based orchestration engine. Compose
>   agents, tools, and functions as nodes in a directed graph executed by a
>   concurrent scheduler: static and dynamic graphs, conditional routing,
>   fan-out/fan-in (JoinNode), parallel workers, per-node retries/timeouts,
>   input/output schema validation, and human-in-the-loop pause/resume.
> - **Collaboration agents** — LlmAgent gains `chat`, `task`, and
>   `single_turn` modes so a coordinator can delegate to specialist
>   sub-agents that return control automatically, each with
>   isolation-scoped conversation history.
> - **Context unification** — the separate `ToolContext`, `CallbackContext`
>   are merged into a single `agent.Context`. `New agent.StrictContextMock`
>   keeps test fakes forward-compatible as the interface grows.
>
> Note: v2 moves the module path to `google.golang.org/adk/v2` and
> requires Go 1.25+.

> ## Migration
> To migrate from v1 please follow the migration guide
> (`README-v2.md`).

**Key constraints for our migration:**

- The release body explicitly says the v2.0.0 module path is `google.golang.org/adk/v2`. We will use this exact import path prefix.
- The release body says the v2.0.0 release requires Go 1.25+. Our `go.mod` says 1.26.4, which is above the floor.
- The release body does not call out the `session.NewEvent` signature change. That change is in the migration guide (`README-v2.md`) and is something we noted as a no-op for our code (zero call sites).

## 9. Summary: what our migration must mechanically do

1. **Replace import paths** in 50+ files:
   `google.golang.org/adk/...` → `google.golang.org/adk/v2/...`
2. **Widen callback parameter types** in 40+ function-type literals
   (`func(ctx agent.ToolContext, ...)` and `func(ctx agent.CallbackContext, ...)`
   become `func(ctx agent.Context, ...)`). This applies to:
   - `llmagent.BeforeToolCallback` / `llmagent.AfterToolCallback` literals
   - `llmagent.BeforeModelCallback` / `llmagent.AfterModelCallback` literals
   - `functiontool.Func[TArgs, TResults]` literals (i.e. every `newTool(…)` call)
3. **Update hand-rolled mocks** (`mockToolCtx`, `mockReadonlyContext`, etc.)
   to add the new `agent.Context` surface methods (`IsolationScope`,
   `ResumedInput`, `WithICDelta`, `Path`, `RunID`, `SubScheduler`,
   `WithAgentContext`, `WithAgentTimeout`, `WithAgentCancel`,
   `OutputForAncestors`, `WithDelta`). Add `var _ agent.Context = (*Y)(nil)`
   (or `var _ agent.ToolContext = (*Y)(nil)` for back-compat) to each.
4. **Bump `go.mod`** to require `google.golang.org/adk/v2 v2.0.0`.
5. **Run `go mod tidy`** to absorb the new indirect deps
   (`gorm.io/gorm`, `glebarez/sqlite`, `mitchellh/mapstructure`).
6. **Verify** all gates from A4 pass.

## 10. Summary: what the migration does NOT do

- Does not adopt the v2.0.0 graph-based workflow engine.
- Does not adopt v2.0.0 collaboration-agent modes (`chat`, `task`, `single_turn`).
- Does not adopt the v2.0.0 `platform` / `plugin` packages.
- Does not call `session.NewEvent` directly (no source change needed for §5.4).
- Does not introduce `StrictContextMock` (we keep our hand-rolled mocks and
  add methods in place, per A1/A5).
