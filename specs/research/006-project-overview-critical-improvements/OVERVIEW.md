# pi-go Project Overview and Critical Improvement Points

## Executive Summary

`pi-go` is a terminal-based coding agent implemented as a single Go module. It combines Google ADK Go, multiple LLM
providers, sandboxed tool execution, session persistence, a Bubble Tea TUI, LSP support, subagents, ACP integration, and
persistent memory systems.

The repository is feature-rich and already has useful architecture documentation, CI, release automation, and a broad
test surface. The highest-value improvements are not broad rewrites; they are targeted reliability, documentation
consistency, cancellation, test stability, and observability fixes around the existing architecture.

## Repository Snapshot

- Module: `github.com/dimetron/pi-go` (`go.mod:1`)
- Declared Go version: `1.26.3` (`go.mod:3`)
- Entry point: `cmd/pi/main.go`
- Main code: `internal/`
- Build/test commands: `Makefile`
- Main architecture docs: `README.md`, `ARCHITECTURE.md`
- Roadmap/tracked issues: `ROADMAP.md`, `ISSUES.md`, `TODO.md`

## Main Capabilities

From `README.md:10-28`, the project provides:

- Multi-provider LLM support: Anthropic, OpenAI, Gemini, Ollama.
- Sandboxed file/shell/git tools rooted in the project directory.
- Interactive Bubble Tea v2 TUI with markdown rendering and slash commands.
- JSONL session persistence with branching, compaction, and resume.
- Model roles for default, smol, slow, plan, commit-style workflows.
- Process-based subagents.
- LSP integration for diagnostics, symbols, references, definitions, and hover.
- Unix socket JSON-RPC/ACP-style integration points.
- Memory Palace / persistent memory features.
- Extension system with hooks, skills, MCP, and skill audit.

## Architecture Overview

### High-level flow

The documented request flow is:

```text
User input -> CLI/TUI -> Agent -> ADK Runner -> LLM provider -> Tool calls -> Session store -> Rendered response
```

References:

- `README.md:50-58`
- `ARCHITECTURE.md:108-137`

### Package map

Key package responsibilities, based on `ARCHITECTURE.md:7-30` and `README.md:30-48`:

| Area                                  | Responsibility                                                            |
|---------------------------------------|---------------------------------------------------------------------------|
| `cmd/pi`                              | Thin binary entry point.                                                  |
| `internal/cli`                        | Cobra commands, runtime wiring, output modes, config/provider/tool setup. |
| `internal/agent`                      | ADK agent construction, runner interaction, retry/session integration.    |
| `internal/provider`                   | Anthropic, OpenAI, Gemini, Ollama/provider resolution and client setup.   |
| `internal/tools`                      | Sandboxed tools, git tools, LSP tools, subagent tool wrapper.             |
| `internal/tui`                        | Bubble Tea v2 interactive UI, slash commands, agent loop rendering.       |
| `internal/session`                    | JSONL session storage, metadata, branching, compaction support.           |
| `internal/subagent`                   | Process/worktree-based subagent orchestration.                            |
| `internal/lsp`                        | JSON-RPC LSP clients, managers, hooks, language support.                  |
| `internal/acp` / `internal/jsonrpc`   | Agent Client Protocol / JSON-RPC integration.                             |
| `internal/memory` / `internal/palace` | Persistent memory and Memory Palace functionality.                        |
| `internal/extension`                  | Hooks, skills, MCP integration.                                           |
| `internal/audit`                      | Skill/security scanning.                                                  |
| `internal/guardrail`                  | Token usage tracking and limits.                                          |
| `internal/otel`                       | OpenTelemetry initialization.                                             |

## Build, Test, and CI Overview

### Local commands

`Makefile` provides:

- `make build`: builds `./cmd/pi` and `./cmd/pi-sandbox` (`Makefile:3-5`).
- `make test`: runs unit tests via `go test ./...` (`Makefile:14-18`).
- `make test-integration`: runs tests with the `integration` tag (`Makefile:19-20`).
- `make test-e2e`: runs tests with the `e2e` tag (`Makefile:22-26`).
- `make test-coverage`: runs internal package coverage (`Makefile:30-31`).
- `make lint`: runs `golangci-lint run ./...` (`Makefile:43-44`).
- `make vet`: runs `go vet ./...` (`Makefile:46-47`).

