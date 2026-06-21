# Plan — Headroom-Style RTK Compactor Overhaul (Redo)

## How to read this plan

12 vertical slices, executed **serially**. Each slice produces a runnable
artifact. Each slice's gate exercises the slice's actual new code
(vacuous-pass protection). The implementer is expected to commit
non-empty work for each slice before moving on.

**Branch:** continue on the current `fix/acp-dedup-and-cleanup` branch
(or cut a feature branch off it; the work is additive, not a fix).

**Working directory:** `/Users/dimetron/p6s/pi-dev/pi-go`.

**Tooling:** Go 1.26.3, stdlib only. No new external deps.

**Per-slice workflow:**

1. Read the slice's "Files" section.
2. Read the slice's "Implementation" notes.
3. Implement + write tests.
4. Run the slice's gate command.
5. Run the slice-gate guard (4 checks).
6. Commit (single commit per slice, message: `compactor: <slice-name>`).
7. Move to the next slice.

**Do not** batch multiple slices into one commit. The failure mode
we're guarding against is exactly the silent loss of work — keeping
commits small and per-slice makes any silent loss trivially visible.

---

## Slice 1 — CCR Store + Content Detection

**Files:**

- `internal/tools/ccr.go` (new)
- `internal/tools/ccr_test.go` (new)
- `internal/tools/content_type.go` (new)
- `internal/tools/content_type_test.go` (new)

**Implementation:**

`internal/tools/ccr.go`:

- `package tools`
- Imports: `crypto/sha256`, `encoding/hex`, `sync`, `time`
- `type CCRStore interface { Put(hash, payload string); Get(hash string) (string, bool); Len() int }`
- `type ccrEntry struct { payload string; ts time.Time }`
- `type InMemoryCCRStore struct { mu sync.RWMutex; data map[string]ccrEntry; capacity int; ttl time.Duration }`
- `const defaultCCRCapacity = 1000; const defaultCCRTtl = 30 * time.Minute`
- `func NewInMemoryCCRStore() *InMemoryCCRStore` — uses defaults
- `func (s *InMemoryCCRStore) Put(hash, payload string)` — locks write,
  inserts, evicts oldest until under capacity (LRU-ish via FIFO map
  iteration; sufficient for v1)
- `func (s *InMemoryCCRStore) Get(hash string) (string, bool)` — locks
  read, returns `(payload, true)` only if entry exists AND
  `time.Since(entry.ts) < s.ttl`. Lazy expiry: expired entries removed
  on read.
- `func (s *InMemoryCCRStore) Len() int` — locks read, returns
  `len(s.data)`
- `func ComputeCCRKey(payload []byte) string` — SHA-256, hex, first 24
  chars. Comment: `// SHA-256[:24] used instead of BLAKE3[:24] — internal
  // hash algorithm; no parity impact (CCR keys are not in fixtures).`
- `func CCRMarker(hash string) string` — `fmt.Sprintf("<<ccr:%s>>", hash)`

`internal/tools/ccr_test.go`:

- `TestComputeCCRKey_Deterministic` — same payload → same hash
- `TestComputeCCRKey_Length24` — hash is exactly 24 hex chars
- `TestCCRMarker_Format` — `CCRMarker("abc123")` == `"<<ccr:abc123>>"`
- `TestInMemoryCCRStore_PutGet` — round-trip
- `TestInMemoryCCRStore_GetMissing` — returns `("", false)`
- `TestInMemoryCCRStore_Len` — empty=0, after Put=1
- `TestInMemoryCCRStore_Expiry` — set ttl=10ms, sleep 20ms, Get returns
  `false`
- `TestInMemoryCCRStore_Capacity` — Put N+5 entries, Len == N

`internal/tools/content_type.go`:

- `package tools`
- Imports: `regexp`, `strings`, `sync`
- `type ContentType int` with iota constants:
  `ContentPlainText, ContentBuildOutput, ContentGitDiff,
  ContentSearchResults, ContentJsonArray, ContentSourceCode, ContentHTML`
- `func (c ContentType) String() string` — matches headroom's `as_str()`:
    - `ContentPlainText` → `"text"`
    - `ContentBuildOutput` → `"build"`
    - `ContentGitDiff` → `"diff"`
    - `ContentSearchResults` → `"search"`
    - `ContentJsonArray` → `"json_array"`
    - `ContentSourceCode` → `"source_code"`
    - `ContentHTML` → `"html"`
- `type DetectionResult struct { Type ContentType; Confidence float64; Metadata map[string]any }`
- Use `sync.OnceValue[[]compiledPattern]` to compile regexes once.
- Compiled patterns (in detection order — first match wins):
    1. `^diff --(combined|cc|git)|^@@[ @]|^--- a/|^+++ b/` → `ContentGitDiff`
    2. `^<(!DOCTYPE|html|head|body|/html)` → `ContentHTML`
    3. `^\s*[\{\[]\s*\{` (looks like JSON object/array start) AND passes
       `json.Valid` on first 200 chars → `ContentJsonArray`
    4. `^[a-zA-Z_/\.][a-zA-Z0-9_/\.\-]*:\d+:` (file:line: pattern) →
       `ContentSearchResults`
    5.
    `^(pytest|ERROR|FAIL|PASS|WARN|INFO|DEBUG|=== RUN|--- PASS|--- FAIL|ok\s|FAIL\s|-----|\d+ passed|\d+ failed|\[INFO\]|\[ERROR\]|\[WARN\])` →
    `ContentBuildOutput`
    6. `^(package |import |func |class |def |public |private |const |var |let )` →
       `ContentSourceCode`
    7. Default → `ContentPlainText`
- `func DetectContentType(content string) DetectionResult` — runs
  pattern matching on the first ~100 lines (split by `\n`, take first
  100). Returns confidence 1.0 for matched, 0.5 for default.
- `func DetectFromToolName(toolName string) ContentType` — simple map:
    - `bash`, `read`, `find`, `tree`, `git_file_diff`, `git_overview`,
      `git_hunk` → `ContentPlainText` (default; the content detector
      will override)
    - `grep` → `ContentSearchResults` hint
    - nothing else — fall through to content detection
- `func IsJSONArrayOfDicts(content string) bool` — tries `json.Unmarshal`
  into `[]map[string]any` on first 500 chars

`internal/tools/content_type_test.go`:

- 21 parity fixtures loaded via `//go:embed testdata/parity/content_detector`
  in a separate `parity_content_detector_test.go` file OR via
  `filepath.Walk` (no embed needed)
- For each fixture: `t.Run(name, func(t *testing.T) { ... })`
- Read JSON, extract `input`, run `DetectContentType(input)`, assert
  `result.Type.String() == fixture.Output.ContentType` byte-for-byte
- Also assert confidence ≥ 0.5 (we don't strict-assert confidence since
  fixtures may pin it at 1.0 but Go may report slightly differently)
- Plus 2 unit tests:
    - `TestDetectContentType_Diff` — sample diff input → `ContentGitDiff`
    - `TestDetectContentType_HTML` — sample HTML → `ContentHTML`

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "CCR|ContentType" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 21 content_detector parity tests pass + 2 unit tests + 8 CCR
tests. Total: ≥31 passing subtests.

---

## Slice 2 — Transform Traits + Orchestrator

**Files:**

- `internal/tools/transform.go` (new)
- `internal/tools/transform_test.go` (new)
- `internal/tools/orchestrator.go` (new)
- `internal/tools/orchestrator_test.go` (new)

**Implementation:**

`internal/tools/transform.go`:

- `package tools`
-
`type ReformatTransform interface { Name() string; AppliesTo() []ContentType; Apply(content string) (ReformatOutput, error) }`
- `type ReformatOutput struct { Output string; BytesSaved int }`
-
`type OffloadTransform interface { Name() string; AppliesTo() []ContentType; EstimateBloat(content string) float32; Apply(content string, ctx CompressionContext, store CCRStore) (OffloadOutput, error); Confidence() float32 }`
- `type OffloadOutput struct { Output string; BytesSaved int; CacheKey string }`
- `type CompressionContext struct { Query string; TokenBudget int }`
- `type TransformErrorKind int` with `TransformInvalidInput`,
  `TransformSkipped`, `TransformInternal`
- `type TransformError struct { Transform string; Kind TransformErrorKind; Message string }`
- `(e *TransformError) Error() string` returns
  `fmt.Sprintf("%s: %s", e.Transform, e.Message)`

