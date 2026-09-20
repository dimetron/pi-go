# Plan: Async Subagent Spawn + Steer

Design: `design.md` (codex-reviewed). Outline: `outline.md`. Requirements:
`requirements.md`.

**Feature flag:** `PI_SUBAGENT_ASYNC`, default **off**. Every slice must leave the
flag-off path byte-for-byte unchanged. If a slice cannot be verified with the flag
off *and* on, it is not done.

**Two hard rules**, repeated in the slices that need them:

- **Lock order:** `o.mu → asyncState.mu`. Never hold any mutex across I/O. Mandatory
  because `Cancel` and `ShutdownWithTimeout` already hold `o.mu` across
  `Process.Cancel()` (`internal/subagent/orchestrator.go:806-819`, `:860-872`).
- **Import direction:** `internal/subagent` must **not import** `internal/tools` —
  it is a cycle (`internal/tools/agent.go:6` imports `internal/subagent`). Copy
  `stream` instead.

**Name your tests to match the slice's gate.** Each slice's verify command pins an
exact test-name prefix (`TestAsync`, `TestPool_TryAcquire`, `TestSpawnArgs_RPC`,
`TestRPCEmitChildLine`, `TestRPCProcess`, `TestRPCInactivity`, `TestAsyncStream`,
`TestAsyncTerminal`, `TestSpawnAsync`, `TestAsyncWriter`, `TestAsyncAttach` /
`TestAsyncSteer`, `TestAsyncLifetime`, `TestSubagentBackground`, `TestSubagentInput`,
`TestSubagentDescriptionAsync`, `TestInventoryIncludesAsyncTools`). A worker must name its new tests under that prefix, or
the presence gate cannot find them and the slice fails its own verify.

**Verify commands must not be vacuous.** `go test -run '<pattern>'` exits **0** with
`[no tests to run]` when the pattern matches nothing, so a slice that adds tests
whose names do not match its own pattern silently passes. For every slice, the
verify command therefore includes a **test-presence gate** in the same `&&` chain:

    go test ./internal/subagent/ -list '<Pattern>' | grep -q '^Test' && go test -race ./internal/subagent/ -run '<Pattern>'

`-list` prints matching test names (and an `ok` line, so match on `^Test`, never on
any line). If the named tests were never written, `-list` finds none, `grep` fails,
and the slice fails its gate instead of passing it. Verify each gate behaves both
ways before moving on: it must **fail** before the slice's tests exist and **pass**
after.

**Slice order.** Steps 1–6 are the protocol/pool foundation (verifiable with a
scripted shell child and no orchestrator). Steps 7–12 are orchestration. Steps 13–17
are the tool surface, registration, and verification.

Two pairs may run concurrently: **1 + 2** (disjoint files) and **13 + 14** (both
edit the tool layer but touch disjoint files; both depend only on step 12). Every
other slice runs one at a time, in order — steps 3–12 all edit `spawner.go` /
`async.go` / `orchestrator.go`, and step 15 needs step 14's constructor to exist.

---

- [ ] Step 1: Feature flag `PI_SUBAGENT_ASYNC`

Create `internal/subagent/async.go` with:

- `const AsyncEnvVar = "PI_SUBAGENT_ASYNC"`
- `func AsyncEnabled() bool` — true for `1`, `true`, `yes`, `on`
  (case-insensitive, trimmed); everything else (unset, empty, `0`, `false`,
  garbage) is **false**.
- Follow the defensive style of `ConcurrencyFromEnv`
  (`internal/subagent/concurrency.go:26-49`): never an error, never a panic. There
  are no invalid values — anything not affirmative is off.
- `DefaultAsyncLifetime = 60 * time.Minute` (used in step 12), with a doc comment
  explaining that a detached agent holds a concurrency-pool slot for its whole life,
  so a forgotten agent must not pin it forever.
- Document that this flag is **inherited by child processes**: `PI_` is a forwarded
  prefix in `DefaultEnvAllowlist` (`internal/subagent/environ.go:44`) and `ChildEnv`
  rewrites only `PI_SUBAGENT_CONCURRENCY` (`:105-113`).

Create `internal/subagent/async_test.go` with table tests via `t.Setenv`: unset ⇒
false; `""` ⇒ false; `"0"` ⇒ false; `"false"` ⇒ false; `"garbage"` ⇒ false; `"1"` ⇒
true; `"true"` ⇒ true; `"YES"` ⇒ true; `"on"` ⇒ true; `" 1 "` ⇒ true (trimmed).

**Files:** `internal/subagent/async.go` (new), `internal/subagent/async_test.go` (new)
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestAsync' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestAsync'`
**Depends on:** nothing
**Parallel-safe:** yes (shares no files with step 2)

---

- [ ] Step 2: `Pool.TryAcquire`

Add to `internal/subagent/pool.go`:

```go
// TryAcquire takes a slot without blocking. It reports false when every slot
// is taken. Unlike Available(), it is atomic with the acquisition, so two
// racing callers cannot both observe a free slot.
func (p *Pool) TryAcquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}
```

Leave `Acquire` (`pool.go:24-33`), `Release`, `Size`, and `Available`
**unchanged**; synchronous `Spawn` keeps blocking `Acquire`. Document on
`Available()` that it is a diagnostic, not a probe to gate acquisition on.

Extend `internal/subagent/pool_test.go`: a 1-slot pool — first `TryAcquire` true,
second false, after `Release` true again; a 3-slot pool — three trues then false;
concurrent callers (spawn N goroutines against a small pool under `-race`; assert
the number of trues equals the size).

**Files:** `internal/subagent/pool.go`, `internal/subagent/pool_test.go`
**Verify:** `go test -race ./internal/subagent/ -list '^TestPool_TryAcquire' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestPool_TryAcquire'`
**Depends on:** nothing
**Parallel-safe:** yes (shares no files with step 1)

