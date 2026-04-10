# Implementation Outline: Session Log Optimizations

## Overview

6 optimizations implemented in 4 phases, each slice independently testable.

---

## Phase 1: Quick Wins (Documentation + System Prompt)

### Slice 1.1: Security Documentation
- **File**: `internal/tools/sandbox.go`
- **Change**: Add godoc to `Sandbox` struct and `Resolve()` method explaining `os.Root` security
- **Verify**: `go doc ./internal/tools/... | grep -A5 Sandbox`

### Slice 1.2: JSON Escaping Instructions
- **File**: `internal/agent/agent.go`
- **Change**: Add JSON escaping section to `SystemInstruction` (8→9 sections)
- **Verify**: `go build ./... && grep -c "JSON String Escaping" internal/agent/agent.go`

---

## Phase 2: Reliability (Edit Retry + Read Retry)

### Slice 2.1: Edit Handler Retry
- **File**: `internal/tools/edit.go`
- **Change**: Add `reReadFile()` helper, `buildEditNotFoundError()`, modify `editHandler` to re-read once on "not found"
- **Verify**: `go test ./internal/tools/... -run TestEdit`
- **Dep**: None (standalone)

### Slice 2.2: Sandbox Transient Retry
- **File**: `internal/tools/sandbox.go`
- **Change**: Add `isTransientReadError()`, `maxReadRetries=3`, modify `Sandbox.ReadFile()` with retry loop
- **Verify**: `go test ./internal/tools/... -run TestSandbox`
- **Dep**: None (standalone)

---

## Phase 3: Stability (Panic Recovery)

### Slice 3.1: TUI Goroutine Panic Recovery
- **File**: `internal/tui/tui.go`
- **Change**: Add `defer recover()` to `runAgentLoop()`, import `runtime/debug`
- **Verify**: `go test ./internal/tui/... -run TestAgent`
- **Dep**: Requires `runtime/debug`

---

## Phase 4: Performance (File Content Cache)

### Slice 4.1: File Content Cache (New File)
- **File**: `internal/tools/cache.go` (NEW)
- **Change**: Create `fileContentCache` struct with `Get()`, `Put()`, `Invalidate()`, `evictOldest()`
- **Verify**: `go test ./internal/tools/... -run TestCache`
- **Dep**: None (new file)

### Slice 4.2: Cache Integration with Read Handler
- **File**: `internal/tools/read.go`
- **Change**: Add optional cache parameter, check cache before read, update cache after read
- **Verify**: `go test ./internal/tools/... -run TestRead`
- **Dep**: Slice 4.1 must complete first

### Slice 4.3: Cache Invalidation on Edit
- **Files**: `internal/tools/edit.go` + `internal/tools/cache.go`
- **Change**: Call `cache.Invalidate()` after successful edit
- **Verify**: `go test ./internal/tools/... -run TestEditWithCache`
- **Dep**: Slice 4.1 and Slice 2.1 must complete first

---

## Key Type Signatures (Header Reference)

```go
// internal/tools/sandbox.go
type Sandbox struct {
    root *os.Root
    dir  string
}
func (s *Sandbox) Resolve(name string) (string, error)
func (s *Sandbox) ReadFile(name string) ([]byte, error)  // + retry logic

// internal/tools/edit.go
type EditInput struct {
    FilePath   string `json:"file_path"`
    OldString  string `json:"old_string"`
    NewString  string `json:"new_string"`
    ReplaceAll bool   `json:"replace_all,omitempty"`
}
type EditOutput struct {
    Path         string `json:"path"`
    Replacements int    `json:"replacements"`
}
func editHandler(sb *Sandbox, input EditInput) (EditOutput, error)  // + retry

// internal/tools/cache.go (NEW)
type fileContentCache struct {
    mu      sync.RWMutex
    entries map[string]*cachedFile
    maxSize int
    maxAge  time.Duration
}
func NewFileContentCache(maxSize int, maxAge time.Duration) *fileContentCache
func (c *fileContentCache) Get(path string, mtime int64) []byte
func (c *fileContentCache) Put(path string, content []byte, mtime int64)
func (c *fileContentCache) Invalidate(path string)

// internal/tools/read.go
type ReadInput struct {
    FilePath string `json:"file_path"`
    Offset   int    `json:"offset,omitempty"`
    Limit    int    `json:"limit,omitempty"`
}
func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error)  // + cache param

// internal/tui/tui.go
func (m *model) runAgentLoop(prompt string)  // + panic recovery
```

---

## Test Commands

| Phase | Command |
|---|---|
| All | `go test ./internal/tools/... ./internal/tui/...` |
| Phase 1 | `go build ./... && go vet ./...` |
| Phase 2 | `go test ./internal/tools/... -run "TestEdit|TestSandbox"` |
| Phase 3 | `go test ./internal/tui/... -run TestAgent` |
| Phase 4 | `go test ./internal/tools/... -run "TestCache|TestRead"` |
| Race | `go test -race ./internal/tools/... ./internal/tui/...` |

---

## Dependency Graph

```
Phase 1
  ├── Slice 1.1 (sandbox docs)
  └── Slice 1.2 (agent prompt)
       │
Phase 2 ───────────────────────────────
  ├── Slice 2.1 (edit retry)───────────┼─── No inter-deps
  └── Slice 2.2 (read retry)───────────┘

Phase 3
  └── Slice 3.1 (panic recovery)       ← Independent

Phase 4
  ├── Slice 4.1 (cache)─────────────────┐── Slice 4.2 depends on 4.1
  ├── Slice 4.2 (read + cache)────────┤   Slice 4.3 depends on 4.1, 2.1
  └── Slice 4.3 (invalidate on edit)──┘
```

---

## Files Summary

| File | Phase | Type | Lines (est.) |
|---|---|---|---|
| `internal/tools/sandbox.go` | 1, 2 | Modify | +40 |
| `internal/agent/agent.go` | 1 | Modify | +25 |
| `internal/tools/edit.go` | 2, 4 | Modify | +30 |
| `internal/tui/tui.go` | 3 | Modify | +8 |
| `internal/tools/cache.go` | 4 | **NEW** | ~100 |
| `internal/tools/read.go` | 4 | Modify | +20 |

**Total new code**: ~220 lines across 6 files