`internal/tools/transform_test.go`:

- Compile-time interface conformance tests using
  `var _ ReformatTransform = (*stubReformat)(nil)` and same for
  `OffloadTransform`
- `TestTransformError_Error` — message formatting

`internal/tools/orchestrator.go`:

- `package tools`
- Imports: `sync`, `log`, `strings`
- `type OrchestratorConfig struct { ReformatTargetRatio float64; BloatThreshold float32; OffloadFallbackRatio float64 }`
- `func DefaultOrchestratorConfig() OrchestratorConfig { return OrchestratorConfig{0.5, 0.5, 0.85} }`
- `type PipelineResult struct { Output string; BytesSaved int; StepsApplied []string; CacheKeys []string }`
-
`type CompressionPipeline struct { reformats []ReformatTransform; offloads []OffloadTransform; config OrchestratorConfig; store CCRStore }`
-
`type CompressionPipelineBuilder struct { reformats []ReformatTransform; offloads []OffloadTransform; config OrchestratorConfig }`
-
`func NewCompressionPipelineBuilder() *CompressionPipelineBuilder { return &CompressionPipelineBuilder{config: DefaultOrchestratorConfig()} }`
-
`(b *CompressionPipelineBuilder) WithReformat(t ReformatTransform) *CompressionPipelineBuilder { b.reformats = append(b.reformats, t); return b }`
-
`(b *CompressionPipelineBuilder) WithOffload(t OffloadTransform) *CompressionPipelineBuilder { b.offloads = append(b.offloads, t); return b }`
-
`(b *CompressionPipelineBuilder) WithConfig(c OrchestratorConfig) *CompressionPipelineBuilder { b.config = c; return b }`
-
`(b *CompressionPipelineBuilder) Build(store CCRStore) *CompressionPipeline { return &CompressionPipeline{reformats: b.reformats, offloads: b.offloads, config: b.config, store: store} }`
- `(p *CompressionPipeline) Run(content string, ct ContentType, ctx CompressionContext) PipelineResult`:
    - If `content == ""`, return empty `PipelineResult{}`.
    - Spawn goroutine A: `reformattedContent, steps := p.runReformats(content, ct)`
    - Spawn goroutine B: `bloats := p.estimateBloat(content, ct)`
    - Use `sync.WaitGroup` to wait for both.
    - For each offload whose `AppliesTo()` contains `ct`:
        - If `bloats[name] >= p.config.BloatThreshold`, run it
        - OR if `len(reformattedContent)/len(content) > p.config.OffloadFallbackRatio && bloats[name] > 0`, run the
          highest-confidence such offload
    - Each successful offload's `CacheKey` appended to `result.CacheKeys`.
    - `result.Output = reformattedContent` (or offload output if any fired).
    - `result.StepsApplied = steps`.
    - `result.BytesSaved = len(content) - len(result.Output)`.
- Helper `(p *CompressionPipeline) runReformats(content string, ct ContentType) (string, []string)`:
    - Sequential loop over `p.reformats`. For each whose `AppliesTo()`
      contains `ct`, call `Apply`. On error of kind `Skipped` or
      `InvalidInput`, log trace + continue. On `Internal`, log warn +
      continue. If successful and `len(current)/len(content) <=
    p.config.ReformatTargetRatio`, stop early.
- Helper `(p *CompressionPipeline) estimateBloat(content string, ct ContentType) map[string]float32`:
    - For each offload whose `AppliesTo()` contains `ct`, call
      `EstimateBloat(content)`. Map name → bloat. Empty/very short inputs
      return `0.0` for all (offloads self-protect via empty-input check).

`internal/tools/orchestrator_test.go`:

- `TestOrchestrator_EmptyInput` — returns empty result
- `TestOrchestrator_NoTransforms` — passes content through unchanged
- `TestOrchestrator_ReformatOnly_StopsAtTargetRatio` — stub reformat
  that halves content; second stub reformat not called when target met
- `TestOrchestrator_OffloadOnly_BelowThreshold_Skipped` — bloat < threshold
- `TestOrchestrator_Offload_AboveThreshold_Fires` — bloat ≥ threshold,
  Apply called
- `TestOrchestrator_Offload_FallbackRatio` — bloat > 0 but below
  threshold, but post-reformat ratio > fallback ratio → highest-
  confidence offload fires
- `TestOrchestrator_Parallel_ReformatAndBloat` — verifies both ran
  (use a reformat that sleeps 10ms and an offload that records its
  bloat call)
- `TestOrchestrator_AppliesTo_Filter` — content type mismatch, offload
  not called
- `TestOrchestrator_SkippedError_Continues` — offload returns
  `Skipped`, next offload still called
- `TestOrchestrator_InternalError_Continues` — same as above for
  `Internal`

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "Orchestrator|Pipeline|Transform" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 13+ stub-based tests pass.

---

## Slice 3 — Line Importance Detection + Adaptive Sizer

**Files:**

- `internal/tools/signals.go` (new)
- `internal/tools/signals_test.go` (new)
- `internal/tools/adaptive_sizer.go` (new)
- `internal/tools/adaptive_sizer_test.go` (new)

**Implementation:**

`internal/tools/signals.go`:

- `package tools`
- `type ImportanceContext int` with `ImportanceText, ImportanceSearch, ImportanceDiff, ImportanceLog`
- `func (c ImportanceContext) String() string` — `"text"|"search"|"diff"|"log"`
- `type ImportanceCategory int` with
  `ImportanceCategoryError, ImportanceCategoryWarning, ImportanceCategorySecurity, ImportanceCategoryImportance, ImportanceCategoryMarkdown`
- `func (c ImportanceCategory) String() string` — `"error"|"warning"|"security"|"importance"|"markdown"`
- `type ImportanceSignal struct { Category *ImportanceCategory; Priority float32; Confidence float32 }`
- `func (s ImportanceSignal) IsMatch() bool { return s.Category != nil }`
-
`const ( ErrorPriority = float32(0.95); SecurityPriority = float32(0.85); WarningPriority = float32(0.75); ImportancePriority = float32(0.60); MarkdownPriority = float32(0.45); KeywordConfidence = float32(0.70) )`
- `type LineImportanceDetector interface { Score(line string, ctx ImportanceContext) ImportanceSignal }`
-
`type KeywordRegistry struct { Error []string; Warning []string; Importance []string; Security []string; MarkdownPrefixes []string; ErrorIndicators []string }`
- `func DefaultKeywordRegistry() KeywordRegistry` — exact headroom
  content (copy verbatim from design §signals.go)
- `type KeywordDetector struct { registry KeywordRegistry }`
- `func NewKeywordDetector() *KeywordDetector` — uses `DefaultKeywordRegistry()`
- `(d *KeywordDetector) Registry() KeywordRegistry` — getter
- `(d *KeywordDetector) Score(line string, ctx ImportanceContext) ImportanceSignal`:
    - Lowercase line once
    - Check `MarkdownPrefixes` if `ctx == ImportanceText` and line starts
      with one of them → `ImportanceMarkdown` priority
    - Check `Error` keywords with word-boundary post-filter → `Error`
    - Check `Warning` keywords if `ctx != ImportanceDiff` → `Warning`
    - Check `Security` keywords if `ctx == ImportanceDiff` → `Security`
    - Check `Importance` keywords → `Importance`
    - Word-boundary filter: `isWordBoundary(line, idx, len)` returns true
      if `idx == 0 || idx+len == len(line) || !isAlnum(line[idx-1]) ||
    !isAlnum(line[idx+len])`. Helper:
      `func isAlnum(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' }`
    - First match wins. Returns signal with confidence `KeywordConfidence`.
    - No match → `ImportanceSignal{Category: nil, Priority: 0, Confidence: 0}`
- `(d *KeywordDetector) ContainsErrorIndicator(text string) bool` —
  simple `strings.Contains` scan over `ErrorIndicators`. No word-
  boundary filter (substring match per headroom).

`internal/tools/signals_test.go`:

- `TestDefaultKeywordRegistry_ExactContent` — assert the registry
  matches headroom's content byte-for-byte (literal string compare
  via `reflect.DeepEqual` after constructing expected slice)
- `TestKeywordDetector_Error` — `"ERROR: foo"` → `ErrorPriority`
- `TestKeywordDetector_Warning` — `"warn: x"` → `WarningPriority`
  (in Log context)