---

- [ ] Step 3: Protocol selection and first-prompt delivery

Add `Mode string` to `SpawnOpts` (`internal/subagent/spawner.go:38-54`) with a doc
comment: `""` or `"json"` is the one-shot `--mode json` path (blocking, prompt in
argv); `"rpc"` is the steerable `--mode rpc` path (stdin-driven, many turns, **no
positional prompt**).

In `spawnArgs` (`spawner.go:110-132`): when `Mode == "rpc"`, emit `--mode rpc` and
**do not append the positional prompt**. All other flags (`--model`, `--url`,
`--insecure`, `--header`, `--system`, `--lsp`) keep their current order and
behaviour; the positional prompt stays last for the json path.

In `Spawn` (`spawner.go:237-245`): keep the single empty-prompt guard
(`"prompt is required"`) for every mode — an rpc spawn still requires a prompt
because it is turn 1 — but for rpc the prompt is **not** placed in argv; it is
stored so the caller can deliver it via `SendPrompt` (step 5). Keep this as one
guard, not a mode-specific branch that drops validation.

Note in a comment that `pi --mode rpc` with no positional argument is legal because
`dispatchMode`'s rpc branch returns before the `prompt == ""` check
(`internal/cli/cli.go:1150-1174` vs `:1176`).

Extend `internal/subagent/spawner_test.go`: with `Mode:"rpc"` the args contain
`--mode rpc` and **not** the prompt string; with `Mode:""` the argv is unchanged
from today (assert the full slice so the json path is pinned); `Mode:"json"`
behaves like `""`; an rpc spawn with an empty prompt still errors.

**Files:** `internal/subagent/spawner.go`, `internal/subagent/spawner_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestSpawnArgs_RPC' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestSpawnArgs'`
**Depends on:** nothing
**Parallel-safe:** no (shares `spawner.go` with steps 4–6)

---

- [ ] Step 4: rpc → `subagent.Event` translation

Add an `rpcEvent` type for the `--mode rpc` stdout schema. The wire shapes are in
`internal/pirpc/rpc.go`: `message_update` nests its payload as
`assistantMessageEvent{type,delta}` (`:385-393`); `tool_execution_start` uses
`toolCallId`/`toolName`/`args` (`:344-350`); `tool_execution_end` uses
`toolCallId`/`result`/`isError` (`:357-363`); `agent_start` / `agent_end` /
`agent_settled` (`:273-274`); and a `response` command envelope (`:137-145`).

Mark `Process` as rpc (`rpc bool`) and branch in `emitChildLine`
(`spawner.go:330-352`) so rpc lines never fall through the `jsonEvent` switch.
Translation table (design §4.2):

- `response` → **not an event**; route to the ack channel (step 5).
- `agent_start`, `agent_end` → ignored.
- `message_update` + `assistantMessageEvent.type == "text_delta"` →
  `Event{Type:"text_delta", Content: delta}` **and append to the accumulated
  result**. The json path appends in its `text_delta` case (`spawner.go:339-341`);
  rpc must accumulate too or `Process.result` stays empty.
- `message_update` + `"thinking_delta"` → `Event{Type:"thinking_delta", Content: delta}`.
- `tool_execution_start` → `Event{Type:"tool_call", Content: toolName, ToolArgs: args}`.
- `tool_execution_end` → `Event{Type:"tool_result", Content: <marshalled result>}`.
  **`result` is an object and `Event.Content` is a `string`**
  (`internal/subagent/types.go:100-108`), so `json.Marshal` it; on a marshalling
  error fall back to `fmt.Sprint` rather than dropping the event, and surface
  `isError` in the content when true.
- `agent_settled` → signal the settle (step 5) and emit
  `Event{Type:"message_end"}`.

Do **not** invent an `error` event. rpc has no such type: the child renders failures
as assistant text via `emitError` (`internal/pirpc/rpc.go:395-402`), which arrives
as `text_delta` and is already model-visible.

Create `internal/subagent/spawner_rpc_test.go` driving the pump with a temp
`#!/bin/bash` script as `Spawner.PiBinary` (`mockPiScript`, `spawner_test.go:14`;
`t.Skip` on Windows). Assert each table row: text arrives as `text_delta` and
accumulates into the result; `thinking_delta` is distinguished; a tool call carries
name + args; a tool result is a JSON **string** (not `nil`, not an object);
`response`/`agent_start`/`agent_end` produce no user-visible event; a non-JSON line
still falls back to text.

