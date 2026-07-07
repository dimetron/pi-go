# ADK 2.0.0 Migration

## Objective

Bump pi-go from `google.golang.org/adk v1.4.0` to `google.golang.org/adk/v2 v2.0.0`
on a single `feature/adk-20-migration` branch (squash-merged to trunk).
Pure, mechanical migration: update import paths, widen callback/functiontool
handler context types to the unified `agent.Context`, grow 4 hand-rolled
context mocks to satisfy `var _ agent.Context = ...`. No new v2 features
adopted; no ADK runtime behavior change. All 8 automated gates must be green
(see Gates below); the known `TestCommitCommand_ConfirmCommits` baseline
failure must be fixed or reliably isolated in this PR.

## Key Requirements

1. **Module path swap** — `google.golang.org/adk/` → `google.golang.org/adk/v2/`
   on all 89 importing files (160 import sites).
2. **Type widening** — every `BeforeToolCallback` / `AfterToolCallback` /
   `BeforeModelCallback` / `AfterModelCallback` / `functiontool.Func` /
   `functiontool.New` handler takes `agent.Context` (not `agent.ToolContext`
   or `agent.CallbackContext`).
3. **Mock forward-compat** — the 4 hand-rolled `mockToolCtx` /
   `mockReadonlyContext` mocks grow the full v2 `agent.Context` method set,
   with assertion retargeted to `var _ agent.Context = ...`. All other
   `agent.InvocationContext` mocks gain the 3 new v2 methods
   (`IsolationScope`, `ResumedInput`, `WithICDelta`).
4. **No new v2 features** — do not adopt the graph workflow engine,
   collaboration-agent modes, `StrictContextMock`, `platform`, `plugin`,
   or `workflow` packages.
5. **No public API changes in pi-go** — migration is internal-only.
6. **Exact pin** — `google.golang.org/adk/v2 v2.0.0` (no floating version,
   no `replace` directive).
7. **`go mod tidy` is idempotent** — second run produces no diff.
8. **`pi audit` runs clean or triaged** — output attached to squash-merge PR.
9. **Coverage does not regress** below the floor measured on the parent commit.

## Acceptance Criteria

### Build & compile

- Given the migration branch, when `go build ./...` is run, then exit 0 with no warnings.
- Given the migration branch, when `go vet ./...` is run, then no diagnostics.
- Given the migration branch, when `go mod tidy` is run twice, then the second run is a no-op.

### Type assertions

- Given the 4 hand-rolled mocks
  (`internal/extension/hooks_test.go:262`,
  `internal/extension/hooks_test.go:581`,
  `internal/tools/tool_invoke_test.go:43`,
  `internal/palace/tool_invoke_test.go:40`),
  when the file is compiled, then the assertion `var _ agent.Context = ...` succeeds.

### Tests

- Given the migration branch, when `go test ./...` is run, then all tests pass.
  The parent-commit `TestCommitCommand_ConfirmCommits` failure is not an
  allowed final-state failure.

### Race detector

- Given the migration branch, when `go test -race ./...` is run, then
  all tests pass with no race reports.

### E2E tests

- Given the migration branch, when `make test-e2e` is run, then all e2e
  tests pass.

### Lint

- Given the migration branch, when `make lint` is run, then no diagnostics
  are produced.

### Coverage

- Given the parent-commit coverage floor `F` (recorded in `summary.md`),
  when `make test-coverage` is run on the migration branch, then coverage
  is `≥ F`.

### Security audit

- Given the migration branch, when `pi audit` is run with default flags,
  then no new critical findings are introduced. Output is attached to
  the squash-merge PR. Any new finding is triaged before merge.

## Implementation Slices

1. **Scaffold branch + measure baseline** — `git switch -c feature/adk-20-migration`,
   run all 8 gates, record baseline pass/fail set + coverage floor in
   `summary.md`. Verify: `summary.md` has the baseline data.

2. **Fix known TUI fixture failure** — make `TestCommitCommand_ConfirmCommits`
  independent of the ambient 1Password signing agent (or add a targeted
  environment-aware skip). Verify: `go test ./internal/tui/...` and `make test` green.

3. **Compile-safe production migration slice** — in one green commit: swap
  `adk v1.4.0` → `adk/v2 v2.0.0`, rewrite all ADK imports, widen production
  callback/functiontool signatures to `agent.Context`, and run `go mod tidy`.
  Verify: `go build ./...`, idempotent `go mod tidy`,
  and `go list -m google.golang.org/adk/v2` reports `v2.0.0`.

