# Existing Test Coverage Analysis

## Current Test Commands

- **Run tests**: `go test ./...`
- **Run with coverage**: `go test ./... -cover`
- **Generate coverage profile**: `go test ./... -coverprofile=coverage.out`
- **View coverage**: `go tool cover -func=coverage.out`

## Testing Framework

- Using Go's standard `testing` package
- No external test frameworks (no testify, ginkgo, etc.)

## Test File Patterns

- Test files follow Go convention: `*_test.go`
- Tests are co-located with implementation files

## Packages with Lowest Coverage (Priority for improvement)

1. **internal/tui** - 44.9% - largest codebase, many UI components
2. **internal/provider** - 46.7% - LLM provider implementations
3. **internal/lsp** - 58.2% - Language server protocol
4. **internal/tools** - 63.8% - Tool implementations
5. **internal/cli** - 67.2% - CLI commands

## Functions with 0% Coverage (Critical gaps)

### internal/tui
- `runAgentLoop` (line 1082)
- `Run` (line 175)
- `Init` (line 208)
- `reloadSkills` (line 1411)
- `renderMarkdown` (line 1623)
- `renderStatusBar` (line 1637)
- `detectBranch` (line 1736)
- `renderInput` (line 1748)
- Various handlers with 0%

### internal/provider
- `antThinkingConfig` (line 255)
- `antRunStreaming` (line 271)
- `antRunNonStreaming` (line 383)
- `GenerateContent` (line 46)
- `oaiRunStreaming` (line 239)
- `oaiRunNonStreaming` (line 344)
- `GenerateContent` (line 43)
- `Name` (line 41)

### internal/lsp
- `NewClient` (line 42)
- `Close` (line 164)
- `collectDiagnostics` (line 121)
- `formatFile` (line 85)
- `Diagnostics` (line 29)
- `Close` (line 156)
- `ServerFor` (line 249)
- `startServer` (line 327)

### internal/tools
- `agentHandler` (line 48)
- `restartHandler` (part of NewRestartTool)
- `treeHandler` (line 50)
- `Declaration` (line 104)
- `ProcessRequest` (line 117)
- `Run` (line 153)
- `coerceArgs` (line 167)

## Test Files Already Present

- `cmd/pi/main_test.go`
- `internal/tools/git_overview_test.go`
- `internal/tools/git_diff_test.go`
- `internal/tools/redact_test.go`
- `internal/tools/agent_test.go`
- `internal/tools/lsp_test.go`
- `internal/tools/tools_test.go`
- `internal/extension/mcp_test.go`
- `internal/extension/skills_test.go`
- `internal/extension/hooks_test.go`
- `internal/logger/logger_test.go`
- `internal/config/config_test.go`
- `internal/tui/plan_run_e2e_test.go`
- `internal/tui/tui_test.go`
- `internal/tui/completion_test.go`
- `internal/tui/plan_flow_test.go`
- `internal/tui/run_test.go`
- `internal/tui/plan_test.go`
- `internal/tui/commit_test.go`
- `internal/tui/run_summary_test.go`

## Build Commands

- **Build**: `go build ./...`
- **Vet**: `go vet ./...`
- **Lint**: (no explicit linter configured)