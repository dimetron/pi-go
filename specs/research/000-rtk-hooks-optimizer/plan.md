# Implementation Plan — RTK Output Compactor

## Checklist

- [ ] Step 1: Core types, config, and callback skeleton
- [ ] Step 2: ANSI stripping and hard truncation
- [ ] Step 3: Bash pipeline — command detection and build output filtering
- [ ] Step 4: Bash pipeline — test output aggregation
- [ ] Step 5: Bash pipeline — git output compaction
- [ ] Step 6: Bash pipeline — linter aggregation
- [ ] Step 7: Read pipeline — source code filtering and smart truncation
- [ ] Step 8: Search pipeline — grep, find, tree compaction
- [ ] Step 9: Git tool pipelines — git-diff, git-overview, git-hunk
- [ ] Step 10: Metrics tracking and persistence
- [ ] Step 11: TUI integration — `/rtk stats` and compaction indicator
- [ ] Step 12: Wire callback, remove old truncation, integration test

---

## Step 1: Core Types, Config, and Callback Skeleton

**Objective:** Establish the compactor foundation — types, config loading, callback builder, and pipeline router that passes through unmodified output.

**Implementation Guidance:**
- Create `internal/tools/compactor.go` with:
  - `CompactorConfig` struct with all fields and `DefaultCompactorConfig()` function
  - `CompactResult` struct
  - `BuildCompactorCallback()` returning `llmagent.AfterToolCallback`
  - `compactToolResult()` router that switches on tool name (all cases return nil initially)
  - `applyCompaction()` helper that replaces result map fields with compacted output
- Add `Compactor *CompactorConfig` field to `internal/config/config.go` Config struct
- Config loading: merge defaults for missing fields in `Load()`

**Test Requirements:**
- `DefaultCompactorConfig()` returns expected defaults (all enabled, doubled limits)
- Config with partial JSON merges correctly with defaults
- Callback passes through result unchanged when compactor disabled
- Callback passes through result unchanged when tool has no pipeline (e.g., `write`)

**Integration Notes:**
- Not yet wired into `cli.go` — that's Step 12
- Tools still use existing truncation until Step 12

**Demo:** Config loads with compactor defaults; callback builder compiles and returns a no-op callback.

---

## Step 2: ANSI Stripping and Hard Truncation

**Objective:** Implement the two universal techniques that apply to all pipelines.

**Implementation Guidance:**
- Create `internal/tools/compactor_ansi.go`:
  - `stripAnsi(s string) (string, bool)` — three regex patterns for CSI, OSC sequences
  - Fast path: return immediately if no `\x1b` byte in input
  - Pre-compile regexes at package init
- Add to `compactor.go`:
  - `hardTruncate(s string, maxChars int) (string, bool)` — truncate at maxChars, append `"\n... (truncated)"`
  - `smartTruncate(s string, maxLines int) (string, bool)` — stub for now, implemented in Step 7

**Test Requirements:**
- ANSI stripping removes color codes, bold, cursor movement, OSC sequences
- ANSI stripping preserves non-ANSI content exactly
- Fast path: no allocation when input has no escape codes
- Hard truncation at exact char limit
- Hard truncation no-op when input is under limit
- Unicode safety: truncation doesn't split multi-byte characters

**Integration Notes:**
- These are building blocks used by all pipelines in subsequent steps.

**Demo:** Pass ANSI-colored `go test` output through stripAnsi → clean text. Pass 30K char string through hardTruncate → 24K output.

---

## Step 3: Bash Pipeline — Command Detection and Build Output Filtering

**Objective:** Create the bash compaction pipeline skeleton with command detection and the first semantic stage: build output filtering.

