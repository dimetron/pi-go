---
name: check-linters-before-commit
description: Before any commit, run linters, vet, tests, and build verification; do not commit until checks pass
tools: bash, read, grep, find
---

# Check Linters Before Commit

You are an expert at ensuring code quality by running linters and static analysis before committing changes.

## When to Use

Use this skill whenever the user asks to commit, prepare a commit, or check whether code is ready to commit. Run the checks before committing anything, and do not proceed with a commit until the required checks pass.

## Workflow

### 1. Check What Changed

First, identify what files have changed:

```bash
git diff --name-only
git diff --cached --name-only
```

### 2. Run Linters

Run the full linting suite (uses `.golangci.yml` v2 config):

```bash
golangci-lint run ./...
```

This covers `go vet`, `staticcheck`, `errcheck`, `errorlint`, `misspell`, `revive`, and more. Falls back to `go vet ./...` only if golangci-lint is not installed.

### 3. Run Tests

Verify tests pass:

```bash
go test ./...
```

Since Go 1.27, `go test` also runs the `stdversion` vet check by default, which fails on
use of stdlib symbols newer than the `go` directive in `go.mod`. If a test fails that
way, bump `go.mod` in the same change — do not `//nolint` around it. See the `go-127`
skill.

### 4. Build Verification

Ensure the code builds:

```bash
go build ./cmd/pi
```

## Success Criteria

All of the following must pass before reporting readiness or creating a commit:

- [ ] `golangci-lint run ./...` exits with code 0 (no issues)
- [ ] `go test ./...` exits with code 0 (all tests pass)
- [ ] `go build ./cmd/pi` exits with code 0 (builds successfully)

## Output Format

Report the status of each check:

```
## Pre-Commit Checks

| Check | Status | Notes |
|-------|--------|-------|
| Lint (golangci-lint) | ✅ PASS / ❌ FAIL | |
| Tests | ✅ PASS / ❌ FAIL | |
| Build | ✅ PASS / ❌ FAIL | |

**Result:** Ready to commit / Fix issues before committing
```

## If Checks Fail

For each failing check:

1. Note the specific file:line where the issue occurs
2. Provide the linter rule/message
3. Suggest a fix or create a TODO for manual fixing

Example:

```
## Issues Found

### Lint Failures

1. **internal/tools/reader.go:42** — `whitespace` rule: unnecessary trailing whitespace
   - Fix: Remove trailing whitespace

2. **internal/agent/runner.go:78** — `errorlint` rule: non-wrapping error creation
   - Fix: Wrap error with `fmt.Errorf("context: %w", err)`
```

## Rules

- Always run these checks before making or suggesting a commit
- Always run `golangci-lint run ./...` first (most comprehensive, uses `.golangci.yml`)
- Fall back to `go vet ./...` only if golangci-lint is not installed
- Do not skip or ignore linter warnings
- Do not claim code is ready to commit if any required check fails or is not run
- If there are legitimate reasons to ignore (e.g., intentional type assertions), suggest adding `//nolint:` directives with justification
- Check both staged (`git diff --cached`) and unstaged (`git diff`) changes
