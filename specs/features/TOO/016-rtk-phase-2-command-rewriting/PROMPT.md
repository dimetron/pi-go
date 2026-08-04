# RTK Compactor Phase 2 — Command Rewriting & High-Value Gaps

## Objective

Implement Phase 2 of the RTK output compactor (`specs/research/000-rtk-hooks-optimizer/`). Closes the highest-value
remaining gaps: language-aware source filtering, expanded bash command detection (ls/cat/find/env/docker), tee overflow
for lossy hard-truncation, a `never_worse` token-aware guard, and the missing TUI per-message compaction indicator. All
work extends `internal/tools/compactor_*.go` and `internal/tui/`. No new packages, no new external dependencies.

Full design and 13-step plan in `design.md` and `plan.md`.

## Key Requirements

1. **`never_worse` guard** — Replace byte-only `compSize >= origSize` checks with token-aware
   `isWorthReplacing(orig, filtered) bool` using `estimateTokens(s) = len(s) / 4`. Apply to every existing compaction
   stage. Catches the "filter added metadata overhead" bug class. Respects `compactor.never_worse` toggle (default
   `true`).

2. **Language-aware source filter** — Port upstream `rtk-ai/rtk`'s `Language` enum and per-language comment patterns.
   Cover: Go, Python, JS, TS, Rust, C, C++, Java, Ruby, Shell, Lua, Haskell, Lisp, Data (
   JSON/YAML/TOML/XML/CSV/SQL/.env/.lock/.md — never strip). Replaces the current language-agnostic `filterSourceCode`
   with `filterSourceCodeLang(s, lang, level)`. String-literal awareness so `//` inside Go strings is preserved.

3. **Read pipeline respects args and size** — Skip compaction when `args` has `offset` or `limit` keys (targeted reads
   pass through) or when `len(content) < cfg.MinReadLines` (default 80).

4. **Bash detector expansion (5 new)** — `ls` (group by directory, drop noise dirs), `cat` (language-aware source
   filter), `find` (group by directory, cap at 100), `env` (categorise PATH/LANG/cloud/tool, truncate long values),
   `docker` (column selection for ps/images, tail-dedup for logs). Each is a detector function + filter function pair,
   respects `compactor.detect_<name>` toggle (default `true`).

5. **Tee overflow** — When `hardTruncate` drops >50% of bytes and `tee_enabled = true`, write raw output to
   `~/.pi-go/sessions/<session-id>/tee/<slug>-<timestamp>.log`, append pointer line to compacted output. Rotate oldest
   files beyond `tee_max_files` (default 20). Cap individual files at `tee_max_bytes` (default 1MB). Best-effort: I/O
   errors log a warning, never block compaction.

6. **TUI per-message compaction indicator** — Add `compactInfo string` field to TUI `message` struct. Render as dimmed
   suffix `[compacted 85% · ansi,test-agg]` on tool-output messages only. Never serialized to the LLM-bound result.
   Plumbed via atomic-pointer side-channel from the AfterToolCallback to the TUI model. No-op in print/json/rpc modes.

7. **Per-detector config toggles** — `compactor.detect_ls`, `compactor.detect_cat`, `compactor.detect_find`,
   `compactor.detect_env`, `compactor.detect_docker`. All default `true`. Config-merge tests cover individual disable.

8. **New metrics technique tags** — `no-op-never-worse`, `ls-group`, `cat-source`, `find-group`, `env-categorize`,
   `docker-ps`, `docker-images`, `docker-logs`, `docker-inspect`, `tee-overflow`. All visible in `/rtk` stats.

## Acceptance Criteria

### Language-Aware Source Filter

- Given a 1500-line Python file with docstrings, when read tool fires with `source_code_filtering = "minimal"`, then
  triple-quoted `"""..."""` and `#` comments are stripped, `#` inside `f"..."` is preserved, and output is at least 30%
  smaller than raw
- Given a 500-line `package.json`, when read tool fires, then no comment stripping is attempted (`Language::Data`
  branch) and output equals input (modulo ANSI strip + smart truncate)
- Given a Go file with `//` inside a string literal `msg := "// not a comment"`, when minimal filter runs, then the
  string-literal content is preserved verbatim

### Bash Detectors

- Given bash output of `ls -la /repo` with 200 entries, when AfterToolCallback fires, then output is grouped by
  top-level directory, entries under `node_modules`, `.git`, `target`, `dist`, `vendor`, `venv`, `.venv`, `__pycache__`,
  `build`, `.next` are dropped, and result is at least 50% smaller with summary line
  `ls: 200→N entries (M in noise dirs)`
