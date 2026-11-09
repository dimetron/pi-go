---
name: verify-history-rewrite
description: Formally verify the session-history rewrite path before or after changing it. Use when editing internal/session/autocompact.go, internal/session/compaction*.go, internal/session/store.go's Compact/ClearEvents/rewriteEvents, internal/agent/agent.go's Run/RunStreaming/runPreTurn, or internal/tui/commands.go's clearConversation/clearSessionContext and the context gauge — or when a turn is canceled mid-flight, a tool call and its result get separated, a session's history disappears while the agent is still working, or the model is sent a tool result whose call was compacted away.
---

# Verify the history-rewrite path

A session's event list is rewritten from four places. One of them is not gated on
whether a turn is running, and one of them cuts without checking that a tool call
and its result stay on the same side. Both are reachable by a user today; neither
is caught by a test, because a test cannot easily produce the interleaving.

The properties: **history is only rewritten between turns**, and **a compaction
cut never separates a tool call from its result**.

**This model reports two findings that were real and are now fixed.** Both rules
broke on the shipped code when the model was written; both are green now because
the code was changed, not because a rule was weakened. See
[Findings](#findings) for each, the path that reached it, and the test that now
holds it.

The model was changed with the fixes: `SlashCompact` now rounds its cut and
`GaugeClearsMidTurn` ends the turn instead of leaving it running. That is the only
legitimate way to make a broken rule green — change the model to match the code,
then confirm the break still reproduces. The regression check at the bottom of this
skill is what keeps it honest.

If a finding is real and *not yet* fixed, declare the rule in
`hack/formal/history/known-findings.txt` (one rule name per line). `check.sh`
then prints `BROKE history: <rule>` / `known finding, declared in ...` and exits
`0`; an undeclared break still exits `1`. Remove the line when the fix lands — if
the rule keeps reproducing, the fix is not covered by the model.

## Does my change touch the verified code

```bash
git diff --stat <sync-commit>..HEAD -- \
  internal/session/autocompact.go internal/session/compaction.go \
  internal/session/compaction_shed.go internal/session/store.go \
  internal/agent/agent.go internal/tui/commands.go internal/tui/context_rule.go \
  internal/tui/tui.go internal/tui/agent_loop.go
```

**Model covers** (each model step names the call it stands for):

| Model action | Code |
|---|---|
| `RunTurn` | a turn appends its call and its result together; `agent.go:801-823` |
| `BeginTurn` / `FinishTurn` | a turn starts and settles; `agent_loop.go:2013-2075` |
| `AutoCompacts` | `runPreTurn` → `AutoCompact` → `advanceToCleanBoundary`; `autocompact.go:60-140, 148-161` |
| `SlashCompact` | `/compact` → `FileService.Compact`; `store.go:1306-1352` |
| `GaugeClearsMidTurn` | gauge `[x]` → `clearConversation` → `ClearEvents`; `tui.go:994-996`, `commands.go:291-333` |

**Not modelled, and why:** `rewriteEvents`' temp-file collision when two rewriters
run at once (`store.go:1447` uses a fixed `.tmp` name); the `\E`-quantified retry
in `agent/retry.go` that re-runs `runPreTurn` on a transient error; the summarizer
call itself; `FileService.mu` ordering beyond the fact that the *decision* happens
outside it; the shed path's superseded-result selection; anything about branches
or `merge.go`. A bug in those is invisible here.

## Run it

```bash
JAVA=/opt/homebrew/opt/openjdk/bin/java hack/formal/check.sh history
```

Exit `0` every rule holds, `1` a rule broke, `2` TLC or the toolchain failed. A
`2` is never a verdict about the code. To see the failing steps in the reader's
words, use the pstack `/statechart` skill:

```bash
TLA2TOOLS="$HOME/.cache/pi-go-formal/tla2tools-1.7.4.jar" \
JAVA=/opt/homebrew/opt/openjdk/bin/java \
bun run <skills>/statechart/scripts/statechart.ts \
  hack/formal/history/HistoryRewrite.tla \
  --config hack/formal/history/HistoryRewrite.cfg \
  --view hack/formal/history/history.view.ts --out /tmp/history-chart
```

`check.sh history --chart` draws the chart only when every rule holds; while these
findings are open it exits before drawing, which is why the command above calls
`/statechart` directly.

## Findings

Both are reported with the shortest path TLC found. Replay them against the code
before acting — the model is a claim about the code, not a substitute for reading
it.

Both findings below are **fixed**. Their guards are named so a regression is
recognisable, and each guard is held by a test as well as by this model.

### 1. The context gauge's `[x]` rewrote history mid-turn — fixed

**Rule:** `NoRewriteMidTurn` — history is rewritten only between turns
(`internal/agent/agent.go:384-387`).

**Path:** a turn runs → you click the context gauge's `[x]` while it works.

**What went wrong.** `hitContextClear` (`context_rule.go:311-320`) checks only the
layout row and `ShowClear`, and `ShowClear` depends on terminal width alone
(`context_rule.go:192-197`) — it never consulted `m.running`. So unlike `/clear`,
which returns early while a turn runs (`tui.go:773`), the mouse path proceeded.
`clearConversation` then set `m.running = false` (`commands.go:299`) and called
`clearSessionContext` → `ClearEvents`, without cancelling `m.agentCancel` or
draining `m.agentCh`.

**Harm (had it shipped):** the in-flight turn's tool calls lose their results — the
orphaning the pre-turn hook placement exists to prevent. `m.running = false` also
removed the keyboard path that could stop the still-running turn.

**The guard now.** `handleMouseClick` calls `m.cancelAgent()` before
`clearConversation` (`tui.go:994-1006`), so the click stops the turn the way Esc
does and the rewrite lands between turns. `GaugeClearsMidTurn` ends the turn in the
model, and it can no longer reach a mid-turn rewrite. Held by
`TestContextGaugeClearStopsTheRunningTurnFirst`, which drives
`handleMouseClick` rather than the helper — the missing guard *was* the bug.

### 2. `/compact` cut between a tool call and its result — fixed

**Rule:** `CutIsClean` — a cut never separates a `FunctionCall` from its
`FunctionResponse` (`internal/session/autocompact.go:110-114`).

**Path:** a turn runs and its tool call is answered → you run `/compact`.

**What went wrong.** `advanceToCleanBoundary` had exactly one caller,
`autocompact.go:115`, on the automatic path. `FileService.Compact` cut at
`splitIdx := len(sess.events) - cfg.KeepRecent` (`store.go:1322`) and never
consulted it, so an odd split point kept a result whose call was compacted.
`KeepRecent` defaults to 10 (`store.go:1283`) — even, so the shipped default rarely
landed on an odd index, but the value is caller-supplied and `/compact` is not its
only caller.

**Harm (had it shipped):** the model is sent a tool result with no matching call.
Providers that validate pairing reject the request, and ADK's own history loader
drops such results, so the turn either fails or silently loses tool output.

**The guard now.** `Compact` calls `advanceToCleanBoundary` on its split point and
returns early if the rounded index leaves nothing to summarize
(`store.go:1322-1330`). `SlashCompact` rounds its cut in the model. Held by
`TestCompactKeepsCallAndResultTogether`, which asserts the surviving tail does not
begin with an orphaned result.

## Telling a bug from a modelling error

Read the counterexample. Every model state names a variable that exists in the
code, and every action names a function or a UI event. If the shortest path
contains a step a user cannot perform, or the state it reaches does not correspond
to a real `events` list, the model is wrong — fix the model, and say so in the
commit. If the path is reachable, it is a bug: report it and leave the rule alone.

## Keeping this in step with the code

- **Sync commit:** `5702a08`, plus the two fixes and their tests (uncommitted at the
  time of writing; move this forward to the commit that lands them).
- **Confirmed against:** that tree, with every file:line citation above re-read
  after the fixes changed `store.go:1322-1330` and `tui.go:994-1006`.

Move the sync commit forward only after confirming that the verification still
describes the code at the new commit — run it, and re-read the file:line citations
in `HistoryRewrite.tla`, which are what a reviewer follows to check the model
against the code. If a citation has drifted, fix the citation before moving the
commit.

## Proving the check can still fail

Before trusting a green run, break each guard in a scratch copy and confirm the
named rule fires. Both directions were confirmed when this model was written:

| Regress this | Rule that must fire |
|---|---|
| let `GaugeClearsMidTurn` fire while `phase = "busy"` | `NoRewriteMidTurn` |
| make `SlashCompact` cut at any index again | `CutIsClean` |

Both were confirmed: with the code unfixed the model reported each rule; with the
fixes in place it holds `11 distinct states`. Re-confirm the `CutIsClean` direction
after any change to `Compact` — reverting the boundary call is a one-line
regression that the model catches in under a second.

## Related

- `/maintain-formal-verification-skill` — bring this back in line after a change
  that did not recheck it.
- The other models in `hack/formal/` cover different subsystems; this one covers
  only the events list. `verify-session-flow` covers the TUI's turn and Ctrl+C
  states, which is where a fix for finding 1 touches.
