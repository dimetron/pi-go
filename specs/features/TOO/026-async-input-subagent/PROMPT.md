# Async Subagent Spawn + Steer

## Objective

Add `background: true` to the existing `subagent` tool so a pi-binary subagent can
be spawned **detached**, and add one new tool **`subagent_input`** that both
**steers** that running subagent (messages queued as its next turn) and **attaches**
to it (incremental reads of its output). Today every `subagent` mode blocks until the
child exits, `SpawnInput.Background` is declared but never read
(`internal/subagent/types.go:17`), and there is no way to deliver input to a running
agent. The whole feature is gated by the environment variable `PI_SUBAGENT_ASYNC`
(**default off**); with it unset, behaviour must be byte-for-byte unchanged.

## Key Requirements

1. **Async spawn** — `subagent{agent, task, background: true}` (single mode only)
   returns immediately with `running: true` and an `agent_id`, and the child keeps
   running after the call returns. Achieved by spawning the child on
   `pi --mode rpc` instead of `--mode json`.
2. **Steer + attach in one tool** — `subagent_input{agent_id, message?, wait_sec?}`
   delivers `message` to the running child **and** returns the output accumulated
   since the agent's last read. Calling it with no `message` is a read-only attach.
3. **Queued, never mid-turn** — steered input becomes the child's **next** turn,
   serialized by the parent. No second `prompt` is written before the previous
   turn's `agent_settled`.
4. **Survives the turn, dies with the session** — a detached agent outlives the
   spawning turn and the user's Esc/Ctrl+C, but is cancelled by session shutdown.
5. **Values, not errors, for agent-level conditions** — missing/evicted agent ⇒
   `status: "unknown"`; full pool ⇒ `status: "no_capacity"`; flag off ⇒
   `status: "disabled"`; codex/ACP backend ⇒ `status: "not_steerable"`.
6. **pi-binary backends only** — codex and ACP steering are follow-ups, not part of
   this work, and must degrade with a model-actionable value.
7. **Model-only** — no new TUI affordances, slash commands, keybindings, or
   user-facing steer.

## Acceptance Criteria

### Async spawn
- Given `PI_SUBAGENT_ASYNC=1`, when `subagent{agent:"worker",task:"…",background:true}` is called, then it returns promptly with `running:true`, a non-empty `agent_id`, and a note naming `subagent_input`, and the child outlives the call.
- Given `PI_SUBAGENT_ASYNC` unset, when `background:true` is requested, then the result is `status:"disabled"` and no process is spawned.
- Given a `codex` or ACP-backed agent name with `background:true`, then the result is `status:"not_steerable"` with a reason and no process is detached.
- Given `PI_SUBAGENT_CONCURRENCY=1` and one detached agent running, when a second `background:true` spawn is requested, then it returns `status:"no_capacity"` **without blocking**.
- Given a synchronous `subagent` call with a full pool, then it still blocks on `Acquire` exactly as today.

### Steer
- Given a running async agent, when `subagent_input{agent_id,message:"…"}` is called, then the message is queued and written as the child's next turn only after the current turn settles, and the result reports it as queued.
- Given a steer arrives mid-turn, then no `prompt` is written before that turn's `agent_settled`.
- Given a completed agent, when steered, then nothing is written and the result reports the terminal status.

### Attach / poll
- Given a running agent, when `subagent_input{agent_id}` is called repeatedly, then each result contains only output produced since the previous call.
- Given output exceeding the buffer cap, then the result reports `dropped` bytes.
- Given a completed agent, when attached repeatedly, then each call returns the final result — no "spent handle" error.
- Given an unknown/evicted `agent_id`, then the result is `status:"unknown"` with no error.
- Given a missing `agent_id`, then it **is** an error naming the live ids.

### Lifetime
- Given a running async agent, when the spawning turn ends, then the agent keeps running.
- Given a running async agent, when the user cancels the parent turn (Esc/Ctrl+C), then the agent keeps running.
- Given a running async agent **idle between turns past the inactivity timeout**, then it is **not** killed.
- Given a turn in flight producing no output past the inactivity timeout, then the child is killed and the status reflects a timeout.
- Given session shutdown, then the agent is cancelled, marked `canceled`, its stdin closed, queued steer messages dropped, and **no deadlock**.
- Given an agent idle past `DefaultAsyncLifetime`, then it is stopped, marked `canceled` (not `completed`), and its pool slot released.
- Given the child crashes mid-turn without `agent_settled`, then the writer exits via `proc.Done()`, no deadlock, status is a failure.
- Given an rpc `prompt` is rejected (`success:false`), then the turn fails with that error rather than hanging.

