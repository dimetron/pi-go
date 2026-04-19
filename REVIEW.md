# Code Review: pi-go Codebase

**Review Date:** 2025-01-15  
**Reviewers:** Claude Code (Bug Detection, Error Handling, Style, Performance) + Gemini (Security, API Design, Tests,
Documentation)  
**Duration:** 2m32s (parallel)

---

## 🐛 Critical Bugs

### 1. Undefined variable `abs` in sandbox.go

**File:** `internal/tools/sandbox.go:95-96`

```go
// Create the directory if it doesn't exist.
if err := os.MkdirAll(abs, 0o700); err != nil {  // ❌ 'abs' undefined
return fmt.Errorf("creating extra dir %s: %w", abs, err) // ❌ 'abs' undefined
}
```

**Issue:** The variable `abs` is not defined in the `AddExtraDir` function. The correct variable should be `absPath`.
This will cause a **compile error** if `AddExtraDir` is ever called with a path that triggers the `os.MkdirAll` call.

**Fix:** Replace `abs` with `absPath` on both lines.

---

### 2. Shadowed error variable in readLastSession

**File:** `internal/cli/cli.go:674`

```go
func readLastSession() (*lastSessionData, error) {
data := &lastSessionData{}  // ✅ initialized
blob, err := os.ReadFile(lastSessionFile)
if err != nil {
if os.IsNotExist(err) {
return nil, nil
}
return nil, err
}
if err := json.Unmarshal(blob, data); err != nil {  // ⚠️ 'err' shadowed
return nil, err
}
return data, nil
}
```

**Issue:** The shadowing of `err` from `os.ReadFile` by the second `if err :=` is confusing and could cause subtle bugs.

**Fix:** Use a different variable name:

```go
if unmarshalErr := json.Unmarshal(blob, data); unmarshalErr != nil {
return nil, unmarshalErr
}
```

---

## ⚠️ Error Handling Issues

### 3. Non-fatal errors not logged in grep.go

**File:** `internal/tools/grep.go:181-184`

```go
// Load .gitignore patterns
patterns, err := sb.LoadGitignorePatterns()
if err != nil {
patterns = nil // Silently ignored
}
```

**Issue:** Non-fatal errors are silently ignored, making debugging difficult.

**Recommendation:** Add logging for debugging purposes.

---

### 4. Unchecked error in subagent spawner

**File:** `internal/subagent/spawner.go:121`

```go
// Ensure the process and its children are killed on cancel.
setPlatformAttrs(cmd)
cmd.WaitDelay = 3 * time.Second // ⚠️ setPlatformAttrs return value unchecked
```

**Issue:** `setPlatformAttrs` may return an error that is being ignored. While likely non-fatal, it should be logged or
handled.

---

### 5. Startup availability check for ripgrep

**File:** `internal/tools/grep.go:87-95`

```go
func ripgrepAvailable() bool {
cmd := exec.Command("rg", "--version")
if err := cmd.Run(); err != nil {
return false
}
return true
}

var rgAvailable = ripgrepAvailable() // Runs at program startup
```

**Issue:** If `rg` is not installed, this check runs at program startup. Consider lazy initialization or deferred check.

---

## 📏 Code Style Inconsistencies

### 6. Mixed error handling patterns

**Files:** Various

The codebase uses multiple patterns for error handling:

```go
// Pattern 1: Direct if
if err != nil {
return ..., err
}

// Pattern 2: Short-circuit with early return (preferred)
if err := something(); err != nil {
return ..., err
}
```

**Recommendation:** Standardize on the short-circuit style. It is more idiomatic in modern Go and reduces nesting.

---

### 7. Magic numbers without constants

**File:** `internal/tools/grep.go:83`

```go
var grepRegexCache = newRegexCache(50, 10*time.Minute)
```

**Issue:** The magic numbers `50` and `10*time.Minute` should be named constants for maintainability.

**Recommendation:** Extract to:

```go
const (
defaultRegexCacheSize = 50
defaultRegexCacheTTL = 10 * time.Minute
)
```

