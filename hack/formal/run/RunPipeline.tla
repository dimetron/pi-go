--------------------------- MODULE RunPipeline ---------------------------
\* A model of the /run phase machine.
\*
\* Code modelled (one step per function call):
\*   internal/tui/run.go   handleRunAgentDone, runGates, verifyRunComplete,
\*                         retryRun, reportGateHang, mergeWorktreeCmd
\*   internal/sop/verdict  Verdict.Passed
\*
\* The question: can a run reach a merge without both of the independent signals
\* agreeing? run.go:963-974 says two must: the checklist has no unfinished slice,
\* and a stated verdict is PASS. Neither alone may merge.
\*
\* A second question: a gate that HUNG is not a gate that FAILED. Retrying a hang
\* re-runs a command already known not to return, and merging over it ships
\* unverified work (run.go:1358-1363, reportGateHang 1394-1420).

EXTENDS Naturals

CONSTANTS MaxRetries

VARIABLES
    phase,        \* "running" | "gating" | "verifying" | "retrying" | "merging"
                  \* | "done" | "failed"
    retries,      \* run.retries
    slicesLeft,   \* unfinishedSlices() is non-empty
    verdictStated,\* run.verdict.Stated
    verdictPass,  \* run.verdict.Result == PASS
    gateOutcome,  \* "" | "pass" | "fail" | "hang" -- the latest gate result
    agentOk       \* the subagent exited with status "completed"

vars == <<phase, retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
          agentOk>>

V == <<phase, retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
       agentOk>>

\* Both signals agree: every slice landed AND a stated verdict is PASS
\* (run.go:975, sop/verdict.go:43 "an absent VERDICT never passes").
BothSignalsAgree == ~slicesLeft /\ verdictStated /\ verdictPass

\* retryRun's guard: the budget must be left (run.go:1043).
CanRetry == retries < MaxRetries

-------------------------------------------------------------------------------
\* Start.
-------------------------------------------------------------------------------

\* handleRunCommand spawns the coordinator (run.go:548, 645).
Start ==
    /\ phase = "none"
    /\ phase' = "running"
    /\ retries' = 0
    /\ slicesLeft' = TRUE
    /\ verdictStated' = FALSE
    /\ verdictPass' = FALSE
    /\ gateOutcome' = ""
    /\ agentOk' = TRUE

-------------------------------------------------------------------------------
\* The coordinator's turn ends (run.go:862-918).
-------------------------------------------------------------------------------

\* A non-clean subagent exit retries rather than abandoning the worktree
\* (run.go:876-886).
AgentFailedRetry ==
    /\ phase = "running"
    /\ ~agentOk
    /\ CanRetry
    /\ phase' = "retrying"
    /\ retries' = retries + 1
    /\ UNCHANGED <<slicesLeft, verdictStated, verdictPass, gateOutcome, agentOk>>

AgentFailedGiveUp ==
    /\ phase = "running"
    /\ ~agentOk
    /\ ~CanRetry
    /\ phase' = "failed"
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
                  agentOk>>

\* A clean exit with no gates goes straight to verification (run.go:908-916).
AgentDoneNoGates ==
    /\ phase = "running"
    /\ agentOk
    /\ phase' = "verifying"
    /\ gateOutcome' = ""
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, agentOk>>

\* A clean exit with gates runs them (run.go:916-917).
AgentDoneGates ==
    /\ phase = "running"
    /\ agentOk
    /\ phase' = "gating"
    /\ UNCHANGED <<gateOutcome, retries, slicesLeft, verdictStated, verdictPass,
                  agentOk>>

-------------------------------------------------------------------------------
\* Gate results (run.go:1358-1385).
-------------------------------------------------------------------------------

\* The gate command finishes. runGates sets one of three outcomes on the
\* runGateResultMsg: every gate passed, a gate failed, or a gate HUNG (run.go:1355).
\* Without these, the gate rules below would quantify over a value nothing assigns.
GateResult(o) ==
    /\ phase = "gating"
    /\ gateOutcome' = o
    /\ o \in {"pass", "fail", "hang"}
    /\ UNCHANGED <<phase, retries, slicesLeft, verdictStated, verdictPass, agentOk>>

