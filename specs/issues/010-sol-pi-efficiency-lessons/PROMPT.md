# Agent Prompt — SoL-Pi Efficiency Lessons (T1–T5)

You are implementing `specs/issues/010-sol-pi-efficiency-lessons`. Read this
file completely before touching code, then read `research.md`, `design.md`, and
`plan.md` in that directory.

## Objective

Adopt the two transferable architectural commitments identified in the SoL-Pi
harness study, plus three prerequisite fixes that make them verifiable:

1. **Separate durable history from the model-visible projection.** pi-go
   currently rewrites `events.jsonl` in place (`internal/session/store.go:1291`)
   and discards oversized tool output at 256 KiB
   (`internal/tools/truncate.go:6,44`) with no readback path.
2. **Make reduction rejectable rather than a mutation already applied.**
   `internal/tools/compactor.go:119` `applyCompaction` mutates in place with no
   retained original.

## Read this before you start

**`research.md` §R2 is not optional reading and it changed after the plan was
written.** The plan originally described ticket T2 as three line-level fixes.
Empirical verification showed **seven of nine registered compaction pipelines are
no-ops in production**: they read `result["output"]`, a key that no tool's output
struct produces. If you implement from this prompt alone, or from a summary of
the plan, you will implement the wrong thing. Read `research.md` §R2 in full,
including the §R2.4 probe output.

Two further findings that change the implementation:

- **R1.3** — `internal/tools/bash_supervisor.go:440-443` applies `redactSecrets`
  **after** `truncateOutput`. Archiving before truncation would persist
  **unredacted** bytes. Design decision D2 (archive the redacted string) exists
  solely for this.
- **R1.4** — `read` addresses by **line**, not byte (`internal/tools/read.go:141`).
  Readback pointers must be line-based.

## Hard constraints

- **Do not change runtime behaviour for bash and read compaction without a
  test.** Those two pipelines work today (verified: `before=11202 after=92`).
  They are the regression baseline.
- **Fail open everywhere.** Any archive failure, sandbox-registration failure, or
  reduction failure must leave the original tool result intact and log through
  the session logger. Never a raw `fmt.Print*`/`log.Print*` on the TUI path —
  see the repo `AGENTS.md` section on TUI output safety.
- **Never write unredacted bytes to disk** (D2). If you archive anything, archive
  the post-`redactSecrets` string.
- **Do not widen the sandbox beyond one session's artifact directory.** Scope
  `AddExtraDir` to `<sessionId>/tool-output/`, never to `sessionsDir()`.
- **Do not commit with `--no-verify`.** Commits must be signed and carry a
  `Signed-off-by` trailer (`git commit -s -S`). Verify with `git verify-commit`,
  never by grepping for a `gpgsig` header.
- **Work in a git worktree**, per the repo `AGENTS.md`.

## Tickets

Implement in this order. T1 and T2 are independent; T3 needs both; T4 needs
T1+T2; T5 is independent.

### T1 — Artifact archive + readback pointer

**Decision prerequisites: D1–D5 in `design.md`.** Confirm the chosen options
before implementing. The plan assumes the recommended option for each.

New file `internal/tools/artifact.go`:

- `ArtifactRoot(sessionDir string) string` — creates and returns the per-session
  artifact dir at mode `0700`.
- `Archive(root, toolName, content string) (string, error)` — writes to
  `<root>/tool-output/<id>.txt`, `id = sha256(toolName \0 content)[:16]`, with
  `O_CREATE|O_EXCL|O_WRONLY` at `0600`. On `EEXIST`, read and byte-compare; a
  mismatch is an integrity error, not a cache hit. Returns `""` (no error) when
  content is below the existing 256 KiB gate.

Wire at the **call sites**, not inside `truncateOutput` — that function is a pure
string helper with 8 call sites and existing unit tests
(`internal/tools/tools_test.go:658-665`, `internal/tools/truncate_utf8_test.go`).

- `internal/tools/bash_supervisor.go:440-443` — **the D2-critical one**: redact
  first, archive the redacted string, then truncate that same redacted string.
  Note the streams are already bounded to one `maxOutputBytes` and `droppedNote`
  reports bytes that aged out; the artifact holds at most one buffer's worth.
  Document that in the pointer text rather than implying completeness.
