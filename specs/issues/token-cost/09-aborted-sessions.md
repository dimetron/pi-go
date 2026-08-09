# Topic 9 — Aborted sessions inflate token spend silently

## Research

### The case that triggered this report

`260809-0249-c53d2-7561f` — `deepseek-v4-flash:0731-cloud` via `ollama`,
created 2026-08-09T02:49:40+02:00, ended 2026-08-09T02:50:42+02:00 (62 s wall
clock). 13 events recorded, 6 model turns.

| # | ts (event start) | author | TC | tools | prompt | cand | Δ prompt | gap_s |
|---|---|---|---|---|---|---|---|---|
| 1 | 02:50:18.670 | user | False | – | 0 | 0 | – | 0.000 |
| 2 | 02:50:20.158 | pi   | True  | read | 10,878 | 78 | +0 | 1.488 |
| 3 | 02:50:20.161 | pi   | False | –    | 0 | 0 | –10,878 | 0.003 |
| 4 | 02:50:27.112 | pi   | True  | ls, bash | 12,512 | 189 | +1,634 | 6.951 |
| 5 | 02:50:27.118 | pi   | False | –    | 0 | 0 | -12,512 | 0.006 |
| 6 | 02:50:33.439 | pi   | True  | read×3 | 13,352 | 600 | +840 | 6.322 |
| 7 | 02:50:33.443 | pi   | False | –    | 0 | 0 | -13,352 | 0.004 |
| 8 | 02:50:35.978 | pi   | True  | read×2 | 17,199 | 424 | +3,847 | 2.535 |
| 9 | 02:50:35.980 | pi   | False | –    | 0 | 0 | -17,199 | 0.002 |
| 10 | 02:50:38.886 | pi   | True  | read×2 | 25,093 | 574 | +7,894 | 2.905 |
| 11 | 02:50:38.889 | pi   | False | –    | 0 | 0 | -25,093 | 0.003 |
| 12 | 02:50:42.629 | pi   | True  | read×4 | 28,664 | 579 | +3,571 | 3.741 |
| 13 | 02:50:42.631 | pi   | False | –    | 0 | 0 | -28,664 | **0.002** |

