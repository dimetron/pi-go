# pi-go Project Rules

## Architecture

pi-go is a Go coding agent built on Google ADK Go (`google.golang.org/adk`) with multi-provider LLM support (Anthropic, OpenAI, Gemini).

See [ARCHITECTURE.md](../ARCHITECTURE.md) for full architecture documentation including diagrams, dependency graph, and the planned memory system.

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

## Subagents

Bundled definitions live in `internal/subagent/bundled/*.md` and are embedded into
the binary. Discovery merges them with `~/.pi-go/agents/` (user) and
`.pi-go/agents/` (project), **project > user > bundled** — so a project agent with
the same name silently shadows the bundled one. Do not copy a bundled agent into
`.pi-go/agents/` to tweak it; edit the bundled file.

**Architecture review is `internal/subagent/bundled/architect.md`.** Route any
question about *where code belongs* — package boundaries, dependency direction,
service/tool contracts, whether a change fits at all — to the `architect` agent
rather than answering it inline or spinning up a generic worker. It is the only
agent that owns those decisions.

The rest, in the order you usually reach for them:

| Agent                  | Use it for                                                                  |
|------------------------|-----------------------------------------------------------------------------|
| `architect`            | **Architecture review**: boundaries, dependency direction, trade-offs, ADRs |
| `plan`                 | *How* to implement a change, once the architecture is settled               |
| `explore`              | Codebase research: find code, trace dependencies, map structure             |
| `code-reviewer`        | Correctness, error handling, edge cases on a diff                           |
| `spec-reviewer`        | Specs and design docs under `specs/`                                        |
| `task`, `designer`     | Implement in an isolated worktree                                           |
| `quick-task`, `worker` | Small or general jobs                                                       |

`architect` and `plan` are not the same job. `architect` answers *should this exist
and where does it live*; `plan` answers *what are the steps*. Asking `plan` an
architecture question gets you a confident implementation sequence for the wrong
design.

## Output Modes

- **print**: Text to stdout, tool status to stderr. Default when stdin is piped.
- **json**: JSONL events (message_start, text_delta, tool_call, tool_result, message_end).
- **rpc**: Unix socket JSON-RPC with JSONL event streaming.
- **interactive**: Bubble Tea v2 TUI with markdown rendering. Default when stdin is a terminal.

## Repository

- **GitHub**: https://github.com/dimetron/pi-go

When the user asks to "open GitHub", "open in browser", "show github", "go to github", or similar, use the `open` command to open the repository URL in the default browser:

```bash
open https://github.com/dimetron/pi-go
```

## Do NOT

- Add multi-module structure. Keep everything in one `go.mod`.
- Import `internal/` packages from outside the module.
- Add external runtime dependencies. The binary must be self-contained.
- Skip error wrapping. Always provide context in error messages.
- Use `init()` functions. Prefer explicit initialization.
