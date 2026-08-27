# Design: /plan and /run as ADK 2.x workflow agents, with live TUI visualization

Status: design verified by an independent review (codex) plus two source-level
verifications (ADK engine semantics; pi-go feasibility). Every claim below that
concerns the engine was read from `google.golang.org/adk/v2@v2.2.0` source.

**The executor exists.** `internal/sop/exec` supplies the `NodeFactory` the
compiler was waiting for, and `exec.Agent` wraps a compiled SOP as an ADK
`agent.Agent`. Both embedded SOPs now execute end to end through a real
`runner.Runner` under test: the forward path, a FAIL routing to `repair`
without also taking the forward edge, a gate failure repairing and merging, a
non-converging loop stopped by the cycle budget, and the plan SOP's
`plan.review` FAIL round trip. What remains is the *work* inside the stages —
the `StageRunner` that spawns subagents, runs gates, and merges worktrees — plus
the TUI wiring.

## Decision

Execute `/plan` and `/run` **through the compiled SOP graph**, hosted as an ADK
workflow agent, and render that same graph in the sidebar with stage status
driven by the engine rather than by anything the TUI or an LLM reports.

Two problems collapse into one solution:

- The graph in `internal/sop` is compiled, linted, and never executed
  (`research/execution-path.md`). Supplying the missing `NodeFactory` runs it.
- The sidebar needs to know the active stage. With the engine executing, that
  comes from the run itself — no `stage_start` marker emitted by a model, no
  mapping of the imperative `phase` field, no drift between picture and reality.

This supersedes the marker-based approach in the original requirements: markers
were a workaround for the absent factory.

## Architecture

```
  plan.sop.yaml / run.sop.yaml
        │  sop.LoadEmbeddedDefinition        (added)
        ▼
   *sop.Definition
        │  sop.Compile(def, factory)         (exists — the factory is the gap)
        ▼
   *sop.Compiled {Edges, Nodes, Order}
        │  compiled.Workflow()               (exists; keeps WithMaxConcurrency)
        ▼
   *workflow.Workflow
        │  adkagent.New{Run: wf.Run}         (~20 lines; see "Own the workflow")
        ▼
     agent.Agent  →  runner.New{Agent: …}    (exists — agent.go:407)
        ▼
   iter.Seq2[*session.Event, error]  ──► chat stream
                                     └──► node status ──► sidebar diagram
```

### Own the workflow, don't use `workflowagent`

`workflowagent.New` looks like the obvious entry point and is the wrong one:
it calls `workflow.New(cfg.Name, cfg.Edges)` with **no options**, silently
dropping `defaults.max_concurrency` (4 for plan, 3 for run), and it keeps the
`*workflow.Workflow` in an unexported wrapper. Building the workflow ourselves
and wrapping it with `agent.New(agent.Config{Run: wf.Run})` — `agent.Config.Run`
is the sanctioned public hook — keeps the options and keeps the handle.

## The node contract

This is the part the first draft got wrong, and it governs every node body.

1. **Routing matches `session.Event.Routes`, never the node's return value.**
   `StringRoute.Matches` → `matchRoute` scans `event.Routes` only
   (`workflow/workflow.go:66-80`). A node that returns `"PASS"` sets
   `Event.Output` and matches nothing.
2. **A node emits exactly one routing event per activation.** A second sets
   `ErrMultipleRoutingEvents` and fails the node (`scheduler.go:765`).
3. **An error never takes the `on_fail` edge.** It marks the node failed,
   applies `RetryConfig`, then fails the whole workflow — there is no
   per-node continue-on-failure. **Failure is a value a stage reports, not an
   error it returns.**
4. **No match and no Default is a silent dead-end**, explicitly "not an error"
   (`scheduler.go:966`). Every routing node needs a Default edge.
5. **Unconditional edges always fire**, regardless of what matched
   (`scheduler.go:998`). See the compiler fix below.