**Most diagnostic number** — the wall-clock gap between the event where the
model emitted the 4 parallel `read` calls (event #12) and the final recorded
event (event #13) is **0.002 s**. The model completed its 6th turn, the four
`read` tools returned, and then the session recorder went silent before the
next model turn began.

**Last event #13** (ID `8c6f7408-9ce6-455d-aef0-00aee80eee4c`):
```
Author:        pi
TurnComplete:  false
Interrupted:   false
ErrorMessage:  ""
ErrorCode:     ""
```

Neither `ErrorMessage` nor `Interrupted` is set, so the abort class is silent
in the existing logs. The cause cannot be determined from the events alone —
the recorder accepted the four tool responses and then stopped without writing
a closing event. Possible causes (not disambiguable here):

- the user killed the process (Ctrl-C) between the FR write and the next
  model call;
- the provider stream closed and the runner bailed before emitting a final
  partial;
- an internal context-cancellation triggered after the tools returned.

Correction to the original triage note: tool responses **did** come back. Event
#13 carries all four `functionResponse` parts (`read` for `04-bugs.md`,
`05-memory.md`, `06-subagents.md`, `07-measurement.md`). The recorder stopped
*after* writing those, not before.

**Note on the "5 of 6 turns issued a single tool call" summary** that
prompted this slice: the actual breakdown is **1 of 6 turns issued a single
tool call** (turn #2, `read`). Turns #4, #6, #8, #10, #12 each issued 2–4
parallel calls. The single-call summary mis-counted the function-response
events as separate model turns; a model turn = one `pi` event with
`TurnComplete: true` (six of them here), not the FC/FR pair.

### Corpus-wide abort class

Scanned every session dir under `$HOME/.pi-go/sessions/*/`:

| class | count | share of dirs |
|---|---|---|
| `events.jsonl` empty (session created, never used) | 1,717 | 51.4% |
| last event `TurnComplete: true` | 1,125 | 33.7% |
| **abort class** (last `TurnComplete: false`, no error, not interrupted) | **~323** | **~9.7%** |
| other (errored, or `Interrupted: true`) | 177 | 5.3% |
| **total session dirs** | **3,342** | 100% |

Numbers drift by a handful per day as new sessions land on disk. The class
definitions are stable; the absolute counts here were captured during the
investigation and are within a percent of what the verification one-liner
returns at any moment.

Among the 1,416 sessions carrying usage data:

| class | sessions | prompt tokens | share of tokens |
|---|---|---|---|
| complete | 1,084 | 1,729,802,377 | 67.0% |
| **abort** | **~255** | **~523 M** | **~20%** |
| other (errored/interrupted) | 77 | 327,383,070 | 12.7% |
| total with usage | 1,416 | 2,580,391,214 | 100% |

The abort class accounts for **~20% of all prompt tokens** and **~18% of
sessions** carrying usage data. Per-abort session:

```
abort turn-count distribution (n≈255 with usage):
  median: 5
  p90:    ~65-66
  mean:   ~22
  max:    372
```

Top models by abort count (from `meta.json::model`):

| model | abort count |
|---|---|
| `minimax-m3:cloud` | 56 |
| `minimax-m2.7:cloud` | 52 |
| `gemini-3.5-flash` | 46 |
| `glm-5.2:cloud` | 44 |
| `gpt-5.5` | 34 |

Top providers (from `meta.json::provider`): only **4 of 323** abort sessions
have a `provider` field in their meta.json. The 319 that lack it are older
sessions (most aborts predate the field being added). Among the 4 that
have it: **3 are `ollama`**, 1 is `opencode`. So the target session here
is one of only three ollama-tagged aborts in the corpus. The model/provider
breakdown for the 319 provider-less sessions is recoverable from the events
themselves; the model breakdown above is exact.

### Adjacent sessions (±10 min)

| Δs | id | model | events | last TC | notes |
|---|---|---|---|---|---|
| +107 | 260809-0251-e92b0-0de4c | deepseek-v4-flash:0731-cloud | 2 | True | title: "check why 260809-0249-…" |
| +160 | 260809-0252-a8fce-650b2 | deepseek-v4-flash:0731-cloud | 4 | True | title: "check pi-go sessions 260809-0249-…" |
| +200 | 260809-0253-3b5f6-57289 | deepseek-v4-flash:0731-cloud | 64 | **False** | title: "check why session 260809-0249-… was repeated - che…" — also in the abort class |
| +422 | 260809-0256-9b725-80b70 | minimax-m3:cloud | 47 | True | title: "A" |

The user retried the same query four times over ~7 minutes. Three of the
retries (0251, 0252, 0256) closed normally; **0253 also aborted** (64 events,
last `TurnComplete: false`, no error, not interrupted). Two aborts in the
same query within 7 minutes is a strong signal that the abort is *not*
correlated with the user pressing Ctrl-C — the user would have noticed the
same silence and moved on.

## Why this matters

`TOKENS.md` reports a 201:1 prompt/output ratio across 1,404 measured
sessions. That headline is the ratio for **complete** sessions — sessions
that produced a `TurnComplete: true` final event. The abort class
contributes 523 M prompt tokens (20.3%) for **zero recorded output** of the
kind the report counts. Including aborts in the corpus would:

- shift the headline toward a worse ratio (output is unchanged; prompt
  climbs by ~30%);
- reveal that the cost of agentic coding is higher than the measured
  corpus suggests, and that the gap is concentrated in sessions that did
  not finish;
- provide a fifth, structural finding for the `TOKENS.md` recommendations
  table: when the recorder dies mid-turn, every `promptTokenCount` from
  that session has been spent with no possibility of being amortised over
  later tool results.

The 20.3% figure is a **lower bound**. 68 of the 323 abort sessions have no
`UsageMetadata` in their event log; their token spend is invisible to the
scan. And the 1,717 empty `events.jsonl` dirs (sessions that were created
but never used) are excluded from the per-session numbers but represent
unaccounted client lifetimes.

The repeating-aborts pattern (target + sibling 0253, both aborts, same
model, same provider, 200 s apart) suggests the abort has a model-route
component that is not visible in the event stream. Until the recorder
writes a closing event with a cause, this is invisible by construction.

## Recommendations

The abort cause cannot be determined from `events.jsonl` alone. The first
recommendation is to fix that; the next two are downstream.

1. **Write a closing event on every exit path, with a cause field.**
   `FileService.AppendEvent` (`internal/session/store.go:345`) is the single
   write path; the `nonInteractiveRuntime.close()` defer
   (`internal/cli/cli.go:567`) only kills subprocesses. There is no
   equivalent `defer AppendEvent(closingEvent)` at the runner level. The
   interactive path (`internal/cli/interactive.go:498`) builds an
   `agentCtx` from `m.ctx` and cancels it on user input but does not log a
   final event. Add a single close hook that writes one of:
   `{reason: "user_interrupt", signal: "SIGINT"}`,
   `{reason: "stream_closed", provider: "...", http_status: 0}`,
   `{reason: "context_cancel", source: "agent_loop"}`,
   `{reason: "complete"}`.
   With this, the 323 silent aborts collapse to 0 and a small
   `from collections import Counter; Counter(r["reason"] for r in aborts)`
   tells you where to look.

2. **Persist `createdAt`, `updatedAt`, and a new `endedAt` plus `endReason`
   on `meta.json` even when no closing event was written.**
   Currently `meta.json` only carries `createdAt` and `updatedAt` (verified
   against `260809-0249-c53d2-7561f/meta.json`), and the meta is rewritten
   by `AppendEvent` (`store.go:384`) only when an event lands. A session
   that exits between events leaves `updatedAt` pointing at the last
   successful event, indistinguishable from a session that paused for 10
   minutes. Add `endedAt` and `endReason` and have the close hook from
   recommendation 1 write both.

3. **Re-run the corpus scan after recommendation 1 lands and add the
   findings to `TOKENS.md`.** Until the cause is captured, this report
   should not be folded into the headline number — the 20.3% is real
   spend but its breakdown by cause is currently unknowable.

## Expected impact

Without root-cause data, this report cannot promise a specific reduction.
The 20.3% figure is the upper bound: if every abort is user-driven
(Ctrl-C), the fix is UX (recover gracefully). If the abort is provider-
driven (stream closure), the fix is provider-observability. If the abort is
context-cancellation, the fix is in the ADK runner. Recommendation 1 turns
the 323-row "silent abort" pile into a categorised list and points the fix
at the right subsystem. Recommendation 2 makes the same information
visible without requiring the events file to be present.

## Risk

Low. Both recommendations are additive: writing one more event and adding
two fields to `meta.json`. They do not change runtime behaviour. They do
not move numbers in `TOKENS.md` until a follow-up re-measurement is
performed (recommendation 3).

## Verification

Reproduce the corpus-wide abort count with the script shipped alongside
this report:

```bash
python3 specs/issues/token-cost/09-replay.py
# Total session dirs:        3342
# Empty events.jsonl:        1717
# Complete (last TC=true):   1125
# Abort class:               323
# Other (err/interrupt/etc): 177
# Abort prompt-token total:  ~525 M
```

Reproduce the per-turn numbers for the trigger session:

```bash
python3 specs/issues/token-cost/09-replay.py 260809-0249-c53d2-7561f
# penultimate -> last gap: 0.002s
# last event: TurnComplete=False Interrupted=False ErrorMessage=''
```

The abort-class definition is exact: `last.TurnComplete is False AND
last.ErrorMessage == "" AND last.Interrupted is False`. No judgments about
session age, model, or tool calls.
