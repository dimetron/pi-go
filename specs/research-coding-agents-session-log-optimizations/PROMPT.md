# Session Log Optimizations Implementation

## Objective

Implement 6 optimizations to improve the reliability, robustness, and performance of the pi-go coding agent's tool layer:

1. JSON escaping instructions in system prompt
2. Edit handler retry with enhanced error messages
3. Sandbox security documentation
4. Sandbox file read transient retry
5. TUI goroutine panic recovery
6. File content cache with invalidation

## Key Requirements

1. **JSON Validation** — Add JSON escaping guidance to SystemInstruction to prevent malformed file paths
2. **Edit Retry** — Re-read file once on "old_string not found" + enhanced error with preview
3. **Security Docs** — Document os.Root security model in sandbox.go godoc
4. **Read Retry** — Implement transient error retry (max 3, 50ms backoff) in Sandbox.ReadFile
5. **Panic Recovery** — Add defer recover() to TUI runAgentLoop with stack trace logging
6. **File Cache** — LRU cache with mtime invalidation, integrated into read handler

## Acceptance Criteria

### Optimization 1: JSON Validation
- Given the system prompt contains JSON escaping guidance, when an LLM generates tool parameters with a Windows path, then the path should be properly escaped.

### Optimization 2: Edit Retry
- Given an edit operation where old_string is not found, when the file was recently modified, then re-read the file once and retry.
- Given old_string is still not found after retry, then return error with expected text, file preview (500 chars), and suggestions.

### Optimization 3: Security Documentation
- Given a file access attempt outside the sandbox, when the error is returned, then the error explains the sandbox restriction and suggests alternatives.
- Given godoc is requested, when running `go doc`, then the Sandbox type shows security documentation.

### Optimization 4: File Read Retry
- Given a file read encounters a transient error (e.g., "text file busy"), when the error is transient, then retry up to 3 times with increasing delay.
- Given a file read encounters a non-transient error (e.g., permission denied), when the error is permanent, then return immediately.

### Optimization 5: Test Panics
- Given a panic in runAgentLoop, when the panic occurs, then recover it, log with stack trace, and send as error via channel.

### Optimization 6: File Content Cache
- Given a file is read twice in quick succession, when the second read occurs, then serve content from cache if mtime matches.
- Given a file is written or edited, when the cache contains that file, then invalidate the cache entry.

## Implementation Slices

1. **Slice 1.1: Security Documentation** — Add godoc to Sandbox struct and Resolve() method in sandbox.go, verify: `go doc ./internal/tools/... | grep Sandbox`

2. **Slice 1.2: JSON Escaping Instructions** — Add JSON escaping section to SystemInstruction in agent.go, verify: `grep "JSON String Escaping" internal/agent/agent.go`

3. **Slice 2.1: Edit Handler Retry** — Add reReadFile() retry, buildEditNotFoundError() with preview, modify editHandler, verify: `go test ./internal/tools/... -run TestEdit`

4. **Slice 2.2: Sandbox Transient Retry** — Add isTransientReadError(), constants, modify ReadFile with retry loop, verify: `go test ./internal/tools/... -run TestSandbox`

5. **Slice 3.1: TUI Panic Recovery** — Add defer recover() to runAgentLoop, import runtime/debug, verify: `go test ./internal/tui/... -run TestAgent`

6. **Slice 4.1: File Content Cache (NEW)** — Create cache.go with fileContentCache struct, Get/Put/Invalidate methods, verify: `go test ./internal/tools/... -run TestCache`

7. **Slice 4.2: Cache Integration** — Modify readHandler to check/update cache, verify: `go test ./internal/tools/... -run TestRead`

8. **Slice 4.3: Cache Invalidation on Edit** — Call cache.Invalidate() after successful edit, verify: `go test ./internal/tools/... -run TestEdit`

## Gates

- **build**: `go build ./...`
- **test**: `go test ./internal/tools/... ./internal/tui/...`
- **vet**: `go vet ./...`
- **race**: `go test -race ./internal/tools/... ./internal/tui/...`
- **docs**: `go doc ./internal/tools/...`

## Reference

- Design: `specs/research-coding-agents-session-log-optimizations/design.md`
- Outline: `specs/research-coding-agents-session-log-optimizations/outline.md`
- Plan: `specs/research-coding-agents-session-log-optimizations/plan.md`
- Requirements: `specs/research-coding-agents-session-log-optimizations/requirements.md`
- Research:
  - `specs/research-coding-agents-session-log-optimizations/research/codebase-exploration.md`
  - `specs/research-coding-agents-session-log-optimizations/research/session-and-recovery.md`
  - `specs/research-coding-agents-session-log-optimizations/research/01-json-validation.md`
  - `specs/research-coding-agents-session-log-optimizations/research/02-old-string-not-found.md`
  - `specs/research-coding-agents-session-log-optimizations/research/03-path-escapes-security.md`
  - `specs/research-coding-agents-session-log-optimizations/research/04-file-not-found-retry.md`
  - `specs/research-coding-agents-session-log-optimizations/research/05-test-panics.md`
  - `specs/research-coding-agents-session-log-optimizations/research/06-duplicate-file-reads.md`

## Constraints

- All slices must compile independently
- Retry logic is bounded (1 retry for edit, 3 for read)
- Cache uses LRU eviction with mtime invalidation
- Panic recovery uses existing channel-based error propagation pattern
- Changes are additive; rollback by reverting additions
