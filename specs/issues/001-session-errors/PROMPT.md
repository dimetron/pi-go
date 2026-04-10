# Session Error Fix Prompts

Analysis of last 24h session logs (307 files, 9594 entries).

## Summary

| Error Type | Count | Priority | Status |
|------------|-------|----------|--------|
| STREAM_ERROR | 182 | **HIGH** | Bug - needs fix |
| "command is required" | 34 | LOW | Expected behavior |
| "file_path is required" / "pattern is required" | 25 | MEDIUM | Tool improvements |
| Edit tool LSP diagnostics | 22 | MEDIUM | Architecture gap |
| "read X: is a directory" | 5 | LOW | Tool improvement |

---

## Pattern 1: STREAM_ERROR (182 occurrences) — **HIGH PRIORITY**

### Detected in
- `internal/provider/anthropic.go:402`
- `internal/provider/openai.go:361`
- `internal/provider/ollama.go:344`

### Root Cause
Network stream interruptions are yielded as `LLMResponse{ErrorCode: "STREAM_ERROR"}` with `nil` error. The retry logic in `internal/agent/retry.go:103` checks `isTransient(err)` which never sees STREAM_ERROR because the error is embedded in the event, not returned as the second yield value.

### Current Broken Code
```go
// All three providers (identical pattern):
if err := stream.Err(); err != nil {
    if ctx.Err() == context.Canceled {
        return
    }
    _ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
    return
}
```

### Fix Suggestion: Option A (Recommended)

**1. Change providers to yield error instead of LLMResponse:**

`internal/provider/anthropic.go:398-404`:
```go
if err := stream.Err(); err != nil {
    if ctx.Err() == context.Canceled {
        return
    }
    yield(nil, fmt.Errorf("stream error: %w", err))
    return
}
```

Apply same change to:
- `internal/provider/openai.go:357-363`
- `internal/provider/ollama.go:340-346`

**2. Add stream error patterns to transient detection:**

`internal/agent/retry.go:43-61` — Add to `transientPatterns`:
```go
"stream error",   // HTTP/2 stream errors
"STREAM_ERROR",   // Error code prefix
```

### Fix Suggestion: Option B (Alternative - Less Invasive)

Modify retry loop to extract errors from `ev.LLMResponse.ErrorCode`:

`internal/agent/retry.go:102-113`:
```go
for ev, err := range runFn() {
    // Extract error from LLMResponse if present (STREAM_ERROR, API_ERROR, etc.)
    if ev != nil && ev.LLMResponse != nil && ev.LLMResponse.ErrorCode != "" {
        err = fmt.Errorf("%s: %s", ev.LLMResponse.ErrorCode, ev.LLMResponse.ErrorMessage)
    }
    if err != nil && isTransient(err) {
        transientErr = err
        break
    }
    // ... rest of loop
}
```

### Implementation Priority
**IMPLEMENT** — This is a real bug causing 182 errors that should auto-retry with exponential backoff (1s → 2s → 4s).

---

## Pattern 2: "command is required" (34 occurrences) — **LOW PRIORITY**

### Detected in
- `internal/tools/bash.go:40-43`

### Root Cause
**Expected behavior** — LLM occasionally sends empty `command` field. The tool correctly rejects it and returns an error. The LLM retries with proper command.

### Evidence
Test at `internal/tools/tools_test.go:270-275` confirms this is intentional validation.

### Fix Suggestion
**REJECT** — Not a bug. The system recovers automatically.

Optional improvement (nice-to-have):
- Return a friendlier error message with shell usage hints instead of just "command is required"

---

## Pattern 3: "file_path is required" / "pattern is required" (25 occurrences) — **MEDIUM PRIORITY**

### Detected in
| File | Line | Error |
|------|------|-------|
| `internal/tools/read.go` | 87 | `file_path is required` |
| `internal/tools/edit.go` | 47 | `file_path is required` |
| `internal/tools/write.go` | 35 | `file_path is required` |
| `internal/tools/grep.go` | 119 | `pattern is required` |
| `internal/tools/find.go` | 40 | `pattern is required` |

### Root Cause
1. Schema intentionally removes required constraints (`internal/tools/registry.go:52-54`)
2. No parameter aliases for grep/find tools
3. Inconsistent tool descriptions

### Fix Suggestions

**1. Add parameter aliases to grep and find:**

`internal/tools/grep.go:112`:
```go
return newTool("grep", "...", func(_ tool.Context, input GrepInput) (GrepOutput, error) {
    return grepHandler(sb, input)
}, map[string]string{"query": "pattern", "regex": "pattern"})
```

`internal/tools/find.go:33`:
```go
return newTool("find", "...", func(_ tool.Context, input FindInput) (FindOutput, error) {
    return findHandler(sb, input)
}, map[string]string{"glob": "pattern"})
```

**2. Improve error messages to include hints:**

```go
// Before
return ReadOutput{}, fmt.Errorf("file_path is required")

// After
return ReadOutput{}, fmt.Errorf("validation error: file_path is required (got empty string)")
```

