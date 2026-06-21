# Plan: Critical Repository Improvements

## Goal

Implement the highest-impact reliability and maintainability fixes identified in `OVERVIEW.md` without changing
user-facing behavior unless explicitly noted.

The plan is intentionally split into small vertical phases so each fix can be implemented, verified, and reviewed
independently.

## Scope

### In scope

- Documentation consistency fixes.
- Tool-call validation verification and regression tests.
- Context/cancellation propagation for long-running tool/subprocess paths.
- Subagent worktree path escaping investigation and fix.
- CI/release workflow parity.
- OTEL console exporter decision/implementation.
- Logging/output hygiene improvements.
- Memory architecture clarification.
- Roadmap/TODO issue hygiene.

### Out of scope

- Large UI redesigns.
- New LLM providers.
- New sandbox backends such as SSH, Docker, or Kubernetes.
- Broad refactors of TUI/CLI beyond documented extraction tasks.
- Changes that weaken sandbox safety.

## Phase 0 — Baseline and Safety Checks

### Objective

Establish the current test/build baseline before changing behavior.

### Tasks

1. Record current git status.
2. Run fast validation:
   ```bash
   go test ./...
   go test ./internal/tools/...
   go test ./internal/subagent/...
   go test ./internal/cli/...
   ```
3. Run build validation:
   ```bash
   make build
   ```
4. If baseline fails, document failures in this plan under a new “Baseline failures” section before implementing fixes.

### Acceptance criteria

- Current pass/fail baseline is known.
- Any pre-existing failures are separated from new failures.

---

## Phase 1 — Documentation and Version Consistency

### Objective

Remove immediate contributor-facing inconsistencies.

### Files

- `README.md`
- `ARCHITECTURE.md`
- `TODO.md`
- optionally `specs/AGENTS.md`

### Tasks

1. Update Go requirement in `README.md` to match `go.mod`:
    - Current: `Go 1.25+`
    - Expected: `Go 1.26.3+` or `Go 1.26+`, depending on intended support.
2. Search for stale `internal/rpc` references:
   ```bash
   rg "internal/rpc|\brpc/" README.md ARCHITECTURE.md specs docs internal cmd
   ```
3. Replace stale `internal/rpc` docs with `internal/jsonrpc` where that is the actual package.
4. Update `TODO.md` so it is not empty. Prefer one of:
    - A top-10 prioritized list linking to `ISSUES.md`, `ROADMAP.md`, and this plan.
    - A short note that active tasks live in specs/issues and roadmap docs.
5. Add a short “Docs source of truth” note in either `README.md` or `ARCHITECTURE.md`:
    - Architecture: `ARCHITECTURE.md`
    - User setup: `README.md`
    - Current critical fixes: this `PLAN.md`
    - Operational issues: `ISSUES.md`

### Verification

```bash
rg "Go 1\.25" README.md ARCHITECTURE.md docs specs || true
rg "internal/rpc" README.md ARCHITECTURE.md docs specs || true
go test ./...
```

### Acceptance criteria

- No stale Go `1.25+` requirement remains in top-level docs.
- `internal/rpc` references are corrected or explicitly explained.
- `TODO.md` points to real active priorities.
- Tests still pass.

---

## Phase 2 — Tool Schema Validation Regression Tests

### Objective

Confirm the current lenient schema/coercion implementation prevents high-frequency LLM tool-call failures and add
regression coverage.

### Files

- `internal/tools/registry.go`
- existing or new tests under `internal/tools/`
- `ISSUES.md`

### Tasks

1. Inspect current `coercingTool` test coverage:
   ```bash
   rg "coerc|lenient|schema|additionalProperties|missing properties|alias" internal/tools -g '*_test.go'
   ```
2. Add targeted unit tests for `coercingTool` or tool construction that cover:
    - Extra unknown properties do not fail before tool execution.
    - Missing optional properties use defaults or produce model-visible tool errors, not ADK pre-validation errors.
    - Integer fields sent as strings are coerced.
    - Boolean fields sent as strings are coerced.
    - Array/object fields sent as JSON strings are parsed when applicable.
    - Common aliases are remapped where aliases exist.
