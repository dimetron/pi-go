# Session Error Fix Prompts

Analysis of pi-go session logs (1523 files, 240,340 entries).

## Executive Summary

| Priority | Issue | Count | Type | Implementable |
|----------|-------|-------|------|---------------|
| 1 | STREAM_ERROR | 343 | Network/Retry | ⚠️ Partial |
| 2 | Tree/Find path validation | 210 | Schema timing | ✅ Yes |
| 3 | Bash "command is required" | 157 | Missing param | ✅ Yes |
| 4 | Read path escapes sandbox | 110 | Security/worktree | ⚠️ Partial |
| 5 | Read missing file_path | 134 | Schema/aliasing | ✅ Yes |
| 6 | Subagent validation errors | 104 | Schema timing | ✅ Yes |
| 7 | Read content noise | 1000+ | Truncation | ✅ Yes |

---

## Pattern: STREAM_ERROR (343 occurrences)

**Detected in**: All provider implementations (Anthropic, OpenAI, Ollama)

**Root cause**: HTTP stream disconnection during LLM streaming. Errors are embedded in `LLMResponse{ErrorCode: "STREAM_ERROR"}` and yielded as session events.

**Error locations**:
- `internal/provider/anthropic.go:402`
- `internal/provider/openai.go:361`
- `internal/provider/ollama.go:344`

**Current retry logic** (`internal/agent/retry.go`):
- `MaxRetries: 3`, exponential backoff (1s → 2s → 4s → 8s → 30s cap)
- **Limitation**: Retries entire agent session, not just LLM call
- **Critical bug**: `STREAM_ERROR` is yielded as `LLMResponse`, not returned as `error` - so `isTransient()` never sees it

**Fix suggestion**:
1. **Extract error from LLMResponse** in retry loop before checking `isTransient()`
2. Add `STREAM_ERROR` patterns to transient list:
   ```go
   "stream error", "STREAM_ERROR", "stream disconnected"
   ```
3. Consider adding jitter to backoff to avoid thundering herd

**Actionability**: ⚠️ **Partial** - Current retry mechanism can't see these errors. Would need refactor to extract `ErrorCode` from events.