### CI

`.github/workflows/ci.yml` contains separate lint, test, coverage, and build jobs:

- Lint uses `golangci-lint-action` version `v2.11` (`.github/workflows/ci.yml:21-24`).
- Tests install Go, `uv`, and Rust toolchain fixtures before `go test -count=1 ./...` (
  `.github/workflows/ci.yml:30-52`).
- Race tests exclude `internal/acp/server` (`.github/workflows/ci.yml:51-52`).
- Coverage uploads `coverage.out` to Codecov (`.github/workflows/ci.yml:74-83`).
- Build matrix covers Linux, Windows, and macOS for amd64/arm64 except Windows arm64 (
  `.github/workflows/ci.yml:85-107`).

### Release

`.github/workflows/release.yml` runs lint, `go test -race -count=1 ./...`, then GoReleaser on `v*` tags (
`.github/workflows/release.yml:25-56`).

## Current Strengths

1. **Clear single-module shape**  
   The project keeps non-CLI code under `internal/`, matching Go package boundary conventions.

2. **Broad capability surface**  
   The agent already includes LLM providers, TUI, tools, subagents, session persistence, LSP, ACP, memory, and extension
   systems.

3. **Sandbox-first tool model**  
   File tools are documented as rooted through `os.Root`, preventing normal path escape and symlink escape patterns (
   `ARCHITECTURE.md:198-212`).

4. **CI/release automation exists**  
   The project has lint, tests, race tests, coverage, cross-platform builds, and GoReleaser wiring.

5. **Tool-call robustness work is underway**  
   `internal/tools/registry.go` implements lenient schemas, declaration/runtime schema split, aliasing, and type
   coercion (`internal/tools/registry.go:53-97`, `internal/tools/registry.go:165-230`).

6. **Existing docs are relatively rich**  
   `README.md` and `ARCHITECTURE.md` contain useful package maps and flow diagrams.

## Critical Improvement Points

### P0 — Documentation/version consistency

**Problem**

The repo currently has doc/code mismatches that can confuse contributors and release consumers:

- `README.md` says Go `1.25+` is required (`README.md:92-95`).
- `go.mod` requires Go `1.26.3` (`go.mod:3`).
- `.golangci.yml` also targets Go `1.26` according to repository exploration.
- `ARCHITECTURE.md` references `internal/rpc`, while the tree/code uses `internal/jsonrpc`.
- `TODO.md` says it contains prioritized action items, but is effectively empty (`TODO.md:1-6`).

**Impact**

New contributors may install the wrong Go version, follow stale package names, or miss the real priorities.

**Recommended actions**

1. Update `README.md` requirements to match `go.mod`.
2. Replace `internal/rpc` references with `internal/jsonrpc`, or add a note explaining historical naming if both
   concepts exist.
3. Either populate `TODO.md` from `ISSUES.md`/`ROADMAP.md`, or replace it with a pointer to the active tracking docs.
4. Add a short “source of truth” section listing authoritative docs for architecture, roadmap, and operations.

---

### P0 — Verify and close tool schema validation failures

**Problem**

`ISSUES.md` records high-frequency tool-call validation failures:

- Additional properties: 253 occurrences.
- Missing required properties: 201 occurrences.
- Type errors: 138 occurrences.

References: `ISSUES.md:15-23`, `ISSUES.md:71-98`, `ISSUES.md:121-139`.

The code now includes runtime lenient schemas and coercion in `internal/tools/registry.go`, but the issue document still
marks parts of this as unresolved.

**Impact**

Tool-call validation failures directly reduce agent reliability. They also waste model tokens and user time because
errors happen before the intended tool action can succeed.

**Recommended actions**

1. Re-run the session-log error query from `ISSUES.md:7-13` for the last 7 and 30 days.
2. Compare current error rates against the documented historical rates.
3. Add regression tests for:
    - Extra tool properties.
    - Missing optional fields with defaults.
    - Numeric fields sent as strings.
    - Boolean fields sent as strings.
    - Array/object fields sent as JSON strings.
