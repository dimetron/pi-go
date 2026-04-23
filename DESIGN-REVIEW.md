# Design Review Report: pi-go

> Last validated: 2026-04-23
> Scope: current repository state plus quick verification of build/test health

## Executive Summary

The codebase remains structurally strong: package boundaries are clear, the single-module layout is intact, and core architectural choices still align with the project’s ADK-first design. The biggest change since the prior review is that the most important issue is now **correctness**, not file organization: the repository does **not** currently pass `go test ./...` because of a nil-pointer panic in the Cursor ACP client finish path.

The previous headline concern about `internal/tui/tui.go` being a 2600+ line file is now outdated. That file is no longer the main organization hotspot. The largest production files are currently `internal/tui/run.go` and `internal/cli/cli.go`.

## Verification Snapshot

### Commands run

```bash
git status --short
go test ./...
go tool golangci-lint run ./...
find internal cmd -name '*.go' -print0 | xargs -0 wc -l | sort -nr | head -20
```

### Results

| Gate | Result | Notes |
|------|--------|-------|
| Build / test baseline | FAIL | `go test ./...` panics in `internal/acp/client/cursor` |
| Lint | BLOCKED | `go tool golangci-lint` unavailable in local environment |
| Repo cleanliness | DIRTY | Existing unstaged changes in `README.md`, `internal/acp/client/cursor/cursor_run_test.go`, `internal/acp/client/cursor/cursor_test.go`, `internal/cli/acp_server.go` |
| Architecture conformance | PASS | Layout still matches project rules |

## Scorecard

| Dimension | Score | Notes |
|-----------|-------|-------|
| Architecture fit | 8/10 | Still aligned with single-module, internal-packages-only design |
| Package design | 8/10 | Boundaries are mostly coherent and purposeful |
| Interface design | 8/10 | Small interfaces and ADK-native integration remain a strength |
| Error handling | 6/10 | Good wrapping patterns overall, but a production nil panic drops confidence |
| Concurrency / lifecycle | 6/10 | Mutex use exists, but session-finalization edge cases need tightening |
| API consistency | 8/10 | Constructors and package usage are mostly consistent |
| Code organization | 7/10 | Better than prior review, but large files remain |
| Testability | 7/10 | Broad test coverage exists, but one failing path indicates insufficient nil-state hardening |
| Documentation | 8/10 | Repo docs and package intent remain solid |
| **Overall** | **7.1/10** | Strong design with a currently failing critical edge path |

---

## Key Strengths

1. **Architecture remains coherent**  
   The repository still follows the intended single-binary, single-module design with business logic under `internal/` and a thin CLI entrypoint in `cmd/pi`.

2. **ADK-native integration is still the right call**  
   The codebase continues to use native ADK concepts instead of inventing local abstractions, which keeps provider/tool/session wiring understandable and future-compatible.

3. **Package boundaries are understandable**  
   Packages such as `internal/agent`, `internal/provider`, `internal/session`, `internal/subagent`, `internal/tools`, and `internal/tui` each have a clear primary responsibility.

4. **Error wrapping style is broadly good**  
   Most code paths still follow the project rule of contextual wrapping via `fmt.Errorf("...: %w", err)`.

5. **Test surface area is substantial**  
   The repository has a large amount of package-level test coverage, which is a positive signal even though one current path is broken.

---

## Current High-Priority Findings

### 1. Nil-pointer panic in Cursor session finish path

- **Severity**: high
- **Impact**: correctness / test stability / likely runtime robustness
- **Where**:
  - `internal/acp/client/cursor/cursor.go:393-410`
  - Specifically `internal/acp/client/cursor/cursor.go:406`
- **What happens**: `RunningSession.finish` unconditionally calls `s.stderr.String()` when `result.Error` is empty.
- **Observed failure**: `go test ./...` fails in `TestRunningSessionFinishDeduplication` with:
  - panic from `github.com/dimetron/pi-go/internal/acp/client/cursor.(*stderrBuffer).String`
  - dereference at `internal/acp/client/cursor/cursor.go:537`
- **Why this matters**: the finish path assumes `s.stderr` is always initialized, but tests demonstrate that assumption is false. That makes session finalization fragile and suggests lifecycle invariants are not fully encoded in the type.
- **Design assessment**: this is the most important issue in the repo right now. The fix should make `RunningSession` safe when stderr capture is absent or intentionally omitted.
- **Recommendation**:
  - Guard `s.stderr` before calling `.String()`.
  - Prefer making the zero value or partially initialized test value of `RunningSession` safer.
  - Consider encapsulating stderr access behind a helper that returns `""` on nil.

