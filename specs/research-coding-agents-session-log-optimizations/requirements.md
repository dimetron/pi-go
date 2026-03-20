# Requirements

## Questions & Answers

### Q1: What is the primary objective for this task?
**A**: Conduct deeper research into the 6 optimization areas identified in `research/coding-agents/session-log-optimizations.md`.

---

## Research Findings Summary

### Optimization 1: JSON Validation Errors (Low Complexity)

**Current State:**
- No JSON escaping guidance in `SystemInstruction` (agent.go:36-112)
- `coercingTool` only handles type coercion (registry.go:167-185)
- Lenient schema allows malformed JSON to pass validation

**Recommended Approach:**
- Add JSON escaping instructions to `SystemInstruction`
- Add defensive path normalization in tool handlers
- Add helpful error messages explaining expected format

**Files to Modify:**
- `internal/agent/agent.go` - Add guidance to SystemInstruction
- `internal/tools/registry.go` - Optional defensive normalization

---

### Optimization 2: old_string Not Found (Medium Complexity)

**Current State:**
- `editHandler` reads file once, returns error on 0 matches
- No retry logic, no fuzzy matching
- Read-modify-write is three separate operations (no atomicity)

**Recommended Approach:**
- Re-read file once when `old_string` not found
- If still not found, return error with:
  - The old_string that wasn't found
  - Preview of current file content
  - Suggestions for fixing

**Files to Modify:**
- `internal/tools/edit.go` - Add retry + enhanced error messages

---

### Optimization 3: Path Escapes Security (Low Complexity - Documentation)

**Current State:**
- Intentional security via `os.Root` (Go 1.24+)
- Blocks access to files outside sandbox directory
- No godoc or inline comments explaining security rationale

**Recommended Approach:**
- Add comprehensive godoc to `Resolve()` and `Sandbox` struct
- Add inline comments explaining security model
- Update README or create ARCHITECTURE.md security section

**Files to Modify:**
- `internal/tools/sandbox.go` - Add documentation
- `ARCHITECTURE.md` or `README.md` - Add security section

---

### Optimization 4: File Not Found Retry (Medium Complexity)

**Current State:**
- `readHandler` and `Sandbox.ReadFile` have no retry logic
- All errors treated equally (immediate failure)
- No transient error classification

**Recommended Approach:**
- Add transient error detection function (`isTransientReadError`)
- Implement bounded retry (max 3 attempts) with backoff
- Retry only for specific transient error patterns

**Files to Modify:**
- `internal/tools/sandbox.go` - Add retry to ReadFile
- `internal/tools/read.go` - Optional additional handling

---

### Optimization 5: Test Panics (Medium-High Complexity)

**Current State:**
- Compactor has panic recovery (compactor.go:156-172)
- TUI `runAgentLoop` has NO panic recovery
- Skill-load goroutines have explicit panic recovery

**Recommended Approach:**
- Add panic recovery to `runAgentLoop` in TUI
- Send error via channel on panic (consistent with existing pattern)
- Include stack traces in panic logs for debugging

**Files to Modify:**
- `internal/tui/tui.go` - Add recovery to runAgentLoop
- `internal/tools/*.go` - Optional for tool handlers

---

### Optimization 6: Duplicate File Reads (High Complexity)

**Current State:**
- No file content cache exists
- `readHandler` reads files fresh on every call
- No session-level tracking of file reads

**Recommended Approach:**
- Implement in-memory file content cache (Option A - priority)
- Track file reads in session state (Option B - future)
- LRU eviction policy with mutex protection
- Invalidate on write/edit operations

**Files to Modify:**
- `internal/tools/read.go` - Add cache integration
- `internal/tools/cache.go` - New file for cache implementation
- `internal/session/store.go` - Optional session tracking

---

## Key Patterns Discovered

### Panic Recovery (compactor.go:159-163)
```go
func runStage(...) (result string) {
    result = input
    defer func() {
        if r := recover(); r != nil {
            log.Printf("stage %q panicked: %v", name, r)
            result = input
        }
    }()
}
```

### Exponential Backoff Retry (agent/retry.go)
- Initial delay: 1s, Max delay: 30s
- Transient error detection via patterns + interfaces
- Partial results block retry (safety)

### Channel-Based Goroutine Errors (tui.go)
- `chan agentMsg` (buffered 64)
- `defer close()` on exit
- `agentDoneMsg{err}` for error propagation

---

## Recommended Implementation Order

1. **Phase 1 - Quick Wins** (~1-2 hours):
   - Optimization 1: JSON validation instructions
   - Optimization 3: Security documentation

2. **Phase 2 - Improved Reliability** (~2-3 hours):
   - Optimization 2: Edit tool retry logic
   - Optimization 4: File read retry logic

3. **Phase 3 - Stability** (~3-4 hours):
   - Optimization 5: Test panic fixes

4. **Phase 4 - Performance** (~4-6 hours):
   - Optimization 6: Context preservation

**Total estimated time: 10-15 hours**

---

## Dependencies Between Optimizations

```
Optimization 3 (Security Documentation)
    └── Unblocks clearer error messages for 1, 2, 4

Optimization 6 (File Content Cache)
    └── Enables Optimization 4 (read retry with cache)

Optimization 5 (Panic Recovery)
    └── Independent - defensive throughout

Optimization 2 (Edit Retry)
    └── Depends on sandbox.go Resolve (no external deps)

Optimization 1 (JSON Instructions)
    └── Depends on agent.go SystemInstruction (no external deps)
```

---

## Acceptance Criteria

### Phase 1
- [ ] System prompt includes JSON escaping guidance
- [ ] Sandbox godoc explains security model
- [ ] Error messages include sandbox path context

### Phase 2
- [ ] Edit tool re-reads file once on "not found"
- [ ] Edit error includes file preview and suggestions
- [ ] Read tool retries transient errors (max 3)
- [ ] Retry uses exponential backoff

### Phase 3
- [ ] TUI goroutine has panic recovery
- [ ] Panic logs include stack trace
- [ ] Tests pass with race detector

### Phase 4
- [ ] File content cache implemented
- [ ] Cache invalidated on write/edit
- [ ] Session tracks file reads
