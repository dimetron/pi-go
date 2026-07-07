# Outline — `google.golang.org/adk` v1.4.0 → v2.0.0 Migration

> Quick-review checkpoint before the full `plan.md`. Read top-to-bottom:
> scope → slices → mock surface → gates. If any line below is wrong, easier
> to correct now than in the 200-line plan.

## Scope (recap from requirements.md A1–A8)

Pure, mechanical migration on branch **`feature/adk-20-migration`**, squashed
into trunk. No new v2 features. Forward-compat mocks. All 8 automated gates
must be green (A4). Run `pi audit` and attach to PR (A7). The known
`TestCommitCommand_ConfirmCommits` environmental failure is fixed or
reliably isolated in this PR (A8a=iii).

## Touch points (89 files, 160 import sites)

- `internal/agent/` (6 files)
- `internal/cli/` (~5)
- `internal/provider/` (8 providers + tests)
- `internal/session/` (3)
- `internal/tools/` (registry + 15 individual tools)
- `internal/extension/` (hooks, mcp, tests)
- `internal/palace/` (8 tool files + tests)
- `internal/tui/`, `internal/atif/`, `internal/lsp/`, `internal/guardrail/`,
  `internal/memory/`, `internal/acp/`, `internal/jsonrpc/`

## Slices (vertical — each ends with a green build/test)

1. **Scaffold branch + measure baseline** — `git switch -c feature/adk-20-migration`,
   run `make test`, `make lint`, `make vet`, `make test-coverage`, capture baseline
   failure set + coverage floor. Record in `summary.md` (per A4-G).
2. **Fix known TUI fixture failure** — make `TestCommitCommand_ConfirmCommits`
   independent of the ambient 1Password signing agent (or add a targeted
   environment-aware skip). Verify: `go test ./internal/tui/...` and `make test` green.
3. **Compile-safe production migration slice** — in one green commit: edit
   `go.mod` (`adk v1.4.0` → `adk/v2 v2.0.0`), rewrite all ADK imports,
   widen production callback/functiontool signatures, and run `go mod tidy`.
   Verify: `go build ./...`, `go mod tidy` no-op, and no v1 production imports
   outside ignored scratch/vendor paths.
4. **Test type sweep** — widen any test callback / functiontool handler
   signatures. Verify: `go vet ./...` reaches only mock
   method-set failures, or is clean if mocks are updated in the same pass.
5. **Mock growth — 4 hand-rolled mocks** —
   - `internal/extension/hooks_test.go:262` `mockToolCtx` — add v2 methods
     (`IsolationScope`, `ResumedInput`, `WithICDelta`, `Path`, `RunID`,
     `SubScheduler`, `WithAgentContext`, `WithAgentTimeout`, `WithAgentCancel`,
     `OutputForAncestors`, `WithDelta`). Flip assertion to
     `var _ agent.Context = (*mockToolCtx)(nil)`.
   - `internal/extension/hooks_test.go:565` `mockReadonlyContext` — same growth
     (it's a `CallbackContext` today; widening to `Context` is a much bigger
     change — full method set). Flip assertion to
     `var _ agent.Context = (*mockReadonlyContext)(nil)`.
   - `internal/tools/tool_invoke_test.go:43` `mockToolCtx` — same growth.
     Flip assertion.
   - `internal/palace/tool_invoke_test.go:40` `mockToolCtx` — same growth.
     Flip assertion.
   Verify: `go vet ./...` clean (compile-time assertions enforce the full set).
6. **InvocationContext mocks (A8b = full)** — find every hand-rolled
   `agent.InvocationContext` mock in the repo and add the 3 new v2 methods
   (`IsolationScope`, `ResumedInput`, `WithICDelta`). Verify: `go vet ./...`
   clean.
7. **Re-baseline gates** — run all 8 A4 gates on the branch:
   `go build`, `go vet`, `go test ./...`, `go test -race ./...`,
   `go test -tags e2e ./...`, `make test-coverage` (≥ floor), `make lint`,
   `go mod tidy` (no-op), and `pi audit` (A7). Record results in `summary.md`.
8. **Write `summary.md` + open squash PR** — PR description cites A4 gates
    green, attaches `pi audit` output, references the spec. Squash-merge
    per A2.

## Mock surface — verbatim from v2.0.0 source

The 4 `mockToolCtx`/`mockReadonlyContext` must satisfy `agent.Context` =
`ReadonlyContext` ∪ `InvocationContext` ∪ callback tools ∪ workflow-node.
The full method set is in `design.md` §5.4 and verified against
`google.golang.org/adk/v2@v2.0.0/agent/context.go` lines 142–240.

## Gates (Makefile targets discovered during research)

- `make test` (`go test ./...`)
- `make test-e2e` (`go test -tags e2e ./...`)
- `make test-coverage` (`go test -coverprofile -coverpkg=./internal/... ./internal/...`)
- `make lint` (`golangci-lint run ./...`)
- `make vet` (`go vet ./...`)
- `go mod tidy` (no-op check, A4-I)
- `go test -race ./...` (A4-D)
- `pi audit` (A7)

## Out of scope

- Adopting graph workflow engine, collaboration-agent modes,
  `StrictContextMock`, `platform`, `plugin`, `workflow` packages.
- Bumping to v2.0.x or v2.1.0 (separate spec).
- Fixing unrelated pre-existing failures beyond the known
   `TestCommitCommand_ConfirmCommits` fixture issue.
