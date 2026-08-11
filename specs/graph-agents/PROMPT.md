# Adopt ADK Graph Engine for pi-go Orchestration

## Objective

Adopt Google ADK Go's workflow graph engine (`google.golang.org/adk/v2/workflow`) as
pi-go's orchestration layer, on top of the existing subprocess spawner. Rebuild the
hand-rolled `/run` spec workflow and the `subagent` tool's parallel/chain modes as
declared graphs with retries, joins, routing, and cross-turn resume. Full design in
`specs/graph-agents/DESIGN.md`.

## Key Requirements

1. **ADK Upgrade** — Bump `google.golang.org/adk/v2` from v2.1.0 to v2.2.0+ in `go.mod`
   (tmp checkout is v2.2.0-4-g817fdc0; includes workflow fixes #1258, #1129).

2. **SpawnNode** — New `internal/graph/spawn_node.go`: a custom `workflow.Node` that
   calls `Orchestrator.SpawnWithInput`, streams events via `emit`, and returns the
   result as `Event.Output`. Retries move from `SpawnWithRetry` into
   `NodeConfig.RetryConfig`; timeouts move into `NodeConfig.Timeout`.

3. **/run Graph** — Rebuild `internal/tui/run.go` orchestration as a declared graph:
   `START → taskAgent(SpawnNode, worktree) → gateCheck(FunctionNode) → merge`, with
   `RetryConfig` (max 10, matching today) for the retry loop and `JoinNode` for the
   parallel 2-agent split. TUI keeps event streaming, gate parsing/execution, and
   merge bookkeeping. Remove `retryRun`, `handleRunParallel`'s manual fan-in,
   `collapseParallel`, and the `maxRetries`/`retries` fields.

4. **Cross-turn resume** — The workflow's `RunState` persists in `session.State`
   (pi-go's store already implements it). Interrupted `/run` resumes on a follow-up
   turn via `Workflow.Resume`. Subsumes `/plan resume` for the run half.

5. **Graph templates** — Add `template` mode to the `subagent` tool: `research`
   (fan-out explores → JoinNode → synthesize), `review` (task → code-reviewer → fix
   loop), `sequential` (chain of spawns). Use `WithMaxConcurrency` as the real
   concurrency limiter; remove `maxParallelTasks`/`maxChainSteps` caps.

6. **HITL** — `gateCheck`/approval nodes emit `RequestInput` and park the graph until
   the user answers; resume continues from the paused node.

## Acceptance Criteria

### ADK Upgrade
- Given `go.mod` pins v2.2.0+, when `make build` runs, then the binary builds.
- Given the workflow package is imported, when `go vet` runs, then no new findings.

### SpawnNode
- Given a `SpawnNode` wrapping the orchestrator, when the node runs, then it spawns a
  subagent, streams events, and returns the result as `Event.Output`.
- Given a failing subagent, when `RetryConfig` is set, then the node retries per the
  policy (backoff, jitter, `ShouldRetry`).
- Given a timeout in `NodeConfig.Timeout`, when the subagent exceeds it, then the node
  fails with a timeout error.

### /run Graph
- Given a valid spec with PROMPT.md, when `/run spec-name` executes, then the task
  agent spawns in an isolated worktree and events stream to the TUI in real-time.
- Given the agent completes and gates pass, then the worktree branch auto-merges.
- Given a gate fails with retries remaining, then the graph retries per `RetryConfig`
  in the same worktree.
- Given a gate fails past the retry budget, then the worktree is left intact and the
  failure is reported with the path.
- Given an interrupted run, when the user resumes the session, then the graph resumes
  from the paused node (RunState persistence).
- Given no `## Gates` section, then merge proceeds without validation.
- Given `specs/{spec-name}/PROMPT.md` does not exist, then an error shows with the list
  of available specs.
- Existing `internal/tui/run_*_test.go` tests pass (adapted to the new structure).

### Templates
- Given `{template: "research", topic: ..., agents: [...]}`, when the subagent tool
  runs, then N explore agents fan out, a JoinNode gathers results, and a synthesize
  agent merges them.
- Given `{template: "review", ...}`, when the tool runs, then task → code-reviewer →
  fix loop executes until approved or the loop budget is exhausted.
- Given a template with more agents than `WithMaxConcurrency`, when it runs, then
  concurrency is capped by the engine, not by a per-call constant.

### HITL
- Given a `gateCheck` node that emits a `RequestInput`, when the graph reaches it, then
  the graph parks and the TUI renders the prompt.
- Given the user answers, when the turn resumes, then the graph continues from the
  paused node.

## Gates

- `make build`
- `make test-unit`
- `go vet ./...`
- `make test-all` (after Step 8)

## Reference

- `specs/graph-agents/DESIGN.md` — full design (architecture, components, decisions)
- `specs/graph-agents/plan.md` — 8-step implementation plan with per-step tests
- `tmp/adk/adk-go/workflow/` — graph engine source (scheduler, state, persistence,
  retry, validation, edgebuilder, dynamic_node, join_node, parallel_worker, run_node)
- `tmp/adk/adk-go/agent/workflowagent/` — `workflowagent.New` (agent wrapper)
- `tmp/adk/adk-go/agent/workflowagents/` — `sequentialagent`, `parallelagent`, `loopagent`
- `tmp/adk/adk-go/examples/workflow/` — basic, routing/llm, dynamic/llm, complex
  (fan-out/fan-in research pipeline), hitl_simple, hitl_rerun
- `tmp/adk/adk-samples/go/agents/llm-auditor/` — sequential critic→reviser
- `tmp/adk/kagent/docs/architecture/a2a-subagents.md` — A2A subagent HITL + live activity
- `internal/tui/run.go` — current hand-rolled /run orchestration
- `internal/tools/subagent.go` — current subagent tool with single/parallel/chain modes
- `internal/subagent/orchestrator.go` — spawner, pool, worktree, retry

## Constraints

- **Keep the subprocess spawner.** Do not replace `pi --mode json` subprocesses with
  in-process agents — isolation (sandbox, worktrees, crash containment) is the point.
- **Don't adopt `adk-utils-go`** — pi-go has its own memory/session/provider layers.
- **Don't adopt kagent's Kubernetes CRD model** — pi-go is a local CLI/TUI agent.
- **Worktree discipline** — work in a git worktree (`.worktrees/graph-agents/`), commit
  with `-s -S`, never `--no-verify`.
- **Don't delete code you didn't create** — replace `internal/tui/run.go` orchestration
  incrementally; keep gate/merge helpers until the graph path is proven.
