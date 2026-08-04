# Implementation Plan — RTK Compactor Phase 2

## Checklist

- [ ] Step 1: `never_worse` guard + apply to all existing stages
- [ ] Step 2: `Language` enum + per-language comment patterns (`compactor_lang.go`)
- [ ] Step 3: `filterSourceCodeLang` + replace `filterSourceCode` call sites
- [ ] Step 4: Read pipeline respects `args.offset/limit` and `cfg.MinReadLines`
- [ ] Step 5: Bash detector `ls` (`compactLsOutput`)
- [ ] Step 6: Bash detector `cat` (`compactCatOutput`)
- [ ] Step 7: Bash detector `find` (`compactFindBashOutput`)
- [ ] Step 8: Bash detector `env` (`compactEnvOutput`)
- [ ] Step 9: Bash detector `docker` (`compactDockerOutput`)
- [ ] Step 10: Tee overflow (`compactor_tee.go`) + wire into `hardTruncate`
- [ ] Step 11: TUI per-message compaction indicator
- [ ] Step 12: Per-detector config toggles + config-merge tests
- [ ] Step 13: Integration tests + benchmarks + final signoff

---

## Step 1: `never_worse` Guard

**Objective:** Add token-aware `isWorthReplacing(orig, filtered)` and replace all byte-only `compSize >= origSize`
checks with it. Catches "filter added metadata overhead" bugs that byte checks miss.

**Implementation:**

- Create `internal/tools/compactor_guard.go`:
    - `estimateTokens(s string) int { return len(s) / 4 }` (matches upstream)
    - `isWorthReplacing(orig, filtered string) bool { return estimateTokens(filtered) < estimateTokens(orig) }`
- Find all sites in `compactor_*.go` that do `if compSize >= origSize { return nil }` and replace with
  `if !isWorthReplacing(...) { ... }`. Include a technique tag `"no-op-never-worse"` in the metrics record for
  visibility.
- Add `CompactorConfig.NeverWorse` bool toggle (default `true`); when off, fall back to old byte check.

**Test Requirements:**

- `TestEstimateTokens` — `""` → 0, `"abcd"` → 1, `"abcde"` → 1
- `TestIsWorthReplacing_TokenAware` — filtered saves 3 bytes but adds 4 tokens of markers → returns false
- `TestIsWorthReplacing_ByteVsToken` — 50-line input with 30 lines of markers: byte check would say "worse", token check
  says "worse" → same answer; 100-byte input with 1-line filter: byte check says "better", token check says "worse" →
  divergent
- All existing `compactor_test.go` tests still pass

**Demo:** `filterBuildOutput` on a 50-line input adds 3 lines of "lines omitted" markers; metric shows technique
`"no-op-never-worse"` and original is returned.

---

## Step 2: `Language` Enum and Comment Patterns

**Objective:** Port the upstream `Language` enum and per-language comment patterns. Data formats (JSON/YAML/TOML/etc.)
skip comment stripping entirely.

**Implementation:**

- Create `internal/tools/compactor_lang.go`:
    - `Language` int enum:
      `LangUnknown, LangGo, LangPython, LangJavaScript, LangTypeScript, LangRust, LangC, LangCpp, LangJava, LangRuby, LangShell, LangLua, LangHaskell, LangLisp, LangData`
    - `LanguageFromExtension(ext string) Language` — pure function, covers all extensions in the upstream reference plus
      `.tsx`, `.jsx`, `.pyw`, `.cjs`, `.mjs`, `.hpp`, `.hh`, `.cc`, `.cxx`
    - `(l Language) Patterns() CommentPatterns` — returns the per-language `Line []string`, `BlockOpen string`,
      `BlockClose string`, `Shebang bool`
    - `(l Language) IsData() bool { return l == LangData }` — for the data-format branch
    - All regex/string constants as package-level `var` (pre-compiled at init for hot-path use)

**Test Requirements:**

- `TestLanguageFromExtension` — every supported extension maps to correct `Language` (table-driven, ≥15 cases)
- `TestLanguageFromExtension_DataFormats` — `.json`, `.yaml`, `.yml`, `.toml`, `.xml`, `.csv`, `.sql`, `.env`, `.lock`,
  `.md` all → `LangData`
