# Research: Terminal-Bench 2.1 (TB2.1)

## What is TB2.1

Terminal-Bench is a benchmark for measuring agents' ability to complete valuable, complex
tasks in container (terminal) environments (assembling proteins, debugging async code,
resolving security vulnerabilities, etc.).

**Terminal-Bench 2.1** is a *more verified iteration of Terminal-Bench 2.0*. 26 tasks were
modified to fix bugs, adjust timeouts/resources, or improve robustness to reward hacking.
Many changes taken directly from Z.ai's "Terminal-Bench 2.0 Verified" dataset.

Official source: `harbor-framework/terminal-bench-2-1` (GitHub), Apache-2.0.
Dataset id on Harbor Hub: `terminal-bench/terminal-bench-2-1`.

Note: this is **different** from the earlier design-only spec
`specs/evaluations/000-evaluation-terminal-bench/`, which targeted "Terminal-Bench **Pro**"
(a separate 225-task registry package) — a Go-based implementation that was planned but never built.

## Repo Layout

```
terminal-bench-2-1/
├── configs/            # leaderboard runner configs (YAML for harbor run)
├── leaderboard/        # leaderboard submission tooling (uv project, lb CLI)
└── tasks/
    ├── dataset.toml    # dataset manifest listing every task + sha256 digest
    └── <89 tasks>/     # one directory per task
```

89 tasks total (same task set as TB2.0; ~28 differ between 2.0 and 2.1 repos).

## Dataset manifest (`tasks/dataset.toml`)

```toml
[dataset]
name = "terminal-bench/terminal-bench-2-1"
description = "Version 2.1 of Terminal-Bench ..."
keywords = ["terminal", "cli", "software-engineering", "data-science"]

[[tasks]]
name = "terminal-bench/adaptive-rejection-sampler"
digest = "sha256:..."
# ... one [[tasks]] entry per task (89 total)
```

## Task Format (per-task directory)

Each task dir contains:

```
<task>/
├── instruction.md          # the prompt given to the agent
├── README.md
├── task.toml               # task metadata + environment + timeouts
├── environment/
│   ├── Dockerfile
│   └── protected.tar.gz.enc
├── tests/
│   ├── test.sh             # verifier script (pass/fail via exit code)
│   └── test_outputs.py     # (some tasks)
└── solution/
    └── solve.sh            # reference solution
```

### `task.toml` schema (`schema_version = "1.1"`)

```toml
[task]
name = "terminal-bench/<name>"
description = "..."
keywords = [...]

[metadata]
difficulty = "easy|medium|hard"
category = "<category>"
tags = [...]
expert_time_estimate_min / junior_time_estimate_min

[verifier]
timeout_sec = 900.0

[agent]
timeout_sec = 900.0

[environment]
build_timeout_sec = 600.0
docker_image = "org/<task>:<tag>"   # prebuilt image (some tasks)
cpus / memory_mb / storage_mb / gpus
allow_internet = true|false
mcp_servers = []

[verifier.env] / [environment.env] / [solution.env]
```

Example task: `terminal-bench/adaptive-rejection-sampler` (category scientific-computing,
difficulty medium) — implement an adaptive rejection sampler in R saved to `/app/ars.R`.

## How it is run (Harbor)

Runs are driven by the **Harbor** framework (`harbor run`):

```shell
uv tool install "harbor[daytona]"
harbor run -d terminal-bench/terminal-bench-2-1 \
  -a <agent> -m <provider/model> -e <sandbox> -k 5 -n <concurrency> \
  --upload --public
```

Leaderboard submission requires: run the *unmodified* dataset, ≥5 trials per task, upload
jobs publicly, then `uv run lb submit <job-links...>` from the `leaderboard/` dir.

The existing pi harness (`hack/test/tb2/tbench_pi_agent.py`) is precisely the Harbor agent
adapter that would be used to run pi-go against TB2.1:

```shell
MOUNTS='["/usr/local/bin/pi:/mnt/pi:ro", "~/.pi-go:/mnt/pi-go:ro"]'
harbor run -d terminal-bench/terminal-bench-2-1 \
  -m anthropic/claude-sonnet-4-6 \
  --agent-import-path tbench_pi_agent:PiAgent \
  --mounts-json "$MOUNTS" -n 4 -y
```

## Leaderboard configs (`configs/*.yaml`)

`leaderboard.yaml` pins the dataset ref `sha256:7d7bdc1cbedad549fc1140404bd4dc45e5fd0ea7c4186773687d177ad3a0699a`,
runs 64 concurrent trials, 5 attempts, retry max 3, environment type `daytona`. Agents
include terminus-2, claude-code, codex, gemini-cli across various models.

## Relationship to existing repo work

- The existing `hack/test/tb2/` harness is Python + Harbor and already references
  `terminal-bench/terminal-bench-2` (TB2.0 dataset). Upgrading to TB2.1 means pointing at
  `terminal-bench/terminal-bench-2-1`.
- There is **no Go-based** eval implementation; the old `specs/evaluations/000-evaluation-terminal-bench/`
  design (Terminal-Bench Pro, Go, 225 tasks) was never implemented.
- ROADMAP.md line 28: `- [ ] Evaluations using terminal bench harbor https://harborframework.com/registry`
