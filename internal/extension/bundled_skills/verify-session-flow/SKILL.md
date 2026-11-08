---
name: verify-session-flow
description: Formally verify the interactive session flow before or after changing it. Use when editing internal/tui/tui.go's handleKey, handleInterruptKey, handleInputSubmit, ctrlCCount or quitting, internal/tui/agent_loop.go's enqueuePrompt, submit, steerPrompt, cancelAgent or handleAgentDone, or the deferred-init splash — or when a user reports that Ctrl+C quit on the first press, that Esc did not stop a turn or a goal loop, that a queued prompt was lost when they canceled, or that the TUI hung on a spinner nothing could clear.
---

# Verify the session flow

This is the model of what a person sees: launch, type, wait, cancel, quit. It
exists because the two failures a user actually notices are structural — a session
that cannot be interrupted, and a quit that discards work — and both span several
handlers, so neither is visible in a unit test of any one of them.

The properties: **the Ctrl+C quit needs two presses**, **Esc stops an armed goal
loop**, **a canceled turn hands its queued prompt to the next turn rather than
dropping it**, and **no TUI state is a dead end**.

## Does my change touch the verified code

```bash
git diff --stat <sync-commit>..HEAD -- \
  internal/tui/tui.go internal/tui/agent_loop.go internal/tui/commands.go \
  internal/tui/goal.go internal/autocompact/hook.go
```

**Model covers** (each model step names the handler it stands for):

| Model action | Code |
|---|---|
| `InitProgress` / `InitDone` / `InitFailed` | the deferred-init splash, `tui.go:563-565, 2391-2417` |
| `SubmitIdle` | `handleInputSubmit` → `enqueuePrompt` → `submit`, `tui.go:783`, `agent_loop.go:985-1049` |
| `SubmitSteers` | while busy, a prompt steers instead of queueing, `tui.go:785-787`, `agent_loop.go:961-975` |
| `GoalCycleQueues` | a goal cycle's own prompt occupies the queue, `goal.go:284-296` |
| `TurnEndQueued` / `TurnEndIdle` / `TurnEndGoalCycle` | `handleAgentDone`, `agent_loop.go:2013-2075` |
| `EscBusy` / `EscIdle` | `handleInterruptKey`, `tui.go:1273-1281` |
| `CtrlCBusyFirst` / `CtrlCBusyAgain` / `CtrlCIdleFirst` / `CtrlCIdleSecond` | `tui.go:1283-1297` |
| `CtrlCTimerFires` | `resetCtrlCCount`, `tui.go:2353-2359` |
| `CancelSettlesToIdle` / `CancelSettlesToQueued` | `cancelAgent`, `agent_loop.go:830-874` |
| `GoalSetIdle` / `GoalStop` | `handleGoalCommand`, `goal.go:361-400` |
| `Quit` / `QuitIdle` | `tea.Quit`, `/quit` |

**Not modelled, and why:** the modal overlays (commit, login, skill-create,
branch and search popups). Each is a pointer that either swallows or forwards a
key; none can strand a running turn, and the ordering claim they make is the
five-line handler list in `handleKey` (`tui.go:1144-1151`), which a reader checks
by eye. Also out of scope: rendering, scrolling, the diff sidebar, `/run` and
`/pr-autofix` (separate machines), and the pre-turn compaction hook, whose
guarantee is only that it never aborts a turn.

## Run it

```bash
hack/formal/check.sh session           # all rules hold, or the first that breaks
hack/formal/check.sh session --chart   # draw the session flow
```

Cost: 34 distinct states, under a second. Deadlock checking is **on** and must
stay on: with it off, a lost-prompt regression passes, which is how an earlier
version of this check was green while proving nothing.

## Read a result

**A rule broke** — the shortest step sequence names the presses in order. Replay
it as a Bubble Tea test in `internal/tui` (the `goal_loop_e2e_test.go` pattern
drives the real `Update` loop) before calling it a bug. **`Deadlock` reported** —
a state with no step out. Every state except `closed` should have one; a dead end
in `canceling` means the user is watching a spinner nothing will clear.

## Rules, and the harm each one names

| Rule | The promise | What breaking it means for a user |
|---|---|---|
| `TwoPressesToQuit` | the Ctrl+C quit path needs two presses | one tap ends the session; the "again to quit" warning is a lie |
| `CtrlCBounded` | the counter never exceeds 2 | "again to quit" would need a third press |
| `IdleHasNoArmedGoal` | Esc/Ctrl+C disarm a goal loop, so an idle session never self-restarts | a stopped goal spends another billed turn |
| `CancelDoesNotStrandTheQueue` | a settled cancel has handed its queued prompt on | a prompt you typed vanishes |
| `NoLostPrompt` | a queued prompt only exists while a turn runs or cancels | a prompt sits in the queue with nothing to start it |
| `SteerOnlyWhileBusy` | a steer belongs to a running or canceling turn | a stale steer hijacks the next prompt |
| `ClosedOnlyFromQuitting` | the terminal closes only via the quit path | the UI is torn down under a live turn |
| `NoDeadlock` (implicit) | every state but `closed` has a step out | a hung TUI |

## What this model has already caught

- **Esc had to disarm the goal loop.** An early version left `goalArmed` set
  across `EscBusy`, and `IdleHasNoArmedGoal` failed with a 5-step path. That is
  the `agent_loop.go:838-841` behaviour, independently rediscovered.
- **A dead model passes.** After adding `quitVia`, a mechanical edit put it in
  every `UNCHANGED` tuple *and* assigned it, so every quitting action was
  unsatisfiable and the model explored 2 states. `hack/formal/lint.py` found it;
  the check itself did not. Always confirm the state count is in the tens and a
  deliberate break reproduces.
- **A rule scoped by `ctrlC > 0` was still wrong**, because `/quit` and a single
  Ctrl+C both left the counter at 1. The rule now reads `quitVia`, an explicit
  record of what asked to quit — the same lesson as `verify-agent-lifecycle`'s
  `cancelAsked`.

## After confirming the model is still true

- **Sync commit:** `cc79aceb43df31012e9e5c1f69a7ba1231c32470` (moved when the
  session model was added; update on the next confirmed change).
- **Constants:** none.
- **Chart budget:** 34 states, so the full chart always draws.

## Draw the chart

`hack/formal/check.sh session --chart` writes `hack/formal/session/out/report.md`
and `chart.mmd` via the pstack `/statechart` skill. This is the chart to show a
person asking "what does pi actually do while I wait?".
