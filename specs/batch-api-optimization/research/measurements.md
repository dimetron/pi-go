# Measurements

Everything below was measured on 2026-08-14 from `$HOME/.pi-go/sessions/*/events.jsonl`,
the ADK event stream pi-go already writes. Reproduction scripts are inline so the
numbers can be re-derived rather than trusted.

## Sample

The 400 most recently modified session directories. 365 have a non-empty
`events.jsonl`; 125 contain at least one model response carrying
`UsageMetadata`. Model-response timestamps in the sample fall entirely on
**2026-08-10 and 2026-08-11** — a two-day window.

```python
# For each event with UsageMetadata (one per model request), accumulate
# prompt_token_count / candidates_token_count / cached_content_token_count,
# and count function_call parts in Content.parts.
```

| Quantity                                  | Measured      |
|-------------------------------------------|---------------|
| Model requests (events with UsageMetadata)| 4,016         |
| Prompt tokens                             | 310,118,987   |
| Output tokens                             | 1,895,967     |
| Cached prompt tokens                      | 84,478,023    |
| Prompt : output ratio                     | **163.6 : 1** |
| Prompt tokens per request (mean)          | **77,220**    |
| Function calls issued                     | 5,248         |
| Function calls per model response (mean)  | **1.307**     |

Requests per session: median 15, mean 32.1, p90 86, max 204.

### M1 — Input dominates, and it is not the preamble

The 163:1 prompt-to-output ratio confirms the framing this spec started from:
output tokens are noise. What the sample *contradicts* is the assumption that
the total is the fixed preamble times the request count.

Median prompt size by request index within a session:

| Request # | n   | Median prompt tokens | Mean     |
|-----------|-----|----------------------|----------|
| 1         | 125 | 9,131                | 11,094   |
| 2         | 123 | 14,318               | 16,002   |
| 3         | 101 | 17,244               | 22,802   |
| 5         | 93  | 24,065               | 30,409   |
| 10        | 73  | 38,341               | 47,572   |
| 20        | 51  | 49,081               | 56,472   |
| 40        | 34  | 66,025               | 73,388   |
| 60        | 20  | 81,481               | 103,476  |
| 80        | 15  | 95,441               | 121,148  |
| 100       | 11  | 151,357              | 144,470  |

Request #1's prompt — system prompt, tool declarations *and* the first user
message — is 9,131 tokens. That is an upper bound on the fixed preamble. Across
4,016 requests the preamble therefore accounts for at most
`9,131 × 4,016 = 36.7M` of 310.1M prompt tokens: **≤ 11.8%**.

The other ~88% is re-sent conversation. By request #100 the fixed preamble is
6% of a 151k-token prompt; the rest is accumulated assistant turns and tool
results being shipped again.

