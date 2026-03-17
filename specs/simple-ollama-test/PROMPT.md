# E2E Ollama Test for pi-go

## Objective

Create a working E2E test that runs `pi --model minimax-m2.5:cloud` against a real Ollama backend, has the agent explore the codebase, and generates a `PI.md` project overview. The test must validate that session logs contain zero tool errors and no skipped commands.

## Key Requirements

1. **Update dependencies**: Run `go get -u ./...` and `go mod tidy` — specifically bump `github.com/modelcontextprotocol/go-sdk` from v1.4.0 to v1.4.1
2. **Test script** at `scripts/test-ollama-e2e.sh` that:
   - Checks prerequisites (Ollama running, model available, jq installed)
   - Builds pi binary
   - Runs `pi --model minimax-m2.5:cloud --mode print` with a prompt to explore codebase and write PI.md
   - Validates PI.md was created with >500 chars of meaningful content
   - Validates session JSONL logs at `~/.pi-go/log/` have zero `"type":"error"` entries
   - Validates every `tool_call` has a matching `tool_result` (no skipped commands)
   - Validates no tool_result content contains error indicators
3. **Makefile target**: `test-ollama` that runs the script
4. **Fix any issues** found during test execution — the model may not follow instructions well, may produce schema errors, or may stop early without completing the task

## Known Issues to Investigate

- The minimax-m2.5 model via Ollama may ignore the user prompt and output greeting text instead of following tool-use instructions
- The model may stop after 1-2 tool calls without completing multi-step tasks
- Check if the agent's system instruction or print mode limits the number of turns/iterations
- The Ollama provider routes through Anthropic SDK — verify tool calling works correctly with this model
- Schema validation errors may occur if the model sends unexpected parameter formats

## Acceptance Criteria

**Given** Ollama is running with `minimax-m2.5:cloud` available
**When** `make test-ollama` is run
**Then** the test passes with all checks green

**Given** the agent runs to completion
**When** PI.md is inspected
**Then** it contains project name, architecture overview, key packages, and tech stack (>500 chars)

**Given** the session log is parsed
**When** entries are analyzed
**Then** zero `"type":"error"` entries exist, and every `tool_call` has a corresponding `tool_result`

## Reference

- Design: `specs/simple-ollama-test/design.md`
- Plan: `specs/simple-ollama-test/plan.md`
- Research: `specs/simple-ollama-test/research/`
- Existing E2E tests: `internal/agent/e2e_test.go`, `internal/agent/e2e_enhanced_test.go`
- CLI print mode: `internal/cli/cli.go:327` (`runPrint`)
- Logger: `internal/logger/logger.go`
- Provider resolution: `internal/provider/provider.go` (`:cloud` suffix → Ollama)
- Tool registry: `internal/tools/registry.go` (lenient schema + type coercion)
