# Design — RTK Compactor Phase 2

## Overview

Phase 2 of the RTK compactor at `specs/research/000-rtk-hooks-optimizer/`. Closes the highest-value remaining gaps:
language-aware source filtering, expanded bash command detection (ls/cat/find/env/docker), tee overflow for lossy
hard-truncation, a `never_worse` token-aware guard, and the missing TUI per-message compaction indicator.

**No new packages.** All work extends `internal/tools/compactor_*.go` and `internal/tui/`. Backwards compatible — every
new feature has a config flag defaulting to its spec'd state (mostly `true`).

**Reference impls** in `tmp/rtk/`:

- Language-aware filtering: `tmp/rtk/src/core/filter.rs` (Language enum, FilterLevel, FilterStrategy trait)
- `never_worse`: `tmp/rtk/src/core/guard.rs` (bytes/4 token estimate, returns raw when filtered is worse)
- Tee overflow: `tmp/rtk/src/core/tee.rs` (slug sanitisation, file rotation, env-var override)

---

## Detailed Requirements

### Functional

1. **Language-aware source filter** — detect file language from extension; apply per-language comment patterns; data
   formats (JSON/YAML/TOML/XML/CSV/SQL/...) skip comment stripping entirely
2. **Read pipeline respects args** — skip compaction if `args` contains `offset` or `limit`; skip if
   `len(content) < 80 lines` (new config `compactor.min_read_lines`)
3. **Bash detector expansion** — 5 new detectors: `ls`, `cat`, `find`, `env`, `docker`. Each routes to a dedicated
   filter.
4. **Tee overflow** — when `hardTruncate` drops >50% of bytes, write raw to
   `~/.pi-go/sessions/<id>/tee/<slug>-<ts>.log`, append pointer line to compacted output
5. **`never_worse` guard** — replace byte-only `compSize >= origSize` with token-aware check using
   `estimateTokens(s) = len(s) / 4` (matches upstream)
6. **TUI per-message compaction indicator** — render `[compacted 85% · ansi,test-agg]` suffix on tool-output TUI
   messages; never sent to LLM
7. **Per-detector config toggles** — `compactor.detect_ls`, `compactor.detect_cat`, etc. All default `true`. Allows
   power users to disable individual detectors without touching the main enable flag.

### Non-Functional

- New filters add <2ms to typical 10KB output (benchmarked)
- No new external dependencies; pure Go stdlib (`regexp`, `strings`, `path/filepath`, `encoding/json`, `os`)
- Tee writes are best-effort: failure logs a warning, never blocks the tool result
- All new code paths covered by both unit and integration tests; new benchmarks in `compactor_bench_test.go`

---

## Architecture

```
                  ┌──────────────────────────────────────────┐
   bash tool      │  AfterToolCallback                       │
   output ──────► │  (compactor)                             │
                  │    │                                     │
                  │    ▼                                     │
                  │  compactBash(args, result, cfg)          │
                  │    │                                     │
                  │    ├─ detectCommand(args)                │
                  │    │   └─ normalize (strip env vars,     │
                  │    │      take last segment after &&)    │
                  │    │                                     │
                  │    ├─ stage 1: ANSI strip (always)       │
                  │    │                                     │
                  │    ├─ stage 2: command-specific filter   │  ← NEW
                  │    │     switch on detector:             │
                  │    │       ls    → compactLsOutput       │
                  │    │       cat   → compactCatOutput       │
                  │    │       find  → compactFindBashOutput  │
                  │    │       env   → compactEnvOutput       │
                  │    │       docker→ compactDockerOutput    │
                  │    │   else fall through to existing     │
                  │    │     build/test/git/linter stages    │
                  │    │                                     │
                  │    ├─ stage 3..7: existing stages        │
                  │    │                                     │
                  │    └─ stage 8: hardTruncate + tee        │  ← CHANGED
                  │       if truncated and tee_enabled      │
                  │         and compSize < origSize/2:       │
                  │         tee raw to disk, append pointer  │
                  │    │                                     │
                  │    └─ stage 9: never_worse guard         │  ← NEW
                  │       if estTokens(filtered) >=         │
                  │          estTokens(orig):                │
                  │         return orig, record technique    │
                  │         = "no-op-never-worse"            │
                  │    │                                     │
                  │    ▼                                     │
                  │  CompactResult{ Output, Techniques,     │
                  │                    OrigSize, CompSize } │
                  └──────────────────────────────────────────┘
```

### New `compactRead` logic

