# Research: Execution Path — Is the Compiled SOP Workflow Executed?

## Finding (critical)

**The compiled ADK workflow graph is NOT executed by `/run` or `/plan`.** Both commands
still run the imperative code path. The graph is dead code from the TUI's perspective —
it is only compiled and validated by a CLI check tool and unit tests. There is no
executable `NodeFactory` in the repo; only the non-executing `DescribeFactory`.

This contradicts the assumption (Q5) that "the compiled workflow execution is already
wired in and only the TUI visualization is missing." The visualization must therefore be
a **derived rendering of the SOP's declared structure**, with the active stage driven by
stage events emitted from the imperative coordinator/plan-agent path — not by a live
workflow-engine execution.

## Evidence

### `/run` — `handleRunCommand` / `startRunAgent` (internal/tui/run.go)
- `handleRunCommand` (run.go:394) parses args, reads PROMPT.md, runs preflight, parses
  gates/checklist, dispatches to `startRunAgent` (run.go:454) or `handleRunParallel`
  (run.go:451). Never touches `sop.Compile`/`Workflow`.
- `startRunAgent` (run.go:511) spawns the worker directly via the Orchestrator:
  - run.go:517 — `m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{Type: "task", ...})`
  - run.go:582,600 — parallel spawns the same way.
  - run.go:1048 — retry path.
- The run state machine is an imperative TUI event loop (`handleRunAgentEvent` run.go:717,
  `handleRunAgentDone` run.go:836, `handleRunGateResult` run.go:1289, `handleRunMergeResult`
  run.go:1479). The only `sop` usage in run.go is `sop.ParseVerdict` (run.go:959) and
  `sop.BuildManifest`/`sop.WriteManifest` (run_preflight.go:61-62) — helpers, not graph execution.

### `/plan` — `handlePlanCommand` / `startPlanSession` (internal/tui/plan.go)
- `startPlanSession` (plan.go:392) loads the PDD SOP **as instruction text**, not a graph:
  - plan.go:398 — `sop.LoadPDD(workDir)` returns a string (the PDD prompt).
  - plan.go:408-417 — concatenates into an `instruction` string.
  - plan.go:428 — `m.cfg.Agent.RebuildWithInstruction(instruction)`.
  - plan.go:483 — `m.startAgentLoop(roughIdea)` — the imperative agent loop.
- No `sop.Compile`/`Workflow` anywhere in plan.go.

### Callers of `sop.Compile` / `Compiled.Workflow` / the workflow package
- `sop.Compile` callers: `hack/sopcheck/describe.go:17` (CLI check tool) and
  `internal/sop/schema_test.go` (unit tests).
- `Compiled.Workflow()` callers: `internal/sop/schema_test.go:36` only. **No production caller.**
- `google.golang.org/adk/v2/workflow` import: only inside `internal/sop` (compile.go:7,
  describe.go:11). No TUI or other production package imports it.
- The only `NodeFactory` is `DescribeFactory` (internal/sop/describe.go:22), which is
  explicitly non-executing: `describeNode.Run` (describe.go:73-77) returns
  `"node %q is a description only; compile with a real NodeFactory to execute"`.
  Its doc comment (describe.go:19-21) says "Phase 3 supplies the factory that runs real
  agents" — that factory does not exist.

## Implication for design
- The sidebar graph is a **visualization of the SOP's declared structure** (stages, edges,
  routes, loop-backs), compiled with `DescribeFactory` (works without a provider).
- The **active stage** is driven by `stage_start`/`stage_end` events emitted by the
  imperative coordinator (run) and plan agent (plan) on the existing event channel.
- No workflow-engine execution is required for this feature.
