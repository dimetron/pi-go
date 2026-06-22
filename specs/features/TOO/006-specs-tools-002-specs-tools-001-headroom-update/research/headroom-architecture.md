# Research — Headroom Reference Source

## Source

Verified reads of `/Users/dimetron/p6s/pi-dev/pi-go/tmp/headroom/crates/headroom-core/src/`.
70 `.rs` files, 41,316 LOC total.

## 1. Scope Filter (what's actually needed for the port)

Of the 41,316 LOC, only the **pipeline** + **signals** + **CCR** modules are
relevant to the planned port (the previous spec incorrectly lumped in
`live_zone.rs`, `tag_protector.rs`, `compression_policy.rs`, `cache_control.rs`,
`relevance/*`, `recommendations.rs`, `magika_detector.rs`, `safety.rs`,
`auth_mode.rs`, `cache_control.rs`, and `tokenizer/*`):

| Module                                           |         LOC | In scope for port? | Why                                                                    |
|--------------------------------------------------|------------:|:------------------:|------------------------------------------------------------------------|
| `transforms/pipeline/traits.rs`                  |         338 |       ✅ yes        | Two trait interfaces (`ReformatTransform`, `OffloadTransform`) + types |
| `transforms/pipeline/orchestrator.rs`            |         848 |       ✅ yes        | `CompressionPipeline`, `PipelineResult`, builder                       |
| `transforms/pipeline/config.rs`                  |         331 |       ✅ yes        | `PipelineConfig` schema (TOML-loaded; we use JSON)                     |
| `transforms/pipeline/mod.rs`                     |         103 |       ✅ yes        | Re-exports                                                             |
| `transforms/pipeline/offloads/mod.rs`            |          34 |       ✅ yes        | Offload registry                                                       |
| `transforms/pipeline/offloads/log_offload.rs`    |         284 |       ✅ yes        | Log bloat estimator + offload impl                                     |
| `transforms/pipeline/offloads/diff_offload.rs`   |         279 |       ✅ yes        | Diff bloat estimator + offload impl                                    |
| `transforms/pipeline/offloads/diff_noise.rs`     |         524 |       ✅ yes        | Lockfile + whitespace hunk dropper                                     |
| `transforms/pipeline/offloads/json_offload.rs`   |         400 |       ✅ yes        | JSON bloat estimator + offload impl                                    |
| `transforms/pipeline/offloads/search_offload.rs` |         331 |     ⚠️ partial     | Intentionally NOT in default pipeline (see §8)                         |
| `transforms/pipeline/reformats/mod.rs`           |          12 |       ✅ yes        | Re-export                                                              |
| `transforms/pipeline/reformats/log_template.rs`  |         545 |       ✅ yes        | Drain-inspired template miner                                          |
| `transforms/pipeline/reformats/json_minifier.rs` |         175 |       ✅ yes        | `serde_json` round-trip                                                |
| `transforms/adaptive_sizer.rs`                   |         610 |       ✅ yes        | `ComputeOptimalK`, `FindKnee`, `SimHash`                               |
| `transforms/content_detector.rs`                 |         769 |       ✅ yes        | `ContentType` enum + `DetectContentType`                               |
| `signals/keyword_detector.rs`                    |         433 |       ✅ yes        | Tier-3 keyword detector                                                |
| `signals/line_importance.rs`                     |          84 |       ✅ yes        | `LineImportanceDetector` trait                                         |
| `signals/tiered.rs`                              |         141 |        ❌ no        | Composition combinator (not needed for v1)                             |
| `signals/mod.rs`                                 |          61 |       ✅ yes        | Re-exports                                                             |
| `ccr/mod.rs`                                     |         119 |       ✅ yes        | `CcrStore` trait + helpers                                             |
| `ccr/backends/mod.rs`                            |         152 |       ✅ yes        | Backend factory                                                        |
| `ccr/backends/in_memory.rs`                      |         317 |       ✅ yes        | `InMemoryCcrStore` (DashMap-backed)                                    |
| `transforms/log_compressor.rs`                   |       1,295 |       ✅ yes        | Used by parity tests; main LogCompressor                               |
| `transforms/diff_compressor.rs`                  |       1,685 |       ✅ yes        | Used by parity tests; main DiffCompressor                              |
| `transforms/search_compressor.rs`                |         902 |       ✅ yes        | Used in pipeline (as `SearchCompressor`)                               |
| `transforms/smart_crusher/*` (18 files)          |      ~5,000 |   ✅ yes (subset)   | Used by parity tests; main SmartCrusher                                |
| **In-scope total**                               | **~14,800** |                    |                                                                        |

