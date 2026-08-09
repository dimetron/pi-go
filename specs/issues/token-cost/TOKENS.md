# Token Cost: Where pi-go's 2.5 Billion Prompt Tokens Go

## Summary

Across 1,404 recorded sessions (`~/.pi-go/sessions/*/events.jsonl`), pi-go has spent
**2,536,063,043 prompt tokens to produce 12,605,019 output tokens** — a ratio of
**201:1**. The average LLM call carries **68,874 prompt tokens**.

That ratio is not inherent to agentic coding. It is the product of four
measurable, independently fixable causes:

1. **Turns are not batched.** 81.8% of tool-issuing turns make exactly one tool
   call. Because the entire context is re-sent on every call, the marginal cost
   of a tool call is ~69k tokens regardless of what it returns. Simulated
   batching cuts total prompt tokens by **48–65%**.
2. **73% of prompt tokens run on routes that report zero cache.** The Ollama
   provider never populates `CachedContentTokenCount`, so the guardrail
   tracker, the context gauge, and — critically — the auto-compaction trigger
   are all operating on wrong numbers for most traffic.
3. **Auto-compaction has effectively never run.** It fired in 9 of 1,046
   sessions (0.9%). The `shed` stage — which is free, requires no LLM call, and
   would recover 6.8% — has never triggered in production.
4. **Three concrete bugs** silently disable compaction for git tools, return
   whole source files uncapped, and leave the file-content cache as dead code.

Separately, the memory subsystem is built but largely unreachable: **the
interactive TUI injects no memory context at all**, and `session_summaries` is
never written.

For calibration, Claude Code working on this same repository runs a **98.5%
cache hit rate** (1.68 B input tokens → ~198 M effective).

## How this was measured

Every session already records `UsageMetadata` per turn. Nothing new was
instrumented; the numbers below come from replaying on-disk history.

```bash
# per-turn usage is already there:
jq -c 'select(.UsageMetadata != null) | .UsageMetadata' \
   ~/.pi-go/sessions/<id>/events.jsonl
# → {"candidatesTokenCount":79,"promptTokenCount":10886}
```

Scope: 1,404 sessions carrying usage data, 36,822 LLM calls, 48,782 tool calls.
Session dirs also hold `meta.json` (model, provider, workDir) and
`trajectory.atif.json`.

---

## Finding 1 — Turns are not batched (48–65% recoverable)

Cost within a session is quadratic in turn count: every token added at turn *i*
is re-sent on all remaining turns. So the lever is not result size, it is
**round trips**.

```
model turns issuing tools:  36,841
tool calls:                 48,782
average per turn:            1.32
```

| tool calls in turn | turns | share |
|---|---|---|
| 1 | 30,141 | **81.8%** |
| 2 | 3,875 | 10.5% |
| 3 | 1,691 | 4.6% |
| 4 | 619 | 1.7% |
| 5 | 209 | 0.6% |
| 6+ | 306 | 0.8% |

Back-to-back calls to the *same* tool — the obviously batchable case:

| tool | consecutive repeats |
|---|---|
| `bash` | 18,295 |
| `read` | 6,581 |
| `edit` | 1,918 |
| `ripgrep` | 1,010 |

Replaying every session with the same content delivered in fewer LLM calls
(deltas regrouped, context growth held constant):

| tools/turn | total prompt tokens | reduction |
|---|---|---|
| 1.32 (actual) | 2,526,022,383 | — |
| 2 | 1,304,609,557 | **48.4%** |
| 3 | 893,974,073 | **64.6%** |
| 4 | 687,810,986 | 72.8% |

This is the largest single lever in this document and it costs no correctness —
the same tools run, on the same inputs, in fewer round trips.

### Concentration

78 sessions with more than 100 turns hold **49.6% of all prompt tokens**.

| turns | sessions | prompt tokens | share | avg/session |
|---|---|---|---|---|
| 1–10 | 673 | 64,192,731 | 2.5% | 95,382 |
| 11–30 | 356 | 225,895,893 | 8.9% | 634,539 |
| 31–60 | 204 | 454,765,021 | 17.9% | 2,229,240 |
| 61–100 | 93 | 534,246,271 | 21.1% | 5,744,583 |
| **100+** | **78** | **1,256,963,127** | **49.6%** | 16,114,911 |

Those same 78 sessions made **61 subagent calls** between them, against 407
across all shorter sessions. The sessions that most need work pushed into a
child context use that mechanism least.

---

## Finding 2 — 73% of tokens run on routes reporting zero cache

`UsageMetadata.CachedContentTokenCount`, aggregated by model:

