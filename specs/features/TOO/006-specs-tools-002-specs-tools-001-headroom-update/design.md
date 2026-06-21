# Design — Headroom-Style RTK Compactor Overhaul (Redo)

## Current State

`internal/tools/compactor*.go` (2,857 LOC) is a flat serial pipeline: 9
tool-routed functions (`compactBash`, `compactRead`, `compactGrep`,
`compactFind`, `compactTree`, `compactGitFileDiff`, `compactGitOverview`,
`compactGitHunk`) plus shared utilities (`stripAnsi`, `hardTruncate`,
`aggregateTestOutput`, `filterBuildOutput`, `groupSearchOutput`,
`smartTruncate`, `compactGitDiffText`, etc.). No content-type detection,
no per-line scoring, no adaptive sizing, no CCR reversibility — every
truncated byte is gone permanently. The `CompactorConfig` struct has 7
booleans + 14 `Max*` integers all flat. Routing is by tool name; the
`config.CompactorConfig` user-facing subset exposes only 4 of those
fields (`enabled`, `source_code_filtering`, `max_chars`, `max_lines`).

`/rtk stats` works only in interactive mode (the metrics reference is
discarded in `print`/`json`/`rpc`). `CompactMetrics.Save()` writes
`<sessionDir>/compactor-metrics.json` but no production code calls it.
No tool named `retrieve_compacted` exists. 85 parity fixtures
(`internal/tools/testdata/parity/{content_detector,diff_compressor,log_compressor,smart_crusher}/`)
are byte-identical to upstream and **unreferenced by any Go code**.

## Desired End State

A headroom-style two-trait pipeline in `internal/tools/` that:

1. Detects `ContentType` from content (not tool name), routing to the
   right transforms.
2. Runs reformats (lossless) and bloat estimation (cheap pre-check) in
   parallel goroutines; gates offloads on `bloat >= threshold` or
   `post_reformat_ratio > fallback_ratio`.
3. Stores dropped bytes in an in-memory `CCRStore` (SHA-256 → 24 hex chars,
   `<<ccr:HASH>>` markers) per-session, lost on restart.
4. Provides a `retrieve_compacted` ADK tool exposing the CCR store to the
   LLM, returning raw bytes prefixed by a metadata header.
5. Implements 6 algorithms: `LogCompressor`, `DiffCompressor`+`DiffNoise`,
   `SearchCompressor`, `SmartCrusher`, `LogTemplate`, `JsonMinifier`.
6. Replaces fixed limits with `ComputeOptimalK` (Kneedle + zlib-ratio).
7. Adds per-line `KeywordDetector` (stdlib `strings.Contains`,
   headroom's `KeywordRegistry::default_set` exactly).
8. Restructures `CompactorConfig` (breaking change) with auto-migration
   on load + deprecation warning.
9. Updates `/rtk stats` to a per-stage/per-content-type/per-strategy
   breakdown.
10. Wires all 85 parity fixtures into Go tests for byte-for-byte parity.

## Architecture Overview

```mermaid
graph TB
    A[Tool Result] --> B[DetectContentType]
    B --> C{ContentType}
    C -->|BuildOutput| D[LogPipeline]
    C -->|GitDiff| E[DiffPipeline]
    C -->|SearchResults| F[SearchPipeline]
    C -->|JsonArray| G[JsonPipeline]
    C -->|SourceCode| H[ReadPipeline]
    C -->|PlainText/HTML| I[GenericPipeline]

    D --> J[CompressionPipeline]
    E --> J
    F --> J
    G --> J
    H --> J
    I --> J

    J -->|goroutine| K[Reformat Phase]
    J -->|goroutine| L[Bloat Estimation]
    K --> M{Gate}
    L --> M
    M -->|bloat >= threshold| N[Offload Phase]
    M -->|bloat < threshold| O[Output Reformatted]
    N --> P[CCRStore.Put]
    P --> O

    O --> Q[Metrics.Record]
    Q --> R[/rtk stats]
```

**Concurrency model:** the reformat phase and per-offload `EstimateBloat`
calls run in goroutines (raw, no `errgroup` dependency). The orchestrator
reads both, then runs offloads serially in registration order.

## Components and Interfaces

### File Layout (final)

```
internal/tools/
├── ccr.go                       # CCRStore interface, InMemoryCCRStore, ComputeCCRKey, CCRMarker
├── ccr_test.go                  # parity-style table tests
├── content_type.go              # ContentType enum, DetectionResult, DetectContentType, DetectFromToolName
├── content_type_test.go         # parity tests against content_detector/*.json
├── transform.go                 # ReformatTransform, OffloadTransform, CompressionContext, TransformError
├── transform_test.go            # interface-conformance smoke tests
├── orchestrator.go              # CompressionPipeline, OrchestratorConfig, PipelineResult, parallel Run
├── orchestrator_test.go         # stub-transform pipeline tests
├── signals.go                   # ImportanceContext, ImportanceCategory, ImportanceSignal, KeywordDetector
├── signals_test.go              # keyword + per-context scoring tests
├── adaptive_sizer.go            # ComputeOptimalK, FindKnee, CountUniqueSimhash, Simhash
├── adaptive_sizer_test.go       # curve fixtures + Kneedle edge cases
├── log_compressor.go            # LogCompressor + LogCompressorConfig
├── log_compressor_test.go       # parity tests against log_compressor/*.json
├── diff_compressor.go           # DiffCompressor + DiffCompressorConfig
├── diff_compressor_test.go      # parity tests against diff_compressor/*.json
├── diff_noise.go                # DiffNoise (offload) + DiffNoiseConfig
├── diff_noise_test.go           # lockfile + whitespace-only hunk tests (table-driven)
├── search_compressor.go         # SearchCompressor + SearchCompressorConfig
├── search_compressor_test.go    # table-driven grep/find/ripgrep scenarios
├── smart_crusher.go             # SmartCrusher + SmartCrusherConfig + 4 sub-types
├── smart_crusher_test.go        # parity tests against smart_crusher/*.json
├── log_template.go              # LogTemplate reformat + LogTemplateConfig
├── log_template_test.go         # table-driven template-mining scenarios
├── json_minifier.go             # JsonMinifier reformat
├── json_minifier_test.go        # table-driven minify/expand scenarios
├── ccr_retrieve.go              # ADK tool `retrieve_compacted`
├── ccr_retrieve_test.go         # retrieve-tool invocation tests
├── compactor.go                 # restructured CompactorConfig, DefaultCompactorConfig, BuildCompactorCallback, MigrateLegacyConfig
├── compactor_metrics.go         # new CompactRecord fields, new FormatStats
├── compactor_test.go            # existing tests + migration tests
└── testdata/parity/             # 85 JSON fixtures (unchanged)
```

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

func (c ContentType) String() string // "plain_text", "build", "diff", ...

type DetectionResult struct {
Type       ContentType
Confidence float64
Metadata   map[string]any
}

func DetectContentType(content string) DetectionResult
func DetectFromToolName(toolName string) ContentType // hint, not authority
```

Regex-based, compiled-once via `sync.OnceValue`. Walks first ~100 lines.
Headroom parity: identical `ContentType` enum, identical `as_str()` values
(`"json_array" | "source_code" | "search" | "build" | "diff" | "html" | "text"`).
Fixture field name `content_type` must match these strings exactly.

### Transform Traits (`internal/tools/transform.go`)

```go
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
EstimateBloat(content string) float32 // method, not free fn
Apply(content string, ctx CompressionContext, store CCRStore) (OffloadOutput, error)
Confidence() float32
}

