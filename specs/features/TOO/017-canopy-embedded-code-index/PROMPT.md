# Agent Prompt — Canopy embedded code index

You are implementing spec `specs/features/TOO/017-canopy-embedded-code-index/`.

Read in this order before writing code:

1. `requirements.md` — acceptance criteria and hard constraints
2. `research/canopy-evaluation.md` — measured facts; do not re-derive them
3. `design.md` — package shape, API, lifecycle
4. `plan.md` — the seven tasks, in order

Also read `.claude/skills/code-guidelines-go` conventions and follow the existing structure of
`internal/tools/lsp.go`, which is the closest analogue to what you are building.

## Goal

Embed canopy (`m31labs.dev/canopy`) as an in-process structural code index. Expose
`code_definition`, `code_references`, `code_symbols`, `code_callgraph`, `code_impact`,
`code_complexity`. Retire the four superseded `lsp_*` navigation tools.

## Hard constraints — violating any of these means stop and report

1. **`CGO_ENABLED=0 go build ./...` must keep working.** pi-go's `go install`-with-only-a-toolchain
   property is not negotiable. Verify this in Task 1 before writing anything else.
2. **Do not remove `lsp_diagnostics` or `lsp_code_action`.** Canopy parses; it does not
   type-check. It cannot see `assignment mismatch: 2 variables but f returns 3 values` in a
   syntactically valid file. Removing the LSP diagnostics path removes the agent's only
   signal that it broke the build.
3. **Never index at startup.** Lazy on first tool use. Indexing this repo's root did not
   finish in 120 s because it walks `tmp/`.
4. **Never return an empty result for a broken index.** "No references found" and "the index
   failed to build" must be distinguishable. Silent degradation is the existing LSP failure
   mode and must not be reproduced.
5. **Pin the canopy version.** `v0.18.0`. Not `@latest`.
6. **Canopy types must not escape `internal/codeindex`.** Return package-local structs so a
   canopy upgrade cannot ripple into tool code.

## Working agreement

- One task per commit. Run `go build ./... && go vet ./... && golangci-lint run` after each.
- Tests use a fixture tree under `testdata/`, never this repository, and must not require
  network access or an installed language server.
- If a measured claim in `research/` turns out to be wrong, **update the research file** and
  say so in your report. Do not silently work around it.
- Task 6 (removing LSP navigation tools) is a separate commit from tasks 1-5 so it can be
  reverted on its own.

## Known traps

- `xref.Graph.FindDefinitions` takes `(pattern string, regexMode bool)` and returns
  `([]Definition, error)` — two arguments and two results.
- Build the `xref.Graph` once in `Ensure` and reuse it. Rebuilding per query is expensive.
- Canopy's chunker is in `canopy/internal/chunk` and **cannot be imported**. Do not plan
  around it; AST chunking for the memory miner is a separate spec.
- `pkg/contextbundle` exports `Greet(name string) string` and appears to be a stub. Do not
  build on it.
- Canopy's MCP server uses `Content-Length` framing, not newline-delimited JSON. Irrelevant
  if you embed the library — which you are — but do not be misled by the MCP docs.

## Definition of done

Every box in `plan.md`'s verification checklist is ticked, `make test` and the coverage gate
pass, `golangci-lint run` is clean, and `SUMMARY.md` is written recording what was built,
what deviated from the design, and what was learned.

The single most important manual check: **introduce a deliberate type error and confirm
`lsp_diagnostics` still reports it.** If it does not, the change is not done regardless of
what the tests say.
