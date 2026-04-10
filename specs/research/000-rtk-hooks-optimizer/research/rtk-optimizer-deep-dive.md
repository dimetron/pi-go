# pi-rtk-optimizer Deep Dive

## Architecture Overview

The extension intercepts two hook points:
1. **`tool_call`** (before execution) — rewrites bash commands
2. **`tool_result`** (after execution) — compacts tool output

Also hooks `session_start`, `session_switch`, `before_agent_start` for init and system prompt injection.

---

## 1. Configuration

Flat JSON at `~/.pi/agent/extensions/pi-rtk-optimizer/config.json`:

```json
{
  "enabled": true,
  "mode": "rewrite|suggest",
  "rewriteGitGithub": true,
  "rewriteFilesystem": true,
  "rewriteRust": true,
  "rewriteJavaScript": true,
  "rewritePython": true,
  "rewriteGo": true,
  "rewriteContainers": true,
  "rewriteNetwork": true,
  "rewritePackageManagers": true,
  "outputCompaction": {
    "enabled": true,
    "stripAnsi": true,
    "truncate": { "enabled": true, "maxChars": 12000 },
    "sourceCodeFiltering": "none|minimal|aggressive",
    "smartTruncate": { "enabled": true, "maxLines": 220 },
    "aggregateTestOutput": true,
    "filterBuildOutput": true,
    "compactGitOutput": true,
    "aggregateLinterOutput": true,
    "groupSearchOutput": true,
    "trackSavings": true
  }
}
```

Atomic write (write `.tmp`, rename). Normalized on load with defaults.

---

## 2. Command Rewriter

### Algorithm
1. Reject: empty, heredocs (`<<`), already starts with `rtk`
2. Check whole-command bypass for compound commands
3. **Tokenize** into segments split by shell operators (`&&`, `||`, `|`, `|&`, `;`, `&`)
4. For each segment, match against ordered `RTK_REWRITE_RULES`
5. Strip env var prefixes before matching
6. Check per-rule bypass conditions
7. Apply first matching rule's regex replacement

