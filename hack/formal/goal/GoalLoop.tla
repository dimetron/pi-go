--------------------------- MODULE GoalLoop ---------------------------
\* A model of the /goal automatic loop.
\*
\* Code modelled (one step per function call):
\*   internal/tui/goal.go        setGoal, pauseGoal, resumeGoal, beginGoalCycle,
\*                               continueGoalLoop, clearGoal, goalMaxCycles
\*   internal/tui/agent_loop.go  handleAgentDone, cancelAgent, steerPrompt
\*   internal/tools/goal.go      GoalController.Complete
\*   internal/session/store.go   Goal.Status, GoalStatus
\*
\* Each step is one function call in the code, so a counterexample is a call
\* sequence a reviewer can follow. What the model is for: whether the completion
\* slot can close a goal it did not work on, and whether the cycle budget bounds
\* the automatic turns.

EXTENDS Naturals, Sequences

CONSTANTS MaxCycles, MaxBudget

VARIABLES
    status,       \* Goal.Status: "none" | "active" | "paused" | "complete"
    canceled,     \* m.goalCanceled, the in-memory stop flag
    budget,       \* goalMaxCycles(m.goal.MaxCycles), the current loop budget
    cycles,       \* Goal.Cycles, spent cycles of this objective
    turn,         \* Goal.Turn: the running cycle, 0 when the loop started none
    goalObj,      \* which objective the goal holds; only "same" vs "different"
                  \* matters, so it alternates between 1 and 2 to stay finite
    pending,      \* GoalController.pending: 0, or the cycle a report names
    reportObj,    \* the objective the pending report was made under
    completedObj, \* the objective a closure was based on
    inCycle,      \* the turn in flight is a goal cycle the loop started
    running,      \* m.running
    queued,       \* m.pendingPrompts is non-empty (a queued prompt exists)
    steering      \* m.steering

vars == <<status, canceled, budget, cycles, turn, goalObj, pending, reportObj,
          completedObj, inCycle, running, queued, steering>>

V == <<status, canceled, budget, cycles, turn, goalObj, pending, reportObj,
       completedObj, inCycle, running, queued, steering>>

\* continueGoalLoop's guard (goal.go:446-448) together with goalActive (goal.go:54).
Armed == status = "active" /\ ~canceled

\* beginGoalCycle's final branch: spend a cycle and start it (goal.go:270-296).
StartCycle ==
    /\ cycles < budget
    /\ cycles' = cycles + 1
    /\ turn' = cycles + 1
    /\ running' = TRUE
    /\ inCycle' = TRUE
    /\ queued' = FALSE
    /\ canceled' = FALSE
    /\ status' = status
    /\ steering' = steering
    /\ budget' = budget
    /\ goalObj' = goalObj
    /\ pending' = pending
    /\ reportObj' = reportObj
    /\ completedObj' = completedObj

-------------------------------------------------------------------------------
\* Entry points, each named for the Go function or command it stands for.
-------------------------------------------------------------------------------

\* /goal <objective> while idle: setGoal (goal.go:63-93) then beginGoalCycle.
\* setGoal starts a fresh budget and drops any leftover report (goal.go:84-87),
\* so a report from the previous objective can never be read by the new one.
SetGoalIdle ==
    /\ ~running
    /\ status /= "active"
    /\ status' = "active"
    /\ canceled' = FALSE
    /\ budget' = MaxCycles
    /\ goalObj' = IF goalObj = 1 THEN 2 ELSE 1
    /\ cycles' = 1
    /\ turn' = 1
    /\ pending' = 0
    /\ reportObj' = 0
    /\ completedObj' = completedObj
    /\ inCycle' = TRUE
    /\ running' = TRUE
    /\ queued' = FALSE
    /\ steering' = FALSE

\* /goal <objective> while a turn runs: setGoal runs, then handleGoalCommand
\* returns early on m.running (goal.go:395-397), so no cycle starts and Turn
\* stays 0. This is the "a turn the loop did not start" shape the loop refuses.
SetGoalBusy ==
    /\ running
    /\ status /= "active"
    /\ status' = "active"
    /\ canceled' = FALSE
    /\ budget' = MaxCycles
    /\ goalObj' = IF goalObj = 1 THEN 2 ELSE 1
    /\ cycles' = 0
    /\ turn' = 0
    /\ pending' = 0
    /\ reportObj' = 0
    /\ completedObj' = completedObj
    /\ inCycle' = inCycle
    /\ running' = running
    /\ queued' = queued
    /\ steering' = steering

