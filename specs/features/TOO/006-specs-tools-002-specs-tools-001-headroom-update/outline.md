# Outline — Headroom-Style RTK Compactor Overhaul (Redo)

## Goal

Replace pi-go's flat serial RTK compactor with a headroom-style two-trait
pipeline + CCR store + 6 algorithms + content-type routing, executed
**serially** (12 slices) to address the failure mode of the previous attempt
(silent subagent worktree discard).

## Stage Map (12 slices, sequential, each compiles + tests independently)

```
S1  CCR + Content Detection          ─┐
S2  Transform Traits + Orchestrator  ─┤
S3  Signals + Adaptive Sizer         ─┤
S4  LogCompressor                    ─┤
S5  DiffCompressor + DiffNoise       ─┼─→ S10a → S10b → S11
S6  SearchCompressor                 ─┤
S7  SmartCrusher                     ─┤
S8  LogTemplate + JsonMinifier       ─┤
S9  CCR Retrieve Tool                ─┘
```

Stages 1–9 are **independent** — each slice's files compile and pass
tests without any other slice's files. S10a integrates config (no
behavior change). S10b wires everything into the existing
`BuildCompactorCallback`. S11 updates TUI.

## Per-Slice Plan (12 slices)

### Slice 1 — CCR Store + Content Detection

**Files:**

- `internal/tools/ccr.go` — `CCRStore` interface, `InMemoryCCRStore`,
  `ComputeCCRKey`, `CCRMarker`, `ccrEntry` struct
- `internal/tools/ccr_test.go` — put/get/Len, eviction, expiry,
  SHA-256[:24] determinism
- `internal/tools/content_type.go` — `ContentType` enum,
  `DetectionResult`, `DetectContentType`, `DetectFromToolName`
- `internal/tools/content_type_test.go` — parity tests against
  `testdata/parity/content_detector/*.json` (21 fixtures)

**Gate:** `go test ./internal/tools/... -run "CCR|ContentType"` passes;
`git diff` non-empty.

### Slice 2 — Transform Traits + Orchestrator

**Files:**

- `internal/tools/transform.go` — `ReformatTransform`, `OffloadTransform`,
  `ReformatOutput`, `OffloadOutput`, `CompressionContext`,
  `TransformError`, `TransformErrorKind`
- `internal/tools/orchestrator.go` — `CompressionPipeline`,
  `CompressionPipelineBuilder`, `OrchestratorConfig`, `PipelineResult`,
  `Run` (parallel reformat + bloat, gated offloads)
- `internal/tools/orchestrator_test.go` — stub transforms exercising
  ordering, gating, parallel execution, error skip

**Gate:** `go test ./internal/tools/... -run "Orchestrator|Pipeline|Transform"` passes;
`git diff` non-empty.

### Slice 3 — Line Importance Detection + Adaptive Sizer

**Files:**

- `internal/tools/signals.go` — `ImportanceContext`, `ImportanceCategory`,
  `ImportanceSignal`, `LineImportanceDetector` interface, `KeywordRegistry`,
  `KeywordDetector`, `DefaultKeywordRegistry` (exact headroom content),
  word-boundary filter, per-context activation rules
- `internal/tools/signals_test.go` — keyword matching, priorities,
  word-boundary edge cases, per-context activation
- `internal/tools/adaptive_sizer.go` — `ComputeOptimalK`, `FindKnee`,
  `ComputeUniqueBigramCurve`, `Simhash`, `HammingDistance`,
  `CountUniqueSimhash`, `ValidateWithZlib` (uses `compress/flate`)
- `internal/tools/adaptive_sizer_test.go` — fast path, Kneedle curves,
  zlib-ratio validation, Simhash determinism

**Gate:** `go test ./internal/tools/... -run "Keyword|Adaptive|Signal|Sizer"` passes;
`git diff` non-empty.

### Slice 4 — LogCompressor

**Files:**

- `internal/tools/log_compressor.go` — `LogFormat`, `LogLevel`,
  `LogLine`, `LogCompressorConfig`, `LogCompressionResult`,
  `LogCompressorStats`, `LogCompressor`, `NewLogCompressor`,
  `Compress`, `CompressWithStore`, format detection (pytest/npm/cargo/
  jest/make/generic), per-line classification, scoring,
  `ComputeOptimalK` integration, CCR marker emission
