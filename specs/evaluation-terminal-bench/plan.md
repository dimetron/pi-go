# Implementation Plan: Terminal-Bench Evaluation for pi-go

## Checklist

- [ ] Step 1: Create project structure and CLI command
- [ ] Step 2: Implement Task Loader
- [ ] Step 3: Implement Container Manager
- [ ] Step 4: Implement pi-go Runner
- [ ] Step 5: Implement Result Verifier
- [ ] Step 6: Implement Evaluation Orchestrator
- [ ] Step 7: Add configuration file support
- [ ] Step 8: Add ATIF output format
- [ ] Step 9: Add progress reporting and logging
- [ ] Step 10: Integration tests and verification

---

## Step 1: Create Project Structure and CLI Command

**Objective:** Add the `pi eval` subcommand to the CLI with basic scaffolding.

**Implementation:**
- Create `internal/eval/` package directory
- Add `cmd/pi/eval.go` - CLI command handler
- Add `internal/eval/eval.go` - main evaluator struct and options
- Wire into main CLI in `cmd/pi/main.go`

**Test Requirements:**
- `pi eval --help` shows available options
- `pi eval` with no args shows error/welcome message

**Integration Notes:**
- Use existing CLI patterns from `internal/cli/`
- Follow same flag parsing style as other commands

**Demo Description:**
Run `pi eval --help` and see the new evaluation command with options for dataset, model, and output.

---

## Step 2: Implement Task Loader

**Objective:** Load and parse Terminal-Bench Pro tasks from local filesystem.

**Implementation:**
- Create `internal/eval/taskloader/taskloader.go`
- Implement `TaskLoader` interface with `LoadTasks()` and `LoadTask()`
- Add `Task`, `Difficulty`, `TaskFilter` types
- Add local filesystem source support
- Add HuggingFace dataset download (optional, can be manual)

**Test Requirements:**
- Load tasks from `testdata/eval/minimal-task/`
- Verify task structure parsing (instruction.md, Dockerfile, test.sh)
- Test filtering by category and difficulty

**Integration Notes:**
- Start with local directory loading; HuggingFace can be future enhancement

**Demo Description:**
Write a test that loads tasks and prints task IDs and difficulty levels.

---

## Step 3: Implement Container Manager

**Objective:** Build and run Docker containers for task execution.

**Implementation:**
- Create `internal/eval/container/container.go`
- Implement `ContainerManager` interface
- Add Docker build using `docker build` command
- Add container run using `docker run` with proper cleanup
- Implement `Exec()` for running commands in container
- Handle container lifecycle (create, exec, remove)

**Test Requirements:**
- Build a simple test container
- Execute command in container and verify output
- Verify cleanup removes containers

**Integration Notes:**
- Use `os/exec` for Docker CLI calls
- Handle Docker not installed error gracefully

**Demo Description:**
Build test container from `testdata/eval/minimal-task/Dockerfile` and run `echo hello`.

---

## Step 4: Implement pi-go Runner

**Objective:** Execute pi-go within a container with task instructions.

**Implementation:**
- Create `internal/eval/runner/runner.go`
- Implement `Runner` interface
- Add pi-go CLI invocation in container
- Add tool call trace collection (parse stdout for tool invocations)
- Add execution metrics collection
- Handle timeout and error cases

**Test Requirements:**
- Run pi-go with simple instruction
- Verify tool calls are recorded
- Verify timeout handling

**Integration Notes:**
- Use existing pi-go binary in container or build fresh
- For testing, can use mocked responses

**Demo Description:**
Run pi-go with instruction "Write hello to /tmp/test.txt" and verify file creation.

---

## Step 5: Implement Result Verifier

**Objective:** Execute task verification scripts and determine pass/fail.

**Implementation:**
- Create `internal/eval/verifier/verifier.go`
- Implement `Verifier` interface
- Execute test script in container
- Capture exit code and output
- Handle `/logs/verifier/reward.txt` reading

**Test Requirements:**
- Run test script that exits 0 - verify Passed=true
- Run test script that exits 1 - verify Passed=false
- Verify output is captured

**Integration Notes:**
- Test scripts run after pi-go completes
- May need to handle different test script formats (bash, python)

**Demo Description:**
Run test script that checks `/tmp/test.txt` contains "hello".

---

## Step 6: Implement Evaluation Orchestrator

**Objective:** Coordinate full evaluation pipeline with concurrency.

**Implementation:**
- Create `internal/eval/evaluator/evaluator.go`
- Implement `Evaluator` struct with all components
- Add task queue with worker pool pattern
- Add parallel execution with configurable concurrency
- Add result aggregation
- Add summary generation

**Test Requirements:**
- Run 3-5 tasks in parallel
- Verify results are aggregated correctly
- Verify summary calculations (success rate, etc.)

**Integration Notes:**
- Use `golang.org/x/sync/errgroup` for concurrency
- Track progress with channels

**Demo Description:**
Run evaluation on 5 tasks and see progress bar and summary output.

---

## Step 7: Add Configuration File Support

**Objective:** Allow eval configuration via YAML file.

**Implementation:**
- Add `EvalConfig` type in `internal/eval/config.go`
- Add config file loading in `internal/eval/eval.go`
- Support `--config` flag in CLI

**Test Requirements:**
- Load config from YAML file
- Verify config values override defaults

**Integration Notes:**
- Reuse config patterns from `internal/config/`

**Demo Description:**
Create `eval.yaml` with custom model and run evaluation with it.

---

## Step 8: Add ATIF Output Format

**Objective:** Generate ATIF-compliant output for Harbor compatibility.

**Implementation:**
- Add ATIF types in `internal/eval/atif/`
- Implement ATIF serialization in output
- Support `--output-format atif` flag

**Test Requirements:**
- Generate valid ATIF JSON for a completed task
- Validate ATIF output against schema

**Integration Notes:**
- Follow ATIF spec from Harbor documentation

**Demo Description:**
Run evaluation with `--output-format atif` and validate output.

---

## Step 9: Add Progress Reporting and Logging

**Objective:** Provide rich progress and logging output.

**Implementation:**
- Add structured logging using existing logger
- Add progress bar (use existing TUI patterns)
- Add category-by-category progress
- Add failure details on completion

**Test Requirements:**
- Verify progress updates during execution
- Verify failure summary includes error details

**Integration Notes:**
- Reuse patterns from `internal/tui/`

**Demo Description:**
Run full evaluation and see progress bar, then failure summary.

---

## Step 10: Integration Tests and Verification

**Objective:** Verify full pipeline works end-to-end.

**Implementation:**
- Add test fixtures in `testdata/eval/`
- Create minimal test tasks
- Run full pipeline in test
- Add CI test for subset of tasks

**Test Requirements:**
- End-to-end test passes with 5+ tasks
- Results file is valid JSON
- Summary is accurate

**Demo Description:**
Run `go test ./internal/eval/... -v` and see all tests pass.

---

## Summary

This plan provides 10 incremental steps, each delivering working functionality:

1. ✅ CLI command scaffolding
2. ✅ Task loading from filesystem
3. ✅ Docker container management
4. ✅ pi-go execution in containers
5. ✅ Result verification
6. ✅ Full orchestration with concurrency
7. ✅ Configuration file support
8. ✅ ATIF output format
9. ✅ Progress and logging
10. ✅ Integration tests

Each step ends with working code that can be demonstrated and tested. Core end-to-end functionality is available by Step 6, with enhancements following.