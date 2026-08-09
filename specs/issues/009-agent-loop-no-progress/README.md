# Issue 009: Agent Turns That Make No Progress — Repetition Collapse and Semantic Looping

## Summary

An agent turn can stop making progress while still producing output: the model reasons,
restates, and never calls a tool. Nothing new enters the loop, so it circles. Left unbounded
this burns tokens quadratically (every turn resends the whole conversation), and until
recently it ran unguarded on four of pi-go's five front ends.

Three distinct failure shapes exist, and they need different guards:

| Shape | What it looks like | Caught by |
|---|---|---|
| **Identical tool calls** | Same call, same args, same result, repeatedly | `Observe` (10) |
| **Tool error streak** | Same tool failing over and over | `ObserveError` (10) |
| **Repetition collapse** | Output degenerates into a byte-exact repeating phrase | `ObserveOutput` (12) |
| **Semantic looping** | Model paraphrases itself forever, calls nothing | **nothing, until 009** |

The fourth is the dangerous one: it precedes the third, produces no byte-exact signature,
and is made *more* likely by the usual mitigation for the third.

## Incident

**2026-08-09**, session `260809-1229-ecff9-4280b`, model `deepseek-v4-flash:0731:cloud`.

A run aborted with:

```
agent loop aborted: model repeated a 89-character phrase 12 times
```

The unit was exactly 89 bytes, repeated 14 times, byte-exact:

```
".\n\nLet me do it.\n\nLet me write the test program.\n\nLet me do it.\n\nLet me write the program"
```

Before that collapse: **68 consecutive thinking events, ~45 KB, zero tool calls.** The model
spent them relitigating one design decision, restating the same trade-off roughly twenty times
without committing. The tell is the ratio — **130 "let me write/run/test" phrases against 43
actual tool calls**. It kept announcing actions it never took.

### Corpus scale

A sweep of 496 sessions across 29 days and 25 models (`pi-loop-forensics` skill,
`scan_logs.py`):

- Longest tool-free thinking run, healthy models: claude-sonnet **7**, minimax-m3 **45**
  (but with 37 tool calls that session — legitimate deep reasoning).
- Degenerate runs, all `deepseek-v4-flash` on Ollama: **21, 34, 38, 57, 68, 78, 87, 89, 122,
  164** events (10 KB–148 KB).
- One turn emitted **148 KB of the same sentence over 87 seconds**
  (`internal/provider/ollama.go`, `defaultOllamaNumPredict` doc comment).

**The ranges overlap.** No threshold cleanly separates deep reasoning from a loop.

## Root cause

Three hypotheses were tested and eliminated before concluding it is model behaviour:

- **Race / double-emission** — ruled out. 415 thinking payloads hashed: **zero exact
  duplicates**, varied lengths, monotonic timestamps ~500 ms apart. Genuine streamed output,
  not pi-go re-emitting chunks.