- `internal/tools/log_compressor_test.go` — parity tests against
  `testdata/parity/log_compressor/*.json` (20 fixtures)

**Gate:** `go test ./internal/tools/... -run "LogCompressor"` passes
all 20 fixtures; `git diff` non-empty.

### Slice 5 — DiffCompressor + DiffNoise

**Files:**

- `internal/tools/diff_compressor.go` — `DiffCompressorConfig`,
  `DiffCompressionResult`, `DiffCompressor`, unified-diff parsing,
  hunk scoring, file/hunk caps, context trim, CCR marker
- `internal/tools/diff_compressor_test.go` — parity tests against
  `testdata/parity/diff_compressor/*.json` (27 fixtures)
- `internal/tools/diff_noise.go` — `DiffNoiseConfig`,
  `DiffNoise`, lockfile detection, whitespace-only hunk drop,
  CCR marker
- `internal/tools/diff_noise_test.go` — table-driven lockfile +
  whitespace scenarios

**Gate:** `go test ./internal/tools/... -run "Diff"` passes
all 27 parity fixtures + DiffNoise table tests; `git diff` non-empty.

### Slice 6 — SearchCompressor

**Files:**

- `internal/tools/search_compressor.go` — `SearchMatch`,
  `FileMatches`, `SearchCompressorConfig`,
  `SearchCompressionResult`, `SearchCompressorStats`,
  `SearchCompressor`, `NewSearchCompressor`, `WithDetector`,
  `Compress`, `CompressWithStore`, parse → score → select → format
- `internal/tools/search_compressor_test.go` — table-driven: 100 matches
  / 5 files, cluster scoring, group-by-file, adaptive total

**Gate:** `go test ./internal/tools/... -run "SearchCompressor"` passes;
`git diff` non-empty.

### Slice 7 — SmartCrusher

**Files:**

- `internal/tools/smart_crusher.go` — `SmartCrusherConfig`,
  `CrushResult`, `SmartCrusher`, 4 `CompressionStrategy` enums,
  `ArrayType` classifier, schema dedup, anchor selection,
  adaptive item count, outlier preservation, CCR marker format
  `{"_ccr_dropped":"<<ccr:HASH N_rows_offloaded>>"}`
- `internal/tools/smart_crusher_test.go` — parity tests against
  `testdata/parity/smart_crusher/*.json` (17 fixtures)

**Gate:** `go test ./internal/tools/... -run "SmartCrusher"` passes
all 17 fixtures; `git diff` non-empty.

**Note:** This is the biggest slice. Budget 2–3× the time of other
algorithm slices.

### Slice 8 — LogTemplate + JsonMinifier (Reformats)

**Files:**

- `internal/tools/log_template.go` — `LogTemplateConfig`,
  `LogTemplate`, Drain-inspired template miner, wildcard `<*>`,
  `[Template Tn: ...] (Nx)` output
- `internal/tools/log_template_test.go` — table-driven: N consecutive
  same-template lines collapse; variant table correctness;
  lossless reconstruction
- `internal/tools/json_minifier.go` — `JsonMinifierConfig`,
  `JsonMinifier`, `encoding/json` round-trip, no-inflate guarantee
- `internal/tools/json_minifier_test.go` — table-driven: minify,
  already-minified passthrough, malformed JSON graceful failure

**Gate:** `go test ./internal/tools/... -run "LogTemplate|JsonMinifier"` passes;
`git diff` non-empty.

### Slice 9 — CCR Retrieval Tool

**Files:**

- `internal/tools/ccr_retrieve.go` — `NewRetrieveTool(store CCRStore)
  (tool.Tool, error)`, ADK tool registration, JSON Schema for `hash`
  arg, return format
  `<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>\n<bytes>`
- `internal/tools/ccr_retrieve_test.go` — 3-entry store (LogCompressor,
  DiffCompressor, SearchCompressor), retrieve each, missing-hash
  structured error, metadata-header format