- `TestKeywordDetector_Security` — `"security check"` → `SecurityPriority`
  (in Diff context)
- `TestKeywordDetector_Importance` — `"TODO: fix"` → `ImportancePriority`
- `TestKeywordDetector_MarkdownPrefix` — `"# heading"` in Text →
  `MarkdownPriority`; same line in Log context → no match
- `TestKeywordDetector_WordBoundary_PanickerNoMatch` — `"panicker"` does
  NOT score Error
- `TestKeywordDetector_WordBoundary_PanicMatches` — `"panic"` (alone)
  scores Error
- `TestKeywordDetector_SecurityNotInTextContext` — `"security check"`
  in Text context → no match (security dropped in Text)
- `TestKeywordDetector_WarningNotInDiffContext` — `"warning"` in Diff
  context → no match
- `TestKeywordDetector_ContainsErrorIndicator_Substring` — `"got error"`
  → true; `"got errors"` → true; `"got terror"` → true (substring-only)

`internal/tools/adaptive_sizer.go`:

- `package tools`
- Imports: `bytes`, `compress/flate`, `crypto/md5`, `encoding/binary`,
  `math`, `sort`, `strings`
- `func ComputeUniqueBigramCurve(items []string) []int`:
    - `curve[0] = 0`
    - For `i := 1; i <= len(items); i++`, compute count of unique bigrams
      in first `i` items. Bigram = (item[i-1], item[i]) for i < n.
      Actually re-reading headroom: curve is the cumulative unique count
      across the items. Use bigram-of-items: `seen := map[[2]string]bool{};
    curve := make([]int, len(items)+1); for i, item := range items { for
    _, w := range bigrams(item) { seen[w] = true }; curve[i+1] =
    len(seen) }`. Where `bigrams(s) = [][2]string{{s[0], s[1]}, ...}`
      on **characters** of each item. So curve[i] = number of unique
      character bigrams seen in items[0..i].
- `func FindKnee(curve []int) (int, bool)`:
    - If `len(curve) < 3`, return `(0, false)`
    - Normalize curve to `[0, 1]` x `[0, 1]`
    - For each point, compute perpendicular distance to diagonal
      `(i, curve[i])` vs `(i/n, curve[i]/max)`
    - If max distance `< 0.05`, return `(0, false)`
    - If curve is flat (max == 0), return `(1, true)`
    - Else return `(kneeIdx + 1, true)` where kneeIdx = argmax distance
- `func Simhash(text string) uint64`:
    - Generate character 4-grams of `text`
    - For each 4-gram, compute `md5.Sum([]byte(gram))`, take first 8
      bytes as `uint64`
    - For each of 64 bits, sum the bits of all hashes weighted by
      1 (or use raw binary add: `if bit set, +1; else, -1`)
    - Sign of each sum gives the simhash bit
    - Return simhash as `uint64`
- `func HammingDistance(a, b uint64) uint32`:
    - `x := a ^ b; return uint32(bits.OnesCount64(x))`
- `func CountUniqueSimhash(items []string, threshold uint32) int`:
    - Compute simhash for each item
    - Greedy cluster: for each item not yet clustered, count items within
      `threshold` hamming distance. Each cluster counts as 1 unique.
- `func ValidateWithZlib(items []string, k, maxK int, tolerance float64) int`:
    - `fullCompressed := zlibLen(strings.Join(items, ""))`
    - `subsetCompressed := zlibLen(strings.Join(items[:k], "")) * len(items) / k`
    - If `subsetCompressed < fullCompressed * (1 - tolerance)`, return
      `min(k*5/4, maxK)`. Else return `k`.
- `func ComputeOptimalK(items []string, bias float64, minK, maxK int) int`:
    - `n := len(items)`
    - If `n <= 8`, return `n`
    - `unique := CountUniqueSimhash(items, 10)`
    - If `unique <= 3`, return `unique`
    - `curve := ComputeUniqueBigramCurve(items)`
    - `knee, ok := FindKnee(curve)`
    - If !ok, knee = n / 2
    - `validated := ValidateWithZlib(items, knee, maxK, 0.2)`
    - `effectiveMax := maxK`
    - If `effectiveMax <= 0`, `effectiveMax = n`
    - `if validated < minK { return minK }`
    - `if validated > effectiveMax { return effectiveMax }`
    - return `validated`
- Helper `func zlibLen(s string) int`:
    - `var buf bytes.Buffer`
    - `w, _ := flate.NewWriter(&buf, flate.DefaultCompression)`
    - `w.Write([]byte(s))`
    - `w.Close()`
    - return `buf.Len()`

`internal/tools/adaptive_sizer_test.go`:

- `TestComputeUniqueBigramCurve_Empty` — `[]int{0}`
- `TestComputeUniqueBigramCurve_OneItem` — depends on string
- `TestFindKnee_FlatCurve` — `[1, 1, 1, 1]` → `(1, true)`
- `TestFindKnee_SteepCurve` — `[0, 10, 20, 30]` → high knee index
- `TestFindKnee_TooShort` — `[0, 1]` → `(0, false)`
- `TestSimhash_Deterministic` — same text → same hash
- `TestSimhash_DifferentTexts_DifferentHashes` — high probability
- `TestHammingDistance_Same` — `HammingDistance(x, x) == 0`
- `TestHammingDistance_Different` — `HammingDistance(0, ^uint64(0)) == 64`
- `TestCountUniqueSimhash_AllSame` — `["a", "a", "a"]` → 1
- `TestCountUniqueSimhash_AllDifferent` — `["a", "b", "c"]` → 3
- `TestComputeOptimalK_SmallN` — `n=5` → returns 5
- `TestComputeOptimalK_NearRedundant` — 100 same items → low K
- `TestComputeOptimalK_Normal` — 50 diverse items → K between 10 and 50

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "Keyword|Adaptive|Signal|Sizer" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 20+ tests pass.

---

## Slice 4 — LogCompressor

**Files:**

- `internal/tools/log_compressor.go` (new)
- `internal/tools/log_compressor_test.go` (new)

**Implementation:**

`internal/tools/log_compressor.go`:

- `package tools`
- Imports: `fmt`, `regexp`, `sort`, `strings`
- `type LogFormat int` with `LogPytest, LogNpm, LogCargo, LogJest, LogMake, LogGeneric` (iotal order matches headroom)
- `func (f LogFormat) String() string` — `"pytest"|"npm"|"cargo"|"jest"|"make"|"generic"`
- `type LogLevel int` with `LogError, LogFail, LogWarn, LogInfo, LogDebug, LogTrace, LogUnknown`
- `func (l LogLevel) String() string` — `"error"|"fail"|"warn"|"info"|"debug"|"trace"|"unknown"`
-
`type LogLine struct { LineNumber int; Content string; Level LogLevel; IsStackTrace bool; IsSummary bool; Score float32 }`
-
`type LogCompressorConfig struct { MaxErrors int; ErrorContextLines int; KeepFirstError bool; KeepLastError bool; MaxStackTraces int; StackTraceMaxLines int; MaxWarnings int; DedupeWarnings bool; KeepSummaryLines bool; MaxTotalLines int; EnableCCR bool; MinLinesForCCR int; MinCompressionRatioForCCR float64 }`
- `func DefaultLogCompressorConfig() LogCompressorConfig` — matches fixture defaults exactly
-
`type LogCompressionResult struct { Compressed string; Original string; OriginalLineCount int; CompressedLineCount int; FormatDetected LogFormat; CompressionRatio float64; CacheKey string; Stats map[string]uint64 }`
-
`type LogCompressorStats struct { Format LogFormat; StackTracesSeen int; StackTracesKept int; WarningsDroppedByDedupe int; LinesDroppedByGlobalCap int; CCREmitted bool; CCRSkipReason string }`
- `type LogCompressor struct { cfg LogCompressorConfig; importance *KeywordDetector }`
-
`func NewLogCompressor(cfg LogCompressorConfig) *LogCompressor { return &LogCompressor{cfg: cfg, importance: NewKeywordDetector()} }`
- `func (c *LogCompressor) Config() LogCompressorConfig { return c.cfg }`
- Format detection patterns (compiled via `sync.OnceValue`):
    - pytest: `^=+( test session starts|=+ FAILURES|=+ ERRORS|=+ warnings| short test summary|PASSED|FAILED|=+ )`
    - npm: `^npm (WARN|ERR!|HTTP|notice) |^added \d+ packages|^removed \d+ packages`
    - cargo: `^(error\[|warning:|note:|  --> )| Compiling| Finished|error: `
    - jest: `^(PASS |FAIL |\s✓|\s✗|\s✕)`
    - make: `^(make\[\d+\]:|make: )`
    - default: anything else → Generic
