# Headroom-Style RTK Compactor Overhaul (Redo)

## Objective

Replace pi-go's flat serial RTK compactor (`internal/tools/compactor*.go`)
with a headroom-style two-trait pipeline (`ReformatTransform` + `OffloadTransform`)

+ CCR store + 6 algorithms + content-type routing, executed **serially in
  12 slices** to address the silent-failure mode of the previous attempt
  (see `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/SUMMARY.md`).

The previous attempt's design and research artifacts are reusable — this
spec adds structural safeguards (per-slice gates that exercise new code,
mandatory non-empty git diff, mandatory new test functions per slice).

## Key Requirements

1. **Two-trait pipeline** — `ReformatTransform` (lossless, no CCR) +
   `OffloadTransform` (CCR-backed, gated by `EstimateBloat` method
   returning `float32`). Bloat estimation is a **method on
   `OffloadTransform`**, not a free function.
2. **Parallel reformat + bloat estimation** via raw goroutines +
   `sync.WaitGroup` (no errgroup dep).
3. **CCR store** — in-memory `CCRStore` interface (1000-entry default,
   30-min TTL), SHA-256[:24] keys, `<<ccr:HASH>>` markers, lazy expiry.
4. **6 algorithms** — `LogCompressor`, `DiffCompressor`+`DiffNoise`,
   `SearchCompressor`, `SmartCrusher`, `LogTemplate`, `JsonMinifier`.
5. **Content-type detection** — regex-based `ContentType` enum
   (`JsonArray`, `SourceCode`, `SearchResults`, `BuildOutput`,
   `GitDiff`, `Html`, `PlainText`) routing **independent of tool name**.
6. **Adaptive sizing** — `ComputeOptimalK` (Kneedle on bigram-coverage
   curve + zlib-ratio validation), `FindKnee`, `Simhash`.
7. **Line importance detection** — `KeywordDetector` via
   `strings.Contains` (no aho-corasick dep) with word-boundary post-
   filter. Categories + priorities match headroom's
   `KeywordRegistry::default_set()` exactly.
8. **CCR retrieval tool** — `retrieve_compacted(hash)` ADK tool
   returning raw bytes prefixed by
   `<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>`
   header.
9. **Byte-for-byte parity** — all 85 fixtures in
   `internal/tools/testdata/parity/{content_detector,diff_compressor,log_compressor,smart_crusher}/`
   wired into Go tests via `filepath.Walk`.
10. **Config restructure + auto-migration** — old flat
    `CompactorConfig` migrated to nested shape in memory at load, with
    one-time deprecation warning logged. Old config files work
    unchanged.
11. **New `/rtk stats` format** — 4 sections (By Content Type, By Stage,
    By Strategy, By Tool) with deterministic alphabetical ordering.
12. **No new external Go deps** — stdlib only (`crypto/sha256`,
    `compress/flate`, `encoding/json`, `regexp`, `sync`, `sort`,
    `strings`, `time`, `errors`).
13. **12 slices, serial execution** — each slice is a single commit,
    each must pass a gate that exercises the slice's new code (no
    vacuous pass).
14. **`SearchOffload` NOT in default pipeline** — `SearchCompressor`
    registered only as `ReformatTransform`, matching headroom.

## Acceptance Criteria

### Slice-by-slice (Given/When/Then)

- **S1 (CCR + ContentType):** Given 21 content_detector parity fixtures,
  when `DetectContentType` runs on each `input`, then
  `result.Type.String() == fixture.output.content_type` byte-for-byte.
  Given any payload, when `ComputeCCRKey(payload)` runs twice, then
  same 24-hex-char hash returned.
- **S2 (Traits + Orchestrator):** Given a stub reformat that halves
  content + a stub offload with `EstimateBloat = 0.6`, when
  `CompressionPipeline.Run` is called with
  `BloatThreshold=0.5`, then offload fires and result includes the
  offload's `CacheKey`.
- **S3 (Signals + AdaptiveSizer):** Given line `ERROR: foo`, when
  `KeywordDetector.Score(line, ImportanceLog)` runs, then
  `result.Priority == 0.95`. Given a flat curve `[1,1,1,1]`, when
  `FindKnee` runs, then `(1, true)` returned.
- **S4 (LogCompressor):** Given 20 log_compressor parity fixtures, when
  `LogCompressor.Compress` runs on each, then all 7 output fields
  (`compressed`, `original_line_count`, `compressed_line_count`,
  `compression_ratio`, `format_detected`, `cache_key`, `stats`) match
  fixture fields byte-for-byte.
