# Formal verification models

TLA+ models of pi-go's implicit state machines, and the harness that checks them.

```bash
hack/formal/check.sh goal           # one model
hack/formal/check.sh all            # every model
hack/formal/check.sh goal --chart   # also draw a statechart (needs the pstack /statechart skill)
```

Each model lives in its own directory as `<Name>.tla` + `<Name>.cfg`, with an
optional `<lowercase>.view.ts` that turns TLA+ state into the reader's words for
the chart. A model's directory name is what `check.sh` takes.

`check.sh` finds a JDK and TLC rather than installing them; it downloads the
pinned `tla2tools` 1.7.4 once into `~/.cache/pi-go-formal` and verifies its
sha256. `JAVA` and `TLA2TOOLS` override both.

The rules each model checks, the code each step stands for, and what the model
leaves out are documented in the matching skill under
`internal/extension/bundled_skills/verify-*/SKILL.md`. Read that before changing
a model: a rule that is broken is a bug report or a modelling error, and the
skill says how to tell which — never weaken a rule to make a run pass.

New models: copy an existing directory, one step per function call, name each
action after the function it stands for, and prove the check can fail by
breaking the model in a scratch copy before trusting a pass.

## `lint.py`

Structural lint that runs before TLC. It exists because three separate defects
produced green runs that proved nothing:

- **unassigned-variable** — a variable no action assigns. TLC stops with
  "Successor state is not completely specified" on some paths, and any rule over
  it is vacuously true. An entire gate rule was silently vacuous this way.
- **duplicate-assignment** — two assignments to one variable in one action. TLA+
  conjoins them, so a contradicting branch can never fire. This hid the ordering
  bug the `/goal` model was written to find.
- **vacuous-rule** — a rule whose premise needs a literal no action produces.

Each check was confirmed against the real defect it was written for, not a
synthetic one.
