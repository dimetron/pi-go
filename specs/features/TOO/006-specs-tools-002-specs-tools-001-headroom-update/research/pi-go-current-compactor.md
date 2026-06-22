# Research — Current State of pi-go's RTK Compactor

## Source

Inspected live pi-go tree at commit `128ae60` (branch
`fix/acp-dedup-and-cleanup`). All facts verified by direct file reads; no
inference.

## 1. File Inventory

`/Users/dimetron/p6s/pi-dev/pi-go/internal/tools/` contains 8 compactor Go files:

| File                   |       LOC |      Bytes | Purpose                                                                                                                                                     |
|------------------------|----------:|-----------:|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `compactor.go`         |       174 |      5,225 | Top-level: `CompactorConfig`, `CompactResult`, `BuildCompactorCallback`, tool-name routing, `applyCompaction`, `runStage`                                   |
| `compactor_ansi.go`    |        41 |        999 | `stripAnsi`, `hardTruncate`, `hardTruncateLines`                                                                                                            |
| `compactor_bash.go`    |       485 |     12,734 | `compactBash` + 5 command detectors + `filterBuildOutput`, `aggregateTestOutput`, `compactGitBashOutput`, `aggregateLinterOutput`, `smartTruncate`, `dedup` |
| `compactor_git.go`     |       293 |      6,815 | `compactGitFileDiff`, `compactGitOverview`, `compactGitHunk`, `compactGitDiffText`, `compactGitLogText`, `compactGitStatusText`                             |
| `compactor_metrics.go` |       162 |      4,135 | `CompactMetrics`, `CompactRecord`, `CompactSummary`, `ToolCompactSum`, `FormatStats`, `Save`                                                                |
| `compactor_read.go`    |       117 |      2,785 | `compactRead`, `filterSourceCode`                                                                                                                           |
| `compactor_search.go`  |       148 |      3,407 | `compactGrep`, `compactFind`, `compactTree` (delegates to `compactFind`), `groupSearchOutput`                                                               |
| `compactor_test.go`    |     1,437 |     40,273 | 77 test functions + 11 benchmarks + 5 test-data helpers                                                                                                     |
| **Total**              | **2,857** | **76,373** |                                                                                                                                                             |

All files in package `tools`.

## 2. `CompactorConfig` (current shape)

From `internal/tools/compactor.go` lines 11–37:

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

    MaxChars         int `json:"max_chars"`
    MaxLines         int `json:"max_lines"`
    MaxTestFailures  int `json:"max_test_failures"`
    MaxTestFailLines int `json:"max_test_fail_lines"`
    MaxBuildErrors   int `json:"max_build_errors"`
    MaxBuildErrLines int `json:"max_build_err_lines"`
    MaxDiffLines     int `json:"max_diff_lines"`
    MaxDiffHunkLines int `json:"max_diff_hunk_lines"`
    MaxStatusFiles   int `json:"max_status_files"`
    MaxLogEntries    int `json:"max_log_entries"`
    MaxLinterRules   int `json:"max_linter_rules"`
    MaxLinterFiles   int `json:"max_linter_files"`
    MaxSearchPerFile int `json:"max_search_per_file"`
    MaxSearchTotal   int `json:"max_search_total"`
}
```

7 boolean feature flags + 14 numeric/string tuning fields, all flat.

`DefaultCompactorConfig()` (lines 41–68): all booleans `true`, limits as:
`MaxChars=24000, MaxLines=440, MaxTestFailures=10, MaxTestFailLines=8,
MaxBuildErrors=10, MaxBuildErrLines=20, MaxDiffLines=100,
MaxDiffHunkLines=20, MaxStatusFiles=10, MaxLogEntries=40,
MaxLinterRules=20, MaxLinterFiles=20, MaxSearchPerFile=20,
MaxSearchTotal=100`.

## 3. Tool-Name Routing (`compactToolResult`, lines 95–117)

```go
switch t.Name() {
case "bash":            return compactBash(result, args, cfg)
case "read":            return compactRead(result, cfg)
case "grep":            return compactGrep(result, cfg)
case "find":            return compactFind(result, cfg)
case "tree":            return compactTree(result, cfg)
case "git_file_diff":   return compactGitFileDiff(result, cfg)
case "git_overview":    return compactGitOverview(result, cfg)
case "git_hunk":        return compactGitHunk(result, cfg)
default:                return nil
}
```

9 tools routed; anything else passes through unchanged. No content-type
detection — routing is by tool name only.

## 4. Callback Wiring (`BuildCompactorCallback`, lines 78–93)

```go
func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics) llmagent.AfterToolCallback {
    return func(ctx agent.ToolContext, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
        if !cfg.Enabled || err != nil { return result, nil }
        compacted := compactToolResult(t.Name(), args, result, cfg)
        if compacted != nil {
            metrics.Record(compacted.Techniques, compacted.OrigSize, compacted.CompSize, t.Name())
            applyCompaction(result, compacted)
        }
        return result, nil
    }
}
```

`applyCompaction` (lines 119–155): mutates result map by precedence
`stdout` → `content` → `output` → `diff` → fallback `result`/`data`. Logs
`"compactor: no known output field in result for replacement"` when no field
matches.

`runStage` (lines 157–173): wraps each compaction stage in `recover()`,
returns input on panic, logs `compactor: stage %q panicked: %v`. On
`applied=true` appends technique name.

## 5. `compactor_metrics.go` — Current Stats Format

```go
type CompactMetrics struct {
    mu      sync.Mutex
    Records []CompactRecord `json:"records"`
}
type CompactRecord struct {
    Tool       string    `json:"tool"`
    Techniques []string  `json:"techniques"`
    OrigSize   int       `json:"orig_size"`
    CompSize   int       `json:"comp_size"`
    Timestamp  time.Time `json:"timestamp"`
}
type CompactSummary struct {
    TotalOrig  int                       `json:"total_orig"`
    TotalComp  int                       `json:"total_comp"`
    SavingsPct float64                   `json:"savings_pct"`
    ByTool     map[string]ToolCompactSum `json:"by_tool"`
}
type ToolCompactSum struct {
    Count int `json:"count"`
    Orig  int `json:"orig"`
    Comp  int `json:"comp"`
}
```

Current `/rtk stats` output (`FormatStats`, lines 85–109):

```
RTK Compactor Stats
═══════════════════
Total calls:    <count>
Original size:  <bytes>
Compacted size: <bytes>
Savings:        <pct>%

