# Requirements

## Rough Idea

`evals-pi-go`

## Questions & Answers

### Q1: What is the scope of "evals-pi-go"?

**A:** Two parts:

1. **Minimal tests to cover all existing harness** — add automated tests for the existing
   Python evaluation harness in `hack/test/tb2/` (collect.py, analyze.py, compare.py,
   tbench_pi_agent.py, tool_token_analysis.py), which currently has zero tests.
2. **TB2.1 full spec** — produce the full specification for running **Terminal-Bench 2.1**
   against pi-go.

### Q2: Research what is the latest TB2.1.

**A:** (see `research/terminal-bench-2-1.md`)

- TB2.1 = more verified iteration of TB2.0; 26/89 tasks modified. Official repo
  `harbor-framework/terminal-bench-2-1` (Apache-2.0); dataset `terminal-bench/terminal-bench-2-1`.
- 89 tasks, each a dir with `instruction.md`, `task.toml` (schema 1.1), `environment/Dockerfile`,
  `tests/test.sh`, `solution/`.
- Runs via Harbor: `harbor run -d terminal-bench/terminal-bench-2-1 ...`. Leaderboard: ≥5
  trials/task, upload publicly, `uv run lb submit`.
- Distinct from the old never-implemented "Terminal-Bench Pro (225 tasks) Go" spec.

### Q3: For part 1 "minimal tests", what coverage scope?

**A:** **(a) Pure-unit tests only.** Focus on deterministic helper functions: pricing/cost,
CSV append, stream parsers via synthetic JSONL, analysis table functions. Use pytest. Do NOT
need real agents, Docker, or Harbor (no subprocess/CLI orchestration mocks of those).

### Q4: Does "TB2.1 full spec" mean implementation or documentation?

**A:** **(a) Specification documents only.** Produce the PDD artifacts (requirements →
research → design → plan → PROMPT.md) describing how to run TB2.1 against pi-go. **No code
changes.**

### Q5: Where should pytest tests live and what tooling?

**A:** **(a)** Add a `hack/test/tb2/pyproject.toml` (uv/pytest) and a `tests/` subdir under
`hack/test/tb2/`, co-located with the harness.

### Q6: Which of the 5 harness scripts need tests, and how to handle `tbench_pi_agent.py`'s Harbor imports?

**A:** (awaiting answer)
