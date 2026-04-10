# Research: old_string Not Found (Edit Tool File Drift)

## Problem Statement

The edit tool fails with "old_string not found" when the file content doesn't exactly match the provided `old_string`. This can happen when:
1. The file was modified by another tool or concurrent edit
2. The LLM's extracted `old_string` is slightly different from the actual content
3. Whitespace or formatting differences exist

## Current State

### editHandler Function (edit.go:39-81)

```go
func editHandler(sb *Sandbox, input EditInput) (EditOutput, error) {
    // ... validation (lines 40-48) ...
    
    // Read file once
    data, err := sb.ReadFile(input.FilePath)
    if err != nil {
        return EditOutput{}, fmt.Errorf("reading file: %w", err)
    }
    content := string(data)
    
    // Count occurrences
    count := strings.Count(content, input.OldString)
    if count == 0 {
        return EditOutput{}, fmt.Errorf("old_string not found in file")
    }
    if count > 1 && !input.ReplaceAll {
        return EditOutput{}, fmt.Errorf("old_string found %d times... use replace_all", count)
    }
    
    // Apply replacement
    if input.ReplaceAll {
        content = strings.ReplaceAll(content, input.OldString, input.NewString)
    } else {
        content = strings.Replace(content, input.OldString, input.NewString, 1)
    }
    
    // Write back
    err = sb.WriteFile(input.FilePath, []byte(content), 0o644)
    if err != nil {
        return EditOutput{}, fmt.Errorf("writing file: %w", err)
    }
    
    return EditOutput{Replacements: count}, nil
}
```

### Key Characteristics

1. **Single read**: File is read once at line 50
2. **Exact matching**: Uses `strings.Count()` for exact substring match
3. **No retry**: Returns error immediately on 0 matches
4. **No atomicity**: Read-modify-write is three separate operations

## Error Scenarios

| Scenario | Current Error Message |
|---|---|
| old_string not in file | "old_string not found in file" |
| Multiple matches, no ReplaceAll | "old_string found N times... use replace_all" |
| File modified between read/write | Silent success or "old_string not found" |

## Root Cause Analysis

1. **Race condition**: File could change between read and write
2. **LLM extraction errors**: Model may mis-copy the old_string
3. **No fuzzy matching**: Exact substring match only
4. **No context preservation**: Original read content not retained

## Investigation Findings

### No Atomic Operations
- `sb.ReadFile()` and `sb.WriteFile()` are separate calls
- No file locking mechanism exists
- Concurrent edits to same file could overwrite each other

### No Retry Logic
- Tool layer has no retry (per codebase-exploration.md:309)
- Relies on LLM to retry with corrected `old_string`
- Each retry reads file again, but may fail again

### Sandbox WriteFile (sandbox.go:79-89)
```go
func (s *Sandbox) WriteFile(name string, data []byte, perm os.FileMode) error {
    rel, err := s.Resolve(name)
    if err != nil {
        return err
    }
    f, err := s.root.OpenFile(rel, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
    if err != nil {
        return err
    }
    // No atomic rename, direct write
    _, err = f.Write(data)
    // ...
}
```

## Options for Fix

### Option A: Retry with Re-read (Low Risk)
- Re-read file when `old_string` not found
- Apply one retry before returning error
- Add helpful context to error message

### Option B: Fuzzy Matching (Medium Risk)
- Implement Levenshtein distance or similar
- Suggest corrections when close match found
- More complex, requires string similarity library

### Option C: Atomic Write with Locking (Medium Risk)
- Use file locking to prevent concurrent modifications
- Atomic write via temp file + rename
- Most robust but most complex

## Recommendation

**Implement Option A first**, then consider Option B for high-value cases:

**Option A Implementation**:
1. Re-read file once on "not found" error
2. Check again with fresh content
3. If still not found, return error with:
   - The old_string that wasn't found
   - Preview of current file content
   - Suggestions for fixing

## Implementation Location

- **Primary**: `internal/tools/edit.go` (editHandler function)
- **Supporting**: Error message formatting utilities

## Test Strategy

1. Unit test for concurrent modification scenario
2. Unit test for near-miss old_string detection
3. Integration test with actual file modifications

## Dependencies

- None - self-contained in edit.go

## Risk Assessment

- **Risk Level**: Low
- **Reasoning**: Bounded retry (1 attempt), enhanced errors only
- **Testing**: Can verify with existing test infrastructure

## Related Patterns

### Agent Retry Pattern (agent/retry.go)
- Exponential backoff for transient errors
- Pattern matching for error classification
- Could be extended for tool-level retry

### Panic Recovery Pattern (compactor.go:159-163)
- Uses `defer recover()` for graceful degradation
- Could apply similar pattern for edit operations
