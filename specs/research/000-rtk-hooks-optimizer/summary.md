# Summary — RTK Output Compactor for pi-go

## Artifacts

| File | Purpose |
|------|---------|
| `specs/rtk-hooks-optimizer/rough-idea.md` | Original concept from pi-rtk-optimizer |
| `specs/rtk-hooks-optimizer/requirements.md` | 11 Q&A decisions on scope, config, limits, architecture |
| `specs/rtk-hooks-optimizer/design.md` | Full design: architecture, components, data models, acceptance criteria, testing |
| `specs/rtk-hooks-optimizer/plan.md` | 12-step incremental implementation plan |
| `specs/rtk-hooks-optimizer/research/adk-hook-api.md` | ADK callback types and capabilities |
| `specs/rtk-hooks-optimizer/research/adk-callback-wiring.md` | Exact code path for wiring Go-native callbacks |
| `specs/rtk-hooks-optimizer/research/pi-go-extension-system.md` | Current architecture, tool flow, TUI, config |
| `specs/rtk-hooks-optimizer/research/rtk-optimizer-deep-dive.md` | Command rewriter, output compactor, techniques |
| `specs/rtk-hooks-optimizer/research/existing-truncation.md` | Current truncation gaps and opportunities |

## Overview

A Go-native output compaction system for pi-go, inspired by pi-rtk-optimizer. Implemented as an ADK `AfterToolCallback` that replaces existing per-tool truncation with a centralized, multi-stage pipeline.

**Scope:** Output compaction only (command rewriting deferred to Phase 2).

**Key decisions:**
- All 9 compaction stages, enabled by default
- Limits doubled from rtk-optimizer defaults (24K chars, 440 lines)
- Replaces existing `truncateOutput()` — single point of output processing
- Lives in `internal/tools/` package
- Config via config.json, no TUI modal
- `/rtk stats` for session metrics, TUI-only compaction indicator
- Per-session metrics persistence

**Implementation:** 12 incremental steps, each independently testable. Core end-to-end flow available by Step 3. Full pipeline by Step 12.

## Suggested Next Steps

1. **Implement** — Use `ralph run --config presets/spec-driven.yml` or implement manually following `plan.md`
2. **Phase 2 planning** — Command rewriting (BeforeToolCallback) to optimize bash commands
3. **Tuning** — After deployment, use `/rtk stats` data to adjust compaction limits