- Level classification (per line, lowercased):
    - starts with `traceback `, `panic`, `fatal `, `error`, `exception` → Error
    - starts with `fail`, `failed`, `failure` → Fail
    - starts with `warn`, `warning` → Warn
    - starts with `info` → Info
    - starts with `debug` → Debug
    - starts with `trace` → Trace
    - else → Unknown
- Stack trace: line `^\s+File "`, `^\s+at `, or starts with whitespace AND previous line was Error/Fail
- Summary line: matches `(\d+ (passed|failed|skipped))|Test Summary:|=== RUN|--- PASS|--- FAIL|test result:|Tests: \d+`
- Scoring:
    - Error = 1.0, Fail = 1.0, Warn = 0.7, Info = 0.3, Debug = 0.2,
      Trace = 0.1, Unknown = 0.4
    - Stack trace = 0.5
    - Summary = 0.6
    - Multiply by line's KeywordDetector score boost: if Score returns
      Error priority, add 0.5; Warning, add 0.2
- `func (c *LogCompressor) Compress(content string, bias float64) (LogCompressionResult, LogCompressorStats, error)`:
    - Detect format
    - Parse lines into `[]LogLine`
    - Classify + score
    - **Gate:** if `len(lines) < c.cfg.MinLinesForCCR`, return
      `LogCompressionResult{Compressed: content, Original: content,
      OriginalLineCount: len(lines), CompressedLineCount: len(lines),
      FormatDetected: format, CompressionRatio: 1.0, CacheKey: "",
      Stats: {}}`. Note: this is the misnomer behavior preserved from
      headroom.
    - Select lines: errors first/last (if config flags), top-N errors by
      score, fails, deduped warnings (if DedupeWarnings), top-M stack
      traces, summary lines
    - Apply `MaxTotalLines` cap via `ComputeOptimalK` on scored lines,
      bias-adjusted (clamp to `[1, MaxTotalLines]`)
    - Sort selected lines by `LineNumber` to preserve order
    - Join with `\n`. Compute `CompressedLineCount` and
      `CompressionRatio = CompressedLineCount / OriginalLineCount`
    - If `EnableCCR && CompressionRatio < MinCompressionRatioForCCR`:
      `hash := ComputeCCRKey([]byte(content))`; if `store != nil`,
      `store.Put(hash, content)`; set `CacheKey = hash`; append
      `fmt.Sprintf("[%d lines compressed to %d. Retrieve more: hash=%s]",
      OriginalLineCount, CompressedLineCount, hash)` to output
    - Return result + stats
-
`func (c *LogCompressor) CompressWithStore(content string, bias float64, store CCRStore) (LogCompressionResult, LogCompressorStats, error)`:
    - Same as Compress, but pass `store` to the CCR section

`internal/tools/log_compressor_test.go`:

- Load 20 fixtures via `filepath.Walk("testdata/parity/log_compressor")`
- For each: `t.Run(name, func(t *testing.T) { ... })`
- Steps:
    1. `rec := readJSON(path)`
    2. `cfg := unmarshalConfig(rec.Config)`
    3. `lc := NewLogCompressor(cfg)`
    4. `result, _, err := lc.Compress(rec.Input, 0.5)`
    5. `if err != nil { t.Fatal(err) }`
    6. Assert `result.Compressed == rec.Output.Compressed` (byte-for-byte)
    7. Assert `result.OriginalLineCount == rec.Output.OriginalLineCount`
    8. Assert `result.CompressedLineCount == rec.Output.CompressedLineCount`
    9. Assert `result.CompressionRatio == rec.Output.CompressionRatio` (use
       `math.Abs(diff) < 1e-9`)
    10. Assert `result.FormatDetected.String() == rec.Output.FormatDetected`
    11. Assert `(result.CacheKey == "") == (rec.Output.CacheKey == nil)`
- Plus unit tests:
    - `TestLogCompressor_BelowMinLines_PassesThrough` — input < 50 lines,
      returns unchanged
    - `TestLogCompressor_FormatDetection_Pytest` — sample pytest log
    - `TestLogCompressor_FormatDetection_Npm`
    - `TestLogCompressor_FormatDetection_Cargo`
    - `TestLogCompressor_StackTraceDetection` — 5 lines, 1 is stack trace
    - `TestLogCompressor_PreservesErrors` — 100-line log with 2 errors at
      different positions, both in output

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "LogCompressor" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 20 parity tests + 6 unit tests = 26+ passing.

---

## Slice 5 — DiffCompressor + DiffNoise

**Files:**

- `internal/tools/diff_compressor.go` (new)
- `internal/tools/diff_compressor_test.go` (new)
- `internal/tools/diff_noise.go` (new)
- `internal/tools/diff_noise_test.go` (new)

**Implementation:**

`internal/tools/diff_compressor.go`:

- `package tools`
- Imports: `bufio`, `fmt`, `regexp`, `sort`, `strings`
-
`type DiffCompressorConfig struct { MaxContextLines int; MaxHunksPerFile int; MaxFiles int; AlwaysKeepAdditions bool; AlwaysKeepDeletions bool; EnableCCR bool; MinLinesForCCR int; MinCompressionRatioForCCR float64 }`
- `func DefaultDiffCompressorConfig() DiffCompressorConfig` — matches fixture defaults
-
`type DiffCompressionResult struct { Compressed string; OriginalLineCount int; CompressedLineCount int; FilesAffected int; Additions int; Deletions int; HunksKept int; HunksRemoved int; CacheKey string }`
- `type DiffCompressorStats struct { ... }` — observability only, not in parity fixture output
- `type DiffCompressor struct { cfg DiffCompressorConfig }`
- `func NewDiffCompressor(cfg DiffCompressorConfig) *DiffCompressor`
- Diff parsing:
    - `^diff --git a/(.+) b/(.+)$` → file header, start new segment
    - `^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@` → hunk header
    - `^ ` (space) → context
    - `^\+` (plus, not `+++`) → addition
    - `^-` (minus, not `---`) → deletion
    - `^--- a/` → old file marker (skip)
    - `^\+\+\+ b/` → new file marker (skip)
    - Other → "no newline" markers, index lines, etc. (preserve)
- `type diffSegment struct { OldPath, NewPath string; Hunks []diffHunk; HeaderLines []string }`
- `type diffHunk struct { OldStart, OldCount, NewStart, NewCount int; Lines []diffLine }`
- `type diffLine struct { Kind int  // context, add, del; Content string }`
- `func (c *DiffCompressor) Compress(content, context string) (DiffCompressionResult, error)`:
    - Parse into segments/hunks/lines
    - `OriginalLineCount = len(strings.Split(content, "\n"))`
    - **Gate:** if `OriginalLineCount < c.cfg.MinLinesForCCR`, return
      result with `Compressed == content`, all counts == 0 (or matching
      input), `CacheKey == ""`
    - For each segment:
        - Score hunks against `context` query (using `KeywordDetector`)
        - Keep first/last/top-N by score, capped at `MaxHunksPerFile`
        - For each kept hunk, trim context to `MaxContextLines` on each
          side, **except** preserve `+`/`-` lines unconditionally if
          `AlwaysKeepAdditions`/`AlwaysKeepDeletions`
        - Drop non-kept hunks (increment `HunksRemoved`)
    - Cap total segments at `MaxFiles` by total-changes-desc
    - Re-emit: for each kept segment, emit header + kept hunks
    - `CompressedLineCount = len(strings.Split(compressed, "\n"))`
    - `CompressionRatio = float64(CompressedLineCount) / float64(OriginalLineCount)`
    - If `EnableCCR && CompressionRatio < MinCompressionRatioForCCR`:
      `hash := ComputeCCRKey([]byte(content))`; set `CacheKey = hash`;
      append `fmt.Sprintf("[%d lines compressed to %d. Retrieve more:
    hash=%s]", OriginalLineCount, CompressedLineCount, hash)` to
      compressed output
