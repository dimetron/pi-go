---
name: verify-goal-loop
description: Formally verify the /goal automatic loop before or after changing it. Use when editing internal/tui/goal.go, internal/tui/agent_loop.go's handleAgentDone/cancelAgent/steerPrompt, or internal/tools/goal.go's GoalController — or when a goal loop will not stop, stops one cycle early, closes a goal that is not met, or restarts a goal you finished.
---

# Verify the /goal loop

The `/goal` loop is a state machine spread over four files, and its interesting
bug is an ordering one: which of two things `beginGoalCycle` reads first. Nothing
in a single-unit test can see a state that only a sequence produces, which is why
this model exists.

The property: **a completion reported under one objective can never close a
different one**, and the cycle budget bounds the automatic turns.

## Does my change touch the verified code

Run this; empty output means the model still describes the code.

```bash
git diff --stat <sync-commit>..HEAD -- \
  internal/tui/goal.go internal/tui/agent_loop.go internal/tools/goal.go \
  internal/session/store.go internal/tui/tui.go
```

**Model covers** (each model step names the function it stands for):

| Model action | Code |
|---|---|
| `SetGoalIdle` | `setGoal` + `beginGoalCycle` first cycle, `goal.go:63-93, 270-296` |
| `SetGoalBusy` | `setGoal` then the early return on `m.running`, `goal.go:392-399` |
| `Pause` | `pauseGoal`, `goal.go:158-180` |
| `Stop` | `cancelAgent`'s goal branch, `agent_loop.go:835-842` |
| `ResumeClose` / `ResumeStopped` / `ResumeRenew` | `resumeGoal`, `goal.go:190-225` |
| `Clear` | `clearGoal`, `goal.go:144-157` |
| `TurnEndQueued` / `TurnEndRefuse` / `TurnEndSteer` / `TurnEndNoCycle` | `handleAgentDone` → `continueGoalLoop`, `agent_loop.go:2013-2075`, `goal.go:445-460` |
| `BeginCycle` | `beginGoalCycle`'s slot read then budget check, `goal.go:245-296` |
| `ToolComplete` | `GoalController.Complete`, `internal/tools/goal.go:135-182` |
| `Steer` | `steerPrompt`, `agent_loop.go:963` |
| `ManualTurn` | an ordinary prompt with no cycle behind it |

**Not modelled, and why:** the TUI event loop and its goroutines (the model
assumes a step completes before the next starts); `persistGoal` and session
restore; `AppendActiveGoal`'s prompt text; the wall-clock `UpdatedAt` stamps;
`plan` mode; and the `turn_complete`/`user_input_required` lifecycle hooks.
A bug in any of those is invisible to this check.

## Run it

```bash
hack/formal/check.sh goal           # all rules hold, or the first one that breaks
hack/formal/check.sh goal --chart   # also draw the statechart into hack/formal/goal/out
hack/formal/check.sh all            # every model under hack/formal
```

TLC and a JDK are found, not installed: the script probes `JAVA`, then `java` on
`PATH`, then Homebrew's keg-only `openjdk`, then
`/Library/Java/JavaVirtualMachines/*`. Override with `JAVA=/path/to/java` and
`TLA2TOOLS=/path/to/tla2tools.jar`. Without `TLA2TOOLS` it downloads the pinned
`tla2tools` 1.7.4 into `~/.cache/pi-go-formal` **and verifies its sha256**, so a
run cannot silently use a different checker.

Runtime is well under a second at the current constants. Exit codes: `0` every
rule holds, `1` a rule broke, `2` TLC or the environment failed — a `2` is never a
verdict about the code.

## Read a result

**Every rule holds.** The model still describes the code within the scope above.

**A rule broke.** TLC names the rule and the shortest step sequence. Read that
sequence as function calls, in order. Then decide which of three things happened:

1. **A real bug.** The sequence is reachable in the product. Replay it against
   the real code as a Go test in `internal/tui` — the pattern is
   `goal_loop_e2e_test.go`'s `runTurns`, which drives `beginGoalCycle` and
   `handleAgentDone` through a real agent. Report it with the sequence; do not
   weaken the rule and do not change product code unless asked.