**Gate:** `go test ./internal/tools/... -run "Retrieve"` passes;
`git diff` non-empty.

### Slice 10a — Config Restructure + Migration

**Files:**

- `internal/tools/compactor.go` — restructured `CompactorConfig`
  (nested per-algorithm + bloat + pipeline configs), `PipelineConfig`,
  `BloatConfigs`, `AlgorithmsConfig`, `LimitsConfig`,
  `DefaultCompactorConfig`, **NEW** `MigrateLegacyConfig` (idempotent,
  logs via package-level `var warnLog = log.Default()`), **NEW**
  `BuildCompactorCallback` (no behavior change yet — still calls
  `compactToolResult` if pipeline returns no steps)
- `internal/tools/compactor_test.go` — `TestMigrateLegacyConfig_AllOldFields`,
  `TestMigrateLegacyConfig_NewShape_PassThrough`,
  `TestMigrateLegacyConfig_Idempotent`,
  `TestMigrateLegacyConfig_DeprecationLogged`

**Gate:** `go test ./internal/tools/... -run "Config|Migration"` passes;
`git build ./...` passes; `git diff` non-empty.

**Behavior:** Old-shape config is auto-migrated in memory at load.
New-shape config passes through unchanged. Deprecation warning logged
exactly once per process via `sync.Once` guard.

### Slice 10b — Pipeline Integration + New Metrics

**Files:**

- `internal/tools/compactor.go` — `BuildCompactorCallback` rewires to
  use `CompressionPipeline.Run` for content-type-routed compaction.
  Detects `ContentType` from content (not tool name). Falls back to
  `compactToolResult` for tools where pipeline returns no steps
  applied (preserves current behavior for unmigrated code paths).
- `internal/tools/compactor_metrics.go` — new `CompactRecord` fields
  (`ContentType`, `Stages`, `Strategies`, `BytesSaved`); new
  `CompactSummary` fields (`ByContentType`, `ByStage`, `ByStrategy`);
  new `FormatStats` with 4-section breakdown; deterministic
  alphabetical key ordering
- `internal/tools/compactor_metrics_test.go` — `TestFormatStats_*`
  covering each breakdown section, deterministic ordering, empty
  case

**Gate:** `go build ./... && go test ./internal/tools/... && go test ./internal/config/...` passes;
`git diff` non-empty.

### Slice 11 — TUI `/rtk stats` Update

**Files:**

- `internal/tui/commands.go` — `handleRTKCommand` already calls
  `m.cfg.CompactMetrics.FormatStats()`; no code change needed **if**
  `FormatStats` correctly renders the new 4-section layout. Verify.
- `internal/tui/commands_test.go` — `TestHandleRTKCommand_RTKStats`
  updated to assert new 4-section output
- `internal/tui/commands_test.go` — `TestHandleRTKCommand_RTKStatsEmpty`
  unchanged

**Gate:** `go test ./internal/tui/... -run "RTK"` passes;
`git diff` non-empty.

## Order & Dependencies

```
S1 ─→ S2 ─→ S3 ─→ S4 ─→ S5 ─→ S6 ─→ S7 ─→ S8 ─→ S9
                                              ↓
                                            S10a
                                              ↓
                                            S10b
                                              ↓
                                            S11
```

S1–S9 are **parallelizable in principle** (each compiles + tests
without the others), but **executed serially** by this redo to avoid
the silent-discard failure mode. S10a requires all of S1–S9's
algorithms exist as types (it imports them for the migration's
`AlgorithmsConfig` defaults), but does not require their behavior
be correct. S10b requires all of S1–S9 to function correctly
(end-to-end pipeline test). S11 requires S10b's new `FormatStats`.

## Key Type Signatures (new interfaces — Go)