- `internal/tools/read.go:253` — same ordering.
- Remaining call sites (`subagent.go:318,466,591`, `tree.go:87`,
  `git_diff.go:87`) — wire uniformly per D4-B, or stop after bash+read per D4-A.

Pointer text must be **line-based** (R1.4) and include total lines/bytes so the
model can navigate without a probe call:

```text
… (output truncated at 262144 bytes; file is 18422 lines / 913204 bytes)
full output: read("<abs path>", offset=1, limit=2000) — continue with offset=N
```

Sandbox access (D3): register the artifact dir via `AddExtraDir`
(`internal/tools/sandbox.go:101-119`). It calls `os.MkdirAll` then `os.OpenRoot`,
so call `ArtifactRoot` first. The session id may not exist at sandbox
construction time — reuse the late-bound capture pattern already in the codebase
at `internal/cli/cli.go:808-812` (`var memSessionID string`) rather than
inventing one. On registration failure, fall back to plain truncated output.

**Required tests** (success criterion 1):

- Under the gate → no artifact, output unchanged.
- Over the gate → artifact exists, file mode `0600`, dir mode `0700`.
- **Round trip:** `read(pointerPath, offset=N)` returns content past the
  truncation point. Integration test, not inspection.
- Idempotency: identical bytes archived twice → one file, byte-identical.
- Integrity: pre-create the target with different bytes → error, no overwrite.
- **Security (most important):** seed a payload containing `sk-…`, a JWT and an
  `AKIA…` key; assert the artifact on disk contains `***` and not the secret.
- Fail open: unwritable artifact dir → plain truncated output, no error escapes.

### T2 — Fix the seven dead compaction pipelines

**Read `research.md` §R2 in full first.**

**T2.0 — Establish real result shapes.** ADK marshals each tool's typed output
struct to JSON and unmarshals into `map[string]any`
(`adk/v2@v2.4.0/tool/functiontool/function.go:231`). Verified keys:

```go
bash          → stdout, stderr, exit_code
read          → content, total_lines
grep/ripgrep  → matches, total_matches
find          → files, total_files
tree          → tree, dirs, files
git-file-diff → file, diff, lines_added, lines_removed
git-overview  → branch, recent_commits, staged_files, ...
git-hunk      → file, hunks, total_hunks
```

**T2.1 — Align each pipeline to its real field:**

- `compactor_search.go:9-13` `compactGrep` — read `matches` (`[]GrepMatch`,
  `grep.go:122-126`); compact; re-render to a string; preserve `total_matches`
  and `truncated`.
- `compactor_search.go:43-44` `compactFind` — read `files` (`[]string`,
  `find.go:25-32`). Note `compactTree` (`:74-76`) delegates to `compactFind`, so
  tree breaks with find; tree actually carries `tree`/`dirs`/`files`.
- `compactor_git.go:45` `compactGitOverview` — read `branch` / `recent_commits` /
  `staged_files` / `unstaged_files` / `untracked_files` (`git_overview.go:32-42`).
- `compactor_git.go:11,79` — `diff` is correct for `git-file-diff`, but
  `git-hunk` has **no `diff` field** (`git_hunk.go:32-39`), so `compactGitHunk`
  must be rewritten against `hunks`.

**T2.2 — Fix name routing:**

- `compactor.go:108-113` — hyphens: `git-file-diff`, `git-overview`, `git-hunk`
  (match `git_diff.go:36`, `git_overview.go:52`, `git_hunk.go:42`).
- `compactor.go:102` — accept both `grep` and `ripgrep` (`grep.go:129-132`).

**T2.3 — `applyCompaction` must write what was read.**
`compactor.go:119-156` probes `stdout` → `content` → `output` → `diff` →
`result` → `data` in first-match order, so a correct pipeline can still write to
the wrong key or none. Route the write by the same key the pipeline read —
simplest correct shape is carrying the target key on `CompactResult`.

