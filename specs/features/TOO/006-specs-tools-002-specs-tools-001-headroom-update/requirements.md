# Requirements

## Context

This spec is a redo of `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/`.
The previous attempt finished with all gates passing but **zero implementation
landed in the tree** — see that spec's `SUMMARY.md` for the full failure analysis.
The design, research, and plan from that prior attempt are sound and largely
reusable; this spec locks in decisions that the previous one left implicit, and
makes structural changes to address the failure mode (subagent work not reaching
the tree).

## Questions & Answers

### Q1: Serial vs parallel (worktree) implementation path?

**A (B):** Serial implementation in a single branch, one slice at a time. Each
slice must produce a runnable artifact and pass `go build` plus the
slice-specific `-run` regex. Subagents are allowed per-slice, but each subagent
must edit the current branch directly (not a worktree) and must leave evidence
(git diff) showing its work landed. Failure mode last time was silent worktree
discard; this eliminates that failure mode entirely.

### Q2: Migration of existing `config.json` files when `CompactorConfig` restructures?

**A (A):** Auto-migrate on load. If old-shape config is detected (presence of
flat booleans like `strip_ansi`, `aggregate_test_output`, integer `Max*`
fields, etc.), transform to the new nested shape in memory and emit a
deprecation warning to the log. Old config files keep working without
editing; the breaking nature of the change is communicated through the
deprecation warning rather than refused startup.

### Q3: Disposition of the 85 orphaned parity fixture JSONs in `internal/tools/testdata/parity/`?

**A (A):** Wire them all into Go tests. Each `*_test.go` file for the
corresponding algorithm loads the fixture directory and asserts byte-for-byte
parity on the expected field. Required by Q6 from the previous round (full
byte-for-byte parity with headroom's Rust/Python output, including parity
fixtures). The "tests tied to frozen fixtures" risk is acceptable because
parity fixtures are by definition stable snapshots; regenerating from upstream
is a deliberate act.

### Q4: CCR retrieve tool — API shape and return format?

**A (single-hash + format ii):** One tool `retrieve_compacted(hash string)
string`. Returns the raw original bytes prefixed with a one-line metadata
header of the form:

```
<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>
<raw original bytes>
```

Algorithm is the name of the offload transform that produced the CCR
(`LogCompressor`, `DiffCompressor`, `SearchCompressor`, `SmartCrusher`,
`DiffNoise`, etc.). `CONTENT_TYPE` is the detected `ContentType` enum value.
`ORIG_SIZE` and `COMP_SIZE` are byte counts before/after compaction. No bulk
or list variants.

### Q5: Slice structure — monolithic integration vs split?

**A (a):** Split slice 10 into two:

- **Slice 10a**: Restructure `CompactorConfig` in `internal/tools/compactor.go`,
  mirror in `internal/config/config.go`. Add auto-migration logic (per Q2).
  **No behavior change yet** — the existing flat stage list still runs. Gate:
  `go build ./... && go test ./internal/tools/... -run "Config|Migration"`.
- **Slice 10b**: Wire `CompressionPipeline` (from slice 2) + all transforms
  (slices 3–8) into `BuildCompactorCallback`. Add new `CompactRecord` fields
  for stage / content-type / strategy. Update `FormatStats`. Gate:
  `go build ./... && go test ./internal/tools/...`.

Slice 11 (TUI `/rtk stats`) is unchanged from the prior plan.

## Consolidated Requirements

### Functional (carry-over from previous spec, locked)

1. **Two-trait pipeline** — `ReformatTransform` (lossless) +
   `OffloadTransform` (CCR-backed, bloat-gated) interfaces with parallel
   goroutine execution (reformat + bloat estimation concurrent).
2. **Bloat estimation** — Each offload carries `EstimateBloat(content) float32`
   (0.0–1.0); orchestrator gates on configurable threshold + reformat fallback
   ratio.
