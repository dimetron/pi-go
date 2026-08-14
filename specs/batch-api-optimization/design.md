# Design

Two directions were investigated. One is rejected with numbers; the other is
accepted and scoped. This document states both decisions with the alternatives
that lost.

## D1 — Provider Batch APIs: rejected

### The decision

**pi-go will not build a Batch API client.** No `internal/provider/batch.go`,
no durable job store, no pending-result reconciliation.

### Why

A batch API needs work whose result nobody is waiting for. `research/call-sites.md`
enumerates every model call in the tree. The result:

| Site | Status |
|------|--------|
| Main agent turn | User waits |
| `LLMSummarizer` (auto-compaction) | User waits, mid-turn |
| `subagent` tool | Parent turn waits |
| `/commit` message generation | User waits |
| `pi ping` | User waits |
| `SummarizeSession` | **No production caller** |
| Palace KG extraction / miners | **Makes no LLM call** |
| `internal/eval` `Judge` | Only caller is a build-tagged test |
| Memory compressor | The one real candidate |

The candidate pool is one site. That site has produced **one observation in the
lifetime of the database** (`research/measurements.md` § M7) because the
after-tool callback chain that feeds it is dead.

### The number, computed anyway

Suppose `specs/memory-fixes` lands and the compressor runs on every tool call.
Measured tool-call volume is 5,248 over two days = **2,624 calls/day**.

Per compression, after memory-fixes R4 moves it in-process against `smol`:
~350 tokens of instruction (`internal/subagent/bundled/memory-compressor.md`)
plus a payload capped at `maxPromptOutput = 4096` bytes ≈ 1,150 tokens, and
~150 output tokens. Call it 1,500 in / 150 out.

Daily: 3.94M prompt tokens, 0.39M output tokens.

| `smol` model | Standard cost/day | With 50% batch | **Saving** |
|--------------|-------------------|----------------|------------|
| gpt-5.6-luna ($0.20 / $1.20) | $1.26 | $0.63 | **$0.63/day** |
| Claude Haiku 4.5 ($1 / $5)   | $5.91 | $2.96 | **$2.96/day** |

**$0.63 to $2.96 per day.** Against that:

- A durable job store. Batches take up to 24 hours; pi is a CLI that exits.
  Pending batch IDs and their custom-ID → observation mappings must survive
  process death, be reclaimed on the next start, and be reconciled against a
  results file that expires after 29 days (Anthropic) or 30 (OpenAI).
- Per-provider divergence. Anthropic takes inline request arrays; OpenAI takes
  an uploaded JSONL file and returns an output file; Gemini takes a
  `BatchJobSource`. Three code paths, three result formats, three failure modes.
- xAI cannot participate at all — `grok-4.5` and `grok-4.6` are explicitly
  rejected by its Batch API (`research/providers.md`). Ollama has no batch API.
- Memory would arrive up to 24 hours late. `MemoryConfig.LookbackHours` defaults
  to 72 (`internal/config/config.go`), so a day's delay does not break
  retrieval — but it does mean a session's own observations are never available
  to the session that produced them, or to the next few.

### The alternative that beats it

`specs/memory-fixes` R4 already requires the compressor to stop spawning a child
`pi --mode json` per tool call. Today each compression is a full process boot
carrying pi's base system prompt — roughly 5,500 prompt tokens instead of 1,500.

| Change | Saving/day (Haiku 4.5 `smol`) |
|--------|-------------------------------|
| memory-fixes R4 (in-process compression) | ~$10.5 |
| Batch API on top of R4                   | ~$3.0  |

**The fix that is already specced saves more than the fix this spec was asked to
consider, and it has no 24-hour latency and no durable job store.** That is the
whole argument.

### What would change the answer

State these plainly so the decision can be revisited on evidence rather than
re-litigated:

1. **An eval harness that runs sweeps.** `internal/eval/judge.go` is
   provider-agnostic (`CompleteFunc`) and a sweep is the canonical batch
   workload. If `pi eval` lands and routinely issues thousands of judge calls,
   Anthropic's batch path is a ~50-line addition behind that one interface.
2. **A `smol` role pointing at a frontier model.** At Opus 5 prices the
   compressor's 3.94M tokens/day costs ~$29/day and batching saves ~$14. Still
   modest, but the arithmetic moves.
3. **Compression volume rising an order of magnitude** — per-message sweeping,
   or memory over full session transcripts rather than single tool results.

None of these is true today.

## D2 — Reduce round trips: accepted

### The decision

Treat **requests per session** as the cost metric, measure it, and change the
system prompt to raise tool calls per response. No new tool, no provider change,
no ADK change.

### The mechanism

Let `C_k` be context size at request `k`, `P` the fixed preamble, `d` the tokens
each step appends:

```
Total = Σ_{k=1..N} C_k = N·P + d·N(N-1)/2
```

Merge `m` calls per request. `N` becomes `N/m`; each step appends `m·d`, because
the same tool results still land in the transcript:

```
Total' = (N/m)·P + m·d·(N/m)((N/m)-1)/2  ≈  (N·P + d·N²/2) / m
```

Both terms divide by `m`. This is why the payoff exceeds the naive "fewer
requests" intuition, and it is corroborated by the measured growth curve: median
prompt size rises 16.6× from request 1 to request 100
(`research/measurements.md` § M1).

### Why nothing needs building

- **ADK dispatches concurrently.** `internal/llminternal/base_flow.go:1063`
  `handleFunctionCalls` builds one task per call and hands them to the platform
  task runner, whose default (`platform/exec.go:68`) fans out goroutines and
  joins a `WaitGroup`. pi-go installs no custom runner. Batching therefore
  *improves* wall-clock latency as well as cost.
