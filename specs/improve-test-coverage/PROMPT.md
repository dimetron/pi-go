# Improve Test Coverage

## Objective

Increase test coverage across all internal packages from 58.0% to 70%+ while maintaining existing tests and following Go testing conventions.

## Key Requirements

1. **Improve internal/provider** — Add tests for GenerateContent, streaming functions, error handling (target: 70%+)
2. **Improve internal/tui** — Add tests for View, render functions, command handlers (target: 60%+)
3. **Improve internal/lsp** — Add tests for client lifecycle, diagnostics, format operations (target: 70%+)
4. **Improve internal/tools** — Add tests for registry functions, restart tool, tree tool (target: 75%+)
5. **Improve internal/cli** — Add tests for Execute and buildCommitMsgFunc (target: 80%+)
6. **Final verification** — Ensure all tests pass and overall coverage reaches 70%+

## Acceptance Criteria

### Package Coverage
- Given current coverage data, when tests are run, then overall coverage increases from 58% to 70%+

### Package-Specific
- Given internal/provider package, when GenerateContent tests are added, then coverage improves from 46.7% to 70%+
- Given internal/tui package, when View and render function tests are added, then coverage improves from 44.9% to 60%+
- Given internal/lsp package, when client lifecycle tests are added, then coverage improves from 58.2% to 70%+

### Existing Tests
- Given the existing test suite, when new tests are added, then all existing tests continue to pass

## Gates

- **test**: `go test ./...`
- **test with coverage**: `go test ./... -cover`
- **vet**: `go vet ./...`
- all packages MUST have 80% or higher test coverage


## Reference

- Design: `specs/improve-test-coverage/design.md`
- Plan: `specs/improve-test-coverage/plan.md`
- Requirements: `specs/improve-test-coverage/requirements.md`
- Research: `specs/improve-test-coverage/research/`

## Constraints

- Exclude cmd/pi from coverage targets (main package)
- Use Go's standard testing package (no external frameworks)
- All tests must pass before considering work complete
- Focus on functions with 0% coverage first for maximum impact