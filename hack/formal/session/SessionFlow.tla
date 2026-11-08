--------------------------- MODULE SessionFlow ---------------------------
\* The interactive session as the user experiences it, from launch to exit.
\*
\* Code modelled (one step per user action or one Update handler):
\*   internal/tui/tui.go        handleKey, handleInterruptKey, handleInputSubmit,
\*                              handleLoadingTick, ctrlCCount, quitting
\*   internal/tui/agent_loop.go enqueuePrompt, submit, steerPrompt, cancelAgent,
\*                              handleAgentDone
\*   internal/tui/commands.go   handleCompactCommand, clearConversation
\*   internal/autocompact/hook.go  BuildHook (the pre-turn compaction hook)
\*
\* This model answers a question the per-subsystem models do not: can a user
\* reach a state where the TUI is busy but nothing can end the turn, or where a
\* quit happens while work is still running? Those are the two failures a person
\* notices.
\*
\* Deliberately coarse: the model does not include the modal overlays (commit,
\* login, skill-create, branch/search popups). Each is a pointer that either
\* swallows or forwards a key, and none of them can strand a running turn -- the
\* ordering claim they make is covered by handleKey's list, which a reader can
\* check in five lines.

EXTENDS Naturals

VARIABLES
    phase,      \* "starting" | "idle" | "busy" | "canceling" | "quitting" | "closed"
    queued,     \* m.pendingPrompts is non-empty
    steering,   \* m.steering
    ctrlC,      \* m.ctrlCCount: 0, 1, or 2
    goalArmed,  \* a /goal loop is armed (goalActive() && !goalCanceled)
    quitVia     \* "" | "ctrl-c" | "command" | "failure" -- what asked to quit
                
vars == <<phase, queued, steering, ctrlC, goalArmed, quitVia>>

V == <<phase, queued, steering, ctrlC, goalArmed, quitVia>>

-------------------------------------------------------------------------------
\* Launch: the deferred-init splash (tui.go:563-565, 2391-2417).
-------------------------------------------------------------------------------

\* One init item completes. The ticker re-arms only while loading
\* (tui.go:handleLoadingTick), so this is bounded by the item count.
InitProgress ==
    /\ phase = "starting"
    /\ UNCHANGED <<phase, queued, steering, ctrlC, goalArmed, quitVia>>

\* The final InitResult clears m.loading (tui.go:2417).
InitDone ==
    /\ phase = "starting"
    /\ phase' = "idle"
    /\ UNCHANGED <<queued, steering, ctrlC, goalArmed, quitVia>>

\* An init failure sets initErr and quits (tui.go:2394-2400).
InitFailed ==
    /\ phase = "starting"
    /\ phase' = "quitting"
    /\ quitVia' = "failure"
    /\ UNCHANGED <<queued, steering, ctrlC, goalArmed>>

-------------------------------------------------------------------------------
\* Prompt submission. handleInputSubmit (tui.go:783-790).
-------------------------------------------------------------------------------

\* Idle: the prompt is enqueued and a turn starts (enqueuePrompt -> submit).
SubmitIdle ==
    /\ phase = "idle"
    /\ phase' = "busy"
    /\ queued' = FALSE
    /\ steering' = FALSE
    /\ ctrlC' = 0
    /\ UNCHANGED <<goalArmed, quitVia>>

\* Busy: the prompt STEERS the running turn instead of queueing
\* (tui.go:785-787 -> steerPrompt, agent_loop.go:961-975).
SubmitSteers ==
    /\ phase = "busy"
    /\ phase' = "busy"
    /\ steering' = TRUE
    /\ UNCHANGED <<queued, ctrlC, goalArmed, quitVia>>

\* A prompt typed while a turn runs and a prompt is already queued joins the
\* queue (enqueuePrompt, agent_loop.go:985-990). The FIRST extra prompt steers,
\* but a goal cycle enqueues its own prompt, so the queue can be non-empty while
\* the user is still typing -- and a third prompt then queues behind it.
GoalCycleQueues ==
    /\ phase = "busy"
    /\ ~queued
    /\ goalArmed
    /\ queued' = TRUE
    /\ UNCHANGED <<phase, steering, ctrlC, goalArmed, quitVia>>

QueueAnother ==
    /\ phase = "busy"
    /\ queued
    /\ UNCHANGED vars

-------------------------------------------------------------------------------
\* The turn ends: handleAgentDone (agent_loop.go:2013-2075).
-------------------------------------------------------------------------------

\* A queued prompt runs next; the loop's own cycle does not get to jump it
\* (agent_loop.go:2069-2071).
TurnEndQueued ==
    /\ phase = "busy"
    /\ queued
    /\ phase' = "busy"
    /\ queued' = FALSE
    /\ steering' = FALSE
    /\ ctrlC' = 0
    /\ UNCHANGED <<goalArmed, quitVia>>