3. Add at least one integration-style test using a representative core tool declaration/runtime path if feasible.
4. Re-run recent session-log query from `ISSUES.md` and update the counts/status:
   ```bash
   find ~/.pi-go/sessions -name "events.jsonl" -mtime -7 | \
     xargs grep -h '"error":"[^"]*"' 2>/dev/null | \
     sed 's/.*"error":"//;s/".*//' | \
     sort | uniq -c | sort -rn
   ```
5. Update `ISSUES.md` to distinguish:
    - Historical errors.
    - Current observed errors.
    - Remaining action items.

### Verification

```bash
go test ./internal/tools/... -run 'Coerc|Schema|Tool' -count=1
go test ./internal/tools/... -count=1
go test ./... -count=1
```

### Acceptance criteria

- Regression tests cover the known schema/coercion failure modes.
- `ISSUES.md` reflects current state rather than stale historical assumptions.
- Full test suite passes.

---

## Phase 3 — Context and Cancellation Propagation

### Objective

Ensure long-running operations honor cancellation from the caller/TUI/CLI session.

### Files to inspect first

- `internal/tools/subagent.go`
- `internal/tools/grep.go`
- `internal/tools/bash.go`
- `internal/acp/client/session.go`
- related tests under `internal/tools/` and `internal/acp/`

### Tasks

1. Search for context misuse:
   ```bash
   rg "context\.Background\(\)|context\.TODO\(\)" internal cmd
   ```
2. Categorize each occurrence:
    - Acceptable initialization/bootstrap use.
    - Should be request-scoped/cancelable.
3. For request-scoped paths, pass the incoming `tool.Context`, command context, or parent context into subprocess/client
   calls.
4. For ripgrep execution, replace unbounded background context with a cancelable context derived from the caller and
   existing timeout rules.
5. For subagent execution, avoid defaulting to `context.Background()` when a cancelable context is available. If ADK
   context can be nil, wrap with a bounded timeout and document why.
6. For ACP session flow, pass session/request context through run loops instead of starting from background.
7. Add cancellation tests:
    - Long-running `bash` command gets terminated on context cancel.
    - Long-running grep/ripgrep operation exits on context cancel or timeout.
    - Subagent call exits on context cancel where practical.

### Verification

```bash
go test ./internal/tools/... -run 'Cancel|Timeout|Context|Bash|Grep|Subagent' -count=1
go test ./internal/acp/... -run 'Cancel|Context|Session' -count=1
go test ./... -count=1
```

### Acceptance criteria

- Request-scoped long-running paths no longer create root `context.Background()` unless justified.
- Cancellation tests pass.
- No goroutine/subprocess leak is introduced.

---

## Phase 4 — Subagent Worktree Path Escaping

### Objective

Fix subagent worktree path handling without weakening sandbox security.

### Files to inspect first

- `internal/subagent/`
- `internal/tools/subagent.go`
- `internal/tools/sandbox.go`
- `internal/cli/cli.go`
- tests under `internal/subagent/` and `internal/tools/`

### Tasks

1. Reproduce the documented failure from `ISSUES.md`:
    - Worktree subagent tries to read a repo-root file using a path like `../../go.mod`.
2. Identify current sandbox root and worktree root behavior:
   ```bash
   rg "PI_SANDBOX_ROOT|PI_WORKTREE_ROOT|NewSandbox|worktree|Root" internal/cli internal/tools internal/subagent
   ```
3. Define the desired contract:
    - Tool calls from a worktree agent should use paths relative to that worktree root.
    - Parent-repo paths should not be accessed through `..` escape.
    - If a subagent needs original repo metadata, pass it explicitly as context, not by broadening filesystem access.
4. Fix path construction at the subagent setup/prompt/environment boundary.
5. Add regression tests for:
    - A worktree subagent can read `go.mod` from its own worktree root.
    - A path containing `../..` remains rejected by sandbox.
    - Absolute paths are normalized only if they are inside the allowed sandbox/worktree root.
