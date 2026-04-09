# Session Error Fix Prompts

This document contains actionable prompts for fixing common error patterns detected in pi-go session logs.

---

## Pattern: Path Escapes from Parent

- **Detected in**: 67 occurrences across session logs
- **Tool**: `read`
- **Error**: `error: reading file: openat ... path escapes from parent`
- **Fix suggestion**: 
  - Use absolute paths from the project root instead of relative `../` traversal
  - Resolve symlinks before passing paths to the read tool
  - When reading files, ensure the path is resolved relative to the current working directory context
  - Example fix: Instead of `read specs/../cmd/pi/main.go`, use `read cmd/pi/main.go`

---

## Pattern: Validating Root Missing Properties

- **Detected in**: 140+ occurrences (76 find + 64 read + other tools)
- **Tool**: `find`, `read`, `lsp-symbols`, `subagent`
- **Error**: `error: validating root: required: missing properties: [`
- **Fix suggestion**:
  - Ensure tool calls include proper `root` property when sandbox validation is enabled
  - Add explicit root context in tool parameter construction
  - For file operations, pass the resolved absolute path
  - When calling subagents, ensure the `task` parameter contains full context including working directory

---

## Pattern: Bash Command Required

- **Detected in**: 24 occurrences
- **Tool**: `bash`
- **Error**: `error: command is required`
- **Fix suggestion**:
  - System prompt should enforce: never call `bash` with empty or missing `command`
  - Add pre-call validation: if command is empty/whitespace, return error message to user instead of calling tool
  - Before invoking bash tool, ensure command parameter is populated with actual shell command

---

## Pattern: Runtime Panic

- **Detected in**: 9 occurrences with exit_code:1
- **Tool**: `bash`
- **Error**: `panic: runtime error: invalid memory address or nil`
- **Fix suggestion**:
  - Add nil pointer checks in RPC handlers (check `client`, `session` before dereferencing)
  - Add nil checks in compactor stage processing
  - Before committing: run `go test ./...` to catch nil dereferences
  - Consider adding defensive nil checks in session package initialization

---

## Pattern: File Not Found

- **Detected in**: 8 occurrences
- **Tool**: `read`
- **Error**: `error: reading file: openat specs...: no such file or directory`
- **Fix suggestion**:
  - Before reading a file, check existence with `ls` or `find`
  - If file doesn't exist, create it first (with `write` tool) or inform the user
  - When planning to read spec files, verify the specs directory exists and has content
  - Use `tree` to explore directory structure before attempting to read specific files

---

## Context: When Each Fix Applies

| Scenario | Applicable Fix |
|----------|----------------|
| Reading files with `../` in path | Path Escapes fix |
| Sandboxed environment (CI, MCP) | Validating Root fix |
| Agent produces no command for bash | Command Required fix |
| During session compaction or RPC | Runtime Panic fix |
| Reading spec files or new files | File Not Found fix |

---

## General Guidelines

1. **Always use absolute paths** when possible to avoid traversal issues
2. **Validate tool parameters** before calling the tool
3. **Check file existence** before reading files in unfamiliar directories
4. **Run tests before committing** to catch runtime panics
5. **Log and report** when path validation fails so patterns can be tracked