**Files:** `internal/subagent/spawner.go`, `internal/subagent/spawner_rpc_test.go` (new)
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestRPCEmitChildLine' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestRPCEmitChildLine'`
**Depends on:** step 3
**Parallel-safe:** no

---

- [ ] Step 5: stdin, turn signalling, and acknowledgements

For `Mode == "rpc"`, `Spawner.buildCommand` (`spawner.go:136-156`) must set
`cmd.Stdin` from `cmd.StdinPipe()`. The pi path currently never sets stdin; ACP and
codex already do (`internal/acp/client/runner.go:37`,
`internal/codex/client.go:110`). Keep `cmd.WaitDelay = 3 * time.Second` and the
existing platform attrs.

Extend `Process` with:

```go
// added to the existing Process struct (spawner.go:78-85)
settled chan struct{} // buffered(1); signalled on each agent_settled
acks    chan ack      // per-turn command acknowledgements
done    chan struct{} // closed when the child exits
stdin   io.WriteCloser
```

Add:

- `func (p *Process) SendPrompt(prompt string) error` — writes one newline-delimited
  `{"type":"prompt","id":"<uuid>","message":<prompt>}` to stdin and records that id
  as the current turn. Returns an error if the write fails.
- `func (p *Process) Settled() <-chan struct{}`
- `func (p *Process) Acks() <-chan ack`
- `func (p *Process) Done() <-chan struct{}`

**Acknowledgement handling.** Every rpc command gets exactly one `response` envelope
(`internal/pirpc/rpc.go:137-145`) and it can report failure (`success:false` /
`error`, e.g. `"message is required"` at `:191-193`, or the malformed-command reply
at `:171-177`). Route `response` lines to the ack channel rather than ignoring them,
correlate by id, and treat a failed ack as **failing the turn** — otherwise the
writer waits forever for an `agent_settled` that will never arrive. Uncorrelated or
stale-id acks are logged and discarded, never treated as a settle.

`agent_settled` does a **non-blocking** send to `settled` (buffered(1)) — never a
blocking send that could wedge the pump. Draining `settled` before each write stops
a late or duplicate settle from a previous turn satisfying the next turn's wait.

Close `done` in `pumpChildOutput`'s exit path alongside its existing `defer
close(p.done)` handling, so a mid-turn crash is observable.

Extend `spawner_rpc_test.go`: a scripted child that emits
`{"type":"response",...,"success":true}` then `agent_settled` — assert `SendPrompt`
succeeds, `Settled()` fires, the ack arrives. A script replying `success:false` —
assert the ack carries the error. A script that exits immediately — assert `Done()`
closes. Assert `settled` is buffered(1): a settle nobody waits on does not block the
pump.

**Files:** `internal/subagent/spawner.go`, `internal/subagent/spawner_rpc_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestRPCProcess' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestRPCProcess'`
**Depends on:** step 4
**Parallel-safe:** no

---

- [ ] Step 6: Per-turn inactivity (the slice that keeps an idle agent alive)

Today the inactivity timer is armed at process spawn and kills on silence
(`spawner.go:280-281,319-323`). An async agent **waiting for its next steer** is
silent by definition, so it would be killed as wedged. Fix: silence counts as wedged
**only while a turn is in flight**.

Ownership: the timer stays created and owned by `pumpChildOutput` (**not** the
writer), so there is exactly one owner and no concurrent `Reset`/`Stop`.
`InactivityTimer` owns a single `*time.Timer` and exposes only `Reset`/`Stop`/`C`
(`internal/subagent/timeout.go:79-113`).

Arm/disarm is therefore a **signal from the writer to the pump**, not a direct timer
call. Add a small turn-state channel (e.g. `turnState chan bool`) to `Process`;
`readChildLines` (`spawner.go:304-326`) selects on stdout lines, the idle channel,
**and** this turn-state channel. On "turn started" it resets the timer and treats
silence as wedged; while no turn is in flight it ignores the idle channel.

The json path (`Mode != "rpc"`) must behave exactly as today: armed at spawn, killed
on silence. Keep that path untouched.

Keep the **process-level absolute** timeout applied once at `Spawn` via
`context.WithTimeout(ctx, timeoutCfg.Absolute)` (`spawner.go:243-244`) — it is a
whole-process deadline, not per-turn. A per-turn absolute is explicitly out of scope.

Make the inactivity value test-overridable (a package-level duration var in the
`ConcurrencyEnvVar` style, or an extension to `ResolveTimeout`) so tests never wait
5 minutes.

Extend `spawner_rpc_test.go` with injected short durations:

- an rpc child that sleeps with **no turn in flight** past the inactivity value is
  **not** killed;
- a turn in flight producing no output past the value **is** killed, and the error
  is a timeout (`childExitError`, `spawner.go:358-372`);
- the json path still kills on silence (regression guard for the old behaviour).

**Files:** `internal/subagent/spawner.go`, `internal/subagent/timeout.go`, `internal/subagent/spawner_rpc_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestRPCInactivity' | grep -q '^Test' && go test ./internal/subagent/ -run '^TestRPCInactivity'`
**Depends on:** step 5
**Parallel-safe:** no

---

- [ ] Step 7: Async state, the copied output stream, and the lock contract

Copy the bounded incremental `stream` from `internal/tools/bash_stream.go:20-114`
into a new `internal/subagent/async_stream.go` **plus its tests**. Required because
`internal/subagent` importing `internal/tools` is an import cycle
(`internal/tools/agent.go:6` already imports `internal/subagent`). Add a comment
naming the source and noting the deliberate duplication. The contract needed is
`since(off int64) (data string, next int64, dropped int64)` (`bash_stream.go:64-75`)
— exactly the incremental read plus dropped-byte accounting R10 requires. Keep the
`streamCap = maxOutputBytes = 256KB` bound.

Add `async *asyncState` to `agentState` (`internal/subagent/orchestrator.go:92-103`),
nil for synchronous agents, so every existing synchronous path is unaffected. Define
`asyncState` in `internal/subagent/async.go` (design §4.3):

```go
type asyncState struct {
	mu       sync.Mutex
	stdin    io.WriteCloser // child stdin; closing it ends the child
	pending  []string       // FIFO steer queue, not yet written
	wake     chan struct{}  // buffered(1): "queue non-empty or stop"
	stop     chan struct{}  // closed once, on shutdown/lifetime/terminal
	out      *stream        // accumulated output (bounded, drops oldest)
	cursor   int64          // agent-owned read cursor into out
	inTurn   bool           // a turn is in flight
	turn     int            // completed turns
	last     string         // final result, replayed idempotently
	done     chan struct{}  // closed when the writer goroutine exits
	stopOnce sync.Once
	termOnce sync.Once
}
```