### 2. Finalization invariants are implicit instead of enforced

- **Severity**: medium-high
- **Impact**: maintainability / correctness under edge cases
- **Where**:
  - `internal/acp/client/cursor/cursor.go:393-428`
- **What**: `finish` and `waitProcess` rely on fields like `stderr`, `stderrDone`, and `cmd` being in a valid lifecycle state, but the contract is implicit.
- **Why this matters**: when object validity depends on “constructor X must always have been called first,” tests and future refactors tend to discover gaps late.
- **Design assessment**: the package would benefit from stronger invariants around `RunningSession` construction and teardown.
- **Recommendation**:
  - Centralize `RunningSession` construction so required fields are always initialized.
  - Add nil-safe helpers for optional resources.
  - Treat “finalization with partial state” as a supported scenario and test it directly.

### 3. Large production files remain a maintainability tax

- **Severity**: medium
- **Impact**: readability / change risk / onboarding speed
- **Where**:
  - `internal/tui/run.go` — 1195 lines
  - `internal/cli/cli.go` — 1137 lines
- **What**: the main size hotspot has moved. The old review’s `internal/tui/tui.go` note is stale; today the largest production files are in run orchestration and CLI wiring.
- **Why this matters**: these files likely combine multiple concerns: command wiring, state transitions, rendering, retries, gate logic, and orchestration.
- **Design assessment**: not an emergency, but these files are now the strongest candidates for future decomposition.
- **Recommendation**:
  - For `internal/tui/run.go`, consider separating message types, gate execution, parallel-agent orchestration, and summary rendering.
  - For `internal/cli/cli.go`, consider splitting root command setup, mode-specific execution, and shared dependency wiring.

---

## Lower-Priority Findings

### 4. Lint verification is environment-sensitive right now

- **Severity**: low
- **Impact**: review confidence
- **Where**: local verification environment
- **What**: `go tool golangci-lint run ./...` failed with `go: no such tool "golangci-lint"`.
- **Assessment**: this is not necessarily a repository design flaw, but it reduces local review confidence because lint gates could not be checked.
- **Recommendation**: ensure the documented lint path matches actual contributor setup, whether via `go tool`, a checked-in tool directive, Makefile, or direct binary installation.

### 5. Best-effort persistence remains acceptable, but should stay explicit

- **Severity**: low
- **Impact**: observability / operator understanding
- **Where**:
  - `internal/session/store.go:383`
- **What**: `saveBranches` is still intentionally ignored as best-effort:
  - `_ = saveBranches(sessionDir, bs) // best-effort`
- **Assessment**: this is acceptable because it is documented inline and appears to concern non-critical metadata persistence rather than core session durability.
- **Recommendation**: keep this pattern explicit and limited to truly non-critical metadata paths.

---

## Outdated Findings from Prior Review

These prior review conclusions should no longer be treated as current:

1. **`internal/tui/tui.go` as the main oversized file**  
   This is no longer the primary organization issue. Current large-file hotspots are `internal/tui/run.go` and `internal/cli/cli.go`.

2. **Cleanup-path unchecked errors as the main error-handling concern**  
   Those still exist in various places and are often acceptable as best-effort cleanup. They are no longer the most important issue. The concrete nil dereference in the Cursor client is far more significant.

---

## Recommended Next Actions

### Immediate

1. Fix the Cursor nil dereference in `RunningSession.finish`.
2. Re-run `go test ./...` until the baseline is green.
3. Re-run lint once `golangci-lint` is available in the local environment.

### Near-term

1. Strengthen `RunningSession` construction and teardown invariants.
2. Add tests covering partially initialized or nil-optional session state.
3. Document the intended lifecycle guarantees for ACP session objects.

### Later

1. Split `internal/tui/run.go` into smaller focused files.
2. Split `internal/cli/cli.go` into command wiring vs runtime wiring concerns.
3. Periodically refresh this review so stale findings do not persist across refactors.

---

## Summary

pi-go still has a solid architectural base and generally good Go package design. The main issue is no longer broad structure; it is a **specific correctness hole** in the Cursor ACP client that currently breaks the repository test baseline. Once that nil-pointer issue is fixed, the next most valuable design work is incremental decomposition of the now-large `internal/tui/run.go` and `internal/cli/cli.go` files.