type OffloadOutput struct {
Output     string
BytesSaved int
CacheKey   string // REQUIRED (not Option)
}

type CompressionContext struct {
Query       string
TokenBudget int // 0 = no signal
}

type TransformErrorKind int

const (
TransformInvalidInput TransformErrorKind = iota
TransformSkipped
TransformInternal
)

type TransformError struct {
Transform string
Kind      TransformErrorKind
Message   string
}
```

**Correction vs. previous spec:** `EstimateBloat` is a **method on
`OffloadTransform`**, not a standalone function. Each offload provides
its own domain-specific estimator.

### CCR Store (`internal/tools/ccr.go`)

```go
type CCRStore interface {
Put(hash, payload string)
Get(hash string) (string, bool)
Len() int
}

type InMemoryCCRStore struct {
mu       sync.RWMutex
data     map[string]ccrEntry // payload + timestamp
capacity int                 // default 1000
ttl      time.Duration       // default 30 min
}

func NewInMemoryCCRStore() *InMemoryCCRStore
func (s *InMemoryCCRStore) Put(hash, payload string)
func (s *InMemoryCCRStore) Get(hash string) (string, bool)
func (s *InMemoryCCRStore) Len() int

func ComputeCCRKey(payload []byte) string // SHA-256[:24]
func CCRMarker(hash string) string // "<<ccr:HASH>>"
```

`Get` returns `(payload, true)` only if present AND not expired. Eviction
is lazy (checked on `Get`) plus size-bounded FIFO eviction on `Put` when
over capacity. **Hash algorithm deviation:** SHA-256 instead of BLAKE3.
Documented in code comments; no parity impact (CCR keys are not stored in
fixtures).

### Orchestrator (`internal/tools/orchestrator.go`)

```go
type OrchestratorConfig struct {
ReformatTargetRatio  float64 // 0.5
BloatThreshold       float32 // 0.5
OffloadFallbackRatio float64 // 0.85
}

type CompressionPipeline struct {
reformats    []ReformatTransform
offloads     []OffloadTransform
orchestrator OrchestratorConfig
bloatCfg     BloatConfigs
}

type PipelineResult struct {
Output       string
BytesSaved   int
StepsApplied []string
CacheKeys    []string
}

type CompressionPipelineBuilder struct{ /* private */ }

func NewCompressionPipelineBuilder() *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) WithReformat(t ReformatTransform) *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) WithOffload(t OffloadTransform) *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) WithConfig(c OrchestratorConfig) *CompressionPipelineBuilder
func (b *CompressionPipelineBuilder) Build(store CCRStore) *CompressionPipeline

