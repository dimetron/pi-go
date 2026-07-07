# Requirements

## Questions & Answers

### Q1

What is the **scope** of this work?

1. **Pure migration** — bump to `google.golang.org/adk/v2 v2.0.0`, update all import paths, fix breaking
   API changes (`session.NewEvent(ctx, …)`, unified `agent.Context`, etc.), keep current behavior and
   existing mock patterns. **No new v2 features used.** Smallest, lowest-risk scope.
2. **Migration + modernize mocks** — same as #1, **plus** adopt `agent.StrictContextMock` everywhere.
3. **Migration + adopt one new v2 feature** — same as #2, plus one new feature.
4. **Full v2 rewrite** — migration + mocks + multiple new v2 features.

### A1

**1 — Pure migration.** Bump the module to `google.golang.org/adk/v2 v2.0.0`, fix all import paths and
breaking API changes, keep current behavior and existing mock patterns. No new v2 features adopted.
Existing hand-rolled `MockCallbackContext` / `MockToolContext` fakes are updated in place (added methods
as needed) — not replaced with `StrictContextMock`.

### Q2

How should the change be landed in trunk?

1. Single drop-in commit / PR (all import + API updates in one pass).
2. Staged, one ADK package at a time (uses a temporary `replace` directive).
3. Big-bang on a feature branch, squash-merge.

### A2

**3 — Big-bang on a feature branch, squash-merge.** Branch name: **`feature/adk-20-migration`**.
The entire migration is developed on this branch and merged to trunk as a single squash commit.
No `replace` directive in `go.mod`. Trunk stays green throughout.

### Q3

How should the new dependency be pinned in `go.mod`?

1. **Exact pin** — `google.golang.org/adk/v2 v2.0.0`. Reproducible.
2. **Floating patch** — `v2.0` (`>= v2.0.0, < v2.1.0`).
3. **Floating minor** — `v2` (`>= v2.0.0, < v3.0.0`).

### A3

**1 — Exact pin** to `google.golang.org/adk/v2 v2.0.0`. Bumping to v2.0.x or v2.1.0 is a separate,
intentional change.

### Q4

What must be true for the squash-merge to trunk to be allowed? Multi-select.

- **A.** `go build ./...` succeeds.
- **B.** `go vet ./...` clean.
- **C.** `go test ./...` passes.
- **D.** `go test -race ./...` passes.
- **E.** `golangci-lint run ./...` passes.
- **F.** All e2e tests pass.
- **G.** Coverage does not regress (current level).
- **H.** Manual TUI / print-mode smoke test.
- **I.** `go mod tidy` produces no diff.

### A4

**A, B, C, D, E, F, G, I.** Every automated gate must pass. **H (manual smoke test) is NOT required**
for this spec — the automated gates are deemed sufficient evidence that the migration is sound.

For **G** (coverage): the threshold is "does not regress below the current level". The current
coverage number is to be measured on the parent commit before the migration begins and recorded in
the spec; that number is the floor for the migrated commit.

### Q5

For the unified `agent.Context` interface, our existing hand-rolled `MockCallbackContext` / mock
context fakes must grow to satisfy the new surface. Two options:

- (a) Add **all** the methods listed in the v2 migration guide (Actions, FunctionCallID,
  ToolConfirmation, RequestConfirmation, SearchMemory, …) — even if no test currently exercises
  them — for forward-compat and clean architecture.
- (b) Add only the **minimum** methods the compiler forces us to add.

### A5

**(a) — follow best practice and clean architecture.** Add every method listed in the v2
migration guide to the hand-rolled mock fakes, even if no test currently calls them. Mocks should
satisfy the full v2 `agent.Context` interface so future context-surface growth doesn't break our
tests again. This matches the migration guide's example block exactly.

### Q6

How should we handle transitive / indirect dependency changes that v2.0.0 pulls in?

- **(α)** Accept whatever `go mod tidy` produces (let deps float to what v2.0.0 requires), or
  minimize the diff by pinning transitive versions to what v1.4.0 had?
- **(β)** v2 ADK pulls in `github.com/a2aproject/a2a-go/v2` (a major-version bump from v0.x).
  pi-go's `go.mod` already has `a2a-go/v2 v2.3.1`. Confirm this is acceptable, or audit
  direct usage of `a2a-go` separately?

### A6

- **(α) = accept.** Let `go mod tidy` do its thing. Bumping transitive deps is the standard
  Go-modules behavior on a major-version bump and is what the project already does.
- **(β) = confirm (no separate audit).** pi-go's `go.mod` already pins `a2a-go/v2 v2.3.1`, which
  matches what v2.0.0 requires. The v2 ADK bringing in `a2a-go/v2` is consistent with what we
  already use; no additional audit is required for this spec.

### Q7 (added)

After the migration, should a security audit be run?

### A7

**Yes — run a security audit after the migration.** The repo already ships a `pi audit` subcommand
(`internal/audit/`, per README "Skills audit — Security scanning for hidden Unicode characters,
BiDi attacks, and supply-chain threats"). After the migration is green on the automated gates, run
`pi audit` (or equivalent) and attach the report to the squash-merge PR. Any new findings introduced
by the v2 ADK bump must be triaged before merge.

Specifics to capture during planning:

- The exact audit command(s) and any flags.
- Where the report is attached (PR description, comment, or `specs/.../summary.md`).
- Triage policy for findings: blocker vs. follow-up issue.

### Q8 (research findings)

How should we handle three open questions raised during research?

- **(a)** Pre-existing `internal/tui.TestCommitCommand_ConfirmCommits` failure (missing 1Password
  CLI agent, unrelated to ADK): (i) accept as known failure, (ii) skip via build tag/filter,
  (iii) fix as part of the migration.
- **(b)** Hand-rolled `agent.InvocationContext` mocks beyond the three `agent.ToolContext` mocks:
  add the new v2 methods (`IsolationScope`, `ResumedInput`, `WithICDelta`) for full
  `agent.Context` compliance, or leave them as-is.
- **(c)** Post-migration `pi audit` scope: (i) default (scan our SKILL.md files), (ii) extend to
  ADK v2 module cache, (iii) run `make check-cve` too.

### A8

- **(a) = iii.** Fix `TestCommitCommand_ConfirmCommits` as part of the migration PR so the
  post-migration gate is honestly green. The fix is environmental (1Password CLI agent not
  installed in the dev container) — likely a `-short`-style guard, an environment-variable
  skip, or a mock of the 1Password subprocess. Exact mechanism decided at design time.
- **(b) = full** (interpreted from "b" alone; consistent with A5's "best practice and clean
  architecture"). All hand-rolled `agent.InvocationContext` mocks in this repo gain the three
  new v2 methods (`IsolationScope`, `ResumedInput`, `WithICDelta`) so they remain forward-
  compatible. The three `agent.ToolContext` mocks (in `internal/extension/hooks_test.go`,
  `internal/tools/tool_invoke_test.go`, `internal/palace/tool_invoke_test.go`) also gain the
  full v2 `agent.Context` method set per A5. **If this interpretation is wrong, correct at
  design review.**
- **(c) = i.** Run `pi audit` against default skill directories (no scope extension, no
  separate `govulncheck`/`grype` run for this spec). Any finding introduced by the migration
  must be triaged. The output is attached to the squash-merge PR description.

