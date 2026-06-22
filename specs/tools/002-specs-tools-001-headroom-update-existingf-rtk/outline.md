# Outline — Headroom-Style RTK Compactor Overhaul

## Stages (each independently mergeable, no cross-stage dependencies)

### Stage 1: CCR Store + Content Detection

- `ccr.go` — `CCRStore` interface, `InMemoryCCRStore`, `ComputeCCRKey`, `CCRMarker`
- `content_type.go` — `ContentType` enum, `DetectContentType`, `DetectFromToolName`
- Tests: `ccr_test.go`, `content_type_test.go`
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "CCR|ContentType"`

### Stage 2: Transform Traits + Orchestrator

- `transform.go` — `ReformatTransform`, `OffloadTransform` interfaces, `CompressionContext`, `TransformError`
- `orchestrator.go` — `CompressionPipeline`, `OrchestratorConfig`, `PipelineResult`, parallel goroutine execution
- Tests: `orchestrator_test.go` with stub transforms
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "Orchestrator|Pipeline"`

### Stage 3: Line Importance Detection + Adaptive Sizer

- `signals.go` — `KeywordDetector`, `ImportanceSignal`, `ImportanceContext`, per-context keyword sets
- `adaptive_sizer.go` — `ComputeOptimalK`, `FindKnee`, `CountUniqueSimhash` (MD5 4-gram)
- Tests: `signals_test.go`, `adaptive_sizer_test.go`
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "Keyword|Adaptive|Kneedle"`

### Stage 4: LogCompressor

- `log_compressor.go` — format detection, per-line classification, scoring, adaptive budget, CCR
- Parity fixtures: `testdata/parity/log_compressor/*.json`
- Tests: `log_compressor_test.go` with parity fixtures
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "LogCompressor"`

### Stage 5: DiffCompressor + DiffNoise

- `diff_compressor.go` — unified-diff parsing, hunk scoring, file/hunk caps, context trim, CCR
- `diff_noise.go` — lockfile + whitespace-only hunk dropping, bloat estimation
- Parity fixtures: `testdata/parity/diff_compressor/*.json`
- Tests: `diff_compressor_test.go`, `diff_noise_test.go`
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "Diff"`

### Stage 6: SearchCompressor

- `search_compressor.go` — match parsing, scoring, adaptive total, group_by_file, CCR
- Tests: `search_compressor_test.go`
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "SearchCompressor"`

### Stage 7: SmartCrusher

- `smart_crusher.go` — array classification, schema dedup, anchor selection, adaptive item count, CCR
- Parity fixtures: `testdata/parity/smart_crusher/*.json`
- Tests: `smart_crusher_test.go` with parity fixtures
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "SmartCrusher"`

### Stage 8: LogTemplate + JsonMinifier (Reformats)

- `log_template.go` — Drain-inspired template miner, lossless collapse
- `json_minifier.go` — encoding/json round-trip whitespace stripping
- Tests: `log_template_test.go`, `json_minifier_test.go`
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "LogTemplate|JsonMinifier"`

### Stage 9: CCR Retrieval Tool

- `ccr_retrieve.go` — ADK FunctionTool `retrieve_compacted`, registered in `CoreTools`
- Tests: `ccr_retrieve_test.go`
- **Verify**: `go build ./internal/tools/... && go test ./internal/tools/... -run "Retrieve"`

### Stage 10: Config Restructure + Callback Rewire + Metrics

- `compactor.go` — restructured `CompactorConfig`, new `DefaultCompactorConfig`, updated `BuildCompactorCallback`
- `compactor_metrics.go` — new `FormatStats` with per-stage/content-type/strategy breakdown
- Wire `CompressionPipeline` with all transforms into callback
- Update `internal/config/config.go` `CompactorConfig` to match
- Update `internal/cli/cli.go` + `interactive.go` wiring
- Tests: updated `compactor_test.go`, `compactor_metrics_test.go`
- **Verify**: `go build ./... && go test ./internal/tools/... && go test ./internal/tui/... -run "RTK"`

### Stage 11: TUI `/rtk stats` Update

- `internal/tui/commands.go` — updated `handleRTKCommand` for new metrics format
- Tests: updated `commands_test.go`
- **Verify**: `go build ./... && go test ./internal/tui/... -run "RTK"`

## Key Type Signatures (new interfaces)

```go
// Stage 1
type CCRStore interface { Put(hash, payload string); Get(hash string) (string, bool); Len() int }
func ComputeCCRKey(payload []byte) string
func CCRMarker(hash string) string
func DetectContentType(content string) DetectionResult

// Stage 2
type ReformatTransform interface { Name() string; AppliesTo() []ContentType; Apply(string) (ReformatOutput, error) }
type OffloadTransform interface { Name() string; AppliesTo() []ContentType; EstimateBloat(string) float32; Apply(string, CompressionContext, CCRStore) (OffloadOutput, error); Confidence() float32 }
type CompressionPipeline struct{ ... }
func (p *CompressionPipeline) Run(content string, ct ContentType, ctx CompressionContext) PipelineResult

// Stage 3
type LineImportanceDetector interface { Score(line string, ctx ImportanceContext) ImportanceSignal }
func ComputeOptimalK(items []string, bias float64, minK, maxK int) int

// Stages 4-8: algorithm structs (see design.md for full signatures)

// Stage 9
func NewRetrieveTool(store CCRStore) (tool.Tool, error)

// Stage 10
func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics, store CCRStore) llmagent.AfterToolCallback
```

## Order and Dependencies

```
Stage 1 (CCR + ContentDetection)  ─┐
Stage 2 (Traits + Orchestrator)   ─┼─→ Stage 10 (Integration) → Stage 11 (TUI)
Stage 3 (Signals + AdaptiveSizer) ─┤
Stage 4 (LogCompressor)           ─┤
Stage 5 (DiffCompressor+Noise)    ─┼─→ all feed into Stage 10
Stage 6 (SearchCompressor)        ─┤
Stage 7 (SmartCrusher)            ─┤
Stage 8 (LogTemplate+JsonMinifier)─┤
Stage 9 (Retrieve Tool)           ─┘
```

Stages 1-9 are **fully independent** — each compiles and tests on its own. Stage 10 wires everything together. Stage 11
updates the TUI.