| model | LLM calls | prompt tokens | cached | hit rate |
|---|---|---|---|---|
| `minimax-m3:cloud` | 14,088 | 1,215,502,810 | 0 | **0.0%** |
| `minimax-m2.7:cloud` | 7,913 | 326,041,328 | 0 | **0.0%** |
| `deepseek-v4-flash:0731-cloud` | 2,597 | 273,278,416 | 0 | **0.0%** |
| `glm-5.2:cloud` | 3,805 | 183,000,068 | 0 | **0.0%** |
| `gpt-5.5` | 3,257 | 181,579,943 | 1,098,752 | **0.6%** |
| `deepseek-v4-flash:cloud` | 758 | 57,796,170 | 0 | **0.0%** |
| `minimax-m3` | 1,074 | 88,062,037 | 87,849,843 | 99.8% |
| `deepseek-v4-flash` | 434 | 63,356,042 | 62,257,024 | 98.3% |
| `gpt-5.6-luna` | 796 | 57,949,768 | 47,508,480 | 82.0% |
| `gemini-3.5-flash` | 552 | 26,686,684 | 22,716,159 | 85.1% |

The same model on two routes reports 99.8% and 0.0%. That is not a caching
difference, it is a reporting difference: `CachedContentTokenCount` is
populated only in three providers —

- `internal/provider/anthropic.go:532,663,808` — `CacheReadInputTokens`
- `internal/provider/openai_completions.go:226,253,322` — `PromptTokensDetails.CachedTokens`
- `internal/provider/openai_responses.go:378,463,538` — `InputTokensDetails.CachedTokens`

`internal/provider/ollama.go` never sets it. Everything routed through Ollama
reports zero.

### Why this matters beyond reporting

`Tracker.AddWithCache` (`internal/guardrail/model_wrapper.go:49`) feeds
`CachePrefixTokens()` and `BodyTokens()`
(`internal/guardrail/guardrail.go:354,363`), and `BodyTokens()` is exactly what
auto-compaction decides on:

```go
// internal/cli/autocompact_hook.go:52-55
body   := deps.Tracker.BodyTokens()
window := deps.Tracker.ContextWindowSize()
if deps.Cfg.Decide(body, window) == pisession.CompactionNone { return nil }
```

With cached tokens reported as 0, the prefix baseline is 0 and `BodyTokens()`
degenerates to the full prompt. Fixing this changes compaction behaviour, so it
must land **before** any threshold tuning.

`gpt-5.5` at 0.6% over 181 M tokens is a separate anomaly worth its own check —
OpenAI caches prefixes automatically, so either the prefix is being invalidated
or `PromptTokensDetails` is not surviving the response path on that model.

---

## Finding 3 — Auto-compaction has effectively never run

Detecting compaction by its signature (a >30% drop in `promptTokenCount`
between consecutive calls):

```
sessions >= 5 turns:                       1,046
sessions showing a compaction drop:            9   (0.9%)
```

Zero drops in `minimax-m3:cloud` (266 sessions), `deepseek-v4-flash:0731-cloud`
(51), `gpt-5.5` (111), or `glm-5.2:cloud` (140).

The cause is threshold arithmetic. `DefaultAutoCompactConfig`
(`internal/session/compaction.go:80-88`) sets `ShedPercent: 60`. Against a ~1 M
context window that triggers at 600k tokens. **The largest context ever
observed across all 1,404 sessions is 513,673 tokens.** The threshold is above
the ceiling.

This matters most for `shed`, because `shed` is the cheap half. Per the design
comment at `internal/session/compaction.go:16-25`, shedding drops payloads of
tool results superseded by a later call on the same target — no LLM call
required. It has never run.

Simulating continuous shedding (stub superseded payloads as soon as they are
superseded, `KeepRecentEvents: 10` respected, 120-byte stub cost):

```
actual prompt tokens:          2,522,163,099
saved by continuous shedding:    170,543,907   (6.8%)
```

Per-session the win is much larger where it counts — 22.7%, 22.6%, 26.6%, 37.2%
on the heaviest sessions.

### The caveat that shapes the fix

Shedding rewrites history mid-prefix, which invalidates the cache suffix. Where
caching works, a cache read costs ~10% of a fresh token, so shedding *X* tokens
is only profitable when:

```
X × 0.1 × remaining_turns  >  0.9 × context_size
```

So shed should be gated on observed cache behaviour rather than run
unconditionally — which is another reason Finding 2 must be fixed first.

---

## Finding 4 — Three bugs

### 4a. The compactor never sees git tool output (one-line fix)

