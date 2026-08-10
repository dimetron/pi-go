# Design — concurrency budget that composes under nesting

Derived from `recomendations.md`. Covers Findings 1 and 3; Finding 2 is
specified in `deferred.md`.

## Current state

`Orchestrator` holds a `Pool` (`internal/subagent/pool.go`), a buffered-channel
semaphore sized by `DefaultPoolSize = 5` at
`internal/subagent/orchestrator.go:110`. `Acquire` is called once per spawn
(`orchestrator.go:388`) and released on every exit path.

The size is a compile-time constant. `PI_SUBAGENT_CONCURRENCY` appears only in
a comment listing the `PI_` prefix as forwarded to subagents
(`internal/subagent/environ.go:43`); no code reads it.

Because a spawned agent is a `pi` process that builds its own orchestrator, the
budget is per-process. Nesting multiplies it: `depth × 5`.

`buildEnv` (`internal/subagent/environ.go:73`) starts from `os.Environ()` and
forwards anything matching the allowlist, so a `PI_`-prefixed variable set by a
parent already reaches its children unchanged. That is the mechanism to reuse —
and the reason the budget currently *would* be inherited verbatim rather than
divided, if it were read at all.

## Desired end state

One knob, honored everywhere, that shrinks as runs nest so total concurrent
agents stays bounded regardless of depth.

## Components

### `subagent.ConcurrencyFromEnv() int`

Reads `PI_SUBAGENT_CONCURRENCY`. Returns `DefaultPoolSize` when unset, empty,
unparseable, or below 1. Never returns less than 1 — a zero budget would
deadlock every spawn.

Clamped to a sane ceiling so a typo cannot uncap the pool.

### Child budget propagation

When the spawner builds a child's environment it sets
`PI_SUBAGENT_CONCURRENCY` to the child's share of the parent's budget rather
than letting the parent's value pass through unchanged.

The rule is integer halving with a floor of 1:

```
child = max(1, parent / 2)
```

Depth 0 gets N, depth 1 gets N/2, depth 2 gets N/4, bottoming out at 1. Total
in-flight agents converge instead of growing with depth.

Halving is chosen over "parent − 1" because it degrades gracefully for any
starting N and reaches the floor quickly; a coordinator two levels down doing
one thing at a time is the desired behavior.

### Default

`DefaultPoolSize` drops from 5 to 3. The evidence is that 5 per process, times
the nesting the SOP now encourages, is not survivable against a per-minute
token limit. Three is a starting point that the environment variable can raise
for accounts with more headroom.

### `session.truncateTitle`

Replaces the byte slice at `internal/session/store.go:647`. Cuts at a rune
boundary at or below `MaxSessionTitle` bytes, so the result never ends in a
partial rune.

## What is deliberately not built

**No adaptive controller.** Widening and narrowing the pool in response to 429s
is a feedback system with its own oscillation and starvation modes. The
observed failure is explained entirely by a static budget that composes wrongly,
so the static budget is what this fixes.

**No new retry.** `retryStream` already covers the recoverable case and
correctly refuses the unrecoverable one; extending it would duplicate tool
calls. See `recomendations.md`, Finding 1.

## Acceptance criteria

- Given `PI_SUBAGENT_CONCURRENCY=2`, when an orchestrator is constructed, then
  its pool size is 2.
- Given the variable is unset, malformed, or `0`, when an orchestrator is
  constructed, then its pool size is `DefaultPoolSize` and never below 1.
- Given a parent with budget N, when it spawns a child, then the child's
  environment carries `max(1, N/2)`, not N.
- Given a parent with budget 1, when it spawns a child, then the child receives
  1 — the floor holds and no spawn can deadlock.
- Given a title longer than `MaxSessionTitle` whose cut point falls inside a
  multi-byte rune, when it is persisted, then the stored title contains no
  U+FFFD and is no longer than `MaxSessionTitle` bytes.

## Testing strategy

Table-driven unit tests for the env parse and the halving rule. A test that
builds a child environment through the real `ChildEnv` path and asserts the
decremented value — and asserts the variable appears exactly once, since the
bug being prevented is the parent's value passing through alongside it. Title
tests assert on properties (valid UTF-8, no U+FFFD, within the byte cap) rather
than on a golden string, so they hold for any cut point.
