---
name: design-review
description: >
  Deep design review of Go codebase — naming, structure, consistency,
  interfaces, error handling. Scores each dimension and provides actionable fixes.
metadata:
  author: dimetron
  version: "1.1"
---

# Design Review

Perform a comprehensive design review of the Go codebase. Evaluate code quality, naming conventions, consistency, and architectural patterns. Produce a scored report with actionable improvements.

## When to use

Use this skill when:
- Auditing overall design quality before a major refactor or release
- Onboarding to an unfamiliar Go codebase and need a structured assessment
- Comparing codebase against Go idioms after a period of rapid development

Do NOT use for:
- Reviewing a single PR or uncommitted diff (use `code-review` instead)
- Writing new code or fixing bugs
- Running linters/tests for CI gating (use `code-review` instead)

### Relationship to code-review

| Aspect | design-review | code-review |
|--------|--------------|-------------|
| Scope | Entire codebase or package | Changed files only |
| Action | Read-only audit, scored report | Fix issues, enforce gates |
| Focus | Architecture, patterns, idioms | Correctness, coverage, linting |
| Output | Scorecard + recommendations | Pass/fail gates + fixes applied |

## Dimensions

Score each dimension 1-10 and provide specific file:line references for issues found.

### 1. Naming Conventions
- Exported types/functions follow Go conventions (MixedCaps, no underscores)
- Package names are short, lowercase, singular (not `utils`, `helpers`, `common`)
- Interface names use `-er` suffix where appropriate (`Reader`, `Writer`)
- Receiver names are short (1-2 chars), consistent within type
- Variable names: short in small scopes, descriptive in large scopes
- Acronyms are all-caps (`ID`, `URL`, `HTTP`, not `Id`, `Url`, `Http`)
- Test function names follow `TestFuncName_Scenario` pattern
- Constants use MixedCaps, not SCREAMING_SNAKE

### 2. Package Design
- Packages have clear, singular responsibility
- No circular dependencies
- Internal packages used appropriately for implementation details
- Package-level doc comments present
- No God packages (too many responsibilities)
- Reasonable file sizes (flag files > 500 lines)

### 3. Interface Design
- Interfaces are small (1-3 methods preferred)
- Interfaces defined where consumed, not where implemented
- No unnecessary interface pollution (concrete types are fine)
- Accept interfaces, return structs
- `io.Reader`, `io.Writer`, `fmt.Stringer` used where applicable

### 4. Error Handling
- Errors wrapped with context using `fmt.Errorf("...: %w", err)`
- Custom error types where callers need to inspect errors
- No swallowed errors (unchecked `err` returns)
- Sentinel errors are `var ErrFoo = errors.New(...)` not string comparison
- Error messages are lowercase, no punctuation, no "failed to" prefix

### 5. Concurrency Patterns
- Goroutines have clear ownership and lifecycle
- Channels used for communication, mutexes for state
- `context.Context` propagated correctly
- No goroutine leaks (all goroutines have exit paths)
- `sync.WaitGroup` or `errgroup` used for fan-out

### 6. API Consistency
- Similar operations use similar signatures across packages
- Config structs vs option functions used consistently
- Constructor functions follow `NewFoo` pattern
- Consistent use of pointer vs value receivers within a type
- Method ordering: constructor, public methods, private methods

### 7. Code Organization
- One primary type per file (type + methods)
- Test files mirror source files (`foo.go` / `foo_test.go`)
- Constants and vars at top of file
- `init()` functions avoided (or justified)
- Build tags and platform files follow conventions

### 8. Documentation
- Exported functions have doc comments starting with function name
- Package doc comments present
- Complex algorithms have inline comments explaining *why*
- No stale/misleading comments
- Examples in tests for complex APIs

## Procedure

1. **Set scope first**:
   - Determine target scope from command args (`/design-review`, package path, or `--focus`)
   - Apply default include/exclude rules from the Scope section
   - Note any skipped areas explicitly in the report

