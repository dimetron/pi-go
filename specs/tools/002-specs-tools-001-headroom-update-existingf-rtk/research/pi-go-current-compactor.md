# pi-go Current RTK Compactor — Existing Implementation

## Files

- `internal/tools/compactor.go` — config, callback builder, routing, stage runner
- `internal/tools/compactor_bash.go` — bash pipeline (ANSI, build, test, git, linter, truncate)
- `internal/tools/compactor_ansi.go` — ANSI escape stripping
- `internal/tools/compactor_git.go` — git diff/log/status compaction
- `internal/tools/compactor_read.go` — read tool compaction
- `internal/tools/compactor_search.go` — grep/find/tree compaction
- `internal/tools/compactor_metrics.go` — session metrics (Record, Summary, FormatStats, Save)
- `internal/tools/truncate.go` — smart/hard truncation

## Architecture

### Routing

`compactToolResult(toolName, args, result, cfg)` switches on tool name:

- `bash` → `compactBash`
- `read` → `compactRead`
- `grep` → `compactGrep`
- `find` → `compactFind`
- `tree` → `compactTree`
- `git_file_diff` → `compactGitFileDiff`
- `git_overview` → `compactGitOverview`
- `git_hunk` → `compactGitHunk`
- default → nil (no compaction)

### Callback Wiring

`BuildCompactorCallback(cfg, metrics)` returns `llmagent.AfterToolCallback`.
Called from:

- `internal/cli/cli.go:552` (non-interactive mode)
- `internal/cli/interactive.go:313` (interactive TUI mode)

Callback skips if `!cfg.Enabled` or `err != nil`. On compaction, calls `metrics.Record(...)` and
`applyCompaction(result, compacted)`.

### applyCompaction

Replaces output field in result map: looks for `stdout`, `content`, `output`, `diff`, then falls back to `result`/
`data`.

## Stage Runner

`runStage(input, &techniques, name, fn)` — applies fn if enabled, tracks technique name. Recover from panic, log, return
input unchanged.

## CompactorConfig (flat struct, JSON tags)

```go
type CompactorConfig struct {
    Enabled               bool   `json:"enabled"`
    StripAnsi             bool   `json:"strip_ansi"`
    AggregateTestOutput   bool   `json:"aggregate_test_output"`
    FilterBuildOutput     bool   `json:"filter_build_output"`
    CompactGitOutput      bool   `json:"compact_git_output"`
    AggregateLinterOutput bool   `json:"aggregate_linter_output"`
    GroupSearchOutput     bool   `json:"group_search_output"`
    SmartTruncate         bool   `json:"smart_truncate"`
    SourceCodeFiltering   string `json:"source_code_filtering"` // "none", "minimal", "aggressive"

    MaxChars         int `json:"max_chars"`         // 24000
    MaxLines         int `json:"max_lines"`         // 440
    MaxTestFailures  int `json:"max_test_failures"` // 10
    MaxTestFailLines int `json:"max_test_fail_lines"` // 8
    MaxBuildErrors   int `json:"max_build_errors"`   // 10
    MaxBuildErrLines int `json:"max_build_err_lines"` // 20
    MaxDiffLines     int `json:"max_diff_lines"`     // 100
    MaxDiffHunkLines int `json:"max_diff_hunk_lines"` // 20
    MaxStatusFiles   int `json:"max_status_files"`   // 10
    MaxLogEntries    int `json:"max_log_entries"`    // 40
    MaxLinterRules   int `json:"max_linter_rules"`   // 20
    MaxLinterFiles   int `json:"max_linter_files"`   // 20
    MaxSearchPerFile int `json:"max_search_per_file"` // 20
    MaxSearchTotal   int `json:"max_search_total"`   // 100
}
```

Defaults: all stages enabled, limits doubled from rtk-optimizer defaults. `SourceCodeFiltering = "none"`.

## Bash Pipeline (compactBash)

1. ANSI stripping (stdout + stderr)
2. Build output filtering — `isBuildCommand` (go build, make, cargo build, npm run build, gcc, g++)
3. Test output aggregation — `isTestCommand` (go test, pytest, npm test, jest, cargo test)
4. Git output compaction — `isGitCommand`
5. Linter aggregation — `isLinterCommand` (golangci-lint, eslint, tsc, mypy, flake8, pylint, ruff)
6. Smart truncation
7. Hard truncation (chars + lines)

### filterBuildOutput

Keeps lines with: "error:", "Error", "failed", "FAIL", "panic", "fatal", "undefined", "cannot find", "warning:", "
import", "#". Context lines around errors. Caps at `MaxBuildErrors` + `MaxBuildErrLines`.

### aggregateTestOutput

Detects test framework (go test, pytest, jest). Extracts: summary lines (PASS/FAIL, ok/FAIL), failed test names, first N
failure detail lines. Caps at `MaxTestFailures` + `MaxTestFailLines`.

### compactGitBashOutput

Routes to `compactGitDiffText` / `compactGitLogText` / `compactGitStatusText` based on subcommand.

## Git Compaction (compactor_git.go)

- `compactGitDiffText`: trims context lines to `MaxDiffHunkLines`, caps total to `MaxDiffLines`.
- `compactGitLogText`: caps to `MaxLogEntries`, keeps HEAD + recent.
- `compactGitStatusText`: caps to `MaxStatusFiles`, groups by status.

## Search Compaction (compactor_search.go)

- `groupSearchOutput`: groups by file prefix, caps `MaxSearchPerFile` per file, `MaxSearchTotal` total. Only fires if >
  20 lines.
- `compactFind`/`compactTree`: hard truncation only.

## Read Compaction (compactor_read.go)

Source code filtering: "minimal" strips comments, "aggressive" strips comments + blank lines + imports. Then truncation.

## Truncation (truncate.go)

- `smartTruncate`: head + tail with `[... truncated N lines ...]` marker. Keeps first/last N lines.
- `hardTruncate`: char-based cap at `MaxChars`.
- `hardTruncateLines`: line-based cap at `MaxLines`.

## Metrics (compactor_metrics.go)

- `CompactMetrics` — mutex-protected slice of `CompactRecord{Tool, Techniques, OrigSize, CompSize, Timestamp}`.
- `Summary()` → `CompactSummary{TotalOrig, TotalComp, SavingsPct, ByTool}`.
- `FormatStats()` → human-readable string for `/rtk` command.
- `Save(sessionDir)` → JSON file persistence.

## TUI Integration

- `/rtk` slash command → `handleRTKCommand` → shows `CompactMetrics.FormatStats()`.
- `CompactMetrics` interface: `CompactStatsProvider` in `internal/tui/types.go`.

## Config Integration

- `internal/config/config.go` has its own `CompactorConfig` (user-overridable, separate from `tools.CompactorConfig`).
- Merged at startup in cli.go / interactive.go.

## Test Patterns

- `internal/tools/compactor_test.go` — extensive table-driven tests for each stage.
- `TestBuildCompactorCallback_*` — callback integration tests.
- `internal/tui/commands_test.go` — `/rtk` command tests.