**3. Standardize tool descriptions with Required/Optional sections**

### Implementation Priority
**CONSIDER** — These are mostly LLM mistakes that auto-recover. Quick wins: add aliases and improve error messages.

---

## Pattern 4: Edit Tool LSP Diagnostics Errors (22 occurrences) — **MEDIUM PRIORITY**

### Detected in
- `internal/tools/edit.go:41-128` — No pre-edit validation
- `internal/lsp/hooks.go:31-82` — Diagnostics collected AFTER edit completes

### Root Cause
Architecture gap: edits are committed to disk BEFORE diagnostics arrive. No pre-edit validation exists.

### Errors Found
- `ping.go:1: expected declaration, found '}'` — Syntax error after malformed edit
- `sandbox.go:1: undefined: GitignorePattern` — Import/type corruption

### Fix Suggestions

**1. Add pre-edit validation (Most impactful):**

`internal/tools/edit.go` — Before `performEdit()`:
```go
// Check for existing errors before editing
if mgr != nil {
    if hasErrors := checkFileErrors(input.FilePath, mgr); hasErrors {
        return EditOutput{}, fmt.Errorf("file already has LSP errors; fix them before making additional edits")
    }
}
```

**2. Surface diagnostics as tool warning (Low effort):**

Add to edit tool description:
```
NOTE: After the edit, LSP diagnostics are collected automatically.
If new errors appear (e.g., "undefined: X"), the file may have been corrupted.
Use the read tool to verify, or use lsp-diagnostics for detailed errors.
```

**3. Add "Dry Run" mode for edit:**
```go
type EditInput struct {
    DryRun bool `json:"dry_run,omitempty"`  // Preview without writing
    // ...existing fields...
}
```

**4. Increase diagnostics delay:**

`internal/lsp/hooks.go:22`:
```go
const DiagnosticsDelay = 3 * time.Second  // Increase from 2s
```

### Implementation Priority
**CONSIDER** — High value for code quality but requires architecture changes. Start with warning in description, then pre-edit validation.

---

## Pattern 5: "read X: is a directory" (5 occurrences) — **LOW PRIORITY**

### Detected in
- `internal/tools/read.go:81-116`

### Root Cause
No directory detection before calling `sb.ReadFile()`. Infrastructure exists (`sb.Stat()`) but isn't used.

### Best Practice Example
`internal/tools/grep.go:148-162` already demonstrates correct pattern:
```go
info, err := sb.Stat(searchPath)
if err != nil {
    return GrepOutput{}, fmt.Errorf("path not found: %w", err)
}
if info.IsDir() {
    // handle directory case
}
```

### Fix Suggestion

**Option A (Recommended - Minimal):**

`internal/tools/read.go:86-88` — Add after empty check:
```go
info, err := sb.Stat(input.FilePath)
if err != nil {
    return ReadOutput{}, fmt.Errorf("reading file: %w", err)
}
if info.IsDir() {
    return ReadOutput{}, fmt.Errorf("cannot read directory %q — use ls or tree tool to list contents", input.FilePath)
}
```

**Option B (Auto-list directory contents):**

Detect directory and return listing format:
```go
if info.IsDir() {
    entries, err := sb.ReadDir(input.FilePath)
    // Return formatted directory listing
}
```

### Implementation Priority
**CONSIDER** — Low volume but improves UX. Start with better error message.

---

## Implementation Checklist

| Issue | Priority | Recommendation | Estimated Effort |
|-------|----------|----------------|------------------|
| STREAM_ERROR retry | HIGH | **IMPLEMENT** Option A or B | 30 min |
| Parameter aliases | MEDIUM | Implement grep/find aliases | 15 min |
| Better error messages | MEDIUM | Add hints to validation errors | 10 min |
| Edit pre-validation | MEDIUM | Add checkFileErrors before edit | 1 hour |
| Edit tool warning | LOW | Add note to description | 5 min |
| Read directory handling | LOW | Add Stat check + hint | 10 min |
| "command is required" | LOW | **REJECT** (expected behavior) | N/A |

---

## Context for Fix Application

### STREAM_ERROR Fix
- **Tools affected**: All streaming providers (Anthropic, OpenAI, Ollama)
- **Scenarios**: Network interruptions, server disconnects, rate limiting
- **Trigger condition**: `stream.Err()` returns non-nil, context not canceled
- **Expected outcome**: Automatic retry with backoff instead of error propagation

### Validation Error Fixes
- **Tools affected**: read, write, edit, grep, find
- **Scenarios**: LLM sends empty or malformed parameters
- **Trigger condition**: Empty string passed to required field
- **Expected outcome**: Better error messages help LLM correct itself faster

### Edit Tool Fixes
- **Tools affected**: edit (write tool indirectly)
- **Scenarios**: Edit corrupts file structure, syntax errors introduced
- **Trigger condition**: File has errors after edit completes
- **Expected outcome**: Warning before edit if file already has errors, better diagnostics surfacing