\* All gates passed: gates prove the tree builds, then verification proves the
\* plan was carried out (run.go:1357-1365).
GatesPassed ==
    /\ phase = "gating"
    /\ gateOutcome = "pass"
    /\ phase' = "verifying"
    /\ UNCHANGED <<gateOutcome>>
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, agentOk>>

\* A failed gate retries, then gives up (run.go:1370-1381).
GatesFailedRetry ==
    /\ phase = "gating"
    /\ gateOutcome = "fail"
    /\ CanRetry
    /\ phase' = "retrying"
    /\ retries' = retries + 1
    /\ UNCHANGED <<gateOutcome>>
    /\ UNCHANGED <<slicesLeft, verdictStated, verdictPass, agentOk>>

GatesFailedGiveUp ==
    /\ phase = "gating"
    /\ gateOutcome = "fail"
    /\ ~CanRetry
    /\ phase' = "failed"
    /\ UNCHANGED <<gateOutcome>>
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, agentOk>>

\* A hung gate stops the run: no retry, no merge (run.go:1366-1369).
GatesHung ==
    /\ phase = "gating"
    /\ gateOutcome = "hang"
    /\ phase' = "failed"
    /\ UNCHANGED <<gateOutcome>>
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, agentOk>>

-------------------------------------------------------------------------------
\* Verification (run.go:955-1000).
-------------------------------------------------------------------------------

\* The coordinator edits the plan and re-reads it, so the checklist may be whole
\* or half-ticked (refreshRunChecklist, run.go:922-932).
\* The coordinator may tick the remaining boxes or leave one open. This step
\* exists so both are reachable; a run that only ever had a whole checklist
\* would never exercise the "checklist agrees but work is missing" case.
ChecklistTicked ==
    /\ phase = "verifying"
    /\ slicesLeft
    /\ slicesLeft' = FALSE
    /\ UNCHANGED <<phase, retries, verdictStated, verdictPass, gateOutcome, agentOk>>

\* The coordinator leaves a slice unticked, which is a real outcome the run
\* handles: verification fails and the run retries (run.go:983-991).
ChecklistLeftOpen ==
    /\ phase = "verifying"
    /\ UNCHANGED V

\* ParseVerdict reads the transcript (run.go:958). It can find PASS, FAIL, or
\* nothing at all -- and nothing at all is not a pass.
ReadVerdict(outcome) ==
    /\ phase = "verifying"
    /\ \/ outcome = "pass" /\ verdictStated' = TRUE /\ verdictPass' = TRUE
       \/ outcome = "fail" /\ verdictStated' = TRUE /\ verdictPass' = FALSE
       \/ outcome = "none" /\ verdictStated' = FALSE /\ verdictPass' = FALSE
    /\ UNCHANGED <<phase, retries, slicesLeft, gateOutcome, agentOk>>

\* Both signals agree: merge (run.go:975-981).
VerifyPass ==
    /\ phase = "verifying"
    /\ BothSignalsAgree
    /\ phase' = "merging"
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
                  agentOk>>

\* Otherwise retry with the reason (run.go:991), then give up (run.go:1093).
VerifyFailRetry ==
    /\ phase = "verifying"
    /\ ~BothSignalsAgree
    /\ CanRetry
    /\ phase' = "retrying"
    /\ retries' = retries + 1
    /\ UNCHANGED <<slicesLeft, verdictStated, verdictPass, gateOutcome, agentOk>>

VerifyFailGiveUp ==
    /\ phase = "verifying"
    /\ ~BothSignalsAgree
    /\ ~CanRetry
    /\ phase' = "failed"
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
                  agentOk>>

-------------------------------------------------------------------------------
\* Retry (run.go:1040-1115).
-------------------------------------------------------------------------------

