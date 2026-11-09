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

**This model currently fails.** Both rules break on the shipped code. See
[Findings](#findings) — they are bug reports, not modelling errors. Do not weaken
either rule to make the run green.

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

### 1. The context gauge's `[x]` rewrites history mid-turn

**Rule:** `NoRewriteMidTurn` — history is rewritten only between turns
(`internal/agent/agent.go:384-387`).

**Path:** a turn runs → you click the context gauge's `[x]` while it works.

**Why it is reachable.** `hitContextClear` (`internal/tui/context_rule.go:311-320`)
checks only the layout row and `ShowClear`, and `ShowClear` depends on terminal
width alone (`context_rule.go:192-197`) — it never consults `m.running`. So unlike
`/clear`, which returns early while a turn runs (`tui.go:773`), the mouse path
proceeds. `clearConversation` then sets `m.running = false`
(`commands.go:299`) and calls `clearSessionContext` → `ClearEvents`, but it does
**not** cancel `m.agentCancel` or drain `m.agentCh`. The canceled-looking turn is
still executing and will keep appending to the events that were just emptied.

**Harm:** the in-flight turn's tool calls lose their results — the exact orphaning
the pre-turn hook placement exists to prevent. `m.running = false` also means the
TUI no longer offers Esc, so the still-running turn cannot be stopped from the
keyboard.

### 2. `/compact` cuts between a tool call and its result

**Rule:** `CutIsClean` — a cut never separates a `FunctionCall` from its
`FunctionResponse` (`internal/session/autocompact.go:110-114`).

**Path:** a turn runs and its tool call is answered → you run `/compact`.

**Why it is reachable.** `advanceToCleanBoundary` is called from exactly one site,
`autocompact.go:115`, on the automatic path. `FileService.Compact` instead cuts at
`splitIdx := len(sess.events) - cfg.KeepRecent` (`store.go:1322`) and never
consults the boundary helper, so an odd split point keeps a result whose call was
compacted. `KeepRecent` defaults to 10 (`store.go:1283`), which is an even number,
so the shipped default rarely lands on an odd index — but the value is
caller-supplied and `/compact` is not the only caller.

**Harm:** the model is sent a tool result with no matching call. Providers reject
that outright, and ADK's own history loader drops such results, so the turn either
fails or silently loses tool output.

## Telling a bug from a modelling error

Read the counterexample. Every model state names a variable that exists in the
code, and every action names a function or a UI event. If the shortest path
contains a step a user cannot perform, or the state it reaches does not correspond
to a real `events` list, the model is wrong — fix the model, and say so in the
commit. If the path is reachable, it is a bug: report it and leave the rule alone.

## Keeping this in step with the code

- **Sync commit:** `80825c9`
- **Confirmed against:** `80825c9` plus this model, with every line reference above
  read at that commit.

Move the sync commit forward only after confirming that the verification still
describes the code at the new commit — run it, and re-read the file:line citations
in `HistoryRewrite.tla`, which are what a reviewer follows to check the model
against the code. If a citation has drifted, fix the citation before moving the
commit.

## Proving the check can still fail

Before trusting a green run, break each guard in a scratch copy and confirm the
named rule fires. Both directions were confirmed when this model was written:

| Break this | Rule that must fire |
|---|---|
| gate `GaugeClearsMidTurn` on `phase = "idle"` | `NoRewriteMidTurn` goes **green** |
| make `SlashCompact` round the cut down to an even index | `CutIsClean` goes **green** |

Both fixed together: `11 distinct states`, all rules hold. A rule that passes
without you having seen it fail proves nothing.

## Related

- `/maintain-formal-verification-skill` — bring this back in line after a change
  that did not recheck it.
- The other models in `hack/formal/` cover different subsystems; this one covers
  only the events list. `verify-session-flow` covers the TUI's turn and Ctrl+C
  states, which is where a fix for finding 1 touches.