4. If failures remain before `coercingTool.Run`, investigate ADK validation order and adjust schema/declaration
   registration accordingly.
5. Update `ISSUES.md` with current status, not historical assumptions.

---

### P0 — Fix cancellation/context propagation gaps

**Problem**

Several subprocess or long-running paths use `context.Background()` or fall back to it instead of honoring user
cancellation/session shutdown. Repository exploration found examples in:

- `internal/tools/subagent.go`
- `internal/tools/grep.go`
- `internal/acp/client/session.go`
- `internal/otel/otel.go:61` uses background during provider setup, which is acceptable for initialization but should be
  bounded where network calls happen.

**Impact**

Canceled TUI operations, interrupted CLI runs, or shutdowns may leave subprocesses, ripgrep calls, ACP sessions, or
subagents running longer than expected.

**Recommended actions**

1. Thread request/session context into tool execution paths.
2. Ensure `bash`, `grep`, subagents, LSP calls, and ACP clients use cancelable contexts.
3. Add tests that start a long-running command/subagent and assert cancellation terminates it.
4. Standardize default timeouts per tool class and document them in `ARCHITECTURE.md`.

---

### P1 — Resolve subagent worktree path escaping

**Problem**

`ISSUES.md` documents subagent worktrees producing paths such as `../../go.mod`, which are rejected by the sandbox:

```text
reading file: openat ../../go.mod: path escapes from parent
```

References: `ISSUES.md:101-117`.

**Impact**

This breaks subagent workflows, especially worktree-based research/design/task agents that need to inspect repo-root
files from isolated directories.

**Recommended actions**

1. Reproduce with a minimal subagent worktree scenario.
2. Define the expected path contract:
    - Should subagents receive absolute paths?
    - Should their sandbox root be the worktree root?
    - Should parent repo files be explicitly mounted/allowed?
3. Fix at the subagent setup boundary rather than by weakening sandbox checks.
4. Add regression tests for reading repo-root files from worktree agents without `..` escapes.
5. Update `ISSUES.md` once fixed.

---

### P1 — Stabilize CI/release parity

**Problem**

CI installs `uv` and Rust toolchain fixtures before tests (`.github/workflows/ci.yml:34-52`), but release tests only run
`go test -race -count=1 ./...` without those setup steps (`.github/workflows/release.yml:29-34`).

**Impact**

A tag release can fail differently than PR/main CI, or tests can silently skip/behave differently due to missing
fixtures.

**Recommended actions**

1. Extract common setup into a reusable workflow or composite action.
2. Make release test setup match CI test setup.
3. Consider running the same package exclusion for race tests in release if `internal/acp/server` is excluded in CI for
   a known reason.
4. Document which tests require external tools and why.

---

### P1 — Reduce oversized orchestration files

**Problem**

Some production files are very large and likely mix too many responsibilities. Repository exploration identified
examples:

- `internal/tui/tui.go` around 1,700+ lines.
- `internal/cli/cli.go` around 1,200+ lines.
- `internal/tui/run.go` around 1,100+ lines.
- `internal/session/store.go` around 800+ lines.
- `internal/auth/auth.go` around 800+ lines.
- `internal/provider/anthropic.go` around 800+ lines.

**Impact**

Large coordination files make targeted changes riskier, increase review time, and make tests harder to localize.

**Recommended actions**

Decompose vertically without changing behavior:

1. `internal/cli`: split command definitions, runtime building, provider setup, mode dispatch, memory setup.
2. `internal/tui`: split model state, update handlers, view rendering, slash commands, agent loop.
3. `internal/session`: split store IO, metadata, branching, compaction, migration/validation.
4. `internal/provider`: split request conversion, streaming, tool-call conversion, error mapping.
5. Add package-level tests around extracted units before moving complex code.

---

### P1 — Complete OTEL console exporter or remove the option

**Problem**

`internal/otel/otel.go` documents `OTEL_TRACES_EXPORTER=console` (`internal/otel/otel.go:7-11`) but the implementation
currently treats `console` as a TODO/no-op (`internal/otel/otel.go:76-81`).

**Impact**

Users enabling console tracing get no traces and no clear signal that the exporter is unimplemented.

