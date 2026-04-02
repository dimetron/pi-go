`pi-go` is a terminal coding agent built on Google ADK Go (`google.golang.org/adk`) with multi-provider LLM support. The repository is a single Go module: `github.com/dimetron/pi-go`.
Use these commands when validating changes:
```bash
make build          # go build ./cmd/pi
make test           # go test ./...
make lint           # golangci-lint run ./...
make vet            # go vet ./...
make test-coverage  # coverage report
make test-e2e       # go test -tags e2e ./...
make clean          # remove pi binary and coverage.out
```
Package-specific tests: `go test ./internal/agent/...`
Race detector: `go test -race ./...`
High-level layout:
- `cmd/pi/` - entry point, delegates to CLI execution
- `internal/agent/` - ADK agent setup and retry logic
- `internal/audit/` - skill security scanning
- `internal/auth/` - OAuth login flows
- `internal/cli/` - Cobra CLI and output mode wiring
- `internal/config/` - global and project config loading
- `internal/extension/` - hooks, skills, MCP integration
- `internal/guardrail/` - token tracking and limits
- `internal/jsonrpc/` - JSON-RPC utilities
- `internal/logger/` - session logging
- `internal/lsp/` - LSP client and language registry
- `internal/memory/` - persistent memory and compression
- `internal/provider/` - LLM provider implementations
- `internal/rpc/` - Unix socket JSON-RPC server
- `internal/session/` - JSONL session persistence
- `internal/sop/` - planning workflows
- `internal/subagent/` - subprocess-based subagents
- `internal/tools/` - sandboxed tools
- `internal/tui/` - Bubble Tea UI
See `ARCHITECTURE.md` for deeper details.
- Prefer native ADK interfaces such as `model.LLM`, `tool.Tool`, and `session.Service` instead of adding parallel abstractions.
- Register tools with `tool.NewFunctionTool` and keep core tool wiring in `tools.CoreTools()`.
- Keep sandboxed file operations rooted to the working directory using `os.Root`.
- Wrap errors with context using `fmt.Errorf("context: %w", err)`.
- Avoid `init()` functions; prefer explicit initialization paths.
- Keep the repository as a single Go module; do not introduce multi-module structure.
- Do not import `internal/` packages from outside the module.
- Avoid runtime dependencies that weaken the self-contained binary model.
- Prefer targeted tests first, then broader validation if needed.
- Maintain package-level `*_test.go` coverage when changing behavior.
- Use the `e2e` build tag only for end-to-end coverage.
- Do not fix unrelated failing tests unless explicitly asked.
Common paths:
- `~/.pi-go/config.json` - global config
- `.pi-go/config.json` - project config
- `.pi-go/AGENTS.md` - project-local agent instructions consumed by the app
- `~/.pi-go/.env` - provider API keys
- `~/.pi-go/sessions/` - JSONL session storage
- `~/.pi-go/log/` - session logs
- Keep changes focused and minimal.
- Match the existing style of the surrounding package.
- Update documentation when behavior or developer workflow changes.
- Prefer `rg` for searches.
- Never revert user changes you did not make unless explicitly asked.
- Do not add a second Go module.
- Do not bypass sandbox restrictions in tool code.
- Do not skip contextual error wrapping.
- Do not add broad abstractions where existing ADK interfaces already fit.