**Implementation Guidance:**
- Create `internal/tools/compactor_bash.go`:
  - `compactBash(result, args map[string]any, cfg CompactorConfig) *CompactResult` — orchestrates bash pipeline stages in order
  - `detectCommand(args map[string]any) string` — extract command string from bash args, normalize (strip env vars, take first segment before `&&`/`||`)
  - `isBuildCommand(cmd string) bool` — match `go build`, `go install`, `cargo build`, `cargo check`, `make`, `tsc`, `gcc`, `g++`, `javac`, `mvn`, `gradle`
  - `filterBuildOutput(s string, cfg CompactorConfig) (string, bool)`:
    - Skip progress lines (Compiling, Downloading, Linking, etc.)
    - Extract error blocks (lines starting with `error:`, `[ERROR]`, `FAIL`, `Error:`)
    - Extract warning blocks (`warning:`, `[WARN]`)
    - Limit: 10 errors × 20 lines each, then summary line
    - Prepend summary: `"Build: N errors, M warnings"`

**Test Requirements:**
- Command detection extracts command from `{"command": "FOO=bar go build ./..."}` → `"go build"`
- Command detection handles compound commands: `"cd src && go build"` → `"go build"` (last segment)
- Build filter extracts errors from `go build` output
- Build filter extracts errors from `cargo build` output
- Build filter preserves all output when no errors detected (not a build command)
- Build filter respects `filter_build_output = false` toggle
- Pipeline runs ANSI strip → build filter → hard truncate in order

**Integration Notes:**
- Pipeline returns `CompactResult` with techniques list.

**Demo:** `go build ./...` output with 50 errors → compacted to top 10 errors with summary line.

---

## Step 4: Bash Pipeline — Test Output Aggregation

**Objective:** Add test runner detection and output aggregation to the bash pipeline.

**Implementation Guidance:**
- Add to `compactor_bash.go`:
  - `isTestCommand(cmd string) bool` — match `go test`, `pytest`, `python -m pytest`, `npm test`, `npx jest`, `npx vitest`, `cargo test`, `bun test`, `yarn test`
  - `aggregateTestOutput(s string, cfg CompactorConfig) (string, bool)`:
    - Detect test runner from output patterns (not just command)
    - Extract stats via runner-specific regexes:
      - Go: `ok|FAIL\s+package\s+[\d.]+s`, `--- FAIL:`, `PASS`, `FAIL`
      - pytest: `\d+ passed`, `\d+ failed`, `\d+ skipped`, `\d+ error`
      - Jest/Vitest: `Tests:\s+\d+ passed`, `\d+ failed`
      - Cargo: `test result: (ok|FAILED)\. \d+ passed; \d+ failed`
    - Extract failure blocks: lines between `--- FAIL:` / `FAIL` and next test or end
    - Output format:
      ```
      Test Summary: PASS: N, FAIL: N, SKIP: N

      Failures:
      --- FAIL: TestFoo
          expected X, got Y
          ...
      ```
    - Limit: 10 failures, 8 lines each

