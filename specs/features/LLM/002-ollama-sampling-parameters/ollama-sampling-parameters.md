# Ollama Sampling Parameters

> Source: Ollama API docs — `options` object in the generate/chat request.
> Captured: 2026-08-06

Ollama exposes four parameters that control how the underlying llama.cpp engine
penalizes token repetition: `repeat_penalty`, `repeat_last_n`, `presence_penalty`,
and `frequency_penalty`.

---

## repeat_penalty

- **What it does:** Applies a multiplicative penalty to tokens that have already
  appeared in the recent context, making the model less likely to repeat them.
- **How it works:** For each token already seen in the last `repeat_last_n`
  tokens, its logit is divided by `repeat_penalty`. A value of `1.1` reduces the
  score of repeated tokens by ~10%.
- **Typical range:** `1.0` (off) to `2.0`. Default is `1.1`.
- **Use case:** The main knob for suppressing loops, stutters, and verbatim
  repetition.

## repeat_last_n

- **What it does:** Sets the window of recent tokens that `repeat_penalty` looks at.
- **How it works:** Only tokens within the last `repeat_last_n` tokens are
  considered "recent" and get penalized. Tokens older than that are ignored.
- **Typical range:** `64`–`512`. Default is `64`.
- **Sentinels:** `0` **disables** the penalty entirely; `-1` means "use the whole
  context" (`num_ctx`).
- **Use case:** Balance between stopping short-range repetition (small window)
  and long-range repetition (large window).

> **Correction (2026-08-09).** An earlier revision of this note stated that `0`
> penalises against the whole context. That is inverted. Verified against
> `github.com/ollama/ollama@v0.32.5`:
> `x/mlxrunner/sample/sample.go:225` — *"RepeatLastN == 0 disables the penalty
> ring per the repeat_last_n API contract (0 = disabled), overriding any penalty
> coefficients"* — and `:236` resolves the `-1` sentinel against `num_ctx`.
> Setting `0` to "be safe" therefore turns repetition control **off**, including
> `presence_penalty` and `frequency_penalty`, which share the same history ring.

## presence_penalty

- **What it does:** Adds a fixed penalty to any token that has appeared at all in
  the context, regardless of how many times.
- **How it works:** A constant is subtracted from the logit of any token present
  in the context. It is a flat "you've been used, so you're less likely to be used
  again" penalty.
- **Typical range:** `-2.0` to `2.0`. Default is `0` (off).
- **Use case:** Encourages the model to talk about new topics. Because it is a
  flat penalty, it does not punish *frequency* — just presence.

## frequency_penalty

- **What it does:** Penalizes tokens in proportion to how many times they've appeared.
- **How it works:** The penalty scales with the token's count in the context — the
  more often a token appears, the more its logit is reduced.
- **Typical range:** `-2.0` to `2.0`. Default is `0` (off).
- **Use case:** Discourages the model from overusing common words or phrases,
  pushing toward more varied vocabulary.

---

## How they relate

| Parameter | Penalty type | Scales with count? | Window-limited? |
|---|---|---|---|
| `repeat_penalty` | multiplicative | no (flat per occurrence) | yes (`repeat_last_n`) |
| `repeat_last_n` | window control | — | — |
| `presence_penalty` | additive, flat | no | no |
| `frequency_penalty` | additive, scaled | yes | no |

**Key distinction:** `repeat_penalty` is the llama.cpp-native mechanism
(multiplicative, windowed). `presence_penalty` and `frequency_penalty` are the
OpenAI-style additive penalties that Ollama also exposes — `presence` is a flat
"seen it" penalty, `frequency` scales with how often a token appears.

---

## Practical guidance

- To stop loops/stutters: raise `repeat_penalty` (e.g. `1.1` → `1.3`).
- To force more diverse vocabulary: raise `frequency_penalty` (e.g. `0.5`–`1.0`).
- To push toward new topics: raise `presence_penalty`.
- If you raise `repeat_penalty` but still see long-range repetition, increase
  `repeat_last_n`.

---

## API usage

In Ollama's API these are passed in the `options` object of a request:

```json
{
  "model": "llama3",
  "prompt": "...",
  "options": {
    "repeat_penalty": 1.2,
    "repeat_last_n": 128,
    "presence_penalty": 0.5,
    "frequency_penalty": 0.5
  }
}
```

---

## Implementation in pi-go

**Status:** implemented 2026-08-09 on `feat/ollama-sampling-options`.
**Code:** `internal/provider/ollama.go` (`ollamaSamplingOptions`, `ollamaEnvFloat`,
`ollamaEnvInt`). **Tests:** `internal/provider/ollama_sampling_test.go`.
**User docs:** README → *Ollama generation tuning*.

Before this change pi-go sent exactly one Ollama option, `num_predict`. All four
repetition controls ran at server defaults with no way to tune them.

| Env var | Ollama option | Ollama default |
|---|---|---|
| `PI_OLLAMA_NUM_PREDICT` | `num_predict` | unlimited (pi-go defaults to `16384`) |
| `PI_OLLAMA_REPEAT_PENALTY` | `repeat_penalty` | `1.1` |
| `PI_OLLAMA_REPEAT_LAST_N` | `repeat_last_n` | `64` |
| `PI_OLLAMA_PRESENCE_PENALTY` | `presence_penalty` | `0.0` |
| `PI_OLLAMA_FREQUENCY_PENALTY` | `frequency_penalty` | `0.0` |

