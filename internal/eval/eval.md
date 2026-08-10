# Eval harness for `/run`

`make eval-run` drives a **real** `/run eval-orchestrator` against the current
repository and measures what actually happened end-to-end:

- **Trajectory** — every ATIF trajectory written during the run, including
  nested subagent sessions and their nesting depth.
- **Subagent concurrency** — the orchestrator's pool budget, the number of
  top-level agents running over time, and nested fan-out.
- **Tools efficiency** — per-tool call/result counts, wasted calls, duplicate
  calls, error-looking results, average result size and latency.

An optional **LLM judge** grades the same axes qualitatively (see below).
Every run starts from a **pinned base commit** so re-runs are comparable.

This is a **manually-run** harness: it needs a built `pi` binary and a live LLM
API key. It is not part of the unit test suite — the driving test is build
tagged `e2e` and skips unless `PI_EVAL_RUN=1`.

## How it works

The driving test lives in package `tui` (`internal/tui/run_eval_e2e_test.go`)
because it drives the unexported `/run` handlers directly (the repo convention
for run-flow tests). It:

1. Resolves the `pi` binary (see below).
2. Sets `HOME` to a temp dir and seeds `$HOME/.pi-go/config.json`, so the run's
   sessions and those of any nested `pi` workers land under one controlled root.
   Isolating HOME also disables git commit signing for the run (`GIT_CONFIG_*`
   env, process-scoped): the repo signs commits/tags with the user's real key,
   which is unreachable from the temp HOME, and the eval's commits are
   throwaway in a temp worktree anyway.
3. Creates a temp git worktree of the repo at the **pinned base commit** (the
   `eval/base` tag) and runs `/run` against it. The primary checkout is never
   touched.
4. Polls `orch.List()` every ~50 ms during the run to sample concurrency.
5. Pumps the `/run` bubbletea cmd chain to completion (worker → gates → verify →
   merge) with a global timeout.
6. Diffs the produced `specs/eval-orchestrator/artifacts/` against the golden
   copy in `specs/eval-orchestrator/golden/`.
7. Optionally asks an **LLM judge** to grade the run from the computed metrics
   plus a condensed tool-call timeline.
8. Writes a JSON + Markdown report to `eval-reports/` and prints the Markdown
   summary.

The metric computation itself is pure and lives in `internal/eval/`
(`metrics.go`, `report.go`), importable without any tui dependency.

## Prerequisites

- A built binary: `make build` (produces `./pi`).
- An LLM API key in the environment (`ANTHROPIC_API_KEY` / `OPENAI_API_KEY` /
  `GEMINI_API_KEY`, matching the model you run with).
- `go` and a working `git`.

## Running

```bash
make eval-run
```

Variants:

| Env | Effect |
|---|---|
| `PI_BINARY` | Path to the `pi` binary. Default: `./pi` → `$GOPATH/bin/pi` → `pi` on `PATH`. |
| `PI_EVAL_MODEL` | Model for the eval (roles config). Default: `config.Defaults()`. |
| `PI_EVAL_CONCURRENCY` | Pool size → exported to workers as `PI_SUBAGENT_CONCURRENCY`. Default: config default (3). |
| `PI_EVAL_PARALLEL=1` | Run in `--parallel` mode. Default: single worker. |
| `PI_EVAL_TIMEOUT` | Overall run timeout (Go duration). Default: `30m`. |
| `PI_EVAL_OUT` | Report output dir. Default: `./eval-reports`. |
| `PI_EVAL_SAVE_GOLDEN=1` | On golden PASS + run `done`, tag `eval/golden-<ts>` and force branch `eval/golden` at the eval worktree HEAD. |
| `PI_EVAL_BASELINE` | A git ref (e.g. `eval/golden`) whose prior artifacts are diffed against this run's. |
| `PI_EVAL_BASE` | Git ref the eval worktree is created from. Default: the `eval/base` tag, or `HEAD` if never pinned. |
| `PI_EVAL_PIN_BASE=1` | (Re)create the `eval/base` tag at HEAD before running — how the starting point is established or moved. |
| `PI_EVAL_JUDGE_MODEL` | Model to grade the run (e.g. `claude-sonnet-4-6`). Unset = no judge. |
| `PI_EVAL_JUDGE_MAX_CALLS` | Tool calls per session included in the judge's timeline. Default: `60`. |

