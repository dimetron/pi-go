# Requirements: Graph-Based Agents for pi-go

## Questions & Answers

### Q1: What is the scope — replace the subagent tool, or add graph orchestration alongside it?

**Answer:** Add graph orchestration **alongside** the existing subagent tool. The `subagent`
tool stays for LLM-initiated single spawns (the right primitive for "spawn an explore
agent"). The graph engine takes over the **deterministic pipelines** that pi-go currently
hand-rolls: the `/run` spec workflow, parallel research fan-out, and review loops.

### Q2: Should subagents become in-process ADK agents, or stay as `pi --mode json` subprocesses?

**Answer:** Stay as subprocesses. The spawner gives real isolation (sandbox, worktrees,
crash containment) that in-process `AgentNode`s don't. The graph wraps the **existing
spawner** as a node body — graph orchestration on top of process isolation. This is the
key architectural decision: `AgentNode`-style graph nodes whose `Run` calls
`Orchestrator.SpawnWithInput` and streams events.

### Q3: Which pipelines become graphs first?

**Answer:** Three, in order of value:

1. **`/run` spec workflow** (`internal/tui/run.go`) — task agent → gate check → retry/merge.
   Biggest win: replaces ~1100 lines of bespoke retry/parallel/gate/merge logic.
2. **Parallel research fan-out** — explore agents → JoinNode → synthesize. Replaces the
   LLM hand-rolling `{tasks:[...]}` JSON and the `maxParallelTasks`/`maxChainSteps` caps.
3. **Review loop** — task → code-reviewer → fix (loopagent or graph cycle).

### Q4: What ADK version does pi-go need?

**Answer:** Upgrade `google.golang.org/adk/v2` from v2.1.0 to v2.2.0+ (the tmp checkout is
v2.2.0-4-g817fdc0). The workflow package exists in v2.1.0, but v2.2.0+ includes fixes
pi-go wants for graph adoption: external context cancellation propagation (#1258) and
nested workflow HITL resume (#1129).

### Q5: How do graph nodes wrap the subprocess spawner?

**Answer:** A `SpawnNode` (custom `workflow.Node` embedding `BaseNode`) whose `Run`
calls `Orchestrator.SpawnWithInput`, forwards events via `emit`, and returns the
aggregated result as `Event.Output`. The node carries the `AgentConfig` (name, role,
worktree, timeout) as configuration. Retries move from the orchestrator's
`SpawnWithRetry` into the graph's `NodeConfig.RetryConfig`.

### Q6: What happens to the existing retry/parallel code in `internal/tui/run.go`?

**Answer:** It is **replaced**, not kept. The graph engine's `RetryConfig` (backoff,
jitter, `ShouldRetry`), `NodeConfig.Timeout`, `JoinNode` fan-in, and `WithMaxConcurrency`
cover the same behavior. The TUI keeps only the event-streaming and gate-merge glue.
The `runState` struct's `maxRetries`/`retries`/`collapseParallel` fields go away.

### Q7: Does the graph engine give cross-turn resume for `/run`?

**Answer:** Yes — this is a headline benefit. `Workflow.RunState` persists in
`session.State` (pi-go's `internal/session/store.go` already implements `session.State`),
so a paused/interrupted `/run` can resume on a follow-up turn. This subsumes the separate
`/plan resume` mechanism (`specs/features/SOP/plan-resume/`) for the run half.

### Q8: What about the `workflowagents` (sequential/parallel/loop) packages?

**Answer:** Use them for simple compositions. `sequentialagent` replaces the hand-rolled
"chain" mode; `loopagent` gives the review loop; `parallelagent` is a ready-made fan-out.
The llm-auditor sample (`adk-samples/go/agents/llm-auditor/`) shows the critic→reviser
pattern. For richer graphs (routing, joins, HITL), use the `workflow` package directly.

### Q9: Should graph templates be exposed to the LLM?

**Answer:** Yes, as a small set of named templates on the `subagent` tool (e.g.
`research` = fan-out explores → JoinNode → synthesize; `review` = task → code-reviewer →
fix loop). The LLM picks a template by name; the engine handles concurrency, joins, and
routing. This is the "graph based agents" ask made concrete.

### Q10: What from kagent should pi-go borrow?

**Answer:** Patterns, not code. kagent shows production ADK wiring: A2A subagents with
HITL propagation and live activity viewing (`docs/architecture/a2a-subagents.md`), an ACP
shim for harness backends (`go/core/pkg/acpshim/`), and memory tools
(`go/adk/pkg/memory/`). pi-go already has A2A tools (`internal/tools/a2a.go`) and ACP
subagents (claude/gemini/cursor/copilot) — the kagent design docs are a reference for
exposing those as first-class graph nodes.

### Q11: What should pi-go NOT adopt from tmp/adk?

**Answer:**

- **Don't replace the subprocess spawner with in-process agents** — isolation is the
  point of the spawner.
- **Don't adopt `adk-utils-go` wholesale** — pi-go already has its own memory
  (`internal/memory/`, `internal/palace/`), session, and provider layers. The utils repo
  is a reference for Redis/Postgres persistence if pi-go ever needs it.
- **Don't adopt kagent's Kubernetes CRD model** — pi-go is a local CLI/TUI agent, not a
  K8s controller.

### Q12: What is the acceptance bar for the first slice?

**Answer:** The `/run` graph must be behaviorally equivalent to today's `/run`:
same gates, same retry semantics (max 10 cycles), same parallel split (2 agents), same
merge behavior — but implemented as a declared graph with the engine doing retries/joins,
and with cross-turn resume working. Existing `internal/tui/run_*_test.go` tests must pass
(possibly adapted to the new structure).