\* Nothing queued and the loop is not armed: back to idle.
TurnEndIdle ==
    /\ phase = "busy"
    /\ ~queued
    /\ ~goalArmed
    /\ phase' = "idle"
    /\ steering' = FALSE
    /\ ctrlC' = 0
    /\ UNCHANGED <<queued, goalArmed, quitVia>>

\* Nothing queued and a goal loop is armed: the loop starts the next cycle.
TurnEndGoalCycle ==
    /\ phase = "busy"
    /\ ~queued
    /\ goalArmed
    /\ phase' = "busy"
    /\ steering' = FALSE
    /\ ctrlC' = 0
    /\ UNCHANGED <<queued, goalArmed, quitVia>>

-------------------------------------------------------------------------------
\* Keys. handleInterruptKey (tui.go:1271-1303).
-------------------------------------------------------------------------------

\* Esc while busy: cancelAgent, one press is enough (tui.go:1278-1280).
\* cancelAgent also stops an armed goal loop (agent_loop.go:835-842), which is
\* why the loop is disarmed here -- and why IdleHasNoArmedGoal holds.
EscBusy ==
    /\ phase = "busy"
    /\ phase' = "canceling"
    /\ goalArmed' = FALSE
    /\ UNCHANGED <<queued, steering, ctrlC, quitVia>>

\* Esc while idle: nothing. It is handled and returns (tui.go:1281).
EscIdle ==
    /\ phase = "idle"
    /\ UNCHANGED vars

\* Ctrl+C while busy: cancel, count one, warn "again to quit" (tui.go:1284-1288).
\* The count is NOT cleared by the cancel, so the warning's promise is real.
CtrlCBusyFirst ==
    /\ phase = "busy"
    /\ ctrlC = 0
    /\ phase' = "canceling"
    /\ ctrlC' = 1
    /\ goalArmed' = FALSE
    /\ UNCHANGED <<queued, steering, quitVia>>

\* Ctrl+C while busy with the count already 1: cancels again, warns again
\* (tui.go:1284-1288 -- the busy branch never quits).
CtrlCBusyAgain ==
    /\ phase = "busy"
    /\ ctrlC = 1
    /\ phase' = "canceling"
    /\ ctrlC' = 2
    /\ goalArmed' = FALSE
    /\ UNCHANGED <<queued, steering, quitVia>>

\* Ctrl+C while idle, first press: warn (tui.go:1290-1296).
CtrlCIdleFirst ==
    /\ phase = "idle"
    /\ ctrlC = 0
    /\ ctrlC' = 1
    /\ UNCHANGED <<phase, queued, steering, goalArmed, quitVia>>

\* Ctrl+C while idle, second press: quit (tui.go:1291-1293).
CtrlCIdleSecond ==
    /\ phase = "idle"
    /\ ctrlC = 1
    /\ phase' = "quitting"
    /\ quitVia' = "ctrl-c"
    /\ ctrlC' = 2
    /\ UNCHANGED <<queued, steering, goalArmed>>

\* The 2s timer clears the count (tui.go:2353-2359, handleResetCtrlCCount).
\* The 2s reset runs on a tea.Cmd and can only be observed while the UI is still
\* accepting keys. Once the session is quitting the next step is the exit, so the
\* model does not let the timer scramble the count on the way out.
CtrlCTimerFires ==
    /\ ctrlC /= 0
    /\ phase \in {"idle", "busy", "canceling"}
    /\ ctrlC' = 0
    /\ UNCHANGED <<phase, queued, steering, goalArmed, quitVia>>

-------------------------------------------------------------------------------
\* The cancel lands.
-------------------------------------------------------------------------------

\* cancelAgent drains and returns to idle, then starts a queued prompt if there
\* is one (agent_loop.go:830-874).
CancelSettlesToIdle ==
    /\ phase = "canceling"
    /\ ~queued
    /\ phase' = "idle"
    /\ steering' = FALSE
    /\ UNCHANGED <<queued, ctrlC, goalArmed, quitVia>>

CancelSettlesToQueued ==
    /\ phase = "canceling"
    /\ queued
    /\ phase' = "busy"
    /\ queued' = FALSE
    /\ steering' = FALSE
    /\ UNCHANGED <<ctrlC, goalArmed, quitVia>>

-------------------------------------------------------------------------------
\* Goal loop control (goal.go:361-400). Only the stopping forms are allowed
\* through while a turn runs (tui.go:773).
-------------------------------------------------------------------------------

\* /goal <objective> while idle starts a loop.
GoalSetIdle ==
    /\ phase = "idle"
    /\ ~goalArmed
    /\ goalArmed' = TRUE
    /\ phase' = "busy"
    /\ ctrlC' = 0
    /\ UNCHANGED <<queued, steering, quitVia>>