### Tokenizer
- Quote-aware (`'`, `"`, backtick)
- Escape-aware (`\`)
- Sed-aware (tracks if current word is sed script to avoid splitting on `|` or `;`)

### Rewrite Rules (~65+ rules, 9 categories)

| Category | Examples |
|----------|---------|
| **gitGithub** | `git <args>` → `rtk git <args>`, `gh <args>` → `rtk gh <args>` |
| **filesystem** | `cat` → `rtk read`, `head -N file` → `rtk read file --max-lines N`, `grep/rg` → `rtk grep`, `ls` → `rtk ls`, `tree` → `rtk tree`, `find` → `rtk find`, `diff` → `rtk diff`, `wc` → `rtk wc` |
| **rust** | `cargo <args>` → `rtk cargo <args>` |
| **javascript** | `vitest` → `rtk vitest run`, `tsc` → `rtk tsc`, `eslint` → `rtk lint` |
| **python** | `pytest` → `rtk pytest`, `ruff` → `rtk ruff`, `pip` → `rtk pip` |
| **go** | `go <args>` → `rtk go <args>`, `golangci-lint` → `rtk golangci-lint` |
| **containers** | `docker compose X` → `rtk docker compose X`, `kubectl X` → `rtk kubectl X` |
| **network** | `curl` → `rtk curl`, `wget` → `rtk wget` |
| **packageManagers** | `npm X` → `rtk proxy npm X`, `pnpm X` → `rtk pnpm X` |

Two strategies: **Direct** (`rtk <tool>`) and **Proxy** (`rtk proxy <tool>`).

### Bypass Rules

**Whole-command bypass** (compound commands only):
- Any segment starts with `find`, `grep`, `rg`, `ls` → bypass (unsafe for pipes)
- Any segment is native shell proxy (`bash -c`, `powershell -c`)

**Per-rule bypass**:
- `gh` with `--json`, `--jq`, `--template` flags
- `cargo help/install/publish`
- `find` with actions: `-delete`, `-exec`, `-execdir`, `-print0`, etc.
- `ls` with any flags
- Container commands launching interactive shells
- `bash -c` inline commands

---

## 3. Output Compaction Pipeline

### Routing by tool name
- `bash` → `compactBashText()`
- `read` → `compactReadText()`
- `grep` → `compactGrepText()`

### Bash Compaction (ordered stages)

1. **Strip ANSI** escape codes
2. **Filter build output** — detect build commands, extract errors/warnings, summarize
3. **Aggregate test output** — detect test commands, extract pass/fail/skip + failure details
4. **Compact git output** — detect git diff/status/log, produce condensed summaries
5. **Aggregate linter output** — detect linter commands, group by rule and file
6. **Hard truncate** — cap at maxChars (default 12000)

### Read Compaction

1. Preserve exact output for: range reads, skill files, files < 80 lines
2. Strip ANSI
3. **Source code filtering** (if truncation would trigger):
   - `minimal`: strip comments/docstrings
   - `aggressive`: strip function bodies, keep signatures
4. **Smart truncate** — priority-based line selection (imports, signatures, constants) up to maxLines (220)
5. Hard truncate
6. Prepend `[RTK compacted output: techniques...]` banner

### Grep Compaction

1. Strip ANSI
2. Group results by file with match counts
3. Hard truncate

---

## 4. Compaction Techniques Detail

### ANSI Stripping
Three regex patterns: CSI sequences, OSC sequences. Fast path: skip if no `\x1b`.

### Build Output (`techniques/build.ts`)
- Matches: cargo build/check, tsc, make, go build, etc.
- Skips progress lines (Compiling, Downloading)
- Extracts error blocks (`error:`, `[ERROR]`, `FAIL`)
- Limits: 5 errors, 10 lines each

### Test Output (`techniques/test-output.ts`)
- Matches: npm test, cargo test, go test, pytest, vitest, jest
- Extracts stats via regex per runner
- Output: `PASS: N, FAIL: N, SKIP: N` + up to 5 failures, 4 lines each

### Git Output (`techniques/git.ts`)
- **diff**: Max 50 lines, 10 lines/hunk, file summaries with `+N -N`
- **status**: Categorize files (staged/modified/untracked/conflicts), 5 per category
- **log**: Max 20 entries, 80 chars/line

### Linter Output (`techniques/linter.ts`)
- Matches: eslint, ruff, pylint, clippy, golangci-lint
- Parses `file:line:col: message` format
- Groups by rule (top 10) and file (top 10, top 3 rules each)

### Search Output (`techniques/search.ts`)
- Parses `file:line:content` format
- Groups by file, 10 matches/file, 50 total
- Paths compacted to 50 chars

### Source Code Filtering (`techniques/source.ts`)
- Language detection by extension
- **minimal**: Remove comments and docstrings
- **aggressive**: Remove function bodies, keep signatures/imports/constants
- **smart truncate**: Priority line selection

---

## 5. Metrics Tracking

In-memory array of per-compaction records: tool, techniques, original/compacted char counts, savings %. Summary grouped by tool name. Queryable via `/rtk stats`.

---

## 6. Key Design Decisions for Go Port

1. **Rule ordering matters** — first match wins, order is critical
2. **Tokenizer is most complex** — quotes, escapes, sed detection, shell operators
3. **Bypass logic is safety-critical** — prevents breaking interactive/compound commands
4. **Compaction is modular** — each technique is independent, can be separate Go functions
5. **Config is flat JSON** with atomic write and normalization
6. **Stateful** — tracks warning dedup (bounded sets), metrics, config cache

### For Go: No External `rtk` Binary

Since we're implementing in Go natively (no `rtk` binary):
- Command rewriting becomes **command optimization** — rewriting bash commands to use pi-go's built-in tools instead
- E.g., `cat file` → use read tool, `grep pattern` → use grep tool
- Or: rewrite to more efficient command forms
- The runtime guard / rtk availability check becomes unnecessary
- Focus shifts to **output compaction** as the primary value