**Priority score**: 7/10 - High volume, but retry already handles most cases (users don't complain).

---

## Pattern: Tree/Find Path Validation (210 occurrences)

**Detected in**: `internal/tools/tree.go`, `internal/tools/find.go`

**Error**: `"type: 3 has type string"` or `"validating root: required: missing properties: [\"pattern\"]"`

**Root cause**: ADK's JSON schema validation runs BEFORE pi-go's coercion (`coerceArgs()`). The schema strictly enforces types.

**Evidence**: 105 tree + 76 find + 29 tree = 210 validation errors

**Fix suggestion**:
1. The schema generation already has `relaxSchema()` but it doesn't handle type coercion
2. **Recommended**: Make `coerceArgs()` run BEFORE ADK's validation by using a custom tool wrapper
3. Or: Add type coercion directly in schema via `$dynamic` types (more complex)

**Actionability**: ✅ **Yes** - Known issue with existing plan at `specs/issues/000-issues-fix/PLAN.md`

**Priority score**: 6/10 - Significant volume, fix is documented.

---

## Pattern: Bash "command is required" (157 occurrences)

**Detected in**: `internal/tools/bash.go:42`

**Error**: `"command is required"` - LLM sends empty `command` field

**Root cause**: LLM sometimes doesn't include `command` in tool call, or sends malformed JSON

**Current behavior**: Returns error immediately, LLM retries with proper command

**Fix suggestion**:
1. **Add default behavior**: If `command` is empty, return helpful message with shell context
2. **Add shell detection**: Default to `bash` if not specified
3. **Improve system prompt**: Remind LLM to always include `command` parameter

**Actionability**: ✅ **Yes** - Simple one-line fix or prompt improvement

**Priority score**: 5/10 - Moderate volume, easy fix.

---

## Pattern: Read Path Escapes Sandbox (110 occurrences)

**Detected in**: `internal/tools/sandbox.go:197, 222`

**Error**: `"path \"../../go.mod\" escapes sandbox root" `

**Root cause**: 
1. Subagent processes run in worktree directories
2. They try to read files relative to parent repo (e.g., `../../go.mod`)
3. Sandbox rejects `..` traversal as security measure

**Evidence**: 67 + 43 = 110 occurrences

**Fix suggestion** (from `specs/issues/000-issues-fix/PLAN.md:Phase 2`):
1. Pass `PI_SANDBOX_ROOT=<repoRoot>` env var to subagents
2. Subagent builds sandbox rooted at repo root, not worktree
3. Paths like `../../go.mod` resolve correctly

**Actionability**: ⚠️ **Partial** - Fix exists on plan but requires coordination between sandbox and subagent initialization

**Priority score**: 5/10 - Moderate volume, moderate complexity.

---

## Pattern: Read Missing file_path (134 occurrences)

**Detected in**: `internal/tools/registry.go`, `internal/tools/read.go`

**Error**: `"validating root: required: missing properties: [\"file_path\"]"`

**Root cause**: LLM sends `path` instead of `file_path`, or omits the field entirely

**Current mitigations**:
- `aliasArgs()` remaps `path` → `file_path` (registry.go:170-178)
- Schema has `Required = nil` (registry.go:52-54)

**Fix suggestion**:
1. Add more aliases: `file`, `filepath`, `fileName`, `filename` → `file_path`
2. Improve coercion for common mistakes
3. Add "last file read" context to help LLM retry correctly

**Actionability**: ✅ **Yes** - Simple alias additions

**Priority score**: 4/10 - Lower priority since aliases already handle most cases.

---

## Pattern: Subagent Validation Errors (104 occurrences)

**Detected in**: `internal/tools/subagent.go`

**Error**: `"validating root: validating path: type: [{\"agent\": \"explore\""` (malformed JSON in error)

**Root cause**: 
1. LLM sends malformed subagent parameters
2. JSON schema validation fails before coercion runs
3. Error message includes truncated input in the error

**Current aliases**:
- `type` → `agent`
- `prompt` → `task`
- `message` → `task`
- `items` → `tasks`
- `steps` → `chain`

**Fix suggestion**:
1. Add more aliases for LLM parameter confusion
2. Improve error messages to show corrected parameters
3. Consider pre-validating JSON before schema check

**Actionability**: ✅ **Yes** - Alias improvements and better error context

**Priority score**: 4/10 - Lower priority, aliases already help.

---

## Pattern: Read Content Noise (1000+ occurrences)

**Detected in**: `internal/tools/read.go`

**Error**: Actually not an error - these are successful reads with truncated content

**Example**: `map[content: 1 package tools 2 3 import ( 4 "context" 5 "fmt" 6 "strings" 7 "syn...`

**Root cause**: 
1. Large file reads get truncated in logs
2. Content appears as `map[content: ... truncated ...]`
3. This is logged as "error" but is actually normal behavior

**Fix suggestion**:
1. Truncate in logging only, not in output to LLM
2. Add proper truncation indicator: `[truncated, N lines]`
3. Add `max_display_lines` parameter to read tool

**Actionability**: ✅ **Yes** - Logging improvement

**Priority score**: 3/10 - Low priority, not actual errors.

---

## Actionable Fixes Checklist

### High Priority (Do First)
- [ ] **STREAM_ERROR retry improvement** - Extract error code from LLMResponse in retry loop
- [ ] **Path validation timing** - Ensure coercion runs before ADK validation (see PLAN.md)

### Medium Priority
- [ ] **Bash default shell** - Default to bash when command empty
- [ ] **Sandbox root for subagents** - Pass PI_SANDBOX_ROOT env var
- [ ] **More file_path aliases** - Add file, filepath, fileName

### Low Priority
- [ ] **Subagent aliases** - Add more parameter aliases
- [ ] **Read truncation logging** - Improve truncation display

---

## Research Notes

### Files to Modify

| File | Changes |
|------|---------|
| `internal/agent/retry.go` | Extract ErrorCode from LLMResponse before isTransient check |
| `internal/tools/registry.go` | Add more aliases, improve coercion |
| `internal/tools/sandbox.go` | Add PI_SANDBOX_ROOT handling |
| `internal/tools/bash.go` | Default shell behavior |
| `internal/tools/subagent.go` | Add more aliases |

### Existing Plans

- `specs/issues/000-issues-fix/PLAN.md` - Detailed fix plan for path validation issues
- `specs/issues/001-session-errors/PROMPT.md` - Previous analysis (may have overlap)

### Test Commands

```bash
# Run session log analysis
bash .pi-go/skills/pi-check-session-logs/analyze.sh

# Run tool tests
go test ./internal/tools/... -v

# Run agent retry tests
go test ./internal/agent/... -v -run Retry
```