- `func (c *DiffCompressor) CompressWithStore(content, context string, store CCRStore) (DiffCompressionResult, error)`:
    - Same as Compress but pass `store` to CCR section

`internal/tools/diff_compressor_test.go`:

- 27 parity fixtures via `filepath.Walk("testdata/parity/diff_compressor")`
- For each: parse, compress, assert all 9 output fields byte-for-byte
- Unit tests:
    - `TestDiffCompressor_BelowMinLines_PassesThrough`
    - `TestDiffCompressor_TrimsContextTo2`
    - `TestDiffCompressor_KeepsAdditions`
    - `TestDiffCompressor_MultipleFiles`

`internal/tools/diff_noise.go`:

- `package tools`
- `type DiffNoiseConfig struct { MinLines int; LockfileSuffixes []string; DropWhitespaceOnlyHunks bool }`
- `func DefaultDiffNoiseConfig() DiffNoiseConfig` — exact headroom defaults
- `type DiffNoise struct { cfg DiffNoiseConfig }`
- `func NewDiffNoise(cfg DiffNoiseConfig) *DiffNoise`
- `(n *DiffNoise) Name() string { return "diff_noise" }`
- `(n *DiffNoise) AppliesTo() []ContentType { return []ContentType{ContentGitDiff} }`
- `(n *DiffNoise) Confidence() float32 { return 0.9 }`
- `(n *DiffNoise) EstimateBloat(content string) float32`:
    - Walk hunks, count hunks touching lockfile paths or whitespace-only.
    - `bloat = touchedHunks / totalHunks`, 0 if `< MinLines`
- `(n *DiffNoise) Apply(content string, ctx CompressionContext, store CCRStore) (OffloadOutput, error)`:
    - Parse diff, drop lockfile hunks (entire segment body, keep header),
      drop whitespace-only hunks if `DropWhitespaceOnlyHunks`
    - Re-emit. If anything dropped, hash full content, store via
      `store.Put(hash, content)`, append
      `[diff_noise: lockfile hunks dropped (N lines)]` and
      `[diff_noise CCR: hash=HASH]` markers
    - Return `OffloadOutput{Output: rebuilt, CacheKey: hash}`

`internal/tools/diff_noise_test.go`:

- `TestDiffNoise_LockfileDropped` — Cargo.lock hunk dropped
- `TestDiffNoise_WhitespaceOnlyDropped` — hunk with only whitespace
  changes dropped
- `TestDiffNoise_NoDrop_ReturnsContent` — non-noise diff passes through
- `TestDiffNoise_BelowMinLines_NoDrop`

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "Diff" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 27 parity tests + 4 DiffCompressor unit tests + 4 DiffNoise
unit tests = 35+ passing.

---

## Slice 6 — SearchCompressor

**Files:**

- `internal/tools/search_compressor.go` (new)
- `internal/tools/search_compressor_test.go` (new)

**Implementation:**

`internal/tools/search_compressor.go`:

- `package tools`
- `type SearchMatch struct { File string; LineNumber int64; Content string; Score float32; IsContext bool }`
- `func NewSearchMatch(file string, line int64, content string) *SearchMatch`
- `type FileMatches struct { File string; Matches []SearchMatch; FileScore float32 }`
- `func (f *FileMatches) First() *SearchMatch { ... }`
- `func (f *FileMatches) Last() *SearchMatch { ... }`
- `func (f *FileMatches) TotalScore() float32 { ... }`
-
`type SearchCompressorConfig struct { MinMatchesForCCR int; MinCompressionRatioForCCR float64; MaxPerFile int; AdaptiveTotal bool; GroupByFile bool }`
- `func DefaultSearchCompressorConfig() SearchCompressorConfig` — defaults:
  `MinMatchesForCCR=10, MinCompressionRatioForCCR=0.5, MaxPerFile=20, AdaptiveTotal=true, GroupByFile=true`
-
`type SearchCompressionResult struct { Compressed string; OriginalMatchCount int; CompressedMatchCount int; CacheKey string; FilesAffected int }`
- `type SearchCompressorStats struct { ... }` — observability only
- `type SearchCompressor struct { cfg SearchCompressorConfig; importance *KeywordDetector }`
- `func NewSearchCompressor(cfg SearchCompressorConfig) *SearchCompressor`
- `func (c *SearchCompressor) WithDetector(d *KeywordDetector) *SearchCompressor { c.importance = d; return c }`
- Parse line: `^(.+?):(\d+):(.*)$` → `(file, lineNumber, content)`. If
  no match, treat as `(filename, 0, raw)` if previous line matched.
- Cluster per file → `map[string]*FileMatches`
- Score each match:
    - Base score from `KeywordDetector.Score(content, ImportanceSearch)`
    - Boost if matches `ctx.Query` substring
    - Context lines (whitespace-prefixed) get score 0.1
- Select top-K per file:
    - If `AdaptiveTotal`: `K = ComputeOptimalK(allScores, bias, 5, MaxPerFile)`
    - Else: `K = MaxPerFile`
- Format output:
    - If `GroupByFile`: group by file, emit header `# <filename> (N matches)`, then lines
    - Else: flat list
-
`func (c *SearchCompressor) Compress(content, context string, bias float64) (SearchCompressionResult, SearchCompressorStats, error)`
-
`func (c *SearchCompressor) CompressWithStore(content, context string, bias float64, store CCRStore) (SearchCompressionResult, SearchCompressorStats, error)`

`internal/tools/search_compressor_test.go`:

- Table-driven tests (no parity fixtures):
    - `TestSearchCompressor_ParseSimpleGrep` — single-file output
    - `TestSearchCompressor_GroupByFile` — multi-file, grouped output
    - `TestSearchCompressor_Scoring_BoostOnKeyword` — line with `error`
      ranks higher than line without
    - `TestSearchCompressor_AdaptiveTotal` — 100 matches across 5 files,
      K capped at `MaxPerFile`
    - `TestSearchCompressor_BelowMinMatches_PassThrough` — 5 matches <
      `MinMatchesForCCR=10`
    - `TestSearchCompressor_ContextLinesLowerPriority` — indented context
      lines ranked below match lines

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "SearchCompressor" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 6+ tests pass.

---

## Slice 7 — SmartCrusher

**Files:**

- `internal/tools/smart_crusher.go` (new)
- `internal/tools/smart_crusher_test.go` (new)

**Implementation:**

`internal/tools/smart_crusher.go`:

- `package tools`
- Imports: `crypto/sha256`, `encoding/hex`, `encoding/json`, `fmt`,
  `math`, `regexp`, `sort`, `strings`
-
`type SmartCrusherConfig struct { Enabled bool; DedupIdenticalItems bool; FactorOutConstants bool; FirstFraction float64; LastFraction float64; IncludeSummaries bool; MaxItemsAfterCrush int; MinItemsToAnalyze int; MinTokensToCrush int; PreserveChangePoints bool; SimilarityThreshold float64; ToinConfidenceThreshold float64; UniquenessThreshold float64; UseFeedbackHints bool; VarianceThreshold float64 }`
- `func DefaultSmartCrusherConfig() SmartCrusherConfig` — matches fixture defaults
- `type CrushResult struct { Compressed string; Original string; WasModified bool; Strategy string }`
-
`type CrushArrayResult struct { Items []json.RawMessage; StrategyInfo string; CCRHash string; DroppedSummary string; Compacted string; CompactionKind string }`
- `type ArrayType int` with `ArrayDict, ArrayString, ArrayNumber, ArrayMixed, ArrayNested, ArrayBool, ArrayEmpty`
- `type CompressionStrategy int` with
  `StrategyNone, StrategySkip, StrategyTimeSeries, StrategyClusterSample, StrategyTopN, StrategySmartSample`
