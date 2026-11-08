--------------------------- MODULE AgentLifecycle ---------------------------
\* A model of one subagent's lifecycle in internal/subagent/orchestrator.go.
\*
\* Code modelled (one step per function call or lock-protected transition):
\*   Spawn                 orchestrator.go:487-607
\*   trackAgent            orchestrator.go:599-607
\*   forwardAgentEvents    orchestrator.go:610-648
\*   terminalStatus        orchestrator.go:650-660
\*   Cancel                orchestrator.go:818-834
\*   ShutdownWithTimeout   orchestrator.go:859-894
\*   evictCompletedAgentsLocked  orchestrator.go:683-711
\*   Get / List            orchestrator.go:766-800
\*
\* Three promises the code's comments make, and the harm each break causes:
\*
\*  1. "First terminal status wins" -- forwardAgentEvents only writes the status
\*     if it is still "running" (:626), because Cancel and Shutdown may have set
\*     "canceled" first. A user who pressed cancel must not be told the agent
\*     failed.
\*  2. "Consumers must use this snapshot rather than querying the evictable
\*     status map" (:634-636) -- the run_done event carries the terminal status
\*     because the map entry may be gone by the time a consumer asks. So an
\*     entry must never disappear before its run_done is published, or a
\*     consumer that polled in between sees an agent vanish with no verdict.
\*  3. Exactly one pool slot is released per spawn (:590-595 and the defer at
\*     :613). pool.Release is an unguarded receive (:36-38), so a second release
\*     panics.

EXTENDS Naturals

VARIABLES
    registered,   \* trackAgent succeeded: the agent is in o.agents
    status,       \* "running" | "completed" | "failed" | "canceled" | "timeout" | "killed"
    eventsOpen,   \* proc.Events() is still producing
    published,    \* the run_done status sent to the consumer; "" until then
    inMap,        \* the entry is still in o.agents (not evicted)
    acquired,     \* a pool slot was taken for this spawn, so one Release is owed
    releases,     \* how many times pool.Release ran for this spawn; must be <= 1
    cancelAsked,  \* a cancel or shutdown asked for this agent to stop
    closed        \* o.closed

vars == <<registered, status, eventsOpen, published, inMap, acquired, releases,
          cancelAsked, closed>>

V == <<registered, status, eventsOpen, published, inMap, acquired, releases,
       cancelAsked, closed>>

\* terminalStatus(waitErr): timeout beats killed beats failed beats completed.
Terminal == {"completed", "failed", "canceled", "timeout", "killed"}

\* pool.Acquire succeeded for this spawn, so exactly one Release is owed. The
\* code takes the slot before the worktree and the process exist, which is why
\* every failure path from here owes a release (abandonSpawn :590-595).
SlotAcquired ==
    /\ ~registered
    /\ ~acquired
    /\ ~closed
    /\ registered' = TRUE
    /\ acquired' = TRUE
    /\ status' = "running"
    /\ eventsOpen' = TRUE
    /\ inMap' = TRUE
    /\ UNCHANGED <<published, releases, cancelAsked, closed>>

\* trackAgent refuses after shutdown, and the caller abandons the spawn: it
\* cancels the process and releases the slot it took (orchestrator.go:499-505).
SpawnRefused ==
    /\ ~registered
    /\ acquired
    /\ releases = 0
    /\ closed
    /\ releases' = releases + 1
    /\ UNCHANGED <<registered, status, eventsOpen, published, inMap,
                  acquired, cancelAsked, closed>>

-------------------------------------------------------------------------------
\* The process runs and then exits.
-------------------------------------------------------------------------------

\* The event stream keeps producing.
Event ==
    /\ registered
    /\ eventsOpen
    /\ UNCHANGED vars

\* A process error kills the group before the run finished; forwardAgentEvents
\* still writes the status only if it is "running" (:626).
ProcExits(why) ==
    /\ registered
    /\ eventsOpen
    /\ eventsOpen' = FALSE
    /\ status' = IF status = "running" THEN why ELSE status
    /\ UNCHANGED <<registered, published, inMap, acquired, releases, cancelAsked, closed>>

\* Cancel while the process is still running (orchestrator.go:818-834). Refused
\* unless the status is exactly "running" (validateAgentForCancel :806-816).
Cancel ==
    /\ registered
    /\ status = "running"
    /\ status' = "canceled"
    /\ cancelAsked' = TRUE
    /\ UNCHANGED <<registered, eventsOpen, published, inMap, acquired, releases, closed>>