Write the lock contract as a doc comment on `asyncState`: **`o.mu → asyncState.mu`,
and never hold any mutex across I/O.** Explain why: `Cancel` and
`ShutdownWithTimeout` hold `o.mu` across `Process.Cancel()`
(`orchestrator.go:806-819`, `:860-872`), so if the async kill path also took
`asyncState.mu`, a drainer holding `asyncState.mu` and needing `o.mu` would deadlock.
Add: `asyncState.mu` guards fields only, never I/O; `o.mu` remains the sole owner of
`Status`/`FinishedAt`; `o.mu` is never taken while holding `asyncState.mu`.

Add `async_test.go` cases: the copied stream's `since` returns incremental disjoint
slices and reports `dropped` past the cap; `agentState.async` is nil for a
synchronous agent (construct via the existing test helpers and assert).

**Import check.** Confirm `internal/subagent` still does not **import**
`internal/tools`, using the compiler's view rather than a text grep — the comment
naming the copy source would trip a grep and make the check pass or fail for the
wrong reason:

```
go list -f '{{join .Imports "\n"}}' ./internal/subagent | grep -c '/internal/tools'
```

must print `0`.

**Files:** `internal/subagent/async_stream.go` (new), `internal/subagent/async_stream_test.go` (new), `internal/subagent/async.go`, `internal/subagent/orchestrator.go`, `internal/subagent/async_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestAsyncStream' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncStream'`
**Depends on:** steps 1, 6
**Parallel-safe:** no

---

- [ ] Step 8: Terminal state machine

Implement the design §4.3 transition table in `internal/subagent/async.go`, guarded
by `termOnce` so exactly one terminal transition happens per agent:

| Trigger | Status |
| --- | --- |
| async lifetime cap fires | `canceled` |
| `Cancel` / `ShutdownWithTimeout` | `canceled` |
| child exits non-zero | `failed` (`terminalStatus`) |
| inactivity fires during a turn | `timeout` |
| whole-process absolute deadline fires | `timeout` |
| rpc ack reports failure | `failed` |

**An async agent has no natural `completed` state, and the code must not invent
one.** The rpc child keeps serving after every turn, so an async agent waits for
further input until the lifetime cap or shutdown ends it. `completed` remains
reserved for synchronous agents (`terminalStatus`). This is why lifetime expiry
must set `canceled` explicitly rather than letting the child's clean exit be read as
`completed`.

**Lifetime expiry must set the status explicitly.** Simply closing stdin makes the
child exit cleanly, which `terminalStatus` (`orchestrator.go:648-659`) would report
as `completed` — hiding that the agent was stopped early. Do exactly what `Cancel`
does today: set the status and `FinishedAt` under `o.mu`
(`orchestrator.go:816-818`).

On the terminal transition, copy the accumulated text into `last` under
`asyncState.mu`. `last` is what makes `Attach` idempotent for a finished agent (R5)
— it lives outside `cursor`, so repeated reads do not consume it.

Set `Status`/`FinishedAt` only under `o.mu`, never under `asyncState.mu`.

Extend `async_test.go`: each row transitions to the expected status; a second
trigger cannot change it (idempotent terminal); lifetime expiry yields `canceled`
and **not** `completed`; `last` is populated from the accumulated output at the
terminal transition (there is no clean `completed` path for an async agent).

**Files:** `internal/subagent/async.go`, `internal/subagent/async_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestAsyncTerminal' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncTerminal'`
**Depends on:** step 7
**Parallel-safe:** no

---

- [ ] Step 9: `SpawnAsync`

Add to `internal/subagent/orchestrator.go`:

```go
var ErrNoCapacity = errors.New("subagent pool has no free slot")
var ErrNotSteerable = errors.New("agent does not accept input in this build")

func (o *Orchestrator) SpawnAsync(ctx context.Context, input SpawnInput) (string, error)
```

`SpawnAsync` mirrors `Spawn` (`orchestrator.go:419-514`) with these differences:

- **Backend gate first:** only agents routed to `o.spawner.Spawn` can be detached —
  the default branch of `dispatchSpawn` (`orchestrator.go:575-584`). `isACPAgent`
  (`:78-84`) or `isCodexAgent` (`internal/subagent/spawner_codex.go:17-20`) ⇒ return
  `ErrNotSteerable` **before acquiring a slot or creating a worktree**.
- **Non-blocking pool:** `o.pool.TryAcquire()` (step 2); on false return
  `ErrNoCapacity` **immediately**. Never block — a stalling async spawn contradicts
  its name and turns a capacity problem into a UI hang.
- **Detached context:** the child's context must not descend from the calling turn,
  or `exec.CommandContext` kills the process group when the parent turn ends or the
  user presses Esc/Ctrl+C (`internal/tui/tui.go:1213-1240` →
  `internal/tui/agent_loop.go:823-858`; kill path `spawner.go:136`, `:243-244`). Use
  the bash precedent for detached work — `context.WithoutCancel(ctx)`
  (`internal/tools/bash_supervisor.go:246`) — and layer the async lifetime cap on
  top.
- Set `SpawnOpts.Mode = "rpc"`, and build the `agentState` with `async` initialised
  (step 7) and `Status: "running"`.
- Register the **drainer before the first turn is written** (step 11), or turn 1
  output can fill the 64-slot event buffer and be dropped.
- On any failure after `TryAcquire` succeeds, release the slot exactly once (mirror
  `abandonSpawn`, `orchestrator.go:588-593`).

