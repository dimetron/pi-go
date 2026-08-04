# Plan — Canopy embedded code index

Each task is independently verifiable. Run `go build ./... && go vet ./... && golangci-lint run`
after every one. Do not proceed past a failing gate.

## Task 1 — Add the dependency, prove the constraint holds

1. `go get m31labs.dev/canopy@v0.18.0` (pinned, not `@latest`), then `go mod tidy`.
2. Verify the non-negotiable property:
   ```
   CGO_ENABLED=0 go build ./...
   ```
3. Record binary size before and after (`go build -o /tmp/pi ./cmd/pi`).

**Gate:** builds with `CGO_ENABLED=0`. If it does not, stop — the requirement is violated and
the design needs revisiting.

## Task 2 — `internal/codeindex` skeleton

Create `config.go` and `index.go`:

- `Config{Root, CachePath, Exclude, Enabled}`
- `New(cfg) *Index`
- `Ensure(ctx)` — load cache → freshness check → build if needed → hold in memory
- Concurrency guard so N concurrent cold callers cause one build
- `Close()`

Ignores: `index.NewBuilderWithWorkspaceIgnoresAndExtras(root, extras)` with extras
`tmp/`, `vendor/`, `node_modules/`, `.git/`, `dist/`, `build/`, `target/`.

**Tests** (fixture tree under `testdata/`, never this repo):

- builds an index over the fixture
- second `Ensure` does not rebuild
- excluded directories are absent from the index
- build failure returns an error rather than a half-built index

**Gate:** `go test ./internal/codeindex/` green; first build of `internal/` under 5 s.

## Task 3 — Query layer

`query.go`: `Definitions`, `References`, `Symbols`, `CallGraph`, `Impact`, `Complexity`.

Backed by `xref.Build` / `xref.Graph.FindDefinitions(pattern, regexMode)`,
`impact.AnalyzeWithGraph`, `complexity.Analyze`. Build the `xref.Graph` once inside `Ensure`
and reuse it — it is not free.

Return package-local structs. No canopy type appears in a signature.

**Tests:** each method against the fixture, including the empty-result and
symbol-not-found cases.

**Gate:** `Definitions("AddDrawer")` against `internal/` returns the two known definitions
(`palace/drawer_service.go`, `palace/palace.go`) — the result verified during evaluation.

## Task 4 — Tools

`internal/tools/codeindex.go`, following the structure of `internal/tools/lsp.go`:

`code_definition`, `code_references`, `code_symbols`, `code_callgraph`, `code_impact`,
`code_complexity`, plus `CodeIndexTools(ix *codeindex.Index) ([]tool.Tool, error)`.

Each returns a structured map plus a display string for `formatToolResult`.

**Error surface:** an index failure returns a descriptive error. Never an empty result —
"no matches" and "index broken" must be distinguishable.

**Tests:** per-tool shape assertions; an index-unavailable case asserting a non-empty error.

**Gate:** tools registered and callable; no test depends on a language server or network.

## Task 5 — Wire into the agent

- `CodeIndexConfig` in `internal/config/config.go` (`enabled`, `cache_path`, `exclude`)
- `setupCodeIndex(cfg, cwd)` in `internal/cli/cli.go`, next to `setupMemory`/`setupPalace`,
  returning `(*codeindex.Index, func())`
- Append `CodeIndexTools` to `coreTools`
- Disabled or failed → register nothing, warn, continue

**Gate:** `pi` starts with no measurable added latency (nothing is indexed at startup).

## Task 6 — Retire the superseded LSP tools

Remove `newLSPDefinitionTool`, `newLSPReferencesTool`, `newLSPSymbolsTool`,
`newLSPWorkspaceSymbolTool` from the `LSPTools` builder list.

**Keep** `newLSPDiagnosticsTool`, `newLSPHoverTool`, `newLSPCodeActionTool`. Leave
`internal/lsp` itself untouched — it still owns servers, diagnostics and the after-edit hook.

**Gate:** `lsp_diagnostics` still fires after an edit. Verify by making a deliberate type
error and confirming it is reported — canopy will not catch it, and that is the whole point
of keeping this path.

## Task 7 — Docs and sign-off

- `ARCHITECTURE.md`: the canopy layer and the canopy/LSP division of labour
- `README.md`: new `code_*` tools; note the binary-size increase
- PR description states: +~30 MB, 10 transitive packages, MIT, no cgo
- `make test`, coverage gate, `golangci-lint run` clean
- Write `SUMMARY.md`

## Verification checklist

- [ ] `CGO_ENABLED=0 go build ./...` succeeds
- [ ] `code_*` tools work with **no** language server installed
- [ ] `lsp_diagnostics` still reports a deliberately introduced type error
- [ ] Index cached at `.pi-go/canopy.idx`, reused, refreshed incrementally
- [ ] `tmp/` and `vendor/` never indexed
- [ ] `code_index.enabled=false` registers no tools and builds nothing
- [ ] Index failure produces an explicit error, not an empty result
- [ ] No test requires network or a language server

## Sequencing

Tasks 1-5 are additive and safe to land together — nothing is removed, so the worst case is
unused tools. **Task 6 is the only breaking change** and should be a separate commit, so it
can be reverted independently if canopy-backed navigation turns out to be worse in practice
than the gopls-backed version it replaces.
