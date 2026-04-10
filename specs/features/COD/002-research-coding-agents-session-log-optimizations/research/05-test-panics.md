# Research: Test Panics (Investigate and Fix Root Causes)

## Problem Statement

Tests have shown panics in various components. The compactor already has panic recovery, but other components may not. Need to investigate and add defensive recovery where needed.

## Current State

### Compactor Panic Recovery (compactor.go:156-172)

The compactor has robust panic recovery:
```go
func runStage(input string, techniques *[]string, name string, fn func(string) (string, bool)) (result string) {
    result = input
    defer func() {
        if r := recover(); r != nil {
            log.Printf("compactor: stage %q panicked: %v", name, r)
            result = input  // fall back to original
        }
    }()
    // ... work ...
    return output
}
```

### Key Characteristics

1. **Named return value**: `result` is modified in defer
2. **Graceful degradation**: Returns original input on panic
3. **Logging**: Logs panic with stage name
4. **No re-throw**: Never propagates panic up stack

### TUI Goroutine Pattern (tui.go:1365-1412)

```go
func (m *model) runAgentLoop(prompt string) {
    defer close(m.agentCh)  // closes channel on exit
    for ev, err := range m.cfg.Agent.RunStreaming(...) {
        if err != nil {
            m.agentCh <- agentDoneMsg{err: err}
            return
        }
        // ... dispatch events ...
    }
}
```

### Key Characteristics

1. **No panic recovery**: If `Agent.RunStreaming` panics, goroutine dies
2. **Channel closes silently**: Main loop won't receive error
3. **Skill-load goroutines have recovery** (tui.go:616, 645):
```go
defer func() {
    if r := recover(); r != nil {
        ch <- agentDoneMsg{err: fmt.Errorf("skill load panicked: %v", r)}
    }
}()
```

## Panic Scenarios

| Component | Has Recovery | Risk |
|---|---|---|
| Compactor stages | Yes | Low |
| TUI runAgentLoop | No | Medium |
| Skill load goroutines | Yes | Low |
| Tool handlers | No | Low-Medium |
| Session store | No | Low |

## Root Cause Analysis

1. **TUI goroutine**: No panic recovery around `Agent.RunStreaming`
2. **Tool handlers**: String operations could panic on malformed input
3. **Session store**: File I/O operations could panic
4. **Agent/subagent**: Could panic on edge cases

## Investigation Findings

### Agent Retry Pattern (agent/retry.go)

The agent has retry logic with error classification:
- Transient errors detected via patterns
- Exponential backoff for retries
- Safety: partial results block retry

### Panic Recovery Template

```go
func safeOperation(input T) (result T) {
    result = input
    defer func() {
        if r := recover(); r != nil {
            log.Printf("operation %q panicked: %v", name, r)
            // result already set to input (graceful degradation)
        }
    }()
    // ... work ...
    return processed
}
```

### Structured Logging

The codebase uses `log` package for logging:
```go
log.Printf("compactor: stage %q panicked: %v", name, r)
```

## Options for Fix

### Option A: Add Recovery to TUI Goroutine (Low Risk)
- Wrap `runAgentLoop` in panic recovery
- Send error via channel on panic
- Log stack trace for debugging

### Option B: Add Recovery to Tool Handlers (Low Risk)
- Wrap tool handler calls in recovery
- Return error instead of panic
- Low overhead, high safety

### Option C: Structured Panic Logging (Medium Risk)
- Include stack traces in panic logs
- Add panic counter metrics
- Track panic frequency

## Recommendation

**Implement Option A + B + C**:

1. **TUI goroutine recovery**:
```go
func (m *model) runAgentLoop(prompt string) {
    defer close(m.agentCh)
    defer func() {
        if r := recover(); r != nil {
            log.Printf("agent loop panicked: %v\n%s", r, debug.Stack())
            m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent panic: %v", r)}
        }
    }()
    // ... existing code ...
}
```

2. **Tool handler recovery** (optional, if handlers are external-facing)

3. **Structured panic logging**:
- Include `debug.Stack()` in logs
- Add panic metrics to compactor metrics

## Implementation Location

- **Primary**: `internal/tui/tui.go` (runAgentLoop)
- **Secondary**: `internal/tools/*.go` (optional)
- **Supporting**: `internal/tools/compactor_metrics.go` (metrics)

## Test Strategy

1. Run tests with race detector: `go test -race ./...`
2. Add test that verifies no panics in common paths
3. Add test for panic recovery behavior

## Dependencies

- `runtime/debug` for stack traces
- Existing `log` package for logging

## Risk Assessment

- **Risk Level**: Low-Medium
- **Reasoning**: Recovery is defensive, changes error propagation slightly
- **Testing**: Can test with controlled panics

## Common Panic Patterns

### Index Out of Bounds
- Accessing slice beyond length
- String operations on malformed input

### Nil Pointer Dereference
- Calling method on nil receiver
- Accessing field on nil struct

### Type Assertion Panic
- Failed type assertion without ok check
- Interface with nil concrete value

## Prevention Strategies

1. **Add nil checks**: Validate inputs before use
2. **Use ok idiom**: Check type assertions
3. **Bounds checking**: Validate indices
4. **Recovery**: Catch unexpected panics

## Relationship to Other Optimizations

Independent of other optimizations - defensive improvement throughout.
