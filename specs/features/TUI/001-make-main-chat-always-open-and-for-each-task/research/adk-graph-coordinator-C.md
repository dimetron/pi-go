# Design C — Hybrid: TUI Queue + External Long-Running Task Sessions

Status: Design candidate for comparison
Related research: `adk-graph-coordinator.md`

## Problem

Support long-running tasks with a non-blocking main chat, without re-plumbing
the root agent into ADK task-mode (Design B) and without relying on the LLM's
discretion (Design A). Keep pi-go's external-process subagents, add a task
queue and a dedicated "task session" abstraction that outlives a single turn.

## Current state (facts)

- Subagents are external OS processes with their own sessions/contexts
  (`Spawner` pi re-invocation, `SpawnerCodex`, ACP claude/gemini/cursor).
- The root agent is a chat `LlmAgent`; subagent spawning is LLM-discretion.
- ACP sessions today do a single `Prompt()` then close stdin; no API to inject
  another prompt into a live session (`RunningSession` exposes Events/Done/
  Cancel/Wait only). `claudecode.RunRequest` has an optional `SessionID` for
  resume of a *completed* session, not live steering.
- `subagent.Pool` caps concurrency (DefaultPoolSize=5); `recentTaskTTL` dedupes.

## Desired end state

- Main chat input is always open; prompts queue and are processed in FIFO.
- A **TaskManager** (in-process) owns long-running task sessions. Each queued
  task is dispatched to an external subagent; results are correlated back to the
  originating prompt via a task ID shown in the chat/sidebar.
- The main LLM can run concurrently for the next task while a long task session
  is in flight (fire-and-forget with result backpressure), or serialize (wait for
  the current task then take the next), per policy.

## Architecture

```
User Enter ─► [running?] ─► queue []QueuedPrompt
                        └► TaskManager.Dispatch(taskID, agent, prompt)
                              │ external spawner (pi/codex/ACP)
                              ▼
                         taskSession (long-running, own context)
                              │ events ─► TUI stream + sidebar
                              │ result  ─► correlated to prompt (taskID)
                        next task starts when current returns (or parallel)
```

- `QueuedPrompt{ID, Text, Mentions}` — ID links a queued prompt to its task
  result in the UI.
- `TaskManager` holds active `*Process`/`*acp.RunningSession` handles, a
  `Pool`, and a `TaskResult` store (replacing/extending `recentTasks`).
- The chat coordinator is still the LLM, but a policy layer routes each queued
  prompt to a subagent (agent chosen from the prompt or a default worker).

## Go signatures

```go
type QueuedPrompt struct {
    ID       string
    Text     string
    Mentions []string
}

type TaskManager struct {
    pool   *subagent.Pool
    active map[string]*taskHandle // taskID -> running session
    results map[string]subagent.AgentResult
}

func (tm *TaskManager) Dispatch(ctx, taskID, agentName string, opts subagent.SpawnOpts) (*taskHandle, error)
func (tm *TaskManager) Result(taskID string) (subagent.AgentResult, bool)
func (tm *TaskManager) Cancel(taskID string) error
```

## Error handling

- A failed task records an error result keyed by taskID; the UI surfaces it on
  the originating prompt. The queue advances regardless.
- Process crash isolation preserved (external subagents).
- In-memory task registry: restart drops in-flight tasks (results are in the
  session/JSONL once written).

## Acceptance criteria

- Given a running long task, when the user types a new prompt, then it is queued
  and a new subagent task starts (or waits per policy) without blocking input.
- Given a completed task, when it finishes, then its result is shown attached to
  the original prompt (by taskID).
- Given N queued tasks, when capacity allows, then up to `Pool.Size()` run
  concurrently and the rest queue.

## Testing strategy

- Unit tests for `TaskManager` dispatch/result/cancel and queue FIFO.
- e2e with a mock external subagent process asserting concurrency cap and result
  correlation.

## Pros / Cons

**Pros:** Keeps external process isolation (fault tolerance for long-running
work). Non-blocking queue. Task correlation in UI. No ADK re-plumb of the root
agent. Natural home for future ACP steering (live session handle is retained).

**Cons:** Coordinator dispatch is a policy layer, not native ADK delegation —
still not as structured as ADK task-mode. Needs its own task-ID ↔ prompt
correlation plumbing. ACP steering (sending into a live session) still requires
new ACP work; this design only retains the handle.

## Fit for long-running tasks

Strongest for true long-running work: external processes isolate crashes,
pooled concurrency scales, and results correlate back to the origin prompt. But
it does not leverage ADK's native coordinator; it is pi's own orchestration
made queue-aware.
