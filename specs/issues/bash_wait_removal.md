# Issue: Can `bash_wait` be removed and folded into `bash`?

**Status:** evaluated — removal rejected; three of the changes below are landed.
**Raised by:** polling storms in the TUI (see Evidence).
**Related branch:** `fix/bash-wait-card-reuse`.

### Decisions taken

| Decision | Where |
|---|---|
| **Keep the tool.** Removal rejected — see Options | this doc |
| **Renamed `bash_output` → `bash_wait`.** The old name described the payload and read as a cheap getter, which is what invited the poll loop; the new one names the act and matches the shell's `wait`. `bash_watch` was considered and dropped: `watch(1)` *re-executes* its argument, the one connotation this tool cannot carry | `bash.go:186`; display accepts the old name for restored sessions (`isBashPoll`, `tool_display.go`) |
| **Foreground budget 120s → 30s.** It is a budget for the *turn*, not the command; nothing is killed at the threshold | `defaultBashTimeout`, `bash.go:28` |
| **The watched command is now inside the card**, not only in its truncating header | `formatBashWindow`, `tool_display.go` |
| Repeat polls fold into one card with a `×N` tally | `findPollCard`, `agent_loop.go` |

Still open: the `background` flag (Recommended change), and D1–D4.

## Summary

The proposal was to collapse `bash_wait` into `bash`, on the reading that the
only difference between them is synchronous vs asynchronous execution — so a
`background: true` flag on `bash` should cover it.

That reading does not survive contact with the code. `bash` is not the
synchronous tool: it is synchronous *until it isn't*, because a command that
crosses either timeout is handed to the supervisor mid-call
(`internal/tools/bash_supervisor.go:307,315`). The axis separating the two tools
is not sync/async but **who owns the process**:

| Tool | Verb | Args | State it owns |
|---|---|---|---|
| `bash` | **start** a process | `command`, `timeout`, `idle_timeout` | none afterwards — it hands off |
| `bash_wait` | **attach** to a running one | `handle`, `wait_ms` | the handle's incremental read cursor, blocking wait |
| `bash_kill` | **stop** one | `handle` | process-group termination, plus a brief wait for reaping |

A flag changes how a command *starts*. Collecting its output happens in a later
turn by definition, so it is a second tool call no matter what it is named. The
flag and the read tool are orthogonal halves, not two spellings of one thing.

**But the question surfaced a real defect**: `BashInput` has no `background`
flag (`internal/tools/bash.go:20-30`), so the only supervisor-managed detach the
API exposes is a timeout or idle-timeout handoff. (A command can of course
background itself with `&` or `nohup`, but that process gets no handle of its
own — no cursor, no cap, and nothing for `bash_wait` or `bash_kill` to attach
to.)

That is the cause of the *premature handoffs* — commands detached at 1s that
should have run to completion in the foreground. It is not, on its own, the
cause of the *polling rate*: how often a detached command gets polled is a
separate policy question, and it is made worse by a rendering defect found while
reviewing this spec (D3 below) that leaves a backgrounded command with no
visible progress, so polling becomes the only way to see anything.

## Evidence

From a live session — session evidence, not reproducible from the repo alone:

```
◉ bash_output(bg_4: cd wiki && make daily-all)
  │ ⏳ 42.1s elapsed, 22.7s without output
  │ (no new output)
  │ idle_timeout 1s, timeout 3.6s
```

`idle_timeout 1s, timeout 3.6s` are not limits anyone chose for a wiki build.
They are a hand-rolled `background: true` — the model set absurd limits to force
an immediate handoff, then polled the handle ~20 times.

The supervisor already anticipates this shape and warns about it
(`limitsHint`, `internal/tools/bash_supervisor.go:417`):

> An idle_timeout under 15s backgrounds ordinary builds and test runs the
> moment they go quiet — pass a larger idle_timeout (or omit it for the 1m30s
> default) rather than polling the handle.

(`shortIdleTimeout = 15s`, `defaultIdleTimeout = 90s`,
`bash_supervisor.go:32,41`.)

The hint names the workaround as the problem, but the tool offers nothing to use
instead. That is the gap.

## What `bash_wait` owns that a flag cannot

Removing the tool means re-homing all of this, and every item lands on the model
if it lands anywhere:

1. **The handle's read cursor.** `p.stdout.since(p.outCur)`
   (`bash_supervisor.go:593`) returns only what was produced since the last
   read. Without it, every read re-sends the entire buffer into context. Note
   the cursor pair lives on the process, not on a caller
   (`outCur`/`errCur`, `bash_supervisor.go:153-157`): it supports one logical
   polling reader per handle, and concurrent readers would race through the
   same position under `curMu`.