Do **not** modify `Spawn`. Synchronous behaviour, including blocking `Acquire`,
stays as it is.

Extend `async_test.go`:

- `t.Setenv(subagent.ConcurrencyEnvVar, "1")` with one agent held ⇒ `TryAcquire`
  fails ⇒ `ErrNoCapacity`, asserted with a short deadline so a regression to
  blocking **fails the test instead of hanging**;
- a codex/ACP name ⇒ `ErrNotSteerable`, and no worktree was created and no slot
  consumed;
- `errors.Is` matches both sentinels;
- a successful async spawn returns a non-empty `agent_id` and leaves
  `agentState.async` non-nil with `Status == "running"`.

**Files:** `internal/subagent/orchestrator.go`, `internal/subagent/async.go`, `internal/subagent/async_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestSpawnAsync' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestSpawnAsync'`
**Depends on:** step 8
**Parallel-safe:** no

---

- [ ] Step 10: Steer queue and the writer goroutine

Implement the writer goroutine in `internal/subagent/async.go`. It owns the turn
loop and is the **only** writer to the child's stdin.

**Invariant (R12): never write a second `prompt` before the previous turn's
`agent_settled`.** Mandatory, not an optimization: the child keeps a single
`s.cancel` slot (`internal/pirpc/rpc.go:99-103,260-275`), and a second concurrent
prompt's deferred cleanup sets `s.cancel = nil`, erasing the first turn's handle so
`abort` reaches only the newer turn.

The loop must select on **all** of: `settled`, `acks`, `proc.Done()`, the lifetime
timer, `stop`, and `wake`. **Never select on `Settled()` alone** — a child that
crashes mid-turn never emits `agent_settled`, so a settle-only wait would deadlock
forever. On `proc.Done()` or a failed ack, take the terminal transition (step 8) and
exit.

Wake-up: `Steer` appends to `pending` under `asyncState.mu`, then does a
**non-blocking send** to `wake` (buffered(1)), so a signal is never lost and a
blocked writer is always woken. The writer drains one message per turn and blocks
when the queue is empty.

Turn 1 is written through the **same** `SendPrompt` path as every later turn — no
first-turn special case (step 3 stores the prompt for this).

Timer interaction: signalling "turn started" arms the per-turn inactivity (step 6);
`agent_settled` disarms it. Do not call the timer directly from the writer — signal
the pump, which owns it.

Create `internal/subagent/async_turn_test.go` with the scripted rpc child. The
**script must record every prompt it receives** (append to a file) so the ordering
invariant is assertable:

- two steers queue in FIFO order and are written one per settle;
- **assert prompt #2 appears only after prompt #1's `agent_settled`** — the R12
  guard, and the single most important test in this plan;
- `wake` is not lost: a steer arriving while the writer is blocked is picked up;
- many rapid steers queue rather than interleave.

**Files:** `internal/subagent/async.go`, `internal/subagent/async_turn_test.go` (new)
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestAsyncWriter' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncWriter'`
**Depends on:** step 9
**Parallel-safe:** no

---

- [ ] Step 11: Drainer, `Steer`, and `Attach`

Implement the **drainer goroutine**. `forwardAgentEvents` does a blocking
`out <- ev` and releases the pool slot only when the stream closes
(`orchestrator.go:611-617`), so an async agent needs a detached consumer or the pump
blocks and the slot is pinned for the process lifetime.

The drainer:

- writes each event's text into `asyncState.out` (bounded; drops oldest);
- forwards each event it receives to the `SubagentEventCallback`;
- treats `message_end` as the turn boundary.

It must **never block**: a blocking drainer pins the pool slot, and
`Process.sendEvent` already drops events when the 64-slot channel fills
(`spawner.go:374-380`). So the requirement is **"never block the pump"**, not
"forward every event" — do not promise or test for lossless forwarding.

Attach the drainer **before the first turn is written** (step 9 ordering).

Implement in `internal/subagent/async.go` / `orchestrator.go`:

```go
func (o *Orchestrator) Steer(agentID, message string) (AsyncStatus, error)
func (o *Orchestrator) Attach(agentID string) (AsyncStatus, error)
```

with `AsyncStatus` per design §4.4 (`AgentID`, `Status`, `Running`, `Output`,
`Dropped`, `Queued`, `Turn`, `Note`).

**Cursor semantics: the cursor is owned by the *agent*, not the caller.** A tool call
carries no caller identity, so "the caller's last read" cannot be tracked per
invocation. `Steer`/`Attach` read `stream.since(cursor)` and advance `cursor`.
Consequence: overlapping tool calls share the cursor; the second sees only what
arrived after the first. Document this in the tool description (step 16).

**Idempotent final result (R5):** on a terminal agent, `Attach` returns `last` and
does **not** advance `cursor`, so repeated polls keep returning the final result —
deliberately unlike bash, which forgets a spent handle
(`internal/tools/bash_supervisor.go:633`).

`Steer` on a terminal agent delivers nothing and returns the terminal status.
`Attach`/`Steer` on an unknown or evicted id returns `Status: "unknown"` with a
**nil error** — agent-level conditions are values, not errors (mirrors
`subagent.go:381-383`). A missing `agent_id` is the caller's mistake and *is* an
error (that check lives in the tool layer, step 14).

Report `Dropped` bytes when output aged out of the buffer, and `Queued` as the
pending-steer count.

Extend `async_turn_test.go`: two attaches return **disjoint, ordered** output; a
finished agent replays `last` on every attach; an unknown id yields
`status:"unknown"` and nil error; `Dropped` is non-zero past the cap; `Queued`
tracks pending steers.

**Files:** `internal/subagent/async.go`, `internal/subagent/orchestrator.go`, `internal/subagent/async_turn_test.go`
**Verify:** `go build ./... && go test ./internal/subagent/ -list '^TestAsyncAttach' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncAttach|^TestAsyncSteer'`
**Depends on:** step 10
**Parallel-safe:** no

