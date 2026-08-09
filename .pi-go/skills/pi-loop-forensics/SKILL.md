---
name: pi-loop-forensics
description: Diagnose pi-go agent loops and degenerate turns — "agent loop aborted", runaway thinking with no tool calls, repeated phrases. Discriminates genuine model repetition collapse from a race, a tool-parse failure, or a too-low guard, and A/B replays a seed session across providers.
---

# Loop Forensics

Use when a run dies with `agent loop aborted: ...`, or a model burns a long turn
thinking without ever calling a tool. This skill decides **why** before anything
is changed: the abort message names a symptom, not a cause.

Sibling skill `pi-check-session-logs` covers *tool call errors*. This one covers
*repetition and runaway turns*. They do not overlap.

## Where the evidence lives

| What | Path |
|---|---|
| Session logs (JSONL, one per run) | `~/.pi-go/log/<yyyy-mm-dd>/session-HH-MM-SS.log` |
| Session state (resumable) | `~/.pi-go/sessions/<id>/` — `events.jsonl`, `meta.json`, `trajectory.atif.json` |
| Detector source | `internal/tui/agent_loop.go` |

Log rows are `{"time","type","content",...}` with `type` in `session_start`,
`thinking`, `llm_text`, `tool_call`, `tool_result`, `user`, `error`. The model
name is on the `session_start` row — always record it, findings are per-model.

## How the guard actually works

`stuckDetector` (`internal/tui/agent_loop.go:119`) has three independent arms:

- `observe` — identical consecutive tool calls, trips at `maxRepeatToolCalls`=10.
  Pagination args are stripped first (`volatileToolArgs`), so paging one file
  collapses to one fingerprint.
- `observeError` — same tool failing `maxToolErrorStreak`=10 times running.
- `observeOutput` (`:246`) — the model's own text/thinking. Needs a period ≥
  `minOutputPeriod`=16 bytes, ≥ `minPeriodVariety`=8 distinct bytes, and
  `maxOutputRepeats`=12 **byte-exact** back-to-back copies inside an 8 KB tail.

The third arm is the only one that sees a turn making no tool calls at all.
`outBuf` is never reset across a run, and the thinking branch (`:690`) skips
`dedup.SkipText`, unlike the text branch (`:697`).

**Timeline caveat — check this before judging any old log.** Sessions predating
these commits cannot be compared against current behavior:

| Commit | Landed | Effect |
|---|---|---|
| `e069b34` | 2026-08-08 06:56:44 +0200 | added `observeOutput` (the phrase-repetition arm) |
| `5a4cb8b` | 2026-08-08 18:52:42 +0200 | stopped aborting productive polling (`bash_output`) |

## Steps

1. **Sweep the corpus** for scale and per-model distribution:

   ```bash
   python3 .pi-go/skills/pi-loop-forensics/scan_logs.py
   ```

   Reports per model: session count, aborts, longest tool-free thinking run
   (max/p95), worst periodic repetition. Also lists aborted sessions and the
   top offenders. Loops are almost always concentrated in one model.

2. **Score individual logs**:

   ```bash
   python3 .pi-go/skills/pi-loop-forensics/score_run.py ~/.pi-go/log/*/session-*.log
   ```

   Three metrics per log: `think_run` (longest tool-free thinking run),
   `reps/period` (longest byte-exact periodic tail), `intent/calls`
   ("let me write/run/test" phrases vs actual tool calls). A high intent:calls
   ratio is the signature of announce-but-never-act.

   `reps>=12` is the reliable discriminator. The `think_run>=20` arm is
   heuristic and does produce false positives on models that legitimately think
   in long bursts — confirm with `reps` before calling it a loop.

