# Plan — `google.golang.org/adk` v1.4.0 → v2.0.0 Migration

> Vertical-slice execution plan for the migration. Each slice is a unit of
> work that lands on a green build/test, then we move to the next slice.
> Trunk never goes red.

## Conventions used throughout

- **Branch:** `feature/adk-20-migration` (per requirements A2).
- **Coverage floor:** measured in slice 1, recorded in `summary.md` (per A4-G).
- **Known baseline failure:** `internal/tui.TestCommitCommand_ConfirmCommits`
   fails on the parent commit; A8(a)=iii requires fixing or reliably isolating
   it in this migration PR before final gates.
- **Type assertions:** the 4 listed in `design.md` §5.5 are the only
  ones that need retargeting. All other `var _ agent.InvocationContext = ...`
   lines stay; their mock structs just grow methods (slice 6).
- **Command for import sweep:** `find . -name '*.go' -not -path './.scratch/*' -not -path './vendor/*' -print0 | xargs -0 sed -i '' -E 's|google\.golang\.org/adk/|google.golang.org/adk/v2/|g'`
  (BSD sed on macOS; `--in-place` without arg). **Do not run `gofmt -w` on
  unrelated changes** — keep the diff to only what the migration requires.
- **Search gates:** every `rg` verification that scans the repo must exclude
   scratch/vendor content, e.g. `--glob '!.scratch/**' --glob '!vendor/**'`,
   because `.scratch/` intentionally contains ADK reference snapshots.
- **All file paths below are relative to the repo root** unless otherwise noted.

---

## Slice 1: Scaffold branch + measure baseline

**Goal:** Establish a clean baseline before any change.

**Steps:**