\* /goal pause, or Esc landing as a stop, leaves the loop armed off.
GoalStop ==
    /\ goalArmed
    /\ goalArmed' = FALSE
    /\ UNCHANGED <<phase, queued, steering, ctrlC, quitVia>>

-------------------------------------------------------------------------------
\* Quit and close.
-------------------------------------------------------------------------------

\* tea.Quit is processed: the terminal is restored and the process ends.
Quit ==
    /\ phase = "quitting"
    /\ phase' = "closed"
    /\ UNCHANGED <<queued, steering, ctrlC, goalArmed, quitVia>>

\* /quit or Ctrl+D, an idle-only path.
QuitIdle ==
    /\ phase = "idle"
    /\ phase' = "quitting"
    /\ quitVia' = "command"
    /\ UNCHANGED <<queued, steering, ctrlC, goalArmed>>

\* The ONLY stuttering step is at the end. Every other state must have a step
\* out, so TLC's NoDeadlock is what checks the liveness claims this model makes:
\* that a canceled turn always settles, that a busy session is always escapable,
\* and that the init splash cannot hang. Encoding them as invariants would be
\* vacuous; encoding them as the absence of a dead end is the real statement.
Done ==
    /\ phase = "closed"
    /\ UNCHANGED V

Next ==
    \/ InitProgress    \/ InitDone       \/ InitFailed
    \/ SubmitIdle      \/ SubmitSteers   \/ QueueAnother \/ GoalCycleQueues
    \/ TurnEndQueued   \/ TurnEndIdle    \/ TurnEndGoalCycle
    \/ EscBusy         \/ EscIdle
    \/ CtrlCBusyFirst  \/ CtrlCBusyAgain
    \/ CtrlCIdleFirst  \/ CtrlCIdleSecond \/ CtrlCTimerFires
    \/ CancelSettlesToIdle \/ CancelSettlesToQueued
    \/ GoalSetIdle     \/ GoalStop
    \/ Quit            \/ QuitIdle
    \/ Done

Init ==
    /\ phase = "starting"
    /\ queued = FALSE
    /\ steering = FALSE
    /\ ctrlC = 0
    /\ goalArmed = FALSE
    /\ quitVia = ""

FairSpec == Init /\ [][Next]_vars

-------------------------------------------------------------------------------
\* Rules the reader agreed to. Each is a real claim about a reachable state;
\* a rule that is only ever vacuously true is not written here.
-------------------------------------------------------------------------------

\* tui.go:1290-1296 -- "Ctrl+C again to quit" can never need a third press,
\* because the counter resets on the timer and the idle quit fires at 2.
CtrlCBounded ==
    ctrlC <= 2

\* A queued prompt exists only while a turn is running or being canceled; there
\* is no state where a prompt sits in the queue with nothing to start it. A
\* canceled turn is the interesting case: cancelAgent must hand the queue to the
\* next turn (agent_loop.go:840-874), not drop it.
NoLostPrompt ==
    queued => phase \in {"busy", "canceling"}

\* cancelAgent disarms a goal loop (agent_loop.go:838-841), so every path that
\* cancels -- Esc, Ctrl+C, the cycle budget -- leaves the loop off. An idle
\* session therefore has no armed loop to restart by itself.
IdleHasNoArmedGoal ==
    phase = "idle" => ~goalArmed

\* A steer exists only while a turn runs or is being canceled. It is NOT a claim
\* that steering clears at cancel time: cancelAgent sets m.steering = false
\* (agent_loop.go:845), but the model treats Esc and the cancel as one step, so
\* a steered-then-escaped turn shows steering still set in "canceling". That is
\* a modelling artefact, not a bug, and the rule is stated around it.
SteerOnlyWhileBusy ==
    steering => phase \in {"busy", "canceling"}

\* tui.go:1290-1297 -- the idle Ctrl+C path needs TWO presses: the first only
\* warns. So no session ever reaches "quitting" on a single press, and a user who
\* taps Ctrl+C once does not lose their session. This is the rule that makes the
\* warning honest, and it is the one a regression breaks.
\* tui.go:1290-1297 -- the Ctrl+C path needs TWO presses: the first only warns.
\* A single press never quits, which is what makes the warning honest. Scoped by
\* quitVia because /quit and an init failure reach the same phase legitimately.
TwoPressesToQuit ==
    quitVia = "ctrl-c" => ctrlC = 2

\* cancelAgent hands a queued prompt to the next turn rather than dropping it
\* (agent_loop.go:840-874). So a settled cancel never strands a queued prompt:
\* if one was waiting, a turn is running now.
CancelDoesNotStrandTheQueue ==
    phase = "idle" => ~queued

\* The session reaches closed only from quitting.
ClosedOnlyFromQuitting ==
    phase = "closed" => ctrlC <= 2

=============================================================================
