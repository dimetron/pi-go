# Research: ADK 2.x workflow engine — verified API surface

Module `google.golang.org/adk/v2 v2.2.0`. Every fact below was read from module
source, not inferred. Paths are relative to the module root.

## A workflow can be an agent — two ways

`agent/workflowagent.New(Config{Name, Description, SubAgents, Edges})` returns an
`agent.Agent`: it calls `workflow.New(cfg.Name, cfg.Edges)` then
`agent.New(agent.Config{… Run: wa.run})` (workflow.go:47-62).

`agent.Agent` has an unexported method (`agent/agent.go:43-52`), so it cannot be
implemented outside the ADK; `agent.Config.Run` is the sanctioned hook.
`*workflow.Workflow` alone is **not** an `agent.Agent` — no `SubAgents`,
`FindAgent`, or `Description`.

**Caveat that decides our design:** `workflowagent.New` passes **no**
`workflow.Option`, so `WithMaxConcurrency` and `WithStateSchema` are
unreachable through it. Building the `*Workflow` ourselves and wrapping it with
`agent.New(agent.Config{Run: wf.Run})` (~20 lines) keeps both.

`runner.Config.Agent` accepts any `agent.Agent`, so `internal/agent`'s
`buildRunner` (agent.go:407) already has the right shape.

## Routing — the load-bearing detail

- `StringRoute.Matches` → `matchRoute` iterates **`event.Routes`** only
  (workflow.go:66-80). `Event.Output` is never consulted. `IntRoute`,
  `BoolRoute`, `MultiRoute` all funnel through the same function.
- The routing event is recorded at `scheduler.go:765` (`if it.ev.Routes != nil`).
  **At most one per activation** — a second yields `ErrMultipleRoutingEvents`
  and fails the node.
- `findSuccessors` (scheduler.go:986-1032):
  - `Route == nil` → **always fires**, and does not count as a concrete match.
  - `Route == Default` → held aside; fires only if no concrete route matched
    (`defaultRoute.Matches` always returns false — it is structural).
  - concrete route → fires iff `event != nil && Route.Matches(event)`.
  - duplicate `To` targets are deduplicated.
- **No match and no Default → silent dead-end**, documented as "a deliberate
  decision not to continue, not an error" (scheduler.go:966).

So a node must **emit an event with `ev.Routes = []string{"PASS"}`**. Returning
the string sets only `Event.Output` and matches nothing. Supported shapes: a
`FunctionNode` fn returning `*session.Event`, or `NewEmittingFunctionNode` /
`NewDynamicNode` emitting it. The ADK example sets both `Routes` and `Output`.

## Errors, retries, failure

- A returned error marks the node failed, applies `RetryConfig`
  (scheduler.go:856-872, `Attempt++` then `ShouldRetry` → `CalculateDelay`),
  and then **fails the whole workflow** — `handleCompletion` cancels all and
  drains. There is no per-node continue-on-failure.
- `NodeConfig.Timeout` is honoured per activation (scheduler.go:388-392).
- Default retry: 5 attempts / 1s / 60s cap / 2.0× / full jitter.

## Fan-out

- **`NodeConfig.ParallelWorker` is a dead field** — declared in config.go:56,
  consumed only by `internal/configurable` YAML plumbing; the scheduler never
  reads it.
- The real mechanism is the `ParallelWorker` **node type**
  (`NewParallelWorker(name, wrapped, maxConcurrency, cfg)`,
  parallel_worker.go:39): input must be a Go slice; one goroutine per item with
  a per-item sub-branch; **one aggregate output event** (intermediate events are
  suppressed); `maxConcurrency <= 0` means unlimited; first failure cancels
  siblings. **The wrapped node must not carry a `RetryConfig`** (constructor
  errors) — the parent's retry is applied per item instead.
- `RunNode[OUT](ctx, child, input, opts…)` works only inside a `NewDynamicNode`
  body, using that body's ctx (it carries the sub-scheduler). Children are
  awaited inline and are **not** subject to `WithMaxConcurrency`.
  `WithUseSubBranch` scopes session event history; `WithIsolationScope` /
  `…FromNodePath` scope LLM prompt history by exact match. **Neither creates a
  git worktree.**

## Cycles

No run-time cap exists. `ErrUnconditionalCycle` is build-time and walks only
`Route == nil` edges (Default counts as conditional). Nothing in the scheduler
counts activations; `NodeState.Attempt` is the retry counter and resets to 0 on
completion. `NodeCompleted` is explicitly re-triggerable — "this is what enables
loops as graph cycles". ADK's own loop test bounds itself with a counter inside
the node body. **`max_cycles` must be enforced by the caller**, in
invocation-scoped, resume-safe state.

## Fan-in

`validateFanIn` (validation.go:369-386, build time) rejects a non-`JoinNode`
target with **two or more unconditional** incoming edges; conditional and
loop-back edges are deliberately excluded. Neither SOP violates this — which is
why `Compiled.Workflow()` already succeeds for both.

Two separate traps:
- `validateUniqueEdges` (validation.go:254) rejects two edges with the same
  `(From, To)` **regardless of route** — multiple route values at one target
  must become a single `MultiRoute` edge.
- At run time, `startNode` (scheduler.go:395) overwrites `runsByName` with no
  in-flight check. Two conditionally-routed predecessors that match concurrently
  double-activate the target and corrupt bookkeeping.

## HITL

`ResumeOrRequestInput` (request_input.go:123) returns the reply if already
resumed, else emits a request event carrying a synthetic FunctionCall named
`adk_request_input` whose ID is the interrupt id, and returns
`ErrNodeInterrupted`; the scheduler parks the node `NodeWaiting`.

`workflowAgent.run` detects the matching `FunctionResponse`, calls
`ReconstructRunState(session, invocationID)` — which **re-scans session event
history**, not a persisted blob — and dispatches `Workflow.Resume`.

Resume has two modes: **re-entry** (`RerunOnResume: &true`, the default for
dynamic nodes) re-activates the node; **handoff** (nil/false) completes the
asker with the response as output and schedules successors — and Pass 2 calls
`findSuccessors` with **`event = nil`** (resume.go:211), so **no concrete route
can fire**. An approval gate that routes on the human's answer must use
re-entry.

## Observability

`RunState` is created in `newScheduler` and held on the unexported scheduler;
`Run`/`RunNode` never expose it. `ReconstructRunState` is the only exported
accessor and serves paused runs between turns. **A live UI must consume the
event stream** — `NodeInfo.Path`, `Output`/`OutputFor`, `RequestedInput` — and
**node start is not an event**: infer "running" from a node's first emitted
event.

## What is missing in pi-go

Only the factory. `sop.Compile` already produces `[]workflow.Edge` and
`Compiled.Workflow()` already calls `workflow.New`. The sole `NodeFactory` is
`DescribeFactory`, whose nodes refuse to run.
