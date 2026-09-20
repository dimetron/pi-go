# Research — Verified Gap Analysis

Every anchor below was read directly from the tree during authoring. Claims that
are inferred rather than read are marked `(inferred)`. Two findings here
**correct** an earlier verbal analysis of mine; both are load-bearing, and are
flagged as **[CORRECTION]**.

---

## R1. pi-go has no durable/replayable split

### R1.1 Reduction is destructive, not projective

`rewriteEvents` rewrites `events.jsonl` in place:

- `internal/session/store.go:1291-1320` — writes to `events.jsonl.tmp`, then
  `os.Rename(tmpFile, eventsFile)`. The original is gone on success.
- Called from `internal/session/autocompact.go` after both shed and summarize.
- `AutoCompactOutcome.TokensAfter` is computed from the rewritten slice
  (`autocompact.go:132`), then persisted.

Both stages are irreversible:

- **Shed** replaces the bulk field with a stub — `internal/session/compaction_shed.go:286-306`
  `shedResponsePayload`: `"[superseded — a later %s call re-read this target; %d bytes dropped to reclaim context. Use the newer result below, or call %s again if you need this content.]"`
- **Summarize** drops all tool traffic — `compaction_shed.go:316` `BuildSummarizedEvents`.

The shed stub instructs the model to "call again if you need this content",
i.e. **re-execute a command** to recover bytes pi-go already had.

### R1.2 There is no artifact spill path anywhere

```
rg 'Full output|fullOutput|full_output|spill' internal/ --type go
→ only internal/tui/terminal_compat.go, internal/tui/minimap_test.go,
  internal/tui/agent_loop.go, internal/mermaid/renderer/canvas.go  (all unrelated)
```

No tool writes oversized output to disk. `internal/procs/` manages process
lifecycle and pipes only; it has no log file.

Therefore `maxOutputBytes` discards silently:

- `internal/tools/truncate.go:6` — `maxOutputBytes = 256 * 1024`
- `internal/tools/truncate.go:44-49` — `truncateOutput` cuts and appends
  `"\n... (output truncated)"`. No pointer, no path, no offset.
- Verified callers: `internal/tools/bash_supervisor.go:441-442`,
  `internal/tools/read.go:253`, `internal/tools/subagent.go:318,466,591`,
  `internal/tools/tree.go:87`, `internal/tools/git_diff.go:87`.

### R1.3 Secret-redaction ordering is a hazard for the archive design — **[CORRECTION]**

`internal/tools/bash_supervisor.go:440-443`:

```go
func budgetStreams(stdout, stderr string) (string, string) {
	return redactSecrets(truncateOutput(stdout)),
		redactSecrets(truncateOutput(stripRuntimeNoise(stderr)))
}
```

`redactSecrets` is applied **after** `truncateOutput`. So the *input* to
truncation — the thing an archive-before-truncate design would persist — is
**unredacted**. `internal/tools/redact.go:8-23` defines eight patterns
(API keys, `sk-`, `ghp_`, `gho_`, JWTs, `AKIA…`, bearer tokens).

**Consequence for ticket 1:** the archive must be written from *redacted* bytes,
i.e. `archive(redact(s))` then truncate, not `archive(s)`. Also note the buffers
are already bounded at one `maxOutputBytes` and `droppedNote` reports bytes that
aged out of the ring (`bash_supervisor.go:445-450`), so for bash the archive
captures at most one buffer's worth — not the full command output. That is a
real limitation to state rather than paper over.

### R1.4 `read` addresses by line, not byte — **[CORRECTION]**

- `internal/tools/read.go:141` — `offset := max(input.Offset, 1)`, compared
  against `totalLines` from `countFileLines`.
- `internal/tools/read.go:130-133` — "Valid offsets are 1-%d" in the past-the-end note.

So a readback pointer must be **line-based** (`offset=N`), not byte-based.
SoL-Pi's `obs_recall` is byte-based because it owns its own tool; the
artifact design must fit `read`'s existing semantics.

### R1.5 The sandbox blocks session-dir paths by default

