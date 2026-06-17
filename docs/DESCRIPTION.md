# pi-go Architecture Documentation

## Project Overview

**pi-go** is a terminal-based coding agent built on the Google ADK (Application Development Kit) with multi-provider LLM support, sandboxed tool execution, session persistence, interactive terminal UI, LSP integration, and subagent orchestration.

**Stack:**
- **Language:** Go 1.26.3+
- **Framework:** Google ADK Go (`google.golang.org/adk`) for agent/tooling
- **CLI:** Cobra
- **TUI:** Bubble Tea v2 with Glamour for Markdown rendering
- **Database:** SQLite (modernc.org/sqlite - pure Go, no CGO)
- **License:** Apache 2.0
- **Size:** 371 files, 73 directories, ~30K+ lines of Go code

---

## Core Architecture

### 1. Entry Point & CLI Layer

**Location:** `cmd/pi/`, `internal/cli/`

The application follows a modular monolithic structure:

```
Main Entry (cmd/pi/main.go)
└── Load `.env` files → Initialize OTEL tracing → cli.Execute()
        └── cobra.Command hierarchy
            ├── pi [prompt]  (root command)
            ├── pi ping
            ├── pi audit
            ├── pi serve (RPC mode)
            ├── pi memory (Memory Palace commands)
            ├── pi login (OAuth flows)
            └── pi acp-server (Agent CP protocol)
```

**Key Initialization Flow:**
1. Loads environment from `~/.pi-go/.env` and `.pi-go/.env` (project-local)
2. Detects OTEL collector availability
3. Parses CLI flags, resolves model role (default/smol/plan/slow)
4. Instantiates LLM provider based on selected role
5. Spawns output mode: **interactive** (TUI), **print**, **json**, or **rpc**

---

### 2. Agent Core

**Location:** `internal/agent/`

The agent is the heart of the system, built on Google ADK:

**Responsibilities:**
- ADK `llmagent.Agent` with streaming execution
- Tool execution loop with retry logic (`internal/agent/retry.go`)
- Callback system: BeforeTool/AfterTool callbacks for extensions
- Session management integration via ADK `session.Service`

**Retry Strategy:**
- Transient errors (429, 5xx, timeouts): Exponential backoff (1s → 2s → 4s, max 30s)
- Persistent errors: Fail immediately
- Max 3 retries by default
- Preserves partial results before retrying

---

### 3. LLM Provider System

**Location:** `internal/provider/`

**Multi-provider abstraction** implementing the ADK `model.LLM` interface:

| Provider | SDK | Models |
|----------|-----|--------|
| **Anthropic** | anthropic-sdk-go | claude-3.5, claude-4 |
| **OpenAI** | openai-go/v3 | gpt-4o, o1, o3, o4-series |
| **Gemini** | ADK native | gemini-pro, gemini-2.5 |
| **Ollama** | anthropic-compatible | Local models via `*:cloud` suffix |
| **Azure OpenAI** | openai-go | Enterprise deployments |

**Architecture Pattern:**
- `provider.Resolve()` → returns `Info` struct with provider, model name
- Auto-detects provider from model prefix (e.g., `claude` → `anthropic`)
- Custom OpenAI-compatible endpoints: Set via `--url` or `OPENAI_BASE_URL`
- **Guardrail wrapper** for daily token usage tracking

---

### 4. Tool System

**Location:** `internal/tools/`

**Sandboxed execution** via Go's `os.Root` for security:

```
CoreTools() -- 13 built-in tools --> All file operations restricted to working directory
├── read, write, edit, ls, tree
├── grep, find, bash (in sandbox dir, timeout-limited)
├── git-overview, git-file-diff, git-hunk
└── subagent (spawn child agents)

LSPTools(manager) -- 5 language tools
├── lsp-diagnostics
├── lsp-definition, lsp-references
├── lsp-hover, lsp-symbols

MemoryTools(store) -- 3 knowledge tools
├── mem-search (compact index)
├── mem-timeline (chronological context)
└── mem-get (full observation details)

PalaceTools(palace) -- Memory Palace tools
├── palace-search, palace-kg-query
├── palace-add-drawer, palace-diary-write
└── palace-traverse
```

**Sandbox Security:**
- All paths relative to working directory
- No escape via `..` or symlinks
- `os.Root` creates chroot-like sandbox
- Timeout enforcement (bash: 10min max, git: 10s, LSP: 5s default)

---

### 5. Session Persistence

**Location:** `internal/session/`

**JSONL append-only event logs** per-session:

