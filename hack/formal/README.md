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