- `TestLanguageFromExtension_UnknownExt` — `.foo`, `` (empty), `.go.bak` → `LangUnknown`
- `TestLanguagePatterns` — each language returns non-empty patterns (or `IsData()` for data formats)
- Case-insensitive: `.GO` and `.go` both → `LangGo`

**Demo:** `LanguageFromExtension("foo.py")` returns `LangPython`; `LanguageFromExtension("Cargo.lock")` returns
`LangData`; `LanguageFromExtension("")` returns `LangUnknown`.

---

## Step 3: Language-Aware Source Filter

**Objective:** Replace the language-agnostic `filterSourceCode` with `filterSourceCodeLang` that respects the detected
language. Doc comments kept in Go, stripped in Python, etc.

**Implementation:**

- Add to `internal/tools/compactor_lang.go`:
    - `filterSourceCodeLang(s string, lang Language, level string) (string, bool) (string, bool)`:
        - If `lang.IsData()` or `level == "none"`: return `(s, false)` (data formats never get comments stripped)
        - If `level == "aggressive"`: keep only signatures/imports (existing aggressive behaviour); use language to
          identify function/class syntax
        - If `level == "minimal"`: strip per-language line + block comments, preserve string literals (the missing piece
          from current filter)
- String-literal awareness: track if inside `"..."`, `'...'`, or `` `...` ``; don't strip comment markers inside
- In `compactRead`: replace `filterSourceCode(s, cfg.SourceCodeFiltering)` with
  `filterSourceCodeLang(s, detectLangFromArgs(args), cfg.SourceCodeFiltering)`
- New helper: `detectLangFromArgs(args map[string]any) Language` — extracts `path`/`file_path` from args, returns
  `LanguageFromExtension(filepath.Ext(path))`

**Test Requirements:**

- `TestFilterSourceCodeLang_Go` — `//` and `/* */` stripped, `//` inside `"..."` preserved, doc comments (above `func`)
  stripped
- `TestFilterSourceCodeLang_Python` — `#` and `"""..."""` stripped, `#` inside `f"..."` preserved
- `TestFilterSourceCodeLang_JavaScript` — `//`, `/* */` stripped, regex literals `/\d+/` not mis-classified
- `TestFilterSourceCodeLang_Rust` — `//` and `/* */` stripped
- `TestFilterSourceCodeLang_Ruby` — `#` stripped, `=begin...=end` block stripped
- `TestFilterSourceCodeLang_Shell` — `#` stripped, `#!/bin/bash` shebang preserved
- `TestFilterSourceCodeLang_Data` — JSON/YAML/TOML input equals output (no stripping)
- `TestFilterSourceCodeLang_Aggressive` — function bodies replaced with `// ...`, signatures kept
- Existing `TestFilterSourceCode*` tests retained and pass with new implementation (or migrated to use the new helper)

**Demo:** 1500-line Python file with docstrings and string literals → minimal filter strips 40% of bytes, no
string-literal corruption.

---

## Step 4: Read Pipeline Respects Args and Min Size

**Objective:** Skip compaction for targeted reads (offset/limit) and short files. Spec 000 Step 7 called for this; we
never landed it.

**Implementation:**

- In `compactRead`, before any compaction:
    - If `args` has key `offset` (non-nil, non-zero) or `limit` (non-nil, non-zero): return `nil` (pass through)
    - If `strings.Count(content, "\n") < cfg.MinReadLines`: return `nil` (too short to benefit)
- New config field: `MinReadLines int` (default 80) in `CompactorConfig`

**Test Requirements:**

- `TestCompactRead_OffsetPresent` — `args={"offset": 100, "limit": 50}` → returns nil, no compaction
- `TestCompactRead_LimitPresent` — `args={"limit": 30}` → returns nil
- `TestCompactRead_BelowMinLines` — 40-line content with `cfg.MinReadLines=80` → returns nil
- `TestCompactRead_AboveMinLines` — 200-line content → compaction runs normally
- `TestCompactRead_ArgsMissing` — `args=nil` or `args={}` → compaction runs

**Demo:** `read foo.go:10-25` (targeted range) bypasses compaction; `read foo.go` on a 50-line file also bypasses;
`read foo.go` on a 2000-line file compacts normally.

