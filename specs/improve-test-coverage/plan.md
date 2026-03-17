# Improve Test Coverage - Implementation Plan

## Target Coverage: 58% → 70%+

## Steps

- [ ] 1: Improve internal/provider coverage (46.7% → 70%+)
  - Add tests for GenerateContent in anthropic.go
  - Add tests for GenerateContent in openai.go  
  - Add tests for streaming functions (antRunStreaming, oaiRunStreaming)
  - Add tests for error handling paths
  - Run `go test ./internal/provider/... -cover` to verify

- [ ] 2: Improve internal/tui coverage (44.9% → 60%+)
  - Add tests for View function and render* functions
  - Add tests for runAgentLoop with mocked agent
  - Add tests for reloadSkills function
  - Add tests for detectBranch and renderInput
  - Run `go test ./internal/tui/... -cover` to verify

- [ ] 3: Improve internal/lsp coverage (58.2% → 70%+)
  - Add tests for NewClient and client lifecycle
  - Add tests for Close methods
  - Add tests for Diagnostics collection
  - Add tests for formatFile function
  - Run `go test ./internal/lsp/... -cover` to verify

- [ ] 4: Improve internal/tools coverage (63.8% → 75%+)
  - Add tests for registry Declaration, ProcessRequest, Run
  - Add tests for restartHandler
  - Add tests for treeHandler
  - Run `go test ./internal/tools/... -cover` to verify

- [ ] 5: Improve internal/cli coverage (67.2% → 80%+)
  - Add tests for Execute function
  - Add tests for buildCommitMsgFunc
  - Run `go test ./internal/cli/... -cover` to verify

- [ ] 6: Final verification
  - Run `go test ./... -cover` to confirm overall coverage
  - Run `go vet ./...` to ensure code quality
  - Ensure all tests pass with `go test ./...`

## Notes

- Each step should be atomic - complete tests for one package before moving to next
- Run coverage command after each step to measure progress
- Focus on functions with 0% coverage first for maximum impact