- **S5 (DiffCompressor + DiffNoise):** Given 27 diff_compressor parity
  fixtures, when `DiffCompressor.Compress` runs on each, then all 9
  output fields match byte-for-byte. Given a Cargo.lock diff hunk, when
  `DiffNoise.Apply` runs, then the hunk is dropped and
  `[diff_noise CCR: hash=HASH]` is emitted.
- **S6 (SearchCompressor):** Given 100 grep matches across 5 files,
  when `SearchCompressor.Compress` runs, then output groups by file
  with adaptive total.
- **S7 (SmartCrusher):** Given 17 smart_crusher parity fixtures, when
  `SmartCrusher.Crush` runs on each, then `compressed`, `original`,
  `was_modified`, `strategy` match fixture fields byte-for-byte.
- **S8 (LogTemplate + JsonMinifier):** Given 10 consecutive lines of
  `INFO foo bar baz`, when `LogTemplate.Apply` runs, then output
  contains `[Template T1: INFO <*> <*> <*>] (10x)`. Given indented
  JSON, when `JsonMinifier.Apply` runs, then output is compact and ≤
  original length.
- **S9 (CCR Retrieve):** Given a CCR store with 3 entries, when
  `retrieve_compacted` is invoked with each hash, then result contains
  `<<ccr_retrieved:...>>` header + original bytes. Missing hash
  returns structured error message.
- **S10a (Config + Migration):** Given old-shape `config.json`, when
  `MigrateLegacyConfig` runs, then new-shape `CompactorConfig` is
  built and a deprecation warning is logged exactly once.
- **S10b (Pipeline Integration + New Metrics):** Given a tool result
  with `bash` command output, when `BuildCompactorCallback` runs, then
  the result is compacted via pipeline and a `CompactRecord` with
  `ContentType`, `Stages`, `Strategies`, `BytesSaved` is recorded.
- **S11 (TUI /rtk stats):** Given 3 CompactRecords spanning
  tools/content types/stages, when `/rtk stats` runs, then output
  shows all 4 sections (Total, By Content Type, By Stage, By Strategy,
  By Tool) with alphabetical ordering.

### Cross-slice

- `go build ./...` passes.
- `go test ./...` passes.
- `go vet ./internal/tools/...` passes.
- All 12 slice gates pass (see Gates section).
- Each slice's `git diff` is non-empty (vacuous-pass protection).

## Implementation Slices

Execute **serially** in a single branch. Each slice is one commit.
Do **not** use worktrees.

1. **S1: CCR + ContentType** — `ccr.go`, `ccr_test.go`, `content_type.go`,
   `content_type_test.go`. Gate:
   `go test ./internal/tools/... -run "CCR|ContentType" -v`
2. **S2: Traits + Orchestrator** — `transform.go`, `transform_test.go`,
   `orchestrator.go`, `orchestrator_test.go`. Gate:
   `go test ./internal/tools/... -run "Orchestrator|Pipeline|Transform" -v`
3. **S3: Signals + AdaptiveSizer** — `signals.go`, `signals_test.go`,
   `adaptive_sizer.go`, `adaptive_sizer_test.go`. Gate:
   `go test ./internal/tools/... -run "Keyword|Adaptive|Signal|Sizer" -v`
4. **S4: LogCompressor** — `log_compressor.go`, `log_compressor_test.go`
   (parity tests against 20 fixtures). Gate:
   `go test ./internal/tools/... -run "LogCompressor" -v`
5. **S5: DiffCompressor + DiffNoise** — `diff_compressor.go`,
   `diff_compressor_test.go` (27 parity), `diff_noise.go`,
   `diff_noise_test.go`. Gate:
   `go test ./internal/tools/... -run "Diff" -v`
6. **S6: SearchCompressor** — `search_compressor.go`,
   `search_compressor_test.go`. Gate:
   `go test ./internal/tools/... -run "SearchCompressor" -v`
7. **S7: SmartCrusher** — `smart_crusher.go`,
   `smart_crusher_test.go` (17 parity). **Largest slice; budget
   extra time.** Gate:
   `go test ./internal/tools/... -run "SmartCrusher" -v`
8. **S8: LogTemplate + JsonMinifier** — `log_template.go`,
   `log_template_test.go`, `json_minifier.go`,
   `json_minifier_test.go`. Gate:
   `go test ./internal/tools/... -run "LogTemplate|JsonMinifier" -v`
9. **S9: CCR Retrieve Tool** — `ccr_retrieve.go`,
   `ccr_retrieve_test.go`, modify `registry.go` to include
   `retrieve_compacted`. Gate:
   `go test ./internal/tools/... -run "Retrieve|CCR" -v`
10. **S10a: Config Restructure + Migration** — modify `compactor.go`
    (nested `CompactorConfig`), add `MigrateLegacyConfig`, modify
    `internal/config/config.go` to mirror new shape. **No behavior
    change.** Gate:
    `go test ./internal/tools/... -run "Config|Migration" -v && go test ./internal/config/...`