6. Update `ISSUES.md` status.

### Verification

```bash
go test ./internal/subagent/... -count=1
go test ./internal/tools/... -run 'Sandbox|Worktree|Path|Escape' -count=1
go test ./... -count=1
```

### Acceptance criteria

- Worktree subagents no longer need `../../` paths for normal repo files.
- Sandbox path escape protection remains intact.
- Regression tests cover both allowed and rejected paths.

---

## Phase 5 — CI and Release Workflow Parity

### Objective

Make release validation match CI validation so tagged releases do not diverge from PR/main checks.

### Files

- `.github/workflows/ci.yml`
- `.github/workflows/release.yml`
- optionally `.github/actions/*` if creating a composite action

### Tasks

1. Compare CI test setup against release test setup.
2. Extract duplicated setup if desired:
    - checkout
    - setup-go
    - install `uv`
    - install Rust toolchain fixture support
3. Update release workflow test job to include the same external tool setup as CI.
4. Decide whether release race tests should exclude `internal/acp/server` like CI:
    - If yes, document why in workflow comments.
    - If no, fix the reason CI excludes it.
5. Keep action pins consistent with existing repository style.

### Verification

```bash
git diff -- .github/workflows/ci.yml .github/workflows/release.yml
# Optional if act is available:
# act -j test
```

Also run local tests:

```bash
go test ./... -count=1
```

### Acceptance criteria

- Release workflow has equivalent test prerequisites to CI.
- Any race-test package exclusions are consistent and documented.
- YAML remains valid.

---

## Phase 6 — OTEL Console Exporter Decision

### Objective

Remove misleading OTEL configuration behavior.

### Files

- `internal/otel/otel.go`
- `internal/otel/*_test.go`
- `README.md` or docs if OTEL settings are documented there

### Tasks

Choose one option.

#### Option A — Implement console exporter

1. Add the OpenTelemetry stdout trace exporter dependency if already available or acceptable.
2. For `OTEL_TRACES_EXPORTER=console`, create a real exporter and tracer provider.
3. Ensure output does not corrupt JSON/RPC/ACP streams. Prefer stderr or a documented destination.
4. Add tests for exporter selection.

#### Option B — Remove/deprecate console exporter

1. Remove `console` from documented supported values.
2. If configured, emit a clear warning and use no-op provider.
3. Add tests that confirm `console` does not silently pretend to work.

### Verification

```bash
go test ./internal/otel/... -count=1
go test ./... -count=1
```

### Acceptance criteria

- `OTEL_TRACES_EXPORTER=console` is either functional or clearly unsupported.
- Tests cover the selected behavior.
- Docs and code agree.

---

## Phase 7 — Logging and Output Hygiene

### Objective

Prevent production logs from corrupting TUI, JSONL, RPC, or ACP output streams.

### Files to inspect first

- `internal/logger/`
- `internal/extension/hooks.go`
- `internal/lsp/hooks.go`
- `internal/tools/compactor.go`
- `internal/tui/agent_loop.go`
- JSON/RPC/ACP output code under `internal/cli`, `internal/jsonrpc`, `internal/acp`

### Tasks

1. Find direct logging:
   ```bash
   rg "log\.Printf|log\.Println|fmt\.Println|fmt\.Printf|os\.Stdout|os\.Stderr" internal cmd
   ```
2. Categorize each occurrence:
    - User-facing intentional output.
    - Protocol output.
    - Debug/error logging.
    - Bootstrap/fatal output.
3. Route debug/error logging through the project logger or explicit event channels.
4. Ensure JSONL/RPC/ACP modes do not receive unrelated text on stdout.
5. Add tests for protocol output cleanliness where practical:
    - JSON mode produces parseable JSONL only on stdout.
    - RPC/ACP handshake is not prefixed by logs.

### Verification

```bash
go test ./internal/cli/... -run 'JSON|Output|Log|RPC|ACP' -count=1
go test ./internal/jsonrpc/... -count=1
go test ./internal/acp/... -count=1
go test ./... -count=1
```

### Acceptance criteria

