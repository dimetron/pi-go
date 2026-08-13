# Rough Idea — Repair the memory subsystem

## Source

A comparison of `tmp/memory/mempalace` (upstream MemPalace, Python, ~45k LOC)
against pi-go's port (`internal/palace` + `internal/memory`, ~14k LOC including
tests), performed 2026-08-13.

The port turned out to be broadly faithful in *feature coverage* and completely
dead in *production behaviour*. The port is not the problem. The wiring is.

## The observation that started it

```
~/.pi-go/memory/claude-mem.db   6228 sessions,  0 observations,  0 summaries
~/.pi-go/palace.db              3 drawers (all NULL embeddings), last write Aug 2
```

Sessions have been recorded for months. Not one observation has ever been
stored. Every downstream feature — recent-context injection, `mem-search`,
`mem-timeline`, session summaries, and the palace observation bridge — reads
from tables that are empty and always have been.

## Why it is empty

ADK ends the after-tool callback chain at the first callback that returns a
non-nil result. pi-go registers five to six after-tool callbacks and every one
of them returns the tool's result map. Only the first ever runs.

The memory recorder is registered last, so it never runs. Neither do the three
callbacks in front of it — the LSP hooks, the output compactor, and the result
deduper. One wiring bug silently disables four subsystems.

## What this spec covers

Five fixes, in dependency order:

1. Compose pi-go's after-tool callbacks so all of them run.
2. Stop compression from blocking the pipeline, and drain before closing the DB.
3. Unify the palace database and embedding-model paths.
4. Wire the palace into the interactive TUI path and scope wake-up per project.
5. Make retrieval measurable and safe — ANN index, query sanitisation, benchmark.

Fix 1 is the load-bearing one. Fixes 2–4 are the difference between "records
something" and "records everything reliably". Fix 5 is what stops the next
regression going unnoticed for four months.

## Non-goals

- Reaching feature parity with upstream MemPalace. The missing pieces (AAAK
  dialect, entity disambiguation, repair/export/onboarding, MCP surface,
  pluggable vector backends) are catalogued in `research/comparison.md` but are
  not scheduled here.
- Changing the palace data model. Wings/rooms/halls/drawers and the temporal
  knowledge graph stay exactly as they are.
- Replacing the observation → drawer bridge with upstream's hook-based
  auto-save. The in-process bridge is a better fit for pi-go; it just needs to
  be reachable.
