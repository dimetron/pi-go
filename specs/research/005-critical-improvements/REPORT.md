# pi-go — Critical Improvements Report

**Generated:** 2025-06-13  
**Scope:** Full repo analysis (lint, tests, coverage, code quality, architecture)

---

## Executive Summary

| Metric           | Value                          | Assessment                    |
|------------------|--------------------------------|-------------------------------|
| Total Go lines   | ~45,519                        | Large codebase                |
| Packages         | 33                             | Moderate complexity           |
| Direct deps      | ~134 (transitive: 319)         | Healthy — no heavy frameworks |
| Linter issues    | **0**                          | ✅ Clean                       |
| Test failures    | **0**                          | ✅ All pass                    |
| Build            | ✅ Passes (`go build ./cmd/pi`) | ✅                             |
| Overall coverage | ~84%                           | Good, but with notable gaps   |

The codebase is in good health — linters are clean and all tests pass. However, several structural and quality concerns
warrant attention before the next major release.

---

## 1. 🚨 Critical Issues (Fix First)

### 1.1 `context.Background()` misuse in production code

**Severity: High**  
**Files:** `internal/tools/subagent.go:20`, `internal/tools/grep.go:273`

```go
// internal/tools/subagent.go:20 — no cancellation possible
return context.Background()

// internal/tools/grep.go:273 — long-running subprocess with no timeout
ctx := context.Background()
```

**Impact:** Long-running operations (grep, subagent spawns) cannot be cancelled. This causes hangs when the user cancels
a command or the TUI closes.

**Fix:** Pass `tool.Context` through the call chain, or use a package-level cancellable context with proper shutdown
hooks.

---

### 1.2 Zero-coverage packages

**Severity: Medium-High**  
**Packages:** `cmd/pi-sandbox`, `hack/test/mcp`

| Package                  | Coverage  | Notes                                                                                  |
|--------------------------|-----------|----------------------------------------------------------------------------------------|
| `cmd/pi-sandbox/main.go` | **0%**    | Entry point — expected, but should have integration tests                              |
| `hack/test/mcp/main.go`  | **32.5%** | Test harness tools with low coverage (tools like `rootsTool`, `elicitFormTool` at 28%) |

**Fix:** Add integration tests for pi-sandbox binary. Increase test coverage on MCP mock tool implementations.

---

### 1.3 Unhandled error silencing

**Severity: Medium**  
**Files:** Multiple locations with `//nolint:errcheck`, `_ = ...Close()`, `_, _ = fmt.Fprint(...)`

```go
// internal/auth/auth.go:593 — HTML response error ignored
_, _ = fmt.Fprint(w, `<!DOCTYPE html>...`)

// internal/tools/sandbox.go:126 — sandbox cleanup error swallowed
_ = r.Close()
```

**Impact:** Resource leaks and silent failures in auth flow and sandbox lifecycle.

---

## 2. 📐 Architecture Issues

### 2.1 Massive files exceeding maintainability thresholds

**Severity: Medium**  
Go convention recommends max ~400 lines per file. Several exceed this significantly:

| File                                | Lines     | Risk                                                              |
|-------------------------------------|-----------|-------------------------------------------------------------------|
| `internal/tui/tui.go`               | **1,781** | Core TUI — too many responsibilities                              |
| `internal/cli/ping.go`              | **812**   | Single command file — over-engineered                             |
| `internal/cli/commands.go`          | **802**   | Command routing — should be split by domain                       |
| `internal/tui/run.go`               | **1,195** | Run flow — complex state machine                                  |
| `internal/cli/cli.go`               | **1,223** | CLI root setup — too many subcommands wired here                  |
| `internal/palace/sqlite_store.go`   | **729**   | SQLite persistence — should split by entity/table                 |
| `internal/tui/tool_display.go`      | **710**   | Tool display logic — separate rendering from data model           |
| `internal/provider/anthropic.go`    | **814**   | Anthropic provider — merge with other providers or extract common |
| `internal/auth/auth.go`             | **891**   | Auth flow + HTML server — split into auth and web handlers        |
| `internal/session/store.go`         | **891**   | Session persistence — branch logic mixed in                       |
| `internal/tui/agent_loop.go`        | **677**   | Agent loop — separate event handling                              |
| `internal/subagent/orchestrator.go` | **644**   | Orchestrator — parallel/sync paths merged                         |

