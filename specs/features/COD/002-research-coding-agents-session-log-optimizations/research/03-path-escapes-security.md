# Research: Path Escapes from Parent (Security Documentation)

## Problem Statement

Users may encounter "path X is outside sandbox Y" errors when trying to access files outside the working directory. This is **intentional security behavior** that should be documented.

## Current State

### Sandbox Resolve Method (sandbox.go:44-56)

```go
func (s *Sandbox) Resolve(name string) (string, error) {
    if filepath.IsAbs(name) {
        rel, err := filepath.Rel(s.dir, name)
        if err != nil {
            return "", fmt.Errorf("path %s is outside sandbox %s", name, s.dir)
        }
        return rel, nil
    }
    return name, nil
}
```

### os.Root Security (sandbox.go:11-14)

```go
type Sandbox struct {
    root *os.Root  // Go 1.24+ secure root handle
    dir  string    // absolute path of the root directory
}
```

## Security Model

### Intentional Design Decisions

1. **Restricts file access to sandbox directory**: Prevents agent from reading/writing files outside the working directory
2. **Prevents directory traversal**: Blocks `..` and symlink escapes
3. **Uses os.Root (Go 1.24+)**: Enforces security at OS level

### What is Blocked

- Parent directories (`../`, `../../`)
- Absolute paths outside sandbox (`/etc/passwd`, `C:\Windows`)
- Symlinks pointing outside
- Path traversal attacks

### What is Allowed

- Relative paths under sandbox root
- Absolute paths that resolve under sandbox
- Files created within sandbox

## Error Messages

| Error Type | Current Message |
|---|---|
| Path outside sandbox | "path %s is outside sandbox %s" |
| File not found | Standard os.Root error |

## Root Cause Analysis

This is **NOT a bug** - it's intentional security design. The error occurs when:
1. User tries to access files outside working directory
2. Agent receives confusing error message
3. No clear documentation for users

## Investigation Findings

### os.Root Behavior (sandbox.go)
- `s.root.ReadFile(rel)` enforces path security
- `s.root.MkdirAll()` is sandboxed
- All operations go through `Resolve()` first

### No Documentation
- No godoc on `Resolve()` method
- No inline comments explaining security rationale
- No ARCHITECTURE.md security section

## Options for Fix

### Option A: Enhanced Documentation (Zero Risk)
- Add godoc to `Resolve()` explaining security
- Add inline comments with rationale
- Update README or create ARCHITECTURE.md

### Option B: Better Error Messages (Low Risk)
- Explain why path is blocked
- Suggest alternatives (change working directory)
- Include sandbox path in error

### Option C: User-Controlled Sandboxing (High Risk)
- Allow users to specify sandbox boundaries
- Add commands to expand/shrink sandbox
- Complex, security implications

## Recommendation

**Implement Options A + B**:

1. **Documentation in code**:
   - Add godoc to `Resolve()` and `Sandbox` struct
   - Explain security model and rationale
   - Document behavior and limitations

2. **Better error messages**:
   - Explain why access was denied
   - Suggest alternatives
   - Include context (sandbox path, requested path)

## Implementation Location

- **Primary**: `internal/tools/sandbox.go` (documentation)
- **Secondary**: `ARCHITECTURE.md` or `README.md` (security section)

## Documentation Template

```go
// Sandbox provides a secure file system abstraction that restricts
// all file operations to a specific directory tree.
//
// SECURITY MODEL:
// - All file paths are resolved relative to the sandbox root
// - Access outside the sandbox is blocked via os.Root (Go 1.24+)
// - This prevents the agent from accessing sensitive files outside
//   the working directory
//
// LIMITATIONS:
// - Files outside the sandbox cannot be accessed
// - Symlinks pointing outside are blocked
// - Absolute paths are converted to relative
//
// WORKAROUNDS:
// - Change the working directory to access different files
// - Use tools that explicitly access external resources (e.g., fetch URLs)
```

## Test Strategy

1. Verify error messages are clear
2. Verify sandbox boundaries are enforced
3. Document known limitations

## Dependencies

- None - documentation only

## Risk Assessment

- **Risk Level**: None
- **Reasoning**: Pure documentation changes
- **Testing**: Manual verification of error messages

## Relationship to Other Optimizations

This optimization unblocks clearer error messages for:
- Optimization 1 (JSON validation)
- Optimization 2 (old_string not found)
- Optimization 4 (file not found retry)

When users understand the security model, they can work around limitations.