2. **Blocking wait.** `awaitChange` parks on `p.stdout.wait()`
   (`bash_supervisor.go:634,639`) for up to 60s (`maxBashWait`, `bash.go:114`).
   This is what makes the operation a *wait* rather than a *poll*, and it is the
   single most valuable thing the tool does — the property the rename now says
   out loud.
3. **Exit status and reaping.** `exit_code` is meaningful only once
   `running=false`; the handle is spent and forgotten after the final read
   (`forget`, `bash_supervisor.go:541`).
4. **Output budgeting and secret redaction.** `budgetStreams`
   (`bash_supervisor.go:434`) truncates and runs `redactSecrets` on the tool
   path — not on anything the command writes elsewhere.
5. **Handle lifecycle.** A cap on concurrent background commands
   (`maxBackgroundProcs = 8`, `bash_supervisor.go:51`) and a 30-minute lifetime
   ceiling (`backgroundMaxLifetime`, `bash_supervisor.go:56`). The cap does not
   reject a new command — `register` evicts and terminates a victim to make
   room (`bash_supervisor.go:458-470`), preferring an already-finished entry but
   falling back to the oldest live one (`oldestLocked`, `:502-521`). Backgrounding
   a ninth command can therefore kill the first.
6. **Consumers keyed on the tool name.** The stuck detector strips
   `elapsed`/`idle` before fingerprinting `bash_wait` results
   (`internal/tui/agent_loop.go:214-225`); the TUI keys its call-time header
   builder (`agent_loop.go:1236`), its result-side header recovery
   (`agent_loop.go:1098`, helper at `:1116`) and its poll-card fold
   (`agent_loop.go:923`, `findPollCard` at `:1150`) off the name. (`formatBashWindow`,
   `internal/tui/tool_display.go:787`, is *not* one of these — it is selected by
   the presence of a `handle` field in the result, so it would survive a rename.)

Note what is **not** on this list: live output. The supervisor fans output to an
`OutputSink` while the command runs (`bash_supervisor.go:59-67`, `sinkWriter` at
`:692-724`), which is how the TUI streams a running command without polling.
That path is deliberately best-effort and is not a substitute for the model's
read: the sink drops events rather than block a pipe (`:59-67`), and
`sinkWriter` emits only completed non-blank lines, holding a trailing partial
until it sees a newline or the chunk passes 8192 bytes (`:711-724`). Those bytes
stay in the retained stream and still come back through `bash_wait`.
`bash_wait` is the *model's* reliable read path; the sink is the *display's*
lossy one.

## Options evaluated

### A. Union tool — `bash(command | handle, ...)`

One tool, mutually exclusive argument groups. **Rejected.**

