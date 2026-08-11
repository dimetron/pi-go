# Summary: Graph-Based Agents for pi-go

## Artifacts

| File | Description |
|------|-------------|
| `specs/graph-agents/rough-idea.md` | Original rough idea and reference to tmp/adk |
| `specs/graph-agents/requirements.md` | 12 Q&A decisions defining scope and boundaries |
| `specs/graph-agents/DESIGN.md` | Detailed design with architecture, components, acceptance criteria |
| `specs/graph-agents/plan.md` | 8-step incremental implementation plan |
| `specs/graph-agents/summary.md` | This file |

## Overview

pi-go already runs on Google ADK Go (`google.golang.org/adk/v2 v2.1.0`) but uses only the
**agent loop** (`llmagent` + `runner`). The ADK **workflow graph engine** (`adk/v2/workflow`)
ships in the same module and is never imported. pi-go hand-rolls all orchestration as
process spawning and prompt-driven JSON pipelines.

This design adopts ADK's graph engine as the orchestration layer — **on top of** the
existing subprocess spawner, not replacing it. Deterministic pipelines (the `/run` spec
workflow, parallel research fan-out, review loops) become declared graphs with retries,
joins, routing, and cross-turn persistence. LLM-initiated single spawns stay as-is.

## Key Decisions

1. **Graph on top of subprocesses, not in-process agents.** `SpawnNode` wraps the
   existing `Orchestrator.SpawnWithInput` as a workflow node body; isolation (sandbox,
   worktrees, crash containment) is preserved.
2. **Deterministic pipelines become graphs; LLM-initiated single spawns stay tools.**
   The `subagent` tool keeps single mode; named templates (`research`, `review`,
   `sequential`) route the deterministic shapes through the engine.
3. **Retries, timeouts, and concurrency move into the engine.** `RetryConfig`,
   `NodeConfig.Timeout`, and `WithMaxConcurrency` replace the hand-rolled
   `SpawnWithRetry` loop, per-agent timeout plumbing, and per-call caps.
4. **Cross-turn resume comes from `RunState` persistence.** pi-go's session store already
   implements `session.State`; the workflow's `RunState` persists with no new storage
   work. This subsumes the `/plan resume` mechanism for the run half.
5. **TUI stays thin.** Event streaming, gate parsing/execution, and merge bookkeeping
   remain in the TUI; orchestration state machines move to the graph.

## First Slice

Step 3-5 of the plan: rebuild the `/run` spec workflow as a declared graph
(`START → taskAgent → gateCheck → merge`, with `RetryConfig` for the retry loop and
`JoinNode` for the parallel split). Acceptance bar: behaviorally equivalent to today's
`/run` — same gates, same retry semantics (max 10 cycles), same parallel split (2
agents), same merge behavior — but graph-driven, with cross-turn resume working.

## Out of Scope

- Replacing the subprocess spawner with in-process agents.
- Adopting `adk-utils-go` (Redis sessions, Postgres memory) — pi-go has its own
  memory/session layers.
- Adopting kagent's Kubernetes CRD model.
- Migrating pi-go's own agents to ACP servers.
- Graph-based memory (kagent EP-1256 lists it as a non-goal too).
