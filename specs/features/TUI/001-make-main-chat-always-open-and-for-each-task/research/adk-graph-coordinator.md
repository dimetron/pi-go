# Research: ADK Go v2 Graph & Coordinator Model Fit

Date: (session 001)

## Purpose

Evaluate whether the proposed "main chat always open + queue tasks + route each
task to a subagent session (coordinator)" model works well with ADK Go v2.0.0
Graph primitives, or whether pi-go should keep its current hand-rolled subagent
orchestration.

## Key finding

**ADK Go v2 natively implements the exact "chat coordinator + task-mode
sub-agents" model the user wants.** pi-go currently does NOT use it — it wires a
plain chat-mode `LlmAgent` and a *manual* `subagent` tool (`internal/tools/
subagent.go`) backed by a custom `subagent.Orchestrator`. ADK v2 already ships a
`task` mode where an LLM sub-agent becomes a first-class tool that the chat
coordinator delegates to and whose completion is driven by a `finish_task`
FunctionCall.

## Facts about pi-go today

- Root agent is a chat `LlmAgent` named "pi" (`internal/agent/agent.go`
  `buildRunner`), with `cfg.Tools` including the custom `subagent` tool.
- `runner.Run(ctx, userID, sessionID, msg, RunConfig{StreamingMode})` runs one
  user turn. `RunStreaming` (`internal/agent/agent.go:593`) is what the TUI
  drives via `runAgentLoop`.
- The TUI (`internal/tui/agent_loop.go`) spawns one agent loop per prompt:
  `submitPrompt` sets `m.running = true` and starts `runAgentLoop`; `handleKey`
  returns nil while `m.running || m.loading` (tui.go:884) so the prompt is
  read-only during a run. There is **no input queue**.
- Subagents today run as **separate OS processes**: `worker`/`quick-task`/
  `explore`/`plan` run the pi binary (`subagent.Spawn`), `codex` runs the codex
  CLI, and claude/gemini/cursor run via ACP (`spawner_acp.go`). Each has its own
  session/context.

## ADK v2 Graph primitives (module google.golang.org/adk/v2@v2.0.0)

### Chat coordinator with task-mode sub-agents (runner.go)

`runner.Run` detects a chat root agent with task-mode sub-agents and, when
present, uses the **coordinator wrapper** (agent/llmagent/llm_agent_wrapper.go,
`runChat`) instead of the legacy sub-agent picker:

- Each `ModeTask` sub-agent is installed as a **`task` tool** on the coordinator
  (`NewTaskAgentTool`, llmagent.go:167).
- On every loop iteration, before re-entering `Agent.Run`, the wrapper scans the
  session for **unresolved task delegations** (task FCs from the coordinator
  without a matching FunctionResponse), dispatches each via `workflow.RunNode`
  under a stable `WithRunID(fc.ID)`, and synthesises a user-role FR event so the
  LLM sees the task result next round.
- The loop ends when the LLM finishes without delegating.
- This is a **single `runner.Run` call** that can contain many internal task
  delegations and interleaved coordinator turns.

### Workflow graph node types (workflow/)

- `NewDynamicNode` / `DynamicFn` — orchestrate children in Go code via `RunNode`,
  with `RerunOnResume` for resume re-entry.
- `NewParallelWorker` — run a wrapped node in parallel over a slice input
  (`maxConcurrency`), fail-fast on first non-retryable error.
- `NewBranch` / `NewJoinNode` — parallel fan-out/fan-in with branch isolation.
- `NewRetryLoop` / `NewFunctionNode` / `NewToolNode` / `NewRunNode` /
  `NewScheduler` (static/parallel/dynamic).
- HITL via `RequestInput` + resume.

### Reentry / resume

- `NodeConfig.RerunOnResume` and workflow `Resume` allow a paused workflow to
  continue when the user submits a FunctionResponse targeting a
  previously-emitted `RequestInput` (workflowagent/workflow.go, reentry_test.go).
- The workflow's `RunState` lives in `session.State`, so a single agent serves
  many concurrent sessions.

### Concurrency

- A single `*Runner` can serve many concurrent sessions. But one `runner.Run`
  invocation runs one user turn; the coordinator's *internal* task delegations
  (to task-mode sub-agents) run *within* that one call via `RunNode`.
- `ParallelWorker` gives concurrency *inside* a node. The chat coordinator
  handles unresolved task FCs on each loop iteration — a "one sub-agent per
  queued prompt, wait then next" model is achievable, and a fire-and-forget
  concurrent model would use `ParallelWorker` or parallel branch nodes.

## Implications for the requested feature

Two sub-models are viable inside ADK v2:

1. **Keep pi-go's manual `subagent` tool + Orchestrator**, and only add the
   non-blocking input queue at the TUI layer (outside the runner). The runner
   still runs one turn per prompt; the TUI serializes queued prompts into
   sequential `RunStreaming` calls. Simple, low-risk, but the coordinator is the
   LLM's own discretion (today's behavior), not a forced dispatch.

2. **Adopt ADK task-mode sub-agents**: register pi's built-in agent types
   (worker, quick-task, explore, plan, etc.) as `ModeTask` sub-agents of the chat
   coordinator. ADK then gives forced/structured delegation, per-task
   `finish_task` completion, and the coordinator loop — all inside a single
   `runner.Run`. This aligns with "main agent always starts a subagent and
   processes next."

   Caveats to validate:
   - pi-go's subagents today are *external processes* (pi re-invocation, codex,
     ACP claude/gemini/cursor). ADK task-mode sub-agents are *in-process* ADK
     agents (same Go process, model-driven). The two are not the same execution
     model. Bridging would mean wrapping the external spawners inside a
     `ModeTask` agent's Run, or reimplementing sub-agent execution as in-process
     ADK agents.
   - The TUI's streaming display and the `Orchestrator` status/sidebar would
     need to be fed by ADK sub-agent events (which the coordinator wrapper does
     emit via `RunNode`) instead of (or in addition to) the custom subagent
     tool's `SubagentEvent` stream.
   - Session routing: each task-mode sub-agent runs within the same ADK session
     by default; giving each its own file-backed sub-session is an added layer.

## Recommendation direction

The user's model is fundamentally a **chat coordinator delegating tasks to
task-mode sub-agents** — which is a first-class, well-tested ADK v2 pattern.
However, pi-go's sub-agents are external processes, so the cleanest incremental
path is likely:

- Ship the **TUI non-blocking input queue** first (outside the runner; low risk).
- Then evaluate migrating the root agent to ADK task-mode sub-agents, wrapping
  the existing external spawners behind each `ModeTask` agent, so "always spawn
  a subagent per task" becomes enforced by ADK rather than left to the LLM.

The ACP steering ask (sending additional input to an in-flight ACP subagent) is
a separate concern: the current ACP `RunningSession` (internal/acp/client/
session.go) does a single `Prompt()` then closes stdin; there is no API to
inject another prompt into a live session. That needs its own design.
