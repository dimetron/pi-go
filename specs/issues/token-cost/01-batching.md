# Topic 1 — Turns are not batched (48–65% recoverable)

## Research

Cost within a session is quadratic in turn count: every token added at turn *i*
is re-sent on all remaining turns. The lever is **round trips**, not result size.

Measured corpus (1,404 sessions, 36,822 LLM calls, 48,782 tool calls):

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

### Concentration

78 sessions with more than 100 turns hold **49.6% of all prompt tokens**
(1,256,963,127 of 2,536,063,043). Those same 78 sessions made only **61 subagent
calls** between them, against 407 across all shorter sessions — the sessions
that most need work pushed into a child context use that mechanism least.

## Where the change lands

- **Prompt side**: the system instruction already contains a `# Parallel
  execution` section (`internal/agent/agent.go:185-191`) that explicitly tells
  the model it can call multiple independent tools in one response, with
  examples (parallel reads, parallel grep searches). So the instruction is not
  missing — the open question is why models ignore it. The work is to strengthen
  or re-emphasise it, not to add it from scratch.
- **Dispatch side**: ordinary `read`, `bash`, `git-*` function calls emitted by
  the main model are executed by the **ADK runner** configured in
  `internal/agent` (`buildRunner`, `agent.go:368`), not by the subagent
  orchestrator. `internal/subagent/orchestrator.go` manages child-agent
  processes and does not dispatch the main model's tool calls. So verifying
  parallel dispatch means inspecting the ADK runner's tool-execution loop, not
  the orchestrator.

## Recommendations

1. **Investigate why the existing `# Parallel execution` instruction is
   ignored** before adding more prompt text. The instruction already exists
   (`agent.go:185-191`); duplicating it is wasted work. Check whether the
   instruction survives into the actual prompt, whether the model is
   de-prioritising it, and whether the ADK runner even supports parallel tool
   execution in a single turn.
2. **Verify the ADK runner executes a turn's tool calls concurrently.** The
   dispatch point is the ADK runner (`buildRunner`, `agent.go:368`), not the
   subagent orchestrator. If the runner serializes tool calls within a turn, the
   latency win is lost even when the model emits several calls. Confirm before
   investing in prompt work.
3. **Target the 78 heavy sessions first.** They hold half the tokens. A
   per-session batching nudge (or the subagent routing in topic 8) has the
   largest absolute effect there.
4. **Do not batch mutating tools blindly.** `edit`/`write` on the same file are
   order-dependent; only batch calls that are provably independent (read-only
   tools, or edits to disjoint files). The dedup allowlist in
   `internal/tools/dedup.go:34-45` is a good starting model of what is safe to
   treat as independent.

## Expected impact

The 48–65% figure is an **upper bound**, not a guaranteed saving. The replay
regroups all recorded deltas as though later tool calls could have been issued
before seeing earlier results — but sequential calls (repeated `read`, `bash`,
and especially `edit`) often derive their arguments from the preceding output,
so they cannot be batched. The realistic saving is the subset of calls that are
provably independent. Treat 48–65% as the ceiling and measure the independent
subset to get the real number.

## Risk

Low. The main risk is the model batching order-dependent calls; mitigate with
the independence rule in recommendation 4.