```
read tool        compactRead(result, cfg)
output ──────►     ├─ if args has "offset" or "limit": skip
                  ├─ if line count < cfg.MinReadLines: skip
                  ├─ stage 1: ANSI strip
                  ├─ stage 2: source filter (NEW: language-aware)
                  │     detectLanguage(filePath from args)
                  │     if Language::Data: skip comment stripping
                  │     else: apply per-language comment patterns
                  ├─ stage 3: smart truncate
                  └─ stage 4: hard truncate + tee + never_worse
```

### TUI per-message indicator

```
compactor callback
  ├─ on success, set message.compactInfo = fmt.Sprintf(
  │     "[compacted %d%% · %s]", savingsPct, strings.Join(techniques, ","))
  │
  └─ TUI message struct gains:
       type message struct {
         // existing fields
         compactInfo string  // "" when not compacted
       }
       View() renders compactInfo as a small styled suffix
       on tool output messages only; chat text messages unchanged.
       compactInfo is NEVER serialized to the LLM-bound result.
```

---

## Components and Interfaces

### New types (`internal/tools/compactor_lang.go`)

```go
// Language identifies a source-file language for comment-pattern selection.
type Language int

const (
    LangUnknown Language = iota
    LangGo
    LangPython
    LangJavaScript
    LangTypeScript
    LangRust
    LangC
    LangCpp
    LangJava
    LangRuby
    LangShell
    LangLua
    LangHaskell
    LangLisp
    LangData // JSON, YAML, TOML, XML, CSV, SQL, .env — no comment stripping
)

// CommentPatterns holds the single-line and block-comment markers for a language.
type CommentPatterns struct {
    Line       []string // e.g. {"//"} for Go, {"#"} for Python
    BlockOpen  string   // e.g. "/*" for Go, `"""` for Python
    BlockClose string   // e.g. "*/" for Go, `"""` for Python
    Shebang    bool     // whether #! is recognised (shell, python, ruby)
}

func LanguageFromExtension(ext string) Language
func (l Language) Patterns() CommentPatterns
func (l Language) IsData() bool { return l == LangData }
```

### New `filterSourceCodeLang` (replaces `filterSourceCode`)

```go
// filterSourceCodeLang removes comments per language. Replaces filterSourceCode
// (which kept the old naive behaviour for the deprecated "minimal" mode).
func filterSourceCodeLang(s string, lang Language, level string) (string, bool) {
    if lang.IsData() || level == "none" {
        return s, false
    }
    // ... per-language patterns, block tracking, string-literal awareness
}
```

### New bash filters (`internal/tools/compactor_bash_detect.go`)

```go
// Detector functions: pure, no side effects.
func isLsCommand(cmd string) bool
func isCatCommand(cmd string) bool
func isFindCommand(cmd string) bool
func isEnvCommand(cmd string) bool
func isDockerCommand(cmd string) bool

// Filter functions: (string, bool) signature, same as existing.
func compactLsOutput(s string, cfg CompactorConfig) (string, bool)
func compactCatOutput(s string, cfg CompactorConfig, filePath string) (string, bool)
func compactFindBashOutput(s string, cfg CompactorConfig) (string, bool)
func compactEnvOutput(s string, cfg CompactorConfig) (string, bool)
func compactDockerOutput(s string, cmd string, cfg CompactorConfig) (string, bool)
```

### New `never_worse` guard (`internal/tools/compactor_guard.go`)

```go
// estimateTokens approximates token count as bytes/4 (matches upstream RTK).
func estimateTokens(s string) int { return len(s) / 4 }

// isWorthReplacing returns true if filtered saves ≥0 tokens over orig.
// Replaces the byte-only `compSize >= origSize` check everywhere.
func isWorthReplacing(orig, filtered string) bool {
    return estimateTokens(filtered) < estimateTokens(orig)
}
```

### New tee overflow (`internal/tools/compactor_tee.go`)

```go
type TeeConfig struct {
    Enabled    bool   `json:"tee_enabled"`
    Directory  string `json:"tee_dir,omitempty"` // default: ~/.pi-go/sessions/<id>/tee/
    MaxFiles   int    `json:"tee_max_files"`     // default 20
    MaxBytes   int    `json:"tee_max_bytes"`     // default 1MB
}

// teeOverflow writes raw to disk and returns a one-line pointer to append.
// Best-effort: returns empty string on any I/O error (after logging).
func teeOverflow(raw, commandSlug, sessionDir string, cfg TeeConfig) string
```