**Recommendation:** Target max 500 lines per file. Split by extracting:

- Types/interfaces → separate `_types.go` or `types/` subpackage
- Handlers/callbacks → separate files
- UI rendering → separate from state management

---

### 2.2 `internal/palace` — monolithic knowledge graph package

**Severity: Medium**  
4,018 lines of production code in a single package with 26+ source files. The package handles:

- SQLite storage (`sqlite_store.go`: 729 lines)
- Knowledge graph operations (`kg.go`, `graph.go`)
- Embedding/ML integration (`embedder.go`, `embedding.go`)
- Tool definitions (8 tool files)
- Miner logic (`miner.go`, `miner_project.go`, `miner_convo.go`)

**Recommendation:** Split into subpackages:

```
internal/palace/
├── storage/     # sqlite_store, db, store interfaces
├── graph/       # kg, graph algorithms
├── embedder/    # embedding models
├── miner/       # mining logic
└── tools/       # tool definitions (already partially done)
```

---

### 2.3 `internal/tui` — too many responsibilities

**Severity: Medium**  
The TUI package has ~100+ Go files and handles:

- Agent event processing (`agent_loop.go`, `agent_event_test.go`)
- Chat rendering (`chat.go`, `tool_display.go`)
- Input handling (`input.go`, `commands.go`)
- Sidebar/history/refs
- Login flow, completion, themes
- Coverage boost testing

**Recommendation:** Split into:

```
internal/tui/
├── core/        # tui.go, types.go, layout.go, theme.go
├── chat/        # chat rendering, tool display
├── input/       # input.go, commands.go, completion.go
├── agent/       # agent_loop.go, agent_event processing
├── sidebar/     # sidebar, history, refs
└── login/       # login flow
```

---

## 3. 🧪 Test Quality Issues

### 3.1 Hardcoded sleep timers in production code

**Severity: Medium**  
`time.Sleep` in non-test code indicates race conditions or polling patterns that should use channels/signals:

| File                        | Line | Sleep Duration     | Issue                                    |
|-----------------------------|------|--------------------|------------------------------------------|
| `internal/tools/edit.go`    | 83   | retry delay        | Should use channel-based backoff         |
| `internal/tools/sandbox.go` | 268  | read retry         | Same — polling loop without cancellation |
| `internal/lsp/hooks.go`     | 134  | `DiagnosticsDelay` | Hardcoded delay instead of event-driven  |
| `internal/tui/tui.go`       | 1345 | 2s sleep           | TUI update polling                       |

**Fix:** Replace with channel-based synchronization or context-aware waits.

---

### 3.2 Long-running tests blocking CI

**Severity: Low-Medium**  
Some packages take >60 seconds per test run:

| Package             | Duration | Concern                                      |
|---------------------|----------|----------------------------------------------|
| `internal/cli`      | **117s** | E2E/integration tests running in unit suite  |
| `internal/subagent` | **107s** | Process spawning tests are slow but expected |
| `internal/provider` | **14s**  | LLM provider init is slow                    |
| `internal/auth`     | **18s**  | Auth flow tests                              |

**Recommendation:** Move E2E/integration tests to separate `_e2e_test.go` files with build tag `e2e`. Run them only in
CI, not locally by default.

---

### 3.3 Test-only `context.Background()` usage

**Severity: Low**  
~15+ instances of `context.Background()` in test code where `context.WithTimeout(t.Context(), ...)` should be used to
prevent hangs on failure.

---

## 4. 🔧 Code Quality Issues

### 4.1 Excessive use of `any` type

**Severity: Low-Medium**  
982 uses of the `any` type across the codebase. While Go generics are still maturing, excessive `any` usage leads to
runtime panics and poor IDE support.

**Hotspots:**