-
`type FieldStats struct { Name string; Type ArrayType; TotalCount int; UniqueCount int; SampleValues []json.RawMessage }`
- `type SmartCrusher struct { cfg SmartCrusherConfig; detector *KeywordDetector }`
- `func NewSmartCrusher(cfg SmartCrusherConfig) *SmartCrusher`
- `func (c *SmartCrusher) Crush(content, query string, bias float64) (CrushResult, error)`:
    - Try `json.Unmarshal(content, &arr []json.RawMessage)`; if fails,
      `CrushResult{Compressed: content, Original: content, WasModified:
      false, Strategy: "passthrough"}`
    - If `len(arr) < c.cfg.MinItemsToAnalyze`:
      `CrushResult{Compressed: content, Original: content, WasModified:
      false, Strategy: "passthrough"}`
    - Classify array: detect if dicts, strings, numbers, mixed
    - For dict arrays, build `FieldStats`, identify
      `id_field` (detect_id_field_statistically: name matches UUID pattern
      OR name is "id"/"uuid"/"_id"/"key" OR entropy > 4.5) and
      `score_field` (detect_score_field_statistically: numeric field with
      high variance)
    - Detect `error_field` (any field whose name contains "status",
      "error", "level" and values match `ERROR_KEYWORDS`)
    - Determine strategy:
        - If time-series detected (sorted by numeric field, regular
          intervals): `StrategyTimeSeries`
        - Else if high cluster signal (variance < threshold AND unique < 0.3
          of total): `StrategyClusterSample`
        - Else if score_field present: `StrategyTopN` (by score desc)
        - Else: `StrategySmartSample` (Kneedle on scores)
    - Compute K = `ComputeOptimalK(scores, bias, 1, c.cfg.MaxItemsAfterCrush)`
    - Always keep: error items (status=error), first fraction * K, last
      fraction * K, score-field top items
    - Fill remaining with cluster samples
    - Emit:
        -
        `Compressed = fmt.Sprintf("[%s,...,\n{\"_ccr_dropped\":\"<<ccr:%s %d_rows_offloaded>>\"}]", keptJSON, hash, droppedCount)`
        - `Original = content`
        - `WasModified = true`
        - `Strategy = fmt.Sprintf("%s(%d->%d)", strategyName, len(arr), len(kept))`
    - CCR: `hash := ComputeCCRKey([]byte(content))`; if result is much
      smaller, append marker; store via `store.Put(hash, content)` if
      `store != nil`
- `func (c *SmartCrusher) CrushArray(items []json.RawMessage, query string, bias float64) (CrushArrayResult, error)`:
    - Inner entry point. Used by `Crush` after parsing.

`internal/tools/smart_crusher_test.go`:

- 17 parity fixtures via `filepath.Walk("testdata/parity/smart_crusher")`
- For each:
    1. Parse `rec.Input` as `{content, query, bias}`
    2. `cfg := unmarshalConfig(rec.Config)`
    3. `sc := NewSmartCrusher(cfg)`
    4. `result, err := sc.Crush(rec.Input.Content, rec.Input.Query, rec.Input.Bias)`
    5. Assert `result.Compressed == rec.Output.Compressed` byte-for-byte
    6. Assert `result.Original == rec.Output.Original`
    7. Assert `result.WasModified == rec.Output.WasModified`
    8. Assert `result.Strategy == rec.Output.Strategy`
- This is the biggest slice. If parity fails, debug by printing
  intermediate `FieldStats` and strategy decision.
- Unit tests:
    - `TestSmartCrusher_NonJSON_Passthrough`
    - `TestSmartCrusher_EmptyArray`
    - `TestSmartCrusher_BelowMinItems_Passthrough`
    - `TestSmartCrusher_ErrorItemsAlwaysKept`

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "SmartCrusher" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 17 parity tests + 4 unit tests = 21+ passing. **Budget extra
time for this slice — debug parity mismatches carefully.**

---

## Slice 8 — LogTemplate + JsonMinifier (Reformats)

**Files:**

- `internal/tools/log_template.go` (new)
- `internal/tools/log_template_test.go` (new)
- `internal/tools/json_minifier.go` (new)
- `internal/tools/json_minifier_test.go` (new)

**Implementation:**

`internal/tools/log_template.go`:

- `package tools`
- `type LogTemplateConfig struct { MinLines int; MinRun int; SimilarityThreshold float32; MinConstantTokens int }`
-
`func DefaultLogTemplateConfig() LogTemplateConfig { return LogTemplateConfig{MinLines: 20, MinRun: 3, SimilarityThreshold: 0.4, MinConstantTokens: 2} }`
- `type LogTemplate struct { cfg LogTemplateConfig }`
- `func NewLogTemplate(cfg LogTemplateConfig) *LogTemplate`
- `(t *LogTemplate) Name() string { return "log_template" }`
- `(t *LogTemplate) AppliesTo() []ContentType { return []ContentType{ContentBuildOutput} }`
- `(t *LogTemplate) Apply(content string) (ReformatOutput, error)`:
    - Split into lines
    - If `len(lines) < t.cfg.MinLines`, return unchanged
    - Tokenize each line (whitespace split)
    - Build initial template from first line
    - For each subsequent line, compute similarity to current template
    - If similarity ≥ `SimilarityThreshold`, replace differing tokens with
      `<*>`, extend template run
    - If similarity < threshold, finalize current template run if length
      ≥ `MinRun`, emit `[Template Tn: <template>] (Nx)\n  <variants>`,
      start new run
    - Finalize last run
    - `BytesSaved = inputLen - outputLen`
    - Return `ReformatOutput{Output: result, BytesSaved: ...}`
- Helper `func (t *LogTemplate) similarity(a, b []string) float32`:
    - Jaccard over token sets. Return `intersect / union`.

`internal/tools/log_template_test.go`:

- `TestLogTemplate_BelowMinLines_Passthrough`
- `TestLogTemplate_SingleTemplateRun_Collapsed` — 10 lines of
  `INFO foo bar baz` → `[Template T1: INFO <*> <*> <*>] (10x)`
- `TestLogTemplate_MultipleRuns_AllCollapsed` — alternating templates
- `TestLogTemplate_NoReformat_AllUnique` — 20 unique lines, no collapse
- `TestLogTemplate_LosslessReconstruction` — given output, original
  lines reconstructable (modulo variants table)

`internal/tools/json_minifier.go`:

- `package tools`
- `type JsonMinifierConfig struct { Enabled bool }`
- `func DefaultJsonMinifierConfig() JsonMinifierConfig { return JsonMinifierConfig{Enabled: true} }`
- `type JsonMinifier struct { cfg JsonMinifierConfig }`
- `func NewJsonMinifier(cfg JsonMinifierConfig) *JsonMinifier`
- `(j *JsonMinifier) Name() string { return "json_minifier" }`
- `(j *JsonMinifier) AppliesTo() []ContentType { return []ContentType{ContentJsonArray} }`
- `(j *JsonMinifier) Apply(content string) (ReformatOutput, error)`:
    - `var v any; if err := json.Unmarshal([]byte(content), &v); err != nil`,
      return unchanged with no error
    - `out, err := json.Marshal(v)`
    - If `len(out) >= len(content)`, return original (no inflation)
    - Else return minified

`internal/tools/json_minifier_test.go`:

- `TestJsonMinifier_Minify` — indented JSON → compact JSON
- `TestJsonMinifier_AlreadyMinified_PassThrough`
- `TestJsonMinifier_Malformed_PassThrough`
- `TestJsonMinifier_Empty_PassThrough`

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "LogTemplate|JsonMinifier" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 4 LogTemplate + 4 JsonMinifier = 8+ tests pass.

---

## Slice 9 — CCR Retrieval Tool

**Files:**

- `internal/tools/ccr_retrieve.go` (new)
- `internal/tools/ccr_retrieve_test.go` (new)
- `internal/tools/registry.go` (modified — add `retrieve_compacted` to CoreTools)

**Implementation:**

`internal/tools/ccr_retrieve.go`:

- `package tools`
- Imports: `fmt`, `strings`, `google.golang.org/adk/tool`
- `const retrieveToolName = "retrieve_compacted"`
- `type ccrMeta struct { Algorithm string; ContentType string; OrigSize int; CompSize int }`
- `type retrieveArgs struct { Hash string \`json:"hash"\` }`
- `type retrieveResult struct { Content string \`json:"content"\` }`
- `type retrieveTool struct { store CCRStore }`
- `func NewRetrieveTool(store CCRStore) (tool.Tool, error)`:
    - **Use the existing `newTool` helper** at `registry.go:170` to build
      the ADK tool — DO NOT call `functiontool.New` directly (it lacks
      the lenient schema, coercion, and alias-resolution logic)
    - Args type: `retrieveArgs{Hash string}`
    - Result type: `retrieveResult{Content string}`
    - Handler: `func(ctx tool.Context, args retrieveArgs) (retrieveResult, error)`:
        - Call `store.Get(args.Hash)`
        - If `(payload, false)`: return result with `Content: fmt.Sprintf("CCR
      hash not found: %s", args.Hash)` and nil error
        - Else: parse payload as JSON envelope (since S9 stores metadata
          inline) OR just return raw. **Decision:** return the full
          formatted output including metadata header
          `<<ccr_retrieved:ALGORITHM:CONTENT_TYPE:ORIG_SIZE:COMP_SIZE>>`
          as a single string in `Content`.
    - Add param alias: `hash` → also accepts `ccr_hash` (common LLM
      mistake)