- Given bash output of `cat internal/tools/compactor.go`, when AfterToolCallback fires, then language is detected as Go,
  `//` and `/* */` comments are stripped, output is at least 20% smaller
- Given bash output of `find . -name "*.go"` with 1500 results, when AfterToolCallback fires, then results are grouped
  by directory, limited to 100, with summary `find: 1500→100 results (grouped, N dirs)`
- Given bash output of `env` with 200 variables, when AfterToolCallback fires, then variables are grouped by category (
  PATH, LANG, cloud, tool, other) in that order, values longer than 50 chars truncated to `preview (N chars)`, output at
  least 40% smaller
- Given bash output of `docker ps -a` with 50 containers, when AfterToolCallback fires, then output contains only
  CONTAINER ID, IMAGE, STATUS, PORTS, NAMES columns (CREATED dropped)
- Given `compactor.detect_docker = false` in config, when bash runs `docker ps`, then docker-specific filtering is NOT
  applied but all other enabled stages still fire

### Tee Overflow

- Given bash output of `git log --all --oneline` producing 800KB, when AfterToolCallback fires with
  `tee_enabled = true`, then compacted stdout is ≤ 24KB, `~/.pi-go/sessions/<id>/tee/git_log-<timestamp>.log` exists
  with 800KB content, and compacted output ends with `[full output: tee/git_log-<timestamp>.log]`
- Given tee write fails (disk full / permission denied), when AfterToolCallback fires, then compaction still completes,
  a warning is logged, and the compacted output is unchanged (no pointer line)
- Given 25 tee files already exist for the session, when a new tee write happens, then the 5 oldest are deleted (
  rotation to `tee_max_files = 20`)
- Given `compactor.tee_enabled = false`, when hard-truncation drops >50%, then no file is written, no pointer line
  appended

### `never_worse` Guard

- Given a 50-line build output, when `filterBuildOutput` adds 30 lines of "lines omitted" markers, then
  `isWorthReplacing` returns `false`, original output is returned, and metric technique `no-op-never-worse` is recorded
- Given `compactor.never_worse = false`, when any compaction runs, then the old byte-only check is used (fallback path)

### Read Pipeline Respects Args

- Given a read tool call with `args = {"offset": 100, "limit": 50}`, when AfterToolCallback fires, then no compaction is
  applied
- Given a read tool call with 40-line content and `cfg.MinReadLines = 80`, when AfterToolCallback fires, then no
  compaction is applied

### TUI Per-Message Indicator

- Given a bash tool call whose output is compacted 85%, when the TUI displays the tool result, then a small dimmed
  suffix `[compacted 85% · ansi,test-agg]` is visible
- The suffix is NOT present in the result map sent to the LLM
- The suffix is NOT visible in print, json, or rpc output modes

## Implementation Slices

1. **`never_worse` guard** — Create `internal/tools/compactor_guard.go` with `estimateTokens` and `isWorthReplacing`.
   Update all existing `compSize >= origSize` sites. Add `NeverWorse bool` to `CompactorConfig` (default `true`).
   Verify: `go test ./internal/tools/...`
2. **`Language` enum** — Create `internal/tools/compactor_lang.go` with `Language` enum, `LanguageFromExtension`,
   `Patterns()`, `IsData()`. Cover all upstream-supported languages plus data formats. Verify:
   `go test ./internal/tools/...`
3. **Language-aware source filter** — Add `filterSourceCodeLang` to `compactor_lang.go`. Replace `filterSourceCode` call
   sites in `compactor_read.go`. Add `detectLangFromArgs` helper. Verify: `go test ./internal/tools/...`
4. **Read pipeline respects args and size** — Update `compactRead` to skip when `args.offset/limit` present or
   `line_count < MinReadLines`. Add `MinReadLines int` to config (default 80). Verify: `go test ./internal/tools/...`
5. **Bash detector: `ls`** — Add `isLsCommand` and `compactLsOutput` to `compactor_bash_detect.go`. Wire into
   `compactBash`. Add `DetectLs` config toggle. Verify: `go test ./internal/tools/...`
6. **Bash detector: `cat`** — Add `isCatCommand` and `compactCatOutput`. Detect source-file language from command, apply
   `filterSourceCodeLang`. Add `DetectCat` toggle. Verify: `go test ./internal/tools/...`