```go
// Slice 1
type CCRStore interface {
    Put(hash, payload string)
    Get(hash string) (string, bool)
    Len() int
}
type InMemoryCCRStore struct{ ... }
func NewInMemoryCCRStore() *InMemoryCCRStore
func ComputeCCRKey(payload []byte) string
func CCRMarker(hash string) string

type ContentType int
type DetectionResult struct {
    Type       ContentType
    Confidence float64
    Metadata   map[string]any
}
func DetectContentType(content string) DetectionResult
func DetectFromToolName(toolName string) ContentType

// Slice 2
type ReformatTransform interface {
    Name() string
    AppliesTo() []ContentType
    Apply(content string) (ReformatOutput, error)
}
type ReformatOutput struct {
    Output     string
    BytesSaved int
}
type OffloadTransform interface {
    Name() string
    AppliesTo() []ContentType
    EstimateBloat(content string) float32
    Apply(content string, ctx CompressionContext, store CCRStore) (OffloadOutput, error)
    Confidence() float32
}
type OffloadOutput struct {
    Output     string
    BytesSaved int
    CacheKey   string
}
type CompressionContext struct {
    Query       string
    TokenBudget int
}
type TransformError struct {
    Transform string
    Kind      TransformErrorKind
    Message   string
}
type TransformErrorKind int  // InvalidInput, Skipped, Internal

type OrchestratorConfig struct {
    ReformatTargetRatio  float64
    BloatThreshold       float32
    OffloadFallbackRatio float64
}
type PipelineResult struct {
    Output       string
    BytesSaved   int
    StepsApplied []string
    CacheKeys    []string
}
type CompressionPipeline struct{ ... }
func NewCompressionPipelineBuilder() *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) WithReformat(t ReformatTransform) *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) WithOffload(t OffloadTransform) *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) WithConfig(c OrchestratorConfig) *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) Build(store CCRStore) *CompressionPipeline
func (p *CompressionPipeline) Run(content string, ct ContentType, ctx CompressionContext) PipelineResult

// Slice 3
type ImportanceContext int  // Text, Search, Diff, Log
type ImportanceCategory int  // Error, Warning, Security, Importance, Markdown
type ImportanceSignal struct {
    Category   *ImportanceCategory
    Priority   float32
    Confidence float32
}
type LineImportanceDetector interface {
    Score(line string, ctx ImportanceContext) ImportanceSignal
}
type KeywordRegistry struct {
    Error            []string
    Warning          []string
    Importance       []string
    Security         []string
    MarkdownPrefixes []string
    ErrorIndicators  []string
}
func DefaultKeywordRegistry() KeywordRegistry
type KeywordDetector struct{ ... }
func NewKeywordDetector() *KeywordDetector

func ComputeOptimalK(items []string, bias float64, minK, maxK int) int
func FindKnee(curve []int) (int, bool)
func ComputeUniqueBigramCurve(items []string) []int
func Simhash(text string) uint64
func HammingDistance(a, b uint64) uint32
func CountUniqueSimhash(items []string, threshold uint32) int
func ValidateWithZlib(items []string, k, maxK int, tolerance float64) int

// Slice 4
type LogFormat int  // Pytest, Npm, Cargo, Jest, Make, Generic
type LogLevel int   // Error, Fail, Warn, Info, Debug, Trace, Unknown
type LogCompressorConfig struct{ ... }
type LogCompressionResult struct{ ... }
type LogCompressorStats struct{ ... }
type LogCompressor struct{ ... }
func NewLogCompressor(cfg LogCompressorConfig) *LogCompressor
func (c *LogCompressor) Compress(content string, bias float64) (LogCompressionResult, LogCompressorStats, error)
func (c *LogCompressor) CompressWithStore(content string, bias float64, store CCRStore) (LogCompressionResult, LogCompressorStats, error)

// Slice 5
type DiffCompressorConfig struct{ ... }
type DiffCompressionResult struct{ ... }
type DiffCompressor struct{ ... }
func NewDiffCompressor(cfg DiffCompressorConfig) *DiffCompressor
func (c *DiffCompressor) Compress(content, context string) (DiffCompressionResult, error)
func (c *DiffCompressor) CompressWithStore(content, context string, store CCRStore) (DiffCompressionResult, error)

type DiffNoiseConfig struct{ ... }
type DiffNoise struct{ ... }
func NewDiffNoise(cfg DiffNoiseConfig) *DiffNoise

// Slice 6
type SearchCompressorConfig struct{ ... }
type SearchCompressionResult struct{ ... }
type SearchCompressorStats struct{ ... }
type SearchCompressor struct{ ... }
func NewSearchCompressor(cfg SearchCompressorConfig) *SearchCompressor
func (c *SearchCompressor) WithDetector(d *KeywordDetector) *SearchCompressor
func (c *SearchCompressor) Compress(content, context string, bias float64) (SearchCompressionResult, SearchCompressorStats, error)

// Slice 7
type SmartCrusherConfig struct{ ... }
type CrushResult struct{ ... }
type SmartCrusher struct{ ... }
func NewSmartCrusher(cfg SmartCrusherConfig) *SmartCrusher
func (c *SmartCrusher) Crush(content, query string, bias float64) (CrushResult, error)

// Slice 8
type LogTemplateConfig struct{ ... }
type LogTemplate struct{ ... }
func NewLogTemplate(cfg LogTemplateConfig) *LogTemplate

type JsonMinifierConfig struct{ ... }
type JsonMinifier struct{ ... }
func NewJsonMinifier(cfg JsonMinifierConfig) *JsonMinifier

// Slice 9
func NewRetrieveTool(store CCRStore) (tool.Tool, error)

// Slice 10a
type CompactorConfig struct {  // restructured
    Enabled    bool
    Pipeline   PipelineConfig
    Bloat      BloatConfigs
    Reformat   ReformatConfigs
    Offload    OffloadConfigs
    Algorithms AlgorithmsConfig
    Limits     LimitsConfig
}
func DefaultCompactorConfig() CompactorConfig
func MigrateLegacyConfig(raw map[string]any) (CompactorConfig, error)
func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics) llmagent.AfterToolCallback

// Slice 10b — CompactRecord extended
type CompactRecord struct {
    Tool        string
    ContentType string     // NEW
    Stages      []string   // NEW
    Strategies  []string   // NEW
    BytesSaved  int        // NEW
    OrigSize    int
    CompSize    int
    Timestamp   time.Time
}

// Slice 11 — no new types; just updated FormatStats output
```