By Tool:
  <tool>          <count> calls  <orig> → <comp>  (<pct>%)
```

- Empty records → returns `"No compaction records in this session."`
- Uses `formatBytes` (B/KB/MB with one decimal)
- Per-tool name is `%-15s`, count is `%3d calls`, savings is `%.0f%%`
- `Save()` writes `<sessionDir>/compactor-metrics.json`; not currently called
  from production code (test-only path)

## 6. CLI Wiring (load paths)

Two near-identical load sites:

**`internal/cli/cli.go:537–561` (`runNonInteractive`)**:

```go
compactorCfg := tools.DefaultCompactorConfig()
if cfg.Compactor != nil {
    if cfg.Compactor.Enabled != nil         { compactorCfg.Enabled = *cfg.Compactor.Enabled }
    if cfg.Compactor.SourceCodeFiltering != "" { compactorCfg.SourceCodeFiltering = cfg.Compactor.SourceCodeFiltering }
    if cfg.Compactor.MaxChars > 0           { compactorCfg.MaxChars = cfg.Compactor.MaxChars }
    if cfg.Compactor.MaxLines > 0           { compactorCfg.MaxLines = cfg.Compactor.MaxLines }
}
compactorCB := tools.BuildCompactorCallback(compactorCfg, tools.NewCompactMetrics())
```

`NewCompactMetrics()` return value is **discarded** — no reference kept.
`/rtk` is therefore unavailable in `print`/`json`/`rpc` modes.

**`internal/cli/interactive.go:300–329` (`deferredInit`)**:
Identical pattern, but `compactMetrics` reference is preserved and passed to
TUI via `InitResult.CompactMetrics` (line 410). `/rtk` works here.

## 7. `config.CompactorConfig` (user-facing subset)

`internal/config/config.go` lines 80–87:

```go
type CompactorConfig struct {
    Enabled             *bool  `json:"enabled,omitempty"`
    SourceCodeFiltering string `json:"source_code_filtering,omitempty"`
    MaxChars            int    `json:"max_chars,omitempty"`
    MaxLines            int    `json:"max_lines,omitempty"`
}
```

Pointer-on-Enabled for explicit `false` vs absent. Only 4 user-overridable
fields. All other tunables (`MaxTestFailures`, `MaxBuildErrors`, etc.) are
not user-configurable from `config.json`.

## 8. TUI `/rtk` Consumer

`internal/tui/commands.go:782–807`:

```go
func (m *model) handleRTKCommand(args []string) {
    sub := "stats"
    if len(args) > 0 { sub = strings.ToLower(args[0]) }
    switch sub {
    case "stats":
        if m.cfg.CompactMetrics == nil {
            m.chatModel.Messages = append(m.chatModel.Messages, message{
                role: "assistant", content: "Output compactor is not active.",
            })
            return
        }
        m.chatModel.Messages = append(m.chatModel.Messages, message{
            role: "assistant", content: m.cfg.CompactMetrics.FormatStats(),
        })
    default:
        m.chatModel.Messages = append(m.chatModel.Messages, message{
            role: "assistant", content: "Usage: `/rtk` or `/rtk stats` — Show output compaction statistics",
        })
    }
}
```

Does **not** read individual `CompactSummary` fields — only consumes the
pre-formatted string from `FormatStats()`. TUI help line (commands.go:659):
`| `/rtk` | Output compaction stats |`.

`/context` also appends compactor stats via `cm.FormatStats()` under
`"*Output compaction*"` (commands.go:585–593).

## 9. `go.mod` Direct Dependencies

- Module: `github.com/dimetron/pi-go`, Go 1.26.3
- 33 direct deps, including: `charm.land/bubbles/v2 v2.1.0`,
  `charm.land/bubbletea/v2 v2.0.7`, `charm.land/lipgloss/v2 v2.0.3`,
  `github.com/a2aproject/a2a-go/v2`, `github.com/alecthomas/chroma/v2`,
  `github.com/anthropics/anthropic-sdk-go v1.46.0`,
  `github.com/charmbracelet/glamour v1.0.0`,
  `github.com/charmbracelet/x/ansi v0.11.7`,
  `github.com/coder/acp-go-sdk v0.13.4`,
  `github.com/creack/pty v1.1.24`,
  `github.com/google/jsonschema-go v0.4.3`,
  `github.com/google/uuid v1.6.0`,
  `github.com/gorilla/websocket v1.5.3`,
  `github.com/modelcontextprotocol/go-sdk v1.6.1`,
  `github.com/ollama/ollama v0.24.0`,
  `github.com/openai/openai-go/v3 v3.38.0`,
  `github.com/spf13/cobra v1.10.2`,
  `github.com/stretchr/testify v1.11.1`,
  `go.opentelemetry.io/otel v1.44.0` (+ sdk/trace/exporters),
  `google.golang.org/adk v1.4.0`,
  `google.golang.org/genai v1.58.0`,
  `modernc.org/sqlite v1.51.0`,
  `gopkg.in/yaml.v3 v3.0.1`

~100 indirect deps.

**No `aho-corasick` Go dep present** — stdlib only for keyword detection.

## 10. `retrieve` / `ccr` / `CCR` Matches

Source-code matches (all unrelated):

- `internal/palace/palace.go:90` — comment about `GetDrawer`
- `internal/webserver/session.go:61` — `GetSession`
- `internal/webserver/session_test.go` — `retrieved` in test name

**No tool named `retrieve_compacted` or `ccr_*` is registered** in
`internal/tools/registry.go`. The only `ccr` strings are inside the
orphaned JSON parity fixtures.

## 11. Existing Test Patterns

Table-driven style is standard:

```go
tests := []struct {
    name    string
    input   string
    want    string
    applied bool
}{ /* cases */ }
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, applied := fn(tt.input)
        if got != tt.want { t.Errorf(...) }
        if applied != tt.applied { t.Errorf(...) }
    })
}
```

Used in `TestStripAnsi`, `TestHardTruncate`, `TestDetectCommand`,
`TestIsTestCommand`, `TestIsBuildCommand`, `TestIsGitCommand`,
`TestIsLinterCommand`, etc. Many other tests use hand-built fixtures + string
assertions.

77 test functions cover all current stages:
`TestStripAnsi`, `TestHardTruncate`, `TestHardTruncateLines*`,
`TestDetectCommand`, `TestIs*Command`, `TestAggregateTestOutput*`,
`TestFilterBuildOutput*`, `TestAggregateLinterOutput*`,
`TestCompactGitDiffText*`, `TestCompactGitLogText*`,
`TestCompactGitStatusText*`, `TestCompactGitBashOutput`,
`TestGroupSearchOutput*`, `TestFilterSourceCode*`, `TestSmartTruncate*`,
`TestCompactMetrics*`, `TestFormatBytes`, `TestDefaultCompactorConfig`,
`TestCompactToolResult_AllTools`, `TestRunStage_*`,
`TestApplyCompaction_*`, `TestCompactBash_*`, `TestCompactRead_*`,
`TestCompactGrep_*`, `TestCompactFind_*`, `TestCompactTree_*`,
`TestCompactGitFileDiff_*`, `TestCompactGitOverview_*`,
`TestCompactGitHunk_*`, `TestDedup*`, `TestBuildCompactorCallback_*`,
`TestCompactToolResult_UnknownTool`.

Plus 11 benchmarks and 5 test-data generator helpers.

## 12. Outstanding Observations

- **No content-type detection exists** — routing is by tool name only.
- **No adaptive sizing** — limits are hardcoded integers.
- **No line-importance scoring** — `smartTruncate` uses a small fixed
  priority table (errors=10, warnings=7, declarations=5, blank=0).
- **No CCR store** — truncated bytes are gone permanently.
- **No offload/gate concept** — every enabled stage runs unconditionally.
- **The `compactor_metrics.go` `Save` method is dead code in production**;
  no caller invokes it outside tests.
- **`/rtk` is wired only in interactive mode**; print/json/rpc modes throw
  away the metrics reference.
- **All compactor functions are unexported** — they're file-scoped within
  `internal/tools/` and never cross package boundaries except for the
  public `BuildCompactorCallback` / `DefaultCompactorConfig` / types.