### Feature flag / non-regression
- Given `PI_SUBAGENT_ASYNC` unset or unparseable, then async is off with no error.
- Given the flag off, then the assembled tool list contains no `subagent_input`.
- Given `PI_SUBAGENT_ASYNC=1` and a synchronous call, then the child still uses `--mode json` (protocol is per-spawn).
- Given a `subagent` call without `background`, then behaviour in single, parallel, and chain mode is unchanged and `detectMode` resolves identically.
- Given `PI_SUBAGENT_ASYNC=1` and a nested spawn, then the child inherits the flag.

## Implementation Slices

Execute in order. Two pairs may run concurrently: **1 + 2** and **13 + 14**; every
other slice runs one at a time, in order. Files marked "(new)" do not exist yet, and
each slice's `verify` includes a **test-presence gate** — so a slice whose new tests
were never written **fails** its gate rather than passing vacuously. Name new tests
with the prefix the slice's gate pins (e.g. `TestAsync*`, `TestAsyncWriter*`).

1. **Feature flag `PI_SUBAGENT_ASYNC`** — `AsyncEnabled()`, `DefaultAsyncLifetime`, table tests. files: `internal/subagent/async.go` (new), `internal/subagent/async_test.go` (new). verify: `go build ./... && go test ./internal/subagent/ -list '^TestAsync' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestAsync'`. parallel-safe: yes.
2. **`Pool.TryAcquire`** — atomic non-blocking acquire; leave `Acquire` unchanged. files: `internal/subagent/pool.go`, `internal/subagent/pool_test.go`. verify: `go test -race ./internal/subagent/ -list '^TestPool_TryAcquire' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestPool_TryAcquire'`. parallel-safe: yes.
3. **Protocol selection + first-prompt delivery** — `SpawnOpts.Mode`; `--mode rpc` with no positional prompt; guard moves. files: `internal/subagent/spawner.go`, `internal/subagent/spawner_test.go`. verify: `go test ./internal/subagent/ -list '^TestSpawnArgs_RPC' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestSpawnArgs'`. parallel-safe: no.
4. **rpc → `subagent.Event` translation** — `rpcEvent`; `emitChildLine` branch; `json.Marshal` for `tool_execution_end.result`. files: `internal/subagent/spawner.go`, `internal/subagent/spawner_rpc_test.go` (new). verify: `go test ./internal/subagent/ -list '^TestRPCEmitChildLine' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestRPCEmitChildLine'`. parallel-safe: no.
5. **stdin, turn signalling, acks** — `SendPrompt`, `Settled`, `Acks`, `Done`; failed ack fails the turn. files: `internal/subagent/spawner.go`, `internal/subagent/spawner_rpc_test.go`. verify: `go test ./internal/subagent/ -list '^TestRPCProcess' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestRPCProcess'`. parallel-safe: no.
6. **Per-turn inactivity (the slice that keeps an idle agent alive)** — silence counts as wedged only while a turn is in flight. files: `internal/subagent/spawner.go`, `internal/subagent/timeout.go`, `internal/subagent/spawner_rpc_test.go`. verify: `go test ./internal/subagent/ -list '^TestRPCInactivity' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestRPCInactivity'`. parallel-safe: no.
7. **Async state + copied `stream` + lock contract** — `asyncState`, `agentState.async`, copy `stream`. files: `internal/subagent/async_stream.go` (new), `internal/subagent/async_stream_test.go` (new), `internal/subagent/async.go`, `internal/subagent/orchestrator.go`, `internal/subagent/async_test.go`. verify: `go build ./... && go test ./internal/subagent/ -list '^TestAsyncStream' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncStream'`. parallel-safe: no.
8. **Terminal state machine** — the transition table with `termOnce`; lifetime ⇒ `canceled`; `last` for idempotent replay. files: `internal/subagent/async.go`, `internal/subagent/async_test.go`. verify: `go build ./... && go test ./internal/subagent/ -list '^TestAsyncTerminal' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncTerminal'`. parallel-safe: no.
9. **`SpawnAsync`** — `TryAcquire` + `ErrNoCapacity`; `context.WithoutCancel`; backend gate ⇒ `ErrNotSteerable`; drainer attached before turn 1. files: `internal/subagent/orchestrator.go`, `internal/subagent/async.go`, `internal/subagent/async_test.go`. verify: `go build ./... && go test ./internal/subagent/ -list '^TestSpawnAsync' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestSpawnAsync'`. parallel-safe: no.
10. **Steer queue + writer** — FIFO; R12 invariant; select on wake/stop/`proc.Done()`/lifetime/settled/acks. files: `internal/subagent/async.go`, `internal/subagent/async_turn_test.go` (new). verify: `go build ./... && go test ./internal/subagent/ -list '^TestAsyncWriter' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncWriter'`. parallel-safe: no.
11. **Drainer + `Steer`/`Attach`** — never block the pump; agent-owned cursor; idempotent `last`; `status:"unknown"`. files: `internal/subagent/async.go`, `internal/subagent/orchestrator.go`, `internal/subagent/async_turn_test.go`. verify: `go build ./... && go test ./internal/subagent/ -list '^TestAsyncAttach' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncAttach|^TestAsyncSteer'`. parallel-safe: no.
12. **Survival, shutdown, lifetime** — `stop` raised outside `o.mu`; drop queued steers; no EOF reliance; lifetime cap. files: `internal/subagent/orchestrator.go`, `internal/subagent/async.go`, `internal/subagent/async_lifetime_test.go` (new). verify: `go test ./internal/subagent/ -list '^TestAsyncLifetime' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncLifetime'`. parallel-safe: no.
13. **`background` on the `subagent` tool** — single mode only; `errors.Is` maps sentinels to values before the generic `"failed"` path; `detectMode` unchanged. files: `internal/tools/subagent.go`, `internal/tools/subagent_test.go`. verify: `go build ./... && go test ./internal/tools/ -list '^TestSubagentBackground' | grep -q '^Test' && go test ./internal/tools/ -run '^TestSubagentBackground'`. parallel-safe: yes.
14. **`subagent_input` tool** — `AsyncSubagentTools` returns `(nil, nil)` when the flag is off; `agent_id` missing ⇒ error naming live ids. files: `internal/tools/subagent_input.go` (new), `internal/tools/subagent_input_test.go` (new). verify: `go build ./... && go test ./internal/tools/ -list '^TestSubagentInput' | grep -q '^Test' && go test ./internal/tools/ -run '^TestSubagentInput'`. parallel-safe: yes.
15. **Register at the five assembly sites** — one guarded `append` each, plus inventory. files: `internal/cli/interactive.go`, `internal/cli/cli.go`, `internal/acp/server/runtime.go`, `piagent/agent.go`, `internal/eval/inventory.go`, `internal/eval/coverage_test.go`. verify: `go build ./... && go test ./internal/eval/ -list '^TestInventoryIncludesAsyncTools' | grep -q '^Test' && go test ./internal/eval/ -run '^TestInventoryIncludesAsyncTools' && go test ./internal/cli/ ./piagent/ ./internal/acp/...`. parallel-safe: no.
16. **Keep the `subagent` description honest** — `buildSubagentDescription` mentions async only when enabled. files: `internal/tools/subagent.go`, `internal/tools/subagent_description_test.go`. verify: `go build ./... && go test ./internal/tools/ -list '^TestSubagentDescriptionAsync' | grep -q '^Test' && go test ./internal/tools/ -run '^TestSubagentDescriptionAsync'`. parallel-safe: no.
17. **Verification sweep** — ADK non-interference, failure paths, non-regression with the flag unset, race sweep. files: `internal/tools/subagent_test.go`, `internal/subagent/async_turn_test.go`, `internal/subagent/async_lifetime_test.go`, `internal/subagent/spawner_test.go`, `internal/subagent/spawner_rpc_test.go`. verify: `go build ./... && go vet ./... && go test ./... && go test -race ./internal/subagent/... ./internal/tools/...`. parallel-safe: no.