---

- [ ] Step 12: Survival, shutdown, and the lifetime cap

**Survival:** assert (do not re-implement) that a detached child survives the
spawning turn ending and the parent turn's cancellation. Step 9's
`context.WithoutCancel` is the mechanism; the TUI cancel path cancels only the
per-turn context (`internal/tui/agent_loop.go:823-866`,
`internal/tui/tui.go:1213-1239`).

**Shutdown:** `ShutdownWithTimeout` (`orchestrator.go:848-883`) already marks every
`"running"` agent `canceled` and calls `Process.Cancel()` — keep that behaviour.
Add, for async agents:

- collect the running async states under `o.mu` and raise their `stop` signal
  **outside `o.mu`** (the loop at `:860-872` holds `o.mu` across `Process.Cancel()`;
  raising `stop` inside would break the step 7 lock order and risk deadlock with a
  drainer);
- close stdin and **drop** queued steer messages — never flush them into a dying
  child;
- use `stopOnce` so this is idempotent when `Cancel` and shutdown race.

**Do not rely on stdin EOF to end an in-flight turn.** EOF only ends the child's
scanner loop; the running turn's context derives from the server context
(`internal/pirpc/rpc.go:161-183`, `:260-275`), so EOF is a shutdown hint, not a
cancellation protocol. `Process.Cancel()` is the reliable stop, and every terminal
path must have it available.

**Lifetime cap:** a `DefaultAsyncLifetime` (60m, step 1) timer per agent; on fire,
close stdin, set the status to `canceled` explicitly (step 8), and release the pool
slot. Make the duration test-overridable.

**`agent_settled` is not terminal.** It ends a *turn* and returns the agent to
"waiting for input"; the agent stays `running` until the lifetime cap or shutdown.
Do not set `completed` on settle — an async agent has no natural completion, and
doing so would make a steered agent appear finished.

Create `internal/subagent/async_lifetime_test.go`:

- cancelling the parent context does **not** kill the child; the agent keeps running
  and keeps accepting a steer;
- `ShutdownWithTimeout` **does** kill it, marks it `canceled`, drops a queued steer,
  and returns without deadlock;
- an injected short lifetime stops an idle agent, marks it `canceled` (not
  `completed`), and frees the pool slot;
- `steer` racing `shutdown` ⇒ exactly one terminal transition, message dropped, no
  deadlock — run under `-race`.

**Files:** `internal/subagent/orchestrator.go`, `internal/subagent/async.go`, `internal/subagent/async_lifetime_test.go` (new)
**Verify:** `go test ./internal/subagent/ -list '^TestAsyncLifetime' | grep -q '^Test' && go test -race ./internal/subagent/ -run '^TestAsyncLifetime'`
**Depends on:** step 11
**Parallel-safe:** no

---

- [ ] Step 13: `background` on the `subagent` tool

Add to `SubagentInput` (`internal/tools/subagent.go:31-42`):

```go
// Background detaches the agent: the call returns immediately with an
// agent_id, and the child keeps running after this turn ends. Single mode
// only. Requires PI_SUBAGENT_ASYNC.
Background bool `json:"background,omitempty"`
```

With `omitempty` this does not join the declaration schema's `required` list and
input schemas stay open (`internal/tools/registry.go:117-143`), so existing callers
are unaffected.

Implement the async branch in `singleModeHandler` (`subagent.go:204-274`):

- `background: true` + flag off ⇒ return `SubagentOutput` with an `AgentResult`
  whose `Status` is `"disabled"` and whose `Error`/`Result` explains how to enable
  it. No process, no error.
- `background: true` + flag on ⇒ `orch.SpawnAsync`; on success return immediately
  with `Status: "running"`, the `agent_id`, and a `Summary`/`Result` note naming
  `subagent_input` (model the note on `bash_supervisor.go:381-389`, which names the
  control tools explicitly).
- **Map the sentinel errors to values with `errors.Is` *before* the generic
  spawn-error path.** The existing path collapses every `Spawn` error into
  `AgentResult{Status:"failed"}` (`subagent.go:227-238`); without an explicit branch
  a full pool would surface as `"failed"` and the model could not tell "retry later"
  from "broken". `ErrNoCapacity` ⇒ `"no_capacity"`; `ErrNotSteerable` ⇒
  `"not_steerable"`.

**Do not change `detectMode`** (`subagent.go:186`). `Background` is not a mode
discriminator, so chain > parallel > single resolution is untouched, and
parallel/chain remain synchronous.

Extend `internal/tools/subagent_test.go` (drives `subagentHandler` directly with a
real orchestrator and a nil `agent.Context`, `subagent_test.go:81`): flag off +
`background` ⇒ `"disabled"`, no process; flag on + full pool ⇒ `"no_capacity"` and
**not** `"failed"`; codex/ACP name + `background` ⇒ `"not_steerable"`; flag on +
success ⇒ returns promptly with `Status:"running"` and a non-empty `AgentID`; a call
**without** `background` is byte-for-byte unchanged.

**Files:** `internal/tools/subagent.go`, `internal/tools/subagent_test.go`
**Verify:** `go build ./... && go test ./internal/tools/ -list '^TestSubagentBackground' | grep -q '^Test' && go test ./internal/tools/ -run '^TestSubagentBackground'`
**Depends on:** step 12
**Parallel-safe:** yes (disjoint files from step 14)

