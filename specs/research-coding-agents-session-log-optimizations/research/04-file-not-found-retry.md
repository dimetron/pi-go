# Research: File Not Found During Read (Retry Logic)

## Problem Statement

File read operations fail immediately on transient errors like momentary file locks, NFS hiccups, or files being created. The tool should retry before failing.

## Current State

### readHandler Function (read.go:41-96)

```go
func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
    // Validation
    if input.FilePath == "" {
        return ReadOutput{}, fmt.Errorf("file_path is required")
    }
    
    // Single read attempt
    data, err := sb.ReadFile(input.FilePath)
    if err != nil {
        return ReadOutput{}, fmt.Errorf("reading file: %w", err)
    }
    
    // ... offset/limit handling ...
    
    return ReadOutput{
        Content:    formatted,
        TotalLines: totalLines,
        Truncated:  truncated,
    }, nil
}
```

### Sandbox ReadFile (sandbox.go:59-67)

```go
func (s *Sandbox) ReadFile(name string) ([]byte, error) {
    rel, err := s.Resolve(name)
    if err != nil {
        return nil, err
    }
    return s.root.ReadFile(rel)
}
```

### Key Characteristics

1. **No retry**: Single read attempt, immediate failure
2. **No caching**: Each read hits filesystem
3. **No transient error detection**: All errors treated equally

## Error Scenarios

| Scenario | Current Behavior | Should Retry? |
|---|---|---|
| File not found | Immediate failure | No - file doesn't exist |
| Permission denied | Immediate failure | No - real error |
| Text file busy | Immediate failure | Yes - transient |
| NFS/network issue | Immediate failure | Yes - transient |
| Momentary lock | Immediate failure | Yes - transient |

## Root Cause Analysis

1. **No retry logic in tools**: Tools fail immediately (per codebase-exploration.md:309)
2. **No transient error classification**: All errors treated the same
3. **No retry configuration**: No constants or options for retry behavior

## Investigation Findings

### Agent-Level Retry Pattern (agent/retry.go)

The agent has retry logic for LLM API calls:
```go
type RetryConfig struct {
    MaxRetries   int           // default 3
    InitialDelay time.Duration // default 1s
    MaxDelay     time.Duration // default 30s
}
```

This pattern could be adapted for file reads.

### Transient Error Detection (retry.go:35-82)

```go
func isTransient(err error) bool {
    patterns := []string{
        "429", "rate limit", "too many requests",
        "500", "502", "503", "504", "internal server error",
        "connection reset", "connection refused", "timeout",
    }
    // + errors.Is with Timeout() and Temporary() interfaces
}
```

This pattern could be extended for file system errors.

## Transient File System Errors

### Linux/macOS
- "text file busy" - file being written
- "resource temporarily unavailable" - lock held
- "input/output error" - transient I/O failure
- "no such file or directory" - rare: file being created

### Windows
- "The process cannot access the file" - locked
- "The file is being used by another process"
- "Access is denied" - sometimes transient

## Options for Fix

### Option A: Retry in Sandbox Layer (Low Risk)
- Add retry logic to `Sandbox.ReadFile()`
- Use transient error detection
- Exponential backoff with jitter

### Option B: Retry in Tool Handler (Low Risk)
- Add retry logic to `readHandler()`
- Keep sandbox simple
- More control over retry behavior

### Option C: File Content Cache (Medium Risk)
- Cache file contents in memory
- Serve cached content on read
- Invalidate on write
- See Optimization 6

## Recommendation

**Implement Option A + B**:

1. **Transient error detection**:
   - Add `isTransientReadError()` function
   - Check for known transient patterns
   - Support `Timeout()` and `Temporary()` interfaces

2. **Retry in Sandbox layer**:
   - Add retry constants (max 3, initial delay 50ms)
   - Implement retry loop with backoff
   - Return last error if all retries fail

3. **Complement with file cache** (Optimization 6):
   - Cache recently read files
   - Reduce repeated reads
   - Improve performance

## Implementation Location

- **Primary**: `internal/tools/sandbox.go` (ReadFile method)
- **Secondary**: `internal/tools/read.go` (optional additional handling)

## Implementation Template

```go
const (
    maxReadRetries = 3
    readRetryDelay = 50 * time.Millisecond
)

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

## Test Strategy

1. Unit test for transient error detection
2. Unit test for retry behavior with mocked errors
3. Integration test with actual file system

## Dependencies

- Could use existing `agent/retry.go` patterns
- Independent otherwise

## Risk Assessment

- **Risk Level**: Low
- **Reasoning**: Bounded retries, specific error patterns only
- **Testing**: Can mock transient errors in tests

## Relationship to Other Optimizations

Complements Optimization 6 (File Content Cache):
- Retry handles transient errors
- Cache reduces repeated reads
- Together provide reliability and performance