- **CCR store metadata:** extend `CCRStore` interface in this slice to
  add `PutWithMeta(hash, payload string, meta ccrMeta)`. The simple
  `Put(hash, payload)` delegates to `PutWithMeta(hash, payload,
  ccrMeta{})` with empty metadata. Offload transforms call
  `PutWithMeta` after computing their metadata. This is the cleanest
  extension — the alternative (JSON-encode payload + metadata) loses
  raw-bytes retrievability.

`internal/tools/registry.go` (modified):

- Find where `CoreTools` is defined (`registry.go:19`)
- After the existing tool list is built, append:
  ```go
  retrieve, err := NewRetrieveTool(globalCCRStore)
  if err != nil {
      return nil, fmt.Errorf("build retrieve tool: %w", err)
  }
  tools = append(tools, retrieve)
  ```
- **Issue:** `CoreTools` currently doesn't take a CCR store. Either:
    - **(a)** Change signature: `CoreTools(sandbox *Sandbox, store CCRStore)`
    - **(b)** Make store a package-level variable in `tools` package
    - **(c)** Wire it lazily via the `llmagent.AfterToolCallback` that
      receives the store
- **Decision:** option (a) — clean signature change. Update both
  call sites (`internal/cli/cli.go:537-561` and
  `internal/cli/interactive.go:300-329`) to pass the store. Use
  `NewInMemoryCCRStore()` at the call sites.

`internal/tools/ccr_retrieve_test.go`:

- `TestRetrieveTool_RegisteredInRegistry`
- `TestRetrieveTool_HappyPath` — store 3 entries with different
  algorithm/content_type, retrieve each, assert metadata header
  format
- `TestRetrieveTool_MissingHash` — assert structured error message
- `TestRetrieveTool_ExpiredEntry` — store with 1ms ttl, sleep, retrieve
  returns error
- `TestRetrieveTool_InvalidHash` — non-hex hash, graceful failure

**Verification checkpoint:**

```bash
go build ./internal/tools/...
go test ./internal/tools/... -run "Retrieve|CCR" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/
```

Expected: 5+ retrieve tests pass + 8+ CCR tests pass = 13+.

---

## Slice 10a — Config Restructure + Migration

**Files:**

- `internal/tools/compactor.go` (modified — restructured `CompactorConfig`,
  new `MigrateLegacyConfig`, `BuildCompactorCallback` no-behavior-change
  signature update)
- `internal/tools/compactor_test.go` (modified — migration tests)
- `internal/config/config.go` (modified — user-facing `CompactorConfig`
  mirrors new shape)

**Implementation:**

`internal/tools/compactor.go` modifications:

- Replace the old `CompactorConfig` struct with the new nested shape
  (see design §Restructured CompactorConfig)
- Add `type PipelineConfig`, `BloatConfigs`, `LogBloatConfig`,
  `DiffBloatConfig`, `SearchBloatConfig`, `ReformatConfigs`,
  `LogTemplateConfig`, `OffloadConfigs`, `JsonOffloadConfig`,
  `DiffNoiseConfig`, `AlgorithmsConfig`, `LogCompressorConfig` (full
  struct), `DiffCompressorConfig` (full struct), `SearchCompressorConfig`
  (full struct), `SmartCrusherConfig` (full struct), `JsonMinifierConfig`,
  `LimitsConfig`
- `func DefaultCompactorConfig() CompactorConfig`:
    - Uses the new nested shape
    - Calls each algorithm's `Default*Config()` for defaults
- `var warnLog = log.Default()` — package-level logger, replaceable in
  tests
- `var migrateOnce sync.Once` — guards the deprecation log line so it
  fires at most once per process
- `func MigrateLegacyConfig(raw map[string]any) (CompactorConfig, error)`:
    - If `raw` has NONE of the legacy fields, return
      `DefaultCompactorConfig()` plus any new-shape fields from `raw`
      (passthrough)
    - If `raw` has legacy fields:
        - Build new `CompactorConfig` from `DefaultCompactorConfig()`
        - Apply migration map (per design §Data Models):
            - `strip_ansi` → ignored (always on)
            - `aggregate_test_output` → ignored
            - `filter_build_output` → ignored
            - `compact_git_output` → ignored
            - `aggregate_linter_output` → ignored
            - `group_search_output` → `Algorithms.SearchCompressor.GroupByFile = true`
            - `smart_truncate` → ignored
            - `source_code_filtering` → preserve in new top-level
              `SourceCodeFiltering` field (for back-compat)
            - `max_chars` → `Limits.MaxChars`
            - `max_lines` → `Limits.MaxLines`
            - `max_test_failures` → `Algorithms.LogCompressor.MaxErrors`
            - `max_test_fail_lines` → `Algorithms.LogCompressor.ErrorContextLines`
            - `max_build_errors` → `Algorithms.LogCompressor.MaxErrors`
              (last-wins; documented in log)
            - `max_build_err_lines` → `Algorithms.LogCompressor.ErrorContextLines`
            - `max_diff_lines` → `Algorithms.DiffCompressor.MaxFiles`
            - `max_diff_hunk_lines` → `Algorithms.DiffCompressor.MaxContextLines`
            - `max_status_files` → preserved at top level as
              `MaxStatusFiles` (out of v1 scope, but back-compat)
            - `max_log_entries` → `Algorithms.LogCompressor.MaxTotalLines`
            - `max_linter_rules` → `Algorithms.LogCompressor.MaxWarnings`
            - `max_linter_files` → `Algorithms.LogCompressor.MaxErrors`
            - `max_search_per_file` → `Algorithms.SearchCompressor.MaxPerFile`
            - `max_search_total` → `Algorithms.SearchCompressor.MaxPerFile * 5`
        - `migrateOnce.Do(func() { warnLog.Printf("compactor: legacy
      config detected, auto-migrating (X old fields present) — see
      changelog for migration notes") })`
        - Return new config
    - Apply any new-shape fields from `raw` last (overrides migration)
- `func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics) llmagent.AfterToolCallback`:
    - Keep the existing flat pipeline for now (call `compactToolResult`
      as before)
    - This is the "no behavior change" slice — only signatures change

`internal/tools/compactor_test.go` additions:

- `TestMigrateLegacyConfig_NewShape_PassThrough` — input is
  new-shape map, output equals `DefaultCompactorConfig()` plus
  overridden fields
- `TestMigrateLegacyConfig_LegacyShape_AllFields` — input has every
  legacy field, output has each correctly mapped
- `TestMigrateLegacyConfig_Idempotent` — apply migration twice,
  second call produces same result with no second log line
- `TestMigrateLegacyConfig_DeprecationLogged` — capture log output
  via custom `*log.Logger` written to `bytes.Buffer`, assert
  message contains "legacy config detected"
- `TestDefaultCompactorConfig_AllDefaultsSet` — assert
  `DefaultCompactorConfig().Enabled == true` and each nested config
  has its expected default values

`internal/config/config.go` modifications:

- Update `CompactorConfig` struct to mirror the new shape, but keep
  the user-facing 4 fields as `omitempty` plus add new optional fields
- Existing `Enabled *bool`, `SourceCodeFiltering string`, `MaxChars
  int`, `MaxLines int` remain
- Add: `Pipeline *PipelineConfig`, `Algorithms *AlgorithmsConfig`, etc.
  — all `omitempty`

**Verification checkpoint:**

```bash
go build ./...
go test ./internal/tools/... -run "Config|Migration" -v
go test ./internal/config/...
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/ OR internal/config/
```

Expected: 5+ migration tests + existing compactor tests still pass
(no behavior change yet).

---

## Slice 10b — Pipeline Integration + New Metrics

**Files:**

- `internal/tools/compactor.go` (modified — `BuildCompactorCallback`
  uses pipeline)