\* pauseGoal (goal.go:158-180): relabels and sets canceled. It does NOT drain
\* the completion slot, which is why a report can outlive the cycle.
Pause ==
    /\ status = "active"
    /\ status' = "paused"
    /\ canceled' = TRUE
    /\ running' = FALSE
    /\ inCycle' = FALSE
    /\ budget' = budget
    /\ cycles' = cycles
    /\ turn' = turn
    /\ goalObj' = goalObj
    /\ pending' = pending
    /\ reportObj' = reportObj
    /\ completedObj' = completedObj
    /\ queued' = queued
    /\ steering' = steering

\* Esc during a cycle: cancelAgent sets goalCanceled and leaves Status Active
\* (agent_loop.go:835-842). It does not drain the slot either.
Stop ==
    /\ status = "active"
    /\ ~canceled
    /\ canceled' = TRUE
    /\ running' = FALSE
    /\ status' = status
    /\ budget' = budget
    /\ cycles' = cycles
    /\ turn' = turn
    /\ goalObj' = goalObj
    /\ pending' = pending
    /\ reportObj' = reportObj
    /\ completedObj' = completedObj
    /\ inCycle' = FALSE
    /\ queued' = queued
    /\ steering' = steering

\* clearGoal (goal.go:144-157): drops the goal and drains the slot.
Clear ==
    /\ status /= "none"
    /\ status' = "none"
    /\ canceled' = TRUE
    /\ pending' = 0
    /\ reportObj' = 0
    /\ running' = FALSE
    /\ inCycle' = FALSE
    /\ completedObj' = completedObj
    /\ budget' = budget
    /\ cycles' = cycles
    /\ turn' = turn
    /\ goalObj' = goalObj
    /\ queued' = queued
    /\ steering' = steering

\* resumeGoal (goal.go:190-225) has three outcomes, so it is three actions.
\* It refuses Complete outright (goal.go:195-201), re-arms the loop, renews a
\* spent budget (goal.go:213-215), then falls into beginGoalCycle. That call
\* reads the slot BEFORE spending a cycle (goal.go:245 vs :270).

\* A report left in the slot closes the goal it names.
ResumeClose ==
    /\ status \in {"active", "paused"}
    /\ canceled
    /\ pending /= 0
    /\ pending = turn
    /\ status' = "complete"
    /\ completedObj' = reportObj
    /\ pending' = 0
    /\ reportObj' = 0
    /\ inCycle' = FALSE
    /\ canceled' = canceled
    /\ budget' = budget
    /\ cycles' = cycles
    /\ turn' = turn
    /\ goalObj' = goalObj
    /\ running' = FALSE
    /\ queued' = queued
    /\ steering' = FALSE

\* Re-arm, then stop again because the budget is still spent.
ResumeStopped ==
    /\ status \in {"active", "paused"}
    /\ canceled
    /\ ~(pending /= 0 /\ pending = turn)
    /\ cycles >= budget
    /\ status' = "active"
    /\ canceled' = TRUE
    /\ budget' = budget
    /\ cycles' = cycles
    /\ turn' = turn
    /\ goalObj' = goalObj
    /\ pending' = pending
    /\ reportObj' = reportObj
    /\ completedObj' = completedObj
    /\ inCycle' = FALSE
    /\ running' = FALSE
    /\ queued' = queued
    /\ steering' = FALSE

\* Re-arm, renew the budget, and spend the next cycle.
ResumeRenew ==
    /\ status \in {"active", "paused"}
    /\ canceled
    /\ ~(pending /= 0 /\ pending = turn)
    /\ cycles < budget
    /\ status' = "active"
    /\ budget' = IF budget + MaxCycles <= MaxBudget
                THEN budget + MaxCycles ELSE budget
    /\ StartCycle

\* The turn ends with a prompt queued: startNextPrompt wins over the loop
\* (agent_loop.go:2070-2071).
TurnEndQueued ==
    /\ running
    /\ queued
    /\ running' = TRUE
    /\ queued' = FALSE
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj, pending,
                  reportObj, completedObj, inCycle, steering>>

\* continueGoalLoop's refusals, in the order it checks them (goal.go:446-456).
TurnEndRefuse ==
    /\ running
    /\ ~queued
    /\ ~Armed
    /\ running' = FALSE
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj, pending,
                  reportObj, completedObj, inCycle, queued, steering>>

TurnEndSteer ==
    /\ running
    /\ ~queued
    /\ Armed
    /\ steering
    /\ running' = FALSE
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj, pending,
                  reportObj, completedObj, inCycle, queued, steering>>

