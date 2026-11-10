# Issue 011: Evaluate AgentPilot (HKUDS) — a learned "what to try next" policy

> **Upstream:** [HKUDS/AgentPilot](https://github.com/HKUDS/AgentPilot) — *"AgentPilot:
> Better Next Moves for Your Agent. Get Things Done Faster."*
> **Status:** To investigate. Not a bug, not a commitment to adopt.
> **Checked:** 2026-11-09, from the upstream README only. Nothing here has been run.
> **Phases:** readme (+ research if the evaluation is worth it)

## Why it came up

pi-go already decides *what to do next* in three places, all hand-rolled:

| Where | Decision | How it is made today |
|---|---|---|
| `internal/tui/run.go` | retry, give up, or merge | fixed `MaxRetries` budget + two-signal gate |
| `internal/tui/goal.go` | continue the loop or stop | fixed `MaxCycles` budget, stop on Esc |
| `internal/sop/` | which stage runs next | static graph compiled from YAML |

All three are **fixed policies**. AgentPilot's claim is that the choice of *next
attempt* — which parent to improve, whether to explore or refine, which direction
is worth trying — can be **learned from the outcomes of previous attempts** rather
than configured.

That is the same shape as `/run`'s retry loop and `/goal`'s cycle loop, which makes
it worth a look. It is not obviously the same problem, which is why this is an
issue rather than a plan.

## What it claims

From the README, without independent verification:

- Maintains a **Shared Search State**: tried versions, version lineage, successful
  and failed attempts, unexplored directions, remaining improvement budget.
- Makes three decisions — **D1** choose a parent version, **D2** generate
  directions (new idea vs refine an existing one), **D3** choose an action.
- Feeds each trial's outcome back, so later attempts are chosen differently.
- Two learning mechanisms: **KL-UCB** for parent/direction selection, and a
  **Policy Memory** that distils experience into reusable guidance.
- Ships with **OpenSpace** (learn and reuse skills) and an **RSI Arena** (measure
  an agent's learning ability against benchmarks such as Terminal-Bench 2.1,
  SWE-bench Verified).
- Python, `uv`-managed, `--python 3.12`.

## The integration question, stated plainly

**This is a Python framework and pi-go's toolchain is Go.** `AGENTS.md` now says
verification and tooling are Go under `hack/`, and that if a task seems to need
Python it needs a Go program instead. So the honest options are:

1. **Treat it as prior art, not a dependency.** Read the decision loop, and if the
   idea holds, implement the *policy* natively in Go inside `/run`'s retry path.
   No interpreter dependency; the model stays a design reference. This is the same
   call `AGENTS.md` records for `rtk`: an independent third-party tool was measured
   against pi-go's real shapes, found not to fit, and its *decisions* — not its
   code — went into a native Go implementation.
2. **Drive it as a subprocess** the way `codex`/ACP agents are driven, over a
   documented protocol, if it has one. That keeps Python out of the build but adds
   an external runtime to the test matrix — a real cost, and CI runs Windows.
3. **Do not adopt.** Plausible if its benchmark claims do not reproduce.

Option 1 is the one that fits this repo's rules, and the one worth researching
first.

## What to check before deciding

Ordered so the cheapest disqualifier comes first.

- [ ] **Is the evaluation honest?** The README's comparisons (Codex, DeepSeek
      Harness, OpenEvolve, ShinkaEvolve) are the author's own. Check whether the
      Arena configs are reproducible, whether the baselines are tuned, and whether
      a gain survives a second run. This repo's own rule applies: *never trust a big
      win without a proof check.*
- [ ] **Is the decision loop separable from the Python runtime?** If D1–D3 are a
      well-specified statistical procedure over a state object, they port to Go in
      a few hundred lines. If they are entangled with the LLM prompts and the
      arena, they do not.
- [ ] **Does pi-go even have the feedback signal it needs?** The policy learns from
      per-attempt outcomes. `/run` has gate results and a Verifier verdict per
      cycle — that is the signal. `/goal` has only "did it finish", which is much
      weaker. Worth mapping which loops could actually supply a reward.
- [ ] **What does it cost per attempt?** A learned policy that explores more
      directions spends more tokens. Every cycle is a full billed turn
      (`goal.go` says so explicitly); a policy that trades attempts for quality
      needs a token budget, not just an accuracy number.
- [ ] **Licence.** Not recorded here; must be checked before any code is copied,
      not just before it is depended on.

## What would make this worth a spec

Any one of:

- Reproducing a learning gain on a pi-go task (the eval harness in
  `internal/eval/` could run it against the pinned `eval/base` baseline).
- A short Go prototype of the UCB parent-selection rule showing it picks better
  retry directions than `MaxRetries` on recorded `/run` traces. This is the
  cheapest falsifiable test and it needs no Python at all: the traces are already
  in the session log.

If neither pans out, close this with the reason recorded — that is a useful result
too, and cheaper than adopting a framework.

## Explicitly not proposed

- Adding Python to the build or CI.
- Replacing the `/run` phase machine or the `/goal` loop with a learned policy
  before a trace-based prototype beats the current fixed budget.
- Any dependency on the RSI Arena for correctness testing. Benchmark scores are
  not a correctness gate; `hack/formal/` and the test suite are.