- `internal/tools/compactor.go` — generic compaction pipeline with `map[string]any`
- `internal/tui/agent_loop.go:82` — `toolFingerprint(name string, args map[string]any)`
- `internal/palace/types.go` — many `any` fields in graph nodes

**Recommendation:** Define concrete types for known structures. Use generics where the type is predictable at compile
time.

---

### 4.2 `log.Printf` in production code

**Severity: Low**  
Production code uses `log.Printf` instead of the project's structured logger (`internal/logger`). This bypasses log
levels, formatting, and file output configured by the user.

| File                          | Lines    |
|-------------------------------|----------|
| `internal/tools/compactor.go` | 153, 161 |
| `internal/extension/hooks.go` | 117, 140 |

**Fix:** Replace with `logger.Ctx(ctx).Info("message", "key", value)` pattern.

---

### 4.3 No `nolint` hygiene in production code

**Severity: Low**  
All `//nolint:errcheck` instances are in test files (acceptable), but production code has no lint directives — which is
good. However, the `interface{}` usage in `internal/webserver/server.go` should be updated to `any`.

---

## 5. 📦 Dependency Health

### 5.1 Bubble Tea v2 dependency chain

**Severity: Low**  
The project uses `charmbracelet/bubbletea/v2@v2.0.6` which pulls in ~30 transitive dependencies. This is the primary UI
framework and is well-maintained, but worth monitoring for breaking changes.

### 5.2 No version pinning on dev/test deps

**Severity: Low**  
Some `hack/` test tools use unpinned imports that could break with upstream updates.

---

## 6. 📋 Improvement Priority Matrix

| Priority | Issue                                       | Effort | Impact                  |
|----------|---------------------------------------------|--------|-------------------------|
| **P0**   | Fix `context.Background()` in subagent/grep | Low    | High — prevents hangs   |
| **P0**   | Handle sandbox/auth errors properly         | Medium | Medium — resource leaks |
| **P1**   | Split `tui/tui.go` (1,781 lines)            | High   | High — maintainability  |
| **P1**   | Split `palace/sqlite_store.go` (729 lines)  | Medium | Medium — testability    |
| **P1**   | Move E2E tests to build-tagged files        | Low    | Medium — CI speed       |
| **P2**   | Replace `log.Printf` with structured logger | Low    | Low                     |
| **P2**   | Add coverage for `cmd/pi-sandbox`           | Medium | Medium                  |
| **P2**   | Replace `time.Sleep` with channels          | Medium | Medium                  |
| **P3**   | Define concrete types instead of `any`      | High   | Low-Medium              |
| **P3**   | Split `palace/` into subpackages            | High   | Medium                  |

---

## 7. 📊 Coverage Breakdown by Package

| Coverage Range | Packages | Names                                                                                              |
|----------------|----------|----------------------------------------------------------------------------------------------------|
| **95%+**       | 4        | audit, guardrail, logger, acp/server/adapter                                                       |
| **85-95%**     | 10       | acp, agent, atif, auth, config, extension, lsp, memory, provider, session, sop, subagent, tui/refs |
| **75-85%**     | 6        | cli, jsonrpc, otel, palace, tools, webserver, tui                                                  |
| **< 75%**      | 4        | cmd/pi (68%), acp/client (75.2%), hack/test/mcp (32.5%)                                            |
| **0%**         | 1        | cmd/pi-sandbox                                                                                     |

---

## 8. 🎯 Recommended Action Plan

### Sprint 1 — Quick wins (1-2 days)

1. Replace `context.Background()` in `subagent.go` and `grep.go` with proper context propagation
2. Fix error handling in `sandbox.go` and `auth/auth.go`
3. Move E2E tests to build-tagged files

### Sprint 2 — Structural (1 week)

4. Split `tui/tui.go` into core/chat/input subpackages
5. Split `palace/sqlite_store.go` by entity
6. Replace `log.Printf` calls with structured logger

### Sprint 3 — Hardening (ongoing)

7. Add coverage for pi-sandbox and low-coverage hack tools
8. Define concrete types to reduce `any` usage
9. Split palace into subpackages
10. Review and optimize long test durations

---

*Report generated by automated analysis of the pi-go codebase.*
