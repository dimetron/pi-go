# pi-context-zone 🧠⚠️🧟

ref: https://raw.githubusercontent.com/arpagon/pi-context-zone/refs/heads/main/README.md

**Visual context health bar for the [Pi coding agent](https://github.com/badlogic/pi-mono) — see your smart/warm/dumb
zone at a glance.**

Inspired by [Dex Horthy's "No Vibes Allowed"](https://www.youtube.com/watch?v=rmvDxxNubIg) talk and the concept of *
*context rot** in AI coding agents.

```
🧠 ████░░░░│░░░░░│░░░░░ smart 36% left
```

## Install

```bash
pi install npm:pi-context-zone
```

Or load directly:

```bash
pi -e ./index.ts
```

## What It Does

Adds a single visual status line to your Pi footer showing:

- **Progress bar** with zone dividers at 40% and 70%
- **Color gradient** — green → yellow → red as context fills
- **Zone label** — which zone you're in (smart / warm / dumb)
- **Remaining %** — how much room before the next zone

Updates automatically after each turn, compaction, and session start.

## Why This Matters

### The Context Rot Problem

LLMs don't degrade gracefully as their context fills up — they hit cliffs. This isn't about forgetting a fact buried in
the middle (needle-in-a-haystack); it's about **reasoning quality collapse**. The model starts cutting corners, ignoring
instructions, repeating mistakes, and hallucinating with full confidence.

Dex Horthy (HumanLayer) coined the term **"Dumb Zone"** after analyzing 100,000+ developer sessions. His framework
identifies ~40% context utilization as the inflection point where AI coding agents transition from sharp, capable
assistants to confused, error-prone machines.

### The Zones

| Zone         | Context Used | What Happens                                                                                                                          |
|--------------|--------------|---------------------------------------------------------------------------------------------------------------------------------------|
| 🧠 **Smart** | 0 – 40%      | Peak reasoning. Follows instructions, catches edge cases, accurate tool selection.                                                    |
| ⚠️ **Warm**  | 40 – 70%     | Degrading. F1 scores drop ~45%. Instruction drift, shallow pattern matching, starts relying on pre-training over your actual context. |
| 🧟 **Dumb**  | 70%+         | Broken. Hallucination rates spike to 40%. Infinite debug loops. Confidently wrong. Auto-compaction triggers here but it's lossy.      |

### How Models Actually Perform (March 2026)

The "dumb zone" threshold varies by model. Here's how current frontier models handle long context on the MRCR v2 (
8-needle) benchmark — the gold standard for measuring **reasoning quality** (not just retrieval) across context lengths:

| Model             | Context Window | MRCR @ 128K | MRCR @ 256K | MRCR @ 1M | Smart Zone Ends                         |
|-------------------|----------------|-------------|-------------|-----------|-----------------------------------------|
| Claude Opus 4.6   | 1M             | ~94%        | 93%         | **78%**   | ~70% (most resilient)                   |
| Claude Sonnet 4.6 | 1M             | —           | —           | 65%       | ~50-60%                                 |
| GPT-5.4           | 1M             | 86%         | 79%         | 37%       | ~30-40%                                 |
| Gemini 3.1 Pro    | 2M             | 85%         | ~50%        | 26%       | ~25-30%                                 |
| MiniMax M2.1      | 1M             | ~73%        | —           | ~32%      | ~30-40%                                 |
| Grok 3            | 1M             | —           | —           | —         | ~50% (severe distractor susceptibility) |
| DeepSeek V3       | 128K           | **95%**     | N/A         | N/A       | Near 100% (within its window)           |
| Llama 4 Scout     | 10M            | —           | —           | —         | Unknown (no MRCR published)             |

> **Key insight**: Claude Opus 4.6's Context Compaction architecture genuinely resists context rot better than any other
> model — it maintains 78% reasoning accuracy at 1M tokens where GPT-5.4 drops to 37% and Gemini 3.1 to 26%. But even Opus
> degrades. The 40% rule is conservative and works well as a universal default.

### What Causes Context Rot

1. **Attention dilution** — Transformer attention is a fixed budget. More tokens = less focus per token.
2. **Lost in the middle** — Models remember the beginning and end of context but forget the middle (U-shaped curve).
3. **Trajectory poison** — Your conversation history is full of the model's own mistakes and your corrections. The model
   learns to predict more mistakes.
4. **KV cache compression** — At high utilization, models compress older context, losing the "why" behind decisions.

### What To Do About It

When the bar turns yellow or red:

1. **Compact** — Use `/compact` or let auto-compaction handle it
2. **New session** — Start fresh with a clean context (RPI workflow: Research → Plan → Implement)
3. **Sub-agents** — Delegate heavy exploration to isolated contexts

This extension simply makes the problem **visible** so you can act before quality degrades.

## Configuration

The extension uses sensible defaults based on the research:

| Setting      | Value    | Rationale                                                    |
|--------------|----------|--------------------------------------------------------------|
| Smart → Warm | 40%      | Dex Horthy's inflection point, validated across models       |
| Warm → Dumb  | 70%      | Where hallucination rates spike and auto-compaction triggers |
| Bar width    | 20 chars | Fits comfortably in most terminal widths                     |

These thresholds are intentionally model-agnostic. While Claude Opus 4.6 can push further into the warm zone without
degrading, the 40% threshold is the safe universal default that works across all providers.

## References

- **"No Vibes Allowed: Solving Hard Problems in Complex Codebases"** — Dex Horthy, AI Engineer
  2025 ([YouTube](https://www.youtube.com/watch?v=rmvDxxNubIg))
- **Context Rot: How Increasing Input Tokens Impacts LLM Performance** — Chroma Research, 2025
- **MRCR v2 (8-needle) benchmark** — Multi-Round Coreference Resolution, the gold standard for long-context reasoning
  evaluation
- **"Lost in the Middle"** — Stanford, 2023 — U-shaped retrieval accuracy in long-context LLMs
- **Claude Opus 4.6 technical report** — Anthropic, Feb 2026 — Context Compaction architecture
- **12-Factor Agents** — Dex Horthy / HumanLayer — Framework for building reliable AI agents

## License

MIT