---

## Step 5: Bash Detector `ls`

**Objective:** Detect `ls` commands in bash output and apply directory-grouped, noise-dir-filtered compaction.

**Implementation:**

- Add to `internal/tools/compactor_bash_detect.go`:
    - `isLsCommand(cmd string) bool` — matches `^ls\b` but not `^lsof`, `^lsblk`, etc. (word-boundary)
    - `compactLsOutput(s string, cfg CompactorConfig) (string, bool)`:
        - Group entries by top-level directory (`path.Split`)
        - Drop entries under noise dirs: `.git`, `node_modules`, `target`, `dist`, `vendor`, `__pycache__`, `venv`,
          `.venv`, `build`, `.next`
        - Count dropped entries; include in summary line
- Wire into `compactBash` between ANSI strip and existing detectors
- Add `compactor.detect_ls` config toggle (default `true`)
- New metric technique tag: `"ls-group"`

**Test Requirements:**

- `TestIsLsCommand` — `ls -la` → true; `ls -la /tmp` → true; `lsof` → false; `lsblk` → false
- `TestCompactLsOutput_Groups` — 50 entries across 5 dirs → grouped, summary line `ls: 50→45 entries (5 in noise dirs)`
- `TestCompactLsOutput_NoiseDirsFiltered` — entries under `node_modules/` dropped
- `TestCompactLsOutput_EmptyDir` — `total 0` header preserved
- `TestCompactLsOutput_NotAListing` — input that doesn't match `ls` format → returns `(s, false)`

**Demo:** `ls -la /repo` with 200 entries including 50 under `node_modules/` → 150 entries shown, grouped, summary.

---

## Step 6: Bash Detector `cat` (Source)

**Objective:** Detect `cat <source-file>` and apply language-aware source filter.

**Implementation:**

- Add to `compactor_bash_detect.go`:
    - `isCatCommand(cmd string) bool` — matches `^cat\b`
    - `compactCatOutput(s string, cfg CompactorConfig, filePath string) (string, bool)`:
        - Detect language from `filePath`
        - Route to `filterSourceCodeLang` with `level = cfg.SourceCodeFiltering`
        - For data files, pass through (cat of a `.json` is usually a deliberate read)
- In `compactBash`, extract the file path from the command (`cat <path>`); for pipes (`cat foo.go | head -10`), use the
  first source arg
- Add `compactor.detect_cat` config toggle (default `true`)
- New metric technique tag: `"cat-source"`

**Test Requirements:**

- `TestIsCatCommand` — `cat foo.go` → true; `catch` → false; `catfish` → false
- `TestCompactCatOutput_GoFile` — Go source stripped of comments via language filter
- `TestCompactCatOutput_PythonFile` — Python source stripped of `#` comments
- `TestCompactCatOutput_DataFile` — `cat config.json` returns input unchanged
- `TestCompactCatOutput_PipedToHead` — extracts source from `cat foo.go | head -10`
- `TestCompactCatOutput_NoFilePath` — `cat` with no args → returns input unchanged

**Demo:** `cat internal/tools/compactor.go` (Go file, 500 lines) → output reduced to ~350 lines, Go comments stripped.

---

## Step 7: Bash Detector `find`

**Objective:** Detect `find` commands and group results by directory with a 100-entry cap.

**Implementation:**

- Add to `compactor_bash_detect.go`:
    - `isFindCommand(cmd string) bool` — matches `^find\b` but not `^findfs`
    - `compactFindBashOutput(s string, cfg CompactorConfig) (string, bool)`:
        - Group results by top-level directory
        - Limit to `cfg.MaxSearchTotal` (100) results
        - Summary line: `find: N→K results (grouped, X dirs)`
- Add `compactor.detect_find` config toggle (default `true`)
- New metric technique tag: `"find-group"`

**Test Requirements:**

- `TestIsFindCommand` — `find . -name "*.go"` → true; `findfs` → false
- `TestCompactFindBashOutput_Groups` — 500 results across 20 dirs → grouped, 100 shown
- `TestCompactFindBashOutput_SingleDir` — all results in one dir → no grouping header, just count
- `TestCompactFindBashOutput_EmptyResults` — input is empty → returns `(s, false)`

