# Session Error Fix Prompts

This document catalogs error patterns detected from pi-go session logs and provides actionable fix suggestions.

---

## Pattern: STREAM_ERROR (Priority: HIGH)

- **Count**: 213 occurrences across 1223 session logs
- **Detected in**: All provider implementations (openai.go, anthropic.go, ollama.go)
- **Root Cause**: HTTP/2 stream errors from connection resets, rate limits (429), server errors (5xx), or timeouts
- **Current Behavior**: Error is emitted but NOT retried by `WithRetry` (only catches iterator errors, not `ErrorCode` field)
- **Classification**: IMPLEMENT

### Fix Suggestions

#### 1. Make WithRetry handle STREAM_ERROR responses (IMPLEMENT)

**File**: `internal/agent/retry.go`

**Problem**: `WithRetry` only catches errors from the iterator's error channel (second return value). `STREAM_ERROR` is emitted as a response with `ErrorCode` set in the first return value.

**Solution**: Modify `WithRetry` to also check `resp.ErrorCode == "STREAM_ERROR"` and treat it as a transient error for retry purposes.

```go
// In the retry loop, after receiving an event:
if ev != nil {
    if resp, ok := ev.(*model.LLMResponse); ok && resp.ErrorCode == "STREAM_ERROR" {
        transientErr = fmt.Errorf("stream error: %s", resp.ErrorMessage)
        break
    }
}
```

#### 2. Consider streaming→nonstreaming fallback (NEEDS_REVIEW)

Reference: `research/hermes-agent/tests/test_streaming.py` has a `test_stream_error_falls_back` test.

This requires a design decision — trade-off between resilience and debuggability.

---

## Pattern: tree tool type validation (Priority: HIGH)

- **Count**: 134+ occurrences
- **Error**: `validating root: validating <path>: type: 3 has type "string", wanted "int"`
- **Root Cause**: LLMs sometimes send depth as string `"3"` instead of integer `3`
- **Current Mitigation**: `coercingTool` in `registry.go` has type coercion, but may not fully work
- **Classification**: NEEDS_REVIEW

### Fix Suggestions

#### 1. Verify intProps registration for tree tool (IMPLEMENT)

**File**: `internal/tools/tree.go`

**Check**: Ensure `Depth` field is registered in `intProps` map when creating the coercingTool.

```go
// When creating tree tool in registry.go or core_tools.go:
coercingTool.intProps["root"] = true   // Already may exist
coercingTool.intProps["depth"] = true  // Ensure this is set
```

#### 2. Debug why coercion isn't working (NEEDS_INVESTIGATION)

The coercion code at `registry.go:371-373` converts to float64, but jsonschema-go expects `integer` type (not just `number`). Verify this is the issue.

---

## Pattern: read tool "path escapes from parent" (Priority: MEDIUM)

- **Count**: 67 occurrences
- **Error**: Security error from Go's `os.Root` when path traversal would escape sandbox
- **Classification**: ACCEPT (Security feature working as intended)

### Fix Suggestions

This is Go's sandbox security working correctly. The LLM is attempting to use `../` traversal to access files outside the allowed directory.

#### 1. Improve error message clarity (IMPLEMENT)

**File**: `internal/tools/read.go`

Current error is cryptic. Add context:

```go
return ReadOutput{}, fmt.Errorf("path escapes from parent directory — use paths within the sandbox root, not relative ../ traversal")
```

#### 2. Document path restrictions (EDUCATE)

Add to system instructions or tool descriptions that:
- Use absolute paths from sandbox root
- Don't use `../` to escape sandbox
- Use `file_path`, not `path`

---

## Pattern: read tool missing file_path (Priority: MEDIUM)

- **Count**: 134+ occurrences (missing `file_path` property)
- **Error**: `"file_path is required"` or schema validation failures
- **Root Cause**: LLM sends `path` instead of `file_path`, or missing parameter entirely
- **Classification**: EDUCATE

### Fix Suggestions

#### 1. Improve error message (IMPLEMENT)

**File**: `internal/tools/read.go:86-88`

```go
return ReadOutput{}, fmt.Errorf("file_path is required — use file_path parameter, not 'path'")
```

