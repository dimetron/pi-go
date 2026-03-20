# Research: Session Storage, Panic Recovery, Caching, and Retry Patterns

This document catalogs existing patterns across the codebase for session management, error recovery, caching, and retry logic.

---

## 1. Session State Storage (`internal/session/store.go`)

### Key Structures

| Structure | File Location | Purpose |
|---|---|---|
| `Meta` | store.go:24-32 | Persisted metadata (ID, AppName, UserID, WorkDir, Model, timestamps) |
| `FileService` | store.go:36-41 | Implements `session.Service` with file-based JSONL persistence |
| `fileSession` | store.go:360-366 | In-memory session holding events, state map, and metadata |
| `liveSession` | store.go:376-378 | Live view of fileSession for ADK runner |
| `filteredSession` | store.go:403-406 | Filtered events view (NumRecentEvents, After timestamp) |
| `liveState` | store.go:416-418 | Backed by fileSession's state map, implements `session.State` |

### State Flow

```
FileService (map[string]*fileSession, in-memory cache)
    │
    ├── fileSession.state (map[string]any) — session state
    │   └── populated via event.Actions.StateDelta on AppendEvent
    │
    ├── fileSession.events ([]*session.Event) — event history
    │   └── persisted to events.jsonl (append-only, compacted via summary events)
    │
    └── fileSession.meta (Meta) — persisted to meta.json
        └── UpdatedAt timestamp updated on each AppendEvent
```

### State Keys and Values

- **State is stored in `fileSession.state`** — a `map[string]any` rebuilt from event `StateDelta` on load
- **`session.KeyPrefixTemp`** — keys prefixed with `"_temp_"` are stripped during `AppendEvent` (store.go:251)
- **No cached file content in session state** — file reads are always performed fresh
- **`EstimateEventTokens()`** (store.go:654-676) — token estimation via `chars/4` heuristic across event content, function calls, and responses

### Session Storage Layout

```
~/.pi-go/sessions/<session-id>/
├── meta.json         — Meta struct (JSON, human-readable)
├── events.jsonl      — Event log (append-only JSONL)
├── branches.json     — Branch state (active branch + head index)
└── (future) compactor-metrics.json — compaction stats
```

### Key Behaviors

- **Atomic writes**: `rewriteEvents()` uses temp file + rename for atomicity (store.go:682-708)
- **Append-only events**: events appended to JSONL on disk; compaction rewrites the file
- **State delta filtering**: temp keys (`_temp_` prefix) stripped before persistence (store.go:251-256)
- **In-memory cache miss**: loads from disk, rebuilds state from all event deltas (store.go:319-322)

---

## 2. Panic Recovery in Compactor (`internal/tools/compactor*.go`)

### Pattern: `runStage()` with Deferred Recover

**Location**: `internal/tools/compactor.go:156-172`

```go
func runStage(input string, techniques *[]string, name string, fn func(string) (string, bool)) (result string) {
    result = input
    defer func() {
        if r := recover(); r != nil {
            log.Printf("compactor: stage %q panicked: %v", name, r)
            result = input  // fall back to original input on panic
        }
    }()

    output, applied := fn(input)
    if applied {
        *techniques = append(*techniques, name)
        return output
    }
    return input
}
```

### Key Characteristics

- **Returns original input on panic** — graceful degradation, never panics up the stack
- **Named return value** — `result` is set as named return, modified in defer
- **Logging only** — logs the panic with stage name, no re-throw
- **Applied flag** — only marks technique as applied if `fn` returns `applied=true`
- **No mutex** — each `runStage` call is single-threaded, no concurrency concerns

### Other Panic-Safe Patterns in Compactor

- **`applyCompaction()`** (compactor.go:119-154) — nil-checks for `result` and `cr` before accessing fields
- **`compactBash()`** (compactor_bash.go) — detects panic/fatal in output strings (heuristic, line 401): `strings.Contains(lower, "panic") || strings.Contains(lower, "fatal")`
- **Tests** explicitly verify no panic on nil inputs (compactor_test.go:813-815)

### Compactor Metrics

**Location**: `internal/tools/compactor_metrics.go`

- `CompactMetrics` struct with `sync.Mutex` for thread-safe recording
- `Record()` — adds `CompactRecord` with tool, techniques, orig/comp sizes, timestamp
- `Summary()` — aggregates stats per tool, computes savings percentage
- `Save()` — persists to `compactor-metrics.json` in session directory
- `FormatStats()` — human-readable output for `/context` command

---

## 3. Goroutine and Error Handling in TUI (`internal/tui/*.go`)

### Goroutine Pattern: Agent Loop

**Location**: `internal/tui/tui.go:1365-1412`