**Demo:** `find . -name "*.go"` with 1500 results → 100 shown, grouped, summary.

---

## Step 8: Bash Detector `env`

**Objective:** Detect `env` / `printenv` and categorise variables, truncate long values.

**Implementation:**

- Add to `compactor_bash_detect.go`:
    - `isEnvCommand(cmd string) bool` — matches `^env\b`, `^printenv\b`
    - `compactEnvOutput(s string, cfg CompactorConfig) (string, bool)`:
        - Parse `KEY=VALUE` lines (handle `export` prefix, quoted values)
        - Categorise: PATH/LANG/cloud (AWS_, GCP_, AZURE_)/tool (NODE_, GO_, PYTHON_, CARGO_, RUST_)/other
        - Truncate values >50 chars to `preview (N chars)` form
        - Output sections in order: PATH, LANG, cloud, tool, other
- Add `compactor.detect_env` config toggle (default `true`)
- New metric technique tag: `"env-categorize"`

**Test Requirements:**

- `TestIsEnvCommand` — `env` → true; `printenv` → true; `envsubst` → false
- `TestCompactEnvOutput_Categories` — input with PATH, LANG, AWS_REGION, NODE_ENV, HOME → output in correct order
- `TestCompactEnvOutput_LongValueTruncation` — `LONG_VAR=abcdef...{200 chars}` → truncated
- `TestCompactEnvOutput_ExportPrefix` — `export FOO=bar` → `FOO=bar` in output
- `TestCompactEnvOutput_EmptyInput` — empty input → returns `(s, false)`
- `TestCompactEnvOutput_NotKeyValue` — input without `=` → returned as-is (could be warning text)

**Demo:** `env` with 200 vars including 50-char `JAVA_HOME` values → categorised, JAVA_HOME preview shown, output ~50%
smaller.

---

## Step 9: Bash Detector `docker`

**Objective:** Detect `docker ps/images/logs` and apply column selection or tail-dedup.

**Implementation:**

- Add to `compactor_bash_detect.go`:
    - `isDockerCommand(cmd string) bool` — matches `^docker\s+(ps|images|logs|inspect|network|volume)\b`
    - `compactDockerOutput(s, cmd string, cfg CompactorConfig) (string, bool)`:
        - For `docker ps -a`: keep only CONTAINER ID, IMAGE, STATUS, PORTS (drop CREATED, NAMES — wait, NAMES is needed;
          revise: keep ID, IMAGE, STATUS, PORTS, NAMES)
        - For `docker images`: keep REPOSITORY, TAG, SIZE (drop IMAGE ID, CREATED, DIGEST)
        - For `docker logs`: tail-dedup (consecutive identical lines → `×N` count)
        - For `docker inspect`: drop `Env`, `Cmd`, `Labels` long fields; keep top-level structure
- Add `compactor.detect_docker` config toggle (default `true`)
- New metric technique tags: `"docker-ps"`, `"docker-images"`, `"docker-logs"`, `"docker-inspect"`

**Test Requirements:**

- `TestIsDockerCommand` — `docker ps -a` → true; `docker compose up` → true; `dockerless` → false
- `TestCompactDockerOutput_Ps` — 50 containers → ID/IMAGE/STATUS/PORTS/NAMES only
- `TestCompactDockerOutput_Images` — 30 images → REPOSITORY/TAG/SIZE only
- `TestCompactDockerOutput_Logs` — 1000 lines of app log → tail-dedup, identical lines counted
- `TestCompactDockerOutput_UnknownSubcommand` — `docker system df` → returns `(s, false)` (not yet supported)
- `TestCompactDockerOutput_NotDocker` — non-docker tabular input → returns `(s, false)` (verify heuristic doesn't
  false-positive)

**Demo:** `docker ps -a` with 50 containers → output reduced to 5 columns × 50 rows = 250 lines from ~1000-line raw
output.

---

## Step 10: Tee Overflow

**Objective:** When `hardTruncate` drops >50% of bytes, write raw to disk and append a pointer.

**Implementation:**