2. **Scan structure**: Use `Glob` (`**/*.go`) to map the package layout.
   Use `Shell` with `go list ./...` to enumerate packages and `wc -l` for line counts.

3. **Automated checks**: Run in parallel:
   - `go vet ./...` — catch common mistakes
   - `golangci-lint run ./...` (if available) — extended lint checks
   - Check for `//nolint` directives and their justifications
   - If a tool is unavailable, continue review and mark that check as "skipped"

4. **Naming audit**: Use `Grep` to sample each package:
   - Search for naming violations: pattern `Id[^s]` instead of `ID`, `Url[^s]` instead of `URL`
   - Use `Read` to check receiver name consistency per type
   - Verify exported function doc comments
   - Check package names against conventions

5. **Interface audit**: Use `Grep` for `type \w+ interface` to find all interface definitions:
   - Count methods per interface (flag > 5 methods)
   - Check if interfaces are defined at consumer or producer
   - Look for interface embedding depth

6. **Error handling audit**: Sample error paths:
   - `Grep` for unchecked errors: `_, _ =` or bare function calls
   - Check error wrapping: `%w` vs `%v` vs `%s`
   - Look for string-based error checking vs sentinel/type errors

7. **Consistency audit**: Compare patterns across packages:
   - Constructor patterns: `Grep` for `func New` across all packages and compare signatures
   - Config patterns (struct vs options)
   - Logging patterns (structured vs printf)
   - Test patterns (table-driven vs individual)

8. **Cross-reference**: Use tooling for efficiency:
   - Dead code: `Grep` for unexported function definitions, then `Grep` for call sites within the package
   - Duplicates: compare function signatures across packages
   - Inconsistent patterns doing the same thing differently

9. **Validate report before output**: Self-check the report against these gates:
   - Every scored dimension has at least one finding with a `file:line` reference, or an explicit "no issues found" note
   - Every Top 5 item has a before/after code snippet or an exact command to run
   - Key Strengths section has at least 2 entries with file references
   - Overall score matches the weighted average (recalculate to confirm)
   - No dimension is scored without evidence — if you can't find evidence, mark `N/A`
   If any gate fails, fix the report before presenting it.

## Scope

- Default include: `*.go` files in requested scope
- Default exclude: `vendor/`, `testdata/`, generated files (`// Code generated`), and protobuf outputs (for example `*.pb.go`) unless explicitly requested
- Optional focus flags:
  - `--include-tests` to include test style and coverage patterns in scoring
  - `--include-generated` to include generated code in analysis
- If scope is narrowed (for example `internal/tui`), score only that scope and state this clearly

## Project-Specific Checks (pi-go)

In addition to general Go review, check:
- **ADK compliance**: uses `model.LLM`, `tool.Tool`, `session.Service` — no custom abstractions wrapping ADK
- **Provider pattern**: all providers under `internal/provider/` implement the same interface consistently
- **Tool registration**: tools created via `tool.NewFunctionTool`, registered in `tools.CoreTools()`
- **Session format**: JSONL append-only, implements `session.Service`
- **Error retry**: transient LLM errors use `internal/agent/retry.go` patterns
- **TUI conventions**: Bubble Tea v2 imports from `charm.land/bubbletea/v2`, not the old `github.com/charmbracelet/bubbletea` path
- **No external runtime deps**: binary must be self-contained
- **No `init()` functions**: prefer explicit initialization

## Scoring Model

Use these weights for overall score:

- Naming Conventions: 15%
- Package Design: 15%
- Interface Design: 15%
- Error Handling: 20%
- Concurrency Patterns: 10%
- API Consistency: 10%
- Code Organization: 10%
- Documentation: 5%

Calibration:

