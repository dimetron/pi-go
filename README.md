# pi-go

A terminal-based coding agent built on [Google ADK Go](https://google.github.io/adk-go/) with multi-provider LLM support, sandboxed tool execution, LSP integration, and a subagent system.

![pi-go TUI](docs/screen/pi-go.png)

## Features

- **Multi-provider LLM** — Claude (Anthropic), GPT/O-series (OpenAI), Gemini (Google), and Ollama for local models
- **Sandboxed tools** — File read/write/edit, shell execution, grep, find, tree, and git operations, all restricted to the project directory via `os.Root`
- **Interactive TUI** — Bubble Tea v2 terminal UI with Markdown rendering (Glamour), slash commands, and theming
- **Session persistence** — JSONL append-only event logs with branching, compaction, and resume
- **Model roles** — Named configurations (default, smol, slow, plan, commit) selectable via CLI flags
- **Subagents** — Process-based multi-agent system with types: explore, plan, designer, reviewer, task, quick_task
- **LSP integration** — JSON-RPC client for Go, TypeScript/JS, Python, Rust with auto-format and diagnostics hooks
- **AI Git tools** — Repository overview, file diffs, hunk parsing, and LLM-generated conventional commits (`/commit`)
- **RPC server** — Unix socket JSON-RPC 2.0 for IDE/editor integration
- **Extensions** — Hooks (shell callbacks), skills (`.SKILL.md` instructions), and MCP server support

## Architecture

```
cmd/pi/             Entry point — CLI parsing, output mode selection
internal/
├── agent/          ADK agent setup, retry logic, runner
├── cli/            Cobra CLI flags, output modes (interactive, print, json, rpc)
├── config/         Global and project config (roles, hooks, MCP, themes)
├── extension/      Hooks, skills, MCP server integration
├── lsp/            LSP JSON-RPC client, language registry, manager, hooks
├── provider/       LLM providers implementing genai model interface
├── rpc/            Unix socket JSON-RPC 2.0 server
├── session/        JSONL persistence, branching, compaction
├── subagent/       Process spawner, orchestrator, concurrency pool
├── tools/          Sandboxed tools (read, write, edit, bash, grep, find, git, lsp)
└── tui/            Bubble Tea v2 UI, slash commands, commit workflow
```

### Request flow

```
User input → CLI → Agent → LLM provider → Tool calls → Sandbox → Response → TUI
                     ↕                        ↕
              Session store              LSP servers
              (JSONL events)          (format, diagnostics)
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed documentation.

## Requirements

- Go 1.25+
- At least one LLM provider API key (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`) or a running Ollama instance

## Build

```bash
make build      # build the pi binary
make test       # run unit tests
make lint       # go vet
make e2e        # run E2E integration tests
make clean      # remove binary
```

## Usage

```bash
# Default interactive mode
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
./pi --mode print "explain this codebase"
./pi --mode json "list all TODO comments"
./pi --mode rpc                         # start RPC server
```

### Slash commands

| Command    | Description                          |
|------------|--------------------------------------|
| `/help`    | Show available commands              |
| `/model`   | Switch model mid-conversation        |
| `/session` | List and switch sessions             |
| `/branch`  | Create a conversation branch         |
| `/commit`  | Generate and apply a git commit      |
| `/compact` | Compact session history              |
| `/clear`   | Clear conversation                   |
| `/exit`    | Exit the agent                       |

## Configuration

Pi looks for configuration in `~/.pi-go/config.json` (global) and `.pi-go/config.json` (project-local):

- **Model roles** — Map role names to specific model strings
- **Hooks** — Shell commands triggered on tool events (e.g., post-write formatting)
- **MCP servers** — External tool servers via Model Context Protocol
- **Themes** — Terminal color schemes via `theme` config field

## License

See [LICENSE](LICENSE) for details.