\* turn = 0 means the loop did not start this turn; the report is drained and
\* dropped (goal.go:450-455).
TurnEndNoCycle ==
    /\ running
    /\ ~queued
    /\ Armed
    /\ ~steering
    /\ turn = 0
    /\ running' = FALSE
    /\ pending' = 0
    /\ reportObj' = 0
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj,
                  completedObj, inCycle, queued, steering>>

\* A real cycle's turn ends: continueGoalLoop calls beginGoalCycle (goal.go:460).
BeginCycle ==
    /\ running
    /\ ~queued
    /\ Armed
    /\ ~steering
    /\ turn /= 0
    /\ IF pending /= 0 /\ pending = turn
       THEN
         \* the goal closes on this report (goal.go:245-266)
         /\ status' = "complete"
         /\ completedObj' = reportObj
         /\ inCycle' = FALSE
         /\ UNCHANGED <<canceled, budget, cycles, turn, goalObj, pending,
                       reportObj, running, queued, steering>>
       ELSE IF cycles >= budget
       THEN
         \* budget spent: stop and leave Status Active (goal.go:269-280)
         /\ canceled' = TRUE
         /\ running' = FALSE
         /\ UNCHANGED <<status, budget, cycles, turn, goalObj, pending,
                       reportObj, completedObj, inCycle, queued, steering>>
       ELSE
         /\ StartCycle

\* The agent calls goal_complete (internal/tools/goal.go:135-182). The tool
\* accepts only cycle == the published Turn; it knows nothing of objectives.
ToolComplete(c) ==
    /\ running
    /\ c /= 0
    /\ c = turn
    /\ pending' = c
    /\ reportObj' = goalObj
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj,
                  completedObj, inCycle, running, queued, steering>>

\* steering replaces the turn the loop was running (agent_loop.go:963).
Steer ==
    /\ running
    /\ steering' = TRUE
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj, pending,
                  reportObj, completedObj, inCycle, running, queued>>

\* An ordinary prompt with no goal cycle behind it.
ManualTurn ==
    /\ ~running
    /\ running' = TRUE
    /\ UNCHANGED <<status, canceled, budget, cycles, turn, goalObj, pending,
                  reportObj, completedObj, inCycle, queued, steering>>

\* The stutter every state has, so NoDeadlock stays meaningful.
Done ==
    UNCHANGED V

Next ==
    \/ SetGoalIdle     \/ SetGoalBusy
    \/ ResumeClose     \/ ResumeStopped \/ ResumeRenew
    \/ Pause           \/ Stop         \/ Clear
    \/ TurnEndQueued   \/ TurnEndRefuse \/ TurnEndSteer \/ TurnEndNoCycle
    \/ BeginCycle
    \/ \E c \in 1..MaxBudget : ToolComplete(c)
    \/ Steer           \/ ManualTurn
    \/ Done

Init ==
    /\ status = "none"    /\ canceled = TRUE
    /\ budget = MaxCycles /\ cycles = 0
    /\ turn = 0           /\ goalObj = 0
    /\ pending = 0        /\ reportObj = 0
    /\ completedObj = 0
    /\ inCycle = FALSE
    /\ running = FALSE    /\ queued = FALSE /\ steering = FALSE

FairSpec == Init /\ [][Next]_vars

-------------------------------------------------------------------------------
\* Rules the reader agreed to.
-------------------------------------------------------------------------------

\* store.go:96 "Only Active drives work": no cycle the loop started is in flight
\* while the goal is not active. Stated on turn as well as running, because
\* pauseGoal leaves Turn stamped after the turn is gone.
OnlyActiveRuns == inCycle => status = "active"

\* store.go:96-99 "Complete is terminal ... not reversible by /goal resume".
\* No new cycle begins after Complete. An ordinary prompt may still run: the
\* goal is finished, the session is not.
CompleteIsTerminal == status = "complete" => ~inCycle

\* goal.go:269 -- the budget bounds the automatic turns of one objective.
BudgetBounded == cycles <= budget

\* goal.go:232-271 -- a cycle in flight is a cycle that was spent.
InFlightCycleIsCurrent == inCycle => turn <= cycles

\* tools/goal.go:146-172 -- a report in the slot names the running cycle.
SlotAttributable == pending = 0 \/ pending <= turn

\* goal.go:239-244 -- "a report that survived a stop [cannot] close a goal it
\* did not work on". A closure is always based on a report about this objective,
\* which is what setGoal's drain (goal.go:87) and clearGoal's drain buy.
CompletionMatchesObjective == status = "complete" => completedObj = goalObj

=============================================================================
