# Agent Prompt — Memory subsystem repair

You are implementing `specs/memory-fixes/plan.md` in the pi-go repository.

## Read first, in order

1. `specs/memory-fixes/rough-idea.md` — what is broken and why it matters
2. `specs/memory-fixes/research/findings.md` — the evidence; every claim is
   measured, do not re-derive it
3. `specs/memory-fixes/requirements.md` — acceptance criteria R1–R9
4. `specs/memory-fixes/design.md` — the chosen approach and the rejected ones
5. `specs/memory-fixes/plan.md` — the slices you are executing

Also read `CLAUDE.md` (worktrees, signed commits, the two environment traps) and
load the `code-guidelines-go` skill before writing Go.

## The one thing to understand before touching code

ADK ends the after-tool callback chain at the first callback returning a non-nil
result. pi-go hands ADK six callbacks that all return the result map, so only
the first runs. The memory recorder is last. That is why
`~/.pi-go/memory/claude-mem.db` holds 6228 sessions and zero observations.

Fixing the order alone does not work — it was tried, and the LSP callback simply
becomes the new short-circuit. Compose the callbacks into one, as `design.md`
§ 1 specifies.

## How to work

- One worktree for the whole task:
  `git worktree add -b fix/memory-subsystem .worktrees/fix-memory-subsystem HEAD`
- One commit per slice, `-s -S`, sandbox disabled for the commit. Never
  `--no-verify`.
- After each slice: `make test`, `make vet`, `make lint`. Run `internal/cli`
  tests **outside the sandbox** — `httptest.NewServer` cannot bind inside it,
  and that panic is not a real failure.
- Do not proceed past Slice 2 until the manual verification in Slice 1 shows a
  non-zero observation count.

## Manual verification that matters

After Slice 1:

```bash
make build
./bin/pi --mode print "read go.mod and tell me the module name"
sqlite3 ~/.pi-go/memory/claude-mem.db "select id,type,title,tool_name from observations order by id desc limit 5"
```

Expect at least one row. Zero rows means the chain is still short-circuiting —
stop and find out where before continuing.

After Slice 4:

```bash
pi memory init . && pi memory status     # note the resolved DB path
```

The path printed must be the one an agent session in the same directory opens.

## Scope discipline

Implement the slices as written. If you find further defects, record them in
`summary.md` rather than fixing them inline — this branch is already touching
four subsystems that have been dead long enough that re-enabling them is itself
a behaviour change worth isolating.

Do not attempt upstream MemPalace parity. `research/comparison.md` lists what is
missing and it is deliberately out of scope.

## Definition of done

- R1–R9 in `requirements.md` all satisfied.
- `TestObservationReachesPalace` and `TestShortSessionRecordsObservation` fail on
  `69b4d03` and pass on the branch.
- A real session records observations; `pi memory recent` shows them.
- `summary.md` written: what landed, the recall@5 baseline, whether re-enabling
  the compactor and LSP hooks visibly changed agent behaviour, and any defect
  you found and deliberately left alone.
