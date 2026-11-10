# Short-context discipline — what "You don't need long context" implies for pi-go

> Status: **research + proposals, not implemented**
> Researched: 2026-11-06
> Source: [arXiv:2611.05306](https://arxiv.org/abs/2611.05306) — *You don't need
> long context*, Peter Belcak et al., NVIDIA, 3 Nov 2026
> Detailed notes: [`research/paper-findings.md`](research/paper-findings.md)

## Why this exists

pi-go already bets on short context. `internal/session/compaction.go` runs a
two-stage shed-then-summarize threshold, and `internal/tools/compactor*.go` caps
results for nine bulk-output tools (`compactorPipelines`, `compactor.go:123`);
any other tool name passes through unchanged. Long inputs are reachable through
`read`/`grep` when the user supplies them as paths — but a long document pasted
into the prompt goes straight into the conversation
(`internal/agent/agent.go:808`), so selective reading is available, not enforced.
None of this was justified by a citation — it was built from Codex parity and
from tool-output compaction work tracked in `AGENTS.md`.

This paper supplies the measurement that was missing, and it disagrees with
pi-go in one specific, cheap-to-fix place: the wording of
`SummarizationPrompt`. That is the actionable finding here. Everything else is
confirmation or a documented gap with a cost attached.

The headline numbers, so the reader does not have to open the paper:

| Claim | Measurement |
|---|---|
| Shrinking the served window 256K → 16K | **3.6×** more decoded tokens/s (GLM 5.2), **5.6×** (Kimi K2.6), **7×** (Qwen 3 Coder Next) on identical H100s |
| On-device (DGX Spark), same range | inter-token latency roughly **doubles** |
| Long-horizon tasks, compacted | accuracy within −3.7…+5.6pp for Claude Opus 4.5, −3.3…+4.4pp for GPT 5.6 Sol — ranges span **all 22 tested configurations**, not one; savings 2.2–8.8× |
| Long-input tasks, selective reading | NIAH 60% → 100%, RULER recall 62.5% → 95.7%, BABILong 67.1% → 92.0%; savings 4.4–233.3× |
| One action per context (SAC) | median peak live context **7–9K**, max 15–37K, down from 130–200K |

## The finding that changes practice

The authors tried the obvious approach first and report it was wrong. Treating
compaction as summarization loses accuracy because a summary gives equal weight
to every fact in the trace, including the parts already resolved. What
outperforms it is *selection for future work*:

> "the best compactions are not those that faithfully capture the gist of the
> agentic trace, but those that best select facts relevant to the work yet to be
> performed — they are not narratives of the execution history but carefully
> cherry-picked briefs for further execution." (Appendix A)

The structure that survived every workload they tested:

1. a **verbatim or near-verbatim copy of the input task**;
2. the agent's current occupation, intentions, and planned next steps;
3. artifacts relevant to *future* execution — a decomposed plan, **a list of
   requirements detailing when to consider the task complete**, and pointers to
   artifacts holding the rest.

**pi-go's prompt is the shape the paper reports as suboptimal.**
`internal/session/compaction.go:141`:

```go
const SummarizationPrompt = `You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.

Include:
- Current progress and key decisions made
- Important context, constraints, or user preferences
- What remains to be done (clear next steps)
- Any critical data, examples, or references needed to continue
- Absolute paths of files read or modified, so the next model does not re-discover them
...
```

**What this does and does not establish.** The paper rejects the *summarization
frame* — "equal weight to all facts", narrative over selection — and pi-go's
prompt is written in that frame ("Current progress", "Important context",
"critical data"). But the paper did **not** run pi-go's prompt, and pi-go's
prompt already asks for next steps and continuation data. So the claim here is a
**hypothesis about four specific missing items**, not a measured regression:

| Paper item | pi-go today | Gap |
|---|---|---|
| Objective as a one-liner | the original task message, retained separately | survives, but not restated in the compaction |
| Intent + planned next steps | "What remains to be done (clear next steps)" | present |
| Decomposed plan | absent | **missing** |
| Completion requirements | absent | **missing** |
| Verification status per change | absent | **missing** |
| Dead ends / failed approaches | absent | **missing** |
| Artifact pointers | "Absolute paths of files read or modified" | present |

The four missing rows are the ones that stop a resumed session from retrying a
failed approach, or declaring success early. That is a testable claim; it is not
an established one.

One caveat the paper's own Appendix G makes clear, and this spec repeats: the
authors' shipped prompt asks for a **one-line objective**, not a verbatim task
copy, because the task is carried separately by the harness. pi-go carries it
separately too (`BuildSummarizedEvents`), so a verbatim copy would duplicate
tokens. See Proposal A for the resolution.

## Where pi-go already complies

| Paper requirement | pi-go mechanism |
|---|---|
| Two-stage thresholds | `ShedPercent: 60`, `SummarizePercent: 90` (`compaction.go:81-89`) |
| Keep user intent and a recent tail | `BuildSummarizedEvents` keeps the initial context and the **newest** user messages within `KeepUserMessageTokens: 20000` (`compaction_shed.go:316`); `KeepRecentEvents: 10`. Note "newest", not "the task" — see the limitation below |
| Make long inputs retrievable | `read` with offset/limit, `grep`, `find`, `ls`, `tree`. The paper reduces every long-input task to a long-horizon one by exactly this move — but pi-go offers this, it does not enforce it |
| Shrink tool results | Nine pipelines in `compactorPipelines` (`compactor.go:123`). The search-family caps preserve `total_matches`/`total_files`/`total_entries` and set `truncated` (`compactor_preservation_test.go`); other tools pass through |
| Threshold as a fraction of a real window | `ctxwindow.Resolve` → `provider.ContextWindowSizeFor` (`ctxwindow.go:27`), overridable by `context_window` in config (`ctxwindow.go:39`) |

### Three things this table overstated before review

1. **"Compact before the window fills" was wrong.** The meter subtracts the
   *entire first request's* prompt tokens as the cached prefix
   (`meter.go:78-87`, `BodyTokens` at `:51-59`), then compares the growth against
   the window. With a 60K first request on a 200K window, summarization at 90%
   body growth fires at **240K total prompt tokens** — past the window. The
   percentages are a growth budget, not a fill budget. This matters for B:
   lowering to 75% triggers at 210K, still over.

2. **"Keep the task" was wrong.** `BuildSummarizedEvents` walks user messages
   newest-first and skips any that do not fit the 20K budget
   (`compaction_shed.go:326-339`). An old task message can be displaced —
   `TestBuildSummarizedEvents_UserMessageBudgetKeepsNewest` asserts exactly that.
   The original *initial context* event is preserved, but that is the system
   block, not the user's task. Any Proposal A that assumes the task survives is
   building on something the code does not guarantee.

3. **`KeepUserMessageTokens` is not a mitigation for verbose trajectories.** It
   bounds retained *user* messages only. It does nothing about assistant output
   length, tool results, or the space left for progress between compactions,
   which is the mechanism the paper blames for the Qwen 3 Coder Next collapse.

## Gaps, and what each one costs

| Gap | Location | Consequence | Evidence |
|---|---|---|---|
| Four compaction items missing | `compaction.go:141` | A resumed session can retry a dead end or declare premature success | Hypothesis from Paper App. A — **unmeasured on pi-go** |
| Summarize threshold at 90% of *body* | `compaction.go:85`; `meter.go:51-59` | With a large first request, the 90% trigger lands **past** the real window (240K on a 200K window at 60K prefix) | Code reading; arithmetic in the compliance section above |
| Comment contradicts code | `compaction.go:66` says "Default 88"; `DefaultAutoCompactConfig` sets 90 | The stated rationale (headroom for the summarization call) was abandoned without the comment following | Code reading |
| No durable history | `rewriteEvents` (`autocompact.go:136` → `store.go:1443`) **overwrites** `events.jsonl` | Everything a compaction drops is gone; no retrieval is possible, not merely unoffered | Code reading + Paper §3.1 |
| Thresholding off without a known window | `Decide` returns `CompactionNone` when `windowSize <= 0` | 62 blanked `agentgateway` models plus `claude-haiku-5-5` and `gpt-6.1-sol` never compact — **unless** `context_window` is set in config | `specs/features/LLM/003-models-api/README.md`; override at `ctxwindow.go:39` |

Two corrections to an earlier draft of this table, both from review:

- The unknown-window case is **not silent** and **not unavoidable**.
  `autocompact/hook.go:83-89` reports it once ("auto-compaction is off; set
  context_window in ~/.pi-go/config.json"), and `ctxwindow.go:39` lets that
  override win. So the models-API work is the *better* fix, not the only one.
- The history gap is worse than "not offered to the model". The pre-compaction
  transcript is **deleted**, so Proposal C cannot be a prompt line pointing at
  `events.jsonl` — the file no longer holds what was dropped.

## Proposals

Three, ordered by evidence per unit of change. None are implemented.

| # | Change | Effort | Reversible | Evidence |
|---|---|---|---|---|
| A | Rewrite `SummarizationPrompt`: add objective, decomposed plan, completion requirements, verification status, dead ends | one constant + one test asserting the sections | trivially | Paper App. A (structure only — the effect is unmeasured here) |
| B | Lower default `SummarizePercent` 90 → 75, fix the stale `88` comment | constant + 3 test cases + `docs/DESIGN-COMPARISON.md` | trivially | **Weak** — see below |
| C | Preserve the pre-compaction transcript durably (write *before* `rewriteEvents`, not a pointer to it) and tell the resuming model it may `grep` it | real plumbing | needs a retention policy | Paper §3.1 |

**Corrections to each, from review:**

- **B's evidence does not survive inspection.** The paper varies the *absolute*
  window (8K–48K) while keeping the ratio near 48K/36K = 75%. That does not
  compare 75% against 90% — every configuration tested sits near 75%, so 90% is
  never measured. It also does not remove the recursive-loss mechanism: a prior
  summary lands inside the next compaction at any threshold. Treat **75 as an
  untested candidate**, not a demonstrated optimum. Changing it also breaks
  `internal/session/compaction_test.go:25-26` (150K and 179K cases assert
  `CompactionShed`; at 75% of a 200K window they become `CompactionSummarize`)
  and the stated defaults in `docs/DESIGN-COMPARISON.md:221,605`.

- **A does not touch when compaction runs**, which is its main attraction, but it
  needs one decision first: the structure above says "verbatim task copy" while
  the authors' shipped prompt (Appendix G) says "Objective — one line" and warns
  *"do not... repeat the full task"*. Since pi-go retains the task message
  separately, **follow Appendix G: one-line objective**, not a verbatim copy. No
  code outside `compaction.go` asserts the current prompt wording
  (`rg SummarizationPrompt` → one definition, one use at `:203`), so A really is
  a constant plus a test.

- **C is the only one that fixes actual data loss**, and it is larger than first
  written: `rewriteEvents` overwrites, so nothing can be retrieved afterwards.

**Recommended order: A, then C, then B.** A is cheap and self-contained. C fixes
a real loss of information. B has the weakest evidence of the three and should
wait for a pi-go measurement rather than a paper citation.

## Where the evidence does not transfer

Stated plainly, because the summary numbers are flattering and these limits
matter more than the averages.

- **Aggressive compaction is not safe for every model.** Qwen 3 Coder Next
  collapses on SWE-bench Verified from **53.0% to 3.6%** at 16K/12K, because
  "its verbose trajectories leave too little room for useful progress between
  compactions" (Table 9). Qwen 3.6 27B (68.6 → 65.0) and MiniMax M2.7 (72.6 →
  67.8) hold up. pi-go drives heterogeneous models through one harness, so a
  threshold tuned for a terse model will hurt a verbose one. **There is no
  existing mitigation for this** — `KeepUserMessageTokens: 20000` caps retained
  *user* messages and does nothing about assistant verbosity or tool-result
  volume, which is the mechanism the paper blames. Verbose-model robustness is an
  evaluation requirement for B, not a property pi-go already has.
- **The one-action-per-context result is a research finding, not a
  recommendation.** SAC loses 2.0 and 3.3pp for GPT 5.6 Sol. The authors present
  it to motivate a hypothetical post-trained model (SSSM), and the SSSM section
  explicitly requires post-training. pi-go cannot adopt that by changing
  inference.
- **Compaction is lossy, and the paper says so.** §3.1 constructs a task where a
  fixed budget provably loses information. The claim is not "compaction is
  harmless"; it is that history should be *retrievable* rather than *resident*.
  Any pi-go change that cites this paper as "compaction costs nothing" is
  misreading it.
- **Percentages are not directly comparable, in two ways.** (a) The paper's
  thresholds are fractions of the *whole* context window; pi-go's `Decide`
  measures `bodyTokens`, which excludes the first request's prompt tokens
  (`meter.go:51-59`). (b) That subtraction is by design — the prefix is more than
  system prompt and tool schemas; it is *whatever the first request cost*. So the
  same 90% can trigger at very different absolute sizes depending on how large
  the first turn was, including a resumed or pre-filled context. Any B
  measurement has to account for both or it will compare two different
  quantities.

## What this document does not claim

- No measurement was run on pi-go. There is no before/after here; the pi-go
  numbers above are all readings of configuration values and arithmetic.
- The paper's savings are GPU cost at fixed hardware, converted to retail price
  by an interpolation the authors describe in Appendix B. pi-go pays per token
  at a retail rate that, as the paper notes, does not move with input length.
  The benefit to pi-go is **peak context headroom and latency**, not a 7× price
  cut.
- No claim that pi-go should support 16K windows. Nothing here suggests lowering
  the window; the argument is about how much of it to occupy at once.
- No claim that pi-go's prompt is the specific prompt the paper tested and
  rejected. The paper rejects the *frame*; pi-go is written in that frame; that
  is the extent of the connection.

## Open questions

1. Does A actually help? The paper's evidence is across 15+ benchmarks with
   frontier models. pi-go's sessions are shorter and tool-heavy. A seeded replay
   of a known-long session would settle it more cheaply than any benchmark.
2. Is the 90% threshold load-bearing for prompt cache? Summarizing invalidates
   the cache (`compaction.go:23-30` already documents this). Lowering to 75
   trades cache hits for smaller compactions, and that trade is unmeasured here.
3. Should C write into the session directory or a temp dir with a retention
   policy? Note the correction: `events.jsonl` does **not** already hold the
   dropped content — `rewriteEvents` overwrites it — so C needs genuinely new
   durable writing, not just a prompt line.
4. Should the task message be pinned rather than budget-competed? The original
   task can currently be displaced by newer user messages that consume the 20K
   budget. Pinning the first user message would be a small, separate fix and
   would also make A's "objective" line redundant.

## Documents

| File | Contents |
|---|---|
| [`research/paper-findings.md`](research/paper-findings.md) | Section-by-section notes, exact figures, harness and regime definitions, the winning compaction structure, per-benchmark tables |

## Review history

Reviewed with Codex CLI (`codex exec -s read-only`, model `gpt-6.1-sol` from
`~/.codex/config.toml`; `gpt-5.4` named in the skill's template is not available
on this account). Twelve findings, all verified against the code before being
accepted; all twelve were correct. None were style nits.

**Corrections made to this document as a result — three were factual errors, not
drafting choices:**

| Was | Now | Why |
|---|---|---|
| "`events.jsonl` is written but never offered to the resuming model" | `rewriteEvents` **overwrites** `events.jsonl`; the content is deleted | This inverted the mechanism. It also invalidates the cheap form of Proposal C |
| "Compact before the window fills" (compliance table) | The meter subtracts the first request wholesale, so 90% of body can land past the window | 60K prefix + 200K window → trigger at 240K. Arithmetic, not opinion |
| "Keep the task and a recent tail" | Newest user messages compete for the 20K budget; the task can be displaced | `TestBuildSummarizedEvents_UserMessageBudgetKeepsNewest` asserts the displacement |
| "`compactor*.go` caps every tool result" | Nine pipelines; other names pass through unchanged | `compactorPipelines` is a map with nine entries |
| "every long input is reachable through read/grep" | Selective reading is available, not enforced; pasted text goes straight in | `agent.go:808` |
| "`KeepUserMessageTokens` is the existing mitigation" for verbose-model collapse | There is no mitigation; that field bounds user messages only | Field semantics |
| "the losing formulation" / "direct before/after" | The paper rejects the *frame*; it did not test pi-go's prompt | Overstated evidence |
| Paper's ~75% "sweet spot" | Untested candidate — every configuration tested sits near 75%, so 90% is never measured | The comparison does not exist in the paper |
| Long-horizon accuracy "compacted to 16K" | Ranges span all 22 configurations | `+5.6pp` is 32K/24K; `+4.4pp` is 48K/36K |
| Long-input savings "6.5–233×" | 4.4–233.3× | Dropped the reported minimum |
| "unknown window is silent / never compacts" | Reported once by `hook.go:83-89`; `context_window` override can enable it | Both halves wrong |
| Opus nominal cost "rises" with compaction | Falls 25.6% / 9.6%; the modeled factor adds the rest | Arithmetic error in the findings doc |

**What survived review unchanged:** the paper's own figures and tables, the
structure of the winning compaction, the Qwen 3 Coder Next collapse and its
cause, the SAC results including the GPT losses, and the recommendation order
(A → C → B). B's evidence weakened under review; C's scope grew.

Two findings were rejected as out of scope: a suggestion to add a
`docs/DESIGN-COMPARISON.md` update for B (correct, but B is not implemented), and
a note that `compaction.go:66`'s stale "88" comment could be fixed independently
(correct, and now recorded as a gap rather than a proposal).