- `internal/tools/sandbox.go:59-72` — `Sandbox` holds `root *os.Root` plus
  `extraRoots`/`extraDirs`.
- `internal/tools/sandbox.go:101-119` — `AddExtraDir` creates the dir `0700`,
  opens a separate `os.Root`.
- `internal/tools/sandbox.go:121-137` — `matchExtraRoot` resolves an absolute
  path under an extra dir.

An artifact under `~/.pi-go/sessions/<id>/…` is **not** readable by `read` unless
its root is registered via `AddExtraDir`. Registration points exist but must be
chosen deliberately (see `design.md` §D3). Alternative: the readback pointer
names a `bash` command (`cat`/`sed`) rather than `read` — but that escapes
redaction and adds a shell round trip.

---

## R2. The compactor has two dead dispatch classes — [CORRECTION]

An earlier verbal summary of mine said fixing the `ripgrep` name mismatch was
sufficient for grep. **It is not.** There are two independent defects.

### R2.1 Three git stages are unreachable (underscore vs hyphen)

`internal/tools/compactor.go:108-113` routes:

```go
case "git_file_diff": return compactGitFileDiff(result, cfg)
case "git_overview":  return compactGitOverview(result, cfg)
case "git_hunk":      return compactGitHunk(result, cfg)
```

Tools register hyphens:

- `internal/tools/git_overview.go:52` — `newTool("git-overview", …)`
- `internal/tools/git_diff.go:36` — `newTool("git-file-diff", …)`
- `internal/tools/git_hunk.go:42` — `newTool("git-hunk", …)`

No case ever matches. Already recorded in `specs/issues/token-cost/04-bugs.md` §4a.

### R2.2 Grep fails twice over — name *and* field

**Name:** `internal/tools/grep.go:129-133`:

```go
grepToolName := "grep"
if rgAvailable {
	grepToolName = "ripgrep"
}
```

but `internal/tools/compactor.go:102` handles only `case "grep"`. On any host
with `rg` — effectively all of them — grep output is never compacted.

**Field:** even with the name fixed, `compactGrep` reads a field that does not
exist. `internal/tools/compactor_search.go:9-13`:

```go
func compactGrep(result map[string]any, cfg CompactorConfig) *CompactResult {
	output, _ := result["output"].(string)
	if output == "" {
		return nil
	}
```

`GrepOutput` (`internal/tools/grep.go:112-119`) has fields `matches`,
`total_matches`, `truncated` — **no `output`**. So `compactGrep` returns `nil`
unconditionally, and `applyCompaction`'s `output` branch never fires for grep
either (`compactor.go:130-134`).

`ResultDeduper` has both names correct (`internal/tools/dedup.go:34-44`
includes `"ripgrep"` and `"grep"`), which is why the gap went unnoticed — grep
*dedupes* but never *compacts*.

**Consequence for ticket 2:** the fix is (a) accept both names, (b) teach
`compactGrep` to read `matches`, and (c) add a test that asserts the compactor
routes on the same names the tools register with, so the class cannot recur.

---

## R3. Reduction cannot be rejected

`internal/tools/compactor.go` — `BuildCompactorCallback`:

```go
compacted := compactToolResult(t.Name(), args, result, cfg)
if compacted != nil {
	metrics.Record(compacted.Techniques, compacted.OrigSize, compacted.CompSize, t.Name())
	applyCompaction(result, compacted)
}
```

- `internal/tools/compactor.go:119` `applyCompaction` mutates `result` in place,
  first-match-wins across `stdout`, `content`, `output`, `diff`, `result`,
  `data` (`compactor.go:118-155`).
- Once applied there is no rejection point and no retained original. Contrast
  SoL-Pi: `reduceToolResult` returns `undefined` on every failure path
  (`src/sol-pi/extensions/evidence-preserving-reducer/index.ts:115-148`) and the
  caller leaves the original result untouched.
- The callback runs as an `AfterToolCallback`; compaction failure is not
  currently capable of "failing open" because the mutation is the success path.

---

## R4. Everything that would measure this is dead

