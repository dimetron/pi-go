# Complete Enhancement Plan: Polish, E2E Tests, and Documentation

## Overview

Finish the remaining items from Step 14 of the enhance-from-oh-my-pi plan. All code is implemented and unit tests pass. This plan covers updating ARCHITECTURE.md with the 3 new packages (lsp, subagent, git tools + commit), adding missing E2E test scenarios, and refining the Makefile.

## Context

- Files involved: ARCHITECTURE.md, internal/agent/e2e_enhanced_test.go, Makefile
- Related patterns: Existing E2E tests use scenarioLLM mock with step sequences
- Dependencies: All implementation steps (1-13) are complete and tests pass

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Update ARCHITECTURE.md with new packages

**Files:**
- Modify: `ARCHITECTURE.md`

- [ ] Add `subagent/` package to the Package Structure tree with description (pool, spawner, worktree, orchestrator)
- [ ] Add `lsp/` package to the Package Structure tree with description (protocol, client, manager, languages, hooks)
- [ ] Update the Dependency Graph mermaid diagram to include cli->subagent, cli->lsp edges
- [ ] Update Tool System diagram to include git-overview, git-file-diff, git-hunk, and the 5 LSP tools (diagnostics, definition, references, hover, symbols), and agent tool
- [ ] Update the tool table with git-overview, git-file-diff, git-hunk rows
- [ ] Add a new "Model Roles" section describing the role-based model routing system
- [ ] Add a new "Subagent System" section with mermaid diagram showing orchestrator->pool, spawner, worktree, and the 6 agent types
- [ ] Add a new "LSP Integration" section describing hooks (format-on-write, diagnostics-on-edit) and explicit tools
- [ ] Update Slash commands list to include /commit and /agents
- [ ] Update Keyboard section if commit confirmation keys were added
- [ ] No tests needed for documentation-only task; verify build still passes

### Task 2: Add missing E2E test scenarios

**Files:**
- Modify: `internal/agent/e2e_enhanced_test.go`

- [ ] Add TestE2ECommitWorkflow: create temp repo, stage changes, simulate /commit flow with scenarioLLM generating a conventional commit message, verify commit created with expected format
- [ ] Add TestE2ESubagentTypes: verify all 6 agent types (explore, plan, designer, reviewer, task, quick_task) are defined with valid role mappings, worktree settings, instructions, and tool lists
- [ ] Add TestE2EWorktreeLifecycle: create temp git repo, use WorktreeManager to create worktree, verify path exists and is a separate working copy, make a change, cleanup, verify worktree removed
- [ ] Add TestE2ELSPToolRegistration: verify LSPTools() returns 5 tools with correct names (lsp-diagnostics, lsp-definition, lsp-references, lsp-hover, lsp-symbols)
- [ ] Add TestE2EAgentToolRegistration: verify agent tool is created with correct schema including type and prompt fields
- [ ] Run e2e tests: go test -tags e2e ./internal/agent/ - all must pass

### Task 3: Refine Makefile test targets

**Files:**
- Modify: `Makefile`

- [ ] Add test-unit target: go test ./... (excludes e2e tag by default)
- [ ] Add test-integration target: go test -tags integration ./... (for tests needing git, real filesystem)
- [ ] Rename existing e2e target to test-e2e for consistency
- [ ] Add test-all target that runs unit + integration + e2e
- [ ] Add test-coverage target: go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
- [ ] Verify all Makefile targets work by running make test

### Task 4: Verify acceptance criteria

- [ ] manual test: go build ./cmd/pi succeeds
- [ ] run full test suite: make test
- [ ] run linter: go vet ./...
- [ ] run e2e tests: make test-e2e (or make e2e)
- [ ] verify ARCHITECTURE.md accurately reflects current codebase

### Task 5: Update documentation

- [ ] move specs/enhance-from-oh-my-pi/plan.md checklist items to completed (all 14 steps checked)
- [ ] move this plan to `docs/plans/completed/`
