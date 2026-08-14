---
name: nightly-session-watch
description: Nightly sweep of the last 24h of pi-go sessions — anomalous runs, loop aborts, tool error rates, token waste, real prompt-token spend, and whether the observation and palace pipelines are still recording. Triages each finding to the specialist skill that diagnoses it. Use for an unattended daily health check, or on demand after a bad night.
---

# Nightly Session Watch

One sweep over everything that happened in the last 24 hours, in priority order:
what **broke**, what it **cost**, and what to **do about it**.

Everything runs through `pi session-stats` — the same Go code path the
`session-stats` agent tool uses, so there is no second implementation to drift.
No LLM call, no network, reads only `~/.pi-go/sessions/*/events.jsonl`.

This is the umbrella check. It is deliberately shallow — it finds and ranks, it
does not diagnose. Every finding names the skill that goes deeper:

| Finding | Goes deeper with |
|---|---|
| `agent loop aborted: ...` | `pi-loop-forensics` |
| Tool call errors | `pi-check-session-logs` |
| Which tools dominate | `tools-stats` |
| Slow turns / throughput | `token-perf` |

Do not re-implement those here. If the sweep surfaces three aborted runs, the
answer is "run loop-forensics on these three", not a second loop analysis.

## Run it

```bash
pi session-stats                 # last 24h
pi session-stats --hours 72      # wider window
pi session-stats --all           # include sessions with no anomalies
pi session-stats --json          # counters only, for a cron wrapper
```

Other flags: `--high-tool-calls` and `--high-turns` move the anomaly
thresholds, `--session-dir` points at a different session root.

## Steps

1. **Run `pi session-stats`.** Present its markdown as-is — it is already
   ordered by severity.

2. **Read the numbers before interpreting them.** Three traps:
   - Tool *error rate* matters more than error count. `read` failing 5 times in
     253 calls is noise; a tool failing 8 of 8 is broken or misconfigured.
   - A heavy session is not automatically a bad session. High tool calls on a
     long task is work, not waste. Waste is the **Token waste** section.
   - **Prompt tokens dwarf everything else.** They are re-sent on every request,
     so the figure is dominated by the fixed block — system prompt plus tool
     declarations — not by the task. A large number there is not evidence of a
     bad night; a large number *per session* on short sessions is.

3. **Triage each finding** to the table above and name the skill to run next.
   Do not run them automatically — they are slower and interactive.

4. **Propose fixes, grouped into config, code, and prompt.** Patterns worth
   recognising:

   | Symptom | Likely fix |
   |---|---|
   | One tool at ~100% error rate | Missing credential or unavailable MCP server — check config before code |
   | `edit` error rate above ~20% | Editing without reading first, or on stale content — a prompt/ledger problem |
   | Oversized results from a tool marked **uncapped** | Wire it into `compactToolResult` in `internal/tools/compactor.go` |
   | Oversized results from a tool marked **yes** | The compactor is not running — check the after-tool callback chain |
   | Duplicate results inside one session | Dedup is not running, same cause as above |
   | `observations` DEGRADED or STALLED | The recording pipeline is down; see `specs/memory-fixes/` |
   | Palace last write many days old | Nothing is filing drawers — the bridge is not wired, or the palace is gated off |

5. **Say plainly when nothing is wrong.** A quiet night is a valid result and
   belongs in one line, not padded into a report.

## What it checks, and why each one

- **Per-session anomalies** — high tool calls, excessive turns, errors, heavy
  git activity, long idle runs.
- **Loop aborts** — the guard ending a run is the most user-visible failure
  there is. Grouped by reason: "repeated a phrase" and "identical tool call"
  have different causes and different fixes.
- **Tool error rates** — as a rate against call count, so a busy tool with a few
  failures does not outrank a broken tool nobody uses.
- **Token waste** — results over the compactor's 24k `MaxChars`, plus results
  re-sent byte-identical inside one session. Each oversized tool is labelled
  with whether `compactToolResult` covers it, which separates *"a component is
  broken"* from *"this tool was never wired up"*.
- **Token spend** — `promptTokenCount` / `candidatesTokenCount` straight from
  the provider's `usageMetadata`. Measured, not estimated.
- **Pipeline health** — the observation and palace stores. Both are best-effort
  by design: every failure downgrades to a warning nobody reads, so they can be
  dead for months while looking fine. The check is a **ratio**, not a presence
  test — a store holding a handful of rows across thousands of sessions is
  broken in the way that looks healthiest.

## Scheduling it

`--json` prints a stable object for a cron wrapper:

```json
{
  "hours": 24,
  "total_sessions": 175,
  "anomalous_sessions": 25,
  "aborted_runs": 0,
  "reclaimable_tokens": 232957,
  "prompt_tokens": 127052076
}
```

Alert on `aborted_runs > 0`, on `reclaimable_tokens` crossing a threshold you
pick, or on `prompt_tokens / total_sessions` drifting up. Keep the JSON as the
trigger and the markdown as what a human reads.

Nightly is the intended cadence: 24h is long enough for a pattern to show and
short enough that the offending session is still fresh.

## Limits worth stating

- `prompt_tokens` and output tokens are provider-reported and exact.
  `reclaimable_tokens` is `chars / 4` — fine for ranking and trends, not a
  billing figure.
- Only **tool result** volume feeds the waste number. The fixed per-request
  overhead is visible in the token-spend section instead.
- The window is selected by `events.jsonl` **mtime**, so a session that started
  before the window but was still running inside it is included whole.
- Duplicate detection is byte-exact within a single session. A near-duplicate,
  or the same file read across two sessions, is not counted.
