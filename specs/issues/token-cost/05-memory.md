# Topic 5 — Memory is built but unreachable

## Research

### The interactive TUI injects no memory context

`memoryInstructionContext` has exactly one caller:

- `internal/cli/cli.go:632` — `instruction += memoryInstructionContext(...)`, on
  the **non-interactive** (`--print` / json) path only.
- Palace equivalent: `internal/cli/cli.go:593` — `## Palace Memory Context`,
  same path only.

`internal/cli/interactive.go:316-328` builds its instruction from
`agent.LoadInstructionParts(...)` alone. `initMemoryAfterUI`
(`interactive.go:511,683`) starts memory purely for *recording* and for the
`mem-*` tools. The primary usage mode gets nothing.

### Injection is once per process

The generated markdown is baked into the static system instruction at startup
and never regenerated (`cli.go:588-632`, consumed by `agent.New`). Observations
recorded during a session never re-enter its own context, and the block cannot
be relevance-filtered against the user's prompt because no prompt exists yet.

### Retrieval is pure recency

`internal/memory/context.go:34-51`: `RecentSummaries(project, 3)` +
`RecentObservations(project, 200)`, filtered to a hardcoded 72 hours. No FTS, no
semantic search, no query. `Store.Search` (FTS5, `search.go:19-33`) and the only
genuine hybrid keyword+semantic path (`palace/drawer_service.go:122-165`) are
reachable **only** if the model chooses to call `mem-search` or the palace
search tool.

`config.MemoryDefaults().LookbackHours` is parsed and copied but never read —
`interactive.go:740` carries `//nolint:govet // reserved for future use`.

### `session_summaries` is never written

`Store.CompleteSession` (`store.go:54`), `Store.UpsertSummary` (`store.go:152`)
and `SubagentCompressor.SummarizeSession` (`compress.go:161`) have **no
production callers** — only the `lazyMemoryStore` pass-throughs at
`interactive.go:559,591`. No session-end hook exists.

Consequence: the `summaries` half of `ContextGenerator.Generate()` is always
empty, and session titles always fall back to raw session IDs
(`context.go:120-123`).

## Recommendations

1. **Call `memoryInstructionContext` from `interactive.go`.** The interactive
   path should append the same memory context block the non-interactive path
   gets. This is the single change that makes memory reachable in the primary
   usage mode. Wire it into the instruction built at `interactive.go:316-328`.
2. **Call the session summarizer at exit.** Add a session-end hook that calls
   `Store.CompleteSession` / `SubagentCompressor.SummarizeSession`, so
   `session_summaries` is actually populated. This makes the `summaries` half of
   `ContextGenerator.Generate()` non-empty and gives sessions real titles.
3. **Regenerate the memory block per turn, not once per process.** The block is
   baked at startup and never refreshed, so observations recorded mid-session
   never re-enter context. Regenerate it (or at least refresh it) on a cadence,
   so the model sees its own recent work.
4. **Make retrieval query-aware.** Replace the pure-recency 72-hour filter with
   a relevance-filtered retrieval once a user prompt exists. The FTS5
   `Store.Search` and the palace hybrid path already exist; surface them through
   the injected context rather than only as opt-in tools.
5. **Honour `LookbackHours`.** It is parsed and copied but never read
   (`interactive.go:740`). Wire it into the recency cutoff in
   `context.go:46` so the 72-hour hardcode becomes configurable.

## Expected impact

Recall quality in the interactive TUI, which is currently the primary usage mode
with zero memory context. Populated `session_summaries` also improves session
titles and cross-session context.

## Risk

Low. The changes are additive (inject existing context, call existing
summarizers). The main consideration is token cost of the injected block — the
`ContextGenerator` already enforces a `tokenBudget` (default 8000), so the
injection is bounded.
