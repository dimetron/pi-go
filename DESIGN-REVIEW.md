# Design Review Report: pi-go

> Last validated: 2026-03-20

## Scorecard

| Dimension           | Score | Notes                                              |
|---------------------|-------|----------------------------------------------------|
| Naming Conventions  | 8/10  | Consistent Go idioms, fmt.Sprintf issues fixed     |
| Package Design      | 8/10  | Well-organized, single responsibility mostly met   |
| Interface Design    | 8/10  | Small interfaces, good ADK compliance              |
| Error Handling      | 6/10  | Unchecked errors in defer cleanup paths            |
| Concurrency         | 7/10  | Proper patterns, some lifecycle concerns           |
| API Consistency     | 7/10  | Good NewFoo pattern, consistent JSON snake_case    |
| Code Organization   | 6/10  | Large TUI file (2625 lines)                        |
| Documentation       | 8/10  | Good package/function docs                         |
| **Overall**         | **7.3/10** | Weighted average                              |

---

## Key Strengths

1. **ADK Compliance** — Proper use of native ADK interfaces (`model.LLM`, `tool.Tool`, `session.Service`) without custom abstractions (`internal/agent/agent.go`, `internal/provider/provider.go`)

2. **Provider Pattern** — Consistent multi-provider architecture with clean factory pattern supporting Anthropic, OpenAI, Gemini, and Ollama (`internal/provider/provider.go`)

3. **Session Service Design** — Well-implemented JSONL append-only persistence with proper `session.Service` interface compliance (`internal/session/store.go`)

4. **Error Wrapping Consistency** — Consistent use of `fmt.Errorf("...: %w", err)` throughout for contextual error propagation

---

## Remaining Issues

### 1. Unchecked Errors in Cleanup Paths (impact: low, effort: medium)

- **What**: Unchecked error returns from `Close()`, `Remove()` in defer/cleanup paths
- **Where**:
  - `internal/rpc/rpc.go:98-99` — `defer s.listener.Close()`, `defer os.Remove(s.socketPath)`
  - `internal/rpc/rpc.go:109` — `s.listener.Close()` in shutdown goroutine
  - `internal/rpc/rpc.go:136` — `defer conn.Close()`
  - `internal/session/store.go:505` — `defer f.Close()` after write
  - `internal/session/store.go:692-693, 699` — `f.Close()`, `os.Remove()` in error paths
  - `internal/provider/provider.go:151` — `defer resp.Body.Close()`
- **Assessment**: All are best-effort cleanup in defer paths. This is standard Go for cleanup operations where failure is non-actionable. Not a functional issue, but adding `//nolint:errcheck` comments would document intent.

### 2. Split `internal/tui/tui.go` (impact: high, effort: high)

- **What**: Single file with 2625 lines
- **Where**: `internal/tui/tui.go`
- **Why**: Difficult to navigate; exceeds reasonable file size
- **Suggested split**:
  - `message.go` — message types and `handleMessage`
  - `status.go` — `renderStatus`, status bar logic
  - `screen.go` — screen rendering helpers

### 3. `saveBranches` Error Ignored (impact: low, effort: low)

- **What**: Silent failure in branch persistence
- **Where**: `internal/session/store.go:284`
- **Assessment**: Already marked `// best-effort` in code. Branch head pointer is non-critical metadata. Acceptable as-is.

### 4. Large Files Near Guideline Limits

| File | Lines | Status |
|------|-------|--------|
| `internal/tui/tui.go` | 2625 | Needs split |
| `internal/cli/cli.go` | 814 | Monitor |
| `internal/tui/run.go` | 776 | Monitor |
| `internal/tools/compactor_bash.go` | 484 | OK |

---

## Fixed Issues (2026-03-20)

| Issue | File | Fix |
|-------|------|-----|
| `WriteString(fmt.Sprintf(...))` | `internal/audit/report.go` | Replaced with `fmt.Fprintf(&b, ...)` |
| Redundant if-return pattern | `internal/tools/sandbox.go` | Simplified to `return errors.Is(...)` |
| `fmt.Sprintf` in tui | `internal/tui/tui.go` | Fixed in prior session |

## Invalidated Findings

| Finding | Reason |
|---------|--------|
| JSON tag casing inconsistency (`subagent/types.go`) | snake_case is consistent throughout and is standard for JSON APIs |

---

## Summary

The pi-go codebase demonstrates solid Go engineering with good architectural decisions around ADK compliance, provider patterns, and session management. The primary remaining concern is **code organization** — the 2625-line TUI file needs splitting. Error handling in cleanup paths follows standard Go patterns and is acceptable as-is.