**Test Requirements:**
- Detects and aggregates `go test` output (pass-only, mixed, all-fail)
- Detects and aggregates `pytest` output
- Detects and aggregates Jest/Vitest output
- Detects and aggregates `cargo test` output
- Preserves full output when all tests pass and output is short
- Respects `aggregate_test_output = false` toggle
- Pipeline order: ANSI strip → build filter (skip, it's test) → test aggregate → hard truncate

**Integration Notes:**
- Build filter and test filter are mutually exclusive based on command detection. If both match, test takes priority.

**Demo:** `go test ./...` with 200 lines including 3 failures → compacted to summary + 3 failure blocks.

---

## Step 5: Bash Pipeline — Git Output Compaction

**Objective:** Add git diff/status/log compaction to the bash pipeline.

**Implementation Guidance:**
- Create `internal/tools/compactor_git.go`:
  - `isGitCommand(cmd string) bool` — match `git diff`, `git status`, `git log`, `git show`
  - `detectGitSubcommand(cmd string) string` — returns "diff", "status", "log", "show", or ""
  - `compactGitDiffOutput(s string, cfg CompactorConfig) (string, bool)`:
    - Parse unified diff format
    - Per file: header line + `+N -M` summary
    - Include up to `MaxDiffHunkLines` (20) lines per hunk
    - Total limit: `MaxDiffLines` (100)
  - `compactGitStatusOutput(s string, cfg CompactorConfig) (string, bool)`:
    - Categorize: staged, modified, untracked, conflicts
    - Limit `MaxStatusFiles` (10) per category
    - Summary: `"Staged: N, Modified: M, Untracked: K"`
  - `compactGitLogOutput(s string, cfg CompactorConfig) (string, bool)`:
    - Keep up to `MaxLogEntries` (40) entries
    - Truncate each line to 80 chars

**Test Requirements:**
- Git diff compaction: large diff → file summaries with line counts + limited hunks
- Git status compaction: many files → categorized with limits
- Git log compaction: long log → truncated entries
- Non-git bash commands pass through unchanged
- Respects `compact_git_output = false` toggle

**Integration Notes:**
- Git compaction in bash pipeline handles `git` commands run via bash tool. Step 9 handles the dedicated git tools.

**Demo:** `git diff` with 500-line diff across 10 files → compacted to file summaries + key hunks.

---

## Step 6: Bash Pipeline — Linter Aggregation

**Objective:** Add linter output detection and grouping to complete the bash pipeline.

**Implementation Guidance:**
- Add to `compactor_bash.go`:
  - `isLinterCommand(cmd string) bool` — match `golangci-lint`, `eslint`, `ruff`, `pylint`, `clippy`, `flake8`, `mypy`, `rubocop`, `shellcheck`
  - `aggregateLinterOutput(s string, cfg CompactorConfig) (string, bool)`:
    - Parse `file:line:col: message [rule]` format (and variants)
    - Group by rule: count occurrences, show top `MaxLinterRules` (20)
    - Group by file: top `MaxLinterFiles` (20), with top 3 rules per file
    - Output format:
      ```
      Linter Summary: N issues

      By rule:
        unused-var (15): internal/tools/bash.go:10, ...
        error-check (8): internal/cli/cli.go:45, ...

      By file:
        internal/tools/bash.go (12 issues): unused-var(5), error-check(3), ...
      ```

**Test Requirements:**
- Parses `golangci-lint` output format
- Parses `eslint` output format
- Parses `ruff` output format
- Groups correctly by rule and file
- Respects limits (top 20 rules, top 20 files)
- Respects `aggregate_linter_output = false` toggle
- Full bash pipeline test: ANSI strip → (build|test|git|linter) → hard truncate

**Integration Notes:**
- Completes the bash pipeline. All 4 semantic detectors (build, test, git, linter) are mutually exclusive based on command detection. Fallback: if no detector matches, only ANSI strip + hard truncate apply.

**Demo:** `golangci-lint run` with 200 issues → grouped summary showing top rules and files.

---

## Step 7: Read Pipeline — Source Code Filtering and Smart Truncation

**Objective:** Implement the read tool compaction pipeline with source code filtering levels and priority-based smart truncation.

**Implementation Guidance:**
- Create `internal/tools/compactor_read.go`:
  - `compactRead(result map[string]any, cfg CompactorConfig) *CompactResult`
  - Skip compaction for: files under 80 lines, explicit range reads (offset/limit in args)
  - `detectLanguage(filePath string) string` — by file extension (.go, .py, .js, .ts, .rs, .java, etc.)
  - `filterSourceMinimal(s string, lang string) (string, bool)`:
    - Strip single-line comments (`//`, `#`, `--`)
    - Strip multi-line comments (`/* */`, `""" """`, `''' '''`)
    - Collapse runs of blank lines to single blank line
    - Preserve string literals (don't strip `//` inside strings)
  - `filterSourceAggressive(s string, lang string) (string, bool)`:
    - Keep: import/require blocks, function/method signatures, type/struct/class definitions, constants, package/module declarations
    - Remove: function bodies (replace with `// ...`)
    - Keep: first line of each block for context
  - `smartTruncate(s string, maxLines int) (string, bool)`:
    - Score each line by priority: imports (high), signatures (high), constants (medium), assignments (low), blanks (lowest)
    - Keep highest-priority lines up to maxLines
    - Preserve original order
    - Insert `// ... N lines omitted` markers at gaps

**Test Requirements:**
- Minimal filter strips Go comments, preserves code
- Minimal filter strips Python docstrings
- Aggressive filter keeps Go func signatures, removes bodies
- Aggressive filter keeps class/struct definitions
- Smart truncate keeps imports and signatures over body lines
- Skip compaction for files under 80 lines
- Skip compaction when source_code_filtering = "none" (only smart truncate + hard truncate)
- Pipeline: ANSI strip → source filter → smart truncate → hard truncate

**Integration Notes:**
- Read tool result has `Content` (line-numbered text) and `TotalLines` fields. Compactor operates on `Content`.

**Demo:** 1500-line Go file → aggressive filter → 200-line signatures-only view.

---

## Step 8: Search Pipeline — Grep, Find, Tree Compaction

**Objective:** Implement compaction for search-oriented tools.

**Implementation Guidance:**
- Create `internal/tools/compactor_search.go`:
  - `compactGrep(result map[string]any, cfg CompactorConfig) *CompactResult`:
    - Results are already structured (array of GrepMatch with File, Line, Content)
    - Group by file, limit `MaxSearchPerFile` (20) matches per file
    - Total limit: `MaxSearchTotal` (100) matches
    - Output summary: `"N matches in M files (showing top K)"`
  - `compactFind(result map[string]any, cfg CompactorConfig) *CompactResult`:
    - Results are array of file paths
    - Group by directory prefix
    - Limit total to `MaxSearchTotal` (100)
    - Summary: `"N files found (showing K)"`
  - `compactTree(result map[string]any, cfg CompactorConfig) *CompactResult`:
    - Tree output is already ASCII text with entry limit
    - Apply hard truncate only (tree is already fairly compact)
    - Strip ANSI if present

**Test Requirements:**
- Grep: 300 matches across 50 files → grouped, limited to 100
- Find: 800 files → limited to 100 with directory grouping
- Tree: passes through when under limits
- Respects `group_search_output = false` toggle (fallback to hard truncate only)

**Integration Notes:**
- Grep/find tools return structured data (arrays). Compactor modifies the array fields, not raw text.

**Demo:** Grep with 300 matches → grouped by file, top 100 shown with summary.

---

## Step 9: Git Tool Pipelines — git-diff, git-overview, git-hunk

**Objective:** Add compaction for the dedicated git tools (separate from `git` commands run via bash in Step 5).

**Implementation Guidance:**
- Add to `internal/tools/compactor_git.go`:
  - `compactGitDiffTool(result map[string]any, cfg CompactorConfig) *CompactResult`:
    - Result has `Diff` (text), `LinesAdded`, `LinesRemoved`, `File`
    - Apply diff compaction: limit hunk lines, add summary
  - `compactGitOverviewTool(result map[string]any, cfg CompactorConfig) *CompactResult`:
    - Result has `StagedFiles`, `UnstagedFiles`, `UntrackedFiles` (arrays)
    - Limit each category to `MaxStatusFiles` (10)
    - Add counts: `"Staged: N (showing 10), Untracked: M (showing 10)"`
  - `compactGitHunkTool(result map[string]any, cfg CompactorConfig) *CompactResult`:
    - Hunk output is targeted (specific lines) — lighter compaction
    - Apply hard truncate + ANSI strip only

**Test Requirements:**
- git-diff tool: large diff compacted to limits
- git-overview: 500 untracked files → limited to 10 per category
- git-hunk: passes through when small, truncated when large
- Respects `compact_git_output = false` toggle

**Integration Notes:**
- These tools return structured data. Compactor modifies specific fields (arrays, text fields).

**Demo:** git-overview with 300 untracked files → limited to 10 with count summary.

---

## Step 10: Metrics Tracking and Persistence

**Objective:** Implement CompactMetrics with per-session persistence and summary calculation.

**Implementation Guidance:**
- Create `internal/tools/compactor_metrics.go`:
  - `CompactMetrics` struct with mutex-protected records slice
  - `NewCompactMetrics() *CompactMetrics`
  - `(m *CompactMetrics) Record(r CompactRecord)` — append to records
  - `(m *CompactMetrics) Summary() MetricsSummary` — compute totals, per-tool breakdown, technique counts
  - `(m *CompactMetrics) Save(sessionDir string) error` — write `compactor-metrics.json`
  - `(m *CompactMetrics) Load(sessionDir string) error` — load existing metrics on session resume
  - `MetricsSummary` struct: TotalOrig, TotalComp, SavingsPct, ByTool map, ByTechnique map
  - `(s MetricsSummary) String() string` — formatted table for `/rtk stats`

**Test Requirements:**
- Record accumulation and summary calculation
- Save/load round-trip to JSON file
- Summary percentages calculated correctly
- Thread safety: concurrent Record() calls
- Empty metrics produce valid summary

**Integration Notes:**
- Metrics instance created in `cli.go`, passed to callback builder
- Save triggered on session end (or periodically)

**Demo:** After 20 tool calls with compaction, `/rtk stats` shows savings table.

---

## Step 11: TUI Integration — `/rtk stats` and Compaction Indicator

**Objective:** Add the `/rtk stats` slash command and compaction indicator to tool output display.

**Implementation Guidance:**
- In `internal/tui/tui.go`:
  - Add `compactMetrics *tools.CompactMetrics` to Model struct
  - Add `handleRtkCommand(args []string)` — displays `metrics.Summary().String()`
  - Register `/rtk` in slash command router
  - Add `/rtk` to help text
  - Add `compactInfo string` field to `message` struct
  - In tool output rendering: if `compactInfo` is non-empty, append styled indicator line
- In `internal/tui/tui.go` message construction:
  - When AfterToolCallback returns compacted result, attach compactInfo to the TUI message
  - This requires the callback to communicate compaction metadata back to the TUI
  - Approach: store compaction info in a side-channel (e.g., `CompactMetrics.LastCompaction()`) that TUI reads when rendering tool results

**Test Requirements:**
- `/rtk stats` displays formatted summary
- `/rtk stats` with no data shows "No compaction data yet"
- `/rtk` with unknown subcommand shows help
- Compaction indicator renders correctly in tool output
- Compaction indicator not present in result sent to LLM

**Integration Notes:**
- TUI needs access to CompactMetrics instance — passed during TUI construction.

**Demo:** Run several tool calls, then `/rtk stats` shows savings table. Tool outputs show `[compacted 85% · ansi,test-agg]` indicator.

---

## Step 12: Wire Callback, Remove Old Truncation, Integration Test

**Objective:** Connect everything: wire the compactor callback in cli.go, remove old truncation from tools, and verify end-to-end.

**Implementation Guidance:**
- In `internal/cli/cli.go`:
  - Load compactor config: `compactorCfg := cfg.CompactorConfig()` (with defaults)
  - Create metrics: `compactMetrics := tools.NewCompactMetrics()`
  - Build callback: `compactorCB := tools.BuildCompactorCallback(compactorCfg, compactMetrics)`
  - Insert in callback chain: after shell hooks, before LSP
    ```go
    afterCBs = append(afterCBs, compactorCB)
    afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))
    ```
  - Pass metrics to TUI constructor
- Remove old truncation:
  - `internal/tools/bash.go`: remove `truncateOutput()` calls on stdout/stderr
  - `internal/tools/read.go`: remove `truncateOutput()` call (keep line limit as input parameter, but remove byte truncation)
  - `internal/tools/grep.go`: remove `truncateLine()` calls
  - `internal/tools/tree.go`: remove `truncateOutput()` call
  - `internal/tools/git_diff.go`: remove `truncateOutput()` call
  - Keep `truncate.go` file (may be used elsewhere) but remove calls from tool implementations
- Integration test:
  - Test full flow: bash tool with verbose output → compactor callback → compacted result
  - Test callback ordering: shell hooks → compactor → LSP
  - Test config toggle: disabled compactor → original behavior

**Test Requirements:**
- End-to-end: `go test` output through full pipeline → compacted summary
- End-to-end: `go build` error output → filtered errors
- End-to-end: large file read → source-filtered output
- Callback ordering verified
- Compactor disabled → no modification
- Existing tool tests still pass (output format may change but structure preserved)

**Integration Notes:**
- This is the final step that activates the compactor. All previous steps are independently testable.
- Run full test suite after removing old truncation to catch any regressions.

**Demo:** Full pi-go session with compaction active. Run `go test`, `go build`, `git diff`, read large files — all outputs compacted. `/rtk stats` shows cumulative savings.
