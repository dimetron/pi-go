---
name: verify-agent-lifecycle
description: Formally verify the subagent lifecycle before or after changing it. Use when editing internal/subagent/orchestrator.go's Spawn, trackAgent, forwardAgentEvents, terminalStatus, Cancel, ShutdownWithTimeout or evictCompletedAgentsLocked, or internal/subagent/pool.go — or when a canceled agent is reported as failed, an agent vanishes from the list with no verdict, the orchestrator panics on a pool release, or an agent can be canceled twice.
---

# Verify the subagent lifecycle

One subagent has six states, three writers, and a pool slot that must be released
exactly once. `pool.Release` is an unguarded channel receive
(`internal/subagent/pool.go:36-38`), so a second release panics the process, and
nothing in a single-agent test can produce the interleaving that does it.

The properties: **a cancel is never overwritten by the process's own exit
reason**, **an agent that leaves the list has already published its verdict**, and
**exactly one pool slot is released per spawn**.

## Does my change touch the verified code

```bash
git diff --stat <sync-commit>..HEAD -- \
  internal/subagent/orchestrator.go internal/subagent/pool.go internal/subagent/types.go
```

**Model covers** (each model step names the function it stands for):

| Model action | Code |
|---|---|
| `SlotAcquired` | `Spawn` past `pool.Acquire`, `orchestrator.go:487-499` |
| `SpawnRefused` | `trackAgent` refuses after shutdown → `abandonSpawn`, `orchestrator.go:499-505, 599-607` |
| `Event` / `ProcExits` | the event loop and `proc.Wait`, `orchestrator.go:610-629` |
| `Cancel` | `Cancel` + `validateAgentForCancel`, `orchestrator.go:806-834` |
| `ShutdownRunning` / `ShutdownIdle` | `ShutdownWithTimeout`, `orchestrator.go:859-894` |
| `PublishKept` / `PublishEvicted` | `forwardAgentEvents`' tail: evict, publish, release, `orchestrator.go:628-648, 683-711` |

**Not modelled, and why:** the worktree manager (its own stash lifecycle, modelled
separately if it becomes a target); `spawner.go`'s event channel and its silent
drops when the 64-slot buffer fills; `terminalStatus`'s signal classification
(the model takes the outcome as a parameter); `recentTasks` and the prune loop;
concurrency above one agent; `Get`/`List` as anything other than a read. A bug in
any of those is invisible here.

## Run it

```bash
hack/formal/check.sh agent           # all rules hold, or the first one that breaks
hack/formal/check.sh agent --chart
```

Cost: 35 distinct states, under a second. Exit `0` all rules hold, `1` a rule
broke, `2` TLC or the environment failed.

## Rules, and the harm each one names

| Rule | The promise | What breaking it means for a user |
|---|---|---|
| `CancelIsSticky` | a cancel request wins over the process exit reason | you press cancel, and pi reports the agent failed instead |
| `PublishedIsTerminal` | `run_done` never says `running` | a consumer waits forever for a verdict already sent |
| `EvictionStillPublishes` | an agent missing from the list has published | an agent disappears with no status: neither running nor done |
| `SlotReleasedOnce` | at most one `pool.Release` per spawn, and only if acquired | `pool.Release` on an empty semaphore panics the process |
| `NoLateCancel` | a finished agent is never relabeled `canceled` | a completed result is reported as canceled |

## Two traps, both found while building this model

**A rule phrased on the state the bug overwrites cannot see the bug.** The first
`CancelIsSticky` read `status = "canceled" => ...`, which is trivially true once a
later step overwrites `status` with `"failed"`. The mutation that drops the
`if status == "running"` guard at `orchestrator.go:626` passed cleanly, on 361
explored states. The fix is the `cancelAsked` variable: track the *request*, and
state the rule about what a consumer reads after it. Re-run the mutation after any
edit to this rule.

**A mutant that does not compile is not a caught regression.** Two attempted
mutations "failed" because they produced a `null` variable — a TLA+ error, not an
invariant violation. Always read *which* line fired: it must be
`Error: Invariant <name> is violated`. A `TLC threw an unexpected exception` or
`Attempted to apply the operator` is a broken mutant, and proves nothing.

Also: `hack/formal/lint.py` runs first and catches unassigned variables and
conjoined assignments, the two defects that produced false passes in the `/goal`
and `/run` models.

## After confirming the model is still true

- **Sync commit:** `cc79aceb43df31012e9e5c1f69a7ba1231c32470`
- **Constants:** none. `MaxCompletedAgents` was modelled as the eviction decision
  it produces rather than a counter, because a counter made the state space
  explode past 2 million.
- **Chart budget:** 35 states, so the full chart always draws.

Move the sync commit forward only after confirming the model describes the code at
the new commit.

## Draw the chart

`hack/formal/check.sh agent --chart` writes `hack/formal/agent/out/report.md` and
`chart.mmd` via the pstack `/statechart` skill.
