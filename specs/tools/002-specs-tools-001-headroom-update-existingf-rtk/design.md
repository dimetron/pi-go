# Design — Headroom-Style RTK Compactor Overhaul

## Current State

pi-go's compactor (`internal/tools/compactor*.go`) is a flat serial pipeline: `BuildCompactorCallback` routes by tool
name to per-tool functions (`compactBash`, `compactGrep`, etc.), each running a fixed sequence of stages gated by
boolean config flags. All stages run unconditionally when enabled. Limits are fixed (`MaxChars=24000`, `MaxLines=440`).
Output is lossy — truncated bytes are gone permanently. No content-type detection, no per-line scoring, no adaptive
sizing, no CCR reversibility, no JSON/structured compression, no log template mining.

## Desired End State

A headroom-style compression pipeline with:

- Two-trait model: `ReformatTransform` (lossless) + `OffloadTransform` (CCR-backed, bloat-gated)
- Parallel reformat + bloat estimation via goroutines
- Content-type detection (regex-based, independent of tool name)
- Adaptive sizing via Kneedle algorithm
- Line importance detection (keyword-based, per-context)
- CCR in-memory store with retrieval tool
- 6 algorithms: LogCompressor, DiffCompressor, SearchCompressor, SmartCrusher, LogTemplate, JsonMinifier
- Byte-for-byte parity with headroom's Rust/Python output
- Breaking config change, new `/rtk stats` format

## Architecture Overview

```mermaid
graph TB
    A[Tool Result] --> B[ContentDetector]
    B --> C{ContentType}
    C -->|BuildOutput| D[LogPipeline]
    C -->|GitDiff| E[DiffPipeline]
    C -->|SearchResults| F[SearchPipeline]
    C -->|JsonArray| G[JsonPipeline]
    C -->|SourceCode| H[ReadPipeline]
    C -->|PlainText| I[GenericTruncate]

    D --> J[Orchestrator]
    E --> J
    F --> J
    G --> J
    H --> J
    I --> J

    J -->|goroutine| K[Reformat Phase]
    J -->|goroutine| L[Bloat Estimation]
    K --> M{Gate: bloat >= threshold?}
    L --> M
    M -->|yes| N[Offload Phase]
    M -->|no| O[Output]
    N --> P[CCR Store Write]
    P --> O

    O --> Q[Metrics Record]
    Q --> R[/rtk stats]
```

## Components and Interfaces

### Content Type Detection (`internal/tools/content_type.go`)

```go
type ContentType int

const (
    ContentPlainText ContentType = iota
    ContentBuildOutput
    ContentGitDiff
    ContentSearchResults
    ContentJsonArray
    ContentSourceCode
    ContentHTML
)

type DetectionResult struct {
    Type       ContentType
    Confidence float64
    Metadata   map[string]any
}

// DetectContentType examines content (not tool name) to determine type.
// Regex-based, no ML. Compiled-once patterns via sync.OnceValue.
func DetectContentType(content string) DetectionResult

// DetectFromToolName provides a fallback hint from the tool name.
func DetectFromToolName(toolName string) ContentType
```

### Transform Traits (`internal/tools/transform.go`)

```go
// ReformatTransform packs input denser without dropping information.
// No CCR needed — output is semantically equivalent to input.
type ReformatTransform interface {
    Name() string
    AppliesTo() []ContentType
    Apply(content string) (ReformatOutput, error)
}

type ReformatOutput struct {
    Output     string
    BytesSaved int
}

// OffloadTransform drops bytes from wire, stashes original via CCR.
// Each carries EstimateBloat for orchestrator gating.
type OffloadTransform interface {
    Name() string
    AppliesTo() []ContentType
    EstimateBloat(content string) float32
    Apply(content string, ctx CompressionContext, store CCRStore) (OffloadOutput, error)
    Confidence() float32
}

type OffloadOutput struct {
    Output    string
    BytesSaved int
    CacheKey  string
}

type CompressionContext struct {
    Query       string
    TokenBudget int // 0 = no budget signal
}

type TransformError struct {
    Transform string
    Kind      TransformErrorKind // InvalidInput, Skipped, Internal
    Message   string
}
```

