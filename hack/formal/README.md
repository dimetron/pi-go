# Formal verification models

TLA+ models of pi-go's implicit state machines, and the harness that checks them.
A test checks the cases someone thought to write; these check every case in a
stated scope.

```bash
hack/formal/check.sh all              # every model
hack/formal/check.sh goal             # one model
hack/formal/check.sh session --chart  # also draw a statechart (pstack /statechart)
```

| Model | What it covers | States | Skill |
|---|---|---|---|
| `goal` | the `/goal` loop: completion attribution, cycle budget | 352 | `verify-goal-loop` |
| `run` | the `/run` pipeline: two-signal merge gate, hung gates, retries | 110 | `verify-run-pipeline` |
| `agent` | one subagent: cancel stickiness, eviction, the pool slot | 35 | `verify-agent-lifecycle` |
| `session` | the interactive session: Ctrl+C, Esc, queued prompts, quit | 34 | `verify-session-flow` |
| `history` | session history rewrites: mid-turn clears, compaction cuts | 11 | `verify-history-rewrite` |

`history` was written to report two real bugs — the context gauge's `[x]` clearing
history mid-turn, and `/compact` cutting between a tool call and its result. Both
are now fixed and covered by tests, so all five models are green. The rules stay, so
a regression fails here as well as in the unit tests. See *Known findings* for what
to do if a model legitimately has to be red.

Every model runs in well under a second. Each lives in its own directory as
`<Name>.tla` + `<Name>.cfg`, with an optional `<lowercase>.view.ts` that turns TLA+
state into the reader's words for the chart. A model's directory name is what
`check.sh` takes.

`check.sh` finds a JDK and TLC rather than installing them; it downloads the pinned
`tla2tools` 1.7.4 once into `~/.cache/pi-go-formal` and verifies its sha256. `JAVA`
and `TLA2TOOLS` override both. It passes `-metadir` so TLC's multi-megabyte
fingerprint dumps stay out of the tree.

Exit codes: `0` every rule holds, `1` a rule broke, `2` TLC or the environment
failed. A `2` is never a verdict about the code.

## Known findings

A model may be red because it is *reporting a bug in the code*, not because the
model regressed. `hack/formal/<model>/known-findings.txt` declares those rules by
name. `check.sh` then prints the rule as a known finding and does **not** fail:

```
BROKE history: CutIsClean
  known finding, declared in known-findings.txt
RESULT history: known finding(s) reproduced; no new breakage
```

An **undeclared** break still fails the run, so `check.sh all` stays green while a
finding is open and goes red the moment anything else breaks. Without this the
suite is red for every run and nobody can tell a known bug from a new one.

Delete a line when the fix lands: the rule must then go green. If it stays listed
and keeps reproducing, the fix is not covered by the model.

Nothing is declared today: every model is green. A declaration is for a finding
that is real, reported, and not yet fixed — not for making a regression quiet.

The rules each model checks, the code each step stands for, and what the model
leaves out are documented in the matching skill under
`internal/extension/bundled_skills/verify-*/SKILL.md`. **Read that before changing
a model**: a broken rule is either a bug report or a modelling error, and the skill
says how to tell which. Never weaken a rule to make a run pass.

## The rule for trusting a model

A green run is a claim, and it needs evidence of the same size. Before trusting
one, do both of these:

1. **Check the state count is in the tens or hundreds.** A model that reaches a
   handful of states is dead, not correct. This has happened three times here,
   each time from an action that could never fire.
2. **Break the model and confirm the check fails.** In a scratch copy, delete the
   guard the rule is about and re-run. If it still passes, the rule is vacuous or
   the model never reaches the case. Both have happened: an entire gate rule
   quantified over a variable no action assigned.

Read *which* line fired when a mutation fails. It must be
`Error: Invariant <name> is violated`. `TLC threw an unexpected exception` or
`Attempted to apply the operator to a non-boolean` means the mutant is broken and
proves nothing.

## `hack/formal/lint`

Structural lint that runs before TLC, because a green run has been wrong for
structural reasons more often than for logical ones. It is a Go package
(`hack/formal/lint`) with a command at `hack/formal/lint/main`, and a test per
defect, so every check has been seen to fire on the shape it catches.

```bash
go run ./hack/formal/lint/main                  # every model
go run ./hack/formal/lint/main hack/formal/goal # one tree
```

- **unassigned-variable** — a variable no action assigns. TLC stops with
  "Successor state is not completely specified" on some paths, and any rule over it
  is vacuously true. An entire gate rule was silently vacuous this way.
- **duplicate-assignment** — the same variable assigned twice in one branch. TLA+
  conjoins them, so a contradicting assignment can never fire. This hid the
  ordering bug the `/goal` model was written to find.
- **assigned-and-unchanged** — `x' = v /\ UNCHANGED <<x>>` pins `x` twice. A
  mechanical edit made every action in the session model dead this way.
- **vacuous-rule** — a rule whose premise needs a literal no action produces.

The lint is branch-aware: `IF/THEN/ELSE` and `\/` alternatives are separate
branches, so an action that differs per branch is not reported. Each check was
confirmed against the real defect it was written for, not a synthetic one.

It is Go rather than a script for the reason in `AGENTS.md`: a checker whose own
behaviour is untested is one more thing trusted without evidence.

## Adding a model

Copy an existing directory and follow its shape:

- **One step per function call**, and name each action after the function it stands
  for, so a counterexample reads as a call sequence.
- **State a rule only where the code makes a promise**, and cite the line and the
  comment it comes from.
- **Write actions as disjoint alternatives**, not one action with a conditional
  assignment — a top-level assignment plus a branch assignment is a conjunction.
- **Keep the state space small** by modelling the decision a counter leads to
  rather than the counter. A count of completed agents made one model pass two
  million states; the eviction decision it produced made it 35.
- **Then prove the check can fail**, and write the skill.