## Execution Model

Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the
**Coordinator**; it delegates rather than implements.

- **Workers**: one `worker` subagent per slice (`quick-task` for a single-file
  mechanical change). Only slices 1, 2, 13, and 14 are marked parallel-safe; batch
  those pairs at most, and only up to the concurrency the `subagent` tool reports
  for the running process. Everything else runs one at a time, in order — steps 3–12
  all edit `spawner.go` / `async.go` / `orchestrator.go`, so ordering is mandatory.
- **Verifier**: after the last slice, a `code-reviewer` subagent checks the Done
  Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to
  10 cycles total.

## Done Criteria

The Verifier checks these against the diff, not against the checklist.

- [ ] `PI_SUBAGENT_ASYNC` unset: the entire pre-existing test suite passes unmodified, and a synchronous spawn still records `--mode json` with the prompt in argv (`internal/subagent/spawner_test.go`).
- [ ] `PI_SUBAGENT_ASYNC` unset: `AsyncSubagentTools` returns `(nil, nil)`, so `subagent_input` is absent from every assembled tool list (`internal/tools/subagent_input.go` + its test).
- [ ] R12 enforced and tested: the scripted rpc child records every prompt it receives, and a test asserts prompt #2 arrives only after prompt #1's `agent_settled` (`internal/subagent/async_turn_test.go`).
- [ ] An async agent idle between turns past the inactivity timeout is **not** killed, while a silent turn in flight **is** — both asserted with injected durations, so neither test sleeps minutes (`internal/subagent/spawner_rpc_test.go`).
- [ ] `Pool.TryAcquire()` exists and is atomic; `SpawnAsync` returns `ErrNoCapacity` **without blocking**, proven by a test that runs under a **short bounded timeout** and fails (never hangs) if the acquire regressed to blocking (`internal/subagent/pool.go`, `async_test.go`).
- [ ] No import cycle: `go list -f '{{join .Imports "\n"}}' ./internal/subagent | grep -c '/internal/tools'` prints `0` — checked via `go list`, **not** a text grep, because the copied `stream` legitimately carries a comment naming its source file.
- [ ] `go test -race ./internal/subagent/... ./internal/tools/...` passes, covering Steer/Attach/Cancel/Shutdown/lifetime/drainer termination.
- [ ] The async tool never sets `IsLongRunning`, so the ADK runner is not parked (asserted in `internal/tools/subagent_test.go`).
- [ ] Agent-level conditions are values, not errors: `unknown`, `no_capacity`, `disabled`, and `not_steerable` all appear as `status` values in tests, and `no_capacity` is **not** collapsed into `"failed"`.
- [ ] No slice is left as a stub: every function the plan names has real behaviour rather than a placeholder body — `AsyncEnabled` reads its environment variable, `SpawnAsync` spawns a child on `--mode rpc`, `Steer`/`Attach` return the recorded output and status, and no planned handler returns a bare `nil, nil` where the plan specifies a result.