4. **Test type sweep** — widen any test callback/functiontool handler
  signatures. Verify: repo import scans exclude `.scratch/**` and `vendor/**`;
  `go vet ./...` reaches only mock method-set failures, or is clean if mocks
  are updated in the same pass.

5. **Mock growth — 4 hand-rolled `agent.Context` mocks** — add the full v2
   `agent.Context` method set to each of the 4 mocks in
   `internal/extension/hooks_test.go:262`,
   `internal/extension/hooks_test.go:581`,
   `internal/tools/tool_invoke_test.go:43`,
   `internal/palace/tool_invoke_test.go:40`.
   Retarget assertions to `var _ agent.Context = ...`.
   Verify: `go vet ./...` clean (the `var _` line is the type-system
   check); `go test ./internal/extension/... ./internal/tools/... ./internal/palace/...` passes.

6. **InvocationContext mock forward-compat (A8b)** — find every other
   hand-rolled `agent.InvocationContext` mock in the repo and add the
   3 new v2 methods (`IsolationScope`, `ResumedInput`, `WithICDelta`).
   Do not retarget assertions. Verify: `go vet ./...` clean; `go test ./...` passes.

7. **Re-baseline all gates** — run all 8 A4 gates + `pi audit`; rerun
  `TestCommitCommand_ConfirmCommits` in isolation. Verify: every gate green,
  audit clean or warning-only with documented triage, coverage ≥ floor.

8. **Open squash PR** — push branch, open PR, attach `pi audit` output,
    squash-merge to trunk per A2.

## Gates

- **build**: `go build ./...`
- **test**: `make test` (= `go test ./...`)
- **race**: `go test -race ./...`
- **e2e**: `make test-e2e` (= `go test -tags e2e ./...`)
- **lint**: `make lint` (= `golangci-lint run ./...`)
- **vet**: `make vet` (= `go vet ./...`)
- **coverage**: `make test-coverage`
- **mod-tidy**: `go mod tidy` (must be idempotent — second run no-op)
- **audit**: `pi audit` (per A7, output attached to PR)

## Reference

- Design: `specs/features/TOO/007-adk-2-0-adoption/design.md`
- Outline: `specs/features/TOO/007-adk-2-0-adoption/outline.md`
- Plan: `specs/features/TOO/007-adk-2-0-adoption/plan.md`
- Requirements: `specs/features/TOO/007-adk-2-0-adoption/requirements.md`
- Rough idea: `specs/features/TOO/007-adk-2-0-adoption/rough-idea.md`
- Research: `specs/features/TOO/007-adk-2-0-adoption/research/`
  - `v1-usage-surface.md` — how pi-go's v1.4.0 code uses ADK today
  - `v2-api-delta.md` — v1.4 → v2.0 API delta
  - `call-sites.md` — per-file inventory of required changes
  - `adk-docs-addendum.md` — official 2.0 docs findings
  - `README.md` — TL;DR + sources
- v2 source (verified): `google.golang.org/adk/v2@v2.0.0/agent/context.go`
  lines 142–240 (the `agent.Context` interface)
- v1 source (verified): `google.golang.org/adk@v1.4.0/agent/callback_context.go`
  (the v1 `CallbackContext` / `ToolContext` interfaces)

## Constraints

- **Branch:** `feature/adk-20-migration` only. No commits to trunk during
  the migration. Squash-merge per A2.
- **Module pin:** exact `google.golang.org/adk/v2 v2.0.0`. No floating
  versions, no `replace` directive, no `v2.0` or `v2` ranges.
- **No new v2 features** — do not add imports from `platform`, `plugin`,
  `workflow` packages; do not use `StrictContextMock`; do not enable
  `chat`/`task`/`single_turn` collaboration modes.
- **Mock forward-compat** — add the **full** v2 method set (per A5), not
  just the minimum the compiler forces. Mocks must satisfy
  `agent.Context` so future context-surface growth doesn't break tests.
- **No new ADK behavior tests** — do not introduce tests for new ADK v2
  features. A narrowly scoped TUI fixture fix is required by A8(a)=iii.
- **Known failure fixed or isolated** — `TestCommitCommand_ConfirmCommits`
  must pass, or be skipped only for a narrowly documented missing-environment
  condition introduced by the fixture fix.
- **Coverage floor** — measured on the parent commit before slice 1
  begins, recorded in `summary.md`, enforced as `≥ floor` in slice 7.
- **`go mod tidy` idempotent** — slice 3 establishes this; slice 7
  re-confirms.
- **Pi audit required** — slice 7 runs it; output is attached to the
  squash-merge PR. Triage any new finding before merge (per A7).