func (p *CompressionPipeline) Run(content string, ct ContentType, ctx CompressionContext) PipelineResult
```

`Run` algorithm:

1. Empty input → return default `PipelineResult{}`.
2. Spawn goroutine A: run reformats serially in registration order, stop
   when `current_len / original_len <= reformat_target_ratio`.
3. Spawn goroutine B: for each registered offload whose `AppliesTo()`
   contains `ct`, call `EstimateBloat(content)`.
4. Wait for both. For each offload with `bloat >= BloatThreshold`, or for
   the highest-confidence offload if `post_reformat_ratio >
   OffloadFallbackRatio && bloat > 0`, run `Apply` serially in
   registration order. On success, append `cache_key` to result.
5. Return final `PipelineResult`.

**Failure semantics:** any `TransformError` with `Kind = Skipped` or
`InvalidInput` is logged at trace level and the transform is skipped.
Any `Kind = Internal` is logged at warn level and the transform is
skipped. The orchestrator never panics; it never returns an error from
`Run` (always returns a `PipelineResult`, possibly with no steps applied).

### Line Importance Detection (`internal/tools/signals.go`)

```go
type ImportanceContext int

const (
ImportanceText ImportanceContext = iota
ImportanceSearch
ImportanceDiff
ImportanceLog
)

func (c ImportanceContext) String() string

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

type KeywordDetector struct {
registry KeywordRegistry
}

type KeywordRegistry struct {
Error             []string
Warning           []string
Importance        []string
Security          []string
MarkdownPrefixes  []string
ErrorIndicators   []string
}

func DefaultKeywordRegistry() KeywordRegistry // exact match to headroom's KeywordRegistry::default_set()
func NewKeywordDetector() *KeywordDetector
func (d *KeywordDetector) Score(line string, ctx ImportanceContext) ImportanceSignal
func (d *KeywordDetector) ContainsErrorIndicator(text string) bool

const (
ErrorPriority = float32(0.95)
SecurityPriority = float32(0.85)
WarningPriority     = float32(0.75)
ImportancePriority = float32(0.60)
MarkdownPriority = float32(0.45)
KeywordConfidence = float32(0.70)
)
```

**Exact `DefaultKeywordRegistry()` content** (must match headroom's
`KeywordRegistry::default_set()` byte-for-byte):

```go
KeywordRegistry{
Error: []string{"error", "exception", "fail", "failed", "failure",
"fatal", "critical", "crash", "panic",
"abort", "timeout", "denied", "rejected"},
Warning:          []string{"warn", "warning"},
Importance:       []string{"important", "note", "todo", "fixme",
"hack", "xxx", "bug", "fix"},
Security:         []string{"security", "auth", "password", "secret"},
MarkdownPrefixes: []string{"# ", "## ", "### ", "#### ", "**", "> "},
ErrorIndicators:  []string{"error", "fail", "exception",
"traceback", "fatal", "panic", "crash"},
}
```

**Per-context activation:**

- `ImportanceText` — all categories except `Security` (security dropped
  to avoid LLM-token false positives in prose)
- `ImportanceSearch` — `Error`, `Warning`, `Importance` (no `Security`,
  no `Markdown`)
- `ImportanceDiff` — `Error`, `Security`, `Importance` (no `Warning`,
  no `Markdown`)
- `ImportanceLog` — `Error`, `Warning`, `Importance` (no `Security`,
  no `Markdown`)

**Word-boundary filter:** a keyword only matches at a `[A-Za-z0-9_]`
boundary. `"panicker"` does NOT match `"panic"`. Implemented via
`(before_rune ∈ {letter, digit, _} || start) && (after_rune ∈ {letter,
digit, _} || end)`.

**Implementation:** `strings.Contains` (no aho-corasick dep, per the
stdlib-only constraint). O(n×m) per line, fast enough for the input
sizes involved.

### Adaptive Sizer (`internal/tools/adaptive_sizer.go`)

```go
func ComputeOptimalK(items []string, bias float64, minK, maxK int) int
func FindKnee(curve []int) (int, bool)
func ComputeUniqueBigramCurve(items []string) []int
func Simhash(text string) uint64
func HammingDistance(a, b uint64) uint32
func CountUniqueSimhash(items []string, threshold uint32) int
func ValidateWithZlib(items []string, k, maxK int, tolerance float64) int
```

`ComputeOptimalK` three-tier (matches headroom):

1. Fast path: `n ≤ 8` → return `n`. Near-total redundancy (`≤ 3` unique
   groups via `CountUniqueSimhash`) → return that count.
2. Kneedle: `FindKnee(curve)`. If returns `Some(idx+1)`, use that.
3. Zlib-ratio validation: `ValidateWithZlib` bumps `k` by 20% when subset
   compresses much better than full.

Zlib validation uses `compress/flate` from stdlib.

### Algorithms

#### `LogCompressor` (`internal/tools/log_compressor.go`)

```go
type LogFormat int // Pytest, Npm, Cargo, Jest, Make, Generic
type LogLevel int  // Error, Fail, Warn, Info, Debug, Trace, Unknown

type LogLine struct {
LineNumber   int
Content      string
Level        LogLevel
IsStackTrace bool
IsSummary    bool
Score        float32
}