Two supported body shapes: a `FunctionNode` whose fn returns a `*session.Event`
directly, or `NewEmittingFunctionNode` / `NewDynamicNode` that `emit`s the
routing event. The canonical ADK example sets **both** `ev.Routes` (picks the
successor) and `ev.Output` (feeds the successor's typed input).

## Compiler fix already landed

`wire()` added the `on_fail` edge outside the `switch` that adds `next`, so
`slices` carried an unconditional edge to `gates` **and** a FAIL edge to
`repair`. Because unconditional edges always fire, a failing stage would have
scheduled both — proceeding to verification of work that had just failed.

Fixed: a stage whose `on_fail` names another stage now gets its forward edge as
`workflow.Default`, which fires only when no concrete route matched — exactly
"next unless it failed". Pinned by
`TestFailoverStagesHaveNoUnconditionalForwardEdge`; `sopcheck` now prints
`slices -> gates [default]` beside `slices -> repair [FAIL]`.

Latent until now precisely because nothing executed the graph.

## Stage inventory

### run.sop.yaml — 7 stages, no reviews

| stage | node | body | routing |
|---|---|---|---|
| `validate_spec` | function | preflight validators (`run_preflight.go`) | `on_fail: abort` ⇒ no edge |
| `slices` | dynamic + `RunNode` per item | one `worker` per slice; `group_by: parallel_safe`, `max_concurrency: 3`, `isolation: sub_branch`, `output_schema: SliceResult`, `join` | FAIL → `repair`, else default → `gates` |
| `gates` | function | gate runner (`gates_from: PROMPT.md`, timeout 10m) | FAIL → `repair`, else default → `verify` |
| `verify` | agent (`code-reviewer`) | reviews the diff; `output_schema: Verdict` | PASS → `merge`, FAIL → `repair` |
| `repair` | dynamic fan-out over `verdict.unmet` | fix workers, `max_concurrency: 2` | `RECHECK` ⇒ `loop_back: verify`, `max_cycles: 10` |
| `merge` | function | worktree merge | → `summary` |
| `summary` | function | `SUMMARY.md` | terminal |

### plan.sop.yaml — 7 stages + 4 review checkpoints

| stage | node | body | routing |
|---|---|---|---|
| `clarify` | agent (`plan`) | → `requirements.md`; `min_qa(min:3)` | → `research` |
| `clarify.review` | **`kind: human`** | "Approve to continue to research?" | HITL pause |
| `research` | **dynamic fan-out** over `research_angles`, agent `explore`, `max_concurrency: 4`, `isolation: sub_branch`, `join: research_summary` | → `research/*.md`; `no_solution_language` | → `design` |
| `design` | agent (`plan`), `inputs: [requirements, research_summary]` | → `design.md`; `acceptance_criteria_are_given_when_then`, `research_at_least(min:2)` | → `outline` |
| `design.review` | **`kind: human`** | "Approve the design?" | HITL pause |
| `outline` | agent (`plan`) | → `outline.md`; `max_lines(120)`, `lists_slices(min:2)` | → `plan` |
| `outline.review` | **`kind: human`** | "Approve the outline?" | HITL pause |
| `plan` | agent (`plan`) | → `plan.md`; `slice_count(1..25)` | — |
| `plan.review` | `kind: agent` (`spec-reviewer`) | verdict | FAIL → `plan`, PASS → `prompt`, `max_cycles: 3` |
| `prompt` | agent (`plan`) | → `PROMPT.md`; gates executable, `done_criteria(min:3)` | → `manifest` |
| `manifest` | function | `.sop-manifest.json` | terminal |

**Three of the four reviews are `kind: human`**, so HITL is critical path, not a
later refinement.

## Visualization

Fixed 23-column sidebar (`SidebarWidth`), inner width 20. **Stage list on top,
text workflow diagram underneath**, for both commands — the list from
`Compiled.Order`, the diagram from `Compiled.GraphEdges()`.

```
 Run: my-spec              Plan: 024-tui-…
 ────────────────          ────────────────
 [x] validate_spec         [x] clarify
 [▶] slices                [x]  └ approved
 [ ] gates                 [x] research
 [ ] verify                [▶] design
 [ ] repair                [ ]  └ review
 [ ] merge                 …
 [ ] summary
 ────────────────          ────────────────
 ✔ validate_spec           ✔ clarify
 │                         └ ✔ review
 ▶ slices                  │
 └─✗→ repair               ✔ research
 │                         │
 ○ gates                   ▶ design
 └─✗→ repair               └ ⏸ review
 │                         │
 ○ verify                  ○ outline
 ├─✗→ repair               └ ○ review
 └─✓→ merge                │
 │                         ○ plan
 ○ repair                  └ ○ review
 └─↺→ verify               ├─✗→ plan
 │                         └─✓→ prompt
 ○ merge                   │
 │                         ○ prompt
 ○ summary                 │
                           ○ manifest
```

Glyphs: `✔` completed · `▶` running · `○` inactive · `✗` failed · `⏸` waiting
(HITL) · `↺` loop-back edge. Implemented in `internal/tui/sop_graph.go`, with
width pinned against the real `SidebarWidth`.

### Where status comes from

**Live `RunState` is not readable.** It is created inside `newScheduler` and
held on the unexported scheduler; `Run`/`RunNode` never expose it, and
`ReconstructRunState` re-scans session history for a *paused* run only.

So the sidebar consumes the **event stream**: `NodeInfo.Path` identifies the
node, `Output`/`OutputFor` its result, `RequestedInput` a pause. **Node start is
not an event** — "running" is inferred from a node's first emitted event, or
from our own graph edges once the predecessor completes. Node bodies are ours,
and a body runs only when the scheduler activates it, so a lifecycle event
emitted at entry is engine truth.

### Degradation, in priority order — the list always wins

1. Not enough rows ⇒ drop the diagram **whole**. `sidebarFrame` clips from the
   bottom (`sidebar.go:517`), so the caller must measure and drop; the renderer
   cannot self-limit.
2. SOP fails to compile ⇒ list only, plus one dim line; no crash.
3. Below 80 columns the sidebar is hidden entirely (`mainWidth`, tui.go:1958) —
   the width-based "too narrow" case needs no handling.

## Graph source: embedded only

Compiled from the **embedded** SOPs via `sop.LoadEmbeddedDefinition` (added;
`resolveDefinition` is unexported and always probes `~/.pi-go/sops/`). Overrides
stay off until the engine is the only executor — until then an override would
draw a pipeline that is not running. Flipping back to `LoadDefinition` is a
one-line change at one call site.

## Engine constraints the factory must respect

| # | Constraint | Consequence |
|---|---|---|
| E1 | `NodeConfig.ParallelWorker` is a **dead field** — the scheduler never reads it | Real parallelism needs a `NewParallelWorker` node. `compile.go` now documents this |
| E2 | `NewParallelWorker` rejects a wrapped node carrying `RetryConfig`, and `NodeConfigFor` always sets one from `defaults.retry` | Every fan-out stage (`slices`, `repair`, `research`) fails to construct unless retry moves to the parent — or fan-out is hand-rolled with `DynamicNode` + `RunNode` |
| E3 | `NewParallelWorker` suppresses intermediate events and emits one aggregate | Worker output would vanish from the TUI. **Decision: `DynamicNode` + `RunNode`**, factory-enforced concurrency, `emit` for streaming |
| E4 | `ParallelWorker` input must be an actual Go slice | The factory assembles `plan.slices` / `research_angles` / `verdict.unmet` itself; nothing resolves them |
| E5 | `ErrDuplicateEdge` is `(From,To)`-keyed regardless of route | Two route values at one target must merge into one `MultiRoute` edge |
| E6 | HITL handoff resumes with `event = nil`, so no concrete route can fire | An approval gate that routes on the answer needs `RerunOnResume: &true` |
| E7 | `startNode` overwrites `runsByName` with no in-flight check | `repair` has three conditional predecessors; two matching concurrently double-activate it. Routes into a shared target must be provably exclusive, or the target must be a `JoinNode` |
| E8 | No run-time cycle cap exists; `NodeState.Attempt` is retry-only and resets on success | `max_cycles` must be enforced by us, in **invocation-scoped, resume-safe** state — not on the factory struct, which serves concurrent sessions |
| E9 | At most one terminal output per run (`ErrMultipleTerminalOutputs`) | A graph whose several terminal nodes all produce output fails at finalize |

`ErrUnsupportedFanIn` is **not** a blocker: `validateFanIn` counts only
unconditional incoming edges, and every back-edge the compiler builds carries a
route. That is why `Workflow()` already succeeds for both SOPs.

## Feasibility of reusing the imperative code

| Area | Verdict | Note |
|---|---|---|
| Gates | feasible | `runGates`/`runGatesTimeout`/`runOneGate` are already package-level and `tea`-free. Put them in a leaf `internal/sop/gates`, not in `exec`, so the retained imperative path doesn't drag `agent`+`subagent` into the TUI. No cycle: `internal/sop` already imports `internal/subagent`; neither imports `internal/tui` |
| Preflight | feasible | Needs only `workDir`. Gap: `--force` has no graph equivalent — it becomes node *input* |
| Merge | needs refactor | `mergeRunTargets` reusable verbatim; `mergeTargets()` is not — it encodes three worktree shapes, and a `repair` loop adding trees per cycle is a fourth |
| Summary | feasible, caveat | Helpers are package-level, but `*runState` **is** the report's data model |
| Sidebar | feasible | One production call site (`tui.go:1732`) |
| Flag | feasible | Follow `PI_NO_GROUNDING` (`agent/grounding.go:186`). Branch `/run` at `run.go:449`, `/plan` inside `startPlanSession` |
| **/plan** | blocked as designed | see below |

### /plan is the hard half

The small half is ~100 lines: split `buildRunner` into `buildLLMAgent` + the
runner wrapper (the `llmagent` is currently a discarded local), add a
`StageAgent(name, description, instruction, tools)` constructor, and add a
`RunStreamingContent` entry point — resuming a paused workflow means submitting
a `FunctionResponse` targeting the interrupt id, which today's text-only
`RunStreaming` cannot express. Doing this also retires
`RebuildWithInstruction`, which today discards and rebuilds the whole runner
mid-session.

What blocks it:

1. **pi-go has no HITL machinery at all** — zero production references to
   `ResumeOrRequestInput`, `Workflow.Resume`, or `ReconstructRunState`.
2. **`finishPlanWorktree` fires on every turn boundary** (`agent_loop.go:1729`),
   so each HITL resume would fire it while the graph is parked at a review.
3. **The plan session is write-only w.r.t. resume**: `PlanContext.Phase` is the
   constant `"plan"`, `GetPlanContext` has no production caller, and
   `handlePlanCommand` "resumes" by re-entering `startPlanSession` from scratch.
4. **`preTurn` compaction spans the wrong unit** — once per `RunStreaming` turn,
   while a workflow turn spans many stage calls.

## Migration

- `PI_SOP_ENGINE=1` selects the engine; default stays imperative.
- **`/run` first, `/plan` second.** They are not the same size of problem.
- Parity gate: the same spec through both paths produces the same artifacts,
  verdict, and merge decision — and **the parity test must cover all six
  terminal outcomes** (`agent_failed`, `verify_failed`, `gate_failed`,
  `gate_hang`, `merge_failed`, `completed`), not just the happy path. The engine
  collapses them to one error unless the factory attributes each failure, and
  `SUMMARY.md` would silently degrade to a generic message.
- **No behaviour change on cycles:** `repair`'s `max_cycles` is now 10, the same
  budget `runState.maxRetries` gives today.

## Test plan

- **Factory unit tests** — each body with a fake orchestrator: gates FAIL routes
  to `repair`, verdict PASS routes to `merge`, `repair` emits `RECHECK`,
  `max_cycles` terminates the loop.
- **Graph traversal** — stub factory whose nodes record their names; assert the
  visited order for happy path, gate failure, verdict FAIL, loop exhaustion.
- **Routing contract** — a node that returns a verdict *without* emitting
  `Routes` must be caught by a test, since the engine's response is a silent
  dead-end.
- **Sidebar renders** — per status glyph, loop-back present, diagram dropped
  (not clipped) when rows are short, list-only when compile fails. *(done)*
- **Guard tests** — embedded stage ids, plan review kinds, no unconditional
  forward edge on a failover stage. *(done)*
- **Parity** — one fixture spec through both paths, all six outcomes.

## Decisions taken

### `repair` keeps a budget of 10, not 3

`run.sop.yaml` now declares `max_cycles: 10`, matching today's
`runState.maxRetries = 10`. The SOP's original `3` would have silently cut the
repair budget on migration; raising it makes the engine path a port rather than
a behaviour change, and the parity gate can compare like with like.

### One worktree per run, owned by the workspace — not one per agent

This is what `workspace.worktree: per-run` already declares, and it is the
better model. Today the spawner creates a worktree **per agent**
(`Worktree: new(true)` at each spawn site, `SkipCleanup: true`), which is what
forces the surrounding machinery: `mergeTargets()` handling three shapes,
`collapseParallel` / `carried` / `ownerBackup` moving trees around on retry, one
backup ref per agent, and `worktreeAgentID` bookkeeping to remember which tree
gates and merge belong to.

Under one tree per run:

- the factory creates the worktree once at run start; every stage — slice
  workers, repair workers, gates, verify, merge — works inside it, and workers
  spawn with `WorkDir: <run worktree>` and no `Worktree` flag of their own;
- `mergeTargets()`'s three shapes collapse to one tree to merge, and the
  retry-shuffling machinery has nothing left to do;
- **the repair loop stops being a problem.** Per-agent trees meant every repair
  cycle spawned workers with new trees the merge step had never heard of — the
  "fourth shape" that had no equivalent in the current merge logic. Sharing one
  tree removes the question;
- gates run where the work is, with no agent-id lookup, and one backup ref
  covers the run;
- `WorktreeManager.Create` serialises on a mutex and stashes the primary
  checkout on every call, so creating one tree instead of N also removes that
  per-agent round trip.

**The cost, stated plainly:** parallel slice workers now share a working tree,
so isolation moves from "separate checkouts" to "disjoint file sets". That
precondition is already declared and validated — `parallel_safe` on each slice,
`every_slice_has(["files", "verify", "parallel_safe"])`, and
`slice_budget(max_files: 10)` — but it is now load-bearing rather than advisory.
Two consequences to design for: concurrent workers can contend on the git index,
and a failed worker's partial edits can no longer be discarded by dropping its
tree.

Mitigation: keep `fan_out.max_concurrency`, but gate concurrent dispatch on the
slices' declared file sets actually being disjoint, and fall back to sequential
dispatch within the tree when they intersect. That is a check the coordinator
prompt used to ask for in prose and nobody enforced.

## Open questions

1. ~~Worktrees vs engine branches.~~ **Resolved above** — one worktree per run,
   owned by the workspace. Note the ADK half is unchanged: `WithUseSubBranch`
   scopes session event history and `WithIsolationScope` scopes LLM history;
   **neither creates a git worktree**, so `WorktreeManager` still runs. `WithUseSubBranch` scopes session event
   history and `WithIsolationScope` scopes LLM history; **neither creates a git
   worktree**. `isolation: sub_branch` cannot replace `WorktreeManager` — both
   must run. Ownership, merge order, repair placement, conflict handling, and
   cleanup after partial failure are unresolved, and `collapseParallel` /
   `carried` / `ownerBackup` is retry state with no engine equivalent.
2. **Where the cycle budget lives** so it is invocation-scoped and resume-safe.
3. **Per-node token accounting** — nice-to-have, not required for parity.