`internal/tools/compactor.go:108-113` routes on underscores:

```go
case "git_file_diff": return compactGitFileDiff(result, cfg)
case "git_overview":  return compactGitOverview(result, cfg)
case "git_hunk":      return compactGitHunk(result, cfg)
```

The tools register with hyphens:

- `internal/tools/git_overview.go:52` — `newTool("git-overview", …)`
- `internal/tools/git_diff.go:36` — `newTool("git-file-diff", …)`
- `internal/tools/git_hunk.go:42` — `newTool("git-hunk", …)`

No case ever matches, so `compactGitFileDiff`, `compactGitOverview` and
`compactGitHunk` are unreachable. `internal/tools/dedup.go:41-43` uses the
hyphenated names and works correctly — which is why this went unnoticed.

### 4b. `read` returns whole source files uncapped

`internal/tools/read.go:136-144`:

```go
limit := input.Limit
if limit <= 0 {
    ext := strings.ToLower(filepath.Ext(input.FilePath))
    if sourceCodeExts[ext] {
        limit = totalLines            // no line truncation for source code
    } else {
        limit = defaultReadLimit      // 2000
    }
}
```

`sourceCodeExts` (`read.go:18-40`) covers `.go`, `.ts`, `.py`, `.rs`, `.java`
and 15 more. Only the 256 KB byte net (`truncate.go:4`) applies. This is why
`read` averages 6,974 bytes per result and accounts for 45.7% of all resend
debt.

### 4c. `FileContentCache` is dead code

`internal/tools/cache.go:10` implements exactly the mtime-invalidated,
TTL-bounded, LRU file cache this problem calls for. `read.go:86`
`readHandlerWithCache` uses it. But `read.go:83` passes `nil`:

```go
func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
	return readHandlerWithCache(sb, input, nil)
}
```

and `registry.go:45` registers `newReadTool`, which routes to `readHandler`.
`NewFileContentCache` has no production caller. `edit.go:38→43` has the same
shape.

Note this is an I/O cache, not a context-level one — wiring it alone would not
keep bytes out of the prompt. It needs to be paired with a "this file is
unchanged since call #N" pointer.

### Also worth flagging: `smartTruncate` reorders `read` output

`compactor_bash.go:382-406` is applied to `read` results too. Above 440 lines
it keeps head 10% / tail 10% and fills the middle with **priority-scored,
reordered** lines (`func `/`type `/`import` scored 5, error/fail scored 10).
For a numbered source listing this yields non-contiguous, out-of-order line
numbers, and the model has no way to detect it. Cheap in bytes, expensive in
correctness.

---

## Finding 5 — Memory is built but unreachable

### The interactive TUI injects no memory context

`memoryInstructionContext` has exactly one caller:

- `internal/cli/cli.go:632` — `instruction += memoryInstructionContext(...)`, on
  the **non-interactive** (`--print` / json) path only.
- Palace equivalent: `internal/cli/cli.go:593` — `## Palace Memory Context`,
  same path only.

`internal/cli/interactive.go:316-328` builds its instruction from
`agent.LoadInstructionParts(...)` alone. `initMemoryAfterUI`
(`interactive.go:511,683`) starts memory purely for *recording* and for the
`mem-*` tools. The primary usage mode gets nothing.

### Injection is once per process

The generated markdown is baked into the static system instruction at startup
and never regenerated (`cli.go:588-632`, consumed by `agent.New`). Observations
recorded during a session never re-enter its own context, and the block cannot
be relevance-filtered against the user's prompt because no prompt exists yet.

### Retrieval is pure recency

`internal/memory/context.go:34-51`: `RecentSummaries(project, 3)` +
`RecentObservations(project, 200)`, filtered to a hardcoded 72 hours. No FTS, no
semantic search, no query. `Store.Search` (FTS5, `search.go:19-33`) and the only
genuine hybrid keyword+semantic path (`palace/drawer_service.go:122-165`) are
reachable **only** if the model chooses to call `mem-search` or the palace
search tool.

`config.MemoryDefaults().LookbackHours` is parsed and copied but never read —
`interactive.go:740` carries `//nolint:govet // reserved for future use`.

### `session_summaries` is never written

`Store.CompleteSession` (`store.go:54`), `Store.UpsertSummary` (`store.go:152`)
and `SubagentCompressor.SummarizeSession` (`compress.go:161`) have **no
production callers** — only the `lazyMemoryStore` pass-throughs at
`interactive.go:559,591`. No session-end hook exists.

