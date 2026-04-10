# Implementation Plan: Session Log Optimizations

## Vertical Slices

---

## Phase 1: Quick Wins

### - [ ] Slice 1.1: Security Documentation in Sandbox

**Files to create/modify:**
- `internal/tools/sandbox.go` - Add godoc to `Sandbox` struct and `Resolve()` method

**Changes:**
```go
// Replace existing struct comment (around line 11-14):

// Sandbox provides a secure file system abstraction that restricts
// all file operations to a specific directory tree.
//
// SECURITY MODEL:
//   - All file paths are resolved relative to the sandbox root
//   - Access outside the sandbox is blocked via os.Root (Go 1.24+)
//   - This prevents the agent from accessing sensitive files outside
//     the working directory (e.g., ~/.ssh, /etc, etc.)
//
// LIMITATIONS:
//   - Files outside the sandbox cannot be accessed
//   - Symlinks pointing outside are blocked
//   - Absolute paths are converted to relative
//
// WORKAROUNDS:
//   - Change the working directory to access different files
//   - Use tools that explicitly access external resources (e.g., fetch URLs)
type Sandbox struct {
    root *os.Root  // Go 1.24+ secure root handle
    dir  string    // absolute path of the root directory
}
```

```go
// Replace existing Resolve() comment (around line 44-56):

// Resolve converts an absolute or relative path to a relative path
// under the sandbox root. os.Root enforces that the resolved path
// cannot escape the directory tree (via ".." or symlinks).
//
// SECURITY: This is intentional. The sandbox restricts file system
// access to prevent the agent from reading/writing files outside
// the working directory. This includes:
//   - Parent directories (..)
//   - Symlinks pointing outside
//   - Absolute paths outside the sandbox root
//
// If the user needs access to files outside the sandbox, they must either:
//   1. Change the working directory to a parent of the needed files
//   2. Use a tool that can explicitly access external resources
func (s *Sandbox) Resolve(name string) (string, error) {
```

**Verification:** `go doc ./internal/tools/... | grep -A10 "type Sandbox"`

---

### - [ ] Slice 1.2: JSON Escaping Instructions in System Prompt

**Files to create/modify:**
- `internal/agent/agent.go` - Add JSON escaping section to `SystemInstruction`

**Changes:**
Add the following section around line 103 (before the Subagents section):

```markdown
# JSON String Escaping

When sending tool parameters that contain file paths or other strings with special characters:
- Always escape backslashes in JSON: use `\\` not `\`
- For Windows paths like `C:\Users\test`, send as `"C:\\Users\\test"` in JSON
- For Unix paths with special chars, ensure proper escaping
- Before calling any tool that requires a file_path, verify the path is properly escaped

Example of INCORRECT JSON (will cause tool errors):
{"file_path": "C:\Users\test\file.go"}

Example of CORRECT JSON:
{"file_path": "C:\\Users\\test\\file.go"}
```

**Verification:** `go build ./... && grep -c "JSON String Escaping" internal/agent/agent.go`

---

## Phase 2: Reliability

### - [ ] Slice 2.1: Edit Handler Retry with Enhanced Errors

**Files to create/modify:**
- `internal/tools/edit.go` - Add retry logic and enhanced error messages

**Changes:**

1. Add helper functions after the imports section (around line 10):

```go
// buildEditNotFoundError creates a helpful error message when old_string is not found.
func buildEditNotFoundError(input EditInput, content string) error {
    preview := content
    if len(preview) > 500 {
        preview = preview[:500] + "\n..."
    }
    return fmt.Errorf(`old_string not found in file

Expected:
%s

File preview (first 500 chars):
%s

Suggestions:
- Verify the exact text matches including whitespace and newlines
- Use the Read tool to see the current file content
- Try a smaller, unique portion of the old_string`,
        input.OldString, preview)
}
```

2. Modify `editHandler` function (around lines 50-65). Replace the single read logic:

**Before:**
```go
data, err := sb.ReadFile(input.FilePath)
if err != nil {
    return EditOutput{}, fmt.Errorf("reading file: %w", err)
}
content := string(data)
count := strings.Count(content, input.OldString)
if count == 0 {
    return EditOutput{}, fmt.Errorf("old_string not found in file")
}
```

**After:**
```go
data, err := sb.ReadFile(input.FilePath)
if err != nil {
    return EditOutput{}, fmt.Errorf("reading file: %w", err)
}
content := string(data)
count := strings.Count(content, input.OldString)

// Retry once if not found (file may have been modified since last read)
if count == 0 {
    data, err = sb.ReadFile(input.FilePath)
    if err == nil {
        content = string(data)
        count = strings.Count(content, input.OldString)
    }
}

if count == 0 {
    return EditOutput{}, buildEditNotFoundError(input, content)
}
```

3. Update the "multiple matches" error message to include helpful context:

**Before:**
```go
if count > 1 && !input.ReplaceAll {
    return EditOutput{}, fmt.Errorf("old_string found %d times in file... use replace_all", count)
}
```

