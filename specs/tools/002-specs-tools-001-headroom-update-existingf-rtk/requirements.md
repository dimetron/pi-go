# Requirements

## Questions & Answers

### Q1: Which areas of headroom's architecture to prioritize?

**A (G):** Full headroom-style overhaul — all areas:

- Pipeline architecture (two-trait Reformat + Offload model with bloat-gated offloads)
- Adaptive sizing (compute_optimal_k / Kneedle algorithm)
- Line importance detection (aho-corasick keyword detector, per-context scoring)
- CCR reversibility (cache store + retrieval markers)
- New algorithms (LogTemplate, SmartCrusher, DiffNoise)
- Content-type detection (regex-based, not just tool-name)

### Q2: CCR storage backend?

**A (A):** In-memory only — per-session, lost on restart. Matches pi-go's session-scoped model. LLM can retrieve dropped
bytes via a new tool.

### Q3: Config format?

**A (A):** Keep JSON — extend existing CompactorConfig struct with new fields for bloat thresholds, adaptive sizing
params, etc. No TOML.

### Q4: SmartCrusher scope?

**A (A):** Include it — activates when bash commands output JSON (jq, kubectl get -o json, gh api), and future tools may
emit structured output.

### Q5: Parallelism approach in Go?

**A (B):** Use goroutines — run reformat and bloat estimation concurrently with errgroup or raw goroutines, matching
headroom's rayon::join parallel approach.

### Q6: Algorithm parity?

**A (A):** Full parity — byte-for-byte equivalence with headroom's Rust/Python output, including parity fixtures. Add Go
tests matching headroom's tests.

### Q7: Staging strategy for subagents?

**A (A):** Fully independent stages — each stage is standalone mergeable, compiles + passes tests on its own, maximum
parallelism for subagents. Stage N does not require Stage N-1.

### Q8: Backward compatibility?

**A (B):** Breaking change allowed — restructure config, new /rtk stats format, old configs need migration. Cleaner
design with user-facing changelog.

## Consolidated Requirements

### Functional

1. **Two-trait pipeline** — Refactor compactor into `ReformatTransform` (lossless) and `OffloadTransform` (CCR-backed)
   interfaces with bloat-gated dispatch.
2. **Bloat estimation** — Each offload carries `EstimateBloat(content) float32` (0.0–1.0); orchestrator gates on
   configurable threshold + reformat fallback ratio.
3. **Parallel execution** — Reformat phase and bloat estimation run concurrently via goroutines.
4. **Adaptive sizing** — `ComputeOptimalK` using Kneedle algorithm on information-saturation curves, with zlib-ratio
   validation. Replaces fixed limits for log/diff/search.
5. **Line importance detection** — Aho-corasick keyword detector with per-context (Text/Search/Diff/Log) pattern sets.
   Categories: Error, Warning, Security, Importance, Markdown. Returns priority + confidence scores.
6. **CCR store** — In-memory `CcrStore` (BLAKE3 key, 24-hex-char, `<<ccr:HASH>>` markers). Per-session, lost on restart.
7. **Retrieval tool** — New `headroom_retrieve`-style tool exposing CCR store to the LLM.
8. **LogTemplate** — Drain-inspired lossless template miner. Collapses consecutive same-template lines into
   `[Template Tn: ...] (Nx)` + variant table.
9. **SmartCrusher** — Statistical JSON array compression (schema dedup, anchor-aware selection, adaptive item count,
   outlier preservation).
10. **DiffNoise** — Drop lockfile + whitespace-only hunks via CCR.
11. **DiffCompressor enhancement** — Relevance-aware hunk scoring (change density + context-word overlap + priority
    patterns), file/hunk caps.
12. **SearchCompressor enhancement** — Match scoring, adaptive total, group_by_file mode.
13. **LogCompressor enhancement** — Format detection (pytest/npm/cargo/jest/make/generic), per-line classification +
    scoring, adaptive budget.
14. **Content-type detection** — Regex-based `ContentType` enum (JsonArray, SourceCode, SearchResults, BuildOutput,
    GitDiff, Html, PlainText), routing independent of tool name.

### Non-Functional

1. **Byte-for-byte parity** with headroom's Rust/Python output for all algorithms. Go tests must match headroom's test
   fixtures.
2. **Breaking config change** — restructured `CompactorConfig`, new `/rtk stats` format. Migration documented in
   changelog.
3. **Each stage independently mergeable** — compiles + passes tests standalone, no cross-stage dependencies.
4. **No external Go dependencies for core algorithms** — use stdlib + existing deps only (no aho-corasick Go lib if not
   already present; implement with strings.Contains or regexp if needed).

### Acceptance Criteria

- Given a 10,000-line build log with 5 errors, when LogCompressor runs, then output ≤ 50 lines preserving all errors +
  stack traces + summary lines.
- Given a git diff with 200 lines but only 3 change lines, when DiffOffload runs, then context trimmed to 2 lines around
  changes, CCR marker emitted.
- Given a Cargo.lock diff hunk, when DiffNoise runs, then hunk dropped from wire output, original stashed in CCR store,
  `<<ccr:HASH>>` marker emitted.
- Given grep output with 100 matches across 5 files, when SearchCompressor runs, then output group_by_file, adaptive
  total via Kneedle, top-scored matches kept.
- Given JSON array output from `kubectl get -o json`, when SmartCrusher runs, then schema dedup applied, rare-status
  values preserved, adaptive item count.
- Given consecutive log lines sharing a template, when LogTemplate runs, then lines collapsed into
  `[Template Tn: ...] (Nx)` block, every original line reconstructible.
- Given any compacted output with CCR marker, when LLM calls retrieve tool with hash, then original bytes returned from
  in-memory store.
- Given `/rtk stats` command, when metrics have records, then new format shows per-stage, per-content-type, per-strategy
  breakdown.