## Risks & Mitigations

1. **SmartCrusher is the largest slice (~1,000 LOC Go).** Mitigation:
   it's an isolated slice — if blocked, all other slices still ship.
   The implementer should break it into 2–3 sub-commits within the
   slice (parity fixtures first, then advanced features).
2. **`MinLinesForCCR` misnomer preservation** — easy to accidentally
   "fix" during port. Mitigation: explicit doc comment + table test
   asserting below-threshold input is returned unchanged.
3. **CCR marker byte-format** — `<<ccr:1d0dd94cf2cd 85_rows_offloaded>>`
   must match exactly (note the space between hash and count).
   Mitigation: parity fixtures assert this string verbatim.
4. **`EstimateBloat` as method, not free function** — easy to revert to
   previous spec's incorrect shape. Mitigation: design.md §Components
   has the correct interface; review checklist verifies it.
5. **SearchOffload NOT in default pipeline** — easy to add by accident.
   Mitigation: `BuildCompactorCallback` does not call
   `WithOffload(SearchCompressor(...))`; only `WithReformat`.
6. **`MigrateLegacyConfig` must be idempotent** — calling twice on
   already-migrated config is a no-op (no second log line).
   Mitigation: idempotency test asserts no second log line.
7. **`internal/config/config.go` mirror** — user-facing config struct
   must be updated to match `tools.CompactorConfig` shape. Mitigation:
   S10a includes the config.go update in the same commit.

## Slice-Gate Guard (vacuous-pass protection)

For each slice, the implementer MUST verify before committing:

```bash
# 1. Non-empty git diff (work landed)
git diff --stat HEAD~1 | grep -E "internal/tools/" | wc -l  # ≥ 1

# 2. New test functions exist
grep -c "^func Test<SliceName>" internal/tools/<file>_test.go  # ≥ 1

# 3. Slice gate passes
go test ./internal/tools/... -run "<slice-regex>"  # PASS

# 4. No regression in unrelated tests
go build ./...  # PASS
```

If any of these fail, the slice is incomplete and must be reworked
before the next slice begins.