## Gates

- **build**: `go build ./...`
- **test**: `go test ./...` (equivalently `make test-unit`)
- **vet**: `go vet ./...`
- **race**: `go test -race ./internal/subagent/... ./internal/tools/...`
- **lint** (before merge): `golangci-lint run ./...` (equivalently `make lint`)

`go version` is 1.27.1. Do not use `make build` as a per-slice gate: it runs
`cache-clean` first, which only slows the loop.

## Reference

- Design: `specs/features/TOO/026-async-input-subagent/design.md`
- Outline: `specs/features/TOO/026-async-input-subagent/outline.md`
- Plan: `specs/features/TOO/026-async-input-subagent/plan.md`
- Requirements: `specs/features/TOO/026-async-input-subagent/requirements.md`
- Research: `specs/features/TOO/026-async-input-subagent/research/`
- Rough idea: `specs/features/TOO/026-async-input-subagent/rough-idea.md`

## Constraints

- **Never modify `internal/pirpc`.** The steerable rpc server already exists; this
  work is parent-side. Its acknowledgement semantics are consumed, not changed.
- **Lock order is `o.mu → asyncState.mu`, and never hold a mutex across I/O.** This
  is mandatory: `Cancel` and `ShutdownWithTimeout` already hold `o.mu` across
  `Process.Cancel()` (`internal/subagent/orchestrator.go:806-819`, `:860-872`).
  Shutdown must collect async states under `o.mu` and signal them **after**
  releasing it.
- **`internal/subagent` must not import `internal/tools`** — it is an import cycle
  (`internal/tools/agent.go:6`). Copy `stream` from
  `internal/tools/bash_stream.go` instead.
- **Do not change synchronous behaviour.** No `background` ⇒ byte-for-byte
  unchanged. `detectMode`, the single/parallel/chain handlers, and
  `forwardAgentEvents`' slot-release semantics stay as they are; `Spawn` keeps its
  blocking `Acquire` and only `SpawnAsync` uses `TryAcquire`.
- **Do not use ADK's long-running-tool support** (`IsLongRunning`,
  `LongRunningToolIDs`, `ResponseDeferrer`). It models an externally supplied
  response with no handle, cursor, or queue, and would **park the parent runner** —
  the opposite of the intent.
- **Do not touch the ADK runner, session, or compaction layers.** Auto-injecting
  results via the `PreTurnHook` seam (`internal/agent/agent.go:386`) is explicitly
  rejected; the model must poll.
- **Do not rely on stdin EOF to end an in-flight turn.** EOF only ends the child's
  scanner loop (`internal/pirpc/rpc.go:161-183`); `Process.Cancel()` is the reliable
  stop.
- **No new TUI affordances and no new external dependencies.**
- **Follow-up specs owed, do NOT implement here:** codex steering (`turn/steer`,
  `turn/start{threadId, input[]}`), ACP steering (repeat `session/prompt` on a
  retained connection), mid-turn injection inside `pirpc`, and a per-turn absolute
  timeout.
- **Pre-existing defects deliberately not fixed:** the sidebar `"done"` status
  mismatch (`internal/tui/sidebar.go:590-603`) and the `pi-acp-mock` `LoadSession`
  nil-deref (`cmd/pi-acp-mock/main.go:137`).
- **Known limitations to record in the summary, not fix:** a detached agent has no
  TUI repaint once the parent turn ends (the 150 ms ticker runs only while
  `m.running`, `internal/tui/tui.go:756-771`), and a detached agent holding a
  worktree keeps it until shutdown cleanup.