The previous spec's "~14.5k LOC" estimate accidentally landed near the right
number but listed the wrong modules (included `live_zone.rs` etc.). The
**actually needed** port surface is closer to **~14,800 LOC of Rust** =
**~6,000 LOC of Go** at ~2.5× compaction (Go's stdlib density, no macros).

## 2. Two-Trait Pipeline Model

`transforms/pipeline/traits.rs`:

```rust
pub trait ReformatTransform: Send + Sync {
    fn name(&self) -> &'static str;
    fn applies_to(&self) -> &[ContentType];
    fn apply(&self, content: &str) -> Result<ReformatOutput, TransformError>;
}

pub trait OffloadTransform: Send + Sync {
    fn name(&self) -> &'static str;
    fn applies_to(&self) -> &[ContentType];
    fn estimate_bloat(&self, content: &str) -> f32;
    fn apply(&self, content: &str, ctx: &CompressionContext, store: &dyn CcrStore)
        -> Result<OffloadOutput, TransformError>;
    fn confidence(&self) -> f32;
}
```

**`estimate_bloat` is a METHOD on `OffloadTransform`**, NOT a standalone
function. Each of the 5 offload types implements its own domain-specific
estimator. The previous spec's `EstimateBloat(content) float32` interface
is a small mischaracterization — should be `OffloadTransform.EstimateBloat(content) float32`.

```rust
pub struct ReformatOutput { pub output: String, pub bytes_saved: usize }
pub struct OffloadOutput  { pub output: String, pub bytes_saved: usize,
                            pub cache_key: String }   // required, not Option
pub struct CompressionContext { pub query: String, pub token_budget: Option<usize> }
```

Three error variants, all signal "skip and continue":
`TransformError::InvalidInput`, `TransformError::Skipped`,
`TransformError::Internal`. Constructors: `invalid_input(t, msg)`,
`skipped(t, msg)`, `internal(t, msg)`.

## 3. Orchestrator (`transforms/pipeline/orchestrator.rs`)

```rust
pub struct PipelineResult {
    pub output: String,
    pub bytes_saved: usize,
    pub steps_applied: Vec<String>,   // names of accepted transforms in order
    pub cache_keys: Vec<String>,       // CCR keys from accepted offloads
}

pub struct CompressionPipeline { /* private */ }
pub struct CompressionPipelineBuilder { /* private */ }

impl CompressionPipeline {
    pub fn builder() -> CompressionPipelineBuilder;
    pub fn run(&self, content: &str, content_type: ContentType,
               ctx: &CompressionContext, store: &dyn CcrStore) -> PipelineResult;
    pub fn config(&self) -> &PipelineConfig;
}

impl CompressionPipelineBuilder {
    pub fn with_reformat<T: ReformatTransform + 'static>(self, t: T) -> Self;
    pub fn with_offload<T: OffloadTransform + 'static>(self, t: T) -> Self;
    pub fn with_config(self, cfg: PipelineConfig) -> Self;
    pub fn build(self) -> CompressionPipeline;
}
```

**Decision logic:**

- Reformats fire in registration order until
  `current_len / original_len ≤ reformat_target_ratio` (default 0.5).
- An offload fires when its `estimate_bloat(content) ≥ bloat_threshold`
  (default 0.5) **OR** when `post_reformat_ratio > offload_fallback_ratio`
  (default 0.85) AND its score is `> 0`.
- Empty input short-circuits with default-empty `PipelineResult`.
- Failures inside transforms are logged (WARN for `Internal`, TRACE for
  `Skipped`/`InvalidInput`) and skipped — orchestrator never panics.
- Parallel: uses `rayon::join` to run reformat phase AND per-offload bloat
  estimation concurrently. Go equivalent: `errgroup.Group` or raw goroutines.

## 4. `ContentType` Enum (`transforms/content_detector.rs`)

```rust
pub enum ContentType {
    JsonArray, SourceCode, SearchResults, BuildOutput, GitDiff, Html, PlainText,
}
impl ContentType {
    pub fn as_str(&self) -> &'static str  // "json_array" | "source_code" | ...
}
pub struct DetectionResult {
    pub content_type: ContentType,
    pub confidence: f64,
    pub metadata: serde_json::Map<String, Value>,
}
pub fn detect_content_type(content: &str) -> DetectionResult;
pub fn is_json_array_of_dicts(content: &str) -> bool;
```

Detects by walking first ~100 lines; uses `LazyLock<Regex>` for
`SEARCH_RESULT_PATTERN` (`^[^\s:]+:\d+:`), `DIFF_HEADER_PATTERN` (covers
`git diff`, `diff --combined`, `diff --cc`, `@@ ... @@`, `@@@ ... @@@`),
`PYTHON_PATTERN`, etc. Returns confidence + per-type metadata for the router.

## 5. Adaptive Sizer (`transforms/adaptive_sizer.rs`)

```rust
pub fn compute_optimal_k(items: &[&str], bias: f64, min_k: usize,
                         max_k: Option<usize>) -> usize;
pub fn find_knee(curve: &[usize]) -> Option<usize>;
pub fn compute_unique_bigram_curve(items: &[&str]) -> Vec<usize>;
pub fn simhash(text: &str) -> u64;   // char 4-gram MD5[:8] as big-endian u64
pub fn hamming_distance(a: u64, b: u64) -> u32;
pub fn count_unique_simhash(items: &[&str], threshold: u32) -> usize;
pub fn validate_with_zlib(items: &[&str], k: usize, max_k: usize,
                          tolerance: f64) -> usize;
```

**Three-tier `compute_optimal_k`:**

1. Fast path: `n ≤ 8` → return `n`; near-total redundancy (`≤ 3` unique
   groups) → return that count.
2. Kneedle on bigram-coverage curve: `find_knee` requires `max diff > 0.05`
   from diagonal; flat curves return `Some(1)`; otherwise
   `Some(knee_idx + 1)`.
3. Zlib-ratio validation: bumps `k` by 20% when subset compresses much
   better than full.

## 6. Keyword Detector (`signals/keyword_detector.rs`)

```rust
pub struct KeywordRegistry {
    pub error: Vec<&'static str>,
    pub warning: Vec<&'static str>,
    pub importance: Vec<&'static str>,
    pub security: Vec<&'static str>,
    pub markdown_prefixes: Vec<&'static str>,
    pub error_indicators: Vec<&'static str>,
}
impl KeywordRegistry { pub fn default_set() -> Self; }

pub struct KeywordDetector { /* private */ }
impl KeywordDetector {
    pub fn new() -> Self;
    pub fn with_registry(registry: KeywordRegistry) -> Self;
    pub fn contains_error_indicator(&self, text: &str) -> bool;
    pub fn registry(&self) -> &KeywordRegistry;
}
impl Default for KeywordDetector;
impl LineImportanceDetector for KeywordDetector;

pub const ERROR_PRIORITY: f32 = 0.95;
pub const SECURITY_PRIORITY: f32 = 0.85;
pub const WARNING_PRIORITY: f32 = 0.75;
pub const IMPORTANCE_PRIORITY: f32 = 0.6;
pub const MARKDOWN_PRIORITY: f32 = 0.45;
pub const KEYWORD_CONFIDENCE: f32 = 0.7;
```

**Exact `default_set()` content (lines 78–118):**

```rust
error:        ["error", "exception", "fail", "failed", "failure",
               "fatal", "critical", "crash", "panic",
               "abort", "timeout", "denied", "rejected"],
warning:      ["warn", "warning"],
importance:   ["important", "note", "todo", "fixme",
               "hack", "xxx", "bug", "fix"],
security:     ["security", "auth", "password", "secret"],
markdown_prefixes: ["# ", "## ", "### ", "#### ", "**", "> "],
error_indicators: ["error", "fail", "exception",
                   "traceback", "fatal", "panic", "crash"],
```

**Important corrections vs. previous spec:**

- Previous spec said priorities are `Error=0.95, Security=0.85, Warning=0.75,
  Importance=0.6, Markdown=0.45`. Verified above. **Correct.**
- Previous spec said `KeywordDetector::default_set()` exists. **Misleading** —
  it's `KeywordRegistry::default_set()`, and `KeywordDetector::new()` calls
  `Self::with_registry(KeywordRegistry::default_set())`.
- Note: `security` set deliberately drops `"token"` to avoid LLM-token false
  positives; `error` set adds `"abort", "timeout", "denied", "rejected"`
  beyond Python's `error_detection.py` regex.

The Go port uses `strings.Contains` (no aho-corasick dep), per the previous
spec's stdlib-only constraint. Per-byte word-boundary post-filter
(`[A-Za-z0-9_]`) prevents "panicker" matching "panic" — must be replicated.

## 7. `LineImportanceDetector` Trait (`signals/line_importance.rs`)

```rust
pub enum ImportanceContext { Text, Search, Diff, Log }
pub enum ImportanceCategory { Error, Warning, Importance, Security, Markdown }
pub struct ImportanceSignal {
    pub category: Option<ImportanceCategory>,
    pub priority: f32,    // 0.0 = drop first, 1.0 = keep at all costs
    pub confidence: f32,  // 0.0 = no info, 1.0 = detector is sure
}
pub trait LineImportanceDetector: Send + Sync {
    fn score(&self, line: &str, ctx: ImportanceContext) -> ImportanceSignal;
}
```

`Send + Sync` is required (compressors share detector instances across
tokio worker threads; in Go, this is just goroutine-safe).

## 8. CCR (`ccr/mod.rs`)

```rust
pub trait CcrStore: Send + Sync {
    fn put(&self, hash: &str, payload: &str);
    fn get(&self, hash: &str) -> Option<String>;
    fn len(&self) -> usize;
    fn is_empty(&self) -> bool { self.len() == 0 }   // default impl
}

pub const DEFAULT_CAPACITY: usize = 1000;
pub const DEFAULT_TTL: Duration = Duration::from_secs(1800);  // 30 min

pub fn compute_key(payload: &[u8]) -> String;   // BLAKE3 hex, first 24 chars
pub fn marker_for(hash: &str) -> String;        // format!("<<ccr:{hash}>>")
```

**Critical caveat for Go port:** headroom uses BLAKE3. Per the previous spec
("Use SHA-256 not BLAKE3 — documented deviation"), the Go port uses SHA-256
and takes first 24 hex chars. Hash algorithm is internal — fixtures only
store the input_sha256 of the original input (not the CCR key), so
BLAKE3→SHA-256 doesn't affect parity assertions.

`InMemoryCcrStore` (`ccr/backends/in_memory.rs`): DashMap-backed FIFO with
`with_capacity_and_ttl(capacity, ttl)`. The Go equivalent uses
`sync.Map` or `sync.RWMutex` + `map[string]entry`.

The **`SearchOffload`** is **intentionally NOT in the default pipeline**:
`offloads::SearchOffload` is reachable via the module path, but the
orchestrator's default registration omits it. The previous spec includes a
`SearchOffload` slice; this should be reconsidered or be made optional /
off-by-default. The `SearchCompressor` (not Offload) is what's used in the
default reformat path.

## 9. LogCompressor (`transforms/log_compressor.rs`)

```rust
pub struct LogCompressor { /* private */ }
impl LogCompressor {
    pub fn new(config: LogCompressorConfig) -> Self;
    pub fn compress(&self, content: &str, bias: f64) -> (LogCompressionResult, LogCompressorStats);
    pub fn compress_with_store(&self, content: &str, bias: f64,
                               store: Option<&dyn CcrStore>) -> (LogCompressionResult, LogCompressorStats);
}

pub struct LogCompressionResult {
    pub compressed: String,
    pub original: String,
    pub original_line_count: usize,
    pub compressed_line_count: usize,
    pub format_detected: LogFormat,
    pub compression_ratio: f64,
    pub cache_key: Option<String>,
    pub stats: BTreeMap<String, u64>,
}
pub enum LogFormat { Pytest, Npm, Cargo, Jest, Make, Generic }
pub enum LogLevel  { Error, Fail, Warn, Info, Debug, Trace, Unknown }
pub struct LogCompressorConfig {
    pub max_errors: usize,
    pub error_context_lines: usize,
    pub keep_first_error: bool,
    pub keep_last_error: bool,
    pub max_stack_traces: usize,
    pub stack_trace_max_lines: usize,
    pub max_warnings: usize,
    pub dedupe_warnings: bool,
    pub keep_summary_lines: bool,
    pub max_total_lines: usize,
    pub enable_ccr: bool,
    pub min_lines_for_ccr: usize,
    pub min_compression_ratio_for_ccr: f64,
}
```

**Defaults** (from fixture `config` blocks, verified against all 20
`log_compressor/` fixtures):

```
max_errors=10, error_context_lines=3, keep_first_error=true,
keep_last_error=true, max_stack_traces=3, stack_trace_max_lines=20,
max_warnings=5, dedupe_warnings=true, keep_summary_lines=true,
max_total_lines=100, enable_ccr=true, min_lines_for_ccr=50,
min_compression_ratio_for_ccr=0.5
```

**Pipeline:** format detection (pytest/npm/cargo/jest/make/generic via
aho-corasick) → per-line level classification → per-line scoring →
adaptive total-lines budget via `compute_optimal_k` → category selection
(errors first/last/top, fails, deduped warnings, stack traces, summaries) →
context windows → final adaptive cap. Optional CCR storage when
`compression_ratio < min_compression_ratio_for_ccr`. `LogLevel::FAIL` and
`LogLevel::Error` are scored equivalently.

## 10. DiffCompressor (`transforms/diff_compressor.rs`)

```rust
pub struct DiffCompressor { /* private */ }
impl DiffCompressor {
    pub fn new(config: DiffCompressorConfig) -> Self;
    pub fn compress(&self, content: &str, context: &str) -> DiffCompressionResult;
    pub fn compress_with_stats(&self, content: &str, context: &str) -> (DiffCompressionResult, DiffCompressorStats);
    pub fn compress_with_store(&self, content: &str, context: &str,
                               store: Option<&dyn CcrStore>) -> (DiffCompressionResult, DiffCompressorStats);
}

pub struct DiffCompressionResult {
    pub compressed: String,
    pub original_line_count: usize,
    pub compressed_line_count: usize,
    pub files_affected: usize,
    pub additions: usize,
    pub deletions: usize,
    pub hunks_kept: usize,
    pub hunks_removed: usize,
    pub cache_key: Option<String>,
}
pub struct DiffCompressorConfig {
    pub max_context_lines: usize,
    pub max_hunks_per_file: usize,
    pub max_files: usize,
    pub always_keep_additions: bool,
    pub always_keep_deletions: bool,
    pub enable_ccr: bool,
    pub min_lines_for_ccr: usize,
    pub min_compression_ratio_for_ccr: f64,
}
```

**Defaults** (from 27 `diff_compressor/` fixtures):

```
max_context_lines=2, max_hunks_per_file=10, max_files=20,
always_keep_additions=true, always_keep_deletions=true,
enable_ccr=true, min_lines_for_ccr=50
```

**Critical naming note** (from source comments):
> **`min_lines_for_ccr` is a misnomer.** It actually gates the entire
> compression path, not just the CCR marker: when
> `original_line_count < min_lines_for_ccr`, the input is returned unchanged,
> no parsing, no summary, no CCR.

**Pipeline:** parse per-file hunks → hunk sampling by `max_hunks_per_file`
(relevance-scored against optional user `context` query) → strip context
lines around retained hunks → when output is much smaller AND
`min_lines_for_ccr` is met, full original stashed in `CcrStore` and CCR
marker `[N lines compressed to M. Retrieve more: hash=...]` appended.

## 11. SearchCompressor (`transforms/search_compressor.rs`)

```rust
pub struct SearchCompressor {
    config: SearchCompressorConfig,
    importance: Box<dyn LineImportanceDetector>,
}
impl SearchCompressor {
    pub fn new(config: SearchCompressorConfig) -> Self;
    pub fn with_detector<D: LineImportanceDetector + 'static>(config: SearchCompressorConfig, detector: D) -> Self;
    pub fn compress(&self, content: &str, context: &str, bias: f64)
        -> (SearchCompressionResult, SearchCompressorStats);
    pub fn compress_with_store(&self, content: &str, context: &str, bias: f64,
                               store: Option<&dyn CcrStore>) -> (SearchCompressionResult, SearchCompressorStats);
}

pub struct SearchMatch     { pub file: String, pub line_number: u64, pub content: String, ... }
pub struct FileMatches     { pub file: String, pub matches: Vec<SearchMatch>, ... }
pub struct SearchCompressorConfig   { /* private */ }
pub struct SearchCompressionResult  { /* private */ }
```

Pipeline: parse lines → per-file `FileMatches` maps → score each match
against optional `context` query via `LineImportanceDetector` → top-K per
file with cluster-aware allocation → re-emit. CCR when per-cluster ratio
clears `min_compression_ratio_for_ccr` AND `original_match_count ≥
min_matches_for_ccr`.

**No parity fixtures exist for `SearchCompressor`** in upstream or in
`internal/tools/testdata/parity/`. Tests must use table-driven style.

## 12. SmartCrusher (`transforms/smart_crusher/`)

18 Rust files, ~5,000 LOC. Substantially more complex than the other
transforms. The Go port is **the single biggest slice** in the plan.

```rust
pub struct SmartCrusher {
    pub config: SmartCrusherConfig,
    pub anchor_selector: AnchorSelector,
    pub scorer: Box<dyn RelevanceScorer + Send + Sync>,
    pub analyzer: SmartAnalyzer,
    pub constraints: Vec<Box<dyn Constraint>>,
    pub observers: Vec<Box<dyn Observer>>,
    pub compaction: Option<CompactionStage>,
    pub ccr_store: Option<Arc<dyn CcrStore>>,
}
impl SmartCrusher {
    pub fn new(config: SmartCrusherConfig) -> Self;
    pub fn without_compaction(config: SmartCrusherConfig) -> Self;
    pub fn crush(&self, content: &str, query: &str, bias: f64) -> CrushResult;
    pub fn crush_array(&self, items: &[Value], query_context: &str, bias: f64) -> CrushArrayResult;
    // ... many more
}

pub struct SmartCrusherConfig {
    pub enabled: bool,
    pub dedup_identical_items: bool,
    pub factor_out_constants: bool,
    pub first_fraction: f64,
    pub last_fraction: f64,
    pub include_summaries: bool,
    pub max_items_after_crush: usize,
    pub min_items_to_analyze: usize,
    pub min_tokens_to_crush: usize,
    pub preserve_change_points: bool,
    pub similarity_threshold: f64,
    pub toin_confidence_threshold: f64,
    pub uniqueness_threshold: f64,
    pub use_feedback_hints: bool,
    pub variance_threshold: f64,
    // ... more
}
pub struct CrushResult       { pub compressed: String, pub original: String, pub was_modified: bool, pub strategy: String }
pub struct CrushArrayResult  { pub items: Vec<Value>, pub strategy_info: String,
                                pub ccr_hash: Option<String>, pub dropped_summary: String,
                                pub compacted: Option<String>, pub compaction_kind: Option<&'static str> }
pub enum CompressionStrategy { None, Skip, TimeSeries, ClusterSample, TopN, SmartSample }
pub enum ArrayType           { DictArray, StringArray, NumberArray, MixedArray, NestedArray, BoolArray, Empty }
```

**Defaults** (from 17 `smart_crusher/` fixtures):

```
enabled=true, dedup_identical_items=true, factor_out_constants=false,
first_fraction=0.3, last_fraction=0.15, include_summaries=false,
max_items_after_crush=15, min_items_to_analyze=5, min_tokens_to_crush=200,
preserve_change_points=true, similarity_threshold=0.8,
toin_confidence_threshold=0.5, uniqueness_threshold=0.1,
use_feedback_hints=true, variance_threshold=2.0
```

**Critical detail for parity:** the SmartCrusher's `input` field in
fixtures is nested `{content, query, bias}` (not flat string), and the
fixture is loaded by `SmartCrusher.without_compaction(cfg).crush(content,
query, bias)`. The Go port must call without_compaction when no
`CompactionStage` is wired in (it never will be in v1).

The dropped-marker format observed in fixtures:

```json
{"_ccr_dropped":"<<ccr:1d0dd94cf2cd 85_rows_offloaded>>"}
```

## 13. LogTemplate (`transforms/pipeline/reformats/log_template.rs`)

```rust
pub struct LogTemplate { config: LogTemplateConfig }
impl LogTemplate {
    pub fn new(config: LogTemplateConfig) -> Self;
}
impl ReformatTransform for LogTemplate {
    fn name(&self) -> &'static str { "log_template" }
    fn applies_to(&self) -> &[ContentType] { &[ContentType::BuildOutput] }
    fn apply(&self, content: &str) -> Result<ReformatOutput, TransformError>;
}
pub struct LogTemplateConfig {
    pub min_lines: usize,                // default 20
    pub min_run: usize,                  // default 3
    pub similarity_threshold: f32,       // default 0.4 (Drain default)
    pub min_constant_tokens: usize,      // default 2
}
const WILDCARD: &str = "<*>";
```

Drain-inspired simplified template miner — order-preserving, collapses
consecutive same-template lines into `[Template Tn: ...] (Nx)` + variant
table.

## 14. JsonMinifier (`transforms/pipeline/reformats/json_minifier.rs`)

```rust
pub struct JsonMinifier;
impl ReformatTransform for JsonMinifier {
    fn name(&self) -> &'static str { "json_minifier" }
    fn applies_to(&self) -> &[ContentType] { &[ContentType::JsonArray] }
    fn apply(&self, content: &str) -> Result<ReformatOutput, TransformError>;
}
```

`serde_json` round-trip; if minified output is longer than input, returns
the original (never inflates).

## 15. DiffNoise (`transforms/pipeline/offloads/diff_noise.rs`)

```rust
pub struct DiffNoise { config: DiffNoiseConfig }
impl DiffNoise {
    pub fn new(config: DiffNoiseConfig) -> Self;
}
impl OffloadTransform for DiffNoise {
    fn name(&self) -> &'static str { "diff_noise" }
    fn applies_to(&self) -> &[ContentType] { &[ContentType::GitDiff] }
    fn confidence(&self) -> f32 { 0.9 }
    // ... estimate_bloat + apply
}
pub struct DiffNoiseConfig {
    pub min_lines: usize,                          // default 30
    pub lockfile_suffixes: Vec<String>,            // default: Cargo.lock,
                                                  //   package-lock.json, yarn.lock,
                                                  //   pnpm-lock.yaml, poetry.lock,
                                                  //   Pipfile.lock, Gemfile.lock,
                                                  //   go.sum, composer.lock
    pub drop_whitespace_only_hunks: bool,          // default true
}
```

Output markers: `[diff_noise: lockfile hunks dropped (N lines)]` and
`[diff_noise CCR: hash=...]`.

## 16. `PipelineConfig` Schema (`transforms/pipeline/config.rs`)

```rust
pub struct PipelineConfig {
    pub pipeline: OrchestratorConfig,
    pub bloat:    BloatConfigs,
    pub reformat: ReformatConfigs,
    pub offload:  OffloadConfigs,
}
impl PipelineConfig {
    pub fn from_default_str() -> Self;
    pub fn from_toml_str(s: &str) -> Result<Self, ConfigError>;
    pub fn from_file(path: impl AsRef<Path>) -> Result<Self, ConfigError>;
}
impl Default for PipelineConfig { fn default() -> Self { Self::from_default_str() } }

pub struct OrchestratorConfig {
    pub reformat_target_ratio: f64,    // 0.5
    pub bloat_threshold: f32,          // 0.5
    pub offload_fallback_ratio: f64,   // 0.85
}
pub struct BloatConfigs    { log: LogBloatConfig, diff: DiffBloatConfig, search: SearchBloatConfig }
pub struct LogBloatConfig  { min_lines, sample_size, high_priority_threshold, uniqueness_weight, priority_dilution_weight }
pub struct DiffBloatConfig { min_lines, normal_context_ratio }
pub struct SearchBloatConfig { min_matches, cluster_threshold }
pub struct ReformatConfigs { log_template: LogTemplateConfig }
pub struct OffloadConfigs  { json: JsonOffloadConfig, diff_noise: DiffNoiseConfig }
pub struct JsonOffloadConfig { min_array_rows, saturation_rows }
pub struct DiffNoiseConfig { min_lines, lockfile_suffixes, drop_whitespace_only_hunks }
```

**Go port note:** JSON instead of TOML. `internal/config/config.go` already
loads JSON; no need to introduce a new format.

## 17. Module Re-exports (`transforms/mod.rs`)

Stable public surface re-exported by `headroom_core::transforms::*`:

```rust
CompressionPipeline, CompressionPipelineBuilder,
DiffNoise, DiffOffload, JsonMinifier, JsonOffload, LogOffload, LogTemplate,
OffloadOutput, OffloadTransform,
PipelineConfig, PipelineResult, ReformatOutput, ReformatTransform, TransformError,
CompressionContext, ContentType, DetectionResult,
is_json_array_of_dicts, detect_content_type,
DiffCompressionResult, DiffCompressor, DiffCompressorConfig, DiffCompressorStats,
LogCompressionResult, LogCompressor, LogCompressorConfig, LogCompressorStats,
LogFormat, LogLevel, LogLine,
FileMatches, SearchCompressionResult, SearchCompressor, SearchCompressorConfig,
SearchCompressorStats, SearchMatch,
```

## 18. File Structure of `tmp/headroom/`

```
tmp/headroom/
├── README.md                              (411 lines, Python/TS user-facing)
├── RUST_DEV.md                            (Rust dev guide)
├── crates/headroom-core/
│   ├── Cargo.toml
│   ├── config/pipeline.toml               (embedded defaults)
│   └── src/                               (70 .rs files, 41,316 LOC)
└── tests/
    ├── parity/
    │   ├── recorder.py                    (850 lines; canonical fixture schema)
    │   └── record_smart_crusher.py        (213 lines; smart_crusher-specific)
    └── test_transforms/
        ├── test_diff_compressor_rust_parity.py   (120 lines)
        └── test_smart_crusher_rust_parity.py     (92 lines)
```

**No Go-side reference implementation exists.** All porting is from Rust.