### R4.1 `CompactMetrics.Save()` has no caller

`internal/tools/compactor_metrics.go:112-150` implements `Save()` writing
`<sessionDir>/compactor-metrics.json`. `rg '\.Save\(\)'` over the compaction
paths finds no caller. `internal/cli/cli.go` constructs `tools.NewCompactMetrics()`
inline in the callback wiring and discards the reference, so per-tool
compression ratios are computed and thrown away each session.

`CompactRecord`/`CompactSummary.SavingsPct` (`compactor_metrics.go:32,78-80`)
and `FormatStats()` (`:85-109`) exist; `FormatStats` is reachable only via the
TUI `/rtk` command (`internal/tui/commands.go:1101-1125`).

### R4.2 The deduper's stats never reach the UI

- `internal/tools/dedup.go:71` `Stats()`, `:81-88` `FormatStats()` ("~N tokens
  of immediate context", bytes/4).
- Wired in `internal/cli/cli.go` and `internal/cli/interactive.go` as
  `tools.BuildDedupCallback(resultDeduper)`, but
  `internal/tui/types.go` has no deduper field — so the stats are unreachable
  from the TUI.

### R4.3 Cost is declared and never computed

- `internal/eval/metrics.go:670` `TokenMetrics.CostUSD` — rendered at
  `internal/eval/report.go:112-113`. **Nothing assigns it.**
- `internal/atif/types.go:70` `Metrics.CostUSD` — `internal/atif/convert.go`
  never sets `Step.Metrics` or `Trajectory.FinalMetrics`.
- No `tokens × rate` multiplication exists in the tree.
- `internal/provider/pricing.go:41-66` defines `PricingModel{Input, Output,
  CacheRead, CacheWrite, Tiers}`; `CostFor` (`:127`) is called only from
  `internal/cli/model.go:312-341` for the `pi model` price table. `CacheRead` /
  `CacheWrite` are parsed and never consumed.

Not in scope here (owned by `token-cost/07-measurement.md`), but recorded because
it bounds how much any item in this spec can be *proven* to help.

### R4.4 No OTel metrics, only spans

`internal/otel/otel.go` sets up traces only (`sdktrace`), env-gated by
`OTEL_TRACES_EXPORTER`. `internal/extension/hooks.go:345-376` stamps
`gen_ai.usage.*` span attributes per LLM call, including
`cached_input_tokens`. No `MeterProvider`, no instruments in first-party code.
No `tools_per_turn` / `turn_index` (`token-cost/07-measurement.md` recommendation 2).

---

## R5. Cache reporting is broken on the Ollama path

- `internal/provider/ollama.go:691-706` `finalResponse` sets **only**
  `PromptTokenCount` and `CandidatesTokenCount`. `CachedContentTokenCount` is
  never set.
- `internal/provider/anthropic.go:552-559` sets `PromptTokenCount = input +
  cacheRead + cacheCreation` and `CachedContentTokenCount = cacheRead` — so
  **cache-write tokens are folded into the prompt count and discarded**.
- `internal/guardrail/guardrail.go:27-31` documents the limit: "Cache *write*
  tokens are not broken out separately: the genai usage metadata has no field
  for them."
- Consumer: `guardrail.go:363-371` `BodyTokens() = lastPromptTokens -
  cachePrefixTokens`, where `cachePrefixTokens` is set from the **first
  request's `inputTokens`** (`guardrail.go:140-144`) — independent of
  `cachedTokens`. So the compaction *threshold* is unaffected by the cache gap;
  what is affected is any future *profitability gate*.
- Impact (from `token-cost/TOKENS.md`): 73% of prompt tokens run on routes
  reporting zero cache.

### R5.1 The economics gate does not exist yet

- `internal/session/compaction.go:123-137` `Decide` compares `bodyTokens/windowSize`
  to `ShedPercent` (60) / `SummarizePercent` (90) and returns a `CompactionKind`.
  Nothing else. `rg 'Cost|Price|breakeven'` over `internal/autocompact/*.go` and
  `internal/session/*.go` finds only comments.
- The correct inequality is already derived in prose at
  `specs/issues/token-cost/03-autocompaction.md:56-66`:
  `X × 0.1 × remaining_turns > 0.9 × context_size`, explicitly gated on the
  cache numbers that R5 says are missing.
- SoL-Pi implements the arithmetic in
  `src/sol-pi/extensions/online-context-compact/economics.ts:122-236`:
  `breakevenRequests = writeTokens × (ratio − 1) / savingTokens`, plus a
  `windowProtection` override (`contextTokens ≥ window − 16384`) that forces
  compaction regardless of price. The override matters: a pure economics gate
  would refuse the one compaction you cannot afford to skip.

Out of scope for this spec (needs R5 first), but item 5 exists so that gate
becomes implementable.

---

## R6. What pi-go already does well (do not "fix" these)

| Mechanism | Location | Note |
|---|---|---|
| Deterministic compactor | `internal/tools/compactor*.go` | pure Go, no model call — SoL-Pi has no equivalent |
| Byte-identical dedup | `internal/tools/dedup.go` | correct tool names; runs *after* compactor by design |
| Superseded shedding | `internal/session/compaction_shed.go:41` | keys on `(tool, canonical target)`; errors never shed (`:285-288`) |
| Window resolution | `internal/ctxwindow/ctxwindow.go:27` | catalog → live Ollama/OpenRouter → config override |
| Two agreeing meters | `guardrail.go` / `autocompact/meter.go` | pinned by `TestTrackerAndMeterAgreeOnBodyTokens` |

Two known redundancies, noted but not in scope:

- `internal/cli/interactive.go:1072` `switchContextWindowSize` reimplements the
  `ctxwindow.Resolve` precedence ladder inline instead of calling it.
- `internal/session/autocompact.go:87` calls `ShedSupersededToolResults` rather
  than the dedup-aware `...WithDedup` variant; harmless today because a dedup
  pointer (~150 bytes) is below `shedMinBytes = 400`.

---

## R7. Deliberately not adopted

### R7.1 Action Fusion — rejected on the merits

SoL-Pi fuses a mutation with a follow-up command via a `then_run` parameter
(`src/sol-pi/extensions/action-fusion/index.ts:86-118`). It needs a TOCTOU guard
because it cannot lock: `src/sol-pi/extensions/action-fusion/then-run.ts:54-68`
hashes the target, yields to the event loop, and re-hashes — refusing to run if
content changed. Its file queue deliberately does not nest Pi's built-in
mutation queue (`docs/compatibility.md:22`).

pi-go's own measurement argues against porting it:

- `specs/issues/token-cost/TOKENS.md` — 81.8% of tool-issuing turns make exactly
  one tool call, and **batching** is simulated to cut prompt tokens 48–65%.
- The marginal cost of a tool call is the whole re-sent context (~69k tokens),
  so the fix is fewer *requests*, not fewer *parameters* per request.

Fusion also has no natural home in pi-go: `write` (`internal/tools/write.go:11-16`),
`edit` (`internal/tools/edit.go:13-22`) and `bash` (`internal/tools/bash.go:38-49`)
have no follow-up field.

### R7.2 Plan-phase semantic triggers — deferred, not rejected

Nothing in `internal/session/compaction*.go` or `internal/autocompact/*.go`
references plan state (`rg 'PlanContext|plan'` → only an unrelated comment at
`compaction_shed.go:284`). pi-go's plan concept is a TUI concern
(`internal/tui/plan.go:457` persists `session.PlanContext`; phases are derived
from on-disk artifacts in `internal/tui/sidebar.go:127`).

Genuinely promising, but needs R3 (rejectable reduction) so a bad semantic cut
can be undone, plus the correctness argument for cutting mid-run. Deferred.

### R7.3 The TUI savings display

`src/sol-pi/tui.ts:52-71` `showSolPiSavings` calls `ctx.ui.notify`/`setStatus`.
Cosmetic, and it creates pressure to over-report savings that R4 says we cannot
currently verify.
