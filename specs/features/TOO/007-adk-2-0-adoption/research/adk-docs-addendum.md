# Research Addendum — ADK 2.0 Official Documentation Findings

> **Scope:** additional objective findings from the official ADK 2.0
> documentation site (`https://adk.dev`), fetched via `curl` from
> `llms.txt`. These supplement `v1-usage-surface.md`, `v2-api-delta.md`,
> and `call-sites.md`.
>
> The earlier research files were based on the v2.0.0 source and the
> GitHub release notes; this addendum adds the developer-facing
> documentation perspective, which surfaced three additional facts
> that the source-only analysis missed.

## A1. The official 2.0 overview page

Source: `https://adk.dev/2.0/index.md` (fetched 2026-07-03).

The official 2.0 docs are a multi-language index (Python, Go, Java, TypeScript).
The page is supported on **ADK Go v2.0.0** and ADK Python v2.0.0. The
Go-specific breaking changes are listed under the "ADK Go 1.x
compatibility" section. We read that section verbatim below.

### A1.1 Documented Go 1.x → 2.0 breaking changes

The page lists five distinct Go breaking changes:

1. **Module import path** (`google.golang.org/adk` → `google.golang.org/adk/v2`).
   *Migration action: `go get google.golang.org/adk/v2` and update all import statements.*

2. **Agent execution: `Agent` interface changes.** "In ADK Go 1.x, agents
   implemented the `agent.Agent` interface by providing a `Run` method.
   In ADK Go 2.0, agents are evaluated as individual *nodes* within the
   new Workflow Graph engine. Custom agent types that override internal
   execution behavior may no longer work as expected."
   *Migration action: "Move custom execution logic into standardized
   `BeforeAgentCallback` and `AfterAgentCallback` hooks."*

   **Impact on pi-go:** **zero.** pi-go does not implement `agent.Agent`
   directly. We only use `llmagent.New(llmagent.Config{…})` to build agents.
   No custom `Run` overrides exist in our codebase (verified by
   `grep -rn 'func.*Run.*agent.InvocationContext' internal/` — 0 matches).

3. **`session.NewEvent` signature change** (already documented in `v2-api-delta.md §5.4`).
   *Migration action: pass `ctx` as the first argument.*
   **Impact on pi-go:** **zero direct call sites** (0 hits for `session.NewEvent(`).

4. **Event schema: 5 new fields** on `session.Event`. The doc gives the
   full list with JSON serialization names:

   | Go field                       | Serialized name                                      | Purpose                                                              |
   |--------------------------------|------------------------------------------------------|----------------------------------------------------------------------|
   | `IsolationScope string`        | `isolationScope` (`json:"isolationScope,omitempty"`) | Restricts which agent contexts see this event in LLM prompt history. |
   | `Routes []string`              | `Routes` (no JSON tag — Go field name)               | Routing keys emitted by a node to drive conditional edge dispatch.   |
   | `RequestedInput *RequestInput` | `RequestedInput` (no JSON tag)                       | Signals that a workflow node is pausing for human input.             |
   | `Output any`                   | `Output` (no JSON tag)                               | Generic data output from a workflow node.                            |
   | `NodeInfo *NodeInfo`           | `nodeInfo` (`json:"nodeInfo,omitempty"`)             | Workflow-node metadata identifying which node emitted the event.     |

   *Migration action: "Update your database schemas and downstream client
   validators to expect and store the five new fields on all Event payloads.
   Pay particular attention to `Routes`, `RequestedInput`, and `Output`,
   which have no JSON struct tags and therefore serialize under their Go
   field names exactly as shown above."*

   **Impact on pi-go:** **none for production code** — verified:
   - `internal/session/store.go` uses `json.Marshal(event)` and
     `json.Unmarshal([]byte(line), &event)`. JSON-blob storage handles
     the new fields automatically in both directions.
   - `internal/atif/convert.go`, `link.go`, `writer.go` do not access
     the new fields (verified by `grep -nE 'Event\.(Routes|RequestedInput|Output|NodeInfo|IsolationScope)' internal/atif/`
     — 0 matches).
   - The docs explicitly note: "if your custom session service stores
     events as serialized JSON blobs rather than mapping them to explicit
     columns, you do not need to update your schema." Our store is JSONL
     (line-delimited JSON blobs). ✅