### CCR Store (`internal/tools/ccr.go`)

```go
// CCRStore is the in-memory cache for Compress-Cache-Retrieve.
type CCRStore interface {
    Put(hash, payload string)
    Get(hash string) (string, bool)
    Len() int
}

// InMemoryCCRStore is the per-session implementation.
type InMemoryCCRStore struct {
    mu   sync.RWMutex
    data map[string]string
}

func NewInMemoryCCRStore() *InMemoryCCRStore

// ComputeCCRKey returns BLAKE3 → first 24 hex chars.
// Uses crypto/sha256 (BLAKE3 unavailable in stdlib; SHA-256 is the
// stdlib equivalent — 24 hex char prefix of SHA-256 digest).
func ComputeCCRKey(payload []byte) string

// CCRMarker returns the <<ccr:HASH>> retrieval marker.
func CCRMarker(hash string) string
```

> **Note on hashing:** Headroom uses BLAKE3; Go stdlib has no BLAKE3. We use SHA-256 (available in `crypto/sha256`)
> truncated to 24 hex chars. This is a deliberate deviation documented in tests — the hash algorithm is an implementation
> detail, not a parity constraint, since CCR keys are internal.

### Orchestrator (`internal/tools/orchestrator.go`)

```go
type OrchestratorConfig struct {
    ReformatTargetRatio  float64 // 0.5 — skip offloads if reformat ≤ this
    BloatThreshold       float32 // 0.5 — run offload if bloat ≥ this
    OffloadFallbackRatio float64 // 0.85 — run offloads if reformat barely helped
}

type CompressionPipeline struct {
    reformatsByType map[ContentType][]ReformatTransform
    offloadsByType  map[ContentType][]OffloadTransform
    config          OrchestratorConfig
    store           CCRStore
}

type PipelineResult struct {
    Output       string
    BytesSaved   int
    StepsApplied []string
    CacheKeys    []string
}

func NewCompressionPipeline(cfg OrchestratorConfig, store CCRStore) *CompressionPipelineBuilder

func (p *CompressionPipeline) Run(content string, ct ContentType, ctx CompressionContext) PipelineResult
// Run executes: parallel(reformat + bloat estimation) → gated offloads → result
```

### Line Importance Detection (`internal/tools/signals.go`)

```go
type ImportanceContext int

const (
    ImportanceText ImportanceContext = iota
    ImportanceSearch
    ImportanceDiff
    ImportanceLog
)

type ImportanceCategory int

const (
    ImportanceCategoryError ImportanceCategory = iota
    ImportanceCategoryWarning
    ImportanceCategorySecurity
    ImportanceCategoryImportance
    ImportanceCategoryMarkdown
)

type ImportanceSignal struct {
    Category   *ImportanceCategory
    Priority   float32
    Confidence float32
}

type LineImportanceDetector interface {
    Score(line string, ctx ImportanceContext) ImportanceSignal
}

// KeywordDetector uses strings.Contains (or regexp) for keyword matching.
// No aho-corasick dependency — implements the same O(n*m) approach with
// stdlib. Keywords match headroom's KeywordRegistry::default_set exactly.
type KeywordDetector struct {
    errorKeywords     []string
    warningKeywords   []string
    importanceKeywords []string
    securityKeywords  []string
    markdownPrefixes  []string
}

func NewKeywordDetector() *KeywordDetector
func (d *KeywordDetector) Score(line string, ctx ImportanceContext) ImportanceSignal
```

### Adaptive Sizer (`internal/tools/adaptive_sizer.go`)