- **Ordering is already deterministic.** Responses are written to
  `fnResponseEvents[i]` by index, not by completion order.
- **Error attribution is already per-call.** Each call gets its own span and its
  own `functionResponse` part; a failure in one does not mask the others.
- **The providers already do it.** Measured, not assumed: responses with 2, 3,
  4, 5 and 6 calls all appear in the logs, against the OpenAI provider.

### The gap is the prompt

`internal/agent/agent.go:185` currently reads:

> "You **can** call multiple tools in a single response when they are
> independent."

Forty-four lines later, `internal/agent/agent.go:235`:

> "- **Prefer parallel over sequential**: when researching a topic, spawn 2-4
> explore agents…"

Subagent batching is an instruction. Tool batching is a permission. The measured
behaviour follows the weaker one: mean 1.307 calls per response, 71.3% of
responses carrying exactly one.

The change is to make the tool-batching guidance directive, name the concrete
batchable tools, and give the model the reason — that each response is a round
trip which re-sends the entire conversation.

### Expected payoff

Measured ceiling: collapsing every run of consecutive single-read-only-call
responses removes **982 of 4,016 responses = 24.5%**
(`research/measurements.md` § M3). That is an upper bound and assumes perfect
independence, which is false — a `read` whose path came from the preceding
`ripgrep` cannot move earlier.

| Scenario | Mean calls/response | Requests | Requests removed | Prompt spend | $/day (luna) | $/day (Opus 5) |
|----------|--------------------|----------|------------------|--------------|--------------|----------------|
| Today    | 1.307              | 4,016    | —                | 310.1M       | $24.55       | $609           |
| Target   | 1.60               | 3,402    | 15.3%            | −15.3%       | −$3.76       | −$93           |
| Ceiling  | 1.73               | 3,034    | 24.5%            | −24.5%       | −$6.01       | −$149          |

Request counts hold the 5,248 measured tool calls and the 122 text-only
responses fixed, and vary only how many calls share a response:
`3,402 = 5,248/1.6 + 122`. Prompt-spend reduction equals the request reduction
because `Total' ≈ Total/m` and `requests removed = 1 − 1/m` — the same `m`.

The 25× spread between the two right-hand columns is the honest headline: at
`gpt-5.6-luna` this work saves the price of a coffee per day and is justified by
latency, not cost. At Opus 5 it saves ~$93/day. **The current default role is
`gpt-5.6-luna`** (`$HOME/.pi-go/config.json`), so today the cost case is weak
and the latency case — concurrent tool execution instead of serialised
round trips — is the stronger one.

### What breaks

State the costs, not just the upside:

- **Wasted work on dependent calls.** If the model batches a `read` that a
  concurrent `ripgrep` was going to redirect, that read is thrown away. It costs
  a tool execution and the result still enters the transcript, so a bad batch is
  *more* expensive than two good serial calls. The existing caveat — "Only
  parallelize when operations are truly independent" — must survive the rewrite.
- **Never batch mutations.** `edit`, `write` and mutating `bash` must stay
  serial. Concurrent edits to the same file are a correctness bug, and ADK runs
  the calls genuinely in parallel — the race is real, not theoretical.
- **Coarser failure granularity for the model.** Five results arrive together;
  the model must attribute a failure to the right call. ADK keeps the parts
  separate, so this is a model-comprehension risk, not a plumbing one.
- **Harder-to-follow TUI.** Five tool cards appearing at once is less readable
  than five in sequence. The status bar already tracks concurrent tools
  (`internal/agent/agent.go:191`), so this is a polish concern.
- **The metric can be gamed.** "Calls per response" rises if the model issues
  useless extra calls. Pair it with total requests per completed task, and never
  report it alone.

## D3 — `bash_batch` tool: rejected

A tool taking an array of commands and returning an array of results was
considered. Rejected:

1. **It duplicates ADK.** N concurrent `bash` calls already work, already run in
   parallel, already return per-call results.
2. **It loses error attribution.** One tool result is one `functionResponse`.
   Five commands inside it become one blob the model must parse, with one
   error channel where ADK gives five.
3. **It saves nothing extra.** The saving is one fewer round trip, and the model
   emitting five `bash` calls in one response already achieves exactly that.
4. **It adds a schema.** Tool declarations are part of the fixed preamble this
   spec is trying not to grow.

The only thing `bash_batch` would add over the status quo is a nudge — a schema
that *suggests* batching. That nudge belongs in the system prompt, where it
costs a paragraph rather than a tool.

## D4 — Measurement

None of D2 is verifiable today. `pi session-stats` counts tool calls; it does
not report requests, calls per response, or prompt tokens per request.

Add three counters, derived from the same `UsageMetadata`-bearing events used
throughout `research/measurements.md`:

| Counter | Definition |
|---------|------------|
| `requests` | Events carrying `UsageMetadata` |
| `calls_per_response` | `function_call` parts ÷ requests |
| `prompt_tokens_per_request` | Σ `prompt_token_count` ÷ requests |

Report them in `--json` and in the human summary. Without these the prompt
change in D2 is an opinion.

## D5 — Regression guard for the poll storm

`bash_wait` blocks up to `maxBashWait = 60s` (`internal/tools/bash.go:114`) and
its description instructs against looping (`internal/tools/bash.go:187`). This
fix is worth 19.7% of prompt spend in the measured window and has no test
pinning the behaviour that delivers it.

Add a test asserting `bash_wait` returns only when the command produces output
or exits — not on a fixed short interval — so a future change cannot silently
restore polling. `specs/…/test/bash-wait-exit-detection` (commit `5362ffd`)
already pins exit detection; the missing half is that an idle command makes the
call *block* rather than return empty immediately.