7. **Bash detector: `find`** — Add `isFindCommand` and `compactFindBashOutput`. Directory grouping, 100-entry cap. Add
   `DetectFind` toggle. Verify: `go test ./internal/tools/...`
8. **Bash detector: `env`** — Add `isEnvCommand` and `compactEnvOutput`. Categorise into PATH/LANG/cloud/tool/other. Add
   `DetectEnv` toggle. Verify: `go test ./internal/tools/...`
9. **Bash detector: `docker`** — Add `isDockerCommand` and `compactDockerOutput` with subcommand dispatch (
   ps/images/logs/inspect). Add `DetectDocker` toggle. Verify: `go test ./internal/tools/...`
10. **Tee overflow** — Create `internal/tools/compactor_tee.go` with `TeeConfig` and `teeOverflow`. Plumb `sessionDir`
    into `BuildCompactorCallback`. Wire into `hardTruncate` (drop >50% threshold). Update `cli.go` and `interactive.go`
    to pass session dir. Verify: `go test ./internal/tools/...`
11. **TUI per-message indicator** — Add `compactInfo string` to TUI `message` struct. Add
    `lastCompactInfo *atomic.Pointer[string]` to Model. Plumb sink from `BuildCompactorCallback` to TUI model. Render
    dimmed suffix in tool-output `View()`. No-op in non-TUI modes. Verify: `go test ./internal/tui/...`
12. **Per-detector config toggles** — Add `DetectLs/Cat/Find/Env/Docker` to `CompactorConfig` and
    `DefaultCompactorConfig()`. Update `compactBash` to respect each. Add config-merge tests. Verify:
    `go test ./internal/tools/...`
13. **Integration tests + benchmarks + signoff** — Add `compactor_bench_test.go` with 5 benchmarks (LsOutput,
    CatOutput_Go, FilterSourceCodeLang, TeeOverflow, IsWorthReplacing). Add `compactor_integration_test.go` with
    end-to-end scenarios. Run `go build ./...`, `go vet ./...`, `golangci-lint run`, `go test ./... -count=1 -race`,
    `go test -tags e2e ./...`. All gates must pass. Signoff commit referencing this spec.

## Reference

- **Phase 1 spec** (shipped): `specs/research/000-rtk-hooks-optimizer/` — original RTK compactor design, requirements,
  12-step plan
- **Design doc**: `specs/features/TOO/016-rtk-phase-2-command-rewriting/design.md` — full architecture, data models,
  acceptance criteria, file plan
- **Implementation plan**: `specs/features/TOO/016-rtk-phase-2-command-rewriting/plan.md` — 13 steps with test
  requirements
- **Upstream reference**: `tmp/rtk/src/core/filter.rs`, `tmp/rtk/src/core/guard.rs`, `tmp/rtk/src/core/tee.rs` —
  implementations to port

## Constraints

- **No new external dependencies** — pure Go stdlib only (`regexp`, `strings`, `path/filepath`, `encoding/json`, `os`,
  `sync/atomic`)
- **No new packages** — all work extends `internal/tools/` and `internal/tui/`
- **Backwards compatible** — every new config field has a default; every new toggle defaults to its spec'd state (mostly
  `true`); no breaking changes to existing public APIs
- **Tee is best-effort** — I/O errors log a warning and return empty pointer, never block compaction
- **Existing tests must still pass** — `compactor_test.go` is 1436 LOC; no regressions allowed
- **Race-safe** — any new shared state (compactInfo side-channel, tee directory) must pass `go test -race`
- **Performance budget** — new filters add <2ms to typical 10KB output; benchmarks in `compactor_bench_test.go` enforce
  the budget
- **Coverage bar** — new code covered by ≥85% unit tests, plus integration tests, plus benchmarks
- **Naming preserved** — keep `/rtk` slash command, keep `compactor.*` config namespace, keep `compactor_*` file naming

## Gates

- `go build ./...` — must pass
- `go vet ./...` — must pass
- `golangci-lint run` — must pass with zero warnings (matches repo standard)
- `go test ./... -count=1` — all existing tests + new tests pass
- `go test ./... -race` — race detector clean (tee writes, TUI side-channel)
- `go test -tags e2e ./...` — E2E tests pass
- `go test -bench=. ./internal/tools/ -benchmem -run=^$` — benchmark results recorded; new filters meet <2ms / <5ms / <
  10ms / <20ms targets
- Coverage of new code ≥85% (matches the 80%+ repo standard)
