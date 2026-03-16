# pi-go

A terminal-based coding agent built on [Google ADK Go](https://google.github.io/adk-go/) with multi-provider LLM support, sandboxed tool execution, LSP integration, and a subagent system.

## Project Overview

pi-go is a command-line coding assistant that leverages large language models to help developers with software engineering tasks. It provides an interactive terminal UI, persistent session management with branching support, and a suite of sandboxed tools for file operations, code search, git operations, and shell execution.

## Architecture

```
cmd/pi/             Entry point — CLI parsing, output mode selection
internal/
├── agent/          ADK agent setup, retry logic, runner
├── cli/            Cobra CLI flags, output modes (interactive, print, json, rpc)
├── config/         Global and project config (roles, hooks, MCP, themes)
├── extension/      Hooks, skills, MCP server integration
├── lsp/            LSP JSON-RPC client, language registry, manager, hooks
├── logger/         Session logging
├── provider/       LLM providers implementing genai model interface
├── rpc/            Unix socket JSON-RPC 2.0 server
├── session/        JSONL persistence, branching, compaction
├── subagent/       Process spawner, orchestrator, concurrency pool
├── tools/          Sandboxed tools (read, write, edit, bash, grep, find, git, lsp)
└── tui/            Bubble Tea v2 UI, slash commands, commit workflow
```

### Request Flow

```
User input → CLI → Agent → LLM provider → Tool calls → Sandbox → Response → TUI
                     ↕                        ↕
              Session store              LSP servers
              (JSONL events)          (format, diagnostics)
```

## Key Packages

### `internal/agent`
The core ADK Go agent setup. Creates an LLM agent with tools, system instructions, and callback hooks. Implements retry logic with exponential backoff for transient LLM errors. Exposes `agent.Run()` which returns an iterator over session events.

### `internal/cli`
Cobra-based command-line interface. Handles all CLI flags (`--model`, `--mode`, `--session`, `--smol`, `--slow`, `--plan`, etc.). Determines output mode (interactive, print, json, rpc) based on terminal state or explicit flag. Routes user input to the appropriate handler.

### `internal/config`
Configuration loading from `~/.pi-go/config.json` (global) and `.pi-go/config.json` (project). Supports model roles (default, smol, slow, plan, commit), API keys from environment variables, base URLs, MCP server definitions, and hook configurations.

### `internal/provider`
Multi-provider LLM abstraction. Supports:
- **Anthropic** (Claude models)
- **OpenAI** (GPT-4o, O-series models)
- **Google Gemini** (gemini-2.5-pro, etc.)
- **Ollama** (local models with `:cloud` suffix for Anthropic-compatible API)

Resolves model names to providers via prefix matching (claude→anthropic, gpt→openai, gemini→gemini).

### `internal/tools`
Sandboxed tool implementations using `os.Root` for filesystem isolation:
- `read` — Read file contents with optional offset/limit
- `write` — Create or overwrite files
- `edit` — Surgical edits using exact string matching
- `bash` — Shell command execution (restricted to project directory)
- `grep` — Regex search across files
- `find` — Glob-based file discovery
- `tree` — Directory tree visualization
- `ls` — Directory listing
- `git_diff`, `git_hunk`, `git_overview` — AI-enhanced Git operations
- `lsp_*` — LSP-powered code navigation and diagnostics

### `internal/session`
File-based session persistence implementing ADK's `session.Service` interface:
- Sessions stored in `~/.pi-go/sessions/<session-id>/`
- `meta.json` — Session metadata (id, app, user, workdir, model, timestamps)
- `events.jsonl` — Append-only event log
- Branching support via `branches.json`
- Compaction to summarize long sessions when token threshold exceeded

### `internal/tui`
Bubble Tea v2 interactive terminal UI:
- Markdown rendering via Glamour
- Slash commands: `/help`, `/model`, `/session`, `/branch`, `/commit`, `/compact`, `/clear`, `/exit`
- Commit workflow: generates conventional commits using LLM
- Session history navigation

### `internal/subagent`
Process-based multi-agent system:
- Agent types: explore, plan, designer, reviewer, task, quick_task
- Worktree support for isolated git worktrees
- Concurrency pool for parallel task execution
- Spawns agents as separate processes for isolation

### `internal/lsp`
Language Server Protocol client:
- Supports Go, TypeScript/JS, Python, Rust
- JSON-RPC communication
- Format-on-write and diagnostics-on-edit hooks
- Explicit tools: `lsp-diagnostics`, `lsp-definition`, `lsp-references`, `lsp-hover`, `lsp-symbols`

### `internal/extension`
Extensibility mechanisms:
- **Hooks** — Shell commands triggered on tool events (before/after tool execution)
- **Skills** — `.SKILL.md` files defining agent capabilities
- **MCP** — Model Context Protocol server integration

### `internal/rpc`
Unix socket JSON-RPC 2.0 server for IDE/editor integration. Allows external tools to communicate with pi-go over a Unix domain socket.

## Technology Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.25+ |
| Agent Framework | Google ADK Go (`google.golang.org/adk`) |
| CLI | spf13/cobra |
| TUI | charm.land/bubbletea/v2 |
| Markdown | charmbracelet/glamour |
| LLM SDKs | anthropics/anthropic-sdk-go, openai/openai-go/v3, google/golang.org/genai |
| MCP | github.com/modelcontextprotocol/go-sdk |

## Building and Running

### Build

```bash
make build      # builds ./pi binary
```

### Testing

```bash
make test       # run unit tests
make test-e2e  # run E2E integration tests
make lint       # go vet
make clean      # remove binary
```

### Running

```bash
# Default interactive mode (uses TUI)
./pi

# Select a model by prefix
./pi --model claude:sonnet
./pi --model openai:gpt-4o
./pi --model gemini:gemini-2.5-pro
./pi --model ollama:llama3

# Use model roles
./pi --smol          # fast, cheap model
./pi --slow          # most capable model
./pi --plan          # planning-oriented model

# Non-interactive modes
./pi --mode print "explain this codebase"   # text to stdout, tools to stderr
./pi --mode json "list all TODO comments"  # JSONL events
./pi --mode rpc                              # start RPC server

# Resume a session
./pi --continue          # continue last session
./pi --session <uuid>   # resume specific session
```

### Environment Variables

```bash
# API Keys
ANTHROPIC_API_KEY      # for Claude models
OPENAI_API_KEY         # for GPT/O-series models
GOOGLE_API_KEY         # for Gemini models

# Base URLs (for custom endpoints / Ollama)
ANTHROPIC_BASE_URL     # e.g., http://localhost:11434 (Ollama)
OPENAI_BASE_URL
GEMINI_BASE_URL
```

### Configuration

Create `~/.pi-go/config.json` or `.pi-go/config.json`:

```json
{
  "roles": {
    "default": { "model": "claude-sonnet-4-20250514" },
    "smol": { "model": "claude-haiku-3-5-20250129" },
    "slow": { "model": "claude-sonnet-4-20250514" },
    "plan": { "model": "claude-sonnet-4-20250514" },
    "commit": { "model": "claude-haiku-3-5-20250129" }
  },
  "hooks": [
    { "event": "after_write", "command": "gofmt -w {{.Path}}", "tools": ["write", "edit"] }
  ],
  "mcp": {
    "servers": [
      { "name": "filesystem", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"] }
    ]
  },
  "theme": "default"
}
```

### Project-Specific Instructions

Place a `.pi-go/AGENTS.md` file in your project directory to add project-specific rules to the agent's system prompt.