\* ShutdownWithTimeout marks every running agent canceled and closes the
\* orchestrator (orchestrator.go:873-886).
\* ShutdownWithTimeout cancels every running agent, then closes. Split in two so
\* the canceled case and the already-finished case do not share a conditional
\* assignment, which TLA+ would conjoin.
ShutdownRunning ==
    /\ ~closed
    /\ registered
    /\ status = "running"
    /\ closed' = TRUE
    /\ status' = "canceled"
    /\ cancelAsked' = TRUE
    /\ UNCHANGED <<registered, eventsOpen, published, inMap, acquired, releases>>

ShutdownIdle ==
    /\ ~closed
    /\ ~(registered /\ status = "running")
    /\ closed' = TRUE
    /\ UNCHANGED <<registered, status, eventsOpen, published, inMap, acquired, releases,
                  cancelAsked>>

-------------------------------------------------------------------------------
\* forwardAgentEvents' tail: record, evict, publish, release (:628-648).
-------------------------------------------------------------------------------

\* After the event stream closes, forwardAgentEvents records the status (:628-630),
\* evicts if the completed count is over the cap (:631), publishes run_done
\* (:643), and releases the pool slot (:613 defer). Eviction and publishing are
\* the same indivisible step in the code, so they are one step here, in two
\* disjoint variants: this entry survives, or the cap evicted it.
\*
\* The order inside the step is what the rule checks: eviction runs BEFORE the
\* publish, so a consumer that read run_done and then polled Get() would find
\* nothing. The code accepts that because the event carries the status
\* (:634-636) -- which is exactly why PublishedIsTerminal must hold.

PublishKept ==
    /\ registered
    /\ ~eventsOpen
    /\ published = ""
    /\ inMap
    /\ published' = status
    /\ inMap' = TRUE
    /\ releases' = releases + 1
    /\ UNCHANGED <<registered, status, eventsOpen, acquired, cancelAsked, closed>>

PublishEvicted ==
    /\ registered
    /\ ~eventsOpen
    /\ published = ""
    /\ inMap
    /\ published' = status
    /\ inMap' = FALSE
    /\ releases' = releases + 1
    /\ UNCHANGED <<registered, status, eventsOpen, acquired, cancelAsked, closed>>

\* A consumer polls Get() (orchestrator.go:766-782).
Poll ==
    /\ UNCHANGED vars

\* The stutter, so NoDeadlock stays meaningful.
Done ==
    /\ published /= ""
    /\ UNCHANGED V

Next ==
    \/ SlotAcquired  \/ SpawnRefused
    \/ Event
    \/ \E why \in {"completed", "failed", "timeout", "killed"} : ProcExits(why)
    \/ Cancel       \/ ShutdownRunning \/ ShutdownIdle
    \/ PublishKept \/ PublishEvicted \/ Poll
    \/ Done

Init ==
    /\ registered = FALSE
    /\ status = "running"
    /\ eventsOpen = FALSE
    /\ published = ""
    /\ inMap = FALSE
    /\ acquired = FALSE
    /\ releases = 0
    /\ cancelAsked = FALSE
    /\ closed = FALSE

FairSpec == Init /\ [][Next]_vars

-------------------------------------------------------------------------------
\* Rules the reader agreed to.
-------------------------------------------------------------------------------

\* orchestrator.go:626-630 -- "first terminal status wins": once something asked
\* this agent to stop, the verdict a consumer reads is "canceled", never the
\* process's own exit reason. The request is tracked separately precisely because
\* the bug this guards is the status being OVERWRITTEN: a rule phrased on status
\* alone cannot see it, which is how the first version of this rule let the
\* mutant pass.
CancelIsSticky ==
    cancelAsked /\ published /= "" => published = "canceled"

\* orchestrator.go:634-636 -- the published run_done carries the stored status,
\* and it is never "running". A consumer waiting for a verdict must never be told
\* the agent is still running after its stream closed.
PublishedIsTerminal ==
    published /= "" => published \in Terminal

\* orchestrator.go:631-643 -- eviction runs BEFORE the publish, so an evicted
\* entry is gone by the time its run_done is on the wire. The rule is therefore
\* not "the entry outlives the publish" (it may not) but "an evicted entry has
\* still published its verdict": a consumer must never be able to observe an
\* agent missing from the map with no run_done carrying its status.
EvictionStillPublishes ==
    ~inMap /\ registered => published /= ""

\* orchestrator.go:590-595 and :613 -- exactly one pool slot is released per
\* spawn. pool.Release is an unguarded receive, so a second one panics.
SlotReleasedOnce ==
    releases <= 1 /\ (releases = 1 => acquired)

\* orchestrator.go:806-816 -- Cancel is refused unless the agent is running, so a
\* terminal status is never replaced by "canceled".
NoLateCancel ==
    status \in {"completed", "failed", "timeout", "killed"} => status /= "canceled"

=============================================================================