- `internal/tools/compactor_metrics.go` (modified — new fields, new
  `FormatStats`)
- `internal/tools/compactor_metrics_test.go` (new)
- `internal/cli/cli.go` (modified — wire pipeline if available, but
  defaults to no-op for now)
- `internal/cli/interactive.go` (modified — same)

**Implementation:**

`internal/tools/compactor.go` modifications:

-
`func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics, store CCRStore) llmagent.AfterToolCallback`:
    - **Signature change:** add `store CCRStore` parameter (last position
      to minimize call-site changes)
    - Build a `CompressionPipeline` via `NewCompressionPipelineBuilder()`:
        - `WithReformat(NewJsonMinifier(DefaultJsonMinifierConfig()))`
        - `WithReformat(NewLogTemplate(DefaultLogTemplateConfig()))`
        - `WithOffload(NewLogCompressor(cfg.Algorithms.LogCompressor))`
        - `WithOffload(NewDiffCompressor(cfg.Algorithms.DiffCompressor))`
        - `WithOffload(NewDiffNoise(cfg.Offload.DiffNoise))`
        - `WithOffload(NewSmartCrusher(cfg.Algorithms.SmartCrusher))`
        - `WithReformat(NewSearchCompressor(cfg.Algorithms.SearchCompressor))`
        - `WithConfig(OrchestratorConfig{ReformatTargetRatio:
      cfg.Pipeline.ReformatTargetRatio, BloatThreshold:
      cfg.Pipeline.BloatThreshold, OffloadFallbackRatio:
      cfg.Pipeline.OffloadFallbackRatio})`
        - `Build(store)`
    - In the callback:
        - Get the tool's result content (via `applyCompaction` extraction
          logic)
        - Detect `ContentType` via `DetectContentType(content)`
        - Run pipeline: `result := pipeline.Run(content, ct, ctx)`
        - If `len(result.StepsApplied) > 0` or `len(result.CacheKeys) > 0`:
            - Build `CompactRecord` with `ContentType: ct.String()`,
              `Stages: result.StepsApplied`, `Strategies: getOffloadNames(...)`,
              `BytesSaved: result.BytesSaved`, etc.
            - `metrics.Record(...)`
            - Apply compacted result back to tool output via
              `applyCompaction`
        - Else (pipeline returned no steps): fall back to
          `compactToolResult(t.Name(), args, result, cfg)` (legacy path)

`internal/tools/compactor_metrics.go` modifications:

- Update `CompactRecord` struct (add fields, keep old with `json:"-"`)
- Update `CompactSummary` struct (add fields, keep old with `json:"-"`)
- Update `Summary()` method to populate new fields
- Update `FormatStats()` to render 4-section output (see design
  §Restructured `CompactRecord`)
- Add helper `formatMapSection(sb *strings.Builder, title string, m
  map[string]X, formatBytes func(int) string)` for DRY rendering

`internal/tools/compactor_metrics_test.go`:

- `TestFormatStats_Empty` — no records, returns "No compaction records"
- `TestFormatStats_OneRecord_AllSections` — 1 record, output contains
  all 4 section headers + totals
- `TestFormatStats_MultipleRecords_DeterministicOrder` — 3 records
  with tools "bash", "grep", "read", output is alphabetically sorted
- `TestFormatStats_ByContentType_Sorted` — content types sorted
- `TestFormatStats_SavingsCalculation` — 100→25 = 75% savings

`internal/cli/cli.go` and `internal/cli/interactive.go` modifications:

- `BuildCompactorCallback(compactorCfg, metrics, nil)` — pass `nil`
  store initially (CCR tool not yet wired). The callback handles
  `store == nil` gracefully (offloads skip CCR but still compress).
- All other wiring unchanged

**Verification checkpoint:**

```bash
go build ./...
go test ./internal/tools/... -v
go test ./internal/config/...
go test ./internal/cli/...
git diff --stat HEAD~1  # ≥ 1 line in internal/tools/ OR internal/cli/
```

Expected: All existing tests pass + 4 new metrics tests + integration
tests pass.

---

## Slice 11 — TUI `/rtk stats` Update

**Files:**

- `internal/tui/commands.go` (likely no change — `handleRTKCommand`
  already calls `FormatStats()`)
- `internal/tui/commands_test.go` (modified — updated assertions)

**Implementation:**

Read `internal/tui/commands.go`:

- `handleRTKCommand` (lines 782–807) calls
  `m.cfg.CompactMetrics.FormatStats()`. **No code change needed** if
  `FormatStats` already renders the new 4-section layout from S10b.

If `FormatStats` output is unchanged in S10b:

- Update `TestHandleRTKCommand_RTKStats` to assert the new 4-section
  layout (Total, By Content Type, By Stage, By Strategy, By Tool)
- Add `TestHandleRTKCommand_RTKStatsEmpty` if not present

If `FormatStats` output is unchanged AND the existing
`TestHandleRTKCommand_RTKStats` already passes — no changes needed in
TUI at all. The slice becomes "verify and update test assertions".

`internal/tui/commands_test.go` updates:

- `TestHandleRTKCommand_RTKStats`:
    - Build a `CompactMetrics` with 3 records spanning tools/content types/stages/strategies
    - Run `/rtk` command
    - Assert the output message contains all 4 section headers
    - Assert content types appear in alphabetical order
- `TestHandleRTKCommand_RTKStats_NoMetrics`:
    - Set `m.cfg.CompactMetrics = nil`
    - Run `/rtk stats`
    - Assert output contains "Output compactor is not active."

**Verification checkpoint:**

```bash
go build ./...
go test ./internal/tui/... -run "RTK" -v
git diff --stat HEAD~1  # ≥ 1 line in internal/tui/
```

Expected: 2+ tests pass.

---

## Cross-Slice Verification (after all 12 slices complete)

```bash
# Build everything
go build ./...

# Run all tools tests
go test ./internal/tools/... -v

# Run all CLI/TUI tests
go test ./internal/cli/... ./internal/tui/...

# Run all tests
go test ./...

# Vet
go vet ./internal/tools/...

# Slice-gate guard for each slice
for slice in 1 2 3 4 5 6 7 8 9 10a 10b 11; do
    case $slice in
        1)  regex="CCR|ContentType" ;;
        2)  regex="Orchestrator|Pipeline|Transform" ;;
        3)  regex="Keyword|Adaptive|Signal|Sizer" ;;
        4)  regex="LogCompressor" ;;
        5)  regex="Diff" ;;
        6)  regex="SearchCompressor" ;;
        7)  regex="SmartCrusher" ;;
        8)  regex="LogTemplate|JsonMinifier" ;;
        9)  regex="Retrieve|CCR" ;;
        10a) regex="Config|Migration" ;;
        10b) regex="FormatStats" ;;
        11)  regex="RTK" ;;
    esac
    go test ./internal/tools/... -run "$regex" 2>&1 | tail -1
done
```

All 12 slice gates must report `PASS`. The total number of new test
functions across the plan is **~70** (counting both unit and parity
subtests), spread across 12 new `_test.go` files plus modifications to
3 existing files.

---

## Slice Order Rationale

1–9 are independent (each compiles + tests in isolation). The plan
executes them serially to avoid silent work loss.

10a is the config-only slice — small, low-risk, builds confidence
that migration works before any pipeline integration.

10b is the integration slice — biggest risk surface (touches
`BuildCompactorCallback`, metrics, both CLI entry points). Doing 10b
after 10a means the config shape is locked before pipeline wiring.

11 is verification + TUI test updates — purely additive.

## Risk Summary

| Risk                                | Slice   | Mitigation                                       |
|-------------------------------------|---------|--------------------------------------------------|
| SmartCrusher parity debug time      | 7       | Sub-commits within slice; budget 2–3× time       |
| `MinLinesForCCR` misnomer behavior  | 4, 5    | Explicit doc comment + below-threshold test      |
| CCR marker byte format              | 4, 5, 7 | Parity fixtures assert verbatim                  |
| `EstimateBloat` as method           | 2       | Design.md + review checklist                     |
| `SearchOffload` omission            | 10b     | S10b wires only `WithReformat(SearchCompressor)` |
| Migration idempotency               | 10a     | `sync.Once` + idempotency test                   |
| Vacuous gate pass                   | all     | Slice-gate guard requires new test functions     |
| Worktree discard (previous failure) | all     | Serial execution + per-slice commits             |