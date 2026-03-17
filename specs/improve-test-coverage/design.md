# Improve Test Coverage - Design

## Objective

Increase test coverage across all internal packages from 58.0% to a target of 70%+ while maintaining existing tests and following Go testing conventions.

## Strategy

### Priority Order (by impact and feasibility)

1. **internal/provider** (46.7% → 70%): High impact - core functionality with low hanging fruit
2. **internal/tui** (44.9% → 60%): Largest codebase, complex UI logic
3. **internal/lsp** (58.2% → 70%): Well-structured, easier to test
4. **internal/tools** (63.8% → 75%): Already has good test coverage
5. **internal/cli** (67.2% → 80%): Command handlers

### Testing Patterns to Follow

- Use Go's standard `testing` package
- Table-driven tests for functions with multiple input/output cases
- Mock interfaces for external dependencies (LLM providers, file systems)
- Test edge cases and error conditions

## Architecture

No architectural changes needed - this is a test addition task.

## Acceptance Criteria

### Given the existing test suite, when running tests, then:
- All existing tests continue to pass
- Overall coverage increases from 58% to 70%+
- No new test files break the build

### Specific Package Improvements

- **internal/provider**: Add tests for `GenerateContent`, streaming functions, and error handling
- **internal/tui**: Add tests for View, render functions, and command handlers
- **internal/lsp**: Add tests for client lifecycle, diagnostics, and format operations
- **internal/tools**: Add tests for registry functions, restart tool, and tree tool

## Testing Strategy

1. Run `go test ./... -cover` before and after changes to measure progress
2. Focus on functions with 0% coverage first (highest impact)
3. Add tests in same package as implementation (*_test.go)
4. Use descriptive test names following Go convention: `Test<FunctionName>_<Scenario>`

## Constraints

- Exclude `cmd/pi` from coverage targets (main package)
- Do not introduce new test frameworks
- All tests must pass before considering the work complete