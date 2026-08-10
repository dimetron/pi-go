# Coordinator → Worker runs: what the session data shows, and what to fix

Date: 2026-08-10
Evidence: 63 sessions under `$HOME/.pi-go/sessions` with `workDir` inside
`/Users/dimetron/p6s/ai-eng-course`, created in the six hours to 12:30.
Runtime reviewed at `main` = `5eee2ba`.

## Summary

The Coordinator → Worker → Verifier SOP works. Delegation happens, context
stays small, and the retry loop fires. What stops runs finishing is no longer
context exhaustion — it is **provider rate limiting, amplified by a
concurrency cap that multiplies instead of dividing when runs nest**.

A secondary finding: the run tree in the session log is only recoverable by
inference. Nothing records which coordinator a worker belongs to.

## What is working

| Claim | Evidence |
|---|---|
| Coordinators delegate rather than implement | Worker sessions carry self-contained slice briefs ("Implement Slice 2 of …, inspect …, verify …") |
| Context pressure is largely solved | 2 of 63 sessions above 150k prompt tokens, against 27 of 92 above 100k before the SOP change |
| The retry loop fires | 9 resume cycles, prompts beginning "The previous execution cycle did not finish" |

This is worth stating plainly because the fixes below should not disturb it.

## Finding 1 — rate limiting is the blocker (critical)

**25 of 64 sessions (39%) ended on `rate_limit_exceeded`** — tokens-per-minute
for `gpt-5.6-luna`. Peak concurrency was **8 simultaneous sessions**.

The cost shows up as churn, not as clean failure:

| Slice | Worker sessions spent on it |
|---|---|
| Slice 4 | 10 |
| Slice 2 | 10 |
| Slice 1 | 9 |
| Slice 3 | 4 |

33 worker sessions for roughly 4 slices.

### Why it compounds

A worker dies mid-slice on a 429. The coordinator immediately dispatches a
replacement. That raises concurrent token draw, which makes the next 429 more
likely. Nothing in the loop responds to being rate limited by slowing down.

### Why the existing retry cannot help

`retryStream` (`internal/provider/retry.go:46`) already re-sends a stream that
failed **before emitting anything**, with `MaxRetries = 3`. The comment on it
is explicit about the limit: once tool calls or text have been forwarded, the
attempt is committed and the error passes through, because re-sending would
duplicate the tool calls.

Every observed failure is mid-slice, after tool calls. So this is correct and
cannot be extended to cover the case. **The answer is not more retrying — it is
not hitting the limit.**

The `retry` package is already careful about 429s that are not rate limits
(`internal/retry/retry.go:50-57` distinguishes "weekly usage limit" and
"exceeded your current quota" from a real per-minute limit). That
classification is good and should be reused, not duplicated.

### Root cause: the cap is per-process

`DefaultPoolSize = 5` (`internal/subagent/orchestrator.go:18`) bounds concurrent
subagents **per orchestrator**. Each orchestrator lives in one `pi` process.

A spawned coordinator is itself a `pi` process, and it gets the subagent tool
(`internal/tools/agent.go:23`) — so it constructs its own orchestrator with its
own pool of 5. Two concurrent `/run` commands therefore permit 2 coordinators ×
5 workers = 10 concurrent agents, plus the coordinators themselves. The
observed peak of 8 sits inside that envelope; the configured 5 never bounded
anything at the level that matters.

`maxParallelTasks = 8` (`internal/tools/subagent.go:25`) caps a single
`subagent` call's fan-out, not the total in flight.

**`PI_SUBAGENT_CONCURRENCY` is named in the env allowlist comment
(`internal/subagent/environ.go:43`) but is never read anywhere in the tree.**
The pool size cannot currently be configured at all.

### Recommendation

1. Honour `PI_SUBAGENT_CONCURRENCY` as the pool size, falling back to
   `DefaultPoolSize`.
2. **Propagate a decremented budget to children.** A coordinator given a budget
   of N should pass a smaller budget down, so nesting divides the allowance
   rather than multiplying it. `PI_*` is already forwarded to subagents, so the
   channel exists.
3. Lower the default. Five concurrent agents against a per-minute token limit
   is optimistic for a single account; the data shows it is not survivable.

