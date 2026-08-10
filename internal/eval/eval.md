# Eval harness for `/run`

`make eval-run` drives a **real** `/run eval-orchestrator` against the current
repository and measures what actually happened end-to-end:

- **Trajectory** — every ATIF trajectory written during the run, including
  nested subagent sessions and their nesting depth.
- **Subagent concurrency** — the orchestrator's pool budget, the number of
  top-level agents running over time, and nested fan-out.
- **Tools efficiency** — per-tool call/result counts, wasted calls, duplicate
  calls, error-looking results, average result size and latency.

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
3. Creates a temp git worktree of the **current repo** at HEAD and runs `/run`
   against it. The primary checkout is never touched.
4. Polls `orch.List()` every ~50 ms during the run to sample concurrency.
5. Pumps the `/run` bubbletea cmd chain to completion (worker → gates → verify →
   merge) with a global timeout.
6. Diffs the produced `specs/eval-orchestrator/artifacts/` against the golden
   copy in `specs/eval-orchestrator/golden/`.
7. Writes a JSON + Markdown report to `eval-reports/` and prints the Markdown
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

The report timestamped filename is `eval-<spec>-<ts>.json` / `.md`.

## Golden baseline flow

1. First run authoring the spec: create the spec under `specs/eval-orchestrator/`
   with the expected artifacts in `golden/`.
2. Run with `PI_EVAL_SAVE_GOLDEN=1 make eval-run`. If the run completes and the
   produced artifacts match `golden/`, the harness records the worktree's HEAD
   as ref `eval/golden`.
3. Re-run later with `PI_EVAL_BASELINE=eval/golden make eval-run` to diff the
   new run's artifacts against the recorded baseline and see whether the `/run`
   behavior drifted.

## Interpreting the report

- **Outcome** — final phase (`done`/`failed`), retry count, gate pass/fail,
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

## Notes

- The eval spec (`specs/eval-orchestrator/`) is a self-contained Go module so
  the task does not touch the pi-go module graph. It is intentionally
  deterministic (exact file contents) so the golden diff is meaningful, while
  still requiring read/write/edit/bash/`go test` iteration.
- The primary checkout's working tree is never modified; the temp worktree is
  removed on completion and its branch deleted. Only the `eval/golden` refs
  persist (in the primary's `.git`).