- Create `internal/tools/compactor_tee.go`:
    - `TeeConfig` struct with `Enabled bool`, `Directory string`, `MaxFiles int`, `MaxBytes int`
    - `teeOverflow(raw, commandSlug, sessionDir string, cfg TeeConfig) (pointerLine string)`:
        - If `!cfg.Enabled`: return ""
        - Sanitise slug: non-alphanum → `_`, truncate to 40 chars
        - Timestamp: `time.Now().Format("20060102-150405")`
        - Path: `<sessionDir>/tee/<slug>-<timestamp>.log`
        - Truncate raw to `cfg.MaxBytes` if needed (avoid runaway disk usage)
        - `os.WriteFile` then `os.Rename` from `.tmp` to final (atomic)
        - Rotate: read dir, sort by mtime, delete oldest if > `cfg.MaxFiles`
        - Return: `"\n[full output: tee/<slug>-<timestamp>.log]"`
- In `hardTruncate` (or a new wrapper): if `len(s) > maxChars*2 && cfg.TeeEnabled`: call
  `teeOverflow(s, commandSlug, sessionDir, cfg.TeeConfig)`, append `pointerLine` to result
- Add `TeeEnabled`, `TeeDirectory`, `TeeMaxFiles`, `TeeMaxBytes` to `CompactorConfig` (defaults: `true`, "", 20, 1MB)
- Plumb `sessionDir` into `BuildCompactorCallback` (new arg, breaking-ish but trivial update in `cli.go` and
  `interactive.go`)

**Test Requirements:**

- `TestTeeOverflow_WritesFile` — raw > `maxChars*2` → file exists, pointer line returned
- `TestTeeOverflow_DisabledNoOp` — `cfg.Enabled=false` → no file, returns ""
- `TestTeeOverflow_BelowThreshold` — raw < `maxChars*2` → no file (use bigger threshold)
- `TestTeeOverflow_RotatesOldFiles` — write 25 files, verify oldest 5 deleted
- `TestTeeOverflow_IOErrorGraceful` — `Directory="/nonexistent/path"` → returns "", logs warning, no panic
- `TestTeeOverflow_SlugSanitisation` — `"git log --all | head -20"` → slug `git_log_all_head_20` (or truncated to 40
  chars)
- `TestTeeOverflow_MaxBytesCap` — raw > `MaxBytes` → file truncated

**Demo:** `git log --all` producing 800KB → compacted to 24KB + pointer to
`~/.pi-go/sessions/<id>/tee/git_log-20260317-120015.log`.

---

## Step 11: TUI Per-Message Compaction Indicator

**Objective:** Render `[compacted 85% · ansi,test-agg]` suffix on TUI tool-output messages. Never sent to LLM.

**Implementation:**

- In `internal/tui/tui.go`:
    - Add `compactInfo string` field to the TUI `message` struct
    - In `Model`, add `lastCompactInfo *atomic.Pointer[string]` (or similar lock-free side-channel)
    - In `View()`, when rendering a tool-output message with non-empty `compactInfo`, append the styled suffix line
- In `BuildCompactorCallback`:
    - Accept a `compactInfoSink func(info string)` arg (writes to the atomic pointer)
    - On success, format `fmt.Sprintf("[compacted %d%% · %s]", savingsPct, strings.Join(techniques, ","))` and call the
      sink
- Update `cli.go` and `interactive.go` to wire the sink to the TUI model's atomic pointer (no-op in non-TUI modes)
- Use a dimmed lipgloss style for the indicator

**Test Requirements:**

- `TestRenderToolMessage_WithCompactInfo` — message with `compactInfo="[compacted 85% · ansi,test-agg]"` → view output
  contains the suffix
- `TestRenderToolMessage_WithoutCompactInfo` — message with empty `compactInfo` → no suffix
- `TestRenderChatMessage_NoSuffixEver` — chat text messages never get a suffix
- `TestCompactInfoSink_NotInLLMResult` — sink is called; the actual `result` map passed to the LLM is unchanged

**Demo:** Run `go test` via bash, see tool output ending with `[compacted 85% · ansi,test-agg]` in TUI; the same output
sent to the LLM has no suffix.

---

## Step 12: Per-Detector Config Toggles

**Objective:** Add `compactor.detect_<name>` toggles for each new detector. Defaults to `true`. Verified by config-merge
tests.