type LogCompressorConfig struct {
MaxErrors, ErrorContextLines                 int
KeepFirstError, KeepLastError                bool
MaxStackTraces, StackTraceMaxLines           int
MaxWarnings                                  int
DedupeWarnings, KeepSummaryLines             bool
MaxTotalLines                                int
EnableCCR                                    bool
MinLinesForCCR                               int
MinCompressionRatioForCCR                    float64
}

func DefaultLogCompressorConfig() LogCompressorConfig // matches fixture defaults exactly

type LogCompressionResult struct {
Compressed            string
Original              string
OriginalLineCount     int
CompressedLineCount   int
FormatDetected        LogFormat
CompressionRatio      float64
CacheKey              string  // "" = no CCR
Stats                 map[string]uint64
}

type LogCompressor struct {
cfg        LogCompressorConfig
importance *KeywordDetector
}

func NewLogCompressor(cfg LogCompressorConfig) *LogCompressor
func (c *LogCompressor) Compress(content string, bias float64) (LogCompressionResult, LogCompressorStats, error)
func (c *LogCompressor) CompressWithStore(content string, bias float64, store CCRStore) (LogCompressionResult, LogCompressorStats, error)
```

When `CacheKey != ""`, the original `content` is stored in `store` under
that key. The compressed output ends with the marker
`[N lines compressed to M. Retrieve more: hash=HASH]`.

Defaults (verified from fixtures):

```
MaxErrors=10, ErrorContextLines=3, KeepFirstError=true, KeepLastError=true,
MaxStackTraces=3, StackTraceMaxLines=20, MaxWarnings=5,
DedupeWarnings=true, KeepSummaryLines=true, MaxTotalLines=100,
EnableCCR=true, MinLinesForCCR=50, MinCompressionRatioForCCR=0.5
```

Implements both `ReformatTransform` (lossless path, no CCR) and
`OffloadTransform` (lossy path, gated by `EstimateBloat` ≥ `MinLinesForCCR`).

#### `DiffCompressor` (`internal/tools/diff_compressor.go`)

```go
type DiffCompressorConfig struct {
MaxContextLines              int
MaxHunksPerFile              int
MaxFiles                     int
AlwaysKeepAdditions          bool
AlwaysKeepDeletions          bool
EnableCCR                    bool
MinLinesForCCR               int
MinCompressionRatioForCCR    float64
}

func DefaultDiffCompressorConfig() DiffCompressorConfig

type DiffCompressionResult struct {
Compressed          string
OriginalLineCount   int
CompressedLineCount int
FilesAffected       int
Additions           int
Deletions           int
HunksKept           int
HunksRemoved        int
CacheKey            string
}

type DiffCompressor struct { /* private */ }

func NewDiffCompressor(cfg DiffCompressorConfig) *DiffCompressor
func (c *DiffCompressor) Compress(content, context string) (DiffCompressionResult, error)
func (c *DiffCompressor) CompressWithStore(content, context string, store CCRStore) (DiffCompressionResult, error)
```

Defaults:

```
MaxContextLines=2, MaxHunksPerFile=10, MaxFiles=20,
AlwaysKeepAdditions=true, AlwaysKeepDeletions=true,
EnableCCR=true, MinLinesForCCR=50
```

**`MinLinesForCCR` is a misnomer** (matches headroom's source comment):
it gates the entire compression path. When `OriginalLineCount <
MinLinesForCCR`, the input is returned unchanged (no parsing, no
summary, no CCR). The Go port preserves this behavior.

Implements `OffloadTransform`.

#### `DiffNoise` (`internal/tools/diff_noise.go`)

```go
type DiffNoiseConfig struct {
MinLines                 int
LockfileSuffixes         []string
DropWhitespaceOnlyHunks  bool
}

func DefaultDiffNoiseConfig() DiffNoiseConfig

// Defaults:
//   MinLines = 30
//   LockfileSuffixes = ["Cargo.lock", "package-lock.json", "yarn.lock",
//     "pnpm-lock.yaml", "poetry.lock", "Pipfile.lock", "Gemfile.lock",
//     "go.sum", "composer.lock"]
//   DropWhitespaceOnlyHunks = true
```

Implements `OffloadTransform` with `Confidence() = 0.9`. Output markers
`[diff_noise: lockfile hunks dropped (N lines)]` and
`[diff_noise CCR: hash=HASH]`.

#### `SearchCompressor` (`internal/tools/search_compressor.go`)

```go
type SearchMatch struct {
File       string
LineNumber int64
Content    string
Score      float32
IsContext  bool
}

type FileMatches struct {
File      string
Matches   []SearchMatch
FileScore float32
}

type SearchCompressorConfig struct {
MinMatchesForCCR              int
MinCompressionRatioForCCR     float64
MaxPerFile                    int
AdaptiveTotal                 bool
GroupByFile                   bool
}

func DefaultSearchCompressorConfig() SearchCompressorConfig

type SearchCompressionResult struct {
Compressed            string
OriginalMatchCount    int
CompressedMatchCount  int
CacheKey              string
FilesAffected         int
}