---

- [ ] Step 14: the `subagent_input` tool

Create `internal/tools/subagent_input.go`:

```go
type SubagentInputToolInput struct {
	AgentID string `json:"agent_id"`           // required
	Message string `json:"message,omitempty"`  // empty = read-only attach
	WaitSec int    `json:"wait_sec,omitempty"` // park up to 60s for new output
}

func AsyncSubagentTools(orch *subagent.Orchestrator, onEvent SubagentEventCallback) ([]tool.Tool, error)
```

**`AsyncSubagentTools` returns `(nil, nil)` when `AsyncEnabled()` is false**, so
every assembly site can `append` unconditionally. This is the memory/palace/LSP
precedent — constructors returning `(nil, nil)` when unusable: `MemoryTools`
(`internal/tools/mem_search.go:207-212`), `PalaceTools`
(`internal/palace/tools.go:7-12`), `LSPToolsFor(mgr, LSPOff)`
(`internal/tools/lsp.go:258-263`). `CoreOption` **cannot** gate tools — it only
injects shared state (`internal/tools/registry.go:17-40`).

Build the tool with `newTool` (`registry.go:219`), name `"subagent_input"`. Consider
the alias map approach used by `subagent` for common LLM field-name mistakes
(`subagent.go:97-103`).

Handler behaviour:

- `agent_id` missing ⇒ **Go error** naming the live ids (mirror `bash_wait`'s
  `"handle is required (running: %v)"`, `internal/tools/bash.go:251-258`);
- `message` non-empty ⇒ `orch.Steer(agentID, message)`; empty ⇒
  `orch.Attach(agentID)`; both return `AsyncStatus` as the tool result;
- `wait_sec` clamps to a max of 60s (mirror `maxBashWait`,
  `bash.go:118,251-258`) and parks for output before reading; `0`/omitted means no
  wait;
- never translate an agent-level status into an error — return the structured
  status (R7/R9).

The tool description must state plainly: input is **queued as the agent's next
turn** (not injected mid-turn), output is read **once** (cursor is agent-owned, not
per caller), and results are values not errors.

Create `internal/tools/subagent_input_test.go`: flag off ⇒ `(nil, nil)`; flag on ⇒
one tool named `subagent_input`; missing `agent_id` ⇒ error naming live ids; unknown
id ⇒ `status:"unknown"` and no error; `message` set ⇒ goes to `Steer`; `message`
empty ⇒ goes to `Attach`; `wait_sec` clamps to 60.

**Files:** `internal/tools/subagent_input.go` (new), `internal/tools/subagent_input_test.go` (new)
**Verify:** `go build ./... && go test ./internal/tools/ -list '^TestSubagentInput' | grep -q '^Test' && go test ./internal/tools/ -run '^TestSubagentInput'`
**Depends on:** step 12
**Parallel-safe:** yes (disjoint files from step 13)

---

- [ ] Step 15: register at the five assembly sites

Append the async control tools at each site where agent tools are added today,
gated on the flag (the constructor itself is flag-aware) and on subagent
enablement at that site:

- `internal/cli/interactive.go` — near `tools.AgentTools(orch, ...)` (`:226-227`),
  matching the surrounding `append(coreTools, ...)` style used for the bash control
  tools (`:418-422`).
- `internal/cli/cli.go` — near `AgentTools(orch, ...)` (`:720-726`).
- `internal/acp/server/runtime.go` — near `AgentTools(orch, noop)` (`:470-475`).
- `piagent/agent.go` — inside the existing `if o.subagentEnabled` block (`:249-255`).
- `internal/eval/inventory.go` — add an inventory entry with a `Requires` label,
  following the existing `add(...)` pattern (`:75-82`).

Keep each edit to a single `append`; do not restructure the existing tool assembly.

Extend tests where a tool-list count is asserted. `internal/eval/coverage_test.go`
hard-codes the print-mode core tool set (`:27-31`), so it needs the async entry
when the flag is on — add `TestInventoryIncludesAsyncTools` under `t.Setenv(
subagent.AsyncEnvVar, "1")`, asserting `subagent_input` is present with a
`Requires` label, and that it is absent with the flag unset.

**Files:** `internal/cli/interactive.go`, `internal/cli/cli.go`, `internal/acp/server/runtime.go`, `piagent/agent.go`, `internal/eval/inventory.go`, `internal/eval/coverage_test.go`
**Verify:** `go build ./... && go test ./internal/eval/ -list '^TestInventoryIncludesAsyncTools' | grep -q '^Test' && go test ./internal/eval/ -run '^TestInventoryIncludesAsyncTools' && go test ./internal/cli/ ./piagent/ ./internal/acp/...`
**Depends on:** step 14
**Parallel-safe:** no

---

- [ ] Step 16: Keep the `subagent` description honest

Extend `buildSubagentDescription` (`internal/tools/subagent.go:117-160`) — it
already varies its text on runtime conditions (agent names, `orch.Concurrency()`,
`:150-157`), so this is the established place:

- when `subagent.AsyncEnabled()`, document `background: true` (single mode,
  detached, returns an `agent_id`) and name `subagent_input` explicitly;
- state that input is queued as the agent's **next** turn, that output is read once
  (agent-owned cursor), and that a finished agent replays its final result;
- recommend **non-worktree** agents for `background`, because a detached worktree
  agent holds its checkout until `CleanupAll`/shutdown
  (`orchestrator.go:636-641`, requirement D7);
- when the flag is off, say **nothing** about async — the description must not
  advertise a tool that is not registered.