### `CompactorConfig` additions

```go
type CompactorConfig struct {
    // ... existing fields ...
    MinReadLines     int             `json:"min_read_lines"`     // default 80
    TeeEnabled       bool            `json:"tee_enabled"`        // default true
    TeeDirectory     string          `json:"tee_dir,omitempty"`
    TeeMaxFiles      int             `json:"tee_max_files"`      // default 20
    TeeMaxBytes      int             `json:"tee_max_bytes"`      // default 1MB
    DetectLs         bool            `json:"detect_ls"`          // default true
    DetectCat        bool            `json:"detect_cat"`         // default true
    DetectFind       bool            `json:"detect_find"`        // default true
    DetectEnv        bool            `json:"detect_env"`         // default true
    DetectDocker     bool            `json:"detect_docker"`      // default true
}
```

### TUI message addition (`internal/tui/tui.go`)

```go
type message struct {
    // ... existing fields ...
    compactInfo string // e.g. "[compacted 85% · ansi,test-agg]"; never sent to LLM
}

// In AfterToolCallback return path:
//   if compacted != nil {
//       compactMetrics.Record(...)
//       applyCompaction(result, compacted)
//       // NEW: set message.compactInfo via side-channel
//       tui.SetLastCompactInfo(formatCompactInfo(compacted))
//   }
```

---

## Data Models

### Config.json Extension

```json
{
  "compactor": {
    "min_read_lines": 80,
    "tee_enabled": true,
    "tee_dir": null,
    "tee_max_files": 20,
    "tee_max_bytes": 1048576,
    "detect_ls": true,
    "detect_cat": true,
    "detect_find": true,
    "detect_env": true,
    "detect_docker": true
  }
}
```

All new fields optional — defaults applied for missing values.

### Tee Directory Layout

```
~/.pi-go/sessions/<session-id>/
├── meta.json
├── events.jsonl
├── compactor-metrics.json
└── tee/                                ← NEW
    ├── git_log-20260317-120015.log
    ├── ls_-la-20260317-120342.log
    └── cat_foo_go-20260317-120501.log
```

Filename pattern: `<slug>-<YYYYMMDD-HHMMSS>.log`. Slug is the command with non-alphanumerics → `_`, truncated to 40
chars. Rotation: keep last `MaxFiles` (default 20), oldest deleted on write.

### TUI Indicator

```
─── bash ──────────────────────────────── [compacted 93% · ansi,test-agg]
ok  github.com/foo/bar  0.042s
Test Summary: PASS=42 FAIL=0 SKIP=0
```

The `[compacted 93% · ansi,test-agg]` line is rendered in dimmed style. Never present in `result` map going to the LLM;
only in the TUI-side `message` struct.

---

## Error Handling

- **Filter panic** — existing `runStage` deferred recover handles; unchanged.
- **Tee I/O error** — log warning, append nothing, compaction continues.
- **Language detection miss** — fall through to `LangUnknown` which uses generic `//` and `#` patterns (current
  behaviour).
- **Detector false positive** — every detector has a `notA` negative test (e.g. `isCatCommand("cat foo")` → true;
  `isCatCommand("catcatch")` → false). Detector also re-checks output shape before filtering.
- **`never_worse` false negative** — none; by construction it never makes output worse, only sometimes returns raw.
- **TUI compactInfo side-channel** — uses a `lastCompactInfo *atomic.Pointer[string]` in TUI model, written by callback,
  read by `View()`. If TUI not running (print/json mode), callback writes nothing.

---

## Acceptance Criteria

### Language-aware source filter

```
Given a 1500-line Python file with docstrings
When read tool fires and SourceCodeFiltering = "minimal"
Then triple-quoted docstrings ("""...""") are stripped
And single-line # comments are stripped
And string literals containing # are preserved
And the output is at least 30% smaller than raw

Given a 500-line package.json file
When read tool fires
Then no comment stripping is attempted (language = Data)
And output equals input (modulo ANSI strip + smart truncate)
```

### Bash detector: ls

```
Given bash output of `ls -la /repo` with 200 entries
When AfterToolCallback fires
Then output is grouped by top-level directory
And entries under node_modules, .git, target, dist, vendor, venv, .venv are dropped
And the result is at least 50% smaller than raw
And the line "ls: compacted 200→45 entries (noise dirs filtered)" is prepended
```

### Bash detector: cat (source)

```
Given bash output of `cat internal/tools/compactor.go`
When AfterToolCallback fires
Then the Language detector identifies it as Go
And Go comments (// and /* */) are stripped
And the output is at least 20% smaller than raw
```

