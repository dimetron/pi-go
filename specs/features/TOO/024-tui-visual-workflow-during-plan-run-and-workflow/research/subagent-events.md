# Research: Subagent Event Channel & Event Types

## Event struct — internal/subagent/types.go:100-109

```go
type Event struct {
    Type       string `json:"type"`                 // "text_delta", "tool_call", "tool_result", "message_end", "error"
    Content    string `json:"content,omitempty"`    // text for text_delta; tool name for tool_call; JSON for tool_result
    Error      string `json:"error,omitempty"`       // error message for error events
    SessionID  string `json:"session_id,omitempty"` // subprocess session ID (from message_start)
    StopReason string `json:"stopReason,omitempty"` // ACP stopReason on message_end
    ToolArgs   any    `json:"tool_input,omitempty"` // tool args for tool_call
    Status     string `json:"status,omitempty"`     // final status for run_done events
}
```

## Full set of Type values emitted

| Type | Emitted at | Fields populated |
|------|-----------|------------------|
| `text_delta` | spawner.go:334,341; spawner_acp.go:212; spawner_codex.go:140 | `Content` |
| `tool_call` | spawner.go:343; spawner_acp.go:214; spawner_codex.go:142 | `Content`, `ToolArgs` |
| `tool_result` | spawner.go:345 | `Content` |
| `message_start` | spawner.go:347; spawner_acp.go:160,191; spawner_codex.go:135,155 | `SessionID` |
| `message_end` | spawner.go:349; spawner_acp.go:177; spawner_codex.go:179 | `StopReason` |
| `error` | spawner.go:296; spawner_acp.go:172,218; spawner_codex.go:146,174 | `Error` |
| `run_done` | orchestrator.go:633 (synthesized after process exit) | `Status` |

**Unknown types pass through verbatim:** the spawner's `emitChildLine` default case
(spawner.go:350-351) forwards any unknown `ev.Type` unchanged. ACP/codex adapters likewise
pass through unknown types (spawner_acp.go:220, spawner_codex.go:148). The orchestrator's
`forwardAgentEvents` republishes everything untouched (orchestrator.go:612-617). So a child
emitting `{"type":"stage_start","content":"..."}` flows through unchanged.

## Event flow: spawned subagent → TUI

- `/run` spawns via `m.cfg.Orchestrator.SpawnWithInput` (run.go:517,582,600,1048), which
  returns `(events <-chan subagent.Event, agentID string, err error)`.
- `Spawn` (orchestrator.go:418-513) creates `events := make(chan Event, 64)` (line 505) and
  launches `go o.forwardAgentEvents(events, proc, state, logACP)` (line 510).
- `forwardAgentEvents` (orchestrator.go:608-641) republishes every `proc.Events()` item onto
  `out`, writes the terminal `run_done` event (line 633), and `defer close(out)` (line 609).
- TUI single-agent: `startRunAgent` stores the channel in `m.run.events` (run.go:549),
  returns `waitForRunAgent(events, agentID)` (run.go:566).
- `waitForRunAgent` (run.go:700-714) is a `tea.Cmd` blocking on `<-events`. On close →
  `runAgentDoneMsg`; on `run_done` → `runAgentDoneMsg{status}`; else wraps in
  `runAgentEventMsg` (run.go:160-163).
- `handleRunAgentEvent` (run.go:717-752) switches on `ev.Type` → `applyRunTextDelta` /
  `applyRunToolCall` / `applyRunToolResult` / `message_start` / `message_end` /
  `applyRunError`, then re-arms via `m.waitForRunEvents()` (run.go:751).
- Parallel mode: `handleRunParallel` (run.go:571-674) stores per-agent channels in
  `m.run.parallel` (run.go:648-651), uses `waitForParallelRunEvents` (run.go:1773-1806) fan-in.

## Adding a new event type (stage_start / stage_end)

- **Producer side:** the spawner's default case already forwards unknown types verbatim, so
  a child emitting `{"type":"stage_start","content":"<stage-id>"}` flows through unchanged.
- **TUI consumer side:** `handleRunAgentEvent` (run.go:727-748) has **no `default` case** —
  an unknown `ev.Type` falls through silently (only the matrix-rain feed at run.go:721-725
  reacts). A new `case "stage_start"` / `case "stage_end"` must be added to this switch.
  The `runAgentEventMsg` wrapper carries the full `subagent.Event`, so all fields are available.

## Plan agent streaming — DIFFERENT mechanism

`/plan` does **not** use the subagent channel. `startPlanSession` (plan.go:483) returns
`m.startAgentLoop(roughIdea)` — the in-process agent loop (agent_loop.go:719-726):

```go
func (m *model) startAgentLoop(prompt string) tea.Cmd {
    ch := make(chan agentMsg, 64)
    m.agentCh = ch
    ...
    go m.runAgentLoop(agentCtx, prompt, ch, m.agentRun())
    return waitForAgent(m.agentCh)
}
```

This uses a `chan agentMsg` (a TUI-internal message type, distinct from `subagent.Event`)
and drives the local `agent.Agent` directly. It does **not** spawn a subagent process and
does **not** use `SpawnWithInput`.

**Implication:** `/run` and `/plan` use two separate streaming paths. Stage events for
`/run` ride the `subagent.Event` channel; stage events for `/plan` must ride the
`agentMsg` channel (or be derived from the plan agent's tool calls / artifact writes).
