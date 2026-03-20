# Research: pi-go Architecture for Benchmark Integration

## Source
Local codebase exploration of `/Users/dimetron/p6s/pi-dev/pi-go`

## 1. CLI Entry Point

**File**: `cmd/pi/main.go` → calls `cli.Execute()`

**Key Flags**:
```
--model <model>       # LLM model
--mode <mode>         # interactive, print, json, rpc
--socket <path>       # Unix socket for RPC
--session <id>        # Resume session
--continue            # Continue last session
--smol/--slow/--plan  # Role shortcuts
--system <instruction># Override system prompt
```

**Output Modes**:
1. **interactive** — Bubble Tea TUI (default for TTY)
2. **print** — text to stdout, status to stderr (default for pipe)
3. **json** — JSONL streaming events
4. **rpc** — Unix socket JSON-RPC 2.0

## 2. Tool Execution System

**Package**: `internal/tools/`

**Core Tools**: read, write, edit, bash, grep, find, ls, tree, git-overview, git-file-diff, git-hunk, lsp-*, agent, screen, restart

**Sandbox** (`sandbox.go`):
- Uses `os.Root` to restrict filesystem access to CWD
- Prevents path traversal via `..` or symlinks

**Tool Interface**: Implements `google.golang.org/adk/tool.Tool` with JSON schema validation

## 3. Session Management

**Package**: `internal/session/store.go`

**File Layout**:
```
~/.pi-go/sessions/<session-uuid>/
├── meta.json          # ID, AppName, UserID, WorkDir, Model, timestamps
├── events.jsonl       # Append-only event log
└── branches/          # Optional branching
```

**Key Methods**:
- `Create()`, `Get()`, `List()`, `AppendEvent()`, `Delete()`
- `Compact()` — summarize old events to save context
- `EstimateTokens()` — estimate session context size
- `LastSessionID()` — for `--continue` flag

## 4. Provider System

**Package**: `internal/provider/`

**Model Resolution** (`provider.go`):
- `claude*` → anthropic
- `gpt*`, `o1*`, `o3*`, `o4*` → openai
- `gemini*` → gemini
- `qwen`, `llama`, `mistral`, etc. → ollama

**Providers**: Anthropic, OpenAI, Gemini, Ollama — all implement ADK `model.LLM` interface

**API Key Detection**: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`, `GEMINI_API_KEY`

## 5. Subagent System

**Package**: `internal/subagent/`

**Orchestrator** spawns child `pi` processes in JSON mode:
```
pi --mode json --model <model> --system <instruction> <prompt>
```

**Pool**: Max 5 concurrent subagents (semaphore-based)

**Bundled Agent Types**: explore, plan, designer, reviewer, task, quick_task

**Spawner** filters environment to block API keys from subagent processes via `environ.go`

## 6. Configuration

**Locations**:
```
~/.pi-go/config.json       # Global
.pi-go/config.json          # Project (overrides global)
.pi-go/AGENTS.md            # Project agent instructions
~/.pi-go/skills/*.SKILL.md  # Global skills
.pi-go/skills/*.SKILL.md    # Project skills
~/.pi-go/.env               # API keys
~/.pi-go/log/               # Session logs
```

**Config Structure**: Roles (default/smol/slow/plan), ThinkingLevel, Tools, MCP, Hooks, Compactor

## 7. Integration Points for Benchmark Framework

| Method | How | Best For |
|--------|-----|----------|
| **JSON mode** | `pi --mode json <prompt>` → parse JSONL from stdout | External process orchestration (Harbor) |
| **Print mode** | `pi --mode print <prompt>` → text stdout | Simple evaluation scripts |
| **RPC mode** | Unix socket JSON-RPC 2.0 | Long-running interactive sessions |
| **Go API** | Import `agent.Run()` / `agent.RunStreaming()` | In-process Go integration |
| **Subagent spawner** | `Orchestrator.Spawn()` | Parallel task execution |

**JSON Output Events**:
```json
{"type": "message_start", "agent": "pi", "role": "assistant"}
{"type": "text_delta", "agent": "pi", "delta": "text chunk"}
{"type": "tool_call", "agent": "pi", "tool_name": "read", "tool_input": {...}}
{"type": "tool_result", "agent": "pi", "tool_name": "read", "content": "result"}
{"type": "message_end"}
```

## Key Observation

The most natural integration path for Harbor/Terminal-Bench is **JSON mode** (`pi --mode json`), which provides structured JSONL streaming that an adapter can parse. This mirrors how the existing `pi-terminal-bench` adapter invokes `pi` as a subprocess.