```go
// ComputeOptimalK decides how many items to keep via information saturation.
// Three-tier: fast path (n≤8) → Kneedle on bigram coverage → zlib validation.
// Uses compress/flate for zlib-ratio validation (stdlib equivalent of flate2).
func ComputeOptimalK(items []string, bias float64, minK int, maxK int) int

// FindKnee finds the knee point in a monotonically-increasing curve.
func FindKnee(curve []int) (int, bool)

// CountUniqueSimhash counts unique items by simhash (MD5 4-gram, 64-bit).
func CountUniqueSimhash(items []string, maxCount int) int
```

### Algorithms (each in its own file, independently testable)

**LogCompressor** (`internal/tools/log_compressor.go`):

```go
type LogFormat int // Pytest, Npm, Cargo, Jest, Make, Generic
type LogLevel int  // Error, Fail, Warn, Info, Debug, Trace, Unknown

type LogLine struct {
    LineNumber    int
    Content       string
    Level         LogLevel
    IsStackTrace  bool
    IsSummary     bool
    Score         float32
}

type LogCompressorConfig struct {
    MaxErrors, ErrorContextLines    int
    KeepFirstError, KeepLastError   bool
    MaxStackTraces, StackTraceMaxLines int
    MaxWarnings                     int
    DedupeWarnings, KeepSummaryLines bool
    MaxTotalLines                   int
    EnableCCR                       bool
    MinLinesForCCR                  int
    MinCompressionRatioForCCR       float64
}

type LogCompressor struct{ cfg LogCompressorConfig }
func (lc *LogCompressor) Compress(content string, store CCRStore) (LogCompressionResult, error)
```

**DiffCompressor** (`internal/tools/diff_compressor.go`):

```go
type DiffCompressorConfig struct {
    MaxContextLines, MaxHunksPerFile, MaxFiles int
    EnableCCR, MinLinesForCCR bool
    MinCompressionRatioForCCR float64
}
type DiffCompressor struct{ cfg DiffCompressorConfig }
func (dc *DiffCompressor) Compress(content string, store CCRStore) (DiffCompressionResult, error)
```

**SearchCompressor** (`internal/tools/search_compressor.go`):

```go
type SearchCompressorConfig struct {
    MaxMatchesPerFile, MaxTotalMatches, MaxFiles int
    AlwaysKeepFirst, AlwaysKeepLast, GroupByFile bool
    ContextKeywords []string
    EnableCCR bool
    MinMatchesForCCR int
    MinCompressionRatioForCCR float64
}
type SearchCompressor struct{ cfg SearchCompressorConfig }
func (sc *SearchCompressor) Compress(content string, ctx CompressionContext, store CCRStore) (SearchCompressionResult, error)
```

**SmartCrusher** (`internal/tools/smart_crusher.go`):

```go
type SmartCrusherConfig struct {
    MinItemsToAnalyze, MinTokensToCrush int
    VarianceThreshold, UniquenessThreshold, SimilarityThreshold float64
    MaxItemsAfterCrush int
    PreserveChangePoints, DedupIdenticalItems bool
    FirstFraction, LastFraction float64
    RelevanceThreshold, LosslessMinSavingsRatio float64
}
type SmartCrusher struct{ cfg SmartCrusherConfig }
func (sc *SmartCrusher) CrushArray(content string, ctx CompressionContext, store CCRStore) (CrushArrayResult, error)
```

**LogTemplate** (`internal/tools/log_template.go`):

```go
type LogTemplateConfig struct {
    MinLines, MinRun int
    SimilarityThreshold float32
    MinConstantTokens int
}
type LogTemplate struct{ cfg LogTemplateConfig }
func (lt *LogTemplate) Apply(content string) (ReformatOutput, error)
```

**JsonMinifier** (`internal/tools/json_minifier.go`):

```go
type JsonMinifier struct{}
func (jm *JsonMinifier) Apply(content string) (ReformatOutput, error)
// encoding/json round-trip: parse → marshal compact
```