```
~/.pi-go/sessions/{session-uuid}/
├── meta.json          (ID, model, timestamp, workDir)
├── events.jsonl       (append-only event log)
└── branches/
    ├── main/events.jsonl
    └── feature-x/events.jsonl
```

**Features:**
- **Branching:** Fork conversations, switch between branches
- **Compaction:** Summarize old events when token count exceeds threshold
- **JSONL Events:** `message_start`, `text_delta`, `tool_call`, `tool_result`, `message_end`
- **Resumability:** `--continue` resumes last session, `--session <id>` resumes specific

**Event Types:**
- **thinking:** Agent internal reasoning
- **user:** User prompt
- **assistant:** LLM response + tool calls
- Tool call events: `FunctionCall`/`FunctionResponse` parts

---

### 6. Terminal UI

**Location:** `internal/tui/`

**Bubble Tea v2** Elm Architecture:

```
Init()
  └── Deferred Initialization (background goroutine)
      ├── Phase 1: Core tools, sandbox
      ├── Phase 2: Parallel init (git detection, LSP, memory, MCP, skills)
      └── Phase 3: Agent builder (after all dependencies ready)

Update(msg) ←─→ View()
  ├─ KeyPressMsg → handleSlashCommand()
  ├─ agentMsg (channel) → appendEvent()
  ├─ WindowSizeMsg → resize handler
  └─ timerMsg → progress spinner

View() → render (lipgloss + glamour)
  ├─ renderMessages() → Markdown rendering
  ├─ renderInput() → text input
  ├─ renderStatusBar() → agent count, memory, token usage
  └─ renderSidebar() → session picker, memory status
```

**Slash Commands:**
```
/help          /model           /session
/branch        /context         /compact
/commit        /agents          /history
/plan          /run             /login
/skills        /theme           /rtk
/ping          /restart         /exit
/memory        /audit           /clear
```

**Deferred Init Pattern:**
- TUI shows spinner immediately
- Heavy I/O (git, LSP, memory, MCP) runs in parallel
- Agent created last after services ready
- `InitEvent{Result: InitResult}` signals readiness

---

### 7. Subagent System

**Location:** `internal/subagent/`

**Process-based multi-agent architecture:**

```
Orchestrator
├── Pool (concurrency limiter, max 5 concurrent)
├── Spawner (process forking to `pi` subprocess)
└── WorktreeManager (git worktree isolation)

Agent Types:
├── explore          → fast, read-only research (smol model)
├── plan             → analysis & planning (plan model)
├── designer         → code creation (slow model, worktree)
├── task             → full coding tasks (default model, worktree)
├── quick-task       → small focused tasks (smol model)
├── worker           → background processing (default model)
├── code-reviewer    → code review (slow model)
├── spec-reviewer    → design document review (slow model)
├── memory-compressor → observation compression, internal use (smol model)
├── discovery        → agent capability enumeration (smol model)
├── claude           → ACP bridge to Claude Code CLI
├── cursor           → ACP bridge to Cursor CLI
└── gemini           → ACP bridge to Gemini CLI
```

**Execution Flow:**
1. Main agent calls `agent` tool → orchestrator
2. Orchestrator validates agent type, resolves model role
3. Optionally creates git worktree (`pi-go/worktrees/`)
4. Spawns `pi` subprocess in JSON output mode
5. Events stream back via JSONL
6. Pool enforces max concurrency
7. Worktrees cleaned up after completion

---

### 8. Extension System

**Location:** `internal/extension/`

**Three-layer extensibility:**

1. **Hooks** (shell callbacks):
   - `BeforeToolCallbacks`, `AfterToolCallbacks`
   - Tool execution triggers shell commands
   - Pass tool name + args + results as JSON on stdin

2. **Skills** (`<name>/SKILL.md` subdirectories):
   - Markdown instruction files with YAML frontmatter (`name`, `description`, `tools`)
   - Loaded from global (`~/.pi-go/skills/<name>/SKILL.md`) and project (`.pi-go/skills/<name>/SKILL.md`) directories
   - Also reads `.claude/skills/` and `.cursor/skills/` when present
   - Project skills override user skills; user skills override bundled skills
   - **Audit system** scans for hidden Unicode threats (BiDi attacks, supply-chain)

3. **MCP Servers** (Model Context Protocol):
   - Launch external tool servers as subprocesses
   - Support HTTP/Streamable and stdio transports
   - Tools bridged into agent via ADK toolsets
   - Configurable in `~/.pi-go/config.json` or `.pi-go/mcp.json`

---

### 9. LSP Integration