type SearchCompressor struct {
cfg        SearchCompressorConfig
importance *KeywordDetector
}

func NewSearchCompressor(cfg SearchCompressorConfig) *SearchCompressor
func (c *SearchCompressor) WithDetector(d *KeywordDetector) *SearchCompressor
func (c *SearchCompressor) Compress(content, context string, bias float64) (SearchCompressionResult, SearchCompressorStats, error)
```

Implements `ReformatTransform` (default) and `OffloadTransform`
(opt-in via config). Note: headroom's `SearchOffload` is **not in the
default pipeline** — the Go port's `BuildCompactorCallback` will register
`SearchCompressor` only as a `ReformatTransform` (lossless), not as an
offload. This matches headroom's behavior.

#### `SmartCrusher` (`internal/tools/smart_crusher.go`)

```go
type SmartCrusherConfig struct {
Enabled                       bool
DedupIdenticalItems           bool
FactorOutConstants            bool
FirstFraction                 float64
LastFraction                  float64
IncludeSummaries              bool
MaxItemsAfterCrush            int
MinItemsToAnalyze             int
MinTokensToCrush              int
PreserveChangePoints          bool
SimilarityThreshold           float64
ToinConfidenceThreshold       float64
UniquenessThreshold           float64
UseFeedbackHints              bool
VarianceThreshold             float64
}

func DefaultSmartCrusherConfig() SmartCrusherConfig

type CrushResult struct {
Compressed   string
Original     string
WasModified  bool
Strategy     string
}

type SmartCrusher struct { /* private */ }

func NewSmartCrusher(cfg SmartCrusherConfig) *SmartCrusher
func (c *SmartCrusher) Crush(content, query string, bias float64) (CrushResult, error)
```

Defaults (verified from fixtures):

```
Enabled=true, DedupIdenticalItems=true, FactorOutConstants=false,
FirstFraction=0.3, LastFraction=0.15, IncludeSummaries=false,
MaxItemsAfterCrush=15, MinItemsToAnalyze=5, MinTokensToCrush=200,
PreserveChangePoints=true, SimilarityThreshold=0.8,
ToinConfidenceThreshold=0.5, UniquenessThreshold=0.1,
UseFeedbackHints=true, VarianceThreshold=2.0
```

Implements `OffloadTransform`. Emits the
`{"_ccr_dropped":"<<ccr:HASH N_rows_offloaded>>"}` marker format when
CCR is used.

#### `LogTemplate` (`internal/tools/log_template.go`)

```go
type LogTemplateConfig struct {
MinLines            int
MinRun              int
SimilarityThreshold float32
MinConstantTokens   int
}

func DefaultLogTemplateConfig() LogTemplateConfig
// MinLines=20, MinRun=3, SimilarityThreshold=0.4 (Drain default),
// MinConstantTokens=2

type LogTemplate struct { cfg LogTemplateConfig }
func NewLogTemplate(cfg LogTemplateConfig) *LogTemplate

// Implements ReformatTransform for ContentBuildOutput
```

Drain-inspired order-preserving template miner. Collapses consecutive
same-template lines into `[Template Tn: ...] (Nx)` + variant table.
Wildcard sentinel `<*>`. **Lossless** — original lines reconstructible
from the template + variants.

#### `JsonMinifier` (`internal/tools/json_minifier.go`)

```go
type JsonMinifier struct{}
func NewJsonMinifier() *JsonMinifier

// Implements ReformatTransform for ContentJsonArray.
// encoding/json round-trip; if minified > original, returns original.
```

### CCR Retrieval Tool (`internal/tools/ccr_retrieve.go`)

```go
func NewRetrieveTool(store CCRStore) (tool.Tool, error)
```

ADK `tool.Tool` implementation. Name: `retrieve_compacted`. Args schema:

```json
{
  "type": "object",
  "properties": { "hash": { "type": "string", "description": "24-char CCR hash" } },
  "required": ["hash"]
}
```

Return format (per Q4 answer):

```
<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>
<raw original bytes>
```

When the hash is missing or expired, returns a structured error message:
`CCR hash not found: <hash>`. Registered in `CoreTools`.

### Restructured `CompactorConfig` (`internal/tools/compactor.go`)

**New shape** (nested, breaking change):

```go
type CompactorConfig struct {
Enabled    bool                       `json:"enabled"`
Pipeline   PipelineConfig             `json:"pipeline"`
Bloat      BloatConfigs               `json:"bloat"`
Reformat   ReformatConfigs            `json:"reformat"`
Offload    OffloadConfigs             `json:"offload"`
Algorithms AlgorithmsConfig            `json:"algorithms"`
Limits     LimitsConfig               `json:"limits"`
}

type PipelineConfig struct {
ReformatTargetRatio  float64 `json:"reformat_target_ratio"`
BloatThreshold       float32 `json:"bloat_threshold"`
OffloadFallbackRatio float64 `json:"offload_fallback_ratio"`
}

type BloatConfigs struct {
Log    LogBloatConfig    `json:"log"`
Diff   DiffBloatConfig   `json:"diff"`
Search SearchBloatConfig `json:"search"`
}