**DiffNoise** (`internal/tools/diff_noise.go`):

```go
type DiffNoiseConfig struct {
    MinLines int
    LockfileSuffixes []string
    DropWhitespaceOnlyHunks bool
}
type DiffNoise struct{ cfg DiffNoiseConfig }
func (dn *DiffNoise) EstimateBloat(content string) float32
func (dn *DiffNoise) Apply(content string, ctx CompressionContext, store CCRStore) (OffloadOutput, error)
```

### Retrieval Tool (`internal/tools/ccr_retrieve.go`)

```go
// NewRetrieveTool creates an ADK FunctionTool that lets the LLM
// retrieve CCR-stashed originals by hash.
func NewRetrieveTool(store CCRStore) (tool.Tool, error)
// Tool name: "retrieve_compacted"
// Input: { "hash": "24-hex-char key" }
// Output: { "content": "original bytes" } or { "error": "not found" }
```

### Config (`internal/tools/compactor.go` — restructured)

```go
type CompactorConfig struct {
    Enabled bool `json:"enabled"`

    // Pipeline orchestration
    Pipeline OrchestratorConfig `json:"pipeline"`

    // Per-domain bloat thresholds
    Bloat BloatConfigs `json:"bloat"`

    // Per-algorithm configs
    LogCompressor     LogCompressorConfig     `json:"log_compressor"`
    DiffCompressor    DiffCompressorConfig    `json:"diff_compressor"`
    SearchCompressor  SearchCompressorConfig  `json:"search_compressor"`
    SmartCrusher      SmartCrusherConfig      `json:"smart_crusher"`
    LogTemplate       LogTemplateConfig       `json:"log_template"`
    DiffNoise         DiffNoiseConfig         `json:"diff_noise"`

    // Legacy fields (still used by some stages)
    StripAnsi         bool   `json:"strip_ansi"`
    SourceCodeFiltering string `json:"source_code_filtering"`
    MaxChars          int    `json:"max_chars"`
    MaxLines          int    `json:"max_lines"`
}
```

### Metrics (`internal/tools/compactor_metrics.go` — enhanced)

```go
type CompactRecord struct {
    Tool        string
    ContentType string
    StepsApplied []string
    OrigSize, CompSize int
    CacheKeys   []string
    Timestamp   time.Time
}

// FormatStats — new format: per-stage, per-content-type breakdown.
func (m *CompactMetrics) FormatStats() string
```

## Data Models

### Tool Result Flow

```
Tool.Run() → result map[string]any
  → BuildCompactorCallback
    → DetectContentType(result output field)
    → CompressionPipeline.Run(content, contentType, ctx)
      → parallel: ReformatPhase + BloatEstimation
      → gated: OffloadPhase (writes to CCRStore)
    → applyCompaction(result, pipelineResult)
    → metrics.Record(...)
  → result returned to LLM
```

### CCR Marker Injection

When an offload drops bytes, the output includes `<<ccr:HASH>>` at the end:

```
[compacted content]
<<ccr:a1b2c3d4e5f6a7b8c9d0e1f2>>
```

The LLM can call `retrieve_compacted` with the hash to get the original.

## Patterns to Follow

- **`runStage` pattern** — existing panic-recover wrapper for each stage. New transforms follow the same safety pattern.
- **Table-driven tests** — `compactor_test.go` style. Each algorithm gets its own `_test.go` file with fixture-based
  tests.
- **Config struct with JSON tags + `Default*Config()`** — matches existing `DefaultCompactorConfig()` pattern.
- **ADK `AfterToolCallback`** — the callback signature stays the same; only the internals change.
- **Tool registration via `newTool[TArgs, TResults]`** — the retrieve tool follows the same pattern as other tools in
  `registry.go`.

## Error Handling Strategy

- **Transform errors are non-fatal** — the orchestrator logs and continues, returning input unchanged (matches existing
  `runStage` panic-recover).