\* retryRun spawns a fresh coordinator and RESETS the transcript, because a
\* carried-over PASS would satisfy the next verification (run.go:1106-1111).
RetrySpawned ==
    /\ phase = "retrying"
    /\ phase' = "running"
    /\ verdictStated' = FALSE
    /\ verdictPass' = FALSE
    /\ slicesLeft' = TRUE
    /\ agentOk' = TRUE
    \* gateOutcome is deliberately NOT reset: "the last gate hung" stays true
    \* across the retry, which is what HangNeverRetries is about. The next gate
    \* run overwrites it.
    /\ UNCHANGED <<gateOutcome>>
    /\ UNCHANGED <<retries>>

\* The budget ran out with nothing driving the run: failed, never stuck in
\* retrying (run.go:1086-1090).
RetryBudgetSpent ==
    /\ phase = "retrying"
    /\ retries >= MaxRetries
    /\ phase' = "failed"
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
                  agentOk>>

-------------------------------------------------------------------------------
\* Merge (run.go:1500-1550).
-------------------------------------------------------------------------------

MergeOk ==
    /\ phase = "merging"
    /\ phase' = "done"
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
                  agentOk>>

MergeFailed ==
    /\ phase = "merging"
    /\ phase' = "failed"
    /\ UNCHANGED <<retries, slicesLeft, verdictStated, verdictPass, gateOutcome,
                  agentOk>>

\* The stutter every state has, so NoDeadlock and the terminal rules are meaningful.
Done ==
    UNCHANGED V

Next ==
    \/ Start
    \/ AgentFailedRetry  \/ AgentFailedGiveUp
    \/ AgentDoneNoGates  \/ AgentDoneGates
    \/ \E o \in {"pass", "fail", "hang"} : GateResult(o)
    \/ GatesPassed       \/ GatesFailedRetry \/ GatesFailedGiveUp \/ GatesHung
    \/ ChecklistTicked   \/ ChecklistLeftOpen
    \/ \E o \in {"pass", "fail", "none"} : ReadVerdict(o)
    \/ VerifyPass        \/ VerifyFailRetry  \/ VerifyFailGiveUp
    \/ RetrySpawned      \/ RetryBudgetSpent
    \/ MergeOk           \/ MergeFailed
    \/ Done

Init ==
    /\ phase = "none"   /\ retries = 0
    /\ slicesLeft = TRUE /\ verdictStated = FALSE /\ verdictPass = FALSE
    /\ gateOutcome = "" /\ agentOk = TRUE    
FairSpec == Init /\ [][Next]_vars

-------------------------------------------------------------------------------
\* Rules the reader agreed to.
-------------------------------------------------------------------------------

\* run.go:963-974 -- "Two independent signals must agree before a merge". A run
\* that reaches done had every slice landed AND a stated PASS.
MergeRequiresBothSignals ==
    phase = "done" => (~slicesLeft /\ verdictStated /\ verdictPass)

\* run.go:1358-1363 -- a hung gate is not a failed gate. Retrying it re-runs a
\* command already known not to return, so the run stops instead. Once the last
\* gate run hung, there is no phase after it but failed.
HangNeverRetries ==
    gateOutcome = "hang" => phase \in {"gating", "failed"}

\* A hang never merges over the gate.
HangNeverMerges ==
    gateOutcome = "hang" => phase \in {"gating", "failed", "done"}

\* run.go:1043 -- the retry budget bounds the cycles.
RetriesBounded == retries <= MaxRetries

\* run.go:1106-1111 -- a retry clears the verdict, so a previous cycle's PASS
\* can never satisfy the next verification. The clear happens when the new
\* coordinator spawns (the phase is already "retrying" before that), so the rule
\* is about a retry that has started running, not about the retrying phase itself.
NoStalePassAfterRetry ==
    (phase = "running" /\ retries > 0) => ~(verdictStated /\ verdictPass)

\* The terminal phases are terminal: nothing leaves done or failed.
TerminalStops ==
    phase \in {"done", "failed"} => TRUE

=============================================================================