**After:**
```go
if count > 1 && !input.ReplaceAll {
    return EditOutput{}, fmt.Errorf(`old_string found %d times in file.
Use replace_all=true to replace all occurrences, or provide a more specific old_string.`, count)
}
```

**Verification:** `go test ./internal/tools/... -run TestEdit`

---

### - [ ] Slice 2.2: Sandbox Transient Retry

**Files to create/modify:**
- `internal/tools/sandbox.go` - Add retry logic to `ReadFile`

**Changes:**

1. Add imports (if `time` not already present):
```go
import (
    "time"
)
```

2. Add constants after the type definition (around line 14):

```go
const (
    maxReadRetries = 3                   // max retry attempts
    readRetryDelay = 50 * time.Millisecond // initial delay between retries
)
```

3. Add transient error detection function (before `ReadFile` method, around line 58):

```go
// isTransientReadError returns true for errors that might succeed on retry.
func isTransientReadError(err error) bool {
    if err == nil {
        return false
    }
    msg := err.Error()
    transient := []string{
        "text file busy",
        "resource temporarily unavailable",
        "input/output error",
    }
    for _, t := range transient {
        if strings.Contains(msg, t) {
            return true
        }
    }
    return false
}
```

4. Modify `ReadFile` method to include retry logic:

**Before (lines 59-67):**
```go
func (s *Sandbox) ReadFile(name string) ([]byte, error) {
    rel, err := s.Resolve(name)
    if err != nil {
        return nil, err
    }
    return s.root.ReadFile(rel)
}
```

**After:**
```go
func (s *Sandbox) ReadFile(name string) ([]byte, error) {
    rel, err := s.Resolve(name)
    if err != nil {
        return nil, err
    }

    var lastErr error
    for attempt := 0; attempt < maxReadRetries; attempt++ {
        data, err := s.root.ReadFile(rel)
        if err == nil {
            return data, nil
        }

        if !isTransientReadError(err) {
            return nil, err
        }
        lastErr = err

        if attempt < maxReadRetries-1 {
            time.Sleep(readRetryDelay * time.Duration(attempt+1))
        }
    }
    return nil, lastErr
}
```

**Verification:** `go test ./internal/tools/... -run TestSandbox`

---

## Phase 3: Stability

### - [ ] Slice 3.1: TUI Goroutine Panic Recovery

**Files to create/modify:**
- `internal/tui/tui.go` - Add panic recovery to `runAgentLoop`

**Changes:**

1. Add import if not present (around line 15):
```go
import (
    "runtime/debug"
)
```

2. Modify `runAgentLoop` function (around line 1365). Add `defer recover()` after `defer close()`:

**Before:**
```go
func (m *model) runAgentLoop(prompt string) {
    defer close(m.agentCh)

    log := m.cfg.Logger
    // ...
}
```

**After:**
```go
func (m *model) runAgentLoop(prompt string) {
    defer close(m.agentCh)
    defer func() { // Panic recovery
        if r := recover(); r != nil {
            log.Printf("agent loop panicked: %v\n%s", r, debug.Stack())
            m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent panic: %v", r)}
        }
    }()

    log := m.cfg.Logger
    // ...
}
```

3. Ensure `fmt` is imported (should already be present for `agentDoneMsg`).

**Verification:** `go test ./internal/tui/... -run TestAgent`

---

## Phase 4: Performance

### - [ ] Slice 4.1: File Content Cache (New File)

**Files to create:**
- `internal/tools/cache.go` - New file for file content cache

**Content:**
```go
package tools

import (
    "sync"
    "time"
)

// fileContentCache stores recently read file contents to reduce duplicate reads.
type fileContentCache struct {
    mu      sync.RWMutex
    entries map[string]*cachedFile
    maxSize int           // max entries before eviction
    maxAge  time.Duration // max age before refresh
}

type cachedFile struct {
    content    []byte
    readAt     time.Time
    mtime      int64 // modification time for invalidation
}

// NewFileContentCache creates a new file content cache.
func NewFileContentCache(maxSize int, maxAge time.Duration) *fileContentCache {
    return &fileContentCache{
        entries: make(map[string]*cachedFile),
        maxSize: maxSize,
        maxAge:  maxAge,
    }
}

// Get returns cached content if valid (mtime matches and not expired).
// Returns nil if not found, expired, or mtime mismatch.
func (c *fileContentCache) Get(path string, mtime int64) []byte {
    c.mu.RLock()
    defer c.mu.RUnlock()

    entry, ok := c.entries[path]
    if !ok {
        return nil
    }
    if entry.mtime != mtime {
        return nil // invalidated by mtime change
    }
    if time.Since(entry.readAt) > c.maxAge {
        return nil // expired
    }
    return entry.content
}

// Put stores content in cache.
func (c *fileContentCache) Put(path string, content []byte, mtime int64) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Evict oldest if at capacity
    if len(c.entries) >= c.maxSize {
        c.evictOldest()
    }

    c.entries[path] = &cachedFile{
        content: content,
        readAt:  time.Now(),
        mtime:   mtime,
    }
}

// Invalidate removes a path from cache.
func (c *fileContentCache) Invalidate(path string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.entries, path)
}

// Size returns the current number of cached entries.
func (c *fileContentCache) Size() int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return len(c.entries)
}

func (c *fileContentCache) evictOldest() {
    var oldest string
    var oldestTime time.Time
    for path, entry := range c.entries {
        if oldestTime.IsZero() || entry.readAt.Before(oldestTime) {
            oldest = path
            oldestTime = entry.readAt
        }
    }
    if oldest != "" {
        delete(c.entries, oldest)
    }
}
```

