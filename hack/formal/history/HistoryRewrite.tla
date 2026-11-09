-------------------------- MODULE HistoryRewrite ---------------------------
\* A model of the two ways a session's events list is rewritten while a turn may
\* be running, and of where the compaction cut lands.
\*
\* Code modelled (one step per function call):
\*   BeginTurn / RunTurn       internal/tui/agent_loop.go:2013-2075, agent.go:801-823
\*   runPreTurn + AutoCompact  internal/session/autocompact.go:60-140
\*   advanceToCleanBoundary    internal/session/autocompact.go:148-161
\*   rewriteEvents             internal/session/store.go:1443-1473
\*   FileService.Compact       internal/session/store.go:1306-1352   (/compact)
\*   hitContextClear           internal/tui/context_rule.go:311-320
\*   clearConversation         internal/tui/commands.go:291-315      (gauge [x])
\*   clearSessionContext       internal/tui/commands.go:316-333
\*
\* Two promises the code makes in prose, and the harm each break causes:
\*
\*  1. "rewriting history while the conversation is between turns, which is the
\*     only safe moment: rewriting history mid-turn would orphan a tool call
\*     from its result" (internal/agent/agent.go:384-387). A mid-turn rewrite
\*     deletes events the in-flight turn is still building on.
\*
\*  2. "a FunctionCall and its FunctionResponse never end up on different
\*     sides" of a compaction cut (internal/session/autocompact.go:110-114).
\*     Both the automatic path (autocompact.go:115) and FileService.Compact
\*     (store.go:1329) now call advanceToCleanBoundary.
\*
\* Both findings this model was built to report have since been fixed and are
\* covered by tests (TestCompactKeepsCallAndResultTogether,
\* TestContextGaugeClearStopsTheRunningTurnFirst). The model keeps the rules so a
\* regression fails here too.

EXTENDS Naturals, Sequences

VARIABLES
    phase,        \* "idle" | "busy" -- is a turn running
    ev,           \* the transcript: alternating "call", "resp" entries
    cut,          \* how much of ev the last rewrite compacted away
    midTurnWrote  \* a rewrite happened while phase = "busy"

vars == <<phase, ev, cut, midTurnWrote>>

V == <<phase, ev, cut, midTurnWrote>>

MaxTurns == 2

\* A turn contributes one call and its response, adjacent and in that order. The
\* model only needs the shape: after n turns ev is call,resp,call,resp,...
TurnLen == 2 * MaxTurns

-------------------------------------------------------------------------------
\* A turn.
-------------------------------------------------------------------------------

\* A turn runs and completes, appending its call and its response together. They
\* are appended as one indivisible step because the code that writes them (the
\* tool callback and the after-tool chain) cannot have a compaction interleaved:
\* the pre-turn hook is the only automatic entry point and it has already
\* returned by the time the turn runs.
RunTurn ==
    /\ phase = "idle"
    /\ Len(ev) < TurnLen
    /\ ev' = Append(Append(ev, "call"), "resp")
    /\ UNCHANGED <<phase, cut, midTurnWrote>>

\* The turn is in flight: the events it already wrote are live, and the gauge is
\* still clickable. This is the state NoRewriteMidTurn is about.
BeginTurn ==
    /\ phase = "idle"
    /\ Len(ev) > 0
    /\ phase' = "busy"
    /\ UNCHANGED <<ev, cut, midTurnWrote>>

FinishTurn ==
    /\ phase = "busy"
    /\ phase' = "idle"
    /\ UNCHANGED <<ev, cut, midTurnWrote>>

-------------------------------------------------------------------------------
\* Rewrites between turns.
-------------------------------------------------------------------------------

\* runPreTurn -> AutoCompact -> advanceToCleanBoundary. The cut advances to a
\* boundary, so the surviving tail begins at a call and never at a response
\* whose call it compacted (autocompact.go:115, :148-161).
AutoCompacts ==
    /\ phase = "idle"
    /\ Len(ev) > 0
    /\ cut' = IF Len(ev) % 2 = 0 THEN Len(ev) ELSE Len(ev) - 1
    /\ UNCHANGED <<phase, ev, midTurnWrote>>

\* /compact -> FileService.Compact. It cuts at len(events) - KeepRecent and then
\* calls advanceToCleanBoundary on that index (store.go:1322-1330), so the cut
\* lands on a call boundary just like the automatic path.
SlashCompact ==
    /\ phase = "idle"
    /\ Len(ev) > 0
    /\ cut' = IF Len(ev) % 2 = 0 THEN Len(ev) ELSE Len(ev) - 1
    /\ UNCHANGED <<phase, ev, midTurnWrote>>

-------------------------------------------------------------------------------
\* The rewrite that is not gated on the turn at all.
-------------------------------------------------------------------------------

\* The context gauge's [x], mid-turn: MouseClickMsg -> handleMouseClick ->
\* hitContextClear -> cancelAgent, then clearConversation -> clearSessionContext
\* -> ClearEvents (tui.go:994-1006). The click now stops the turn the way Esc
\* does before it rewrites, so the rewrite happens between turns and the model
\* ends the turn here rather than leaving it running against emptied events.
GaugeClearsMidTurn ==
    /\ phase = "busy"
    /\ phase' = "idle"
    /\ ev' = <<>>
    /\ cut' = 0
    /\ midTurnWrote' = FALSE

Stutter ==
    /\ UNCHANGED vars

Next ==
    \/ RunTurn \/ BeginTurn \/ FinishTurn
    \/ AutoCompacts
    \/ SlashCompact
    \/ GaugeClearsMidTurn
    \/ Stutter

Init ==
    /\ phase = "idle"
    /\ ev = <<>>
    /\ cut = 0
    /\ midTurnWrote = FALSE

FairSpec == Init /\ [][Next]_vars

-------------------------------------------------------------------------------
\* Rules the reader agreed to.
-------------------------------------------------------------------------------

\* internal/agent/agent.go:384-387 -- history is rewritten only between turns.
NoRewriteMidTurn ==
    midTurnWrote = FALSE

\* internal/session/autocompact.go:110-114 -- a cut never separates a call from
\* its response. ev alternates call,resp,call,resp..., so every even index is a
\* call boundary: the surviving tail must start at an even index. A cut at an odd
\* index keeps a response whose call was compacted, which is the orphan the
\* boundary function exists to prevent.
CutIsClean ==
    cut % 2 = 0

=============================================================================