### Bash detector: find

```
Given bash output of `find . -name "*.go"` with 1500 results
When AfterToolCallback fires
Then results are grouped by directory
And limited to 100 entries
And the result includes a summary line: "find: 1500→100 results (grouped)"
```

### Bash detector: env

```
Given bash output of `env` with 200 variables
When AfterToolCallback fires
Then variables are grouped by category (PATH, LANG, cloud, tool, other)
And values longer than 50 chars are truncated with "... (N chars)" suffix
And the result is at least 40% smaller than raw
```

### Bash detector: docker

```
Given bash output of `docker ps -a` with 50 containers
When AfterToolCallback fires
Then only NAME, STATUS, PORTS columns are kept
And CREATED and IMAGE columns are dropped
```

### Tee overflow

```
Given bash output of `git log --all --oneline` producing 800KB
When AfterToolCallback fires and cfg.TeeEnabled = true
Then compacted stdout is ≤ 24KB
And ~/.pi-go/sessions/<id>/tee/git_log-...log exists with 800KB content
And the compacted output ends with: "[full output: tee/git_log-...log]"

Given tee write fails (disk full)
When AfterToolCallback fires
Then compaction still completes
And a warning is logged
And the compacted output is unchanged (no pointer line)
```

### `never_worse` guard

```
Given a 50-line build output
When filterBuildOutput adds 30 lines of "lines omitted" markers
Then isWorthReplacing returns false (filtered is 30% larger in tokens)
And the original output is returned
And a technique "no-op-never-worse" is recorded in metrics
```

### Read pipeline respects args

```
Given a read tool call with args={"offset": 100, "limit": 50}
When AfterToolCallback fires
Then no compaction is applied (targeted read passes through)

Given a read tool call with 40-line file
When AfterToolCallback fires
Then no compaction is applied (below MinReadLines threshold)
```

### TUI per-message indicator

```
Given a bash tool call whose output is compacted 85%
When the TUI displays the tool result message
Then a small dimmed suffix appears: [compacted 85% · ansi,test-agg]
And the suffix is NOT present in the result map sent to the LLM
And the suffix is NOT visible in non-TUI output modes (print, json, rpc)
```

### Per-detector config toggles

```
Given config.json has compactor.detect_docker = false
When bash tool runs `docker ps`
Then docker-specific filtering is NOT applied
And all other enabled stages still fire (ANSI strip, hard truncate, etc.)
```

---

## Testing Strategy

### Unit Tests

- `TestLanguageFromExtension` — every supported extension maps to correct `Language`
- `TestFilterSourceCodeLang` — per-language golden file fixtures (Go/Py/JS/TS/Rust/Ruby/Shell)
- `TestFilterSourceCodeLang_DataSkipped` — JSON/YAML/TOML/XML/CSV input equals output
- `TestCompactLsOutput` — `ls -la` golden; noise dir exclusion
- `TestCompactCatOutput` — source files per language
- `TestCompactFindBashOutput` — directory grouping, 100-result cap
- `TestCompactEnvOutput` — categorisation, 50-char truncation
- `TestCompactDockerOutput` — column selection
- `TestIsWorthReplacing` — token estimate edge cases
- `TestTeeOverflow` — file write, slug sanitisation, rotation
- `TestTeeOverflowDisabled` — no-op when `TeeEnabled=false`
- `TestTeeOverflowIOError` — graceful when directory unwritable
- `TestCompactRead_RespectsArgs` — offset/limit pass-through
- `TestCompactRead_SkipsShortFiles` — below-threshold pass-through
- `TestConfigMergesDefaults` — new fields default correctly

### Integration Tests

- Full pipeline: `go test ./...` output → all 5 new detectors fire correctly on representative commands
- Tee rotation: write 25 files, verify oldest 5 deleted
- TUI indicator: compactInfo appears in TUI view, absent from LLM-bound result

### Benchmarks (`compactor_bench_test.go`)

- `BenchmarkCompactLsOutput` — 10KB `ls` output → <2ms
- `BenchmarkCompactCatOutput_Go` — 50KB Go file → <5ms
- `BenchmarkFilterSourceCodeLang` — 100KB Go file → <10ms
- `BenchmarkTeeOverflow` — write 1MB → <20ms
- `BenchmarkIsWorthReplacing` — 100KB strings → <1ms

### Regression Tests

