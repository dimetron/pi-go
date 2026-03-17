# Existing Output Truncation & Compaction in pi-go

## Current Infrastructure

Two-tier truncation: byte-level (100KB) and count-level (200-500 items).

### Global Limits (`truncate.go`)
- `maxOutputBytes = 100KB` — hard limit for all tool output
- `maxLineLength = 500` chars per line (grep only)

### Per-Tool Summary

| Tool | Limit | Mechanism | Truncation Flag | Secret Redaction |
|------|-------|-----------|-----------------|------------------|
| **bash** | 100KB | `truncateOutput()` | implicit | ✅ `redactSecrets()` |
| **read** | 2000 lines + 100KB | line count + bytes | `Truncated` bool | ❌ |
| **grep** | 200 matches, 500 chars/line | count + line length | `Truncated` bool | ❌ |
| **find** | 500 results | count-based | `Truncated` bool | ❌ |
| **tree** | 500 entries, depth 10 | entry count + depth | "(truncated)" text | ❌ |
| **git-diff** | 100KB | byte-based | `Truncated` bool | ❌ |
| **git-overview** | 10 commits only | **No file count limit** | No flag | ❌ |

### What's Already Good
- Structured JSON outputs with metadata (total counts, truncation flags)
- Directory traversal filtering (hidden dirs, node_modules, vendor, etc.)
- Separate stdout/stderr in bash output

## Gaps & Opportunities for RTK Optimizer

### High Value
1. **No semantic compaction** — Bash output (build logs, test output, linter output) returned raw up to 100KB. The RTK optimizer's multi-stage pipeline (build filter → test aggregate → git compact → linter aggregate) would massively reduce tokens.
2. **Git overview unbounded** — No limits on file lists (thousands of untracked files possible)
3. **No ANSI stripping** — ANSI escape codes waste tokens in all tool outputs

### Medium Value
4. **Grep: no deduplication** — Returns first 200 raw matches, many may be similar
5. **Read: no source filtering** — Returns full content up to 2000 lines, no comment stripping or signature-only mode
6. **Git diff: no summarization** — Raw diff up to 100KB, no hunk summary option

### Consistency Issues
7. **Secret redaction only in bash** — read, grep, git tools don't redact
8. **Line truncation only in grep** — bash stdout can have unlimited line length
9. **Tree truncation opaque** — No count of excluded entries when truncated

## Compaction Stages That Would Add Value

Based on gaps, these RTK-optimizer stages would help most:

1. **ANSI stripping** — trivial, universal benefit
2. **Test output aggregation** — `go test`, `pytest`, `jest` output → pass/fail/skip summary + failures
3. **Build output filtering** — `go build`, `cargo build`, `make` → errors/warnings only
4. **Git output compaction** — diff summarization, status limiting
5. **Linter aggregation** — `golangci-lint`, `eslint` → grouped by rule
6. **Smart truncation** — priority-based line selection for long outputs
