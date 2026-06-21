# Headroom Architecture — Compression Pipeline Analysis

Source: `/Users/dimetron/p6s/pi-dev/pi-go/tmp/headroom` (Rust crate `headroom-core`).
Headroom is a context-compression layer for AI agents: 60–95% fewer tokens, library/proxy/MCP, 6 algorithms,
local-first, reversible.

## High-Level Architecture

```
input + content_type
  │
  ▼  rayon::join (parallel)
  ┌──────────────────────────┐    ┌────────────────────────────┐
  │ Reformat phase            │    │ Per-offload bloat phase    │
  │   serial over reformats   │    │   par_iter over offloads   │
  │   stop early if ratio ≤   │    │   each calls estimate_bloat│
  │   reformat_target_ratio   │    │                            │
  └──────────────────────────┘    └────────────────────────────┘
  │                                              │
  ▼                                              ▼
 ┌──────────────────────────────────────────────────────┐
  │ Decide which offloads to run                         │
  │   run_it = score ≥ bloat_threshold                   │
  │         OR (reformat_ratio > offload_fallback_ratio  │
  │             AND score > 0)                           │
  └──────────────────────────────────────────────────────┘
  │
  ▼  serial — each offload sees previous output
 ┌────────────────────────────────────┐
  │ Run gated offloads against store   │
  └────────────────────────────────────┘
```

## Two-Trait Pipeline Model

Headroom splits transforms into two categories — both "lossless w.r.t. information" because of CCR:

### ReformatTransform (lossless, no CCR needed)

- Packs input denser; output bytes semantically equivalent to input.
- No retrieval marker, no store write.
- Examples: `JsonMinifier` (whitespace stripping), `LogTemplate` (Drain-inspired template miner).

### OffloadTransform (CCR-backed, reversible)

- Drops bytes from wire, stashes original in `CcrStore` keyed by hash, emits `<<ccr:HASH>>` retrieval marker.
- Each offload carries `estimate_bloat(content) -> f32` (0.0–1.0) — cheap structural heuristic the orchestrator gates
  on.
- Examples: `LogOffload`, `DiffOffload`, `DiffNoise`, `JsonOffload`, `SearchOffload`.

### Orchestrator Decision Logic

- `reformat_target_ratio = 0.5` — if reformat output ≤ 50% of input, skip offloads unless bloat demands.
- `bloat_threshold = 0.5` — run offload if bloat score ≥ 0.5.
- `offload_fallback_ratio = 0.85` — if reformat barely helped (>85% of original), run offloads even below threshold.

## Content Type Detection (regex-based, no ML)

`ContentType` enum: `JsonArray`, `SourceCode`, `SearchResults`, `BuildOutput`, `GitDiff`, `Html`, `PlainText`.
Detection via compiled-once regex patterns for `file:line:` (search), `diff --git`/`@@` (diff), code keywords (`def`/
`class`/`import`/`func`/`fn`), JSON array-of-dicts shape.

## The 6 Compression Algorithms

### 1. LogCompressor (build/test output)

- Format detection: pytest/npm/cargo/jest/make/generic.
- Per-line classification: level (ERROR/FAIL/WARN/INFO/DEBUG/TRACE), stack-trace membership, summary-line membership.
- Per-line scoring: level base + stack-trace + summary boosts.
- **Adaptive total-lines budget** via `compute_optimal_k` (Kneedle algorithm on information saturation curve).
- Category selection: errors (first/last/top), fails, warnings (deduped), stack traces, summaries; context window around
  each.
- Optional CCR storage when `compression_ratio < 0.5`.
- Config: `max_errors=10`, `error_context_lines=3`, `keep_first_error=true`, `keep_last_error=true`,
  `max_stack_traces=3`, `stack_trace_max_lines`, `max_warnings`, `dedupe_warnings`, `keep_summary_lines`,
  `max_total_lines`.

### 2. DiffCompressor (git diff)

- Parses unified-diff into files + hunks.
- Caps file count (`max_files=20`) — sorts by total changes, keeps heaviest.
- Caps per-file hunk count (`max_hunks_per_file=10`) — keeps first + last + top-scored middle (relevance-aware via
  priority patterns + query-context word overlap).