**T2.4 — The guard test.** The existing tests are *why this shipped*:
`compactor_test.go` feeds synthetic maps with an `output` key
(`:702,709,784,999,1019,1028,1061,1076`) — a shape no tool produces. Build test
results by `json.Marshal`/`Unmarshal` of the **real output structs**; assert all
nine names compact on a payload large enough to trigger; assert routing exists
for every registered tool name, sourced from the registry.

### T3 — Make reduction rejectable

`internal/tools/compactor.go`: `applyCompaction` (line 119) returns `bool`;
`BuildCompactorCallback` (line 79) records metrics only when applied. Per D6,
do not refactor pipeline signatures.

Tests: `applyCompaction` returns `false` on a map with no known field; metrics
not recorded for a non-applied compaction; existing behaviour unchanged for all
known fields.

### T4 — Wire the dead measurement surfaces

- **T4.1** — `internal/tools/compactor_metrics.go:112` `Save()` has no caller.
  `internal/cli/cli.go:806` constructs `NewCompactMetrics()` inline and discards
  it; `internal/cli/interactive.go:568-569` likewise. Retain the reference
  (same capture shape as `cli.go:808-812`) and call `Save(sessionDir)` at session
  end.
- **T4.2** — `internal/tools/dedup.go:71,81` `Stats()`/`FormatStats()` are wired
  into the callback but `internal/tui/types.go` has no deduper field, so they are
  unreachable. Add the field and render alongside the existing `/rtk` path
  (`internal/tui/commands.go:1101-1125`).

Tests: after a session that compacted, `compactor-metrics.json` exists and parses
(success criterion 3); the TUI renderer includes dedup counts given a populated
deduper.

### T5 — Ollama cache reporting

**Unresolved external dependency — investigate before coding.**
`internal/provider/ollama.go:691-706` sets only `PromptTokenCount` and
`CandidatesTokenCount`. Determine what the Ollama wire response actually carries
for cached prompt tokens — confirm against a recorded or live response, do not
assume a field name, and record the evidence in `SUMMARY.md`. If Ollama reports
nothing, the legitimate outcome is the log line below plus a documented negative
result.

- Populate `CachedContentTokenCount` accordingly.
- Add a **once-per-provider-route** log when usage arrives with no cache field
  (one line per route per session, not per request).
- Separately: `internal/provider/anthropic.go:552-559` folds
  `cacheCreationTokens` into `PromptTokenCount`; surface it as its own field.

Note: the compaction *threshold* does not depend on this — `cachePrefixTokens` is
set from the first request's `inputTokens` (`guardrail.go:140-144`), independent
of `cachedTokens`. Only a future profitability gate does.

Tests: Ollama fixture with a cached count → field populated; Anthropic fixture →
cache-creation surfaced separately with `PromptTokenCount` total unchanged;
`TestTrackerAndMeterAgreeOnBodyTokens` still passes.

## Out of scope — do not implement

- Compaction **economics / breakeven gate** — blocked on T5.
- **Semantic / plan-phase** compaction triggers — needs T3 + T5.
- **Tool-call batching** — owned by `specs/issues/token-cost/01-batching.md`.
- **Capping `read` on source files / wiring `FileContentCache`** — owned by
  `specs/issues/token-cost/04-bugs.md` §4b/4c.
- **Action Fusion** — rejected on the merits; see `research.md` §R7.1.
- **`pi tokens`, OTel metrics, `CostUSD`** — owned by
  `specs/issues/token-cost/07-measurement.md`.

## Verification requirements

Per ticket, report with **pasted output**:

```bash
go build ./...
go test ./internal/tools/... ./internal/session/...
# T5 additionally: ./internal/provider/... ./internal/cli/...
make lint
make vet
```

For T1 additionally: the round-trip test proving content past the truncation
point is recoverable, and the secret-redaction test output.

Do **not** claim token savings. `token-cost/TOKENS.md`'s 201:1 ratio is not
observable as a CI number until T4 lands and `pi tokens` exists. Any savings
claim in a summary must be marked unverified.

## Completion report

Report: files changed (`git diff --name-only`), each ticket's status, every
verification command with its result, any decision you changed from the plan and
why, and any blocker. Do not describe work as complete if a required check is
missing.