| Score | Meaning |
|-------|---------|
| 9-10  | Stdlib/kubernetes quality — exemplary, publishable as reference |
| 7-8   | Production quality — minor issues, follows idioms well |
| 5-6   | Functional — noticeable gaps, inconsistencies, or missing patterns |
| 3-4   | Below standard — systematic issues, needs refactoring |
| 1-2   | Problematic — fundamental design issues |

Rules:

- Score each dimension `1-10` using the calibration table above
- Allow `N/A` when a dimension does not apply to the reviewed scope
- Compute overall as weighted average of applicable dimensions only
- Round overall score to 1 decimal place

## Output Format

Present results as a scorecard table, then detailed findings per dimension.

```
## Scorecard

| Dimension           | Score | Notes                           |
|---------------------|-------|---------------------------------|
| Naming Conventions  | X/10  | brief note                      |
| Package Design      | X/10  | brief note                      |
| Interface Design    | X/10  | brief note                      |
| Error Handling      | X/10  | brief note                      |
| Concurrency         | X/10  | brief note                      |
| API Consistency     | X/10  | brief note                      |
| Code Organization   | X/10  | brief note                      |
| Documentation       | X/10  | brief note                      |
|---------------------|-------|---------------------------------|
| **Overall**         | X/10  | weighted average (1 decimal)   |

## Key Strengths
1. **[Title]** — why it's good + `file:line` reference
2. ...
(minimum 2 strengths required)

## Top 5 Actionable Improvements

1. **[Title]** (impact: high/medium/low, effort: high/medium/low)
   - Confidence: high/medium/low
   - What: one-sentence description of the problem
   - Where: `file:line` references (every affected location)
   - Why: what breaks, degrades, or confuses without the fix
   - How: concrete fix — include before/after code snippet

   ```go
   // before
   func GetUserId() string { ... }
   // after
   func GetUserID() string { ... }
   ```

... (repeat for each — every item MUST have a before/after snippet or exact command)

## Detailed Findings

### Naming Conventions (X/10)

**Issues** (each must have evidence):

| # | Location | Issue | Suggested fix |
|---|----------|-------|---------------|
| 1 | `file.go:42` | `userId` should be `userID` | Rename to `userID` |
| ... | ... | ... | ... |

**What's working well**: brief note on what this dimension does right.

... (repeat per dimension — every scored dimension MUST have the issues table)
```

## Guidelines

- DO NOT make changes — this is a read-only audit
- **Evidence is mandatory**: every finding must include a `file:line` reference. For systemic issues, provide at least one concrete example plus a count ("12 occurrences across 4 packages")
- **Fixes must be concrete**: "improve naming" is not a fix; `rename userId to userID in store.go:42` is
- **Before/after required**: every Top 5 improvement must include a code snippet showing the current state and the proposed fix
- Be fair: note strengths as well as weaknesses in every dimension
- Prioritize by impact: focus on issues that affect maintainability
- Prioritize repository conventions and Effective Go first; use stdlib/well-known projects as secondary tie-breakers
- Score honestly — use the calibration table; 7/10 is good, 10/10 means stdlib-quality
- **No empty dimensions**: if a dimension is scored, it must have the issues table populated. If no issues are found, state "no issues found" explicitly and justify the score
- **Report is incomplete if**: any scored dimension lacks evidence, any Top 5 item lacks a before/after snippet, or the weighted average doesn't match the computed overall score

## Parallel Execution Strategy

For codebases with 5+ packages, split work across subagents:
1. **Main agent**: scan structure (step 2), run automated checks (step 3), produce final report
2. **Subagent per package group**: steps 4-7 (naming, interface, error, consistency audits)
   - Group small packages together (< 3 files each)
   - Each subagent returns dimension scores + findings for its packages
3. **Main agent**: merge subagent findings, resolve cross-package issues (step 8), compute final scores

## Examples

- `/design-review` — Full design review of entire codebase
- `/design-review internal/tui` — Review only the TUI package
- `/design-review --focus naming` — Review only naming conventions