- Trims context lines to `max_context_lines=2` on either side of `+`/`-`.
- Hunk scoring: `change_density_weight=0.03` (cap 0.3) + `context_word_weight=0.2` + `priority_pattern_boost=0.3`, total
  cap 1.0.
- CCR marker when `compressed < original * 0.8`.

### 3. SearchCompressor (grep/ripgrep)

- Parses `file:line:content` (handles Windows paths, filenames with dashes, ripgrep `-C` context).
- Scores each match: context-word overlap + `LineImportanceDetector` priority signals + config keywords.
- Sorts files by total match score; caps to `max_files`.
- Adaptive total via `compute_optimal_k` with bias.
- Per-file: always-keep first/last, fill remaining by score, sort back to line order.
- `group_by_file` mode (rg --heading style): emit file path once, then `line:content` rows.
- CCR when `min_matches_for_ccr` cleared and ratio < 0.8.

### 4. SmartCrusher (JSON arrays of dicts)

- Statistical compression of structured tool output (70–90% on JSON arrays).
- Array classification: object array, number array, string array, mixed.
- Schema dedup: detects ID/score fields statistically, preserves rare-status values, structural outliers, error items.
- **Adaptive item count** via `compute_optimal_k` (Kneedle on unique-bigram coverage curve + zlib-ratio validation).
- k-split: distributes keep-count across first/last/middle by fraction.
- Anchor-aware selection: query anchors extracted from user question, items matching anchors prioritized.
- Constraints: `KeepErrorsConstraint`, `KeepStructuralOutliersConstraint`.
- Observer pattern for tracing/telemetry.

### 5. LogTemplate (reformat, Drain-inspired)

- Collapses runs of consecutive same-template lines into `[Template Tn: ...] (Nx)` + variant table.
- Lossless — every original line reconstructible from template + variants.
- Algorithm: walk lines, split on whitespace, group into runs by `(token_count, leading_token)` shape +
  `similarity_threshold=0.4` positional match.
- Flush when run breaks: if `run.len() >= min_run=3` AND template has ≥ `min_constant_tokens=2` anchors, emit template
  block; else verbatim.

### 6. JsonMinifier (reformat)

- `serde_json` round-trip: parse → re-serialize compact. Strips whitespace.

## Bloat Estimation Heuristics (per-domain, O(n), cheap)

### LogOffload bloat

Two signals, weighted sum ≤ 1.0:

- **Repetition** (`uniqueness_weight=0.5`): `1 − unique_lines / sample_size`. Catches heartbeat spam.
- **Priority dilution** (`priority_dilution_weight=0.5`): `low_priority_lines / sample_size` via
  `LineImportanceDetector`. Catches unique-noise burying errors.
- `min_lines=50`, `sample_size=100`, `high_priority_threshold=0.4`.

### DiffOffload bloat

- Context-to-change ratio: `(context / (context + change) − normal_context_ratio) / (1 − normal_context_ratio)`,
  clamped [0,1].
- `min_lines=50`, `normal_context_ratio=0.6`.

### DiffNoise bloat

- Fraction of input bytes in droppable sections (lockfile hunks + whitespace-only hunks).
- Lockfile suffixes: `Cargo.lock`, `package-lock.json`, `yarn.lock`, `poetry.lock`, `go.sum`, etc.
- Whitespace-only: pair `-`/`+` lines, whitespace-collapse, compare.

### SearchOffload bloat

- Match clustering: `avg_matches_per_file` large → high bloat.
- `min_matches=10`, `cluster_threshold=10.0`.

## Signals: Line Importance Detection

`LineImportanceDetector` trait — cheap per-line classifier returning
`ImportanceSignal { category, priority[0.0-1.0], confidence[0.0-1.0] }`.

### KeywordDetector (aho-corasick, O(n+m))

- Single DFA scan finds all keywords on a line.
- Categories: Error (0.95), Security (0.85), Warning (0.75), Importance (0.6), Markdown (0.45).
- Error keywords: error, exception, fail, failed, failure, fatal, critical, crash, panic, abort, timeout, denied,
  rejected.
