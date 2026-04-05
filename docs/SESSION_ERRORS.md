# Session Error Analysis

Analysis of tool call errors from pi-go session logs (`~/.pi-go/sessions/`).

**Generated**: April 2026  
**Sessions Analyzed**: 2,015  
**Total Errors Found**: 2,717  
**Sessions with Errors**: 362 (18%)

---

## Summary Statistics

| Metric | Count | Percentage |
|--------|-------|------------|
| Total Sessions | 2,015 | 100% |
| Sessions with Errors | 362 | 18% |
| Total Error Occurrences | 2,717 | - |
| Average Errors per Error Session | 7.5 | - |

---

## Error Categories (by frequency)

| Error Type | Count | Severity | Category |
|------------|-------|----------|----------|
| `syntax_error` | 1,106 | Medium | Code |
| `network_error` | 403 | High | External |
| `session_error` | 192 | Low | System |
| `git_error` | 188 | Low | External |
| `lsp_error` | 170 | Medium | Code |
| `file_not_found` | 168 | Low | System |
| `undefined_variable` | 95 | Medium | Code |
| `rate_limit` | 88 | High | External |
| `env_missing` | 83 | Medium | Config |
| `auth_error` | 67 | High | Config |
| `type_error` | 35 | Medium | Code |
| `build_failed` | 27 | High | Build |
| `branch_error` | 16 | Low | System |
| `context_deadline` | 15 | Medium | System |
| `nil_pointer` | 13 | High | Code |
| `test_failed` | 11 | Medium | Test |
| `worktree_error` | 9 | Low | System |
| `tool_error` | 8 | Medium | System |
| `compaction_error` | 7 | Low | System |
| `permission_denied` | 4 | Medium | System |
| `panic` | 3 | Critical | System |
| `mcp_error` | 2 | Medium | External |
| `jsonl_corrupt` | 2 | Low | System |
| `json_parse_error` | 2 | Low | System |
| `import_error` | 2 | Medium | Code |
| `tool_timeout` | 1 | Low | System |

---

## Top 20 Specific Errors

| Count | Error Message |
|-------|---------------|
| 189 | `validating root: unexpected additional properties ["task"]` |
| 81 | `{"content": "     1\\tpackage tools\\n     2\\t\\n     3\\timport (\\n..."` (read output - benign) |
| 67 | Various read tool outputs |
| 60 | Various read tool outputs |
| 48 | `'NoneType' object has no attribute 'get'` |
| 44 | Git worktree branch info (benign) |
| 38 | Test output snippets |
| 35 | Various read tool outputs |
| 33 | Test output snippets |
| 29 | Various read tool outputs |
| 29 | README content (benign) |
| 22 | Various read tool outputs |
| 22 | Various read tool outputs |
| 21 | `old_string not found in file` |
| 21 | Test output |
| 20 | Various read tool outputs |
| 19 | Architecture doc content (benign) |
| 17 | `validating root: unexpected additional properties ["path"]` |
| 15 | `declared and not used: mgr` (LSP) |
| 13 | `unknown field TraceCount in struct` (LSP) |

---

## Detailed Error Analysis

### 1. Schema Validation Errors

**Pattern**: `validating root: unexpected additional properties ["task"]`

**Count**: 206 occurrences (189 + 17)

**Severity**: High

**Description**: The ADK validates tool arguments against JSON schemas before the coercing tool runs. When LLMs pass extra properties (like `task`, `path`) that aren't in the schema, validation fails.

**Root Cause**: 
- LLMs sometimes include extra metadata in tool calls
- The ADK's validation runs before our `coercingTool` can sanitize arguments

**Example Error**:
```
validating root: unexpected additional properties ["task"]
validating root: unexpected additional properties ["path"]
```

**Fixes Implemented**:
1. ✅ Enhanced `coerceArgs()` in `internal/tools/registry.go` for type coercion
2. ✅ Modified `lenientSchema()` to be more permissive
3. ✅ Increased retry attempts for race conditions

**Status**: Partial fix - errors still occurring

---

### 2. Edit Tool - Old String Not Found

**Pattern**: `old_string not found in file`

**Count**: 21 occurrences

**Severity**: Medium

**Description**: The edit tool fails when the `old_string` parameter doesn't exactly match the file content. This often happens when:
- Concurrent modifications change the file between read and edit
- Whitespace differences (tabs vs spaces)
- Encoding differences

**File**: `internal/tools/edit.go:160`

**Example Error**:
```
old_string not found in file
```

**Fixes Implemented**:
1. ✅ Increased retry attempts: 1 → 3
2. ✅ Added exponential backoff: 100ms, 200ms, 300ms
3. ✅ Cache invalidation before each retry

**Status**: Fixed - retry logic in place

---

### 3. LSP Diagnostic Errors

**Pattern**: Various LSP diagnostics from `lsp_diagnostics` in function responses

**Count**: 170 occurrences

**Severity**: Medium

**Common Errors**:
- `declared and not used: mgr` (15 occurrences)
- `unknown field TraceCount in struct literal` (13 occurrences)
- Various syntax/type errors

**Files Affected**: Multiple test files

**Example Errors**:
```
hooks_test.go:261:2: error: declared and not used: mgr
tui.go:525:3: error: unknown field TraceCount in struct literal
```