Extend `internal/tools/subagent_description_test.go`, which already pins the
description's varying text (`:16-46`) and uses the
`t.Setenv(subagent.ConcurrencyEnvVar, "4")` seam (`:15-21`): with
`PI_SUBAGENT_ASYNC` unset the description contains no `background`/`subagent_input`;
with it set, both appear and the next-turn/queued wording is present. This is the
same guard pattern as `bash_control_test.go:20-43`, which asserts the `bash`
description names its control tools.

**Files:** `internal/tools/subagent.go`, `internal/tools/subagent_description_test.go`
**Verify:** `go build ./... && go test ./internal/tools/ -list '^TestSubagentDescriptionAsync' | grep -q '^Test' && go test ./internal/tools/ -run '^TestSubagentDescriptionAsync'`
**Depends on:** step 13
**Parallel-safe:** no

---

- [ ] Step 17: verification sweep

**ADK non-interference.** Assert the async tool is an ordinary immediate-result tool:
it does **not** set `IsLongRunning` and does not park the runner. ADK v2 has
long-running support (`IsLongRunning`, `LongRunningToolIDs`, `ResponseDeferrer`) but
it models an externally supplied *response* — no handle, cursor, or queue — and
would park the parent runner, the opposite of the intent (design §7b). Add a test
asserting the tool's `IsLongRunning()` is false.

**Failure-path tests** (fold into the new test files):

- child crashes mid-turn without `agent_settled` ⇒ writer exits via `proc.Done()`,
  no deadlock, status is a failure;
- rpc `prompt` rejected (`success:false`) ⇒ the turn fails with that error and the
  writer does not hang;
- stdin EOF during a turn ⇒ not treated as a clean settle;
- `steer` racing `shutdown` ⇒ one terminal transition, message dropped;
- lifetime expiry ⇒ `canceled`, slot released.

**Non-regression with the flag unset:**

- the existing suite passes **unmodified**;
- no `--mode rpc` child is created (assert a synchronous spawn's recorded argv
  contains `--mode json` and the prompt);
- `subagent_input` is absent from the assembled tool list;
- `detectMode` resolves identically for every existing input shape;
- no import cycle: `go list -f '{{join .Imports "\n"}}' ./internal/subagent` contains
  no `internal/tools` entry (checked via `go list`, not a text grep, because the
  copy-source comment legitimately names it).

**Race sweep:** `go test -race ./internal/subagent/... ./internal/tools/...` over
Steer/Attach/Cancel/Shutdown/lifetime/drainer termination.

If any check fails, fix it in the owning slice rather than weakening the assertion
here.

**Files:** `internal/tools/subagent_test.go`, `internal/subagent/async_turn_test.go`, `internal/subagent/async_lifetime_test.go`, `internal/subagent/spawner_test.go`, `internal/subagent/spawner_rpc_test.go`
**Verify:** `go build ./... && go vet ./... && go test ./... && go test -race ./internal/subagent/... ./internal/tools/...`
**Depends on:** steps 15, 16
**Parallel-safe:** no

---

## Gates

Run after every slice; the last two are for the merge.

| Gate | Command |
| --- | --- |
| build | `go build ./...` |
| vet | `go vet ./...` |
| unit tests | `go test ./...` (equivalently `make test-unit`) |
| race | `go test -race ./internal/subagent/... ./internal/tools/...` |
| lint before merge | `golangci-lint run ./...` (equivalently `make lint`) |

There is no generic "targeted" gate: use each slice's own **Verify** command, which
names its concrete `-run` pattern.

`go version` in this repo is 1.27.1. Do not use `make build` as a gate: it runs
`cache-clean` first (`golangci-lint cache clean`), which is unnecessary per-slice and
adds a golangci-lint dependency to the loop.

## Out of scope — record, do not implement

- **codex steering** — `turn/steer {threadId, expectedTurnId, input[]}` and
  `turn/start {threadId, input[]}` exist; `Session.finish` → `client.close()` kills
  the process group (`internal/codex/session.go:586`,
  `internal/codex/client.go:349-353`), `threadID` is unexported, and the single-slot
  `turnID` tracker would misattribute a second turn. Own spec.
- **ACP steering** — repeat `session/prompt` on a retained connection; needs
  `RunningSession` to keep `conn` + an `io.WriteCloser` stdin, stop closing stdin
  (`internal/acp/client/session.go:191,210`), stop `cmd.Wait()`ing after the first
  prompt, and expose the session id. Own spec.
- **Mid-turn injection** inside `pirpc` — `set_steering_mode`/`set_follow_up_mode`
  are accepted no-ops (`internal/pirpc/rpc.go:244-248`); needs child-side changes.
- **Per-turn absolute timeout** — v1 keeps the whole-process absolute (20m) plus the
  per-turn inactivity plus the 60m lifetime.
- **User-facing steer / per-agent cancel UI** — `Orchestrator.Cancel` stays unwired
  (`orchestrator.go:806`).
- **Auto-injecting async results into the conversation** — would use the
  `PreTurnHook` seam (`internal/agent/agent.go:386`, autocompact-only); rejected to
  avoid touching session/compaction.
- **Async spawn in `parallel`/`chain`** — single mode only in v1.
- **Worktree GC for detached agents**, **live TUI repaint for a detached agent**,
  and the pre-existing **sidebar `"done"` status defect**
  (`internal/tui/sidebar.go:590-603`).
- Pre-existing `pi-acp-mock` `LoadSession` nil-deref
  (`cmd/pi-acp-mock/main.go:137`) — only reachable if a later spec adds ACP session
  loading.