**Recommended actions**

1. Implement the stdout/stderr console trace exporter if it is useful for debugging.
2. If console output would interfere with TUI/ACP streams, remove it from docs and emit a clear warning when configured.
3. Add tests around exporter selection behavior.

---

### P2 — Normalize logging and diagnostics

**Problem**

Repository exploration found direct `log.Printf` usage in production paths such as hooks, LSP hooks, compactor, and TUI
panic recovery. This can bypass structured/session logging and can pollute user-facing streams.

**Impact**

Debugging becomes inconsistent, and terminal/JSON/ACP output modes may receive unexpected logs.

**Recommended actions**

1. Define logging rules per output mode:
    - interactive TUI
    - print
    - JSONL
    - RPC/ACP
2. Route production logs through the project logger package or explicit event streams.
3. Reserve direct stderr writes only for carefully documented fatal/bootstrap cases.
4. Add tests for JSON/ACP modes to ensure logs do not corrupt protocol output.

---

### P2 — Clarify memory architecture

**Problem**

Both `internal/memory` and `internal/palace` exist, while docs emphasize “Memory Palace”. CLI/runtime exploration
indicates legacy/current memory paths may both be present.

**Impact**

Contributors may not know which memory system is authoritative, and duplicated concepts can cause drift.

**Recommended actions**

1. Add a memory architecture note explaining the relationship between `internal/memory` and `internal/palace`.
2. Mark one system as active/current and one as legacy/transitional if applicable.
3. Align README, ARCHITECTURE, CLI help, and package docs.
4. Add migration/deprecation notes if one package is being phased out.

---

### P2 — Improve roadmap and issue hygiene

**Problem**

`ROADMAP.md` contains useful future items, `ISSUES.md` contains operational findings, but `TODO.md` is empty and there
is no single prioritized action list.

**Impact**

The project has several sources of planning truth, which makes it harder to decide what to work on next.

**Recommended actions**

1. Convert this document’s P0/P1/P2 list into tracked issues/specs.
2. Keep `TODO.md` as a generated/current top-10 list or remove it.
3. Link each roadmap item to a spec or issue once it becomes active.
4. Record status and last validation date for operational issues like tool-call errors.

## Suggested 30-Day Plan

### Week 1: Consistency and validation

- Update Go version documentation.
- Fix `internal/rpc` vs `internal/jsonrpc` docs.
- Refresh tool-call error statistics from session logs.
- Update `ISSUES.md` with current observed rates.

### Week 2: Reliability hardening

- Add cancellation tests for subprocess/subagent flows.
- Thread contexts into the most important long-running paths.
- Reproduce and fix subagent worktree path escaping.

### Week 3: CI/release and observability

- Make release test setup match CI.
- Decide and implement/remove OTEL console exporter.
- Add protocol-output pollution tests for JSON/ACP modes.

### Week 4: Decomposition planning

- Create small specs for splitting `internal/cli/cli.go` and `internal/tui/tui.go`.
- Extract one low-risk vertical slice from each large file.
- Add tests around extracted units before further refactoring.

## Quick Reference: Highest-Impact Fixes

| Priority | Improvement                                         | Why it matters                                                          |
|----------|-----------------------------------------------------|-------------------------------------------------------------------------|
| P0       | Fix doc/version/package-name mismatches             | Prevents contributor setup errors and stale architecture understanding. |
| P0       | Verify/close tool schema validation issues          | Directly improves agent tool reliability.                               |
| P0       | Propagate cancellation to subprocess/subagent paths | Prevents leaked or runaway work.                                        |
| P1       | Fix subagent worktree path escaping                 | Restores reliability of multi-agent workflows.                          |
| P1       | Align CI and release setup                          | Prevents release-only failures.                                         |
| P1       | Decompose largest orchestration files               | Reduces future change risk.                                             |
| P1       | Complete/remove OTEL console exporter               | Avoids misleading observability configuration.                          |
| P2       | Normalize logging                                   | Keeps protocol/TUI output clean.                                        |
| P2       | Clarify memory package ownership                    | Reduces architectural drift.                                            |
| P2       | Consolidate roadmap/TODO/issue tracking             | Makes prioritization easier.                                            |