#### 2. Add more aliases (IMPLEMENT)

**File**: `internal/tools/registry.go` or tool registration

```go
map[string]string{
    "path":     "file_path",   // Already exists
    "filename": "file_path",   // Add
    "file":     "file_path",   // Add
}
```

---

## Pattern: bash/find tool missing required parameters (Priority: MEDIUM)

- **Count**: 123 (bash), 76 (find)
- **Error**: `"command is required"`, `"command is required"`, `"pattern is required"`
- **Root Cause**: `lenientSchema()` strips Required constraints, so LLM sees no required fields
- **Classification**: EDUCATE

### Fix Suggestions

#### 1. Add system prompt instructions (IMPLEMENT - Low Risk)

**File**: `internal/agent/agent.go` or system prompt configuration

Add to SystemInstruction:

```markdown
# Tool Call Rules
- Never call bash tool without a command parameter
- Never call find tool without a pattern parameter
- Always provide required parameters before invoking any tool
- If a tool call fails with "X is required", retry with that parameter included
```

---

## Pattern: subagent tool validation errors (Priority: MEDIUM)

- **Count**: 78+ occurrences
- **Error**: Various schema validation failures (type mismatches, missing properties)
- **Root Cause**: jsonschema-go default constraints too strict for nested schemas
- **Classification**: IMPLEMENT (partially fixed)

### Fix Suggestions

#### 1. Verify Phase 1 fixes are complete (VERIFY)

**File**: `internal/tools/registry.go:62-86`

Check that `relaxSchema()` recursively handles nested objects in `SubagentInput` (TaskItem, ChainItem).

#### 2. Verify Phase 3 fixes are complete (VERIFY)

Check that all numeric fields with defaults have `omitempty`:
- `LSPPositionInput.Line`
- `LSPPositionInput.Column`

#### 3. Add more aliases for common LLM mistakes (IMPLEMENT)

**File**: `internal/tools/subagent.go:96-102`

```go
map[string]string{
    "type":    "agent",   // Already exists
    "prompt":  "task",    // Already exists
    "message": "task",    // Already exists
    "items":   "tasks",   // Already exists
    "steps":   "chain",   // Already exists
    "workers": "tasks",   // Add
    "subtasks": "tasks",  // Add
}
```

---

## Summary Table

| Pattern | Count | Priority | Classification | Action |
|---------|-------|----------|----------------|--------|
| STREAM_ERROR | 213 | HIGH | IMPLEMENT | Fix WithRetry to handle ErrorCode |
| tree type validation | 134 | HIGH | NEEDS_REVIEW | Debug coercion vs verify intProps |
| read path escapes | 67 | MEDIUM | ACCEPT | Improve error message |
| read missing file_path | 134 | MEDIUM | EDUCATE | Add aliases, improve errors |
| bash missing command | 123 | MEDIUM | EDUCATE | Add system prompt rules |
| find missing pattern | 76 | MEDIUM | EDUCATE | Add system prompt rules |
| subagent validation | 78 | MEDIUM | IMPLEMENT | Verify/extend fixes |

---

## Implementation Priority

1. **Immediate (Low Risk, High Impact)**:
   - Add system prompt rules for bash/find required params
   - Improve error messages for read tool
   - Add more aliases for read tool

2. **Next Sprint (Medium Risk)**:
   - Fix `WithRetry` to handle STREAM_ERROR
   - Debug tree tool type coercion

3. **Backlog (Needs Design Decision)**:
   - Streaming→nonstreaming fallback on STREAM_ERROR
   - Deeper tree type coercion investigation

---

## Context for When Each Fix Applies

| Tool | Scenario | Fix |
|------|----------|-----|
| All providers | Stream connection interrupted | Retry on STREAM_ERROR |
| tree | LLM sends `{"depth": "3"}` | Verify intProps coercion |
| read | LLM uses `../` to escape sandbox | ACCEPT - explain to user |
| read | LLM sends `path` instead of `file_path` | Add more aliases |
| bash | LLM calls without command | Add system prompt rules |
| find | LLM calls without pattern | Add system prompt rules |
| subagent | LLM uses wrong param names | Extend alias map |