```go
func (m *model) runAgentLoop(prompt string) {
    defer close(m.agentCh)  // closes channel when goroutine exits
    log := m.cfg.Logger

    for ev, err := range m.cfg.Agent.RunStreaming(m.ctx, m.cfg.SessionID, prompt) {
        if err != nil {
            if log != nil {
                log.Error(err.Error())
            }
            m.agentCh <- agentDoneMsg{err: err}  // send error to main loop
            return
        }
        // ... dispatch events to channel
    }
}
```

### Key Characteristics

- **Channel-backed**: Uses `chan agentMsg` (buffered 64, tui.go:909) to communicate with main Bubble Tea loop
- **`defer close()`**: Ensures channel is closed when goroutine exits
- **No panic recovery**: If `Agent.RunStreaming` panics, the goroutine dies and the channel is closed — the TUI would not receive an error message
- **Error sent as message**: Errors are wrapped in `agentDoneMsg{err: err}` and sent to the channel, not thrown

### Agent Message Types (tui.go:34-61)

| Type | Fields | Purpose |
|---|---|---|
| `agentTextMsg` | `text` | LLM text output |
| `agentThinkingMsg` | `text` | Thinking/thinking block output |
| `agentToolCallMsg` | `name`, `args` | Tool invocation |
| `agentToolResultMsg` | `name`, `content` | Tool result |
| `agentDoneMsg` | `err` | Agent finished (error or success) |
| `agentSubEventMsg` | `agentID`, `kind`, `content` | Subagent streamed events |

### Error Handling in TUI Goroutines

**Location**: `internal/tui/tui.go:616, 645` — two anonymous goroutines in skill loading:

```go
go func(ch chan agentMsg) {
    defer func() {
        if r := recover(); r != nil {
            ch <- agentDoneMsg{err: fmt.Errorf("skill load panicked: %v", r)}
        }
    }()
    // ... skill loading ...
}()
```

- **Explicit panic recovery** in skill-loading goroutines (not in `runAgentLoop`)
- **`agentDoneMsg` on panic**: graceful error propagation via channel

### Wait Pattern

**Location**: `internal/tui/tui.go:913` — `waitForAgent(m.agentCh)` blocks until channel is closed

---

## 4. Existing Caching Mechanisms

### LSP Diagnostic Cache

**Location**: `internal/lsp/manager.go:296-301`

```go
func (m *Manager) CachedDiagnostics(fileURI string) []Diagnostic {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.diagnostics[fileURI]
}
```

- In-memory map `map[string][]Diagnostic` indexed by file URI
- Thread-safe with mutex
- Pre-populated on file change, read by tools/lsp.go for tool output

### Session In-Memory Cache

**Location**: `internal/session/store.go:39-40`

```go
type FileService struct {
    baseDir string
    mu      sync.RWMutex
    // In-memory cache of sessions for fast access during a run.
    sessions map[string]*fileSession
}
```

- `sessions` map: session ID → `*fileSession`
- Loaded lazily on `Get()` / `AppendEvent()`
- Populated on `Create()` and `loadSession()`
- Evicted on `Delete()`

### Compactor Metrics Cache

- `CompactMetrics.Records` — in-memory slice of compaction records
- Persisted to disk via `Save()` on session directory

### No File Content Cache

- **No file content cache exists** in the codebase
- `readHandler` reads files fresh on every call (per codebase-exploration.md:424)
- This is a documented optimization opportunity

---

## 5. Existing Retry Patterns

### Agent-Level Retry (`internal/agent/retry.go`)

**Configuration**:

```go
type RetryConfig struct {
    MaxRetries   int           // default 3
    InitialDelay time.Duration // default 1s
    MaxDelay     time.Duration // default 30s
}
```

**Algorithm**: Exponential backoff with jitter cap

```go
func retryDelay(cfg RetryConfig, attempt int) time.Duration {
    delay := float64(cfg.InitialDelay) * math.Pow(2, float64(attempt))
    if delay > float64(cfg.MaxDelay) {
        delay = float64(cfg.MaxDelay)
    }
    return time.Duration(delay)
}
```

**Transient Error Detection** (`isTransient`, retry.go:35-82):

```go
func isTransient(err error) bool {
    patterns := []string{
        "429", "rate limit", "rate_limit", "too many requests",
        "500", "502", "503", "504", "internal server error",
        "bad gateway", "service unavailable", "gateway timeout",
        "connection reset", "connection refused", "timeout",
        "deadline exceeded", "temporary failure", "overloaded",
    }
    // + errors.Is with Timeout() and Temporary() interfaces
}
```

**Usage** (cli.go:445-446, 502-503):
```go
retryCfg := agent.DefaultRetryConfig()
for ev, err := range agent.WithRetry(retryCfg, func() iter.Seq2[*session.Event, error] {
    return agent.RunStreaming(...)
}) { ... }
```

### Retry Wrapping Rules

