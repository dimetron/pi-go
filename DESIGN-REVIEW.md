# Design Review Report: pi-go

## Scorecard

| Dimension           | Score | Notes                                              |
|---------------------|-------|----------------------------------------------------|
| Naming Conventions  | 7/10  | Minor issues, mostly consistent with Go idioms     |
| Package Design      | 8/10  | Well-organized, single responsibility mostly met   |
| Interface Design    | 8/10  | Small interfaces, good ADK compliance              |
| Error Handling      | 6/10  | 50+ unchecked errors, especially in session/rpc   |
| Concurrency         | 7/10  | Proper patterns, some lifecycle concerns           |
| API Consistency     | 7/10  | Good NewFoo pattern, some inconsistencies          |
| Code Organization   | 6/10  | Large TUI file (2625 lines), single-file concern  |
| Documentation       | 8/10  | Good package/function docs, some style issues     |
| **Overall**         | **7.1/10** | Weighted average                            |

---

## Key Strengths

1. **ADK Compliance** — Proper use of native ADK interfaces (`model.LLM`, `tool.Tool`, `session.Service`) without custom abstractions, enabling clean integration with Google ADK Go (`internal/agent/agent.go:161`, `internal/provider/provider.go:166`)

2. **Provider Pattern** — Consistent multi-provider architecture with clean factory pattern in `internal/provider/provider.go:82-182`, supporting Anthropic, OpenAI, Gemini, and Ollama with uniform initialization

3. **Session Service Design** — Well-implemented JSONL append-only persistence with proper `session.Service` interface compliance (`internal/session/store.go:712`)

4. **Error Wrapping Consistency** — Consistent use of `fmt.Errorf("...: %w", err)` pattern throughout the codebase for contextual error propagation

---

## Top 5 Actionable Improvements

### 1. Fix Unchecked Errors in Production Code (impact: high, effort: medium)

- **Confidence**: high
- **What**: 50+ unchecked error returns from `Close()`, `Remove()`, `Write()` calls
- **Where**:
  - `internal/rpc/rpc.go:98-99, 109, 136`
  - `internal/session/store.go:284, 505, 692-693, 699`
  - `internal/provider/provider.go:151`
- **Why**: Resource leaks possible, silent failures, violates Go error handling idioms
- **How**:

```go
// before
defer s.listener.Close()
defer os.Remove(s.socketPath)

// after
defer func() {
    s.listener.Close() //nolint:errcheck // best-effort cleanup
}()
defer os.Remove(s.socketPath) //nolint:errcheck // best-effort cleanup
```

### 2. Split `internal/tui/tui.go` (impact: high, effort: high)

- **Confidence**: high
- **What**: Single file with 2625 lines violates single-type-per-file convention
- **Where**: `internal/tui/tui.go`
- **Why**: Unmaintainable, difficult to navigate, exceeds reasonable file size
- **How**:

```go
// Move to internal/tui/message.go
type message struct { ... }
func (m *model) handleMessage(msg tea.Msg) tea.Cmd { ... }

// Move to internal/tui/status.go
func (m *model) renderStatus() string { ... }
```

### 3. Fix JSON Tag Casing (impact: low, effort: low)

- **Confidence**: high
- **What**: Inconsistent JSON tag naming (snake_case vs Go-style)
- **Where**: `internal/subagent/types.go:10, 64-69, 73-79`
- **Why**: Mixed conventions, harder to predict API format
- **How**:

```go
// before
type SpawnInput struct {
    Agent       AgentConfig `json:"agent"`
    WorkDir     string      `json:"work_dir,omitempty"`
}

// after
type SpawnInput struct {
    Agent   AgentConfig `json:"agent"`           // Keep agent for API compat
    WorkDir string      `json:"workDir,omitempty"` // Use Go-style
}
```

### 4. Add Error Return to `saveBranches` (impact: medium, effort: low)

- **Confidence**: high
- **What**: Silent failure in branch persistence
- **Where**: `internal/session/store.go:284`
- **Why**: Branch state may not be persisted, silent data loss risk
- **How**:

```go
// before
saveBranches(sessionDir, bs) // best-effort

// after
if err := saveBranches(sessionDir, bs); err != nil {
    return fmt.Errorf("saving branches: %w", err)
}
```

### 5. Remove Unnecessary `fmt.Sprintf` in String Operations (impact: low, effort: low)

- **Confidence**: high
- **What**: Staticcheck flagged unnecessary `fmt.Sprintf` calls
- **Where**:
  - `internal/tui/tui.go:1345, 1368`
  - `internal/audit/report.go:29-30, 33, 103`
- **Why**: Minor performance overhead, style inconsistency
- **How**:

```go
// before
b.WriteString(fmt.Sprintf("\n*Daily token usage*\n"))
b.WriteString(fmt.Sprintf("\n*Subagents*\n"))

// after
b.WriteString("\n*Daily token usage*\n")
b.WriteString("\n*Subagents*\n")
```

---

## Detailed Findings

### Naming Conventions (7/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/subagent/types.go:10` | `json:"agent"` tag, prefer consistency with other tags | Use `json:"agent,omitempty"` or `json:"agentID"` |
| 2 | `internal/tui/tui.go:1345` | `fmt.Sprintf("\n*Daily...")` is unnecessary | Use `b.WriteString("\n*Daily...\n")` |
| 3 | `internal/tui/tui.go:1368` | Same pattern | Use `b.WriteString("\n*Subagents*\n")` |
| 4 | `internal/audit/report.go:29-30, 33` | Use `fmt.Fprintf` instead of `b.WriteString(fmt.Sprintf(...))` | `fmt.Fprintf(&b, "...")` |

**What's working well**: Consistent use of MixedCaps for exported names, proper `ID` acronym usage, meaningful receiver names (`s` for services, `m` for models).

---

### Package Design (8/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/tui/tui.go:2625 lines` | God file — too many responsibilities | Split into message.go, status.go, screen.go |
| 2 | `internal/tools/compactor_bash.go:484` | Large file, consider splitting | Extract specific compactors into separate files |

**What's working well**: Clear package boundaries, proper use of `internal/`, single module structure, meaningful package names.

---

### Interface Design (8/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/tui/tui.go:38` | Tagging interface `agentMsg interface{ agentMsg() }` | Consider removing or documenting purpose |

**What's working well**: Small interfaces (1-3 methods), proper ADK interface compliance, `memory.Store` and `memory.Compressor` are well-designed.

---

### Error Handling (6/10)

**Issues** (production code only):

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/rpc/rpc.go:98-99` | Unchecked `listener.Close()`, `os.Remove()` | Add `//nolint:errcheck` with comment |
| 2 | `internal/rpc/rpc.go:109, 136` | Unchecked `listener.Close()`, `conn.Close()` | Add `//nolint:errcheck` or handle |
| 3 | `internal/session/store.go:284` | `saveBranches` error ignored | Propagate error or log |
| 4 | `internal/session/store.go:505` | Unchecked `f.Close()` | Add `//nolint:errcheck` |
| 5 | `internal/session/store.go:692-693, 699` | Unchecked `f.Close()`, `os.Remove()` in cleanup | Add `//nolint:errcheck` |
| 6 | `internal/provider/provider.go:151` | Unchecked `resp.Body.Close()` | Add `//nolint:errcheck` |
| 7 | `internal/tools/sandbox.go:35` | Redundant if-check for error | Use direct return |

**What's working well**: Consistent `%w` wrapping pattern, proper context wrapping, sentinel errors used in config (`ErrNoDefaultRole`).

---

### Concurrency Patterns (7/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/session/store.go` | `saveBranches` called without mutex protection consideration | Review for race conditions |
| 2 | `internal/tui/tui.go` | Complex goroutine management | Document lifecycle clearly |

**What's working well**: Proper `sync.RWMutex` usage in `FileService`, correct `context.Context` propagation, channel-based communication.

---

### API Consistency (7/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | Provider constructors | Slight signature variations | Standardize optional parameter handling |
| 2 | `internal/tools/` | Varying patterns for tool creation | Document expected patterns |

**What's working well**: Consistent `NewFoo` constructor pattern, uniform error return signatures, cohesive provider factory.

---

### Code Organization (6/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/tui/tui.go:2625` | Exceeds 500-line guideline by 5x | Split into message, status, screen packages |
| 2 | `internal/cli/cli.go:812` | Near guideline limit | Consider extracting output mode handlers |
| 3 | `internal/tui/run.go:776` | Near guideline limit | Consider extracting run logic |

**What's working well**: Test files mirror source files, constants/vars at top, no `init()` functions, proper build tags.

---

### Documentation (8/10)

**Issues**:

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `internal/tui/tui.go:1345, 1368` | Unnecessary `fmt.Sprintf` | Use direct `WriteString` |
| 2 | `internal/audit/report.go:29-30, 33, 103` | Same issue | Use `fmt.Fprintf` |

**What's working well**: Package doc comments present, exported functions have doc comments, complex algorithms have inline comments explaining *why*.

---

## Summary

The pi-go codebase demonstrates solid Go engineering with good architectural decisions around ADK compliance, provider patterns, and session management. The primary concerns are:

1. **Error handling gaps** (50+ unchecked returns) — mostly in cleanup/defer patterns
2. **Code organization** — the massive TUI file needs refactoring
3. **Minor style inconsistencies** — unnecessary `fmt.Sprintf` patterns

The overall score of **7.1/10** reflects a production-quality codebase with noticeable room for improvement in error handling hygiene and code organization.