- All existing `compactor_test.go` tests still pass (no behaviour change when new fields are at defaults)
- `never_worse` doesn't regress any existing compaction stage

---

## Appendices

### A. Technology Choices

- **Pure Go** — no new external dependencies
- **regexp stdlib** — pre-compiled at package init for hot-path filters (matches existing `compactor_bash.go` pattern)
- **os.WriteFile + atomic rename** for tee writes (matches existing session-write pattern)
- **atomic.Pointer[string]** for TUI compactInfo side-channel (avoids mutex contention in TUI render loop)

### B. Research Findings

#### B.1 Upstream `rtk-ai/rtk` `core/filter.rs`

Has a `Language` enum (Rust/Python/JS/TS/Go/C/Cpp/Java/Ruby/Shell/Lua/Haskell/Lisp/Data/Unknown) with per-language
`CommentPatterns`. `Language::from_extension` maps file extension to enum. Data formats (JSON/YAML/TOML/XML/CSV) are
explicitly marked `Data` and skip comment stripping. Strategy pattern via `FilterStrategy` trait with
`get_filter(level) -> Box<dyn FilterStrategy>` factory.

#### B.2 Upstream `tmp/rtk/src/core/guard.rs`

```rust
pub fn never_worse<'a>(raw: &'a str, filtered: &'a str) -> &'a str {
    if estimate_tokens(filtered) > estimate_tokens(raw) { raw } else { filtered }
}
```

`estimate_tokens = bytes / 4`. Test suite verifies tie-breaking (filtered wins on tie) and empty-input handling. Our Go
port follows the same semantics, with `isWorthReplacing` for boolean checks.

#### B.3 Upstream `tmp/rtk/src/core/tee.rs`

- `MIN_TEE_SIZE = 500` — don't tee small outputs (we use threshold-based: drop >50%)
- Slug sanitisation: non-alphanum → `_`, truncate to 40 chars
- Env override: `RTK_TEE_DIR` (we use `compactor.tee_dir` config field)
- Default location: `~/.local/share/rtk/tee/` (we use `~/.pi-go/sessions/<id>/tee/`)
- Rotation: keep last 20 files, delete oldest

### C. Alternative Approaches Considered

1. **Per-tool string-format detection instead of detector functions** — e.g. peek at output and decide. Rejected:
   brittle, fails on small outputs, doesn't know the command.
2. **Compile a list of rewrite rules in a single `rules.go` file** (matches upstream `discover/rules.rs`). Rejected:
   detector functions are easier to test and reason about; no regex engine needed.
3. **Use `cmd/subcommands` pattern from upstream** (one file per command, registry of handlers). Rejected: overkill for
   5 new commands; existing `isXxxCommand` + `compactXxxOutput` pattern fits our codebase.
4. **External `rtk` binary for all new filters** (call out to upstream). Rejected: contradicts the "no external runtime
   deps" rule in `AGENTS.md`; we already own the compactor.

### D. File Plan

| File                                           | Status   | LOC est.                           |
|------------------------------------------------|----------|------------------------------------|
| `internal/tools/compactor_lang.go`             | NEW      | ~250                               |
| `internal/tools/compactor_lang_test.go`        | NEW      | ~300                               |
| `internal/tools/compactor_bash_detect.go`      | NEW      | ~250                               |
| `internal/tools/compactor_bash_detect_test.go` | NEW      | ~350                               |
| `internal/tools/compactor_guard.go`            | NEW      | ~30                                |
| `internal/tools/compactor_guard_test.go`       | NEW      | ~80                                |
| `internal/tools/compactor_tee.go`              | NEW      | ~120                               |
| `internal/tools/compactor_tee_test.go`         | NEW      | ~150                               |
| `internal/tools/compactor.go`                  | MODIFIED | +30 (new config fields)            |
| `internal/tools/compactor_bash.go`             | MODIFIED | +50 (route to new detectors)       |
| `internal/tools/compactor_read.go`             | MODIFIED | +20 (use lang filter, skip short)  |
| `internal/tools/compactor_test.go`             | MODIFIED | +50 (default-merging tests)        |
| `internal/tui/tui.go`                          | MODIFIED | +60 (compactInfo field + render)   |
| `internal/tui/compactor_indicator_test.go`     | NEW      | ~100                               |
| `internal/config/config.go`                    | MODIFIED | (no change — uses generic merge)   |
| `internal/cli/cli.go`                          | MODIFIED | +5 (pass sessionDir to tee config) |
| **Total**                                      |          | **~1845 LOC**                      |