- **Tool-parse failure** — ruled out. Zero occurrences of `<tool_call`, `<function`,
  `tool_calls`, `"name":…,"arguments"`, ` ```json `, `<think>`, `<｜tool` in either the
  thinking or text channel. Nothing was emitted-and-dropped; the model never called a tool.
- **Threshold too low** — ruled out. Healthy sessions peak at ~1 periodic repeat against a
  threshold of 12 byte-exact copies.

It is inference-level repetition collapse. Reproduced on demand by resuming a seed session
whose last persisted event is the tool result immediately before the spiral: **3/3 on
ollama-local, 2/3 on ollama-cloud**.

Thinking level is not the lever. Measured across `low`/`medium`/`high`/`max` on one prompt,
within-level variance (703 → 25 436 chars) dwarfed any between-level difference.

## Guardrails

Four layers. No single one is sufficient, and two of them interact badly — that interaction
is the most important thing on this page.

### 1. Prevent — make the model less likely to collapse

`internal/provider/ollama.go`, opt-in via env (see
`specs/features/LLM/002-ollama-sampling-parameters`):

| Env var | Option | Ollama default |
|---|---|---|
| `PI_OLLAMA_REPEAT_PENALTY` | `repeat_penalty` | 1.1 |
| `PI_OLLAMA_REPEAT_LAST_N` | `repeat_last_n` | 64 |
| `PI_OLLAMA_PRESENCE_PENALTY` | `presence_penalty` | 0.0 |
| `PI_OLLAMA_FREQUENCY_PENALTY` | `frequency_penalty` | 0.0 |

The default penalty window is 64 tokens; observed cycles run 25–55 tokens, so a full cycle can
fall outside what the penalty can see. Widening `repeat_last_n` targets that directly.

> **⚠ This layer degrades layer 3.** Measured: with `repeat_last_n=512, repeat_penalty=1.2`,
> byte-exact repetition was **eliminated** (`reps` 67 and 42 → 0 in every trial) — but one
> trial still spun **57 thinking events with no tool call**. Penalties work by pushing the
> model off tokens it just used, which is exactly how verbatim repetition becomes paraphrase.
> They convert a *detectable* loop into an *undetectable* one. **Never raise these without
> the semantic-loop arm in place.**

### 2. Bound — cap the blast radius

- `defaultOllamaNumPredict = 16384` (`internal/provider/ollama.go:176`) caps **one turn's**
  output. Override with `PI_OLLAMA_NUM_PREDICT`.
- **Gap:** there is no session-level cap. A 68-turn loop can still burn 68 × 16 K. The only
  `TokenBudget` in the tree (`internal/config/config.go:36`, `Memory.TokenBudget`) belongs to the memory subsystem,
  not spend control.
- Cost is dominated by **input**, not output: a measured day showed **16.8 M input vs 192 K
  output** across 295 requests, because every turn resends the whole conversation. Output caps
  barely touch the bill; a session cap must count input.

### 3. Detect — abort a turn that has stopped progressing

`StuckDetector` in `internal/agent/stuck.go`, one entry point `ObserveEvent`, called by every
front end.

> **The guard must live in the shared agent layer, not a front end.** It was instantiated only
> in `internal/tui/agent_loop.go`, so `runPrint`, `json`, `rpc` and ACP had **no protection at
> all** — replay trials with **71, 67 and 42 byte-exact repeats ran to completion without
> aborting**, against a threshold of 12. Fixed by moving it; the lesson generalises to any
> future guard.

Arms and thresholds: `MaxRepeatToolCalls` 10, `MaxToolErrorStreak` 10, `MaxOutputRepeats` 12,
and `MaxThinkingEventStreak` 50 **and** `MinThinkingStreakBytes` 16384 for the semantic arm.

The semantic arm gates on both axes because neither is safe alone: providers chunk streams
differently (measured events run 1 byte to ~2.9 KB), so a token-level stream reaches 50 events
inside one sentence. Requiring volume too means a legitimate burst must beat the observed
healthy ceiling on **both** axes to trip.

Threshold guidance: because healthy (45) and degenerate (21+) ranges overlap, the semantic arm
must be set **conservatively above legitimate deep reasoning**. A false abort on a model that
was genuinely thinking is worse than a missed short loop, because the other three arms remain
as backstops. At 50 events this **concedes 3 of the 10 measured degenerate runs** (those at 21,
34 and 38 events, all below the healthy ceiling of 45) — an accepted trade, not an oversight.

### 4. Diagnose — make failures visible after the fact

- Every mode must write a session log. ACP wrote **none** until `feat/shared-stuck-guard`:
  across 533 sessions, `session_start` carried only `interactive`, `print`, `json`, `rpc`. An
  ACP loop was unguarded *and* undiagnosable.
- `pi-loop-forensics` skill: `scan_logs.py` (corpus sweep), `score_run.py` (per-log scoring on
  `think_run` / `reps` / intent-vs-calls), `ab_replay.sh` (provider A/B replay).
- Seed-replay technique: a spiralling turn's thinking is usually never committed to
  `events.jsonl`, so the session ends at the tool result *before* the spiral and resuming it
  reproduces the failure.

## Status

| Item | State |
|---|---|
| Sampling knobs exposed | done — PR #112 |
| Guard shared across all front ends | done — PR #113 |
| ACP session logging | done — PR #113 |
| Semantic-loop arm (thinking with no tool call) | done — PR #114 |
| Session-level token cap (input-counting) | **open** |
| Default penalty values | **deliberately not set** — blocked on the semantic arm |

## Pitfalls for whoever picks this up

- **`pi ping` is not a provider health check.** It resolves URLs and credentials differently
  from the real agent path and produced three false "provider down" readings (empty-host DNS
  on `opencode/*`; `:cloud` dialing `localhost:11434` despite a key being set; `[::1]` when the
  daemon binds IPv4). Verify with `pi --mode print "reply with exactly: OK"`.
- **Check the commit timeline before judging an old log.** `e069b34` (2026-08-08 06:56) added
  the output-repetition arm; `5a4cb8b` (2026-08-08 18:52) stopped aborting productive polling.
  Sessions before those cannot be compared against current behaviour, and "no abort" in a
  pre-move log does not mean "no loop".
- **Reproduction is probabilistic.** Nothing pins temperature or seed. Compare rates across
  several trials, never single runs.