type LogBloatConfig struct {
MinLines                  int     `json:"min_lines"`
SampleSize                int     `json:"sample_size"`
HighPriorityThreshold     float32 `json:"high_priority_threshold"`
UniquenessWeight          float32 `json:"uniqueness_weight"`
PriorityDilutionWeight    float32 `json:"priority_dilution_weight"`
}

type DiffBloatConfig struct {
MinLines            int     `json:"min_lines"`
NormalContextRatio  float64 `json:"normal_context_ratio"`
}

type SearchBloatConfig struct {
MinMatches       int     `json:"min_matches"`
ClusterThreshold float32 `json:"cluster_threshold"`
}

type ReformatConfigs struct {
LogTemplate LogTemplateConfig `json:"log_template"`
}

type OffloadConfigs struct {
Json      JsonOffloadConfig `json:"json"`
DiffNoise DiffNoiseConfig   `json:"diff_noise"`
}

type JsonOffloadConfig struct {
MinArrayRows  int `json:"min_array_rows"`
SaturationRows int `json:"saturation_rows"`
}

type DiffNoiseConfig struct {
MinLines                int      `json:"min_lines"`
LockfileSuffixes        []string `json:"lockfile_suffixes"`
DropWhitespaceOnlyHunks bool     `json:"drop_whitespace_only_hunks"`
}

type AlgorithmsConfig struct {
LogCompressor    LogCompressorConfig    `json:"log_compressor"`
DiffCompressor   DiffCompressorConfig   `json:"diff_compressor"`
SearchCompressor SearchCompressorConfig `json:"search_compressor"`
SmartCrusher     SmartCrusherConfig     `json:"smart_crusher"`
LogTemplate      LogTemplateConfig      `json:"log_template"`
JsonMinifier     JsonMinifierConfig     `json:"json_minifier"`
}

type JsonMinifierConfig struct {
Enabled bool `json:"enabled"`
}

type LimitsConfig struct {
MaxChars int `json:"max_chars"`
MaxLines int `json:"max_lines"`
}

func DefaultCompactorConfig() CompactorConfig
```

**`MigrateLegacyConfig`** (per Q2 answer) — detects old-shape config by
checking for any of the legacy fields (`strip_ansi`, `aggregate_test_output`,
`filter_build_output`, `compact_git_output`, `aggregate_linter_output`,
`group_search_output`, `smart_truncate`, `source_code_filtering`, the
`Max*` integer fields). If detected, builds the new shape in memory,
logs
`log.Printf("compactor: legacy config detected, auto-migrating (X old fields present) — see changelog for migration notes")`,
and returns the new struct. Idempotent.

The migration is invoked from `internal/cli/cli.go` and
`internal/cli/interactive.go` after `DefaultCompactorConfig()` is loaded,
before `BuildCompactorCallback` is called.

### Restructured `CompactRecord` (`internal/tools/compactor_metrics.go`)

```go
type CompactRecord struct {
Tool        string    `json:"tool"`
ContentType string    `json:"content_type"` // NEW
Stages      []string  `json:"stages"`     // NEW (renamed from Techniques)
Strategies  []string  `json:"strategies"` // NEW (offload names that fired)
BytesSaved  int       `json:"bytes_saved"` // NEW (sum of saved)
OrigSize    int       `json:"orig_size"`
CompSize    int       `json:"comp_size"`
Timestamp   time.Time `json:"timestamp"`
}

type CompactSummary struct {
TotalOrig     int                            `json:"total_orig"`
TotalComp     int                            `json:"total_comp"`
SavingsPct    float64                        `json:"savings_pct"`
ByTool        map[string]ToolCompactSum      `json:"by_tool"`
ByContentType map[string]ContentTypeSum      `json:"by_content_type"` // NEW
ByStage       map[string]StageSum            `json:"by_stage"` // NEW
ByStrategy    map[string]StrategySum         `json:"by_strategy"` // NEW
}

type ContentTypeSum struct {
Count int `json:"count"`
Orig  int `json:"orig"`
Comp  int `json:"comp"`
}

type StageSum struct {
Count int `json:"count"`
Orig  int `json:"orig"`
Comp  int `json:"comp"`
}

type StrategySum struct {
Count int `json:"count"`
Orig  int `json:"orig"`
Comp  int `json:"comp"`
}
```

**New `FormatStats` output**:

```
RTK Compactor Stats
═══════════════════
Total calls:    <count>
Original size:  <bytes>
Compacted size: <bytes>
Savings:        <pct>%

By Content Type:
  <type>          <count> calls  <orig> → <comp>  (<pct>%)

By Stage:
  <stage>         <count> calls  <orig> → <comp>  (<pct>%)

By Strategy (offloads):
  <strategy>      <count> calls  <orig> → <comp>  (<pct>%)

By Tool:
  <tool>          <count> calls  <orig> → <comp>  (<pct>%)