- **CCR store failures are logged at WARN** — compaction succeeds even if CCR write fails; output is still compacted but
  without retrieval marker.
- **Content detection failures fall back to `ContentPlainText`** — no crash on malformed input.
- **Empty input short-circuits** — all transforms return early on empty content.

## Acceptance Criteria

### Log Compression

- Given a 10,000-line pytest log with 5 errors and 3 stack traces, when LogCompressor runs, then output ≤ 50 lines
  preserving all errors, stack traces, and summary lines.
- Given npm output with repeated "added N packages" lines, when LogTemplate runs, then lines collapsed into
  `[Template Tn: ...] (Nx)` block, every line reconstructible.
- Given output below `min_lines_for_ccr`, when LogCompressor runs, then input returned unchanged (no CCR marker).

### Diff Compression

- Given a 200-line diff with 3 change lines, when DiffOffload runs, then context trimmed to 2 lines around changes,
  `<<ccr:HASH>>` marker emitted, original in CCR store.
- Given a Cargo.lock diff hunk, when DiffNoise runs, then hunk dropped, original stashed, marker emitted.
- Given a diff below `min_lines_for_ccr`, when DiffCompressor runs, then input returned unchanged.

### Search Compression

- Given grep output with 100 matches across 5 files, when SearchCompressor runs with `group_by_file=true`, then output
  grouped by file header, adaptive total via Kneedle, top-scored matches kept.

### JSON Compression

- Given JSON array of 100 dicts from `kubectl get -o json`, when SmartCrusher runs, then schema dedup applied,
  rare-status values preserved, adaptive item count via Kneedle.

### CCR Retrieval

- Given any compacted output with `<<ccr:HASH>>` marker, when LLM calls `retrieve_compacted` with hash, then original
  bytes returned from in-memory store.
- Given a hash not in store, when retrieve is called, then `{ "error": "not found" }` returned.

### Content Detection

- Given `file:line:content` output, when DetectContentType runs, then returns `ContentSearchResults` with confidence >
  0.7.
- Given `diff --git` output, when DetectContentType runs, then returns `ContentGitDiff`.
- Given `[{"id":1,...},{"id":2,...}]`, when DetectContentType runs, then returns `ContentJsonArray`.

### Metrics

- Given `/rtk stats` command, when metrics have records, then output shows per-stage, per-content-type, per-strategy
  breakdown with savings percentages.

### Parity

- Given headroom's parity fixture `tests/parity/fixtures/log_compressor/*.json`, when Go LogCompressor runs with same
  config on same input, then output matches expected `compressed` field byte-for-byte.
- Given headroom's parity fixture for diff_compressor, when Go DiffCompressor runs, then output matches byte-for-byte.
- Given headroom's parity fixture for smart_crusher, when Go SmartCrusher runs, then output matches byte-for-byte.

## Testing Strategy

### Unit Tests (per algorithm)

Each algorithm file gets a `_test.go` with:

- Table-driven tests for config edge cases (empty input, below min_lines, above caps)
- Tests matching headroom's parity fixtures (JSON files from `tests/parity/fixtures/`)
- Tests for bloat estimation heuristics
- Tests for CCR marker emission and store writes

### Integration Tests

- `orchestrator_test.go` — pipeline dispatch, parallel execution, gating logic
- `content_type_test.go` — detection across all content types
- `ccr_test.go` — store put/get, key computation, marker format
- `compactor_test.go` — `BuildCompactorCallback` end-to-end with new pipeline

### Parity Test Harness

- `parity_test.go` — loads headroom fixture JSON files, runs Go algorithm, compares output.
- Fixtures copied from `tmp/headroom/tests/parity/fixtures/` into `testdata/parity/`.

### Build Commands

- **build**: `go build ./internal/tools/...`
- **test**: `go test ./internal/tools/... -v`
- **vet**: `go vet ./internal/tools/...`
- **full**: `go build ./... && go test ./...`