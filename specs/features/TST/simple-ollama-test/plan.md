# Implementation Plan: Simple Ollama E2E Test

## Checklist

- [ ] Step 1: Update dependencies
- [ ] Step 2: Create the E2E test script
- [ ] Step 3: Create the log validator
- [ ] Step 4: Add Makefile target
- [ ] Step 5: Run the test and validate

---

## Step 1: Update Dependencies

**Objective:** Bring all Go dependencies to latest compatible versions.

**Implementation:**
- Run `go get -u ./...` to update all direct dependencies
- Run `go mod tidy` to clean up
- Verify build: `go build ./...`
- Verify tests: `go test ./...`

**Test:** Project compiles and existing tests pass.

**Demo:** `go list -m all` shows updated versions.

---

## Step 2: Create E2E Test Script

**Objective:** Create `scripts/test-ollama-e2e.sh` — the main test orchestrator.

**Implementation:**
```
scripts/test-ollama-e2e.sh:
1. Check prerequisites:
   - ollama binary exists
   - ollama is running (curl localhost:11434)
   - model minimax-m2.5:cloud is available (ollama list)
2. Build pi binary (go build -o ./pi ./cmd/pi)
3. Clean up any existing PI.md
4. Run pi with test prompt:
   ./pi --model minimax-m2.5:cloud --mode print \
     "Explore this codebase using tree, read, and grep tools.
      Then create a PI.md file with a project overview including:
      project name, purpose, architecture, key components, and tech stack."
5. Validate PI.md exists and has > 500 chars
6. Run log validation (Step 3)
7. Clean up test artifacts
8. Report results
```

**Test:** Script runs to completion with proper error messages when prerequisites are missing.

**Demo:** Running `./scripts/test-ollama-e2e.sh` shows step-by-step progress and pass/fail.

---

## Step 3: Create Log Validator

**Objective:** Validate session logs have no tool errors and no skipped commands.

**Implementation:**
```
Inline in test script or separate scripts/validate-logs.sh:
1. Find latest session log in ~/.pi-go/log/$(date +%Y-%m-%d)/
2. Check for error entries: jq 'select(.type == "error")'
3. Pair tool_calls with tool_results:
   - Extract all tool_call entries
   - Extract all tool_result entries
   - Verify counts match (every call got a result)
4. Check tool_result content for error indicators
5. Report any failures with details
```

**Test:** Validator correctly identifies errors in a test log file.

**Demo:** Shows "Logs OK: N tool calls, N tool results, 0 errors" or lists failures.

---

## Step 4: Add Makefile Target

**Objective:** Add `make test-ollama` target for easy invocation.

**Implementation:**
Add to Makefile:
```makefile
test-ollama: build
	@echo "Running Ollama E2E test..."
	@bash scripts/test-ollama-e2e.sh
```

**Test:** `make test-ollama` runs the full test.

**Demo:** `make test-ollama` from project root.

---

## Step 5: Run Test and Validate

**Objective:** Execute the full E2E test and verify all acceptance criteria.

**Implementation:**
1. Ensure Ollama is running with minimax-m2.5:cloud
2. Run `make test-ollama`
3. Verify:
   - PI.md was generated with meaningful content
   - Session logs have zero errors
   - All tool calls have results
   - No commands were skipped/ignored
4. Review PI.md quality

**Test:** All acceptance criteria pass.

**Demo:** Complete test run with green output.
