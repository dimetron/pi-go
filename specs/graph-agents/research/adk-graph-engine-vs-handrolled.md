# Research: ADK Graph Engine vs pi-go's Hand-Rolled Orchestration

## Source

Review of `tmp/adk/` (adk-go v2.2.0-4-g817fdc0, adk-samples, adk-utils-go, kagent)
against pi-go's current architecture. August 2026.

## Finding 1: pi-go already runs on ADK — but only the agent loop

pi-go pins `google.golang.org/adk/v2 v2.1.0` (`go.mod:43`) and builds a single
`llmagent` + `runner.Runner` in `internal/agent/agent.go:368-385`. The `tmp/adk/adk-go`
checkout is v2.2.0-4-g817fdc0 — the same repo, with the full `workflow/` graph package
that pi-go **never imports** (zero references to `adk/v2/workflow` outside `tmp/`).

The gap isn't "adopt ADK" — it's "adopt ADK's **graph engine** for orchestration, which
pi-go currently reimplements by hand as process spawning."

## Finding 2: What the graph engine gives (that pi-go hand-rolls today)

| ADK workflow primitive | pi-go's current equivalent | Where pi-go hand-rolls it |
|---|---|---|
| `RetryConfig` (backoff, jitter, `ShouldRetry`) | `maxRetries: 10` retry loop | `internal/tui/run.go:880-920` (`retryRun`) |
| `NodeConfig.Timeout` | per-agent timeout plumbing | `internal/subagent/orchestrator.go:438-441` |
| `JoinNode` fan-in barrier | manual parallel split + fan-in | `internal/tui/run.go:462-520` (`handleRunParallel`) |
| `Route`/`StringRoute` LLM-as-router | LLM manually picks subagent mode via JSON | `internal/tools/subagent.go:164-200` |
| `RequestInput`/`Resume` (HITL) | no orchestration-level pause | — |
| `ParallelWorker` + per-item sub-branches | `sync.WaitGroup` + pool semaphore | `internal/tools/subagent.go:317-456` |
| `WithMaxConcurrency` | `Pool` semaphore | `internal/subagent/pool.go` |
| `RunState` persisted in `session.State` | no cross-turn orchestration resume | — |
| `AgentNode` wrapping any `agent.Agent` | spawn `pi --mode json` subprocess | `internal/subagent/spawner.go:102-130` |

The engine also has validation pi-go lacks: cycle detection, unreachable nodes,
duplicate names, chat-mode wiring rules (`workflow/validation.go:197-240`).

## Finding 3: The architectural insight

pi-go's orchestration is **process-based and prompt-driven**: the LLM decides pipeline
shape at runtime by calling the `subagent` tool with `{tasks:[...]}` or `{chain:[...]}`
JSON, and each subagent is a fresh `pi` subprocess. ADK's workflow engine is
**graph-based and code-driven**: the pipeline is declared as nodes+edges, and the engine
executes it deterministically with retries, routing, joins, branch isolation, and
persistence.

They are complementary, not competing:

- **Keep** the `subagent` tool for LLM-initiated *single* spawns (the right primitive
  for "spawn an explore agent").
- **Move the deterministic pipelines into graphs**: parallel research (fan-out explore →
  JoinNode → synthesize), the `/run` spec workflow (task → gate → retry/merge), and
  plan→run→review.

## Finding 4: ADK version gap

The tmp checkout is 4 commits past v2.2.0 and includes workflow fixes pi-go would want
for graph adoption:

- `8bfc9ca` fix(workflow): propagate external context cancellation (#1258)
- `79b22d9` fix(runner): preserve nested workflow HITL resume responses (#1129)

The `workflow` package exists in v2.1.0, so the upgrade is a hardening step, not a
prerequisite for the API surface.

## Finding 5: What NOT to adopt

- **Don't replace the subprocess spawner with in-process agents.** The `pi --mode json`
  subprocess model gives real isolation (sandbox, worktrees, crash containment) that
  in-process `AgentNode`s don't. The right move is `SpawnNode` wrapping the *existing
  spawner* as a node body — graph orchestration on top of process isolation.
- **Don't adopt `adk-utils-go` wholesale** — pi-go already has its own memory
  (`internal/memory/`, `internal/palace/`), session, and provider layers; the utils repo
  is a reference for Redis/Postgres persistence if pi-go ever needs it.
- **Don't adopt kagent's Kubernetes CRD model** — pi-go is a local CLI/TUI agent, not a
  K8s controller.

## Finding 6: kagent patterns worth referencing

kagent (`tmp/adk/kagent`) shows production ADK wiring:

- **A2A subagents with HITL propagation and live activity viewing**
  (`docs/architecture/a2a-subagents.md`) — pi-go already has A2A tools
  (`internal/tools/a2a.go`); the doc shows how to expose remote agents as first-class
  graph nodes with session continuity.
- **ACP shim** (`go/core/pkg/acpshim/`) — pi-go already spawns ACP subagents
  (claude/gemini/cursor/copilot); the shim pattern is a reference for wrapping them as
  graph nodes.
- **Memory tools** (`go/adk/pkg/memory/`) — reference for agent-controlled memory
  operations; pi-go already has `internal/memory/` and `internal/palace/`.

## Finding 7: The /run workflow is the biggest single win

`internal/tui/run.go` is ~1100 lines of hand-rolled orchestration: spawn task agent,
retry up to 10×, parallel split into 2 agents, gate checks, merge, backup branches. As
a graph it becomes: `Start → taskAgent(worktree) → gateCheck(FunctionNode) → [retry
loop via RetryConfig | merge] → done`. The engine's `RetryConfig` + `NodeConfig.Timeout`
+ `JoinNode` replace the bespoke retry/parallel code, and `RunState` persistence gives
cross-turn resume for free (the `/plan resume` feature in
`specs/features/SOP/plan-resume/` is currently a separate hand-rolled mechanism).

## Finding 8: workflowagents for simple compositions

`agent/workflowagents/` in the checkout has ready-made `sequentialagent`,
`parallelagent`, `loopagent`. The llm-auditor sample
(`adk-samples/go/agents/llm-auditor/auditor/auditor.go`) shows the critic→reviser
pattern. pi-go's "chain" mode is a hand-rolled sequential agent.

## Sources

- `tmp/adk/adk-go/workflow/` — graph engine source
- `tmp/adk/adk-go/agent/workflowagent/` + `agent/workflowagents/`
- `tmp/adk/adk-go/examples/workflow/` — basic, routing/llm, dynamic/llm, complex,
  hitl_simple, hitl_rerun
- `tmp/adk/adk-samples/go/agents/llm-auditor/` — sequential critic→reviser
- `tmp/adk/adk-utils-go/README.md` — Redis sessions, Postgres memory, contextguard
- `tmp/adk/kagent/docs/architecture/a2a-subagents.md` — A2A subagent HITL
- `tmp/adk/kagent/go/core/pkg/acpshim/` — ACP shim
- `internal/tui/run.go`, `internal/tools/subagent.go`, `internal/subagent/orchestrator.go`
- `specs/features/SOP/plan-command-sop/` — existing /plan + /run PDD pipeline
