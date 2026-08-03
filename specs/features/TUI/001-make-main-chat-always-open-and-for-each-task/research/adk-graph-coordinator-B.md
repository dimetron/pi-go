# Design B — ADK Chat Coordinator + Task-Mode Sub-Agents (In-Process)

Status: Design candidate for comparison
Related research: `adk-graph-coordinator.md`

## Problem

Make the main chat a persistent coordinator that always routes each task to a
subagent session, with enforced delegation and support for long-running tasks.

## Current state (facts)

- ADK v2 `runner.Run` natively supports a chat root agent with `ModeTask`
  sub-agents: each task sub-agent is installed as a `task` tool on the
  coordinator; the wrapper (`agent/llmagent/llm_agent_wrapper.go` `runChat`)
  scans for unresolved task FCs, dispatches each via `workflow.RunNode`, feeds
  results back, and loops until the LLM finishes without delegating.
- pi-go today does NOT use this. It wires a plain chat `LlmAgent` + a custom
  `subagent` tool backed by `subagent.Orchestrator`, whose sub-agents are
  **external OS processes** (pi re-invocation, codex, ACP).
- ADK `ModeTask` sub-agents are **in-process ADK agents** (same Go process,
  model-driven). Different execution model from pi-go's external spawners.
- One `runner.Run` call runs one user turn; internal task delegations run within
  that single call via `RunNode`. `ParallelWorker`/branch nodes give concurrency
  inside a run.

## Desired end state

- Root agent becomes a chat coordinator whose `SubAgents` are task-mode agents.
- Each incoming prompt is delegated: the coordinator chooses a task sub-agent,
  dispatches it, waits for `finish_task`, and can continue.
- Long-running tasks get structured lifecycle (spawn → run → finish_task) with
  resume/HITL support from ADK.

## Architecture

```
runner.Run (chat coordinator, 1 call)
   │   loop: LLM turn
   ├─ task FC for sub-agent S ─► RunNode(S, fc.Args) ─► finish_task FR
   │        ▲                          │
   └────────┴── result fed back ───────┘
   loop ends when LLM stops delegating
```

Two bridging options for pi-go's external sub-agents:

- **B1 (adapter):** Keep external spawners; wrap each as a `ModeTask` agent whose
  `Run` invokes the existing `Spawner`/`SpawnerCodex`/`startACPSession` and
  translates the external event stream into the task agent's output. Enforces
  delegation while preserving process isolation.
- **B2 (native):** Reimplement the built-in agent types (worker, quick-task,
  explore, plan) as in-process ADK `ModeTask` agents. Removes process isolation
  and the sandbox/`--mode json` plumbing, but loses fault isolation.

## Go signatures

```go
// internal/agent: root becomes coordinator with task sub-agents
llmagent.New(llmagent.Config{
    Name: "pi", Model: cfg.Model,
    SubAgents: []agent.Agent{ /* task-mode worker, quick-task, explore, plan ... */ },
    Tools:     cfg.Tools, // no longer needs the manual subagent tool
})

// B1 adapter (in internal/subagent or internal/agent)
type TaskSubAgent struct { name string; dispatch func(SpawnOpts) (*Process, error) }
func (a *TaskSubAgent) Run(ctx agent.Context, input any) iter.Seq2[*session.Event, error]
```

## Error handling

- Delegation failures surface as a `finish_task` non-success FR so the LLM sees
  the validation error and can retry/redirect (ADK handles this).
- Process-isolation (B1) keeps crashes contained to the subagent process; the
  coordinator remains healthy.

## Acceptance criteria

- Given a task prompt, when the coordinator runs, then it delegates to a
  task-mode sub-agent (never does the work itself).
- Given multiple delegations in one turn, when the coordinator loops, then each
  resolves via `finish_task` before the next.
- Given a long-running sub-agent, when the coordinator is still running, then the
  main chat input can still accept the next queued prompt.

## Testing strategy

- ADK-level tests with mock task sub-agents asserting delegation + finish_task
  handshake (reuse upstream `reentry_test.go` patterns).
- Wrap tests for the B1 adapter: external spawner called with correct opts,
  events translated.

## Pros / Cons

**Pros:** Native, well-tested ADK pattern; enforced delegation ("always spawn a
subagent"); structured long-running lifecycle (spawn/run/finish_task, resume,
HITL); concurrency via `ParallelWorker`. Aligns exactly with the user's mental
model. B1 keeps process isolation.

**Cons:** Largest change. Re-plumbs how the root agent is built and how the TUI
streams/status are fed (sidebar `Orchestrator` events vs ADK sub-agent events).
B2 loses process isolation / sandbox guarantees. Requires validating ADK
concurrency of multiple runs against one session. ACP steering still separate.

## Fit for long-running tasks

Strong. Enforced delegation + per-task lifecycle + resume means each long task
maps to a sub-agent whose progress the coordinator tracks, and the coordinator
can take the next queued task while one is in flight.
