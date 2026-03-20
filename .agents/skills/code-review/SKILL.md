---
name: code-review
description: Review code changes for quality, run linters and tests, check coverage, fix issues, and enforce quality gates. Use when reviewing uncommitted changes or auditing codebase quality before committing. Do not use for writing new features or refactoring.
---

# Code Review

Review code for quality, test coverage, and fix problems found. Auto-detects project language and applies appropriate tooling.

## Steps

### 1. Detect scope and language

Run `git diff --name-only` to identify changed files. If no uncommitted changes, review all source files.

Detect project language from files present:

| Signal | Language | Build | Lint | Test |
|--------|----------|-------|------|------|
| `go.mod` | Go | `go build ./...` | `go vet ./...` && `golangci-lint run --new-from-rev=HEAD~1 ./...` | `go test -race -count=1 -coverprofile=coverage.out ./...` |
| `package.json` | TS/JS | `npx tsc --noEmit` | `npx eslint .` | `npm test -- --coverage` |
| `pyproject.toml` | Python | &mdash; | `ruff check .` | `pytest --cov --cov-report=term-missing` |
| `Cargo.toml` | Rust | `cargo build` | `cargo clippy -- -D warnings` | `cargo test` |

### 2. Run linters

Execute language-appropriate linters. Collect all warnings and errors.

### 3. Run tests and check coverage

Execute test command with coverage. Identify:
- Failing tests
- Functions with 0% coverage in changed files
- Packages/modules below 80% coverage

### 4. Review changed code

Read each changed file. Check for:
- **Correctness**: Logic errors, off-by-one, null/nil handling
- **Missing tests**: New/changed functions without coverage
- **Error handling**: Missing checks, swallowed errors
- **Security**: Injection, hardcoded secrets, unsafe operations
- **Resources**: Unclosed files, connections, goroutine/thread leaks
- **Concurrency**: Race conditions, missing locks
- **API surface**: Breaking changes, inconsistent naming

### 5. Fix issues

For each package needing fixes, work on it directly or spawn a subagent:
- Write tests for uncovered functions (happy path + error cases)
- Fix linter warnings and code quality issues
- Never delete existing tests

### 6. Verify gates

All gates must pass:

| Gate | Requirement |
|------|-------------|
| Build | Zero compile/type errors |
| Lint | Zero warnings |
| Test | All tests pass |
| Coverage | >= 80% per package for changed code |

If any gate fails, fix and re-run until all pass.

### 7. Report

Present summary table:

```
## Code Review Report

### Gates
| Gate | Result |
|------|--------|
| Build | PASS / FAIL |
| Lint  | PASS / FAIL |
| Test  | PASS / FAIL |
| Coverage | PASS / PARTIAL |

### Coverage
| Package | Before | After |
|---------|--------|-------|

### Issues Fixed
- file:line - description
```
