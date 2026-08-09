# Topic 8 — Dependency order and sequencing

## Research

The eight recommendations in TOKENS.md are not independent. The ordering
constraints that fall out of the code:

1. **Topic 2 (cache reporting) must precede topic 3 (shed gating).** Shedding
   rewrites history mid-prefix and invalidates the cache suffix; on well-cached
   routes it is only profitable when `X × 0.1 × remaining_turns > 0.9 ×
   context_size`. That decision needs observed cache behaviour, which the 73% of
   Ollama-routed traffic currently reports as zero. (Note: the cache fix does
   **not** change the compaction threshold itself — `cachePrefixTokens` is set
   from the first request's `inputTokens`, independent of `cachedTokens` — so it
   is not a prerequisite for threshold tuning, only for shed gating.)
2. **Topic 1 (batching) is independent** of the cache fix; it is a prompt and
   dispatch change. It can proceed in parallel with topic 2.

## Recommended order

| # | Change | Expected | Risk | Depends on |
|---|---|---|---|---|
| 1 | Populate `CachedContentTokenCount` on the Ollama path; log when a route reports nothing | unblocks shed gating (3) | low–med | — |
| 2 | Raise tools-per-turn (prompt + parallel dispatch) | **48–65%** (upper bound; see 01) | low | — |
| 3 | Make `shed` continuous, gated on observed cache behaviour | 6.8%, up to 37% on heavy sessions | medium | 1 |
| 4 | Fix compactor git tool names | unblocks 3 dead stages | trivial | — |
| 5 | Cap `read` on source files at 2,000 lines; wire `FileContentCache` + an unchanged-file pointer | ~45% of resend debt | low | — |
| 6 | Replace `smartTruncate` for `read` with contiguous head/tail | correctness | low | — |
| 7 | Call `memoryInstructionContext` from `interactive.go`; call the session summarizer at exit | recall quality | low | — |
| 8 | Route long sessions (>60 turns) to subagents | attacks the 49.6% | medium | 1 (for accurate context-pressure gating) |

## Sequencing rationale

- **1 first, always.** It is low-to-medium risk (not a one-line fix — see 02),
  and it unblocks the shed gating in 3. It does **not** gate threshold tuning,
  which is independent of the cache gap.
- **2 and 3 are the big wins.** 2 is independent of 1 and can start immediately;
  3 depends on 1 for its cache gate. They are independent of each other.
- **4, 5, 6 are independent bug fixes** with no ordering constraint; they can be
  done in any order, ideally before the threshold tuning in 3 so the compactor
  and `read` behave correctly first.
- **7 (memory) is orthogonal** to the token-cost levers; it is a recall-quality
  improvement that can land whenever.
- **8 (subagents) benefits from 1** so its context-pressure gate is accurate,
  but can be prototyped on a turn-count heuristic immediately.

## Verification at each step

Use the `pi tokens` replay (topic 7) after each change to confirm the 201:1 ratio
moves in the expected direction and no regression is introduced. The regression
fixtures (topic 7) make this a CI number rather than a manual check.
