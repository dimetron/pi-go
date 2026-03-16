---
name: code-review
description: Review code for quality, run linters, check test coverage, fix issues, and enforce gates. Use before committing changes.
---

# Code Review

Review code for quality, test coverage, and fix problems found.

## Steps

1. **Identify scope**: Run `git diff --name-only` to find modified files. If there are changes, review only modified packages. If no uncommitted changes, review all packages (`./...`).

2. **Run linters**: Execute linters on target packages:
   - `go vet ./...`
   - `golangci-lint run --new-from-rev=HEAD~1 ./...` (if available)

3. **Run tests and check coverage**:
   - `go test -race -count=1 -coverprofile=coverage.out ./...`
   - `go tool cover -func=coverage.out`
   - Identify failing tests, functions with 0% coverage, packages below 80%

4. **Review code quality**: Read target files and check for:
   - Missing tests for new/changed functions
   - Error handling (missing checks, swallowed errors)
   - Resource leaks (unclosed files, connections)
   - Concurrency issues (race conditions, missing mutexes)

5. **Fix issues in parallel using subagents**: For each package needing coverage fixes, spawn a subagent to work on it concurrently:
   - Use the `agent` tool to spawn one subagent per package that needs test coverage improvement
   - Each subagent prompt: "Write tests for uncovered functions in package <pkg>. Read the source files, identify untested functions using the coverage data, write table-driven tests covering happy path and error cases. Run `go test -race ./<pkg>/...` to verify. NEVER delete existing tests."
   - While subagents write tests, fix linter warnings and code quality issues yourself
   - Wait for all subagents to complete, then verify their work compiles

6. **Verify gates**: All gates must pass before reporting success:
   - GATE 1: `go build ./...` — zero errors
   - GATE 2: `go vet ./...` — zero warnings
   - GATE 3: `go test -race ./...` — all tests pass
   - GATE 4: `go test -cover ./...` — >= 80% per package
   - If any gate fails, fix and re-run until all pass

7. **Report**: Present final summary:
   - Gates: PASS / FAIL for each
   - Test results: pass/fail count
   - Coverage: per-package percentages (before → after)
   - Issues fixed with file:line references

## Gates

All gates are mandatory. Do not report success unless every gate passes.

| Gate | Command | Requirement |
|------|---------|-------------|
| Build | `go build ./...` | Zero errors |
| Vet | `go vet ./...` | Zero warnings |
| Test | `go test -race ./...` | All pass |
| Coverage | `go test -cover ./...` | >= 80% per package |

## Examples

- `/code-review` — Full review: lint, test, coverage, parallel fix, verify gates

## Guidelines

- If uncommitted changes exist, focus on modified packages
- If no changes, review all packages for full codebase quality check
- NEVER delete existing tests — only add or fix tests
- Use subagents to parallelize test writing across packages (one subagent per package)
- While subagents handle coverage, fix linter and quality issues in parallel
- Tests are mandatory: every new/changed exported function needs a test
- Table-driven tests preferred for Go code
- Test both happy path and error cases
- Fix issues directly — don't just report them
- Keep iterating until all gates pass