The report timestamped filename is `eval-<spec>-<ts>.json` / `.md`.

## The pinned starting point

Every run branches from the commit tagged **`eval/base`**, not from a moving
HEAD. This is what makes two runs comparable: a run against whatever HEAD
happens to be measures the repository's drift as much as `/run`'s behavior, so
a regression in the numbers could not be attributed to either.

```bash
make eval-pin      # tag eval/base at HEAD, run, and save golden refs
make eval-run      # every later run starts from that same commit
```

The report records both `base_ref` and the commit it resolved to, so a report
stays meaningful after the tag is moved. Moving the tag is deliberate — do it
when the `/run` flow itself changes and the old baseline no longer applies.

## Golden baseline flow

1. First run authoring the spec: create the spec under `specs/eval-orchestrator/`
   with the expected artifacts in `golden/`.
2. Run `make eval-pin` (`PI_EVAL_PIN_BASE=1 PI_EVAL_SAVE_GOLDEN=1`). This pins
   `eval/base` at HEAD and, if the run completes and the produced artifacts
   match `golden/`, records the worktree's HEAD as ref `eval/golden`.
3. Re-run later with `make eval-judge` (`PI_EVAL_BASELINE=eval/golden` plus the
   judge) to diff the new run's artifacts against the recorded baseline and see
   whether the `/run` behavior drifted.

## LLM judge

Set `PI_EVAL_JUDGE_MODEL` (or run `make eval-judge`) to have a model grade the
run 1–5 on four dimensions: `outcome_correctness`, `trajectory_quality`,
`concurrency_use`, `tools_efficiency`. It sees the computed metrics plus a
condensed tool-call timeline (one line per call, with truncated arguments and
error markers) — not the raw trajectories, which do not fit in a prompt.

The verdict is **advisory and never asserted on**. A grader is not a stable
enough signal to fail a build, but it catches what the counters miss: a worker
that thrashed, retried the same failing command without changing approach, or
reached the right answer by accident. A judge that cannot run (no API key,
transport error, unparseable reply) degrades to a one-line note in the report
rather than failing the eval.

Use a *different* model than the one under evaluation where you can — a model
grading its own trace is a weaker check.

## Interpreting the report

- **Outcome** — final phase (`done`/`failed`), retry count, `reason` (the
  run flow's terminal failure message, e.g. a merge/sign error), gate pass/fail,
  golden and (optional) baseline file matches. The run *outcome* is reported,
  not asserted: a legitimately failing run still produces a full metrics
  report.
- **Trajectory** — one row per session with depth. `max_depth` reflects nested
  fan-out: the coordinator process cannot be sampled directly, so nested
  concurrency is measured from the trajectory refs instead.
- **Concurrency** — `max/mean concurrent` and a sparkline of the running count.
  `pool_budget` is the top-level pool; `worker_budget` is what a nested worker
  inherits (`max(1, budget/2)`, mirroring `childConcurrency`).
- **Tools** — per-tool table. `wasted` = tool call with no matching
  observation; `duplicates` = repeated identical `(function, arguments)` calls;
  `errors` = results whose content reads as a failure (conservative heuristic).
- **LLM judge** — per-dimension 1–5 scores with rationales, an overall mean, a
  pass/fail verdict and any issues raised. Advisory: read it alongside the
  numbers, not instead of them.

## Notes

- The eval spec (`specs/eval-orchestrator/`) is a self-contained Go module so
  the task does not touch the pi-go module graph. It is intentionally
  deterministic (exact file contents) so the golden diff is meaningful, while
  still requiring read/write/edit/bash/`go test` iteration.
- The primary checkout's working tree is never modified; the temp worktree is
  removed on completion and its branch deleted. Only the `eval/golden` refs
  persist (in the primary's `.git`).
- `/run` names the worker's worktree after the spec, so a run that is killed or
  crashes can leave that branch registered in the shared `.git` and block the
  next run. The harness removes any such leftover before starting.
- The isolated `HOME` is **not** a `t.TempDir()`. The run's nested `go test`
  populates `$HOME/go/pkg/mod`, and the Go module cache is written read-only —
  `TempDir`'s cleanup cannot unlink those files and fails the test *after* a
  successful measurement. The harness chmods the tree writable before removing
  it, and logs rather than fails if removal still cannot finish.
