# Summary — RTK Compactor Phase 2

## Artifacts

| File              | Purpose                                                                                                                                                 |
|-------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|
| `rough-idea.md`   | One-paragraph concept; references Phase 1 spec                                                                                                          |
| `requirements.md` | 9 Q&A decisions on scope, language filter depth, tee threshold, detector count, TUI indicator, `never_worse`, read-pipeline behaviour, naming, test bar |
| `design.md`       | Full design: architecture diagram, components, data models, error handling, acceptance criteria, file plan, research findings                           |
| `plan.md`         | 13-step incremental implementation plan with test requirements per step                                                                                 |
| `PROMPT.md`       | Runnable spec for `/run`: objective, requirements, acceptance criteria, slices, gates, constraints                                                      |

## Overview

Phase 2 of `specs/research/000-rtk-hooks-optimizer/`. Closes the highest-value remaining gaps in the shipped RTK output
compactor:

1. **`never_worse` token-aware guard** — replaces byte-only checks; catches "filter added overhead" bugs
2. **Language-aware source filter** — port upstream `Language` enum + per-language comment patterns; data formats skip
   stripping
3. **Read pipeline respects args and size** — skip compaction for targeted reads (`offset`/`limit`) and short files
4. **Bash detector expansion (5 new)** — `ls`, `cat`, `find`, `env`, `docker` route to dedicated filters
5. **Tee overflow** — when hard-truncation drops >50% of bytes, write raw to
   `~/.pi-go/sessions/<id>/tee/<slug>-<ts>.log` and append a pointer
6. **TUI per-message compaction indicator** — render `[compacted 85% · ansi,test-agg]` suffix on tool-output messages;
   never sent to LLM
7. **Per-detector config toggles** — `compactor.detect_<name>` for each new detector, all default `true`

**No new packages, no new external dependencies, all backwards compatible.** All work extends
`internal/tools/compactor_*.go` and `internal/tui/`.

**Estimated scope:** ~1845 LOC across 16 files (7 new, 5 modified, 4 new test files). Recommended PR split: 4 PRs (
guard+language+read / bash detectors / tee / TUI+config+signoff).

## Out of Scope (Deferred)

Explicitly excluded from this spec; tracked for future specs:

- **Command rewriting before execution** (`BeforeToolCallback`) — would mean rewriting `cat foo.go` → `read foo.go`,
  `ls -la` → `tree`, etc. Substantial new surface (shell tokenizer, rewrite registry, suggestion UX). Spec 000 deferred
  this; we keep it deferred.
- **`learn` module** (repeated CLI-mistake detection) — genuinely new product, not parity work
- **JSON / log-dedup filters** — large standalone filter; not on the highest-value path
- **Subagent / hook to invoke external `rtk` binary** — we don't ship a CLI proxy
- **Per-message TUI indicator for chat text** (not just tool output) — UX question, not compaction
- **Remaining 5 bash detectors** (kubectl, curl, package-managers, ps/df/du, mypy/ruff/prettier) — would form a Phase
  2.5 spec

## Suggested Next Steps

1. **Review this spec** — sanity check scope, naming, and PR split
2. **Run** —
   `ralph run --config presets/spec-driven.yml specs/features/TOO/016-rtk-phase-2-command-rewriting/PROMPT.md` (or use
   the in-tree `/plan` + `/run` SOP)
3. **Phase 2.5 spec** (future) — the remaining 5 bash detectors + JSON filter + log-dedup
4. **Phase 3 spec** (future) — command rewriting BeforeToolCallback; bigger scope; needs a new rough-idea and fresh
   requirements round
5. **Tuning pass** — after deployment, use `/rtk` stats to identify remaining waste; feed back into detector thresholds
   and config defaults