The wrong branch is destructive, not merely wrong: a poll that arrives carrying
`command` instead of `handle` does not return stale output — it **runs the
command a second time**. `make deploy` twice, `terraform apply` twice. The
current split makes *that* failure unreachable: `bash_wait` has no code path
that can start anything, so a mis-shaped poll errors instead of executing.
(Nothing stops a model from deliberately calling `bash` again — the guarantee is
about the poll, not about the model's judgment.)

It also saves less than it looks: `bash_kill` still needs a handle-shaped tool,
so three tools become two, at the cost of the one failure mode that must never
happen.

### A2. Single tool with an explicit operation discriminator

The objection to A is specific to an *implicit* union — the branch is inferred
from which field happens to be set. A tagged schema removes that inference:

```json
{"op": "start", "command": "make all", "background": true}
{"op": "read",  "handle": "bg_1", "wait_ms": 60000}
{"op": "kill",  "handle": "bg_1"}
```

Dispatch validates before touching a process: `start` requires `command` and
forbids `handle`, `read`/`kill` require `handle` and forbid `command`, anything
ambiguous is rejected. Internally it still calls `Run`, `readOutput`,
`killHandle`, so every capability above survives.

**Viable, but not adopted.** It is honestly the strongest one-tool design and
the spec should not pretend otherwise. What it trades:

- The destructive branch moves from *unreachable* to *guarded*. Today
  `bash_wait` has no code path that can start a process; under A2 it does,
  behind a validation check. That is a real downgrade for the one failure mode
  that must never occur, in exchange for a schema saving.
- It saves no calls. Reading still happens in a later turn; only the name on the
  wire changes.
- Three narrow schemas become one wide one with conditionally-required fields —
  the shape models describe worst and get wrong most.
- Every name-keyed consumer in §"What `bash_wait` owns" item 6 has to switch
  to dispatching on an argument instead, including the stuck detector.

Worth revisiting if tool-schema context cost ever becomes the binding
constraint; it is not today.

### B. Log-file redirect + existing read tools

`bash("make all > /tmp/run.log 2>&1", background: true)`, then `read` / `grep`
the log. **Rejected.**

Genuinely fewer tools, all pre-existing — but it loses items 1-4 above. No
blocking wait, so the model busy-polls a file (worse than today); no convenient
exit status; no redaction or truncation on the output path; and the model must
track byte offsets itself, which it will not, so every read re-sends the whole
log. It also adds temp-file lifecycle to something the supervisor currently
handles.

Two things this variant does *not* necessarily lose, and the spec should not
claim it does: if the command is still started through the supervisor, the
handle and therefore `bash_kill` remain (`bash.go:184-196`,
`bash_supervisor.go:645-689`); and the supervisor still reaps the process — what
the model loses is the status *read channel*, not the reaping.

### C. Add `background` to `bash`, keep all three tools — **recommended**

Removes zero tools. Removes the *fake timeouts*, and with them the accidental
backgrounding of every build that pauses for a moment.

### D. Do nothing

Leaves the model forging detachment out of 1-second idle timeouts, and leaves
`limitsHint` warning about a workaround the API forces. **Rejected.**

### E. Completion injection — the host pushes, the model never polls

Attack the *reason* for polling instead of the tool. When a backgrounded command
exits, the host appends its final output and exit status to the model's history
as an event, unprompted. The model stops asking "is it done yet" because it is
told.

**Viable, and the most interesting of the alternatives — but much larger.**
Nothing today can carry it: `OutputSink` is explicitly lossy and must not block
(`bash_supervisor.go:59-67`), and the TUI's consumer only mutates card state
(`internal/tui/agent_loop.go:1250-1278`). It needs a durable session-level event
channel plus rules for appending a message after the function response that
already closed the call — which is a session/protocol change, not a tool change.

It also does not remove `bash_wait`: a model that wants output *before* the
command exits still has to ask. It removes the *end-of-life* poll, which is the
most common one. Worth its own spec if polling volume remains a problem after
the `background` flag lands.

### F. Remove background handoff entirely

Delete the third state: on timeout, kill the command and return what it printed,
as the foreground cancellation path already does
(`bash_supervisor.go:297-304`). No handles, so no `bash_wait` and no
`bash_kill` — the whole family goes.

**Rejected, but honestly the cleanest design on paper.** It trades away every
long-running workload: no dev server, no watcher, no 20-minute build, no
`make daily-all`. The supervisor exists precisely because killing those at the
two-minute mark was worse than handing them off. Recording it here so the
trade-off is explicit rather than assumed.

## Defects found while reviewing this spec

Checking the spec's claims against the code turned up four real problems that
are not about the spec at all. They are listed here because three of them
directly shape how much a `background` flag helps, and they should be fixed
before intentional detachment makes backgrounded commands more common.

### D1. A poll can block 60s with unread output already buffered

`readOutput` calls `awaitChange` **before** reading the cursor
(`bash_supervisor.go:582-596`). `awaitChange` selects on the stream's notify
channel (`:634-643`), and `stream.Write` wakes waiters by closing the *current*
notify channel and installing a fresh one (`bash_stream.go:42-59`). So output
written between two polls has already closed the channel the previous poll held;
the new poll gets the fresh, unclosed one and waits for the *next* write.

A command that prints a burst and then goes quiet therefore makes the next poll
block the full 60s while its output sits in the buffer, unread. The fix is
ordering: read `since(cursor)` first, and only wait when it comes back empty.

### D2. Waits ignore turn cancellation

The `bash_wait` handler discards its context — `func(_ agent.Context, input
BashOutputInput)` (`bash.go:174-179`) — and `readOutput` takes no context at
all. An aborted turn still has up to 60 seconds of unstoppable wait in it.

### D3. A backgrounded command stops showing progress

The TUI renders the live event window only while the card has no result:
`if msg.content == "" && len(msg.agentEvents) > 0`
(`internal/tui/tool_display.go:491-496`). The handoff *is* a result, so the
moment a command goes to the background its card fills with the handoff summary
and the live window disappears — while `handleBashEvent` keeps faithfully
appending events to it (`agent_loop.go:1266-1275`) that nothing draws.

This is the rendering half of the polling problem: after handoff there is no
visible progress, so polling is the only way to see whether anything is
happening. Fix it and the pressure to poll drops on its own.

### D4. `background` publishes the handle before initializing cursors

`background` calls `register` (making the handle lookupable) and only then
computes and stores `outCur`/`errCur` (`bash_supervisor.go:370-378`). A read
landing in that window would start from 0 and re-deliver everything, and the
late assignment would rewind the cursor under it.

Latent rather than live: the model cannot know the handle until the call
returns, so nothing can currently race it. Worth reordering anyway — an
in-process consumer of the supervisor API has no such protection.

## Recommended change

1. **`BashInput.Background bool`** (`internal/tools/bash.go:20`):

   ```go
   // Background starts the command detached: bash returns a handle
   // immediately instead of waiting for output or for a timeout to hand it
   // off. Use for servers, watchers, and long builds — not as a way to avoid
   // waiting for a command that will finish in seconds.
   Background bool `json:"background,omitempty"`
   ```

2. **Handler path** — `background: true` should go through a supervisor entry
   point of its own (`StartBackground`, say) rather than having the tool handler
   reach for `sup.background(p, reason)` (`bash_supervisor.go:370`), which today
   is an internal step of `supervise` and assumes a foreground attempt already
   ran. The detached path arms no idle timer. The 30-minute lifetime ceiling and
   the 8-process cap still apply; detaching is not a licence to leak processes.

3. **Add a third handoff reason.** `background` already takes one and reports
   it: `"still running after 2m"` for the hard timeout (`:306-307`) and
   `"no output for 1s"` for the idle one (`:313-315`). Neither can express
   intent, so a detached start needs its own — `"started detached"` — and the
   card should render that differently from "backgrounded after going quiet".

4. **`bashDescription`** (`bash.go:102`) should point at the flag: *start
   long-running work with `background: true`; do not shrink the timeouts to
   force a handoff.* Keep `limitsHint` as the backstop for callers that do it
   anyway.

5. **TUI** — poll cards fold in place instead of appending one block per poll
   (done on `fix/bash-output-card-reuse`); the header names the command from
   the poll's own `BashStatus.Command` so it survives compaction and `/resume`.

### Acceptance criteria

- [ ] `bash(command, background: true)` returns `running=true` + a handle
      without waiting for either timeout to expire.
- [ ] The result distinguishes an intentional detach from a timeout handoff, in
      the `note` and on the TUI card.
- [ ] A detached command still counts against `maxBackgroundProcs` and is still
      reaped at `backgroundMaxLifetime`. Decide explicitly whether an
      intentional detach may evict and kill an older live command the way
      `register` does today (`bash_supervisor.go:458-470`) — silently killing a
      running build to make room for a detached one is a worse surprise when the
      caller asked for the detach than when a timeout forced it.
- [ ] `bash_wait` / `bash_kill` are unchanged and remain the only *model-facing*
      way to read from or stop a handle; the `OutputSink` live stream to the TUI
      is unaffected.
- [ ] Tests: detach is immediate (no dependence on wall-clock timeouts), caps
      still enforced, note text distinguishable, existing timeout-handoff
      behaviour unaffected.

## Non-goals

- Merging `bash_wait` into `bash` in any form (Options A, A2 and B above).
- Removing `bash_kill`.
- Merging the poll card into the originating `bash` card in the TUI. Feasible —
  binding by handle already exists — but the merged card sits where the command
  *started*, which after fifty polls is far up the scrollback, so live status
  would update off-screen. Tracked separately; the likely answer is one card per
  command in the transcript plus a live line for each running command in the
  status bar. That last part is not free: `status.go:200-212` renders active
  tools by *name* only (`tool: bash (1.2s)`) and knows nothing about handles or
  commands, so it is an insertion point, not existing plumbing.

## Open questions

- Should `background: true` suppress `idle_timeout` entirely, or keep a very
  long one as a leak guard? Proposal: suppress it; `backgroundMaxLifetime`
  already bounds the process.
- Should a detached command still stream live events to its card
  (`bash:output` via `AgentEventCh`)? It should — but note that event *delivery*
  already works while *rendering* does not (D3): the events arrive at the card
  and are dropped on the floor by the renderer. This is a renderer change, not
  new plumbing.
- Does the `background` flag warrant a different default `wait_ms` on the first
  poll of an intentionally detached command? The flag says "I do not expect this
  soon", which is an argument for a longer wait, not the 60s cap — but see D1
  first, since today a long wait can be spent ignoring output already in hand.
