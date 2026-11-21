# Paper findings — *You don't need long context*

**Source:** [arXiv:2611.05306](https://arxiv.org/abs/2611.05306) v1, 3 Nov 2026 (cs.AI).
Peter Belcak, Saurav Muralidharan, Jindong Jiang, Yonggan Fu, Wonmin Byeon,
Hanrong Ye, Ryan Angilly, Ali Taghibakhshi, Shih-Yang Liu, Siru Ouyang,
Greg Heinrich, Pavlo Molchanov — NVIDIA.

Notes are organised by section so a reader can go straight to the part a claim
came from. Figures and table numbers match the paper.

## Abstract claim

Decode cost is linear in context depth; on frontier open-weight models the
disparity between early and late tokens reaches **7×**. Frequent compaction
sacrifices "little to no task accuracy" on long-horizon and long-input tasks.
Performance does not collapse on long-horizon SWE tasks even at **one action per
context**, which motivates *semantic state space models* (SSSMs).

## 1. Introduction

### The cost curve (Figure 1)

Dollar cost of generating 1M output tokens on an existing KV cache, as a
function of context window size, converting measured peak decode throughput at
on-demand H100 rates ($3.85/GPU-hour, Nebius, Sept 2026):

| Model | Serving | 256K vs 16K cost |
|---|---|---|
| GLM 5.2 | INT4 weights, BF16 activations | **3.6×** |
| Kimi K2.6 | INT4 weights, BF16 activations | **5.6×** |
| Qwen 3 Coder Next | BF16 | **7×** |

The authors' framing: this reduction "rivals or exceeds that of many other
orthogonal optimization methods such as aggressive quantization or structured
sparsification." It is invisible to users because retail pricing is a flat rate
per 1M tokens regardless of input length — a point that matters for pi-go, which
pays retail (see the README's "What this document does not claim").

### The latency curve (Figure 2)

Token generation latency, throughput and memory footprint of locally-run coding
models on an **NVIDIA DGX Spark** (128GB unified memory, ~109GB available for
serving). For several models, inter-token latency approximately **doubles**
across the measured context lengths.

### Table 1 — context windows exceed literature

Comparing literary works against model context windows, at ~1.33 tokens per
English word:

| Work | Tokens | Model | Tokens |
|---|---|---|---|
| The Old Man and the Sea | ~37K | MiniMax M2.7 | 200K |
| The Great Gatsby | ~64K | Claude Opus 4.5 | 200K |
| Qur'an (Saheeh Int'l) | ~207K | Gemma 4 31B | 256K |
| King James New Testament | ~241K | Kimi K2.6 | 256K |
| Great Expectations | ~245K | Qwen 3 Coder Next | 262K |
| The Lord of the Rings | ~640K | Gemini 3.8 Flash | 1M |
| War and Peace | ~753K | GLM 5.2 | 1M |
| King James Old Testament | ~802K | GPT 5.6 Sol | 1.05M |

The rhetorical point: a 16K–50K window already holds an elaborate literary work,
so "long context" is not about comprehension capacity.

### Research questions

- **RQ0** Are long contexts necessary?
- **RQ1** Can long-horizon tasks be completed without long contexts?
- **RQ2** Can long-input tasks be completed without long contexts?
- **RQ3** Can long-horizon tasks be completed taking no more than a single
  action per context?

Workload taxonomy: **LCW1** long-horizon (context builds as the agent acts);
**LCW2** long-input (a large body of information must be operated across). The
key observation is that "the tasks in LCW2 can always be reduced to LCW1 by
storing the long input as a resource and equipping the LM with a tool for
selective input reading" — precisely what a `read`/`grep` toolbelt does.

## 2. Methodology

### Definitions worth keeping (§2.1)

- **Inference step** — one LM invocation; in agentic use, one agent turn.
- **Input context** — everything made available for the step: system
  instructions, task, prior outputs, tool results, history.
- **Output context** — everything generated in the step.
- **Context window size** — max tokens the *endpoint* processes per invocation,
  whether architectural or narrowed by serving configuration. (Relevant to
  pi-go: the agentgateway path is narrowed by the gateway, not the model.)
- **Long context** — a significant fraction of a frontier window, ~256K+.
  A *long-context regime* is any strategy that permits growth toward that scale.
- **Short context** — "much closer to zero than to the context window size";
  in practice **~16K–50K**.
- **Long-input task** — a task accompanied by a large input, irrespective of
  whether a harness places it all in context or exposes it for selective reading.
- **Long-horizon task** — completion requires many sequential actions, later ones
  depending on earlier observations.

### Benchmarks (§2.2)

**Long-horizon (LCW1)** — 6 benchmarks:

| Benchmark | Size | What it tests |
|---|---|---|
| SWE-bench Verified | 500 human-validated GitHub issues | patch passes held-out tests |
| SWE-bench Multilingual | 300 issues, 9 languages | cross-ecosystem repo work |
| Terminal-Bench 2.1 | 89 sandboxed CLI tasks | end-to-end terminal problem-solving |
| FRAMES | 824 multi-hop questions over 2–15 Wikipedia articles | iterative retrieval, evidence integration |
| τ2-bench | 184-task golden set (retail, airline, telecom) | dialogue + policy-constrained tool calls, final env state |
| BFCLv3 | 200 long-context episodes | multi-turn tool use, state-changing calls |

**Long-input (LCW2)** — 7 benchmarks:

| Benchmark | Size | Notes |
|---|---|---|
| LongBench v2 | 503 MCQ, 8K–~2M words | 6 task families incl. code-repo understanding |
| LOONG | 1,600 questions | multi-doc over financial/legal/academic papers, EN + ZH |
| AA-LCR | 100 questions | ~100K tokens of real documents, 7 categories |
| NIAH | 125 instances | insertion depth × doc length, 16K–~500K |
| RULER | 13 tasks at 256K and 500K | needle variants, variable tracking, word extraction, multi-hop |
| Counting-Stars | full 192-question set | 32 marked numbers, 4K–128K, reasoning variant has corrected counts |
| BABILong | 550 questions | bAbI facts in book text, 64K–1M, 10 task types |

Even the needle benchmarks "often do not feature a clear lexical overlap aiding
easy retrieval" — they require aggregation, variable tracking, or multi-fact
reasoning.

### Models (§2.3)

**Closed-weight workers:** Claude Opus 4.5, GPT 5.6 Sol — chosen from
independently developed families "to reduce the risk that an observed effect of
compaction is specific to one provider or post-training recipe."

**Open-weight, cloud throughput study:** GLM 5.2 (≤1M), Kimi K2.6 (≤256K),
Qwen 3 Coder Next (≤256K).

**Open-weight, local (DGX Spark, NVFP4 or GGUF 3–4 bit):**

| Model | Params | Attention |
|---|---|---|
| Qwen 3.6 27B | 27B dense | 3:1 Gated DeltaNet / gated full attention |
| Qwen 3 Coder Next | 80B total, 3B active MoE | same hybrid pattern |
| Gemma 4 31B | 31B dense | 1024-token sliding window interleaved with global |
| Step 3.7 Flash | 198B total, 11B active MoE | 512-token sliding / full in 3:1, head-wise gated GQA |
| MiniMax M2.7 | 230B total, 10B active MoE | full-softmax GQA |

Deliberately diverse attention mechanisms, "to test the sufficiency of short
context claimed in RQ1 across models with different attention mechanisms."

### Harnesses (§2.4)

Both are minimal ReAct loops: task in the initial user message, one Bash action
at a time, action + result appended.

- **mini-swe-agent** (released, v2.4) — SWE and terminal tasks.
- **mini-agent** — their generalisation, benchmark-specific instructions,
  environments, submission rules; a benchmark adapter recognises the submission
  command, extracts the payload, terminates the episode.

### Three context-management regimes (§2.4.1, Figure 3)

| Regime | Behaviour |
|---|---|
| **FHA** — Full-History Accumulation | append everything to one conversation; the baseline; default in both harnesses |
| **EoCC** — End-of-Conversation Compaction | run full history until a threshold, then instruct an in-conversation bounded compaction; start a fresh conversation with the original task + compacted state; repeat |
| **SAC** — Single-Action Compaction | carry a compact semantic state and no history; one action per conversation, then update state and discard |

Thresholds are written **total context limit / compaction threshold**. A higher
total limit reserves space for the compaction turn.

Length measures: **total length** (for EoCC/SAC, the sum of final lengths of all
conversations) and **peak length** (the largest single conversation).

### Long-input handling (§2.4.2)

Each long input is stored as one or more files (`input01.txt`, …) in the agent's
filesystem; the task message identifies them; the agent uses `sed`, `awk`, `head`
via Bash. "This method ... makes selective input reading an explicit environment
action and thereby reduces a long-input workload to a long-horizon workload."

### Serving measurements (§2.5)

- Cloud: smallest H100 node that holds the checkpoint plus KV cache at the
  maximum window (8 H100s for Qwen 3 Coder Next, 16 for GLM 5.2 and Kimi K2.6);
  request lengths drawn from nested prefixes; prefix caching enabled; every
  request generates exactly 1,024 tokens; sweep concurrency, report sustained
  aggregate decode throughput without KV-cache preemption. Repeated measurements
  varied by **≤2%**.
- Local: one instance, one request; measure mean inter-token latency over the
  next 1,024 tokens and total device memory.
- **Figure 4** re-expresses the same curve as speedup vs the 256K operating
  point: 3.6× (GLM), 5.6× (Kimi), 7× (Qwen) at 16K.

## 3. RQ1 — long-horizon tasks do not need long contexts

**Answer: yes.** EoCC on 6 benchmarks vs FHA baselines, thresholds tightened
progressively:

| Benchmark | Total limit / threshold settings |
|---|---|
| SWE-bench Verified, TB-2.1, SWE-bench Multilingual, FRAMES | 48K/36K, 32K/24K, 24K/18K, 16K/12K |
| BFCLv3 | 24K/18K, 16K/12K, 12K/8K |
| τ2-bench | 16K/12K, 12K/8K, 8K/6K |

**Figure 5** results:

- Opus 4.5 relative accuracy: **−3.7 … +5.6pp**.
- GPT 5.6 Sol relative accuracy: **−3.3 … +4.4pp**.
- For either model, **no more than 5 of 22** threshold configurations lose more
  than 2pp.
- Estimated cost savings: **2.2×–8.8×** (Opus), **2.4×–5.0×** (GPT).

Per-benchmark accuracy (Appendix C, Tables 3–8):

| Benchmark | Opus FHA → best | GPT FHA → best |
|---|---|---|
| SWE-bench Verified | 70.0 → 74.8 (24K/18K) | 84.4 → 88.8 (48K/36K) |
| SWE-bench Multilingual | 66.3 → 68.0 (48K/36K) | 79.3 → 80.0 (48K/36K) |
| Terminal-Bench 2.1 | 47.2 → 52.8 (32K/24K) | 87.6 → 88.8 (48K/36K) |
| FRAMES | 81.0 → 79.7 (48K/36K) | 90.2 → 88.7 (48K/36K) |
| BFCLv3 | 62.0 → 64.0 (24K/18K) | 39.5 → 39.5 (12K/8K) |
| τ2-bench | 79.3 → 79.3 (16K/12K) | 73.9 → 77.2 (12K/8K) |

Notable: **FRAMES is the only long-horizon benchmark where compaction costs
accuracy for both models** (1.3–3.7pp Opus, 1.5–3.3pp GPT). It is also the one
whose evidence is most dispersed across many documents, which is consistent with
a compaction dropping a retrieved passage before it is used.

Token-spend patterns they report as consistent: new input tokens increase as the
window shrinks, cached tokens decrease, output tokens are generally elevated.

### 3.1 The honest counterexample

This is the section to cite against over-claiming.

> "Compaction is a lossy context compression method. Information will be lost as
> a consequence of compacting a conversational history. Even more information
> will be lost if the conversational history being compacted already contains a
> result of an earlier compaction."

Their constructive failure case: an agent with a coin-toss tool. With a fixed
compaction budget it must eventually lose some outcomes; if the task later asks
for the *i*-th outcome, it fails, and the compaction is the cause.

Their resolution is **not** a better summary. It is to "create a harness that
keeps a record of every message of any agentic conversation, and makes it readily
accessible to the agent by the means of a selective reading tool" — which turns
querying history into a long-input task, covered by RQ2.

The finding, stated precisely: "you don't need to keep the entire conversational
history in the context in order to complete long-horizon tasks" — not "you never
need to access conversational history."

## 4. RQ2 — long-input tasks do not need long contexts

**Answer: yes.** Selective input reading + compaction vs full-input baselines,
7 benchmarks (**Figure 6**, Appendix F Tables 11–17):

| Benchmark | Opus Δ | GPT Δ | Savings (Opus / GPT) |
|---|---|---|---|
| LongBench v2 (16K/12K) | −0.4pp | **+1.8pp** | 7.4× / 4.4× |
| LOONG (avg) | **+10.1pp** | **+9.7pp** | up to 13.6× / 17.4× |
| AA-LCR | **+19.0pp** (65.0→84.0) | +1.0pp | up to 9.8× / 16.2× |
| NIAH (48K/36K) | **+40.0pp** (60→100) | **+20.0pp** (80→100) | 54.6× / 233.3× |
| RULER recall | **+33.2pp** (62.5→95.7) | **+30.9pp** (65.5→96.4) | 42.8× / 80.5× |
| Counting-Stars exact | **+16.1pp** | **+4.6pp** | 25.9× / 40.7× |
| BABILong | **+24.9pp** | **+23.3pp** | 31.5× / 88.0× |

Aggregate: Opus **−0.4 … +40.0pp**, GPT **+1.0 … +30.9pp**; savings **6.5×–54.6×**
(Opus) and **4.4×–233.3×** (GPT).

Their explanation for why selective reading often *improves* on full input: the
baseline pastes a large document and the model must find a few relevant passages
in it; the agent instead greps, so the relevant text arrives near the end of a
short context.

Also worth noting for honesty: **NIAH reaches 100% for both models and neither
performs a single compaction.** The authors attribute it to lexical overlap
between needles and the query terms. Selective reading does nearly all the work;
compaction is the fallback.

Conversation-length evidence (Figures 7, 8): plotted with log document length
against linear conversation length, LongBench v2 conversations stay in a
20K–100K band across documents spanning 10⁴–10⁶ tokens, and LOONG stays in
0–350K across 10⁴–10⁵ document tokens. The `y = x` reference "rises
exponentially in these plotting coordinates, whereas most conversations do not
grow proportionally with document length" — conversation length is broadly
bounded as input grows.

Their conclusion: "the size of a task's input does not itself establish a need
for long context."

## 5. RQ3 — one action per context

**Answer: yes, with a caveat for GPT.**

**Table 2**, mini-swe-agent baseline vs best EoCC vs SAC:

| Model | Benchmark | FHA | Best EoCC | SAC | FHA peak (med/max) | SAC peak (med/max) |
|---|---|---|---|---|---|---|
| Opus 4.5 | SWE-bench Verified | 70.0 | 74.8 (24K/18K) | **70.2** | 32K / 200K | 8K / 16K |
| Opus 4.5 | Terminal-Bench 2.1 | 47.2 | 52.8 (32K/24K) | **51.8** | 27K / 130K | 9K / 19K |
| GPT 5.6 Sol | SWE-bench Verified | 84.4 | 88.8 (48K/36K) | **82.4** | 18K / 173K | 7K / 15K |
| GPT 5.6 Sol | Terminal-Bench 2.1 | 87.6 | 88.8 (48K/36K) | **84.3** | 17K / 133K | 8K / 37K |

So: SAC preserves or improves Opus; GPT incurs 2.0 and 3.3pp losses. Median peak
live context under SAC is **7–9K tokens**, with maxima 15–37K instead of
130–200K.

**The caveat is in the numbers, and the paper states it plainly.**

### 5.1 Semantic State Space Models (SSSMs)

Proposed model type, post-trained to:

1. comprehend a natural-language description of a conversation's state;
2. reason about the next action / the task;
3. take an action;
4. register its outcome;
5. update the language description of state — conclusions, decisions, outcomes —
   so a *separate inference instance* can continue toward completion.

The contrast with a State-Space Model is the point: an SSM keeps a black-box
neural state between token generations; an SSSM keeps an **explainable natural
language** ("semantic") state between turns.

Claimed benefits: token efficiency (Figure 1), and data efficiency — no longer
teaching the model "to pay attention to the suitable patches of earlier context."
Their closing question: "Why train full-history-accumulating models in the first
place?"

This is a research direction requiring post-training. pi-go cannot adopt it by
changing inference behaviour, and the README records that.

## 6. Conclusion

Affirmative answers to RQ1–RQ3, therefore to RQ0: long contexts are not
necessary for the completion of "long-context" tasks. They connect it to
computability: anything computable can be computed "by comprehending only a
bounded amount of input at a time and maintaining only a finite state of mind."

Their own scope limits, quoted because they matter:

> "Our evidence does not rule out the existence of tasks for which long context
> is beneficial, nor does it prove that every possible task can be solved with
> short context. It does, however, show that the standard arguments in favor of
> long contexts stemming from long input and long conversational horizons are
> insufficient."

## Appendix A — what makes a good compaction

The most directly actionable part for pi-go.

They report that summarization is suboptimal: "LM summarizations generally
attempt to give equal weight to all facts present in the input and are prone to
overemphasize repeated segments of the agent trace regardless of their ultimate
relevance to the tail of the trace being compacted."

They explored two axes — method (summarization vs message pruning) and what is
pruned (whole trace vs only the segment since the last compaction plus that
compaction's contents).

**Finding:** "the best compactions are not those that faithfully capture the gist
of the agentic trace, but those that best select facts relevant to the work yet
to be performed — i.e., they are not narratives of the execution history but
carefully cherry-picked briefs for further execution."

The structure, which preserved accuracy at every compaction frequency and
transferred across all workload types:

1. **Verbatim or near-verbatim copy of the input task.**
2. Agent's **current occupation, intentions, and planned next steps.**
3. All artifacts of direct relevance to future execution:
   - **(3a)** a decomposed plan made from the input task;
   - **(3b)** **a list of requirements detailing when to consider the task
     complete**;
   - information or pointers to artifacts holding what is relevant to (3a)/(3b).

**Reconcile this with their shipped prompt before acting on it.** Item 1 says
"verbatim copy of the input task", but the prompt in Appendix G asks for
*"Objective — one line describing the required result"* and the mini-agent
variant adds *"Do not invent observations or repeat the full task"*. The two are
not contradictory once the harness is accounted for: both harnesses re-supply the
task in the new conversation's user message, so a verbatim copy inside the
compaction would duplicate it. Item 1 is a statement about what the compaction
must not lose, satisfied by the harness carrying the task **plus** the one-line
objective. Any implementation that already retains the task message should follow
Appendix G's one-liner, not a copy.

They also flag the human-in-the-loop complication: compactions for agent-assisted
SWE must account not only for autonomous progress but for "all potential
information that might be relevant to follow-up autonomous work that might be
triggered as a result of the human's feedback."

### Their actual compaction prompts (Appendix G)

**mini-swe-agent** — user message once the threshold is met:

> Stop work. Write only a concise handoff recap; do not run commands or call
> tools. The conversation will reset to the system prompt, task, and this recap.
> Record: 1. Objective — one line describing the required result. 2. Established
> facts — exact paths, relevant symbols or commands, observed values, and
> important errors/output. Separate observations from unverified ideas.
> 3. Applied changes — each changed/created file, what changed, and whether
> verified. 4. Verification — exact checks run, outcomes, failures, obstacles,
> and workarounds. 5. Dead ends — failed approaches that should not be repeated.
> 6. Remaining plan — outstanding subgoals, in execution order; the immediate
> next command or edit; and the condition for completion.

**mini-agent** — same shape, with "Current progress and best answer/result,
separating verified facts from assumptions" and "What was solved, what worked,
what remains unresolved, what failed, and useful commands already tried," plus:
"Do not invent observations or repeat the full task or document manifest."

Comparing this to pi-go's `SummarizationPrompt` (`compaction.go:141`): pi-go has
"current progress", "what remains to be done", and "absolute paths". It lacks
**objective stated as a one-liner**, **verification status per change**, **dead
ends**, and **the completion condition**. Those four are exactly what prevents a
resumed session from retrying a failed approach or declaring premature success.

## Appendix B — the cost model

Stated here so the savings multiples can be read as estimates, not retail prices.

For measured model M, `T_M(W)` is peak aggregate decode throughput at window W;
fixed GPU allocation implies

```
r_M = C(16K)/C(256K) = T(256K)/T(16K)
    = 0.2772 (GLM 5.2), 0.1777 (Kimi K2.6), 0.1439 (Qwen 3 Coder Next)
```

They take the rounded mean **r = 0.20**. Reference windows: 200,000 for Opus 4.5,
272K for GPT 5.6 Sol (its standard-price ceiling; the model supports 1.05M but
experiments never exceeded the ceiling).

Output price anchors at standard retail ($25 Opus, $20 GPT), interpolated to
`r ×` standard at 16K, clamped below. Input and cached prices are derived by
preserving the models' 5:1 output-to-uncached-input and 10:1
uncached-input-to-cached-input ratios. Total cost is
`I·C_input + H·C_cached + O·C_output`.

Two consequences for reading the paper:

- The multiples are **optimized-retail estimates under a modelled pricing
  scheme**, not observed bills. The paper's tables distinguish "Nominal retail
  cost" (published prices × measured tokens) from "Estimated optimized retail
  cost" (the same tokens with the context factor applied). On SWE-bench Verified
  the nominal cost *also* falls, but by much less than the estimated optimized
  cost: Opus $420.00 baseline → $312.65 at 16K/12K (**−25.6%**) and → $379.85 at
  48K/36K (**−9.6%**), against estimated optimized $420.00 → $62.53 (**−85.1%**).
  So real token savings account for part of the reduction and the pricing model
  supplies the rest. Do not attribute the whole multiple to the model.
- More compaction does not mean fewer input tokens. Opus SWE-bench Verified new
  input tokens go **20.0M → 28.3M** as the window tightens while output stays
  roughly flat (4.4M → 4.3M). The saving is concentrated in cached tokens
  (**420M → 127M**) — which is why the nominal cost still falls despite more new
  input, and why the paper's cached-token price ratio of 10:1 does most of the
  work.

## Appendix D — open-weight results, and where compaction breaks

**SWE-bench Verified** (500 instances, % resolved; patch-application failures
count unresolved):

| Model | Baseline | 48K/40K | 32K/24K | 16K/12K |
|---|---|---|---|---|
| Qwen 3.6 27B | 68.6 | 69.4 | 68.2 | 65.0 |
| **Qwen 3 Coder Next** | **53.0** | 49.2 | 40.4 | **3.6** |
| Gemma 4 31B | 65.2 | 65.0 | 65.8 | 58.0 |
| Step 3.7 Flash | 70.0 | 66.8 | 64.6 | 56.6 |
| MiniMax M2.7 | 72.6 | 73.0 | 73.2 | 67.8 |

**Terminal-Bench 2.1** (89 tasks, % solved):

| Model | Baseline | 48K/40K | 32K/24K | 16K/12K |
|---|---|---|---|---|
| Qwen 3.6 27B | 43.8 | 48.3 | 49.4 | 43.8 |
| Qwen 3 Coder Next | 29.2 | 34.8 | 34.8 | 30.3 |
| Gemma 4 31B | 40.4 | 34.8 | 29.2 | 33.7 |
| Step 3.7 Flash | 41.6 | 32.6 | 34.8 | 28.1 |
| MiniMax M2.7 | 40.4 | 37.1 | 38.2 | 40.4 |

The Qwen 3 Coder Next collapse (53.0 → 3.6) is attributed to "its verbose
trajectories leave too little room for useful progress between compactions."
Robustness is model-dependent; Qwen 3.6 27B and MiniMax M2.7 are robust, Gemma 4
and Step 3.7 Flash lose steadily.

This is the table to cite against any proposal to lower pi-go's threshold for all
models uniformly.

## Appendix E — selective reading instructions

The instructions given to the agent are worth reading as a design reference for
pi-go's own guidance:

- `doc-read [--start CHAR] [--chars N] [--lines A:B]` — bounded window, default
  6000 chars, hard-capped at 8000; the header tells you the command to read the
  next contiguous window.
- `grep -n -i -E '<regex>'` to locate, then `doc-read` around the hits —
  "usually far faster than reading straight through."
- Explicit preference: "IMPORTANT — read only what you need. Keep each read small
  (<= ~6000 chars). Do NOT try to dump the whole document. Search for the
  entities/claims each option hinges on, read the surrounding passages, and
  verify."

Recorded traces show the pattern: `grep` for a name → line number → 36-line read;
`grep -rn` across two docs → 1K-char read to verify a title; successive greps
narrowing to one passage containing names, counts and time span.

pi-go's `read` (offset/limit) + `grep` support the same discipline; the difference
is that pi-go's guidance does not state a byte budget per read.

## Numbers I could not verify

- The paper's per-benchmark tables are reproduced above from the text; no
  independent run was done and none is claimed.
- The DGX Spark memory figure (~109GB available of 128GB unified) is stated in
  §2.3; I did not verify hardware behaviour.
- The pricing inputs in Appendix B are the authors' as of 2026-09-29, including a
  GPT 5.6 Sol 272K standard-price ceiling. Whether that matches pi-go's
  `provider.CostFor` table today is unchecked — the models-API spec
  (`specs/features/LLM/003-models-api/`) is the place that would answer it.