2. **A modelling error.** The sequence turns on something the model made
   indivisible that the code does not, or on a variable that does not mean what
   the rule assumes. Fix the model, never the rule.
3. **A rule that is stronger than the code promises.** Check the comment the
   rule cites. If the code does not claim it, the rule is wrong and must be
   restated or removed. This happened twice while building this model
   (`turn /= 0` does not mean "a cycle is in flight", because `pauseGoal` leaves
   `Turn` stamped; and "the goal is finished" does not mean "no turn may run").

**TLC cannot run.** Exit `2`. Read the message: a missing variable means an
action does not assign it, which TLA+ reports as `Successor state is not
completely specified`.

## Rules, and the harm each one names

| Rule | The promise | What breaking it means for a user |
|---|---|---|
| `OnlyActiveRuns` | a cycle never runs for a goal that is not active | a paused goal keeps spending billed turns |
| `CompleteIsTerminal` | no cycle begins after Complete | `/goal resume` restarts finished work, the loop the comment forbids |
| `BudgetBounded` | `cycles <= budget` | a runaway loop bills without end |
| `InFlightCycleIsCurrent` | a running cycle was actually spent | the budget under-counts and the cap is not a cap |
| `SlotAttributable` | a report in the slot names the running cycle | a stale report is read as a fresh one |
| `CompletionMatchesObjective` | a closure is based on a report about *this* objective | the previous objective's "done" closes the new one |

## Two traps this model has already fallen into

**A top-level assignment plus a branch assignment is a conjunction.** Writing
`/\ canceled' = FALSE` and then, in a nested `IF` branch, `/\ canceled' = TRUE`
makes that branch unsatisfiable, and the action silently never fires. It cost a
false pass on the very property this model is for. Write such actions as
disjoint alternatives (see `ResumeClose`/`ResumeStopped`/`ResumeRenew`), and
after any edit run this audit — it lists every action that assigns one variable
twice:

```bash
python3 - <<'PY'
import pathlib, re, collections
s = pathlib.Path("hack/formal/goal/GoalLoop.tla").read_text()
for part in re.split(r"\n(?=\w+ ==)", s):
    name = part.split("==")[0].strip()
    if not name or name in ("Init","Next","FairSpec") or "==" not in part: continue
    d = {k:v for k,v in collections.Counter(re.findall(r"(\w+)'\s*=", part)).items() if v>1}
    if d: print(f"{name}: duplicate assignments {d}")
PY
```

**A small state count can mean the model never ran.** An early version reported
"all rules hold" on 16 distinct states because `SetGoalIdle` never started a
cycle, so no loop ever existed. Check the count is in the hundreds and that a
deliberately broken model **fails**:

```bash
# In a scratch copy, delete setGoal's drain (goal.go:87) -- set pending' = pending
# instead of pending' = 0 in SetGoalIdle. CompletionMatchesObjective must break.
```

If a break does not reproduce, the model is wrong and its passing result means
nothing.

## After confirming the model is still true

Move the sync commit forward only once you have confirmed the model describes the
code at the new commit:

```bash
git log -1 --format=%H    # the commit you verified
```

Record it below, replacing the old value. Do not move it because the diff looked
unrelated.

- **Sync commit:** `cc79aceb43df31012e9e5c1f69a7ba1231c32470`
- **Constants:** `MaxCycles = 1`, `MaxBudget = 3` (chosen to stay under the
  chart budget of 500 states; `MaxCycles = 2, MaxBudget = 4` gives 556 states and
  the same verdicts, and is what to use when you want the wider bound without a
  chart).

## Draw the chart

`hack/formal/check.sh goal --chart` writes `hack/formal/goal/out/report.md` and
`chart.mmd`, and runs the pstack `/statechart` skill. Read every TLA+ result with
`/statechart`: it turns each violation into the shortest path in the user's
words and, when nothing breaks, draws how the system moves between states. When
the chart hides steps, the report says how many.
