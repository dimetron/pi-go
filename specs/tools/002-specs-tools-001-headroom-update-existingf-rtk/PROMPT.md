# Headroom-Style RTK Compactor Overhaul

## Objective

Port headroom's 6-algorithm context compression pipeline into pi-go's existing RTK compactor (`internal/tools/`),
replacing the flat serial stage list with a two-trait (Reformat + Offload) bloat-gated orchestrator, adaptive sizing (
Kneedle), line importance detection, CCR reversibility, and content-type-based routing — with byte-for-byte parity
against headroom's test fixtures.

## Key Requirements

1. **Two-trait pipeline** — `ReformatTransform` (lossless) + `OffloadTransform` (CCR-backed, bloat-gated) interfaces
   with parallel goroutine execution (reformat + bloat estimation concurrent).
2. **Adaptive sizing** — `ComputeOptimalK` via Kneedle algorithm on bigram-coverage curves + zlib-ratio validation,
   replacing fixed limits.
3. **Line importance** — `KeywordDetector` with per-context (Text/Search/Diff/Log) keyword sets matching headroom's
   `KeywordRegistry::default_set()` exactly. Error=0.95, Security=0.85, Warning=0.75, Importance=0.6, Markdown=0.45.
4. **CCR store** — In-memory `CCRStore` (SHA-256 → 24 hex chars, `<<ccr:HASH>>` markers). Per-session, lost on restart.
   LLM retrieves via `retrieve_compacted` tool.
5. **6 algorithms** — LogCompressor, DiffCompressor+DiffNoise, SearchCompressor, SmartCrusher, LogTemplate,
   JsonMinifier.
6. **Content-type detection** — Regex-based `ContentType` (PlainText, BuildOutput, GitDiff, SearchResults, JsonArray,
   SourceCode, HTML), routing independent of tool name.
7. **Byte-for-byte parity** — Go tests match headroom's `tests/parity/fixtures/` JSON files. Copy fixtures into
   `internal/tools/testdata/parity/`.
8. **Breaking config** — Restructured `CompactorConfig` (nested per-algorithm + bloat configs), new `/rtk stats` format
   with per-stage/content-type/strategy breakdown.
9. **Each stage independently mergeable** — compiles + passes tests standalone, maximum parallelism for subagents.
10. **No new external Go deps** — stdlib only (`crypto/sha256`, `compress/flate`, `encoding/json`, `regexp`). No
    aho-corasick; use `strings.Contains`.

## Acceptance Criteria

### Log Compression

- Given a 10,000-line pytest log with 5 errors and 3 stack traces, when LogCompressor runs, then output ≤ 50 lines
  preserving all errors, stack traces, and summary lines.
- Given npm output with repeated "added N packages" lines, when LogTemplate runs, then lines collapsed into
  `[Template Tn: ...] (Nx)` block, every line reconstructible.

### Diff Compression

- Given a 200-line diff with 3 change lines, when DiffOffload runs, then context trimmed to 2 lines around changes,
  `<<ccr:HASH>>` marker emitted, original in CCR store.
- Given a Cargo.lock diff hunk, when DiffNoise runs, then hunk dropped, original stashed, marker emitted.

### Search Compression

- Given grep output with 100 matches across 5 files, when SearchCompressor runs with group_by_file, then output grouped
  by file header, adaptive total via Kneedle, top-scored matches kept.

### JSON Compression

- Given JSON array of 100 dicts from kubectl, when SmartCrusher runs, then schema dedup applied, rare-status preserved,
  adaptive item count.

### CCR Retrieval

- Given any compacted output with `<<ccr:HASH>>`, when LLM calls `retrieve_compacted` with hash, then original bytes
  returned from in-memory store.

### Parity

- Given headroom's parity fixture JSON, when Go algorithm runs with same config on same input, then output matches
  expected field byte-for-byte.

## Implementation Slices

1. **CCR + Content Detection** — `ccr.go` (store, key, marker), `content_type.go` (regex detection), verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "CCR|ContentType"`
2. **Traits + Orchestrator** — `transform.go` (interfaces), `orchestrator.go` (parallel pipeline), verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "Orchestrator|Pipeline"`
3. **Signals + Adaptive Sizer** — `signals.go` (keyword detector), `adaptive_sizer.go` (Kneedle), verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "Keyword|Adaptive"`
4. **LogCompressor** — `log_compressor.go` + parity fixtures, verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "LogCompressor"`
5. **DiffCompressor + DiffNoise** — `diff_compressor.go`, `diff_noise.go` + parity fixtures, verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "Diff"`
6. **SearchCompressor** — `search_compressor.go`, verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "SearchCompressor"`
7. **SmartCrusher** — `smart_crusher.go` + parity fixtures, verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "SmartCrusher"`
8. **LogTemplate + JsonMinifier** — `log_template.go`, `json_minifier.go`, verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "LogTemplate|JsonMinifier"`
9. **CCR Retrieve Tool** — `ccr_retrieve.go` (ADK tool), verify:
   `go build ./internal/tools/... && go test ./internal/tools/... -run "Retrieve"`
10. **Config + Callback + Metrics** — restructure `CompactorConfig`, rewire `BuildCompactorCallback`, update
    `FormatStats`, update config/cli wiring, verify:
    `go build ./... && go test ./internal/tools/... && go test ./internal/config/...`
11. **TUI /rtk stats** — update test expectations for new stats format, verify:
    `go build ./... && go test ./internal/tui/... -run "RTK"`

## Gates

- **build**: `go build ./internal/tools/...`
- **test**: `go test ./internal/tools/... -v`
- **full-build**: `go build ./...`
- **full-test**: `go test ./...`
- **vet**: `go vet ./internal/tools/...`

## Reference

- Design: `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/design.md`
- Outline: `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/outline.md`
- Plan: `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/plan.md`
- Requirements: `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/requirements.md`
- Research: `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/research/`
    - `headroom-architecture.md` — full analysis of headroom's 6 algorithms, pipeline, CCR, bloat heuristics
    - `pi-go-current-compactor.md` — existing implementation files, config, test patterns
- Headroom source: `tmp/headroom/crates/headroom-core/src/` (Rust reference implementation)
- Parity fixtures: `tmp/headroom/tests/parity/fixtures/{log_compressor,diff_compressor,smart_crusher,content_detector}/`

## Constraints

- No new external Go dependencies — stdlib only (`crypto/sha256`, `compress/flate`, `encoding/json`, `regexp`, `sync`).
- Use SHA-256 (not BLAKE3) for CCR keys — documented deviation, hash algorithm is internal.
- Use `strings.Contains` (not aho-corasick) for keyword detection — stdlib only.
- All slices in package `internal/tools` — "independent" means each slice's files compile/test without other slices'
  files being required.
- Slices 1–9 are parallelizable across subagents. Slice 10 integrates (requires 1–9 merged). Slice 11 requires 10.
- Copy parity fixtures:
  `cp tmp/headroom/tests/parity/fixtures/{log_compressor,diff_compressor,smart_crusher,content_detector}/*.json internal/tools/testdata/parity/...`
- Breaking config change is allowed — old configs need migration, document in changelog.
- Reference headroom source for exact algorithm behavior: `tmp/headroom/crates/headroom-core/src/transforms/` and
  `tmp/headroom/crates/headroom-core/src/signals/`.