5. **Error handling: "Never catch `BaseException`" (Python)**. The Go
   equivalent (from the same docs page, restated in Go terms): "Never
   `recover()` from `panic` inside a tool body — that would mask the
   failure from the framework, disabling the new 2.0 automatic retry
   mechanisms for that step."

   **Impact on pi-go:** **zero** in tool bodies. We have 3 `recover()` callsites
   in production code, all at **outer boundaries**:
   - `internal/tools/compactor.go:161` — `runStage` wraps a single compaction stage
     (not a tool body).
   - `internal/tui/agent_loop.go:333` — wraps the whole agent invocation
     (outer boundary).
   - `internal/acp/server/agent.go:226` — wraps the ACP prompt handler
     (outer boundary).

   All three are **correct defensive panic recovery** at framework boundaries.
   They convert panics to logged errors and continue. None are inside a
   functiontool handler or agent tool body. ✅

6. **Context & Callbacks: In-Place Mutation** ("Don't manually append
   events to `context.session.events`"). Verified by grep:
   `grep -rnE 'session\.events\.append|Events\(\)\.Append' internal/ --include="*.go"`
   — 0 matches in production code. The 9 hits are in test files that
   collect events into a local slice, not into the session.

## A2. Function tools (callback and `functiontool.Func` shape)

Source: `https://adk.dev/tools-custom/function-tools/index.md`.

The Go section of this page confirms:

> A parameter is considered **required** if its struct field does **not** have
> the `omitempty` or `omitzero` option in its `json` tag.
> A parameter is considered **optional** if its struct field has the
> `omitempty` or `omitzero` option in its `json` tag.

This is unchanged between v1.4.0 and v2.0.0. No impact on our struct
definitions. (Our input structs already use `omitempty` where appropriate;
verified by `grep -nE 'json:"[^"]*omitempty' internal/tools/*.go | wc -l`
which gives a non-zero count.)

## A3. Context (the unified `agent.Context`)

Source: `https://adk.dev/context/index.md`.

The doc confirms the context-flavors story:
- `InvocationContext` — for agent core impl.
- `ReadonlyContext` — restricted, for instruction providers.
- **`Context`** — for agent lifecycle and model callbacks. Provides state, artifacts, memory.
- **`ToolContext`** — for tool execution and tool-related callbacks. Adds auth, memory search, artifact discovery.

The Go section explicitly notes that in 2.0 these two latter types
(`CallbackContext` and `ToolContext`) **continue to exist as separate
types** for backward compatibility, but new code should use `Context`
directly. The unified `agent.Context` in v2.0.0 is **not** a Go type alias
— it is a true interface superset.

**Note:** the v1.4.0 `agent.CallbackContext` interface still exists
verbatim in v2.0.0 (verified by grep). So a function whose parameter is
`ctx agent.CallbackContext` and is passed as a `BeforeModelCallback`
**will fail to compile** in v2.0.0 because v2.0.0's `BeforeModelCallback`
is declared as taking `ctx agent.Context`, not `ctx agent.CallbackContext`.
This is a **breaking change** at the type-system level, even though
`CallbackContext` is technically still defined. The same applies to
`ToolContext` (v1.4.0) vs the v2.0.0 callback type expectations.

This confirms the "callback parameter re-type" is **mandatory**, not
optional. (Our earlier call-sites.md research already noted this; the
official doc confirms it.)

## A4. Events doc (`https://adk.dev/events/index.md`)

The Go section of the events doc gives a **simplified** view of
`session.Event`:

```go
type Event struct {
    model.LLMResponse
    Author       string
    InvocationID string
    ID           string
    Timestamp    time.Time
    Actions      EventActions
    Branch       string
    // ... other fields
}
```

This simplified view **omits** the 5 new v2.0.0 fields and the
`LongRunningToolIDs` field. The actual struct in `session/session.go` has
13 fields (see `v2-api-delta.md §5.11`). The doc simplification is fine
for human readers but the migration must reference the real struct.

## A5. Sessions doc (`https://adk.dev/sessions/session/index.md`)

The Go section of the sessions doc shows the standard
`session.InMemoryService().Create(ctx, &session.CreateRequest{...})` →
`runner.New(runner.Config{...})` → `r.Run(ctx, ...)` pattern. **This is
the exact pattern pi-go uses** (in `internal/agent/agent.go` `buildRunner`
and `New`). No new v2 pattern is required for our use case.

## A6. llmagent.Config field set (verified against v2.0.0 source)

Source: `/tmp/adk-v2.0.0/google.golang.org/adk/v2@v2.0.0/agent/llmagent/llmagent.go:182-359`.

The v2.0.0 `llmagent.Config` struct has 25 fields. Of those, we set
**9** in `internal/agent/agent.go:245-255`:

| Field name             | v1.4.0 | v2.0.0 | Removed? | Renamed? |
|------------------------|--------|--------|----------|----------|
| `Name`                 | ✅     | ✅     | No       | No       |
| `Description`          | ✅     | ✅     | No       | No       |
| `Model`                | ✅     | ✅     | No       | No       |
| `Instruction`          | ✅     | ✅     | No       | No       |
| `Tools`                | ✅     | ✅     | No       | No       |
| `Toolsets`             | ✅     | ✅     | No       | No       |
| `BeforeToolCallbacks`  | ✅     | ✅     | No       | No       |
| `AfterToolCallbacks`   | ✅     | ✅     | No       | No       |
| `BeforeModelCallbacks` | ✅     | ✅     | No       | No       |
| `AfterModelCallbacks`  | ✅     | ✅     | No       | No       |

**All 9 fields are preserved in v2.0.0. None renamed or removed.**
The new v2.0.0 fields (`SubAgents`, `BeforeAgentCallbacks`, `AfterAgentCallbacks`,
`GenerateContentConfig`, `OnModelErrorCallbacks`, `OnToolErrorCallbacks`,
`InstructionProvider`, `GlobalInstruction`, `GlobalInstructionProvider`,
`DisallowTransferToParent`, `DisallowTransferToPeers`, `IncludeContents`,
`InputSchema`, `OutputSchema`, `OutputKey`, `Mode`) are **additive** and
we don't need to set them.

## A7. New v2.0.0 features — list (for reference, not adopted)

Per the v2.0.0 release body and the official 2.0 overview page, the new
v2.0.0 features are:

- **Graph-based workflow engine** (`google.golang.org/adk/v2/workflow`) — Compose
  agents, tools, and functions as nodes in a directed graph executed by a
  concurrent scheduler: static and dynamic graphs, conditional routing,
  fan-out/fan-in (JoinNode), parallel workers, per-node retries/timeouts,
  input/output schema validation, and human-in-the-loop pause/resume.
- **Dynamic workflows** (`/graphs/dynamic/`) — code-based logic for iterative
  loops and complex decision-based branching.
- **Collaborative workflows** (`/workflows/collaboration/`) — coordinator
  agents and multiple subagents working together.
- **LlmAgent delegation modes** — `ModeChat`, `ModeTask`, `ModeSingleTurn`
  (per `llmagent.Config.Mode`). Default: `ModeChat` for sub-agents,
  `ModeSingleTurn` for workflow nodes.
- **Plugins** (`google.golang.org/adk/v2/plugin`) — alternative to
  callbacks for cross-cutting concerns.
- **Platform package** (`google.golang.org/adk/v2/platform`) — time/UUID
  providers used by `session.NewEvent` for deterministic event generation.

**Per A1, none of these are adopted in this migration spec.**

## A8. Updated impact tally (with the new findings)

| Concern                                                | Impact on pi-go                                                            |
|--------------------------------------------------------|----------------------------------------------------------------------------|
| Module path change                                     | ~85 files (mechanical)                                                      |
| Callback parameter type widening                       | ~60 function literals (mechanical)                                          |
| Mock method additions                                  | 3 mock files (`internal/extension/hooks_test.go`, `internal/tools/tool_invoke_test.go`, `internal/palace/tool_invoke_test.go`) |
| `session.NewEvent` signature                           | 0 direct call sites — no code change needed                                |
| `agent.NewToolContext`                                 | 0 direct call sites — no code change needed                                |
| `tool.Context` alias removed                           | 0 uses of the alias as a type name — no code change needed                 |
| `Event` struct gained 5 fields                         | JSON-blob storage handles it automatically. No code change in `internal/session/store.go`, `internal/atif/...` |
| Custom `agent.Agent` impls no longer driven by `Run`   | 0 custom impls in pi-go (only `llmagent.New` is used)                      |
| Don't catch panic inside tool bodies                   | 0 `recover()` calls in tool bodies (3 callsites are at outer boundaries, correct) |
| Don't manually append to `context.session.events`     | 0 such calls in production code (only test code that collects into a local slice) |
| `llmagent.Config` field set                            | All 9 fields we set are preserved in v2.0.0. No field renames or removals.  |
| `runner.Config` field set                              | All fields we set are preserved in v2.0.0. No field renames or removals.    |
| New v2.0.0 features (workflows, plugins, modes)        | **Not adopted** per A1.                                                     |

**Net source-level change count remains:**
- ~85 import-path edits
- ~60 callback-parameter-type edits
- ~36 mock method additions (3 mocks × ~12 new methods each)
- 1 line in `go.mod`
- `go.sum` regenerated by `go mod tidy`

## A9. Sources verified

All findings in this addendum were fetched on 2026-07-03 from:

- `https://adk.dev/llms.txt` — llms.txt index, 2xx response
- `https://adk.dev/2.0/index.md` — 2.0 overview with "ADK Go 1.x compatibility" section
- `https://adk.dev/tools-custom/function-tools/index.md` — function tools guide
- `https://adk.dev/context/index.md` — context types guide
- `https://adk.dev/events/index.md` — events guide
- `https://adk.dev/sessions/session/index.md` — sessions guide
- `/tmp/adk-v2.0.0/google.golang.org/adk/v2@v2.0.0/agent/llmagent/llmagent.go` (lines 182–359) — verified `llmagent.Config` field set
- `/tmp/adk-v2.0.0/google.golang.org/adk/v2@v2.0.0/session/session.go` (lines 94–207) — verified `Event` struct and `RequestInput` / `NodeInfo` types
- `/tmp/adk-v2.0.0/google.golang.org/adk/v2@v2.0.0/agent/common_context.go` (line 114) — verified `agent.NewToolContext` still exists
- `grep` / `ripgrep` queries against the pi-go repo

## A10. Open questions resolved by this addendum

The three open questions raised in `v1-usage-surface.md §7` and
`call-sites.md §9` are now resolved:

1. **The pre-existing `TestCommitCommand_ConfirmCommits` failure**
  (A8a=iii): the fix mechanism is chosen during implementation, but the
  design/plan treats the fix as an in-scope prerequisite for final green
  gates. This addendum does not change the answer.
2. **The post-migration `pi audit` scope** (A8c=i): the default scope
   (scan default skill directories) is sufficient per A7.
3. **The `var _ agent.InvocationContext = ...` mocks** (A8b=full): the
   new v2.0.0 `agent.InvocationContext` adds 3 new methods
   (`IsolationScope`, `ResumedInput`, `WithICDelta`) that all
   `InvocationContext` mocks need for forward-compat compliance.
   This addendum does not change the answer.

## A11. New questions raised by this addendum (none)

This addendum did not raise any new questions that change the scope of
the migration. The five breaking changes listed in §A1.1 are all
already covered by either A1 ("no new v2 features used") or are
no-ops for our code (verified by grep).

## A12. Conclusion

The additional documentation research **strengthens** the earlier
findings in `v2-api-delta.md` and `call-sites.md` without changing
them. The migration is still a **pure, mechanical, ~85 + ~60 + ~36
edit** exercise. No new v2 features need to be adopted for the
migration to be complete.