```

All four breakdowns sorted alphabetically by key for deterministic output.

## Data Models — Migration Summary

| Old field                 | New field                                                               |
|---------------------------|-------------------------------------------------------------------------|
| `strip_ansi`              | folded into `LogCompressor` + `DiffCompressor` reformats (always-on)    |
| `aggregate_test_output`   | folded into `LogCompressor`                                             |
| `filter_build_output`     | folded into `LogCompressor`                                             |
| `compact_git_output`      | folded into `DiffCompressor`                                            |
| `aggregate_linter_output` | folded into `LogCompressor` (linter detection)                          |
| `group_search_output`     | folded into `SearchCompressor.GroupByFile` (default `true`)             |
| `smart_truncate`          | folded into `LogCompressor` line-priority scoring                       |
| `source_code_filtering`   | preserved at top level as `SourceCodeFiltering` for back-compat reading |
| `max_chars`               | `Limits.MaxChars`                                                       |
| `max_lines`               | `Limits.MaxLines`                                                       |
| `max_test_failures`       | `Algorithms.LogCompressor.MaxErrors`                                    |
| `max_test_fail_lines`     | `Algorithms.LogCompressor.ErrorContextLines`                            |
| `max_build_errors`        | `Algorithms.LogCompressor.MaxErrors` (alias)                            |
| `max_build_err_lines`     | `Algorithms.LogCompressor.ErrorContextLines` (alias)                    |
| `max_diff_lines`          | `Algorithms.DiffCompressor.MaxFiles`                                    |
| `max_diff_hunk_lines`     | `Algorithms.DiffCompressor.MaxContextLines`                             |
| `max_status_files`        | preserved in `GitCompressor` config (out of v1 scope)                   |
| `max_log_entries`         | `Algorithms.LogCompressor.MaxTotalLines`                                |
| `max_linter_rules`        | `Algorithms.LogCompressor.MaxWarnings`                                  |
| `max_linter_files`        | `Algorithms.LogCompressor.MaxErrors` (alias)                            |
| `max_search_per_file`     | `Algorithms.SearchCompressor.MaxPerFile`                                |
| `max_search_total`        | `Algorithms.SearchCompressor.MaxPerFile * 5` heuristic                  |

If multiple old fields map to the same new field, the **last one in
the JSON unmarshal order wins** (deterministic, documented in the
migration log line).

## Patterns to Follow (existing pi-go conventions)

- **Table-driven tests** with `[]struct{name, input, want, applied}` +
  `t.Run(tt.name, ...)`. Used in `TestStripAnsi`, `TestHardTruncate`,
  `TestDetectCommand`, etc.
- **Tests live alongside source** (`foo.go` ↔ `foo_test.go`), all in
  `package tools` (not `package tools_test`).
- **Unexported per-feature file** (`compactor_bash.go`,
  `compactor_git.go`, etc.) — each new algorithm gets its own file.
- **Sync.Mutex for concurrent state** — `CompactMetrics.mu sync.Mutex`
  already used; CCR store uses `sync.RWMutex` (read-heavy).
- **No new external deps** — stdlib only: `crypto/sha256`,
  `compress/flate`, `encoding/json`, `regexp`, `sync`, `sort`, `strings`,
  `time`, `errors`.
- **`log.Printf` for non-fatal warnings**, never `panic` after init.
- **Tool name pattern**: `<feature>.go` ↔ `<feature>_test.go` ↔
  `Test<Feature>*` test functions.

## Error Handling Strategy

- `ReformatTransform.Apply` / `OffloadTransform.Apply` return errors of
  type `*TransformError`. The orchestrator inspects `Kind` and either
  skips silently (`Skipped`, `InvalidInput`) or logs a warning
  (`Internal`).
- All public API entry points (`BuildCompactorCallback`,
  `RetrieveCompacted` tool) **never panic** after init. Errors are
  logged and a sensible default returned.
- `MigrateLegacyConfig` returns an error only if JSON unmarshalling
  fails; never on field-level issues (those are warnings).
- CCR store `Get` returns `(empty, false)` for missing/expired entries.
  Callers must handle the `false` case explicitly.
- `RetrieveTool` returns the structured error message text rather than
  an error result, so the LLM sees a clear "hash not found" line.

## Acceptance Criteria (Given/When/Then)

### Slice-by-slice gates

Each slice ships only when its gate passes (verified by the implementer
in the same commit):

- **Slice 1 (CCR + Content Detection)** — given a CCR store, when
  `ComputeCCRKey(payload)` is called, then SHA-256[:24] is returned;
  given 21 content_detector fixtures, when `DetectContentType` runs on
  each `input`, then output matches the fixture's
  `output.content_type` byte-for-byte.
- **Slice 2 (Traits + Orchestrator)** — given stub transforms registered
  in a `CompressionPipeline`, when `Run(content, ct, ctx)` is called,
  then reformats run in registration order, offloads run only when
  bloat clears threshold, and `PipelineResult.Output` reflects the
  chain.
- **Slice 3 (Signals + Adaptive Sizer)** — given lines containing
  `error`, `warn`, `security`, `TODO`, `> ` prefix, when
  `KeywordDetector.Score` runs in `ImportanceLog`/`ImportanceText`
  contexts, then priorities match
  `Error=0.95, Warning=0.75, Security=0.85, Importance=0.6,
  Markdown=0.45`. Given a known bigram curve, when `FindKnee` runs,
  then the knee index is returned.
- **Slice 4 (LogCompressor)** — given 20 log_compressor fixtures, when
  `LogCompressor.Compress` runs, then all output fields
  (`compressed`, `original_line_count`, `compressed_line_count`,
  `compression_ratio`, `format_detected`, `cache_key`, `stats`)
  match fixture fields byte-for-byte.
- **Slice 5 (DiffCompressor + DiffNoise)** — given 27 diff_compressor
  fixtures, when `DiffCompressor.Compress` runs, then all output
  fields match byte-for-byte. `DiffNoise` passes table-driven tests for
  lockfile + whitespace-only hunks.
- **Slice 6 (SearchCompressor)** — table-driven tests for grep output
  with 100 matches / 5 files / cluster scoring, no parity fixtures.
- **Slice 7 (SmartCrusher)** — given 17 smart_crusher fixtures, when
  `SmartCrusher.Crush` runs, then `compressed`, `original`,
  `was_modified`, `strategy` match fixture fields byte-for-byte.
- **Slice 8 (LogTemplate + JsonMinifier)** — table-driven: given N
  consecutive same-template lines, when `LogTemplate.Apply` runs, then
  output contains `[Template T1: ...] (Nx)`. Given JSON with
  whitespace, when `JsonMinifier.Apply` runs, then output is minified
  and ≤ original length.
- **Slice 9 (CCR Retrieve Tool)** — given a `CCRStore` with 3 entries
  (LogCompressor, DiffCompressor, SearchCompressor), when
  `retrieve_compacted` is invoked with each hash, then the returned
  content matches the original payload prefixed with the metadata
  header. Missing hash returns the structured error.
- **Slice 10a (Config Restructure + Migration)** — given a legacy
  `config.json` with all old fields set, when `MigrateLegacyConfig` runs,
  then the new `CompactorConfig` has the equivalent fields populated
  and a deprecation warning is logged.
- **Slice 10b (Pipeline Integration + New Metrics)** — given a tool
  result, when `BuildCompactorCallback` runs in interactive mode, then
  the result is compacted via `CompressionPipeline.Run` and a
  `CompactRecord` with `ContentType`, `Stages`, `Strategies`,
  `BytesSaved` is recorded.
- **Slice 11 (TUI `/rtk stats`)** — given 3 `CompactRecords` from
  bash/grep/read tools, when `/rtk stats` is invoked, then output
  shows all 4 breakdowns (Total, By Content Type, By Stage, By Strategy,
  By Tool) with deterministic alphabetical ordering.

### Cross-slice acceptance

- `go build ./...` passes.
- `go test ./internal/tools/... -v` passes all 12 slices' `-run`
  regexes (one per slice).
- `go vet ./internal/tools/...` passes.
- `git diff` after each slice shows non-empty work (vacuous-pass
  protection).

## Testing Strategy

**Per-slice:** one focused `_test.go` file with at least one
`Test<Slice>_*` function that exercises the new code. The slice gate
runs `go test ./internal/tools/... -run "<slice-regex>"`.

**Parity tests:** each of the 85 fixtures gets a `Test<Algo>Parity/<fixture>`
subtest that loads the JSON, runs the Go algorithm, and asserts
field-by-field equality on the output. Implemented via
`//go:embed testdata/parity/<algo>` (one embed per algorithm file).

