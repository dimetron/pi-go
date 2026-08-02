# 008 — adk-utils-go Review

Deep comparison of `tmp/adk-utils-go/` (third-party ADK utility library at `github.com/achetronic/adk-utils-go`) against
pi-go, producing a scored list of features pi-go could lift or port.

## Phase 1: Research (this prompt)

**Date:** 2026-07-30
**Method:** Two parallel `explore` subagents produced file:line-anchored inventories of both codebases. A third top-down
review synthesized the comparison.

## Artifacts

- `FEATURE_COMPARISON.md` — the full report (~1000 lines), including:
    - Executive summary (10 headlines)
    - Feature matrix (~110 rows across 11 categories)
    - 17 detailed feature analyses with value/effort/risk scoring
    - Phased roadmap (Phase 0–3 + deferred)
    - Appendices A–D (package maps, score matrix, file-by-file port plan, risks)

## Headline Findings

1. **`runner.PluginConfig` is not plumbed in pi-go** (`internal/agent/agent.go:361-365`). This is the single most
   important architectural gap — it gates every ADK plugin.
2. **Anthropic prompt caching is missing** except on the advisor tool beta. adk-utils-go's 75-LOC `caching.go` could
   deliver ~10× Claude cost reduction on agentic loops.
3. **No `artifact.Service` exists** — only a TUI sidebar stub at `tui.go:1736-1746` returning `nil`.
4. **No ADK `memory.Service` implementation** — pi-go's `internal/memory/` is a custom observation store, not the ADK
   interface.
5. **OTel infrastructure exists but doesn't enrich LLM spans** with prompts/responses/tokens. The langfuse plugin's
   `enrichingExporter` + `enrichedSpan` pattern is the canonical fix.

## Top Recommendations (sorted by total score)

| # | Feature                        | Total | Phase |
|--:|--------------------------------|:-----:|:-----:|
| 1 | `runner.PluginConfig` plumbing |  +23  |   0   |
| 1 | Anthropic prompt caching       |  +23  |   1   |
| 1 | ADK `memory.Service` interface |  +23  |   1   |
| 4 | Filesystem artifact service    |  +21  |   1   |
| 5 | Context-window auto-compaction |  +18  |   2   |

Scoring: `Total = Value × 5 − Effort − Risk`. See `FEATURE_COMPARISON.md` for the full matrix.

## Status

WIP — awaiting user prioritisation. See `FEATURE_COMPARISON.md` → `## Open Questions for the User` for the 8 questions
that need answers before any port work begins.