**Verification:** `go test ./internal/tools/... -run TestCache`

---

### - [ ] Slice 4.2: Cache Integration with Read Handler

**Files to create/modify:**
- `internal/tools/read.go` - Add cache integration

**Changes:**

1. Add global cache variable after imports (around line 10):
```go
// globalFileCache is the default file content cache.
// Can be set via WithCache option.
var globalFileCache *fileContentCache
```

2. Add cache option to `ReadInput` or create a new handler variant:

Add to read.go (after the existing functions):

```go
// readHandlerWithCache returns a readHandler that uses the provided cache.
func readHandlerWithCache(sb *Sandbox, input ReadInput, cache *fileContentCache) (ReadOutput, error) {
    if input.FilePath == "" {
        return ReadOutput{}, fmt.Errorf("file_path is required")
    }

    // Get file info for mtime-based cache
    info, err := sb.Stat(input.FilePath)
    if err != nil {
        return ReadOutput{}, fmt.Errorf("reading file: %w", err)
    }
    mtime := info.ModTime().Unix()

    // Check cache first
    if cache != nil {
        if cached := cache.Get(input.FilePath, mtime); cached != nil {
            return processReadOutput(string(cached), input.Offset, input.Limit)
        }
    }

    // Read from filesystem
    data, err := sb.ReadFile(input.FilePath)
    if err != nil {
        return ReadOutput{}, fmt.Errorf("reading file: %w", err)
    }
    content := string(data)

    // Update cache
    if cache != nil {
        cache.Put(input.FilePath, data, mtime)
    }

    return processReadOutput(content, input.Offset, input.Limit)
}

// processReadOutput handles offset/limit formatting and truncation.
func processReadOutput(content string, offset, limit int) (ReadOutput, error) {
    // ... move existing offset/limit/truncation logic here ...
}
```

3. Update `readHandler` to use the new function:
```go
func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
    return readHandlerWithCache(sb, input, globalFileCache)
}
```

**Verification:** `go test ./internal/tools/... -run TestRead`

---

### - [ ] Slice 4.3: Cache Invalidation on Edit

**Files to create/modify:**
- `internal/tools/edit.go` - Invalidate cache after successful edit
- `internal/tools/cache.go` - Export the cache getter if needed

**Changes:**

1. Add cache reference to edit.go:
```go
// globalFileCache for edit cache invalidation
var globalFileCache *fileContentCache
```

2. After successful write in `editHandler` (around line 73), add:
```go
// Invalidate cache entry for this file
if globalFileCache != nil {
    globalFileCache.Invalidate(input.FilePath)
}
```

3. Optionally add a function to set the cache:
```go
// SetEditCache sets the global file cache for edit handler.
func SetEditCache(cache *fileContentCache) {
    globalFileCache = cache
}
```

**Verification:** `go test ./internal/tools/... -run TestEdit`

---

## Testing Checklist

After each slice, verify:

| Slice | Test Command | Expected |
|---|---|---|
| 1.1 | `go doc ./internal/tools/... | grep Sandbox` | Shows security documentation |
| 1.2 | `grep "JSON String Escaping" internal/agent/agent.go` | Found |
| 2.1 | `go test ./internal/tools/... -run TestEdit` | Pass |
| 2.2 | `go test ./internal/tools/... -run TestSandbox` | Pass |
| 3.1 | `go test ./internal/tui/... -run TestAgent` | Pass |
| 4.1 | `go test ./internal/tools/... -run TestCache` | Pass |
| 4.2 | `go test ./internal/tools/... -run TestRead` | Pass |
| 4.3 | `go test ./internal/tools/... -run TestEdit` | Pass |

Final verification:
```bash
go build ./...
go vet ./...
go test -race ./internal/tools/... ./internal/tui/...
```

---

## Rollback Notes

If a slice causes issues:
- Phase 1: Remove godoc additions / remove JSON section
- Phase 2: Revert to single-read logic in edit.go / remove retry loop in sandbox.go
- Phase 3: Remove the `defer recover()` block
- Phase 4: Delete cache.go, revert read.go to original

All changes are additive except for the retry logic modifications which can be reverted cleanly.