**Location:** `internal/lsp/`

**Language server protocol integration** with auto-formatting:

```
LSP Manager
├── Protocol layer (JSON-RPC over stdio)
├── Client (gopls, tsserver, ruff, rust-analyzer)
├── Manager (lifecycle, caching)
└── Hooks (AfterToolCallback integration)

Format-on-Write:
  └── After `write`/`edit` → request format → apply edits (5s timeout)

Diagnostics-on-Edit:
  └── After file modification → collect errors (2s delay for server processing)

Explicit Tools:
  └── LLM can call lsp-* tools on demand
```

**Supported Languages:**
- **Go:** gopls
- **TypeScript/JS:** typescript-language-server
- **Python:** ruff (LSP mode)
- **Rust:** rust-analyzer

---

### 10. Memory Palace

**Location:** `internal/palace/`

**4-layer contextual memory** (SQLite + AI embeddings):

```
Layer L0: Identity
  → Static identity file (who/what/why)

Layer L1: Essential Story
  → Top-15 drawers by importance (injected into system prompt)

Layer L2: On-Demand Recall
  → Context-filtered drawer chunks

Layer L3: Search
  → Semantic (embedding) or keyword (FTS5) search
```

**Components:**
- **SQLite Store:** `palace.db` with FTS5 virtual tables
- **Embedder:** `KnightsAnalytics_all-MiniLM-L6-v2` (local model)
- **Knowledge Graph:** Triples (subject, predicate, object) with temporal edges
- **Miners:** Convo-miner (conversation patterns), Project-miner (codebase patterns)
- **Observation Bridge:** Auto-file observations as palace drawers

**CLI Commands:**
```bash
pi memory model download   # Download embedding model
pi memory init [dir]       # Create palace.db + config
pi memory mine <dir>       # Ingest source files as drawers
pi memory mine --convos    # Mine conversation JSONL
pi memory status           # Palace overview
pi memory search <query>   # Semantic/keyword search
pi memory wake-up          # L0+L1 context text
pi memory kg query         # Knowledge graph queries
```

---

### 11. Memory System

**Location:** `internal/memory/`

**Persistent memory compression** (inspired by `claude-mem`):

```
Tool Usage (AfterToolCallback)
  └── Enqueue to buffered channel (non-blocking)
      └── Background Goroutine
          └── Spawn memory-compressor subagent (smol model)
              └── Generate structured observation
                  └── Write to SQLite
```

**Database Schema:**
- **sessions:** User sessions with timestamps
- **observations:** Compressed tool usage events (FTS5 index)
- **session_summaries:** Aggregated project insights

**3-Layer Search Workflow:**
1. `mem-search(query)` → compact index with IDs (~50-100 tokens)
2. `mem-timeline(anchor=ID)` → chronological context
3. `mem-get(ids=[])` → full details (~500-1000 tokens)

**Observation Types:**
- `decision`, `bugfix`, `feature`, `refactor`, `discovery`, `change`

**Privacy Filtering:**
- PII detection (API key redaction)
- Configurable via `memory.privacy` config

---

### 12. Guardrail System

**Location:** `internal/guardrail/`

**Daily token usage tracking:**

```
LLM Request → guardrail.Tracker
└── Check against daily limit (configurable)
    └── Within limit → proceed
    └── Exceeds limit → LimitExceededError
```

**Metrics Tracked:**
- Input/output tokens per request
- Request count
- Daily total with percentage used

**Storage:** `~/.pi-go/usage.json` (resets at midnight)
- Format: `{"input_tokens": 12345, "output_tokens": 67890, "request_count": 42, "date": "2025-05-17"}`

---

### 13. Authentication System

**Location:** `internal/auth/`

**OAuth flows for API key management:**

```
Flows:
├── PKCE (Browser-based)
│   → Anthropic, Google Gemini
│   → Token exchange → API key stored in .env
│
└── Device Code (CLI-friendly)
    → OpenAI OAuth
    → Browser authorization → API key stored
```

**Components:**
- `LoginFlow` interfaces for each provider
- `TLSPreflight` for OpenAI certificate validation
- `.env` file as credential store (never sends raw tokens to servers)

---

### 14. Audit System

**Location:** `internal/audit/`

**Hidden character security scanner for skill files:**

**Detected Threats:**
- **U+200B-200F** (ZWSP, direction overrides) → Critical → Exit code 1
- **U+2028/29** (line/paragraph separators) → Warning → Exit code 2
- **U+00AD** (soft hyphen) → Info → Exit code 0
- **BOM** (byte order marks) → Info (at file start)

