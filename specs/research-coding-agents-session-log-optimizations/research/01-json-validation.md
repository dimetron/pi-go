# Research: JSON Validation Errors (Backslash Escaping in File Paths)

## Problem Statement

When LLMs generate tool parameters with file paths containing backslashes (especially on Windows), the JSON escaping can be malformed. For example, `C:\Users\test` might be sent with improper escaping like `C:\Users\test` instead of `C:\\Users\\test`.

## Current State

### System Instruction (agent.go:36-112)

The `SystemInstruction` constant is an 8-section system prompt covering:
- Role definition (lines 36-39)
- Codebase exploration strategy (lines 41-56)
- Coding task workflow (lines 57-76)
- Context management (lines 77-84)
- Multi-step tasks (lines 85-91)
- Parallel execution (lines 92-99)
- Internal tools (lines 100-103)
- Subagents (lines 104-112)

**No section exists for JSON string escaping or file path formatting.**

### Tool Coercion (registry.go:94-185)

The `coercingTool` handles type coercion for tool arguments:

```go
type coercingTool struct {
    tool.Tool
    intProps  map[string]bool  // fields that should be coerced from string to int
    boolProps map[string]bool  // fields that should be coerced from string to bool
}
```

**Current coercion logic (lines 167-185):**
- Integer fields: Parse via `strconv.ParseInt()` → convert to `float64`
- Boolean fields: Parse via `strconv.ParseBool()`
- **No backslash unescaping or path normalization**

### Lenient Schema (registry.go:42-52)

The schema is intentionally lenient:
```go
schema.AdditionalProperties = &jsonschema.Schema{}  // Allows extra props
```

This means malformed JSON may pass schema validation.

## Root Cause Analysis

1. **LLM JSON generation**: LLMs may not properly escape backslashes when generating JSON strings
2. **No validation layer**: The coercion logic only handles type conversion, not string content
3. **Platform differences**: Windows paths have more backslashes, increasing failure probability

## Investigation Findings

### Tool Input Validation (edit.go:40-48)
```go
// Current validation only checks non-empty
if input.FilePath == "" {
    return EditOutput{}, fmt.Errorf("file_path is required")
}
// No validation for escaped characters
```

### Read Handler (read.go:42-44)
```go
if input.FilePath == "" {
    return ReadOutput{}, fmt.Errorf("file_path is required")
}
// No path normalization or escaping validation
```

## Options for Fix

### Option A: System Prompt Instructions (Low Risk)
- Add JSON escaping instructions to `SystemInstruction`
- Pros: Zero code changes, zero runtime overhead
- Cons: Relies on LLM following instructions

### Option B: Defensive Path Unescaping (Medium Risk)
- Add pre-processing in tool handlers to normalize paths
- Unescape common patterns before use
- Pros: Robust defense against malformed input
- Cons: May mask real errors, adds complexity

### Option C: Validation + Helpful Errors (Low Risk)
- Add validation in `coercingTool` or tool handlers
- Return clear error messages with expected format
- Pros: Self-documenting, helps debugging
- Cons: LLM still needs to retry

## Recommendation

**Implement Option A + C**: 
1. Add JSON escaping guidance to `SystemInstruction`
2. Add defensive path normalization in tool handlers
3. Add helpful error messages explaining the expected format

## Implementation Location

- **Primary**: `internal/agent/agent.go` (lines 36-112, SystemInstruction)
- **Secondary**: `internal/tools/registry.go` (coercion logic)
- **Tertiary**: Individual tool handlers (edit.go, read.go, etc.)

## Test Strategy

1. Unit tests for path unescaping logic
2. Integration tests with malformed JSON input
3. Manual testing with Windows-style paths

## Dependencies

- None - can be implemented independently

## Risk Assessment

- **Risk Level**: Low
- **Reasoning**: Changes are additive and defensive
- **Testing**: Can verify with unit tests before deployment
