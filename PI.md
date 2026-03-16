# pi-go

A terminal-based coding agent built on [Google ADK Go](https://google.github.io/adk-go/) with multi-provider LLM support, sandboxed tool execution, session persistence, and an interactive terminal UI.

## Project Purpose

pi-go is an AI-powered coding assistant that helps users with software engineering tasks. It provides:
- Multi-model LLM support (Claude, OpenAI, Gemini, Ollama)
- Sandboxed file operations restricted to the project directory
- Interactive terminal UI with Markdown rendering
- Session persistence with branching support
- Subagent system for parallel task execution
- LSP integration for language intelligence
- Unix socket JSON-RPC for IDE/editor integration

## Architecture Overview

```
User Input → CLI/TUI → Agent → LLM Provider → Tool Execution → Response
                                      ↓                             
                              Session Store (JSONL)            
                                      ↓
                              LSP Servers (format, diagnostics)
```

The project follows a layered architecture:
- **Entry point**: `cmd/pi/main.go` → CLI dispatcher
- **Core**: Agent wraps ADK runner with tools and session management
- **Providers**: Pluggable LLM implementations
- **Tools**: Sandboxed file/shell operations via `os.Root`
- **UI**: Bubble Tea v2 interactive terminal with Glamour markdown

## Key Packages and Responsibilities

| Package | Responsibility |
|---------|----------------|
| `cmd/pi` | Entry point, CLI argument parsing |
| `internal/cli` | Cobra CLI, output mode selection (interactive, print, json, rpc), wiring |
| `internal/agent` | ADK agent setup, retry logic with exponential backoff, system prompt |
| `internal/config` | Config loading from `~/.pi-go/` and `.pi-go/`, model roles |
| `internal/provider` | LLM providers: Anthropic (Claude), OpenAI (GPT/O-series), Gemini, Ollama |
| `internal/tools` | Sandboxed tools: read, write, edit, bash, grep, find, ls, tree, git tools |
| `internal/session` | JSONL event persistence, branching, compaction |
| `internal/tui` | Bubble Tea v2 interactive UI, slash commands, commit workflow |
| `internal/subagent` | Process-based subagent orchestration, worktree isolation, pool concurrency |
| `internal/lsp` | LSP client, language server management (Go, TypeScript, Python, Rust) |
| `internal/rpc` | Unix socket JSON-RPC 2.0 server for IDE integration |
| `internal/extension` | Hooks, skills (`.SKILL.md`), MCP server integration |

## Technology Stack

- **Language**: Go 1.25+
- **Core Framework**: [Google ADK Go](https://google.golang.org/adk) v0.6.0
- **LLM Providers**:
  - Anthropic SDK (`anthropic-sdk-go` v1.26.0)
  - OpenAI (`openai-go/v3` v3.28.0)
  - Google GenAI (`genai` v1.50.0)
  - Ollama (Anthropic-compatible API)
- **CLI**: Cobra v1.10.2
- **TUI**: Bubble Tea v2 + Lipgloss
- **Markdown**: Glamour v1.0.0
- **MCP**: `modelcontextprotocol/go-sdk` v1.4.1

## Build and Run

### Prerequisites

- Go 1.25 or later
- At least one LLM API key:
  - `ANTHROPIC_API_KEY` (Claude)
  - `OPENAI_API_KEY` (GPT/O-series)
  - `GOOGLE_API_KEY` (Gemini)
- Or a running Ollama instance for local models

### Build

```bash
# Build the binary
make build
# or
go build ./cmd/pi

# Run tests
make test

# Run linting
make lint

# Run E2E tests
make e2e

# Clean build artifacts
make clean
```

### Usage

```bash
# Default interactive mode (TUI)
./pi

# Select model by prefix
./pi --model claude:sonnet
./pi --model openai:gpt-4o
./pi --model gemini:gemini-2.5-pro
./pi --model ollama:llama3

# Use model roles
./pi --smol          # fast, cheap model
./pi --slow          # most capable model
./pi --plan          # planning-oriented model

# Non-interactive modes
./pi --mode print "explain this codebase"
./pi --mode json "list all TODO comments"
./pi --mode rpc      # start RPC server on /tmp/pi-go.sock

# Resume sessions
./pi --continue      # continue last session
./pi --session <id>  # resume specific session
```

### Slash Commands (Interactive Mode)

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/model` | Switch model mid-conversation |
| `/session` | List and switch sessions |
| `/branch` | Create a conversation branch |
| `/commit` | Generate and apply a git commit |
| `/compact` | Compact session history |
| `/clear` | Clear conversation |
| `/exit` | Exit the agent |

## Configuration

Config files are loaded from (in order of precedence):
1. `.pi-go/config.json` (project-local)
2. `~/.pi-go/config.json` (global)

Example configuration:

```json
{
  "roles": {
    "default": { "model": "claude-sonnet-4-20250514" },
    "smol": { "model": "claude-haiku-3-20240307" },
    "slow": { "model": "claude-opus-4-20250514" },
    "plan": { "model": "claude-sonnet-4-20250514" }
  },
  "hooks": {
    "after_write": ["gofmt -w {file_path}"]
  },
  "mcp": {
    "servers": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/path/to/files"]
  }
}
```

## Project Structure

```
pi-go/
├── cmd/pi/main.go          # Entry point
├── internal/
│   ├── agent/              # ADK agent, retry logic
│   ├── cli/                # CLI, output modes
│   ├── config/             # Configuration loading
│   ├── extension/          # Hooks, skills, MCP
│   ├── lsp/                # LSP client, hooks
│   ├── provider/           # LLM providers
│   ├── rpc/                # JSON-RPC server
│   ├── session/            # JSONL persistence
│   ├── subagent/           # Subagent system
│   ├── tools/              # Sandboxed tools
│   └── tui/                # Bubble Tea UI
├── Makefile
├── go.mod
└── README.md
```

For detailed architecture documentation, see [ARCHITECTURE.md](ARCHITECTURE.md).