**Smart Contextualization:**
- ZWJ between emoji: Downgraded from critical to info
- **Auto-fix:** `--strip` removes dangerous chars (creates `.bak` backups)

---

### 15. Standard Operating Procedures

**Location:** `internal/sop/`

**Prompt-Driven Development (PDD) framework:**

```
PDD Workflow:
├── Phase 1: Skeleton (generate outline)
├── Phase 2: Requirements (Q&A with user)
├── Phase 3: Research (codebase exploration)
├── Phase 4: Design (architecture decisions)
├── Phase 5: Outline (high-level structure)
├── Phase 6: Plan (verbalized implementation steps)
└── Phase 7: PROMPT.md (concise briefing for autonomous execution)

Artifacts:
├── requirements.md       (scope & constraints)
├── research/             (findings, codebase analysis)
├── design.md             (architecture, components)
├── outline.md            (structural breakdown)
├── plan.md               (task checklist)
└── PROMPT.md             (compressed agent instruction)
```

---

## Configuration File Structure

```
~/.pi-go/                              # Global config
├── config.json                        # Model roles, hooks, MCP, guardrails
├── .env                               # API keys (from /login)
├── skills/*.SKILL.md                  # Global skills
├── sessions/                          # Conversation JSONL logs
├── memory/                            # Memory database (palace.db, claude-mem.db)
├── sops/pdd.md                        # Custom SOP template
├── models/                            # AI embedding models
└── log/YYYY-MM-DD/session-HH-MM-SS.log # Session logs

.pi-go/                                # Project-local config (overrides global)
├── config.json                        # Project-specific model/roles
├── AGENTS.md                          # Agent instructions for this repo
├── skills/*.SKILL.md                  # Project-specific skills
└── sops/                              # Project SOP artifacts
```

**Config Schema:**
```json
{
  "roles": {
    "default": { "model": "claude-sonnet-4-20250514" },
    "smol":    { "model": "claude-haiku-3-20240307" },
    "plan":    { "model": "claude-sonnet-4-20250514" },
    "slow":    { "model": "claude-opus-4-20250514" },
    "commit":  { "model": "o4-mini" }
  },
  "hooks": [...],
  "mcp": { "servers": [...] },
  "guardrail": { "max_daily_tokens": 0 },
  "memory": { "enabled": true },
  "palace": { "enabled": true },
  "compactor": { "enabled": true }
}
```

---

## Design Patterns

1. **Elm Architecture (TUI):** Init → Update → View cycle
2. **Deferred Initialization:** TUI shows immediately, subsystems load in background
3. **Sandboxing:** `os.Root` for secure file operations
4. **Retry with Exponential Backoff:** Resilient LLM calls
5. **Observer Pattern:** Callback chains for hooks, LSP, memory, compaction
6. **Chain of Responsibility:** BeforeTool → CoreTool → AfterTool
7. **Strategy Pattern:** Output modes (interactive/print/json/rpc)
8. **Repository Pattern:** Session, memory, palace store interfaces
9. **Factory Pattern:** Tool registration, provider resolution, LSP creation
10. **Command Pattern:** Slash commands, tool invocations

---

## Key Dependencies

**Core:**
- `google.golang.org/adk` → Agent, tool, session interfaces
- `charm.land/bubbletea/v2` → Terminal UI
- `modernc.org/sqlite` → Pure Go SQLite (no CGO)
- `github.com/modelcontextprotocol/go-sdk` → MCP protocol

**LLM SDKs:**
- `anthropics/anthropic-sdk-go`
- `openai/openai-go/v3`
- `ollama/ollama`

**Utilities:**
- `spf13/cobra` → CLI framework
- `charmbracelet/glamour` → Markdown rendering
- `golang.org/x/exp` → Generics, iterators
- `go.opentelemetry.io/otel` → OpenTelemetry tracing

---

## Summary

pi-go is a **production-grade coding agent** with:
- **Modular architecture** built on Google ADK
- **Multi-provider LLM** flexibility
- **Secure tool execution** via sandboxing
- **Persistent memory** across sessions
- **Rich extensibility** (hooks, skills, MCP)
- **Interactive TUI** with full streaming
- **Subagent orchestration** for parallel tasks
- **Language intelligence** via LSP integration
- **OAuth authentication** for API credentials
- **Security scanning** for supply-chain threats

The codebase is well-tested (most packages have comprehensive test suites), uses modern Go patterns (generics, iterators, error wrapping), and follows the ADK's native interfaces rather than creating custom abstractions.