1. `git fetch origin && git switch -c feature/adk-20-migration origin/trunk`
   (or `main`, per repo's primary branch). Confirm branch is up-to-date.
2. Verify working tree is clean: `git status --porcelain` returns nothing.
3. Run and record baseline results (each command's pass/fail set + duration):
   - `go build ./...`
   - `make vet` (i.e. `go vet ./...`)
   - `make test` (i.e. `go test ./...`)
   - `make test-e2e` (i.e. `go test -tags e2e ./...`)
   - `go test -race ./...`
   - `make lint` (i.e. `golangci-lint run ./...`)
   - `make test-coverage` (note the trailing `%` from `go tool cover -func`)
4. Record the **baseline failure set** in
   `specs/features/TOO/007-adk-2-0-adoption/summary.md` under
   `## Baseline (parent commit)`. Expected: `TestCommitCommand_ConfirmCommits`
   fails; everything else passes. Note that this failure is not an allowed
   final-state failure. If anything else fails, **stop** and reconcile with
   the user before proceeding.
5. Record the **coverage floor** (the percentage reported by
   `make test-coverage`) in `summary.md` under `## Coverage floor`.

**Verify:** `summary.md` exists with the baseline data filled in.

**Commit:** `chore(spec): record ADK migration baseline`

**Depends on:** nothing.

---

## Slice 2: Fix known TUI fixture failure

**Goal:** Make the parent-commit `TestCommitCommand_ConfirmCommits` failure
green before the ADK bump, so the migration's final `go test ./...` gate is
honestly green.

**Steps:**

1. Inspect `internal/tui/commit_test.go` and the commit-command code path to
   identify where the test inherits ambient git signing / 1Password config.
2. Prefer a fixture-local fix that preserves the behavior under test, such as:
   - configure the temporary test repository with `commit.gpgsign=false`,
     `tag.gpgsign=false`, and any needed signing-related overrides;
   - inject/mock the subprocess boundary if the test is meant to validate
     command orchestration rather than real signed commits.
3. Use a targeted environment-aware skip only if the signing dependency cannot
   be isolated without weakening the test's commit assertions.
4. Do not change ADK code in this slice.

**Verify:**
- `go test -run TestCommitCommand_ConfirmCommits ./internal/tui/ -v` passes or
  is skipped only for a narrowly documented missing-environment condition.
- `go test ./internal/tui/...` passes.
- `make test` passes on the parent ADK version.

**Commit:** `test(tui): isolate commit test from ambient signing config`

**Depends on:** slice 1.

---

## Slice 3: Compile-safe production migration

**Goal:** Bump ADK, rewrite all ADK imports, widen production
callback/functiontool handler types, and regenerate module metadata in one
buildable slice.

**Steps:**

1. Edit `go.mod` line 36:
   ```diff
   -    google.golang.org/adk v1.4.0
   +    google.golang.org/adk/v2 v2.0.0
   ```
2. Run the import sweep on all `.go` files before `go mod tidy` so test
   imports cannot cause the old `google.golang.org/adk` module to be re-added:
   ```bash
   find . -name '*.go' -not -path './.scratch/*' -not -path './vendor/*' -print0 | \
       xargs -0 sed -i '' -E 's|google\.golang\.org/adk/|google.golang.org/adk/v2/|g'
   ```
   (If running on Linux instead of macOS, drop the `''` after `-i`.)
3. In production files, widen every callback/functiontool first-parameter
   type from `agent.ToolContext`, `agent.CallbackContext`, or `tool.Context`
   to `agent.Context`. This includes `llmagent` callbacks and
   `functiontool.Func` handlers in `internal/tools/*.go` and
   `internal/palace/tool_*.go`.
4. Do not change method bodies. Every existing context method used by the
   bodies is preserved on `agent.Context`.
5. Run `go mod tidy`. This will download `google.golang.org/adk/v2 v2.0.0`,
   absorb the new indirect deps, drop the old ADK module requirement, and
   regenerate `go.sum`.
6. Verify the count of remaining v1-style imports is 0 outside scratch/vendor:
   ```bash
   rg -t go -n 'google\.golang\.org/adk/[a-z]' --glob '!.scratch/**' --glob '!vendor/**' .
   ```
   Expected: 0 matches.
7. Run `go build ./...`.
8. Run `go mod tidy` a second time and confirm idempotency (A4-I).
9. If `go build ./...` fails, the most likely cause is either a callback /
   functiontool literal that still takes an old context type, or a sub-package that
   doesn't exist in v2 (e.g. a renamed package). Check
   `research/call-sites.md` for any flagged rename; otherwise report and stop.

**Verify:**
- `go build ./...` exit 0.
- `go mod tidy` produces no diff on the second run.
- `go list -m google.golang.org/adk/v2` reports `v2.0.0`.
- No v1 ADK imports remain outside ignored scratch/vendor paths.

**Commit:** `refactor(adk): migrate production code to ADK v2.0.0`

**Depends on:** slice 2.

---

## Slice 4: Test type sweep

**Goal:** Update test callback/functiontool handler signatures now that test
imports already point at the v2 module path.

**Steps:**

1. Widen any test-local callback/functiontool handler parameters from
   `agent.ToolContext`, `agent.CallbackContext`, or `tool.Context` to
   `agent.Context` when they are assigned to v2 callback/functiontool types.
2. Verify zero remaining v1 imports outside scratch/vendor:
   ```bash
   rg -t go -n 'google\.golang\.org/adk/[a-z]' --glob '!.scratch/**' --glob '!vendor/**' .
   ```
   (Note the trailing `[a-z]` to exclude any false-positive matches against
   `google.golang.org/adk/v2/...`.) Expected: 0 matches.
3. Run `go vet ./...`. The vet step is more sensitive than build — it will
   flag unresolved symbols, unused imports, and broken type assertions.
4. If vet fails with a type-assertion error on the 4 lines in design §5.5,
   that's expected and **not fixed in this slice**. Note the errors and
   continue. (The fix is slice 5.)
5. If vet fails with any other error (e.g. an unresolved identifier that
   isn't a type-assertion mismatch), stop and investigate.

**Verify:** `rg -t go 'google\.golang\.org/adk/[a-z]' --glob '!.scratch/**' --glob '!vendor/**'` returns 0 matches.
`go vet ./...` reports only the 4 known type-assertion mismatches (or none,
if the mocks are updated in the same pass).

**Commit:** `refactor(adk): update test context signatures for ADK v2`

**Depends on:** slice 3.

---

## Slice 5: Mock growth — 4 hand-rolled `agent.Context` mocks

**Goal:** Make the 4 hand-rolled mocks in `design.md` §5.5 satisfy
`var _ agent.Context = ...`.

**Files & lines (from `design.md` §5.5 and verified via `rg`):**

- `internal/extension/hooks_test.go:262` — `var _ agent.ToolContext = (*mockToolCtx)(nil)`
- `internal/extension/hooks_test.go:581` — `var _ agent.CallbackContext = (*mockReadonlyContext)(nil)`
- `internal/tools/tool_invoke_test.go:43` — `var _ agent.ToolContext = mockToolCtx{}`
- `internal/palace/tool_invoke_test.go:40` — `var _ agent.ToolContext = mockToolCtx{}`

**Note on `mockReadonlyContext`:** today it satisfies `CallbackContext`
(small interface). Widening to `agent.Context` requires adding the **full**
v2 method set (this is the bigger change among the 4 — confirm with
the design's interpretation of A5 = full set, which the user has approved).

**Steps (per file):**

1. **Read** the current `mockToolCtx` (or `mockReadonlyContext`) struct
   and method set. Cross-reference against the v2.0.0 `agent.Context`
   interface (`/Users/dimetron/p6s/pi-dev/pi-go/.scratch/adk-v2/agent/context.go`
   lines 142–240, or
   `https://raw.githubusercontent.com/google/adk-go/v2.0.0/agent/context.go`).
2. **Add** the missing methods using the convention from `design.md` §5.4
   (safe no-op defaults; struct fields if a test needs a non-default value).
3. **Retarget** the type assertion:
   - `mockToolCtx` files: `var _ agent.ToolContext = ...` → `var _ agent.Context = ...`
   - `mockReadonlyContext` file: `var _ agent.CallbackContext = ...` → `var _ agent.Context = ...`
4. **Run** `go vet ./...` after each file. The vet step is the type-system
   assertion — if any method is missing, vet fails on the `var _` line.
5. If vet fails with a method-set mismatch, read the error to learn which
   method is missing and add it.

**Exact method set to add (per `design.md` §5.4, verified against v2 source):**

```go
// ReadonlyContext methods (already present on most mocks; verify)
context.Context       // via embedded ctx field
UserContent() *genai.Content
InvocationID() string
AgentName() string
ReadonlyState() session.ReadonlyState
UserID() string
AppName() string
SessionID() string
Branch() string

// InvocationContext methods
Artifacts() agent.Artifacts
Memory() agent.Memory
Session() session.Session
RunConfig() *agent.RunConfig
EndInvocation()
Ended() bool
WithContext(ctx context.Context) agent.InvocationContext

// CallbackContext extensions
State() session.State

// ToolContext extensions
FunctionCallID() string
Actions() *session.EventActions
SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error)
ToolConfirmation() *toolconfirmation.ToolConfirmation
RequestConfirmation(hint string, payload any) error

// NEW in v2 (must be added)
IsolationScope() string
ResumedInput(interruptID string) (any, bool)
WithICDelta(d *agent.InvocationContextDelta) agent.InvocationContext
Path() string
RunID() string
SubScheduler() agent.DynamicSubScheduler
WithAgentContext(ctx context.Context) agent.Context
WithAgentTimeout(timeout time.Duration) (agent.Context, context.CancelFunc)
WithAgentCancel() (agent.Context, context.CancelFunc)
OutputForAncestors() []string
WithDelta(d *agent.CommonContextDelta) agent.Context
```

**Implementation convention** (from `design.md` §5.4):

```go
func (m *mockToolCtx) IsolationScope() string { return m.IsolationScopeVal }
func (m *mockToolCtx) ResumedInput(string) (any, bool) { return nil, false }
func (m *mockToolCtx) WithICDelta(d *agent.InvocationContextDelta) agent.InvocationContext { return m }
func (m *mockToolCtx) Path() string { return "" }
func (m *mockToolCtx) RunID() string { return "" }
func (m *mockToolCtx) SubScheduler() agent.DynamicSubScheduler { return nil }
func (m *mockToolCtx) WithAgentContext(ctx context.Context) agent.Context { m.Ctx = ctx; return m }
func (m *mockToolCtx) WithAgentTimeout(d time.Duration) (agent.Context, context.CancelFunc) {
    return m, func() {}
}
func (m *mockToolCtx) WithAgentCancel() (agent.Context, context.CancelFunc) {
    return m, func() {}
}
func (m *mockToolCtx) OutputForAncestors() []string { return nil }
func (m *mockToolCtx) WithDelta(d *agent.CommonContextDelta) agent.Context { return m }
```

For the `Memory() agent.Memory` method, if the existing mock struct doesn't
already store a memory service, default to `nil` (matches v2's "may be nil"
semantics — v2's `SearchMemory` already guards on `Memory() == nil`).

**Verify:** `go vet ./...` clean (no type-assertion errors). `go test ./internal/extension/... ./internal/tools/... ./internal/palace/...` passes.

**Commit:** `test(adk): grow hand-rolled context mocks for v2.0.0 agent.Context`

**Depends on:** slice 4.

---

## Slice 6: InvocationContext mock forward-compat (A8b)

**Goal:** All other hand-rolled `agent.InvocationContext` mocks in the
repo gain the 3 new v2 methods.

**Steps:**

1. Find all `agent.InvocationContext` type assertions and mock structs:
   ```bash
   rg -t go -n 'var _ agent\.InvocationContext' --glob '!.scratch/**' --glob '!vendor/**' .
   rg -t go -n 'InvocationContext interface' --glob '!.scratch/**' --glob '!vendor/**' .
   ```
2. For each non-trivial mock struct, add the 3 methods:
   - `IsolationScope() string` — return `""` (or a struct field if tests need it).
   - `ResumedInput(interruptID string) (any, bool)` — return `(nil, false)`.
   - `WithICDelta(d *agent.InvocationContextDelta) agent.InvocationContext` — return `m` (self).
3. **Do not retarget** the assertion (it already says `agent.InvocationContext`,
   which is unchanged in shape — just grew 3 methods).
4. Run `go vet ./...` after each mock struct to confirm the assertion still
   holds.

**Verify:** `go vet ./...` clean. `go test ./...` passes.

**Commit:** `test(adk): add v2.0.0 InvocationContext methods to hand-rolled mocks`

**Depends on:** slice 5.

---

## Slice 7: Re-baseline all gates

**Goal:** Verify the A4 + A7 gate set is green on the branch.

**Steps:**

1. Run, in order, capturing pass/fail:
   - `go build ./...`
   - `make vet`
   - `make test`
   - `go test -race ./...`
   - `make test-e2e`
   - `make lint`
   - `make test-coverage` (compare the trailing `%` against the floor from
     slice 1)
2. Run `go mod tidy` and confirm zero diff in `go.mod` / `go.sum` (A4-I).
3. Run `pi audit` (per A7). Capture the output. **Triage any new finding
   introduced by the v2 ADK bump** (per A7: "any new finding introduced
   by the v2 ADK bump must be triaged before merge"). Save the report
   under `specs/features/TOO/007-adk-2-0-adoption/pi-audit-report.txt`.
   Exit code 0 is clean; exit code 2 is acceptable only when warning findings
   are documented and triaged; exit code 1 blocks merge.
4. Run the formerly failing TUI test in isolation and record the result:
   ```bash
   go test -run TestCommitCommand_ConfirmCommits ./internal/tui/ -v
   ```
   It must pass, or be skipped only for the narrowly documented condition
   introduced in slice 2.
5. Update `summary.md` with the post-migration gate results, coverage
   comparison, audit result, and TUI fixture result.

**Verify:** All 8 A4 gates green; `pi audit` clean or triaged; coverage ≥ floor.

**Commit:** `chore(spec): record post-migration gate results in summary.md`

**Depends on:** slice 6.

---

## Slice 8: Open squash PR

**Goal:** Open the PR per A2 (squash-merge).

**Steps:**

1. `git log --oneline` — confirm the commits from slices 1 through 7
   are all present on the branch.
2. `git push origin feature/adk-20-migration`.
3. Open a PR against trunk with the description:
   ```
   ## ADK 2.0.0 migration

   - Bumps `google.golang.org/adk` v1.4.0 → `google.golang.org/adk/v2 v2.0.0`.
   - Updates 89 files / 160 import sites (mechanical).
   - Widens callback & functiontool handler context types to unified `agent.Context`.
   - Grows 4 hand-rolled `mockToolCtx` / `mockReadonlyContext` mocks to satisfy
     `var _ agent.Context = ...` (A5 = full forward-compat set).
   - Grows `agent.InvocationContext` mocks with the 3 new v2 methods (A8b).

   ## Gates (all green)

   - [ ] `go build ./...` ✅
   - [ ] `go vet ./...` ✅
   - [ ] `go test ./...` ✅
   - [ ] `go test -race ./...` ✅
   - [ ] `go test -tags e2e ./...` ✅
   - [ ] `make lint` ✅
   - [ ] `make test-coverage` (≥ floor: XX.X%) ✅
   - [ ] `go mod tidy` no-op ✅
   - [ ] `pi audit` (output attached) ✅

   Spec: `specs/features/TOO/007-adk-2-0-adoption/`
   ```
4. Attach the `pi audit` output to the PR (as a comment or file in the
   spec dir, per the user's preference at the time).
5. Mark the PR as **ready for review**. Once approved, **squash-merge**
   into trunk as a single commit.

**Verify:** PR is open, all CI checks green, review approved, squash-merged.

**Commit:** the squash-merge on trunk.

**Depends on:** slice 7.

---

## Summary of the slices

| # | Slice                                  | Files touched            | Verify command          | Commit scope |
|---|----------------------------------------|--------------------------|-------------------------|--------------|
| 1 | Baseline                               | `summary.md`             | (recording)             | spec         |
| 2 | TUI fixture fix                        | `internal/tui/...`       | `go test ./internal/tui/...` | test    |
| 3 | Production ADK migration               | `go.mod`, `go.sum`, Go import paths, production `.go` files | `go build ./...` | refactor |
| 4 | Test type sweep                        | test `.go` files         | `go vet ./...`          | refactor     |
| 5 | Mock growth + assertion retarget       | 4 test files             | `go vet ./...` clean    | test         |
| 6 | InvocationContext mock forward-compat  | TBD by `rg`              | `go vet ./...` clean    | test         |
| 7 | Re-baseline gates                      | `summary.md`, audit report | all 8 A4 + A7 gates   | chore        |
| 8 | Squash PR                              | (none — merge)           | PR open + CI green      | squash       |

Total instruction count: well under the 50-per-phase guideline. The work
is mostly mechanical; the main judgment calls are in slice 2 (how to
isolate the TUI fixture) and slice 5 (which fields to expose on each mock).