3. **Discriminate the cause.** Run all three tests; do not stop at the first
   plausible one.

   - **Race / double-emission?** Hash every `thinking` payload in the session
     and count exact duplicates, and check timestamps. Genuine model output has
     **zero duplicate payloads**, varied lengths, and monotonic timestamps
     spaced by stream latency. Duplicated payloads or identical timestamps
     would mean pi-go re-emitted chunks — a real bug in the stream path.
   - **Tool-parse failure?** Grep thinking *and* text for tool-call syntax that
     leaked in as prose: `<tool_call`, `<function`, `tool_calls`,
     `"name":...,"arguments"`, ` ```json `, `<think>`, `<｜tool`. If the model
     emitted a call the provider layer failed to parse, the raw syntax shows up
     in a text channel and the model never receives a result — which looks
     exactly like a loop. Zero matches rules this out.
   - **Guard too low?** Compare the tripping value against the corpus. Healthy
     sessions peak around 1 periodic repeat; the threshold is 12 byte-exact
     copies. If healthy runs sit far below the threshold, the guard is not the
     problem.

   If all three are ruled out, it is inference-level repetition collapse, and
   the fix is a provider/sampling question, not a pi-go parsing question.

4. **A/B replay across providers.** Find a seed session whose **last persisted
   event is the tool result immediately before the spiral** — the degenerate
   thinking usually never gets committed to `events.jsonl`, so resuming
   restores the exact pre-failure state and the next turn is the one that broke.

   ```bash
   # From an isolated worktree — a resumed agent can write files.
   PI=/path/to/pi TRIALS=3 OUT=/tmp/ab-replay \
     bash .pi-go/skills/pi-loop-forensics/ab_replay.sh
   ```

   Arms are `ollama-cloud`, `ollama-local`, `opencode` (override with
   `ARMS="ollama-cloud opencode"`). Each arm runs a **preflight** one-shot first
   and is skipped with its error if credentials are dead, so a bad key costs one
   call instead of every trial. Override the seed with `SEED=<session-id>`.

   Per trial the script copies the seed to a throwaway session ID (the original
   is never mutated), rewrites `meta.json` (`id`/`model`/`provider`/`workDir`),
   resumes with `--trace-http --mode print`, scores the log, and deletes the copy.

   - Nothing pins temperature or seed, so reproduction is **probabilistic** —
     run several trials per arm and compare rates, not single outcomes.
   - `--trace-http` writes full request/response bodies to the session log.
     Credentials are masked (`internal/provider/provider.go:440`); **prompts and
     source context are not**.
   - Run from a git worktree, never the primary checkout: a resumed agent can
     write files.

## Guidelines

- Always name the model and check it against the commit timeline before
  concluding anything about an old log.
- An abort message describes the symptom the guard caught, not the cause. A
  correct abort on genuine degeneration and a false positive on healthy polling
  look identical in the log.
- Ollama requests set only `num_predict` (`internal/provider/ollama.go:104-110`)
  — no `repeat_penalty`, `repeat_last_n`, `temperature`. A looping Ollama model
  currently has no tunable knob, which makes provider A/B the informative test.
- When reporting, separate *the guard behaved correctly* from *the model
  degenerated*. Both can be true at once, and they imply different fixes.

## Probing providers: `pi ping` is not trustworthy

Verified 2026-08-09. `pi ping` resolves URLs and credentials differently from the
real agent path, so a ping failure is **not** evidence a provider is down.
Confirm with a one-shot real run instead:

```bash
./pi --model <model> --mode print "reply with exactly: OK"
```

Observed ping-only failures, all of which the real path handled fine:

| Symptom | Cause |
|---|---|
| `DNS resolution failed: lookup : no such host` (empty host) on `opencode/*` | ping never applies `opencodeDefaultBaseURL` (`internal/provider/opencode.go:62`) |
| `<model>:cloud` dials `localhost:11434` despite `OLLAMA_API_KEY` being set | ping does not pass the key, so the cloud-URL switch (`internal/provider/ollama.go:38`) never fires |
| `dial tcp [::1]:11434: connection refused` while the daemon is up | ping's dialer picks the IPv6 literal; the Ollama daemon binds IPv4. Pass `--url http://127.0.0.1:11434` |

Keys live in `~/.pi-go/.env` **and** `<repo>/.pi-go/.env`; pi-go merges both
(`internal/config/config.go:387-389`). Neither is in a shell profile, so an
agent's own shell will not have them exported — but pi-go reads the files
itself, so that only matters for scripts that gate on env vars.

## Examples

- `/pi-loop-forensics` — sweep all logs, report per-model loop distribution
- "why did my run abort with 'repeated a 89-character phrase'" — steps 2 and 3
- "does this loop happen on the other provider too" — step 4