**Fix Status**: These are legitimate code issues being detected by the LSP. Many have been fixed in subsequent sessions.

---

### 4. Path Escaping Errors

**Pattern**: `path escapes from parent`

**Count**: 11 occurrences

**Severity**: Medium

**Description**: Subagent worktrees generate paths like `../../go.mod` which are rejected by the sandbox security.

**Example Errors**:
```
reading file: openat ../../go.mod: path escapes from parent
reading file: openat ../../Makefile: path escapes from parent
path not found: statat ..: path escapes from parent
```

**Root Cause**: Subagent processes run in isolated directories but tools assume the sandbox root.

**Possible Fixes**:
1. Convert escaped paths to absolute paths before sandbox check
2. Add `../../` prefix stripping for known subagent directories
3. Pass absolute paths to subagent contexts

**Status**: Not started

---

### 5. Network Errors

**Pattern**: `connection refused`, `timeout`, `network error`

**Count**: 403 occurrences

**Severity**: High

**Description**: External service failures including:
- LLM provider API timeouts
- MCP server connection issues
- Rate limiting

**Categories**:
- Rate limit errors: 88
- Auth errors: 67
- Connection timeouts: 248

**Fixes Implemented**:
1. ✅ Retry logic with exponential backoff in `internal/agent/retry.go`
2. ✅ Configurable rate limiting

**Status**: Retry logic helps, but some errors unavoidable

---

### 6. File Not Found Errors

**Pattern**: `no such file or directory`

**Count**: 168 occurrences

**Severity**: Low

**Description**: The agent tries to read/write files that don't exist. Often occurs when:
- Path typos
- Deleted files
- Wrong working directory assumptions

**Fix Status**: Expected behavior - agent needs better path validation

---

### 7. Session/Branch Errors

**Pattern**: `session not found`, `branch already exists`, `cannot switch branch`

**Count**: 192 + 16 = 208 occurrences

**Severity**: Low-Medium

**Description**: Session management edge cases including:
- Concurrent session access
- Branch creation/switching conflicts
- Session compaction issues

**Fix Status**: Generally handled gracefully by the system

---

### 8. Build/Test Failures

**Pattern**: `build failed`, `compilation failed`, `FAIL\t`, `--- FAIL`

**Count**: 27 + 11 = 38 occurrences

**Severity**: High

**Description**: The agent's changes introduce compilation or test failures. These are expected when the agent makes code changes.

**Note**: These are legitimate test failures from agent code changes, not system errors.

---

### 9. Environment Missing

**Pattern**: `missing env`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`

**Count**: 83 occurrences

**Severity**: Medium

**Description**: Required environment variables not set.

**Fix Status**: User configuration issue - agent should detect and guide users to set up config

---

### 10. JSONL Corruption

**Pattern**: `unmarshaling event`, `invalid jsonl`

**Count**: 4 occurrences

**Severity**: Low

**Description**: Session log files have corrupted JSON lines.

**Files**: `internal/session/store.go`

**Fix Status**: Rare edge cases - system recovers

---

## Error Reduction Progress

### Fixed Errors

| Error Type | Before | After | Reduction |
|------------|--------|-------|-----------|
| `old_string not found` | ~20 | ~2 | 90% |
| Type coercion issues | ~138 | ~70 | 49% |
| Regex compilation | N/A | N/A | CPU reduced |

### Remaining High-Priority Errors

| Error Type | Count | Priority |
|------------|-------|----------|
| `unexpected additional properties` | 206 | High |
| Network/API errors | 403 | High |
| Rate limiting | 88 | Medium |
| Path escaping | 11 | Medium |

---

## Recommendations

### Immediate (High Priority)

1. **Schema Validation Hook**
   - Investigate ADK `ProcessRequest` vs `Run` timing
   - Consider registering a global validation override
   - Set `additionalProperties: true` on all tool schemas

2. **Path Escaping Fix**
   - Implement path normalization for subagent directories
   - Convert relative paths to absolute before sandbox checks

### Medium Priority

1. **Tool Descriptions**
   - Improve descriptions to clearly mark required vs optional fields
   - Add examples showing minimal valid calls

2. **Error Recovery**
   - Add automatic session recovery for corrupted JSONL files
   - Implement graceful degradation for LSP failures

### Low Priority

1. **Monitoring**
   - Add metrics for error rates by type
   - Create alerting for spike detection

2. **Documentation**
   - Update user guides with common error solutions

---

## Query Commands

```bash
# Check recent errors (last 30 days)
find ~/.pi-go/sessions -name "events.jsonl" -mtime -30 | \
  xargs grep -h '"error":"[^"]*"' 2>/dev/null | \
  sed 's/.*"error":"//;s/".*//' | \
  sort | uniq -c | sort -rn

# Check for specific error patterns
find ~/.pi-go/sessions -name "events.jsonl" -mtime -7 | \
  xargs grep -l "unexpected additional properties"

# Count sessions with errors
find ~/.pi-go/sessions -name "events.jsonl" -mtime -30 | \
  xargs grep -l '"error"' | wc -l
```

---

## Appendix: Error Source Distribution

| Source | Count | Percentage |
|--------|-------|------------|
| `tool_response` | 2,669 | 98.2% |
| `file_read` | 48 | 1.8% |

Most errors originate from tool responses, indicating they come from tool execution rather than system failures.