- **Partial results block retry**: If any events were yielded before transient error, does NOT retry (retry.go:119-123)
- **Non-transient errors**: Yielded immediately without retry
- **Exhausted retries**: Error with count included

### Subagent Retry (`internal/subagent/worktree.go:104-140`)

```go
// If cleanup fails, the entry is re-added to the active map so callers can retry.
func (m *WorktreeManager) cleanupWorktree(agentID string, info worktreeInfo) error {
    // ...
    if len(errs) > 0 {
        // Re-add entry so a retry pass can attempt again.
        m.mu.Lock()
        m.active[agentID] = info
        m.mu.Unlock()
        return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
    }
}
```

- **Worktree cleanup retry**: Re-adds to `active` map on failure for caller retry
- **No automatic retry loop** — caller is responsible for retrying

### Plan Run Gate Retry (`internal/tui/run.go`)

- Gate failures trigger agent retry in same worktree (run.go:406-427)
- Max retries configurable (default 3)
- Retry prompt includes gate output and original task

### No Tool-Level Retry

- **No retry logic in tool layer** — tools fail immediately
- Rationale: retrying with same input produces same result for file operations
- (Per codebase-exploration.md:420)

---

## 6. Maximum Prompt Length

### Token Estimation

**Location**: `internal/session/store.go:654-676`

```go
func estimateEventTokens(events []*session.Event) int {
    total := 0
    for _, ev := range events {
        if ev.Content == nil { continue }
        for _, part := range ev.Content.Parts {
            if part.Text != "" {
                total += len(part.Text) / 4
            }
            if part.FunctionCall != nil {
                data, _ := json.Marshal(part.FunctionCall.Args)
                total += (len(part.FunctionCall.Name) + len(data)) / 4
            }
            if part.FunctionResponse != nil {
                data, _ := json.Marshal(part.FunctionResponse.Response)
                total += (len(part.FunctionResponse.Name) + len(data)) / 4
            }
        }
    }
    return total
}
```

**Heuristic**: `chars / 4` — approximate, underestimates for code-heavy content

### Compactor Threshold

**Location**: `internal/session/store.go:549-565`

```go
type CompactConfig struct {
    MaxTokens  int // default: 100000
    KeepRecent int // default: 10 events
}
```

- **Default trigger**: 100,000 tokens (~400K chars)
- **Best practice from research**: Avoid filling past **60% of context window capacity**
- **Different models have different limits**: Claude 3.5 (200K), Gemini 1.5 (1M/2M), GPT-4o (128K)

### Context Window Best Practices (from `research/coding-agents/insights-context-engineering.md`)

- Current best practice: avoid filling past **60%** of context window capacity
- Using **less** of the context window leads to **better** results
- **Auto-compaction** triggers when context window reaches a threshold
- LLM should have control over managing its own context window

---

## 7. Patterns to Follow

### Panic Recovery Template

```go
func safeOperation(input T) (result T) {
    result = input // establish baseline
    defer func() {
        if r := recover(); r != nil {
            log.Printf("operation %q panicked: %v", name, r)
            // result already set to input (graceful degradation)
        }
    }()
    // ... actual work ...
    return processed
}
```

### Channel-Based Goroutine Error Propagation

```go
type doneMsg struct{ err error }

func (m *model) worker() {
    defer close(m.ch)
    for item := range m.items {
        if err := process(item); err != nil {
            m.ch <- doneMsg{err: err}
            return
        }
    }
}
```

### Mutex-Protected Shared State

```go
type Cache struct {
    mu sync.Mutex
    m  map[string]Value
}

func (c *Cache) Get(k string) Value {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.m[k]
}
```

### Token Budget Configuration

```go
type TokenBudget struct {
    MaxTokens      int // compaction trigger
    SafetyMargin   float64 // 0.6 = 60% rule
}
```

---

## Summary Table

| Area | Mechanism | Location |
|---|---|---|
| Session state | `map[string]any` in `fileSession`, via `StateDelta` | store.go:364 |
| Temp state keys | Prefix `"_temp_"` stripped on persist | store.go:251 |
| Session persistence | JSONL append + atomic rewrite | store.go:494-709 |
| Panic recovery | `defer recover()` in `runStage()` | compactor.go:159-163 |
| Goroutine errors | Channel-backed `agentDoneMsg` | tui.go:34-47, 1365-1412 |
| LSP cache | `map[string][]Diagnostic` + mutex | lsp/manager.go:296-301 |
| Session cache | `map[string]*fileSession` + RWMutex | store.go:39-40 |
| Agent retry | Exponential backoff, transient detection | agent/retry.go |
| Subagent retry | Worktree re-add on cleanup failure | subagent/worktree.go:133-137 |
| Token estimation | `chars/4` heuristic | store.go:654-676 |
| Compaction trigger | 100K tokens default | store.go:561 |
