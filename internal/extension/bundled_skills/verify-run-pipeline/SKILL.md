---
name: verify-run-pipeline
description: Formally verify the /run phase machine before or after changing it. Use when editing internal/tui/run.go's phase transitions (handleRunAgentDone, runGates result handling, verifyRunComplete, retryRun, reportGateHang, merge), internal/sop/verdict.go's Passed, or the checklist refresh — or when a run merged work it should not have, retried a hung gate, lost a cycle at the retry budget, or merged on a stale PASS.
---

# Verify the /run pipeline

`/run` has seven phases as bare strings and a merge gate that depends on two
independent signals agreeing. Both are easy to weaken by accident: dropping one
signal still merges, and treating a hang like a failure still retries.

The property: **a run merges only when every slice landed AND a stated verdict is
PASS**, and **a hung gate never retries and never merges**.

## Does my change touch the verified code

```bash
git diff --stat <sync-commit>..HEAD -- \
  internal/tui/run.go internal/sop/verdict.go internal/tui/sop_status.go
```

**Model covers** (each model step names the function it stands for):

| Model action | Code |
|---|---|
| `Start` | `handleRunCommand` spawn, `run.go:548, 645` |
| `AgentFailedRetry` / `AgentFailedGiveUp` | `handleRunAgentDone` bad status, `run.go:872-893` |
| `AgentDoneNoGates` / `AgentDoneGates` | all agents finished, `run.go:900-917` |
| `GateResult` | the `runGateResultMsg` outcome, `run.go:1355` |
| `GatesPassed` | `run.go:1357-1365` |
| `GatesFailedRetry` / `GatesFailedGiveUp` | `run.go:1370-1385` |
| `GatesHung` | `reportGateHang`, `run.go:1366-1369, 1394-1420` |
| `ChecklistTicked` / `ChecklistLeftOpen` | `refreshRunChecklist` + `unfinishedSlices`, `run.go:922-932` |
| `ReadVerdict` | `sop.ParseVerdict`, `run.go:958`, `sop/verdict.go:84` |
| `VerifyPass` | the two-signal merge gate, `run.go:975-981` |
| `VerifyFailRetry` / `VerifyFailGiveUp` | `run.go:983-1000` |
| `RetrySpawned` / `RetryBudgetSpent` | `retryRun`, `run.go:1040-1115` |
| `MergeOk` / `MergeFailed` | the merge command result, `run.go:1500-1550` |

**Not modelled, and why:** the parallel-agent fan-in and
`collapseParallel`/`carried` (the model has one coordinator); gate execution
(timing, sandbox, `GateHang` detection by elapsed time); `writeRunSummary`;
`pr-autofix` (its own stage machine, derived from the SOP engine); the sidebar's
rendering of the phase (`sop_status.go`). A bug in any of those is invisible here.

## Run it

```bash
hack/formal/check.sh run           # all rules hold, or the first one that breaks
hack/formal/check.sh run --chart   # also draw the statechart
```

Current cost: 110 distinct states, well under a second. Exit `0` every rule
holds, `1` a rule broke, `2` TLC or the environment failed (never a verdict about
the code). The run begins with `go run ./hack/formal/lint/main`, so a structurally broken
model is reported before TLC is started.

## Read a result

Same three-way triage as `verify-goal-loop`: **a real bug** (replay the step
sequence against the real code as a test in `internal/tui`), **a modelling error**
(fix the model, not the rule), or **a rule stronger than the code promises**
(check the comment the rule cites; this model has already required restating
`NoStalePassAfterRetry`, because `retryRun` sets the phase to `retrying` *before*
the new coordinator clears the verdict).

## Rules, and the harm each one names

| Rule | The promise | What breaking it means for a user |
|---|---|---|
| `MergeRequiresBothSignals` | done ⇒ checklist whole AND stated PASS | half-implemented work merges; this is the failure `verifyRunComplete` exists to catch |
| `HangNeverRetries` | a hung gate never reaches `retrying` | the retry budget is spent re-running a command known not to return |
| `HangNeverMerges` | a hung gate never reaches `done` | unverified work ships over a gate that never answered |
| `RetriesBounded` | `retries <= MaxRetries` | an unbounded retry loop bills without end |
| `NoStalePassAfterRetry` | a running retry has no PASS in hand | cycle N's verdict satisfies cycle N+1's verification |

## The trap that made this model pass wrongly

**A rule over a variable that no action assigns is vacuously true.** The first
version of this model never assigned `gateOutcome` — it only *tested* it — so both
gate rules held over an empty set. A mutation that made a hung gate retry passed
cleanly. `go run ./hack/formal/lint/main` now fails the run for an unassigned variable and
for a rule whose premise needs a literal no action produces; both checks were
confirmed against this exact defect.

The second trap is the one `verify-goal-loop` documents: **two assignments to one
variable in one action are a conjunction**, so the contradicting branch is dead.
The lint catches that too. If you add an action, run the lint before trusting a
pass, and confirm a deliberate break reproduces:

```bash
# In a scratch copy, make GatesHung retry instead of fail:
#   add `/\ CanRetry` and change `phase' = "failed"` to `phase' = "retrying"`.
# HangNeverRetries must break.
```

## After confirming the model is still true

- **Sync commit:** `cc79aceb43df31012e9e5c1f69a7ba1231c32470`
- **Constants:** `MaxRetries = 2` (enough for one retry and one exhaustion).
- **Chart budget:** 110 states, so the full chart always draws.

Move the sync commit forward only after confirming the model describes the code
at the new commit, and record the new sha here.

## Draw the chart

`hack/formal/check.sh run --chart` writes `hack/formal/run/out/report.md` and
`chart.mmd` via the pstack `/statechart` skill. Read results with `/statechart`:
it renders each violation as the shortest path in the user's words.