11. **S10b: Pipeline Integration + New Metrics** — modify
    `compactor.go` (`BuildCompactorCallback` uses pipeline, signature
    gains `store CCRStore`), modify `compactor_metrics.go` (new
    fields, new `FormatStats`), add
    `compactor_metrics_test.go`, modify `internal/cli/cli.go` and
    `internal/cli/interactive.go` to pass `nil` store. Gate:
    `go test ./internal/tools/... -v && go test ./internal/cli/...`
12. **S11: TUI /rtk stats** — modify `internal/tui/commands_test.go`
    to assert new 4-section format (commands.go itself needs no change
    if FormatStats already renders new layout). Gate:
    `go test ./internal/tui/... -run "RTK" -v`

## Slice-Gate Guard (run after each slice)

```bash
# 1. Work landed (non-empty diff)
git diff --stat HEAD~1 | grep -E "internal/" | wc -l  # must be ≥ 1

# 2. New test functions exist for this slice
grep -c "^func Test<SliceName>" internal/tools/<file>_test.go  # must be ≥ 1

# 3. Slice-specific gate passes
go test ./internal/tools/... -run "<slice-regex>"  # PASS

# 4. No regression
go build ./...  # PASS
```

If any check fails, the slice is incomplete — fix and re-verify before
moving on.

## Gates

- **build**: `go build ./...`
- **test**: `go test ./...`
- **tools-test**: `go test ./internal/tools/... -v`
- **vet**: `go vet ./internal/tools/...`
- **slice-gates**: one per slice, see Implementation Slices above
- **diff-check**: `git diff --stat HEAD~1 | grep -c "internal/"` ≥ 1

## Reference

- Design: `specs/features/TOO/006-specs-tools-002-specs-tools-001-headroom-update/design.md`
- Outline: `specs/features/TOO/006-specs-tools-002-specs-tools-001-headroom-update/outline.md`
- Plan: `specs/features/TOO/006-specs-tools-002-specs-tools-001-headroom-update/plan.md`
- Requirements: `specs/features/TOO/006-specs-tools-002-specs-tools-001-headroom-update/requirements.md`
- Research:
    - `research/pi-go-current-compactor.md`
    - `research/headroom-architecture.md`
    - `research/parity-fixtures.md`
- Previous spec (reuse design/research, ignore outcome):
    - `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/`
- Headroom source: `tmp/headroom/crates/headroom-core/src/` (Rust
  reference implementation)
- Parity fixtures: `internal/tools/testdata/parity/{content_detector,
  diff_compressor,log_compressor,smart_crusher}/` (85 JSON files,
  byte-identical to upstream)
- Existing ADK tool pattern: `internal/tools/registry.go:170` (`newTool`
  helper — use this instead of calling `functiontool.New` directly)

## Constraints

- **No new external Go dependencies** — stdlib only.
- **SHA-256, not BLAKE3**, for CCR keys. 24-hex-char prefix. Documented
  in code comments. No parity impact (CCR keys are not stored in
  fixtures).
- **`strings.Contains`, not aho-corasick**, for keyword detection. Word-
  boundary post-filter to avoid false positives (`panicker` ≠ `panic`).
- **Stdlib zlib** (`compress/flate`) for ratio validation.
- **Each slice compiles + tests independently** — no slice's source
  imports from a future slice.
- **In-memory CCR store only** for v1 — SQLite and Redis backends are
  out of scope.
- **`SearchOffload` is NOT in the default pipeline** (matches headroom).
  `SearchCompressor` is registered only as a `ReformatTransform` in
  `BuildCompactorCallback`.
- **`EstimateBloat` is a method on `OffloadTransform`**, not a free
  function.
- **`MinLinesForCCR` is a misnomer** (matches headroom's source
  comment) — it gates the entire compression path, not just the CCR
  marker. Below threshold, input is returned unchanged.
- **SmartCrusher is the single largest slice** — budget 2–3× the time
  of other algorithm slices. ~1,000 LOC of Go.
- **`MigrateLegacyConfig` is idempotent** — calling twice on
  already-migrated config is a no-op. Use `sync.Once` to guard the
  deprecation log line.
- **CCR metadata** is stored via the extended `PutWithMeta(hash,
  payload, meta)` method. `Put(hash, payload)` delegates with empty
  metadata.
- **The `retrieve_compacted` tool uses the existing `newTool` helper
  at `registry.go:170`**, not `functiontool.New` directly (the helper
  provides lenient schema + coercion + alias resolution).
- **`CoreTools` signature gains `store CCRStore` parameter** in slice 9.
  Update both call sites (`cli.go` + `interactive.go`) accordingly.