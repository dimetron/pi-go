# Outline: Async Subagent Spawn + Steer

Design: `design.md` (codex-reviewed). Requirements: `requirements.md`. Gated by
`PI_SUBAGENT_ASYNC` (default off) — **every slice must leave the flag-off path
byte-for-byte unchanged.**

Tracks: **A (1–6)** protocol & pool primitives. **B (7–11)** orchestration — async
state + lock order, spawn, writer, drainer, Steer/Attach. **Join (12–17)** survival,
tool surface, registration, verification. 1–2 parallel-safe; from 3 on the slices
share files and must run in order.

## Slices

**1. Feature flag.** `async.go`: `AsyncEnvVar`, `AsyncEnabled()`, defensive parsing
(`concurrency.go:26` is the model). Unset/empty/garbage/`0` ⇒ off;
`1`/`true`/`YES`/`on` ⇒ on. Files: `async.go`, `async_test.go` (both new).

**2. Pool `TryAcquire`.** Atomic non-blocking acquire; `Available()` must **not** be
used as a probe (it races). Sync `Acquire` untouched. Files: `pool.go`, `pool_test.go`.

**3. Protocol selection + first-prompt delivery.** `SpawnOpts.Mode`; `spawnArgs`
emits `--mode rpc` with **no positional prompt**; the empty-prompt guard moves — rpc
still requires a prompt but delivers it via `SendPrompt`, so there is one write path
and no first-turn special case. Files: `spawner.go`, `spawner_test.go`.

**4. rpc translation.** `rpcEvent`; `emitChildLine` branches on `p.rpc`; table per
design §4.2, including `json.Marshal` for `tool_execution_end.result` (object →
`string` field). Files: `spawner.go`, `spawner_rpc_test.go` (new).

**5. stdin, turn signalling, acks.** `cmd.StdinPipe()` for rpc; `SendPrompt`,
`Settled()`, `Acks()`, `Done()`; `agent_settled` signals the turn; a `success:false`
ack **fails the turn** so the writer cannot hang. Files: `spawner.go`,
`spawner_rpc_test.go`.

**6. Per-turn inactivity.** Split the process-level absolute (applied once at
`Spawn`) from a per-turn inactivity: arm on write, stop on settle, pump-owned timer
signalled by the writer. **Without this an idle async agent is killed as wedged.**
Test-overridable durations. Files: `spawner.go`, `timeout.go`, `spawner_rpc_test.go`.

**7. Async state + lock contract.** `asyncState` per §4.3; `agentState.async`;
`stream` **copied** from `bash_stream.go` (`internal/tools` is a cycle); lock order
`o.mu → asyncState.mu`, never across I/O. Files: `async.go`, `async_stream.go`,
`async_test.go` (last two new).

**8. Terminal state machine.** The §4.3 table with `termOnce`; lifetime expiry sets
`canceled` explicitly (a clean exit would read as `completed`); final result copied
to `last` for idempotent replay. Files: `async.go`, `async_test.go`.

**9. `SpawnAsync`.** `TryAcquire` + `ErrNoCapacity`; `context.WithoutCancel` for the
child ctx; `errors.Is` mapping for `ErrNoCapacity`/`ErrNotSteerable`. Files:
`orchestrator.go`, `async_test.go`.

**10. Steer queue + writer.** FIFO; write one `prompt` only after the prior turn's
settle (**R12**); select on wake/stop/`proc.Done()`/lifetime/settled/acks — never
`Settled()` alone (a mid-turn crash would deadlock). Files: `async.go`,
`async_turn_test.go` (new).

**11. Drainer + Steer/Attach.** Detached drainer into `out` (bounded; never blocks —
`sendEvent` already drops, so "forward every event" is not promised), attached
**before** turn 1. `Steer`/`Attach` → `AsyncStatus`; agent-owned cursor;
`status:"unknown"`; idempotent `last`. Files: `async.go`, `orchestrator.go`,
`async_turn_test.go`.

**12. Survival, shutdown, lifetime.** Child outlives turn end and parent cancel;
shutdown raises `stop` **outside `o.mu`** (held across `Process.Cancel()` today);
drops queued steers; never relies on stdin EOF to end a turn;
`DefaultAsyncLifetime = 60m`. Files: `orchestrator.go`, `async_lifetime_test.go` (new).

**13. `subagent` tool: `Background`.** Single mode only; `detectMode` unchanged;
`disabled`/`no_capacity`/`not_steerable` are **values** via `errors.Is` before the
generic `"failed"` path. Files: `internal/tools/subagent.go`, `subagent_test.go`.

**14. `subagent_input` tool.** Input `{agent_id (required), message?, wait_sec?}`,
handler, `AsyncSubagentTools` returning `(nil, nil)` when the flag is off. Files:
`subagent_input.go`, `subagent_input_test.go` (both new).

**15. Registration at the five assembly sites.** `interactive.go`, `cli.go`,
`acp/server/runtime.go`, `piagent/agent.go`, `eval/inventory.go` — one `append` each,
gated on the flag **and** subagent enablement. Files: those five.

**16. Description honesty.** `buildSubagentDescription` mentions `background` /
`subagent_input` only when async is enabled, and recommends non-worktree agents.
Mirrors `bash_control_test.go:20-43`. Files: `subagent.go`,
`subagent_description_test.go`.

**17. Verification sweep.** ADK non-interference (the tool does **not** set
`IsLongRunning`, so the runner is never parked); failure paths (crash mid-turn, ack
failure, EOF mid-turn, shutdown racing steer, lifetime expiry); non-regression with
the flag unset (existing suite unmodified, no `--mode rpc` child); then
`go test -race ./internal/subagent/...`. Files: `subagent_test.go`,
`async_turn_test.go`, `async_lifetime_test.go`, `spawner_test.go`.

## Key signatures

All types and signatures are specified in `design.md`: `asyncState` and the lock
order in §4.3, the output buffer in §4.3b, `Pool.TryAcquire` in §4.4b,
`Process.SendPrompt`/`Settled`/`Acks`/`Done` and the ack path in §4.4c,
`SpawnAsync`/`Steer`/`Attach`/`AsyncStatus` in §4.4, and
`AsyncSubagentTools` plus the async tool input in §4.5.

## Testing

- Per slice: `go build ./...` + `go test ./internal/subagent/...`.
- Slices 4–6 need a **scripted rpc child** (temp `#!/bin/bash` as `PiBinary`,
  `mockPiScript`, `spawner_test.go:14`) that reads stdin, prints the `response`
  envelope, then `agent_start` → `message_update`(text) → `agent_settled`, recording
  each prompt so R12 can be asserted. Variants: ack failure, crash, EOF mid-turn.
- Slices 7–12 need **injected durations** — never sleep 60 minutes in a test.
- Slices 4 and 7 are dense; 7 is a blocker if the lock order is wrong.
- Gates: `go build ./...`, `go vet ./...`, `go test ./...`,
  `go test -race ./internal/subagent/...`.