**Table-driven tests:** for algorithms without parity fixtures
(SearchCompressor, LogTemplate, JsonMinifier, DiffNoise), use the same
table-driven pattern as existing pi-go tests.

**Migration tests:** in `compactor_test.go`, `TestMigrateLegacyConfig`
loads a fixture JSON with old shape, calls `MigrateLegacyConfig`,
asserts each new field is set correctly, and asserts
`log` captured the deprecation warning (via custom `*log.Logger` injected
through package-level `var warnLog = log.Default()`).

**Slice-gate guard:** each `_test.go` file MUST contain at least one
test function whose name starts with `Test<Slice>`. The implementer
verifies this via
`grep -c "^func Test<Slice>" internal/tools/<file>_test.go`.

## Constraints

- **No new external Go dependencies.** Stdlib only.
- **SHA-256, not BLAKE3** for CCR keys (24 hex char prefix). Documented
  in code comments. No parity impact.
- **`strings.Contains`, not aho-corasick** for keyword detection.
  Word-boundary post-filter to avoid "panicker" matching "panic".
- **Stdlib zlib** (`compress/flate`) for ratio validation.
- **Each slice compiles + tests independently** — no slice's source
  imports anything from a future slice.
- **In-memory CCR store only** for v1 — SQLite and Redis backends are
  out of scope.
- **`SearchOffload` is NOT in the default pipeline** (matches headroom).
  `SearchCompressor` is registered only as a `ReformatTransform`.
- **`SmartCrusher` is the single largest slice** — ~1,000 LOC of Go.
  Plan for a longer commit; the implementer should budget 2–3× the
  time of other algorithm slices.
- **`config.CompactorConfig` (in `internal/config/config.go`)** mirrors
  the new `tools.CompactorConfig` shape but exposes only the
  user-overridable subset. Existing user-facing 4 fields continue to
  work; new fields are optional with defaults.