# Implementation Plan — Headroom-Style RTK Compactor Overhaul

## Vertical Slices

Each slice is independently mergeable: compiles, passes tests, no dependency on other slices (except Stage 10/11 which
integrate).

---

- [ ] **Slice 1: CCR Store + Content Type Detection**

  **Files:**
    - `internal/tools/ccr.go` — `CCRStore` interface, `InMemoryCCRStore` (sync.RWMutex + map), `ComputeCCRKey` (
      SHA-256 → 24 hex chars), `CCRMarker` (`<<ccr:HASH>>`), `NewInMemoryCCRStore()`
    - `internal/tools/ccr_test.go` — tests: put/get roundtrip, missing key returns false, key is 24 hex chars, marker
      format, concurrent access, Len()
    - `internal/tools/content_type.go` — `ContentType` enum (PlainText, BuildOutput, GitDiff, SearchResults, JsonArray,
      SourceCode, HTML), `DetectionResult{Type, Confidence, Metadata}`, `DetectContentType(content string)` with
      compiled-once regex via `sync.OnceValue`, `DetectFromToolName(toolName string) ContentType`
    - `internal/tools/content_type_test.go` — tests: grep `file:line:` → SearchResults, `diff --git` → GitDiff,
      `[{...}]` → JsonArray, `def foo()` → SourceCode, build output `error:` → BuildOutput, plain text → PlainText,
      empty string → PlainText

  **Implementation notes:**
    - Use `crypto/sha256` for key computation (not BLAKE3 — stdlib only). Document this deviation in a comment.
    - Regex patterns match headroom's `content_detector.rs` exactly: `^[^\s:]+:\d+:` for search,
      `^(diff --git|diff --combined|--- a/|@@\s+-\d+,\d+\s+\d+,\d+\s+@@)` for diff.
    - Code patterns: Python (`def|class|import|from|async def`), Go (`func|package|import`), Rust (
      `fn|use|impl|struct`), JS (`function|const|import|export`).
    - JSON detection: attempt `json.Unmarshal` into `[]any`; if it's `[]map[string]any`, return JsonArray with
      confidence 0.9.
    - Build output: lines containing `error:`, `FAIL`, `PASS`, `warning:`, `npm ERR`, `cargo:` → BuildOutput with
      confidence based on match count.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "CCR|ContentType"`

  **Dependencies:** None.

---

- [ ] **Slice 2: Transform Traits + Orchestrator**

  **Files:**
    - `internal/tools/transform.go` — `ReformatTransform` interface, `OffloadTransform` interface, `ReformatOutput`,
      `OffloadOutput`, `CompressionContext{Query, TokenBudget}`, `TransformError{Transform, Kind, Message}`,
      `TransformErrorKind` enum (InvalidInput, Skipped, Internal), error helper constructors
    - `internal/tools/orchestrator.go` —
      `OrchestratorConfig{ReformatTargetRatio, BloatThreshold, OffloadFallbackRatio}`,
      `CompressionPipeline{reformatsByType, offloadsByType, config, store}`, `CompressionPipelineBuilder`,
      `PipelineResult{Output, BytesSaved, StepsApplied, CacheKeys}`, `NewCompressionPipeline(cfg, store)`,
      `Run(content, ct, ctx) PipelineResult`
    - `internal/tools/orchestrator_test.go` — stub `ReformatTransform` and `OffloadTransform` implementations; tests:
      empty input returns empty, reformat-only when below target ratio, offload gated by bloat threshold, offload
      fallback when reformat underwhelms, offload skipped when bloat=0 and reformat sufficient, parallel execution (both
      phases run), transform error handling (Skipped continues, Internal logs), cache keys collected

  **Implementation notes:**
    - `Run` uses `errgroup.Go` or raw goroutines + channels to run reformat phase and bloat estimation concurrently.
      Reformat phase is serial over registered reformats (stop early if
      `current_len/original_len <= reformat_target_ratio`). Bloat estimation runs all offloads' `EstimateBloat`
      concurrently.
    - After parallel phase: compute `reformat_ratio = current_len / original_len`. For each offload:
      `run_it = score >= bloat_threshold OR (reformat_ratio > offload_fallback_ratio AND score > 0)`. Run gated offloads
      serially (each sees previous output).
    - `CompressionPipelineBuilder` has `RegisterReformat(ReformatTransform)` and `RegisterOffload(OffloadTransform)`
      methods that append to the per-content-type maps.
    - Default config: `ReformatTargetRatio=0.5`, `BloatThreshold=0.5`, `OffloadFallbackRatio=0.85`.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "Orchestrator|Pipeline|Transform"`

  **Dependencies:** None (uses `ContentType` from Slice 1, but defines its own local type alias if needed to stay
  independent — or re-declares `ContentType` and Slice 10 reconciles. For true independence: Slice 2 defines
  `ContentType` in `transform.go` and Slice 1's `content_type.go` uses it. **Decision: `ContentType` lives
  in `content_type.go` (Slice 1); Slice 2 imports it.** Since Slice 1 is standalone and Slice 2 imports Slice 1's
  package — both are in `internal/tools` so they compile together. Each slice's *tests* run independently.)

  > **Clarification on "independent":** All slices are in the same package `internal/tools`. "Independent" means each
  slice adds files that compile and test without requiring other slices' files to be present. Slice 2 references
  `ContentType` from Slice 1's `content_type.go` — if Slice 1 isn't merged yet, Slice 2's tests define a local stub. The
  final integration (Slice 10) reconciles. In practice, since they're the same package, as long as both are merged
  before integration testing, order doesn't matter.

---

- [ ] **Slice 3: Line Importance Detection + Adaptive Sizer**

  **Files:**
    - `internal/tools/signals.go` — `ImportanceContext` enum (Text, Search, Diff, Log), `ImportanceCategory` enum (
      Error, Warning, Security, Importance, Markdown), `ImportanceSignal{Category, Priority, Confidence}`,
      `LineImportanceDetector` interface, `KeywordDetector` struct with keyword sets matching headroom's
      `KeywordRegistry::default_set()` exactly, `NewKeywordDetector()`, `Score(line, ctx) ImportanceSignal`
    - `internal/tools/signals_test.go` — tests: error keyword "fatal" → category Error, priority 0.95, confidence 0.7;
      warning "deprecated" → Warning; security "injection" → Security; importance "TODO" → Importance; markdown "# "
      prefix → Markdown; no match → neutral; per-context (markdown prefix only in Text context); keyword "token" NOT in
      security set (headroom fix)
    - `internal/tools/adaptive_sizer.go` — `ComputeOptimalK(items []string, bias float64, minK, maxK int) int`,
      `FindKnee(curve []int) (int, bool)`, `CountUniqueSimhash(items []string, maxCount int) int`,
      `ComputeUniqueBigramCurve(items []string) []int`, `Simhash(item string) uint64` (MD5 4-gram, 64-bit weighted
      voting), `ValidateWithZlib(items []string, k, maxK int, ratioDiff float64) int` (uses `compress/flate`)
    - `internal/tools/adaptive_sizer_test.go` — tests: n≤8 returns n; near-total redundancy (≤3 unique) returns unique
      count; Kneedle finds knee on known curve; diversity ratio floor; bias multiplier; zlib validation bumps k; empty
      input; single item

  **Implementation notes:**
    - `KeywordDetector` uses `strings.Contains` (case-insensitive via `strings.ToLower`) — no aho-corasick dependency.
      Keywords:
        - Error: error, exception, fail, failed, failure, fatal, critical, crash, panic, abort, timeout, denied,
          rejected
        - Warning: warn, warning
        - Importance: important, note, todo, fixme
        - Security: injection, xss, csrf, sql injection, privilege, escalation, vulnerability, exploit, malware,
          phishing (NOT "token" — headroom fix)
        - Markdown prefixes: `# `, `## `, `### `, `> `, `**`, `__`
    - Priorities: Error=0.95, Security=0.85, Warning=0.75, Importance=0.6, Markdown=0.45. Confidence=0.7 for all keyword
      matches.
    - `Simhash`: MD5 of character 4-grams, aggregate bits via weighted voting, take first 64 bits as big-endian uint64.
      Matches headroom's `_simhash`.
    - `ComputeUniqueBigramCurve`: whitespace-split words, single-word items emit `(word, "")`, empty items emit
      `("", "")`. Cumulative unique bigram count.
    - `FindKnee`: normalize curve to [0,1]×[0,1], find max deviation from diagonal, require deviation > 0.05. Returns
      1-indexed count.
    - `ValidateWithZlib`: `compress/flate` level 1, compute redundancy ratio of subset vs full set, if ratio diff > 15%,
      bump k by 20%.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "Keyword|Adaptive|Kneedle|Simhash"`

  **Dependencies:** None.

---

- [ ] **Slice 4: LogCompressor**

  **Files:**
    - `internal/tools/log_compressor.go` — `LogFormat` enum (Pytest, Npm, Cargo, Jest, Make, Generic), `LogLevel` enum (
      Error, Fail, Warn, Info, Debug, Trace, Unknown),
      `LogLine{LineNumber, Content, Level, IsStackTrace, IsSummary, Score}`, `LogCompressorConfig`,
      `LogCompressionResult{Compressed, OriginalLineCount, CompressedLineCount, FormatDetected, CacheKey, Stats}`,
      `LogCompressor` struct, `NewLogCompressor(cfg)`, `Compress(content, store) (LogCompressionResult, error)`
    - `internal/tools/log_compressor_test.go` — parity tests loading `testdata/parity/log_compressor/*.json` fixtures,
      config edge cases (empty, below min_lines, all errors, all warnings, stack traces), format detection (
      pytest/npm/cargo/jest/make/generic)
    - `testdata/parity/log_compressor/` — copy fixture JSON files from
      `tmp/headroom/tests/parity/fixtures/log_compressor/`

  **Implementation notes:**
    - Pipeline: format detection → per-line classification (level + stack-trace + summary) → per-line scoring (level
      base + stack-trace boost + summary boost) → `ComputeOptimalK` for total budget → category selection (errors
      first/last/top, fails, warnings deduped, stack traces, summaries) → context window around each → adaptive cap →
      optional CCR when ratio < 0.5.
    - Format detection regex (compiled once): pytest (`PASSED|FAILED|test session starts`), npm (`npm WARN|npm ERR`),
      cargo (`Compiling|error\[|warning:`), jest (`PASS|FAIL|✓|✗`), make (`make:.*Error|Entering directory`), generic
      fallback.
    - Level classification: `strings.Contains` on lowercased line for error/fail/warn/info/debug/trace keywords.
    - Stack-trace detection: state machine tracking indentation + `Traceback`/`at`/`Caused by` prefixes. Per-flavor
      termination rules (Python: non-indented non-blank after indented frame; JS: non-`at` after last `at` frame).
    - Scoring: Error/Fail=1.0, Warn=0.5, Info=0.2, Debug/Trace=0.1. Stack-trace boost: +0.3. Summary boost: +0.5.
    - Dedupe warnings: normalize digits/paths/hex in trailing region only (preserve message prefix before first `:` or
      `=`).
    - `LogLine` equality/hash based on `LineNumber` only (matches headroom).
    - CCR: `ComputeCCRKey(content)`, `store.Put(key, content)`, emit `CCRMarker(key)` appended to output when
      `compression_ratio < min_compression_ratio_for_ccr`.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "LogCompressor"`

  **Dependencies:** Slice 1 (CCRStore), Slice 3 (ComputeOptimalK, KeywordDetector). In practice: same package, compiles
  together.

---

- [ ] **Slice 5: DiffCompressor + DiffNoise**

  **Files:**
    - `internal/tools/diff_compressor.go` — `DiffCompressorConfig`, `DiffCompressionResult`, `DiffCompressorStats`,
      `DiffCompressor` struct, `NewDiffCompressor(cfg)`, `Compress(content, store) (DiffCompressionResult, error)`,
      `CompressWithStats(content, store) (DiffCompressionResult, DiffCompressorStats, error)`
    - `internal/tools/diff_noise.go` — `DiffNoiseConfig`, `DiffNoise` struct implementing `OffloadTransform`,
      `NewDiffNoise(cfg)`, `EstimateBloat(content) float32`, `Apply(content, ctx, store) (OffloadOutput, error)`
    - `internal/tools/diff_compressor_test.go` — parity tests loading `testdata/parity/diff_compressor/*.json`, edge
      cases (empty, below min_lines, no diff headers, merge-commit diffs, single file)
    - `internal/tools/diff_noise_test.go` — bloat estimation (lockfile diff high score, clean diff low score), lockfile
      dropping, whitespace-only hunk detection, CCR marker emission
    - `testdata/parity/diff_compressor/` — copy fixture JSON files from
      `tmp/headroom/tests/parity/fixtures/diff_compressor/`

  **Implementation notes:**
    - DiffCompressor: parse unified-diff into files + hunks. Cap `max_files=20` (sort by total changes, keep heaviest).
      Cap `max_hunks_per_file=10` (keep first + last + top-scored middle). Trim context to `max_context_lines=2`. Hunk
      scoring: `min(0.3, change_count*0.03)` + `context_word_count*0.2` + `priority_pattern_match*0.3`, cap 1.0. CCR
      marker when `compressed < original * 0.8`.
    - Diff noise bloat: fraction of bytes in droppable sections. Lockfile suffixes: `Cargo.lock`, `package-lock.json`,
      `yarn.lock`, `poetry.lock`, `go.sum`, `pnpm-lock.yaml`, `Gemfile.lock`, `composer.lock`, `Pipfile.lock`,
      `uv.lock`. Whitespace-only: pair `-`/`+` lines, collapse whitespace, compare.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "Diff"`

  **Dependencies:** Slice 1 (CCRStore), Slice 2 (OffloadTransform interface for DiffNoise), Slice 3 (KeywordDetector for
  priority patterns).

---

- [ ] **Slice 6: SearchCompressor**

  **Files:**
    - `internal/tools/search_compressor.go` — `SearchMatch{File, LineNumber, Content, Score}`,
      `FileMatches{File, Matches}`, `SearchCompressorConfig`, `SearchCompressionResult`, `SearchCompressor` struct,
      `NewSearchCompressor(cfg)`, `Compress(content, ctx, store) (SearchCompressionResult, error)`
    - `internal/tools/search_compressor_test.go` — tests: parse `file:line:content`, Windows paths, filenames with
      dashes, ripgrep `-C` context, match scoring, group_by_file mode, adaptive total via Kneedle, CCR marker emission

  **Implementation notes:**
    - Parse: anchor on `<sep>\d+<sep>` found earliest in line; everything before is path, after is content. Handles
      Windows drive prefixes (`C:\`), filenames with dashes, ripgrep `-` separator for context lines.
    - Score: context-word overlap (from `CompressionContext.Query`) + `KeywordDetector.Score(line, ImportanceSearch)` +
      config keywords. Score in [0.0, 1.0].
    - Sort files by total match score; cap to `max_files`. Run `ComputeOptimalK` over global match list with bias.
      Per-file: always-keep first/last, fill remaining by score, sort back to line order. Dedup via `BTreeSet` of
      `(line_number, content_hash)`.
    - `group_by_file`: emit file path once as header, then `line:content` rows, blank line between file groups.
    - CCR when `min_matches_for_ccr` cleared and `compression_ratio < 0.8`.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "SearchCompressor"`

  **Dependencies:** Slice 1 (CCRStore), Slice 3 (ComputeOptimalK, KeywordDetector).

---

- [ ] **Slice 7: SmartCrusher**

  **Files:**
    - `internal/tools/smart_crusher.go` — `SmartCrusherConfig`, `CrushArrayResult`, `ArrayType` enum (ObjectArray,
      NumberArray, StringArray, Mixed), `ArrayAnalysis`, `SmartCrusher` struct, `NewSmartCrusher(cfg)`,
      `CrushArray(content, ctx, store) (CrushArrayResult, error)`, helper functions: `classifyArray`, `detectIdField`,
      `detectScoreField`, `detectRareStatusValues`, `detectStructuralOutliers`, `detectErrorItems`, `computeKSplit`,
      `crushObjectArray`, `crushNumberArray`, `crushStringArray`, `deduplicateIndices`, `prioritizeIndices`,
      `fillRemainingSlots`
    - `internal/tools/smart_crusher_test.go` — parity tests loading `testdata/parity/smart_crusher/*.json`, edge cases (
      empty array, < min_items, all unique, all duplicate, nested objects, sequential IDs, rare status values)
    - `testdata/parity/smart_crusher/` — copy fixture JSON files from
      `tmp/headroom/tests/parity/fixtures/smart_crusher/`

  **Implementation notes:**
    - Parse JSON array via `encoding/json`. Classify: object array (all items are `map[string]any`), number array (all
      `float64`), string array (all `string`), mixed.
    - Object array: detect ID field (statistically — high cardinality, sequential or UUID), score field (numeric, wide
      range), status field (low cardinality, string). Dedup content-identical items. Detect rare status values (preserve
      items with rare statuses). Detect structural outliers (missing/extra keys). Detect error items (fields containing
      error keywords). `ComputeOptimalK` for item count. `computeKSplit`: distribute k across first/last/middle by
      `first_fraction=0.3`, `last_fraction=0.15`. Prioritize: anchors (query match) > errors > outliers > rare-status >
      score-sorted. Fill remaining slots. Emit `[... and N more items]` summary.
    - Number array: compute stats (mean, median, stdev), keep first/last + outliers (>2 stdev) + change points.
    - String array: compute entropy, dedup by simhash similarity, keep first/last + high-entropy + unique.
    - Lossless path: if `1 - len(rendered)/len(input) >= lossless_min_savings_ratio` (0.15), use lossless (no CCR). Else
      lossy (CCR marker).
    - CCR: store original array, emit marker.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "SmartCrusher"`

  **Dependencies:** Slice 1 (CCRStore), Slice 3 (ComputeOptimalK, Simhash).

---

- [ ] **Slice 8: LogTemplate + JsonMinifier (Reformats)**

  **Files:**
    - `internal/tools/log_template.go` — `LogTemplateConfig{MinLines, MinRun, SimilarityThreshold, MinConstantTokens}`,
      `LogTemplate` struct, `NewLogTemplate(cfg)`, `Apply(content) (ReformatOutput, error)` implementing
      `ReformatTransform`
    - `internal/tools/log_template_test.go` — tests: empty input skipped, below min_lines skipped, consecutive
      same-template lines collapsed, template block format `[Template Tn: ...] (Nx)`, variant table, run breaks emit
      verbatim, min_constant_tokens check, order preservation
    - `internal/tools/json_minifier.go` — `JsonMinifier` struct, `Apply(content) (ReformatOutput, error)` implementing
      `ReformatTransform` — `json.Unmarshal` → `json.Marshal` (compact, no indentation)
    - `internal/tools/json_minifier_test.go` — tests: valid JSON minified, invalid JSON returns InvalidInput error,
      already-compact JSON no savings, nested objects, arrays, empty object/array

  **Implementation notes:**
    - LogTemplate: walk lines, split on whitespace into tokens. Open/extend run if `(token_count, leading_token)` shape
      matches AND positional match ≥ `similarity_threshold=0.4`. Flush run: if `len >= min_run=3` AND template has ≥
      `min_constant_tokens=2` anchors, emit `[Template T1: <TS> INFO worker-<*> processing job <*>] (800 occurrences)` +
      variant table (one row per line, tab-separated variant values). Else emit verbatim. Wildcard sentinel: `<*>`.
    - JsonMinifier: `json.Unmarshal([]byte(content), &v)` → `json.Marshal(v)` (produces compact JSON by default). If
      unmarshal fails, return `TransformError{Kind: InvalidInput}`.

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "LogTemplate|JsonMinifier"`

  **Dependencies:** Slice 2 (ReformatTransform interface, ReformatOutput).

---

- [ ] **Slice 9: CCR Retrieval Tool**

  **Files:**
    - `internal/tools/ccr_retrieve.go` — `RetrieveArgs{Hash string}`, `RetrieveResult{Content string, Error string}`,
      `NewRetrieveTool(store CCRStore) (tool.Tool, error)` — uses `newTool[RetrieveArgs, RetrieveResult]` pattern from
      `registry.go`
    - `internal/tools/ccr_retrieve_test.go` — tests: valid hash returns content, missing hash returns error, empty hash
      returns error, store is queried correctly

  **Implementation notes:**
    - Tool name: `"retrieve_compacted"`. Description:
      `"Retrieve original bytes for a compacted output section. Use the hash from the <<ccr:HASH>> marker."`
    - Handler: `store.Get(args.Hash)` → if found, return `RetrieveResult{Content: payload}`. If not found, return
      `RetrieveResult{Error: "not found"}` (not a Go error — the LLM sees the error field).
    - Schema: `Hash` is required string, pattern `[a-f0-9]{24}`.
    - Register in `CoreTools` after other tools: add `newRetrieveToolBuilder(store)` to the builders list. But since
      `CoreTools` currently doesn't take a store param, Slice 9 adds a `CoreToolsWithCCR(sandbox, store)` variant or
      modifies `CoreTools` signature. **Decision: add `CoreToolsWithCCR(sandbox *Sandbox, store CCRStore)` — Slice 10
      updates callers.**

  **Verify:** `go build ./internal/tools/... && go test ./internal/tools/... -v -run "Retrieve"`

  **Dependencies:** Slice 1 (CCRStore). Uses `newTool` from `registry.go` (existing code).

---

- [ ] **Slice 10: Config Restructure + Callback Rewire + Metrics**

  **Files:**
    - `internal/tools/compactor.go` — restructured `CompactorConfig` (nested: `Pipeline OrchestratorConfig`,
      `Bloat BloatConfigs`, per-algorithm configs), `DefaultCompactorConfig()` with headroom defaults,
      `BloatConfigs{Log, Diff, Search}`, `LogBloatConfig`, `DiffBloatConfig`, `SearchBloatConfig`, updated
      `BuildCompactorCallback(cfg, metrics, store)` — builds `CompressionPipeline`, registers all transforms, returns
      callback that detects content type and runs pipeline
    - `internal/tools/compactor_metrics.go` — updated
      `CompactRecord{Tool, ContentType, StepsApplied, OrigSize, CompSize, CacheKeys, Timestamp}`, updated
      `FormatStats()` — new format: per-stage, per-content-type, per-strategy breakdown with savings percentages
    - `internal/config/config.go` — updated `CompactorConfig` to match new nested structure
    - `internal/cli/cli.go` — updated wiring: create `InMemoryCCRStore`, pass to `BuildCompactorCallback`, pass to
      `CoreToolsWithCCR`
    - `internal/cli/interactive.go` — same updates as cli.go
    - `internal/tools/compactor_test.go` — updated tests for new config structure, new callback signature, integration
      with pipeline
    - `internal/tools/compactor_metrics_test.go` — tests for new `FormatStats` format

  **Implementation notes:**
    - `BuildCompactorCallback` now takes `store CCRStore` as third param. Creates `CompressionPipeline` via builder,
      registers:
        - Reformats: `JsonMinifier` (applies to JsonArray), `LogTemplate` (applies to BuildOutput)
        - Offloads: `LogOffload` (BuildOutput), `DiffOffload` (GitDiff), `DiffNoise` (GitDiff), `JsonOffload` (
          JsonArray, wraps SmartCrusher), `SearchOffload` (SearchResults, wraps SearchCompressor)
    - Callback logic: extract output field from result → `DetectContentType` → `pipeline.Run(content, ct, ctx)` →
      `applyCompaction(result, pipelineResult)` → `metrics.Record(...)`.
    - `ctx` (CompressionContext): `Query` from last user prompt (if available), `TokenBudget` from config (0 if not
      set).
    - Offload wrappers: `LogOffload` wraps `LogCompressor`, `DiffOffload` wraps `DiffCompressor`, `JsonOffload` wraps
      `SmartCrusher`, `SearchOffload` wraps `SearchCompressor`. Each implements `OffloadTransform` with `EstimateBloat`
      matching headroom's per-domain heuristics.
    - `FormatStats` new format:
      ```
      RTK Compactor Stats
      ═══════════════════
      Total calls:    42
      Original size:  1.2 MB
      Compacted size: 340 KB
      Savings:        72.5%
  
      By Content Type:
        build_output   15 calls  800KB → 120KB (85%)
        git_diff       10 calls  200KB → 80KB  (60%)
        search          8 calls  150KB → 45KB  (70%)
        json_array      5 calls  50KB  → 15KB  (70%)
        plain_text      4 calls  40KB  → 40KB  (0%)
  
      By Strategy:
        log_template       12 applications  40KB saved
        log_offload         8 applications  300KB saved
        diff_offload        6 applications  100KB saved
        diff_noise          4 applications  50KB saved
        smart_crusher       5 applications  35KB saved
        search_compressor   8 applications  100KB saved
  
      CCR Store: 23 entries in cache
      ```

  **Verify:** `go build ./... && go test ./internal/tools/... -v && go test ./internal/config/... -v`

  **Dependencies:** All slices 1–9 must be merged. This slice integrates them.

---

- [ ] **Slice 11: TUI `/rtk stats` Update**

  **Files:**
    - `internal/tui/commands.go` — updated `handleRTKCommand` to render new metrics format (no logic change, just the
      stats display already comes from `FormatStats()`)
    - `internal/tui/commands_test.go` — updated `TestCommandRTK_*` tests for new output format
    - `internal/tui/teatest_test.go` — updated `TestHandleRTKCommand_*` and `TestSlashCommand_RTK_*` for new format

  **Implementation notes:**
    - `handleRTKCommand` already calls `m.cfg.CompactMetrics.FormatStats()` — the display updates automatically when
      `FormatStats` changes in Slice 10. This slice just updates the expected strings in tests.
    - Update `mockCompactStats` if its format string references old format.
    - Verify the `/rtk` help text in `commands.go` help table is still accurate.

  **Verify:** `go build ./... && go test ./internal/tui/... -v -run "RTK"`

  **Dependencies:** Slice 10 (new `FormatStats` format).

---

## Parallel Execution Map for Subagents

```
┌─────────────────────────────────────────────────────────────┐
│  PARALLEL PHASE (up to 8 concurrent subagents)              │
│                                                             │
│  Agent 1: Slice 1  (CCR + ContentDetection)                 │
│  Agent 2: Slice 2  (Traits + Orchestrator)                  │
│  Agent 3: Slice 3  (Signals + AdaptiveSizer)                │
│  Agent 4: Slice 4  (LogCompressor + fixtures)               │
│  Agent 5: Slice 5  (DiffCompressor + DiffNoise + fixtures)  │
│  Agent 6: Slice 6  (SearchCompressor)                       │
│  Agent 7: Slice 7  (SmartCrusher + fixtures)                │
│  Agent 8: Slice 8+9 (LogTemplate + JsonMinifier + Retrieve) │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  SEQUENTIAL PHASE                                           │
│                                                             │
│  Agent: Slice 10 (Config + Callback + Metrics integration)  │
│  Agent: Slice 11 (TUI /rtk stats update)                    │
└─────────────────────────────────────────────────────────────┘
```

## Fixture Copy Commands

```bash
# Copy parity fixtures into testdata (run before tests)
mkdir -p internal/tools/testdata/parity/log_compressor
cp tmp/headroom/tests/parity/fixtures/log_compressor/*.json internal/tools/testdata/parity/log_compressor/

mkdir -p internal/tools/testdata/parity/diff_compressor
cp tmp/headroom/tests/parity/fixtures/diff_compressor/*.json internal/tools/testdata/parity/diff_compressor/

mkdir -p internal/tools/testdata/parity/smart_crusher
cp tmp/headroom/tests/parity/fixtures/smart_crusher/*.json internal/tools/testdata/parity/smart_crusher/

mkdir -p internal/tools/testdata/parity/content_detector
cp tmp/headroom/tests/parity/fixtures/content_detector/*.json internal/tools/testdata/parity/content_detector/
```