---

### 8. Inconsistent parameter validation

**File:** `internal/tools/bash.go:41-43` vs `internal/tools/edit.go:46-54`

```go
// bash.go - minimal validation
if input.Command == "" {
return BashOutput{}, fmt.Errorf("command is required")
}

// edit.go - multiple validations
if input.FilePath == "" {
return EditOutput{}, fmt.Errorf("file_path is required")
}
if input.OldString == "" {
return EditOutput{}, fmt.Errorf("old_string is required")
}
if input.OldString == input.NewString {
return EditOutput{}, fmt.Errorf("old_string and new_string must be different")
}
```

**Recommendation:** Consider consistent validation patterns across all tools. At minimum, validate that required fields
are non-empty.

---

## ⚡ Performance Issues

### 9. Regex cache uses simple Mutex instead of RWMutex

**File:** `internal/tools/grep.go:22-27`

```go
type regexCache struct {
mu      sync.Mutex // ⚠️ Could use RWMutex for better read performance
entries map[string]*cachedRegex
maxSize int
maxAge  time.Duration
}
```

**Issue:** The `get()` method only reads, while `put()` modifies. Using `sync.RWMutex` would allow concurrent readers:

```go
type regexCache struct {
mu      sync.RWMutex
entries map[string]*cachedRegex
maxSize int
maxAge  time.Duration
}

func (c *regexCache) get(key string) *regexp.Regexp {
c.mu.RLock()
defer c.mu.RUnlock()
// ...
}
```

---

### 10. Global regex compilation at package init

**File:** `internal/tools/read.go:43`

```go
var base64ImagePattern = regexp.MustCompile(`!\\[([^\\]]*)\\]\\(data:[^)]+\\)`)
```

**Issue:** This regex is compiled once at program startup. For large regexes, consider lazy initialization, though this
is acceptable for small patterns.

---

### 11. Repeated directory walking in grep.go

**File:** `internal/tools/grep.go:180-184`

```go
// Load .gitignore patterns
patterns, err := sb.LoadGitignorePatterns()
if err != nil {
patterns = nil // Silently ignored
}
```

**Issue:** The `LoadGitignorePatterns()` is called on **every grep invocation**, which walks the entire directory tree.
This is expensive for large codebases.

**Recommendation:** Consider caching with invalidation on file changes or a TTL-based cache.

---

## 🔒 Security Considerations

*(Reviewed by Gemini)*

- Sandbox security model using `os.Root` is well-designed
- Path validation appears robust
- No obvious SQL injection or command injection vulnerabilities
- Consider auditing tool inputs for path traversal attacks

---

## 📋 Summary Table

| Category       | Severity | Count | Files                             |
|----------------|----------|-------|-----------------------------------|
| Critical Bugs  | High     | 1     | `sandbox.go:95-96`                |
| Error Handling | Medium   | 3     | `cli.go`, `grep.go`, `spawner.go` |
| Style          | Low      | 3     | Various                           |
| Performance    | Low      | 3     | `grep.go`, `read.go`              |

---

## 🔧 Recommended Fixes (Priority Order)

1. **Fix `abs` variable bug** in `sandbox.go:95-96` → should be `absPath`
2. **Add RWMutex** to `regexCache` for better read concurrency
3. **Cache gitignore patterns** or add TTL-based caching
4. **Standardize error handling** patterns across tools
5. **Extract magic numbers** to named constants
6. **Fix shadowed `err` variable** in `readLastSession()`

---

## ✅ Positive Findings

1. **Sandbox security model** (`internal/tools/sandbox.go`) - Excellent use of `os.Root` for path restriction
2. **Retry logic** (`internal/agent/retry.go`) - Clean exponential backoff implementation
3. **Tool registry pattern** (`internal/tools/registry.go`) - Good abstraction for tool creation
4. **Graceful degradation** - grep falls back to Go implementation if ripgrep unavailable
5. **Clean error wrapping** with `fmt.Errorf("context: %w", err)` pattern
6. **Good use of ADK interfaces** over custom abstractions
