# Design A — TUI Non-Blocking Input Queue (Incremental Coordinator)

Status: Design candidate for comparison
Related research: `adk-graph-coordinator.md`

## Problem

The main chat is read-only while an agent runs. The user cannot type or queue
the next task until the current turn finishes, so long-running tasks block the
conversation. This design keeps pi-go's existing subagent architecture and adds
a queue at the TUI layer.

## Current state (facts)

- `handleKey` returns nil while `m.running || m.loading` (tui.go:884-887). The
  prompt view renders "(waiting for response...)" instead of the input
  (input.go:150 `View(running)`).
- `submitPrompt` sets `m.running = true` and starts one `runAgentLoop` per
  prompt; on completion `handleAgentDone` sets `m.running = false`
  (agent_loop.go:802).
- The agent runs one turn per `RunStreaming` call. Subagents are external
  processes (pi re-invocation via `Spawner`, codex CLI, ACP claude/gemini/
  cursor) managed by `subagent.Orchestrator`. The root agent is a plain chat
  `LlmAgent`; spawning a subagent is at the LLM's discretion (system prompt),
  not enforced.

## Desired end state

- The input stays editable while an agent runs. Pressing Enter while running
  enqueues the prompt.
- Queued prompts are processed one after another as sequential turns in the same
  session (FIFO). Only the first sets the session title.
- The "always spawn a subagent per task" behavior is expressed only as a system
  prompt change (encouraged, not enforced).

## Architecture

```
User Enter ──► [running?] ── no ──► submitPrompt (start run)
                 │ yes
                 ▼
            queue ([]string)      next turn starts
                                      when current agentDoneMsg
```

- Add `pendingInputs []string` to `model`.
- In `InputSubmitMsg` handling: if `m.running`, push to `pendingInputs`, do not
  start a loop. Else `submitPrompt`.
- In `handleAgentDone`: after clearing `m.running`, if `pendingInputs` is
  non-empty, pop and start the next loop.
- Remove the `running` branch from `InputModel.View` so the input stays
  editable; optionally show a queue-count badge in the status bar.

## Go signatures

```go
// model
pendingInputs []string

func (m *model) enqueuePrompt(text string, mentions []string)
func (m *model) drainNextQueued() (tea.Model, tea.Cmd) // called on agentDone
```

## Error handling

- Queued inputs survive only in-memory; a process restart drops them (acceptable
  for v1). If a turn fails, still advance to the next queued input.
- If the agent is nil (unit tests), drain no-ops.

## Acceptance criteria

- Given a running agent, when the user types a prompt and presses Enter, then
  the input is accepted and the prompt is queued (no error flash).
- Given queued prompts, when the current run completes, then the next queued
  prompt starts automatically in order.
- Given queued prompts, when a turn errors, then the next queued prompt still
  runs.

## Testing strategy

- Unit tests on `enqueuePrompt`/`drainNextQueued` (FIFO, empty, error path).
- TUI e2e test simulating two submits while a mock agent runs, asserting both
  turns run sequentially.

## Pros / Cons

**Pros:** Low risk; small diff; no change to subagent execution model; builds on
existing `runAgentLoop`. Reusable foundation for B/C.

**Cons:** Coordinator role is not enforced (LLM may do work directly). No
structured per-task sub-agent lifecycle; each turn is just another `RunStreaming`
call. No long-running-task isolation beyond what the LLM chooses. Queued inputs
are ephemeral.

## Fit for long-running tasks

Weakest of the three for long-running tasks: nothing forces delegation, so a
queued task could be attempted by the main LLM (tying up the single running
turn). It solves "input not blocked" but not "each task routed to a subagent."