This is deliberately not adaptive. A rate-limit-aware controller that widens
and narrows the pool is a bigger change with its own failure modes; a correct
static budget that composes under nesting fixes the observed problem and can be
tuned from the environment.

## Finding 2 — the run tree is inference, not tracking

`meta.json` carries exactly ten fields: `appName`, `createdAt`, `host`, `id`,
`model`, `provider`, `title`, `updatedAt`, `userID`, `workDir`.

Reconstructing which workers belonged to which coordinator required grouping
sessions by `workDir` and guessing roles from title prefixes. Nothing records:

- `agentID` — no join to the orchestrator's own agent records
- `parentSessionID` — the worker → coordinator edge does not exist
- `agentType` — coordinator vs worker vs verifier is guessed from prose
- `specName` and slice number — extracted with a regex over the title
- cycle / retry index — the 9 resumes are findable only by matching prompt text
- terminal status — absent; determining it means scanning `events.jsonl` for
  `ErrorCode`

Worse: **14 of 63 sessions live in numerically-named worktrees** such as
`.pi-go/tasks/948887880000`, and those directories contain no coordinator
session at all — they are resume cycles. They cannot be attributed to a spec by
any recorded field.

### Recommendation

Add a nested, optional `Agent` block to `session.Meta`, mirroring the existing
`PlanContext` precedent (`internal/session/store.go:28`): agent ID, agent type,
parent session ID, spec name, worktree path and branch, cycle index, and
terminal status. Populate it from the environment the spawner already
forwards. This makes every question above a field lookup instead of a
heuristic.

## Finding 3 — session titles are corrupted by byte truncation

**50 of 200 recent titles contain U+FFFD.** `internal/session/store.go:647`
truncates with `title[:MaxSessionTitle]`, a byte slice, which cuts mid-rune:

```
'…Report concrete bugs with seve�'
```

This is the same defect class PR #125 fixed in `truncateOutput`, which now
backs up to a rune boundary. The fix never reached the title path. Titles are
also embedded in terminal escape sequences (OSC 0), so a broken rune is written
to the user's terminal.

### Recommendation

Truncate on a rune boundary. One function, reusing the approach already proven
in `internal/tools/truncate.go`.

## Finding 4 — the SOP promises parallelism the runtime will not deliver

Found by reviewing the SOP against the runtime *after* the Finding 1 fix, which
is the only order in which it is visible.

`coordinatorContract` (`internal/tui/run.go`) told the coordinator it may batch
"several slices at once … (max 8)". The SOP repeated the advice for both slice
execution and Phase 3 research. Both numbers came from `maxParallelTasks = 8`
(`internal/tools/subagent.go:25`), and the subagent tool description advertised
the same figure.

But `maxParallelTasks` caps **how many tasks one call may name**. What actually
gates a spawn is the pool. With the budget from Finding 1 a coordinator sits at
depth 1 and gets a pool of 1, so an eight-task batch does not overlap at all:
the tasks queue inside a single tool call and that call takes roughly eight
times as long. A batch that was supposed to save time becomes the most likely
thing to hit the agent timeout.

This is worth stating as its own finding because it is a hazard *created by*
fixing Finding 1. Lowering concurrency without correcting the guidance would
have traded a rate-limit failure for a timeout failure.

### Recommendation

Report the effective concurrency, not the per-call cap, and make the advice
follow it:

- `Orchestrator.Concurrency()` exposes the pool size.
- The subagent tool description states both numbers and what happens past the
  smaller one; at a concurrency of 1 it says plainly that parallel mode buys
  nothing.
- The coordinator contract and the SOP tell the agent to size batches to the
  reported concurrency, and that one slice per call is the normal case.

## Priority

1. **Finding 1** — this is what stops runs completing.
2. **Finding 4** — must ship with Finding 1, not after it. On its own, Finding
   1 converts a rate-limit failure into a timeout failure.
3. **Finding 3** — small, self-contained, and already corrupting stored data.
4. **Finding 2** — additive and valuable, but nothing is blocked on it today.

## What this does not address

Gates still run only in the primary worktree in parallel mode, so a second
agent's code is never gated. That needs worktree-to-worktree consolidation
before validation, which `MergeBack` does not do — it merges into the main
repo. It is unrelated to the findings here and belongs in its own change.
