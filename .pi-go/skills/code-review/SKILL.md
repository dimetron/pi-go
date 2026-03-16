---
name: code-review
description: Review code for quality on modified uncommitted files, running linters if available.
tools: read, grep, bash, ls
---

# Code Review

Review modified (uncommitted) files for code quality issues and run available linters.

## Steps

1. **Identify changes**: Run `git status` and `git diff --name-only` to find modified files.

2. **Read modified files**: Use `read` to examine the content of changed files. Focus on new and modified code sections.

3. **Check for linters**: Look for common Go linters in the project:
   - `golangci-lint` (check if `.golangci.yml` exists)
   - `go vet`
   - `staticcheck`
   - `revive`
   - `golangci-lint run` or individual linter commands

4. **Run linters**: Execute available linters on the modified files:
   ```bash
   golangci-lint run --new-from-rev=HEAD~1 ./...
   go vet ./...
   ```

5. **Analyze quality**: Look for common issues:
   - Error handling (missing error checks)
   - Resource leaks (unclosed files, connections)
   - Concurrency issues (race conditions, missing mutexes)
   - Code complexity (nested loops, large functions)
   - Naming conventions
   - Comment/documentation
   - Empty branches (unhandled errors)

6. **Summarize findings**: Present a clear summary with:
   - Files reviewed
   - Linter results (if any)
   - Quality issues found (with file:line references)
   - Severity: High / Medium / Low

## Examples

- `/code-review` — Review all uncommitted changes in the current repo
- `/code-review` — Run after making changes but before committing

## Guidelines

- Only review files shown by `git diff` — don't review the entire codebase
- Run linters first; they catch many common issues automatically
- Prioritize real bugs over style preferences
- If no uncommitted changes exist, inform the user
- Be concise — focus on actionable findings
- Suggest fixes when obvious (don't just point out problems)