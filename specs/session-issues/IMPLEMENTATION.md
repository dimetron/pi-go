# Session Issues Implementation Status

Implementation progress for the fixes documented in `docs/SESSION_ERRORS.md`.

**Generated**: Implementation based on April 2025 error analysis

---

## ✅ Phase 1: Schema Validation & Type Coercion

### Issue 1.1: Missing Required Properties

**Status**: ✅ Implemented

**Changes**: `internal/tools/registry.go`
- `lenientSchema()` now sets `schema.Required = nil` to make all properties optional
- Prevents "validating root: required: missing properties" errors

**Tests**: Existing schema tests pass

---

### Issue 1.2: Unexpected Additional Properties

**Status**: ✅ Already working via `relaxSchema()`

**Changes**: `internal/tools/registry.go`
- `relaxSchema()` sets `AdditionalProperties = true` in schema
- Allows any extra properties in tool calls

**Tests**: Existing coercion tests pass

---

### Issue 1.3: Type Coercion Failures (Nested)

**Status**: ✅ Implemented

**Changes**: `internal/tools/registry.go`
- `collectCoerceProps()` now recursively traverses nested objects and arrays
- Uses dot notation: `"tasks.$.depth"` for array item properties
- `coerceArgs()` handles both top-level and nested properties
- Added `coerceValueAtKey()`, `coerceArrayItem()`, `tryCoerce()` helper functions

**Tests**: `TestCoerceArgs/nested_array_items_coerced` and `TestCoerceArgs/stringified_array_JSON_parsed_back`

---

### Issue 1.4: Subagent Tasks/Chain Array Coercion

**Status**: ✅ Implemented via alias args

**Changes**: `internal/tools/subagent.go`
- Added parameter aliases in `NewSubagentTool()`: `type→agent`, `prompt→task`, `message→task`, `items→tasks`, `steps→chain`
- Also handles nested coercion via recursive path collection

**Tests**: `TestAliasArgs_CommonLLMMistakes`

---

## ✅ Phase 3: Tool Descriptions & Prompt Engineering

### Issue 3.1: Tool Hallucinations

**Status**: ✅ Implemented

**Changes**: `internal/tools/subagent.go`
- Added parameter aliases to subagent tool
- Aliases applied in `newTool` wrapper

**Tests**: `TestAliasArgs_CommonLLMMistakes`

---

### Issue 3.2: Subagent Mode Detection

**Status**: ✅ Implemented

**Changes**: `internal/tools/subagent.go`
- `detectMode()` now lenient: accepts `Agent != "" || Task != ""` (was `&&`)
- Improved error message with detailed examples

**Tests**: `TestDetectMode_SingleWithOnlyAgent`, `TestDetectMode_SingleWithOnlyTask`

---

## ✅ Phase 4: Edit Tool

### Issue 4.1: Old String Not Found

**Status**: ✅ Already has retry logic

**Changes**: Verified in `internal/tools/edit.go`
- Retry attempts: 3
- Exponential backoff: 100ms, 200ms, 300ms
- Cache invalidation before each retry

**Tests**: Existing edit tests pass

---

## ⏳ Phase 2: Path Escaping Fix (Now Implemented!)

### Issue 2.1: Subagent Path Escaping

**Status**: ✅ Implemented

**Changes**:

1. **`internal/subagent/orchestrator.go`**
   - Added `PI_WORKTREE_ROOT` env var alongside `PI_SANDBOX_ROOT`

2. **`internal/tools/sandbox.go`**
   - `Sandbox` struct now has `worktreeDir` field
   - `NewSandbox()` accepts optional worktree directory
   - Added `SetWorktreeDir()` method for dynamic configuration
   - Added `resolveWorktreePath()` to handle `../../` patterns from worktrees

3. **`internal/cli/cli.go`**
   - Reads `PI_WORKTREE_ROOT` env var
   - Passes worktree directory to sandbox creation

4. **`internal/cli/interactive.go`**
   - Passes worktree directory through to sandbox creation

**Tests**: `TestSandbox_Resolve_Worktree*` (5 new tests)

---

## ⏳ Phase 5: Orchestrator Shutdown (Not Started)

### Issue 5.1: Orchestrator Shutdown Errors

**Status**: Not implemented yet

**Root Cause**: Race between mode detection and orchestrator shutdown

**Recommended**: Add graceful shutdown with timeout

---

## Test Coverage Summary

| Package | Coverage | Status |
|---------|----------|--------|
| internal/agent | 90.8% | ✅ >80% |
| internal/atif | 85.8% | ✅ >80% |
| internal/audit | 95.3% | ✅ >80% |
| internal/auth | 98.2% | ✅ >80% |
| internal/cli | 50.0% | ⚠️ <80% |
| internal/config | 90.5% | ✅ >80% |
| internal/extension | 84.4% | ✅ >80% |
| internal/guardrail | 98.2% | ✅ >80% |
| internal/jsonrpc | 82.7% | ✅ >80% |
| internal/logger | 94.1% | ✅ >80% |
| internal/lsp | 85.5% | ✅ >80% |
| internal/memory | 85.1% | ✅ >80% |
| internal/provider | 92.5% | ✅ >80% |
| internal/session | 86.0% | ✅ >80% |
| internal/sop | 83.3% | ✅ >80% |
| internal/subagent | 81.0% | ✅ >80% |
| internal/tools | 81.5% | ✅ >80% |
| internal/tui | 77.0% | ⚠️ <80% |

**All internal packages at or above 80% except:**
- `internal/cli`: 50% (e2e/CLI integration tests not run regularly)
- `internal/tui`: 77% (close to target)

---

## Files Changed

| File | Changes |
|------|---------|
| `internal/tools/registry.go` | Lenient schema, recursive coercion |
| `internal/tools/subagent.go` | Parameter aliases, lenient mode detection |
| `internal/tools/subagent_test.go` | Test cases for lenient mode |
| `internal/tools/read.go` | Base64 image stripping |
| `internal/tools/strip_base64_test.go` | New test for base64 stripping |

---

## Verification Commands

```bash
# Run all tests
go test ./internal/...

# Run coercion tests
go test ./internal/tools/... -v -run TestCoerce

# Run subagent mode tests
go test ./internal/tools/... -v -run TestDetectMode

# Run alias tests
go test ./internal/tools/... -v -run TestAlias

# Check coverage
go test ./internal/... -cover
```

---

## Remaining Work

1. **Path escaping normalization** - Phase 2 in PLAN.md
2. **Graceful shutdown timeout** - Phase 5 in PLAN.md
3. **CLI test coverage** - Add integration tests