- Per-context: `ImportanceContext::{Text, Search, Diff, Log}` — different pattern sets fire per context.

### Tiered detector combinator

- Chains detectors by confidence; escalates to next tier if confidence < threshold.

## Adaptive Sizing: compute_optimal_k

Three-tier decision for "how many items to keep":

1. **Fast path**: `n <= 8` → keep all; ≤3 unique-by-simhash → keep that count.
2. **Kneedle**: on cumulative unique-bigram coverage curve. Coverage stops growing → knee → return that count. Diversity
   ratio floor: `keep_fraction = 0.3 + 0.7 * diversity_ratio`.
3. **Validation**: zlib-ratio sanity check. If keeping k items produces more-redundant subset than full set, bump k by
   20%.

## CCR (Compress-Cache-Retrieve)

- `CcrStore` trait: `put(hash, payload)`, `get(hash)`, `len()`.
- Backends: `InMemoryCcrStore` (DashMap), `SqliteCcrStore` (WAL, production default), `RedisCcrStore` (multi-worker).
- Key: BLAKE3 → first 24 hex chars.
- Marker: `<<ccr:HASH>>` injected into compressed output.
- Default TTL: 30 minutes. Default capacity: 1000.
- LLM retrieves dropped bytes via `headroom_retrieve` tool call.

## Pipeline Config (TOML, embedded defaults + runtime override)

```toml
[pipeline]
reformat_target_ratio = 0.5
bloat_threshold = 0.5
offload_fallback_ratio = 0.85

[bloat.log]
min_lines = 50
sample_size = 100
high_priority_threshold = 0.4
uniqueness_weight = 0.5
priority_dilution_weight = 0.5

[bloat.diff]
min_lines = 50
normal_context_ratio = 0.6

[bloat.search]
min_matches = 10
cluster_threshold = 10.0

[reformat.log_template]
min_lines = 20
min_run = 3
similarity_threshold = 0.4
min_constant_tokens = 2

[offload.json]
min_array_rows = 5
saturation_rows = 50

[offload.diff_noise]
min_lines = 20
lockfile_suffixes = ["Cargo.lock", "package-lock.json", ...]
drop_whitespace_only_hunks = true
```

## Key Differences from pi-go's Current RTK Compactor

| Aspect            | pi-go Current                                                 | Headroom                                                                      |
|-------------------|---------------------------------------------------------------|-------------------------------------------------------------------------------|
| Architecture      | Flat stage list, all stages run serially                      | Two-trait (Reformat + Offload) with parallel bloat estimation                 |
| Gating            | Per-stage bool flags in config                                | Per-offload `estimate_bloat` score + threshold gating                         |
| Adaptivity        | Fixed limits (MaxChars, MaxLines)                             | Adaptive `compute_optimal_k` (Kneedle + zlib validation)                      |
| Reversibility     | Lossy (truncation drops bytes permanently)                    | CCR-backed (originals stashed, retrievable via `<<ccr:HASH>>`)                |
| Content detection | Tool-name-based routing (`switch toolName`)                   | Content-type detection (regex) + tool-name fallback                           |
| Line importance   | No per-line scoring                                           | `LineImportanceDetector` with aho-corasick keyword detector, per-context      |
| Log compaction    | `filterBuildOutput` + `aggregateTestOutput` (format-specific) | `LogCompressor` with format detection + per-line scoring + adaptive budget    |
| Diff compaction   | `compactGitDiffText` (context trim only)                      | `DiffCompressor` + `DiffOffload` + `DiffNoise` (lockfile/whitespace dropping) |
| Search compaction | `groupSearchOutput` (group by file, cap per-file)             | `SearchCompressor` with match scoring + adaptive total + group_by_file        |
| JSON compaction   | None                                                          | `SmartCrusher` (statistical, schema dedup, anchor-aware)                      |
| Log templates     | None                                                          | `LogTemplate` (Drain-inspired, lossless template mining)                      |
| Config            | Flat `CompactorConfig` struct, JSON                           | Nested TOML, embedded defaults + runtime override                             |