- Non-user-facing logs are routed consistently.
- Protocol stdout remains clean.
- Tests cover at least one protocol-output cleanliness case.

---

## Phase 8 — Memory Architecture Clarification

### Objective

Clarify the relationship between `internal/memory` and `internal/palace`.

### Files

- `README.md`
- `ARCHITECTURE.md`
- package docs if present or new `doc.go` files under:
    - `internal/memory/`
    - `internal/palace/`

### Tasks

1. Inspect both packages and current CLI usage:
   ```bash
   rg "internal/memory|internal/palace|memory\.|palace\." internal cmd README.md ARCHITECTURE.md
   ```
2. Decide and document:
    - Which package is the active user-facing memory implementation?
    - Which package is legacy, lower-level, or transitional?
    - How data migrates or is shared.
3. Update README and architecture docs to use consistent names.
4. Add package comments if missing.

### Verification

```bash
go test ./internal/memory/... ./internal/palace/... -count=1
go test ./... -count=1
```

### Acceptance criteria

- Docs clearly explain memory package ownership and lifecycle.
- Package comments match the architecture docs.

---

## Phase 9 — Planning Document Hygiene

### Objective

Make roadmap/TODO/issues easy to navigate and current.

### Files

- `TODO.md`
- `ISSUES.md`
- `ROADMAP.md`
- this `PLAN.md`
- `OVERVIEW.md`

### Tasks

1. Add links between `OVERVIEW.md`, `PLAN.md`, `ISSUES.md`, and `ROADMAP.md`.
2. Update `TODO.md` to show the current top-priority fixes or point to this plan.
3. Mark completed items in `ISSUES.md` as completed only after tests pass.
4. Add “last validated” dates to operational sections where counts are based on logs.

### Verification

```bash
rg "006-project-overview-critical-improvements|PLAN.md|OVERVIEW.md" TODO.md ISSUES.md ROADMAP.md specs/research/006-project-overview-critical-improvements
```

### Acceptance criteria

- A contributor can start from `TODO.md` or `ROADMAP.md` and find this plan.
- Operational issue statuses include current validation context.

---

## Phase 10 — Low-risk Decomposition Specs

### Objective

Prepare safe future refactors for large orchestration files without doing a risky rewrite now.

### Files

Potential new specs:

- `specs/issues/005-cli-decomposition/PLAN.md`
- `specs/issues/006-tui-decomposition/PLAN.md`
- `specs/issues/007-session-store-decomposition/PLAN.md`

### Tasks

1. Measure largest files:
   ```bash
   find internal cmd -name '*.go' -not -name '*_test.go' -print0 | xargs -0 wc -l | sort -nr | head -30
   ```
2. For each top candidate, identify natural seams:
    - CLI command setup vs runtime creation vs mode dispatch.
    - TUI model/update/view/slash-command/agent-loop separation.
    - Session IO vs metadata vs branching vs compaction.
3. Create separate implementation plans for each decomposition.
4. Include test-before-move requirements in each plan.

### Verification

No production code changes required in this phase. Validate docs exist:

```bash
find specs/issues -maxdepth 2 -name 'PLAN.md' | sort
```

### Acceptance criteria

- Refactor plans exist for the largest files.
- Each plan uses behavior-preserving extraction steps.
- No production refactor is attempted without a dedicated plan.

---

## Final Verification Gate

Run before considering the critical-improvements plan complete:

```bash
go test ./...
go test -race -count=1 $(go list ./... | grep -v '/internal/acp/server$')
make build
make lint
```

If available and configured:

```bash
go test -tags integration ./...
go test -tags e2e ./...
```

## Done Criteria

This plan is complete when:

- P0 items from `OVERVIEW.md` are fixed and tested.
- P1 items either have fixes merged or dedicated follow-up specs with acceptance criteria.
- Docs and code agree on Go version, package names, memory architecture, and OTEL behavior.
- Tool schema validation has regression coverage.
- Subagent path handling has regression coverage.
- CI and release workflows are aligned.
- Full local validation passes or known exceptions are documented with reasons.