3. **Adaptive sizing** — `ComputeOptimalK` via Kneedle algorithm on
   bigram-coverage curves + zlib-ratio validation, replacing fixed limits.
4. **Line importance detection** — Aho-corasick keyword detector with
   per-context (Text/Search/Diff/Log) keyword sets matching headroom's
   `KeywordRegistry::default_set()` exactly. Error=0.95, Security=0.85,
   Warning=0.75, Importance=0.6, Markdown=0.45. Implemented via
   `strings.Contains` (no aho-corasick dep).
5. **CCR store** — In-memory `CCRStore` (SHA-256 → 24 hex chars,
   `<<ccr:HASH>>` markers). Per-session, lost on restart.
6. **Retrieval tool** — `retrieve_compacted(hash string) string` ADK tool
   returning raw bytes prefixed by metadata header
   `<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>\n`.
7. **6 algorithms** — LogCompressor, DiffCompressor+DiffNoise, SearchCompressor,
   SmartCrusher, LogTemplate, JsonMinifier.
8. **Content-type detection** — Regex-based `ContentType` enum
   (PlainText, BuildOutput, GitDiff, SearchResults, JsonArray, SourceCode,
   HTML), routing independent of tool name.

### Non-Functional

1. **Byte-for-byte parity** with headroom's Rust/Python output for all
   algorithms. Go tests must load and assert against the fixtures in
   `internal/tools/testdata/parity/{content_detector,diff_compressor,log_compressor,smart_crusher}/`.
2. **Auto-migration** — old-shape `CompactorConfig` is detected and rewritten
   in memory at load time, with a deprecation warning logged. No user action
   required.
3. **Serial execution in a single branch** — slices land one at a time, each
   gated on `go build` + slice-specific test regex. No worktree-based
   parallelism. Subagents per slice must commit to the current branch and
   leave a non-empty git diff.
4. **No new external Go deps** — stdlib only (`crypto/sha256`, `compress/flate`,
   `encoding/json`, `regexp`, `sync`).

### Acceptance Criteria (carry-over from previous spec, locked)

- Given a 10,000-line pytest log with 5 errors and 3 stack traces, when
  LogCompressor runs, then output ≤ 50 lines preserving all errors, stack
  traces, and summary lines.
- Given npm output with repeated "added N packages" lines, when LogTemplate
  runs, then lines collapsed into `[Template Tn: ...] (Nx)` block, every line
  reconstructible.
- Given a 200-line diff with 3 change lines, when DiffOffload runs, then
  context trimmed to 2 lines around changes, `<<ccr:HASH>>` marker emitted,
  original in CCR store.
- Given a Cargo.lock diff hunk, when DiffNoise runs, then hunk dropped,
  original stashed, marker emitted.
- Given grep output with 100 matches across 5 files, when SearchCompressor
  runs with group_by_file, then output grouped by file header, adaptive total
  via Kneedle, top-scored matches kept.
- Given JSON array of 100 dicts from kubectl, when SmartCrusher runs, then
  schema dedup applied, rare-status preserved, adaptive item count.
- Given any compacted output with `<<ccr:HASH>>`, when LLM calls
  `retrieve_compacted` with hash, then raw original bytes returned prefixed by
  `<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>` header.
- Given an old-shape `config.json` (with `strip_ansi`, `MaxChars`, etc.),
  when the app starts, then new-shape config is constructed in memory, a
  deprecation warning is logged, and behavior is equivalent to the
  pre-restructure flat pipeline.
- Given any of the 85 parity fixtures in `internal/tools/testdata/parity/`,
  when the corresponding Go algorithm runs with the same config and input,
  then the output matches the expected field byte-for-byte.

### Slice gate additions (new, from Q1)

Every slice must additionally leave a non-empty `git diff` after its work, and
the gate command must reference at least one test function or file that
specifically exercises the slice's new code (not just any pre-existing test).
Gates that pass vacuously because the slice's tests don't exist are forbidden.