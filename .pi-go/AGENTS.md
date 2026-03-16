# pi-go Project Rules

## Architecture

pi-go is a Go coding agent built on Google ADK Go (`google.golang.org/adk`) with multi-provider LLM support (Anthropic, OpenAI, Gemini).

- **Single module**: `github.com/dimetron/pi-go`, Go 1.26+
- **Internal packages**: all non-CLI code lives under `internal/`
- **Single binary**: `go build ./cmd/pi` produces one executable

## Package Layout

| Package | Purpose |
|---------|---------|
| `cmd/pi` | Entry point |
| `internal/agent` | ADK agent setup, runner, retry logic |
| `internal/cli` | Cobra CLI, output modes (print, json, rpc, interactive) |
| `internal/config` | Config loading from `~/.pi-go/` and `.pi-go/` |
| `internal/extension` | Hooks, skills (SKILL.md), MCP tool integration |
| `internal/provider` | Multi-provider LLM implementations (Anthropic, OpenAI, Gemini) |
| `internal/rpc` | Unix socket JSON-RPC server |
| `internal/session` | JSONL session persistence, compaction, branching |
| `internal/tools` | Core tools: read, write, edit, bash, grep, find, ls |
| `internal/tui` | Bubble Tea v2 interactive terminal UI |

## Conventions

- **ADK interfaces**: Use ADK Go's native interfaces (`model.LLM`, `tool.Tool`, `session.Service`) rather than custom abstractions.
- **Testing**: Every package has `*_test.go`. E2E tests use build tag `e2e`. Run with `go test ./...` or `go test -tags e2e ./...`.
- **Error handling**: Wrap errors with `fmt.Errorf("context: %w", err)`. Transient LLM errors use retry with exponential backoff (`internal/agent/retry.go`).
- **Tool registration**: Tools are ADK `FunctionTool` instances created via `tool.NewFunctionTool`. Register in `tools.CoreTools()`.
- **Extensions**: Hooks use ADK's `BeforeToolCallbacks`/`AfterToolCallbacks`. MCP uses `mcptoolset.New()`. Skills parse `*.SKILL.md` files.
- **Session persistence**: JSONL append-only format in `~/.pi-go/sessions/`. Implements ADK `session.Service`.

## Output Modes

- **print**: Text to stdout, tool status to stderr. Default when stdin is piped.
- **json**: JSONL events (message_start, text_delta, tool_call, tool_result, message_end).
- **rpc**: Unix socket JSON-RPC with JSONL event streaming.
- **interactive**: Bubble Tea v2 TUI with markdown rendering. Default when stdin is a terminal.

## Do NOT

- Add multi-module structure. Keep everything in one `go.mod`.
- Import `internal/` packages from outside the module.
- Add external runtime dependencies. The binary must be self-contained.
- Skip error wrapping. Always provide context in error messages.
- Use `init()` functions. Prefer explicit initialization.
