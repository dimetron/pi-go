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
| `history` | session history rewrites: mid-turn clears, compaction cuts | 35 | `verify-history-rewrite` |

`history` is the one model that **fails**: both its rules break on the shipped
code. That is a bug report, not a broken model — `check.sh history` exits 1, and
`check.sh all` therefore exits 1. The two findings and their shortest paths are in
`internal/extension/bundled_skills/verify-history-rewrite/SKILL.md`. Fix the code
and the model goes green; do not weaken the rule.

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

## `lint.py`

Structural lint that runs before TLC, because a green run has been wrong for
structural reasons more often than for logical ones:

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