**Consequence.** Shrinking the preamble (the tool-gating work in PR #160) has a
hard ceiling of ~12% and falls off as sessions get longer. Removing *requests*
attacks the dominant term — and does so superlinearly.

### M2 — Why removing a request is worth more than it looks

Let `C_k` be the context size at request `k`, `P` the fixed preamble, and `d`
the tokens each step appends (assistant message + tool result):

```
C_k    = P + (k-1)·d
Total  = Σ C_k = N·P + d·N(N-1)/2
```

Merge `m` tool calls into each request. The number of requests becomes `N/m`;
each step now appends `m·d` because the same tool results still land in the
transcript:

```
Total' = (N/m)·P + m·d·(N/m)((N/m)-1)/2  ≈  (N·P + d·N²/2) / m
```

**Both terms divide by `m`.** Issuing two tool calls per request instead of one
roughly halves total prompt spend for the same work. This is the whole economic
case for direction 2, and it is why it outranks everything else in this spec.

The empirical growth curve in M1 is consistent with this: prompt size per
request grows 16.6× from request 1 to request 100, so total spend is dominated
by the quadratic term, not the linear one.

### M3 — Current tool-calls-per-response, and the headroom

Distribution of function calls per model response (n = 4,016):

| Calls in response | Responses | Share  |
|-------------------|-----------|--------|
| 0 (final text)    | 122       | 3.0%   |
| 1                 | 2,863     | 71.3%  |
| 2                 | 828       | 20.6%  |
| 3                 | 111       | 2.8%   |
| 4                 | 68        | 1.7%   |
| 5                 | 20        | 0.5%   |
| 6                 | 4         | 0.1%   |

Mean 1.307. **Multi-call responses already happen** — 25.7% of responses carry
two or more — so nothing in the stack forbids them. The most common multi-call
shapes are `('bash',)` ×565, `('read',)` ×132, `('bash','read')` ×93,
`('read','ripgrep')` ×57. The model batches when it feels like it.

Headroom, measured directly: collapse every maximal run of *consecutive
single-read-only-call responses* into one request and count the responses that
disappear.

| Run length | Runs |
|------------|------|
| 1          | 404  |
| 2          | 106  |
| 3          | 48   |
| 4          | 23   |
| 5          | 15   |
| 6–12       | 45   |
| 13–57      | 13   |

Removable responses: **982 of 4,016 = 24.5%**.

This is an **upper bound**, not a forecast. It assumes every call in a run is
independent, and many are not — a `read` whose path came from the preceding
`ripgrep` cannot move earlier. Treat 24.5% as the ceiling and anything above
~10% realised as a good outcome.

### M4 — Tool call mix

| Tool          | Calls |
|---------------|-------|
| bash          | 2,691 |
| read          | 904   |
| bash_output   | 818   |
| edit          | 252   |
| ripgrep       | 246   |
| find          | 55    |
| subagent      | 51    |
| ls            | 47    |
| git-file-diff | 42    |
| tree          | 32    |
| git-overview  | 29    |
| bash_kill     | 28    |
| write         | 26    |
| (MCP + rest)  | <30   |

`bash` + `read` + `ripgrep` + `find` + `ls` + `tree` = 3,975 of 5,248 = **75.7%**
of all calls, and all six are read-only and order-independent in the common
case. That is the batchable population.

### M5 — The poll storm, and what it cost (already fixed)

799 of 4,016 responses — **19.9%** — contained nothing but `bash_output` calls.
They consumed **61,072,982 prompt tokens, 19.7% of the sample's entire prompt
spend**, to retrieve the incremental output of a background shell command.

Of those 799, **504 were consecutive repeat polls** of the same handle. The
worst single sessions contain runs of 88 and 91 back-to-back poll-only requests.

**This is already fixed on `main`.** Commit `ffe337a` renamed the tool to
`bash_wait`, made it block for up to 60 s (`maxBashWait`,
`internal/tools/bash.go:114`), and rewrote its description to say so:

> "Wait once with a generous `wait_ms` rather than calling this in a loop —
> each call is a round trip, and an empty result means nothing new since the
> last one, not that the command is stuck."
> — `internal/tools/bash.go:187`

Every poll in the sample is named `bash_output`; `bash_wait` does not appear,
and no session after 2026-08-11 issues a poll at all. The measurement is
therefore a **valuation of a fix that has already landed**, not an outstanding
opportunity. It is recorded here because it is the cleanest available proof of
M2's thesis: 800 round trips that added almost no information to the transcript
still cost a fifth of the bill, purely because each one re-sent everything.

Per day by tool name, across all sessions:

| Day        | bash_output calls |
|------------|-------------------|
| 2026-08-08 | 380               |
| 2026-08-09 | 377               |
| 2026-08-10 | 806               |
| 2026-08-11 | 155               |
| after      | 0                 |

### M6 — What the sample actually costs

The configured default role is `gpt-5.6-luna` on the `openai` provider
(`$HOME/.pi-go/config.json`). List price: **$0.20 / $0.02 cached / $1.20** per
MTok ([OpenAI pricing](https://developers.openai.com/api/docs/pricing),
retrieved 2026-08-14).

| Component                        | Tokens        | Cost    |
|----------------------------------|---------------|---------|
| Uncached input                   | 225,640,964   | $45.13  |
| Cached input                     | 84,478,023    | $1.69   |
| Output                           | 1,895,967     | $2.28   |
| **Total (2-day window)**         |               | **$49.10** |

**~$24.55/day, of which 95.4% is input tokens.**

The same traffic on Claude Opus 5 ($5 / $0.50 cached / $25 per MTok) would be
**$1,217.84 for the two days, ~$609/day**. Every payoff figure in this spec
therefore has a 25× swing depending on which model the default role names. The
ranking between opportunities does not change; the threshold for "worth the
engineering" does.

Cache read share of prompt tokens: 84.5M / 310.1M = **27.2%** overall, but
39% on 2026-08-10 and 10% on 2026-08-11. Caching is working and is highly
variable.

### M7 — The memory compressor issues zero calls today

```
$ sqlite3 ~/.pi-go/memory/claude-mem.db \
    "select 'sessions',count(*) from sessions union all
     select 'observations',count(*) from observations union all
     select 'summaries',count(*) from session_summaries;"
sessions|6278
observations|1
summaries|0
```

One observation, written 2026-08-13T21:08:50Z — during the `specs/memory-fixes`
work itself. The compressor has never run in production because the after-tool
callback chain that feeds it is dead (`specs/memory-fixes/rough-idea.md`).

Any batch-API saving on compression is therefore a saving on **hypothetical
future traffic**, gated behind `specs/memory-fixes` landing first. This is the
single most important caveat on direction 1.