**Implementation:**

- Add fields to `CompactorConfig`:
    - `DetectLs bool`, `DetectCat bool`, `DetectFind bool`, `DetectEnv bool`, `DetectDocker bool`
- Update `DefaultCompactorConfig()` to set all to `true`
- In each detector's invocation in `compactBash`: `if cfg.DetectX { ... }`
- Add `TestConfigMergesDefaults` (already in `compactor_test.go`); add new test cases for each new field

**Test Requirements:**

- `TestConfigDetectors_DefaultTrue` — empty JSON `{}` → all 5 detectors enabled
- `TestConfigDetectors_IndividualDisable` — `{"detect_docker": false}` → only `DetectDocker` false, others true
- `TestCompactBash_RespectsDetectorToggle` — `DetectCat=false` → `cat` output not filtered; `DetectCat=true` (default) →
  filtered
- `TestCompactBash_FallsThroughWhenDisabled` — when all detectors disabled, only ANSI strip + smart truncate + hard
  truncate run

**Demo:** User sets `compactor.detect_docker = false` in `config.json`; `docker ps` output goes through ANSI strip +
hard truncate but not the docker-specific column selection.

---

## Step 13: Integration Tests + Benchmarks + Final Signoff

**Objective:** Verify the full Phase 2 pipeline works end-to-end and meets performance budget. Run linters, full test
suite, signoff commit.

**Implementation:**

- Add `internal/tools/compactor_bench_test.go`:
    - `BenchmarkCompactLsOutput` — 10KB `ls` output
    - `BenchmarkCompactCatOutput_Go` — 50KB Go file
    - `BenchmarkFilterSourceCodeLang` — 100KB Go file
    - `BenchmarkTeeOverflow` — write 1MB
    - `BenchmarkIsWorthReplacing` — 100KB strings
- Add `internal/tools/compactor_integration_test.go`:
    - End-to-end bash pipeline with all 5 new detectors
    - Tee rotation scenario
    - Read pipeline with offset/limit pass-through
    - TUI indicator presence/absence
- Run: `go build ./...`, `go vet ./...`, `golangci-lint run`, `go test ./... -count=1`, `go test ./... -race`,
  `go test -tags e2e ./...`
- All gates must pass; if any fail, fix root cause (no `//nolint`, no skipping tests)
- Signoff commit referencing this spec

**Test Requirements:**

- All Step 1–12 tests pass
- All existing `compactor_test.go` tests still pass
- New benchmarks meet the <2ms / <5ms / <10ms / <20ms targets
- Race detector clean
- Coverage of new code ≥85% (matches the repo's 80%+ bar)

**Demo:** Full session: read 1500-line Python file (compacted, language-aware), run `ls -la` (grouped), `cat` a Go
file (source-filtered), `find` returning 1500 results (grouped, capped), `env` with 200 vars (categorised),
`docker ps` (columns trimmed), `git log --all` (compacted + tee'd). All metrics recorded, TUI shows per-message
indicator, `/rtk` stats reflect all of the above with new techniques.

---

## Sequencing Rationale

- **Step 1 first** — `never_worse` is a single-file change with broad impact; safer to land and de-risk before adding
  new filters.
- **Steps 2–3 together** — language enum and filter are inseparable; land as one PR.
- **Step 4** — read pipeline change is small and isolated.
- **Steps 5–9** — bash detectors are independent of each other; can be split into separate PRs or combined. Combined is
  faster for end-to-end testing.
- **Step 10** — tee overflow is independent but depends on `compactor.go` plumbing changes (sessionDir arg); land after
  the detectors to keep the diff small per PR.
- **Step 11** — TUI indicator depends on the callback returning compactInfo; needs Step 1 (which sets the compact
  result) at minimum.
- **Step 12** — config toggles naturally land with their respective detectors; rolled into the same PR.
- **Step 13** — final integration and signoff.

**Recommended PR split:**

1. PR 1: Steps 1, 2, 3, 4 (guard + language + read pipeline)
2. PR 2: Steps 5, 6, 7, 8, 9 (bash detectors)
3. PR 3: Step 10 (tee)
4. PR 4: Steps 11, 12, 13 (TUI + config + signoff)
