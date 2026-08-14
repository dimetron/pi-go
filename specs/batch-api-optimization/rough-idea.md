# Rough Idea — Cut LLM cost by removing round trips, not by batching APIs

## Source

A cost analysis of pi-go performed 2026-08-13/14, starting from provider usage
metadata across 2,446 sessions and re-measured here against 400 session logs in
`$HOME/.pi-go/sessions/`.

The question asked was: where can pi-go reduce LLM cost through batching —
provider Batch APIs, or batching tool calls?

The two halves of that question have opposite answers.

## The observation that started it

Across a two-day window, 4,016 model requests consumed **310,118,987 prompt
tokens and 1,895,967 output tokens** — a ratio of 163 to 1. Output is noise.
The entire bill is the input side.

The reflex explanation is "a big system prompt, re-sent every request". The data
says otherwise. Request #1 of a session — system prompt, tool declarations and
the first user message together — has a median size of 9,131 tokens. Request
#100 has a median of **151,357**. The preamble accounts for at most 11.8% of
total prompt spend; the rest is the conversation being re-shipped, growing with
every step.

That changes what is worth fixing. Shrinking the preamble has a ceiling around
12%. Removing a *request* removes a whole copy of everything accumulated so far,
and because the context grows with each step, halving the request count roughly
halves the total — both the linear and the quadratic term divide by the same
factor.

## What we found

**Provider Batch APIs do not apply to pi-go.** Anthropic, OpenAI and Gemini all
offer a clean 50% discount at a 24-hour worst case, and all three Go SDKs are
already in `go.mod`. None of that matters, because pi-go has almost nothing to
put in a batch. Of eight model call sites, five block a user who is watching,
one (`SummarizeSession`) has no production caller, one (palace KG extraction)
turns out to make no LLM call at all *by explicit design*, and the last — the
memory compressor — has issued exactly **one** request in the database's
lifetime, because the callback chain that feeds it is dead.

Costing that one candidate turned up three defects that matter more than
batching ever could: the compressor's `tools: []` declaration is inert, its
`role: smol` silently falls back to the frontier model, and the config key that
appears to control the role is read by nothing. On shipped defaults a repaired
compressor would run `gpt-5.6-sol` once per tool call, in a child process
carrying the full toolset. Fixing that is worth ~40× what a Batch API would
save on top of it — and `specs/memory-fixes` R4 already covers it.

**Batching tool calls is where the money is, and it needs no new machinery.**
ADK already dispatches multiple function calls from one response concurrently.
The providers already return them. 25.7% of responses already carry more than
one call. The mean is 1.307. Collapsing every run of consecutive
single-read-only-call responses would remove **24.5% of all requests**.

The gap between 1.307 and what the code permits is not a capability gap. It is
an instruction gap: `internal/agent/agent.go:185` tells the model it *can*
batch, in a paragraph that gives no reason, 44 lines before a section that tells
it to **prefer** parallel subagents.

## The proof, from a bug that is already fixed

799 of 4,016 responses — 19.9% — did nothing but poll a background shell command
with `bash_output`. They cost **61 million prompt tokens, 19.7% of the window's
entire spend**, and 504 of them were consecutive repeat polls of the same
handle; one session contains a run of 91.

Commit `ffe337a` already fixed this by making the tool block for up to 60
seconds and renaming it `bash_wait`. No session after 2026-08-11 issues a poll.

The measurement is worth keeping anyway, because it is the cleanest available
demonstration of the thesis: 800 round trips that added almost nothing to the
transcript still consumed a fifth of the bill, purely because each one re-sent
everything that came before.

## What this spec covers

1. Make request count a first-class, measured quantity — calls per response and
   requests per session, reported by `pi session-stats`.
2. Rewrite the parallel-execution guidance so the model batches independent
   read-only calls by default, and measure whether it worked.
3. Guard the `bash_wait` fix with a regression test so the poll storm cannot
   come back unnoticed.
4. Record, with numbers, why provider Batch APIs are not worth building — and
   the specific conditions that would change that answer.

## Non-goals

- **Building a Batch API client.** Section D1 of `design.md` shows the payoff is
  $0.63–$2.96/day against a durable job-tracking subsystem. It is a "no", not a
  "later, maybe".
- **A `bash_batch` tool.** Rejected in `design.md` § D3: it would duplicate a
  mechanism ADK already provides and would destroy per-call error attribution to
  do it.
- **Shrinking tool output.** Tool results are a small share of prompt spend and
  the compactor already handles them.
- **Prompt caching for Gemini and Ollama.** A real gap, unquantified here, and
  a different mechanism — caching cuts price per token, not the number of tokens
  sent. Its own spec.
- **Auto-compaction's cache invalidation** (`internal/session/compaction.go:26`).
  Noted, not scheduled.