Consequence: the `summaries` half of `ContextGenerator.Generate()` is always
empty, and session titles always fall back to raw session IDs
(`context.go:120-123`).

---

## Recommendations, in dependency order

| # | Change | Expected | Risk |
|---|---|---|---|
| 1 | Populate `CachedContentTokenCount` on the Ollama path; log explicitly when a route reports nothing | unblocks 2–3 | low |
| 2 | Raise tools-per-turn (prompt + parallel dispatch) | **48–65%** | low |
| 3 | Make `shed` continuous, gated on observed cache behaviour | 6.8%, up to 37% on heavy sessions | medium |
| 4 | Fix compactor git tool names | unblocks 3 dead stages | trivial |
| 5 | Cap `read` on source files at 2,000 lines; wire `FileContentCache` + an unchanged-file pointer | ~45% of resend debt | low |
| 6 | Replace `smartTruncate` for `read` with contiguous head/tail | correctness | low |
| 7 | Call `memoryInstructionContext` from `interactive.go`; call the session summarizer at exit | recall quality | low |
| 8 | Route long sessions (>60 turns) to subagents | attacks the 49.6% | medium |

**1 must precede 2 and 3.** Threshold tuning against wrong body-token numbers
will produce the wrong thresholds.

## How to keep measuring

The substrate exists; almost nothing needs new plumbing.

- **Ship the replay as `pi tokens`.** Per session: prompt total, turns,
  tools/turn, cache hit rate, and `prompt_tokens / output_tokens` as the
  headline. That ratio — **currently 201:1** — is the single number to drive
  down.
- **Extend the OTel span.** `internal/extension/hooks.go:355-370` already emits
  `gen_ai.usage.input_tokens`, `gen_ai.usage.cached_input_tokens`,
  `gen_ai.usage.reasoning_tokens`, `gen_ai.usage.total_tokens`. Adding
  `tools_per_turn` and `turn_index` makes the quadratic visible directly.
- **Persist compactor metrics.** `compactor_metrics.go:112` `Save()` has no
  caller, and `cli.go:613` builds the metrics object inline and discards it. The
  real per-tool compression ratio is currently unknown to everyone.
- **Guard against regressions.** Pin a handful of representative sessions as
  fixtures and assert total prompt tokens for a fixed transcript. Context
  changes then show up as a number in CI rather than as a feeling.

### Reproducing the numbers

```bash
# per-turn usage for one session
jq -c 'select(.UsageMetadata != null) | .UsageMetadata' \
   ~/.pi-go/sessions/<id>/events.jsonl

# corpus-wide totals, cache rates by model, batching histogram,
# shed simulation and batch simulation all replay:
#   ~/.pi-go/sessions/*/meta.json      (model, provider, workDir)
#   ~/.pi-go/sessions/*/events.jsonl   (UsageMetadata, functionCall, functionResponse)

# today's aggregate, already tracked:
cat ~/.pi-go/usage.json
```

## Appendix — corpus at time of writing

```
sessions with usage data        1,404
LLM calls                      36,822
tool calls                     48,782
prompt tokens           2,536,063,043
output tokens              12,605,019
ratio                             201:1
avg prompt per call            68,874
median first-turn prompt        6,260
p90 first-turn prompt          16,237
largest context observed      513,673
```

Resend debt by tool — result bytes weighted by how many later LLM calls re-send
them:

| tool | calls | result MB | avg B/call | share of debt |
|---|---|---|---|---|
| `read` | 11,743 | 81.9 | 6,974 | **45.7%** |
| `bash` | 23,311 | 49.6 | 2,128 | **32.3%** |
| `ripgrep` | 2,640 | 19.2 | 7,281 | 11.3% |
| `subagent` | 450 | 5.8 | 12,966 | 4.1% |
| `edit` | 4,160 | 1.3 | 314 | 1.1% |
| `tree` | 439 | 2.7 | 6,094 | 0.9% |

16% of all `read` bytes (13.5 MB of 82.6 MB) are re-reads of a file already read
earlier in the same session — 132 M tokens of resend debt. `bash` is
deliberately excluded from dedup (`internal/tools/dedup.go:34-45`), so its
23,311 calls get none.

### On-disk footprint (not a token cost, but worth knowing)

| Location | Size |
|---|---|
| `~/.pi-go/sessions` | 831 MB (3,328 sessions; 593 MB in `archive/`) |
| `pi-go/.pi-go/palace.db` | 99 MB (vs 168 KB at `~/.pi-go/palace.db` — the repo-local one is live) |
| `/tmp/claude-501` | 631 MB |
| `~/.claude/projects` | 1.0 GB |