### Design decisions

- **Opt-in, no new defaults.** An unset var is omitted from the request entirely
  rather than sent as a zero value, so Ollama's own defaults stay in force and
  behaviour is unchanged for anyone who sets nothing. Picking new defaults would
  change generation quality for every Ollama model, and a value that helps one
  model can degrade another.
- **`0` and `-1` are forwarded, not swallowed.** Both are meaningful to Ollama
  (see the correction above), so "set to 0" must not be indistinguishable from
  "unset".
- **Invalid values are ignored, not fatal.** A typo in an env var should not take
  down a session that would otherwise run. A valid sibling still applies.
- **One request path.** Local Ollama and Ollama Cloud share it, so these apply to
  both. The only local/cloud divergence is `num_ctx`, deliberately not sent for
  cloud models.

### Why this was needed

From a corpus sweep of 496 pi-go session logs (29 days, 25 models), degenerate
"repetition collapse" turns — where a turn stops making progress and restates
one phrase — were concentrated almost entirely in `deepseek-v4-flash` served by
Ollama. Other models peaked at a longest tool-free thinking run of 0–45 events;
DeepSeek reached 164 events / 148 KB.

Measured properties of those turns:

- Repeating units of **89–194 bytes**, roughly **25–55 tokens**.
- Repeated **12–71 times back to back**, byte-exact.
- Up to **80 KB of thinking with zero tool calls** in a single turn.

The default `repeat_last_n` of 64 tokens may not span a full cycle at that
length, so the penalty can fail to see the repetition it exists to suppress.
That is the specific reason `repeat_last_n` is the knob of interest here, and
why `num_predict` alone was insufficient: it bounds how far a degenerate turn
runs, it does not stop the turn degenerating.

Thinking level is *not* a viable lever for this model. Measured on one prompt at
`low`/`medium`/`high`/`max`, within-level variance (703 → 25 436 chars) dwarfed
any between-level difference, so the level does not reliably control reasoning
length for `deepseek-v4-flash`.

### Validation

Method: resume a seed session whose last persisted event is the tool result
immediately before a known spiral, then score the resulting log (see the
`pi-loop-forensics` skill and its `ab_replay.sh`).

Wire format confirmed on a live request:
`"options":{"num_predict":16384,"repeat_last_n":512,"repeat_penalty":1.2}`.

| Configuration | Trials | Looped | Byte-exact `reps` observed |
|---|---|---|---|
| Baseline, ollama-local | 3 | **3/3** | 0, 67, 42 |
| Baseline, ollama-cloud | 3 | **2/3** | 1, 71, 1 |
| `repeat_last_n=512`, `repeat_penalty=1.2` | 3 | **1/3** | 0, 0, 0 |

Per-trial detail for the tuned run (`think_run` = longest tool-free thinking run):

| Trial | Verdict | `think_run` | `reps` |
|---|---|---|---|
| 1 | ok | 3 (1.4 KB) | 0 |
| 2 | LOOP | 57 (23 KB) | 0 |
| 3 | suspect | 8 (4.0 KB) | 0 |

**Interpretation — the penalties do what they are designed to do, and no more.**
Byte-exact repetition was eliminated: `reps` fell to 0 in every tuned trial,
against 67 and 42 in the baseline. But trial 2 still spun for 57 consecutive
thinking events and 23 KB without calling a tool. The model stopped repeating
*verbatim* and carried on repeating *semantically* — paraphrasing itself instead
of copying itself.

Two consequences follow:

1. `repeat_penalty` / `repeat_last_n` are a genuine mitigation for the collapse
   shape that `observeOutput` detects, and appear to reduce the overall loop rate
   (3/3 → 1/3). On n=3 with unpinned sampling that rate difference is suggestive,
   not established; the `reps` → 0 effect is the solid part.
2. They make the failure **harder to detect**. The guard keys on byte-exact
   periodicity, so suppressing verbatim repeats can convert a detectable loop
   into an undetectable one. Any default change here should land together with a
   detector that catches semantic looping — a cap on consecutive thinking events
   with no tool call — or it will trade a visible failure for a silent one.

No default is proposed on this evidence.

### Follow-up

**Done (`feat/shared-stuck-guard`).** The runaway guard was instantiated only in
`internal/tui/agent_loop.go`, so `runPrint`, the json/rpc paths and ACP had no
repetition protection at all — trials with 71, 67 and 42 byte-exact repeats ran
to completion without aborting. `StuckDetector` now lives in
`internal/agent/stuck.go` behind one `ObserveEvent` entry point, and every front
end calls it. ACP also gained a session log, so its runs are analysable by the
tooling above like any other mode.

**Still open.** There is no session-level token cap; the only `TokenBudget` in
the tree belongs to the memory subsystem. And the guard still keys on byte-exact
periodicity, so it does not catch the semantic looping that survived the
penalties in the table above — a cap on consecutive thinking events with no tool
call is the missing arm.
