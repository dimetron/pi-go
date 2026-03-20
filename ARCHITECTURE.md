# pi-go Architecture

## Overview

pi-go is a coding agent built on [Google ADK Go](https://google.golang.org/adk) with multi-provider LLM support, sandboxed tool execution, session persistence, an interactive terminal UI, LSP integration, and a subagent orchestration system.

## Package Structure

```
pi-go/
├── cmd/pi/main.go                  # Entry point → cli.Execute()
└── internal/
    ├── agent/                       # ADK agent setup, retry logic
    ├── cli/                         # CLI flags, output modes, wiring
    ├── config/                      # Config loading (global + project), model roles
    ├── extension/                   # Hooks, skills, MCP integration
    ├── lsp/                         # LSP integration (protocol, client, manager, languages, hooks)
    ├── provider/                    # LLM providers (Anthropic, OpenAI, Gemini)
    ├── rpc/                         # Unix socket JSON-RPC server
    ├── session/                     # JSONL persistence, branching, compaction
    ├── subagent/                    # Subagent orchestration (pool, spawner, worktree, orchestrator)
    ├── tools/                       # Sandboxed tools (read, write, edit, bash, grep, find, ls, tree, git, lsp, agent)
    └── tui/                         # Bubble Tea v2 interactive UI
```

## Dependency Graph

```mermaid
graph TD
    main["cmd/pi/main.go"] --> cli["cli"]
    cli --> agent["agent"]
    cli --> config["config"]
    cli --> provider["provider"]
    cli --> tools["tools"]
    cli --> extension["extension"]
    cli --> session["session"]
    cli --> tui["tui"]
    cli --> rpc["rpc"]
    cli --> subagent["subagent"]
    cli --> lsp["lsp"]

    agent --> adk_runner["ADK runner"]
    agent --> adk_llmagent["ADK llmagent"]
    agent --> adk_session["ADK session"]

    provider --> anthropic_sdk["anthropic-sdk-go"]
    provider --> openai_sdk["openai-go"]
    provider --> adk_gemini["ADK model/gemini"]

    tools --> sandbox["os.Root sandbox"]
    tools --> adk_tool["ADK tool/functiontool"]

    subagent --> config
    subagent --> provider

    lsp --> config

    tui --> bubbletea["Bubble Tea v2"]
    tui --> glamour["Glamour (markdown)"]
    tui --> agent

    rpc --> agent

    extension --> mcp_sdk["MCP Go SDK"]

    session --> adk_session

    style main fill:#2d5016,color:#fff
    style cli fill:#1a3a5c,color:#fff
    style agent fill:#1a3a5c,color:#fff
    style provider fill:#5c1a3a,color:#fff
    style tools fill:#3a5c1a,color:#fff
    style tui fill:#5c3a1a,color:#fff
    style session fill:#1a5c5c,color:#fff
    style subagent fill:#3a1a5c,color:#fff
    style lsp fill:#5c5c1a,color:#fff
```

## Request Flow

```mermaid
sequenceDiagram
    participant U as User
    participant CLI as CLI / TUI
    participant A as Agent
    participant R as ADK Runner
    participant LLM as LLM Provider
    participant T as Tool (sandboxed)
    participant S as Session Store

    U->>CLI: prompt text
    CLI->>A: Run(ctx, sessionID, message)
    A->>R: runner.Run(content)
    R->>LLM: GenerateContent(req)
    LLM-->>R: Response (text or tool call)

    alt Tool Call
        R->>T: Execute tool
        T-->>R: Tool result
        R->>LLM: GenerateContent(with tool result)
        LLM-->>R: Final text response
    end

    R->>S: AppendEvent(event)
    R-->>A: yield events
    A-->>CLI: iter.Seq2[Event, error]
    CLI-->>U: render output
```

## Tool System

```mermaid
graph LR
    subgraph Sandbox["os.Root Sandbox (cwd)"]
        read["read<br/>Read file with line numbers"]
        write["write<br/>Write/create file"]
        edit["edit<br/>Find & replace in file"]
        ls["ls<br/>List directory"]
        tree["tree<br/>Directory tree view"]
        find["find<br/>Glob file search"]
        grep["grep<br/>Regex content search"]
    end

    subgraph GitTools["Git Tools"]
        git_overview["git-overview<br/>Repo status & info"]
        git_file_diff["git-file-diff<br/>Unified file diff"]
        git_hunk["git-hunk<br/>Parsed diff hunks"]
    end

    subgraph LSPTools["LSP Tools"]
        lsp_diag["lsp-diagnostics<br/>Errors & warnings"]
        lsp_def["lsp-definition<br/>Go to definition"]
        lsp_ref["lsp-references<br/>Find references"]
        lsp_hover["lsp-hover<br/>Type info & docs"]
        lsp_sym["lsp-symbols<br/>Document symbols"]
    end

    bash["bash<br/>Shell command<br/>(runs in sandbox dir)"]
    agent_tool["agent<br/>Spawn subagent"]

    registry["CoreTools(sandbox)"] --> read
    registry --> write
    registry --> edit
    registry --> bash
    registry --> grep
    registry --> find
    registry --> ls
    registry --> tree
    registry --> git_overview
    registry --> git_file_diff
    registry --> git_hunk

    lsp_registry["LSPTools(manager)"] --> lsp_diag
    lsp_registry --> lsp_def
    lsp_registry --> lsp_ref
    lsp_registry --> lsp_hover
    lsp_registry --> lsp_sym

    agent_registry["AgentTool(orchestrator)"] --> agent_tool

    style Sandbox fill:#1a2a1a,stroke:#4a4,color:#fff
    style GitTools fill:#1a1a2a,stroke:#44a,color:#fff
    style LSPTools fill:#2a1a1a,stroke:#a44,color:#fff
    style registry fill:#333,color:#fff
    style lsp_registry fill:#333,color:#fff
    style agent_registry fill:#333,color:#fff
```

All file tools operate through the `Sandbox` which uses Go's `os.Root` to restrict access to the working directory tree. Paths cannot escape via `..` or symlinks.

| Tool | Input | Output | Limits |
|------|-------|--------|--------|
| read | file_path, offset, limit | content, total_lines | 2000 lines default, 100KB |
| write | file_path, content | path, bytes_written | Auto-creates parent dirs |
| edit | file_path, old_string, new_string | path, replacements | Unique match required |
| bash | command, timeout | stdout, stderr, exit_code | 2min default, 10min max |
| grep | pattern, path, glob | matches, total_matches | 200 matches max |
| find | pattern, path | files, total_files | 500 results max |
| ls | path | entries (name, is_dir, size) | — |
| tree | path, depth | tree, dirs, files | Depth 10 max, 500 entries |
| git-overview | — | branch, commits, staged, unstaged, untracked | 10s timeout |
| git-file-diff | file, staged | diff | 10s timeout |
| git-hunk | file, staged | hunks (header, content, lines) | 10s timeout |

## Model Roles

The model roles system maps abstract role names to specific LLM models, enabling different components to use appropriate models for their task complexity.

```
config.json:
{
  "roles": {
    "default": { "model": "claude-sonnet-4-20250514" },
    "smol":    { "model": "claude-haiku-3-20240307" },
    "plan":    { "model": "claude-sonnet-4-20250514" },
    "slow":    { "model": "claude-opus-4-20250514" }
  }
}
```

`ResolveRole(role)` resolves a role name to a model and provider. Falls back to "default" role if the requested role is not configured. The provider is auto-detected from the model name prefix (claude→anthropic, gpt/o1-4→openai, gemini→gemini).

CLI flags `--smol`, `--plan`, `--slow` override the active role for a single invocation.

## Subagent System

The subagent system enables the main agent to spawn autonomous child agents for parallel task execution.

```mermaid
graph TD
    agent_tool["agent tool<br/>(LLM-initiated)"] --> orchestrator["Orchestrator"]

    orchestrator --> pool["Pool<br/>Concurrency limiter<br/>(max 5)"]
    orchestrator --> spawner["Spawner<br/>Process manager"]
    orchestrator --> worktree["WorktreeManager<br/>Git worktree isolation"]

    spawner --> subprocess["pi subprocess<br/>(JSON output mode)"]
    worktree --> git["git worktree<br/>.pi-go/worktrees/"]

    subgraph AgentTypes["Agent Types"]
        explore["explore<br/>Fast read-only<br/>(smol model)"]
        plan["plan<br/>Analysis & planning<br/>(plan model)"]
        designer["designer<br/>Code creation<br/>(slow model, worktree)"]
        reviewer["reviewer<br/>Code review<br/>(slow model)"]
        task["task<br/>Full coding tasks<br/>(default model, worktree)"]
        quick_task["quick_task<br/>Small tasks<br/>(smol model)"]
    end

    orchestrator --> AgentTypes

    style orchestrator fill:#3a1a5c,color:#fff
    style pool fill:#1a3a5c,color:#fff
    style spawner fill:#1a3a5c,color:#fff
    style worktree fill:#1a3a5c,color:#fff
    style AgentTypes fill:#1a1a2a,color:#fff
```

Each agent type defines: model role, worktree isolation, system instruction, and allowed tools. The orchestrator validates agent type, resolves the model via roles, acquires a pool slot, optionally creates a git worktree for isolation, and spawns a `pi` subprocess in JSON output mode. Events stream back via JSONL.

## LSP Integration

The LSP system provides language intelligence through two mechanisms:

**Hooks** (automatic, via `AfterToolCallback`):
- **Format-on-write**: After `write` or `edit` tool calls, requests formatting from the language server and applies edits (5s timeout)
- **Diagnostics-on-edit**: After file modifications, collects compiler errors/warnings with a 2s delay for server processing

**Explicit tools** (LLM-invoked):
- `lsp-diagnostics` — Get errors and warnings for a file
- `lsp-definition` — Go to definition of symbol at position
- `lsp-references` — Find all references to a symbol
- `lsp-hover` — Get type information and documentation
- `lsp-symbols` — List all symbols in a file

The `Manager` starts language servers on demand based on file extension, caches connections, and shuts them down on exit. Supported languages: Go (gopls), TypeScript (typescript-language-server), Python (pylsp), Rust (rust-analyzer).

## Provider System

```mermaid
graph TD
    resolve["provider.Resolve(modelName)"]

    resolve -->|"claude*"| anthropic["Anthropic<br/>anthropic-sdk-go"]
    resolve -->|"gpt*, o1*, o3*, o4*"| openai["OpenAI<br/>openai-go"]
    resolve -->|"gemini*"| gemini["Gemini<br/>ADK native"]
    resolve -->|"*:cloud"| ollama["Ollama<br/>Anthropic-compatible API"]

    anthropic --> llm["model.LLM interface"]
    openai --> llm
    gemini --> llm
    ollama --> anthropic

    llm --> agent["Agent"]

    style resolve fill:#333,color:#fff
    style llm fill:#1a3a5c,color:#fff
```

Each provider implements the ADK `model.LLM` interface:

```go
type LLM interface {
    Name() string
    GenerateContent(ctx, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error]
}
```

**API keys** from environment: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`
**Base URLs** from environment: `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GEMINI_BASE_URL`

## Session Management

```mermaid
graph TD
    subgraph Storage["~/.pi-go/sessions/"]
        subgraph Session["<session-uuid>/"]
            meta["meta.json<br/>ID, AppName, UserID,<br/>WorkDir, Model, timestamps"]
            events["events.jsonl<br/>Append-only event log"]
            subgraph Branches["branches/"]
                main["main/events.jsonl"]
                feat["feature-x/events.jsonl"]
            end
            bstate["branches.json<br/>Active branch state"]
        end
    end

    create["CreateSession"] --> meta
    create --> events
    append["AppendEvent"] --> events
    branch["CreateBranch"] --> Branches
    branch --> bstate
    compact["Compact"] -->|"summarize old events"| events

    style Storage fill:#0a1a2a,color:#fff
    style Session fill:#1a2a3a,color:#fff
```

- **Persistence**: JSONL append-only event log per session
- **Branching**: Fork conversations, switch between branches
- **Compaction**: Replace old events with summary when token count exceeds threshold
- **Resume**: `--continue` resumes last session, `--session <id>` resumes specific session

## Output Modes

```mermaid
graph LR
    agent["Agent Events"] --> mode{Output Mode}
    mode -->|"interactive<br/>(tty default)"| tui["TUI<br/>Bubble Tea + Markdown"]
    mode -->|"print<br/>(pipe default)"| print["Print<br/>Text → stdout<br/>Status → stderr"]
    mode -->|"json"| json["JSON<br/>JSONL streaming events"]
    mode -->|"rpc"| rpc["RPC<br/>Unix socket JSON-RPC 2.0"]

    style mode fill:#333,color:#fff
```

**JSON event types**: `message_start`, `text_delta`, `tool_call`, `tool_result`, `message_end`

## Extension System

```mermaid
graph TD
    subgraph Extensions
        hooks["Hooks<br/>Shell commands<br/>before/after tool calls"]
        skills["Skills<br/>*.SKILL.md files<br/>Reusable instructions"]
        mcp["MCP Servers<br/>External tool providers<br/>via subprocess"]
    end

    config["config.json"] --> hooks
    skilldir["~/.pi-go/skills/<br/>.pi-go/skills/"] --> skills
    config --> mcp

    hooks --> agent["Agent Callbacks"]
    skills --> agent
    mcp --> agent

    style Extensions fill:#1a1a2a,color:#fff
```

**Hooks**: Execute shell commands before/after tool execution. Tool name + args/results passed as JSON on stdin.

**Skills**: Markdown instruction files with YAML frontmatter. Loaded from global and project directories.

**MCP**: Launch external tool servers as subprocesses. Tools bridged into agent's toolset via ADK.

## Configuration

```
~/.pi-go/config.json          # Global config
.pi-go/config.json             # Project config (overrides global)
.pi-go/AGENTS.md               # Project-specific agent instructions
~/.pi-go/skills/*.SKILL.md     # Global skills
.pi-go/skills/*.SKILL.md       # Project skills (override global)
~/.pi-go/sessions/             # Session storage
```

## Retry & Error Handling

```mermaid
graph TD
    call["LLM Call"] --> check{Error?}
    check -->|No| done["Success"]
    check -->|Yes| transient{Transient?}
    transient -->|"429, 5xx,<br/>timeout, reset"| retry["Wait (exp backoff)<br/>1s → 2s → 4s"]
    transient -->|"400, auth,<br/>other"| fail["Fail immediately"]
    retry --> attempt{Retries<br/>exhausted?}
    attempt -->|No| call
    attempt -->|Yes| fail

    style retry fill:#5c5c1a,color:#fff
    style fail fill:#5c1a1a,color:#fff
    style done fill:#1a5c1a,color:#fff
```

Defaults: 3 retries, 1s initial delay, 30s max delay. Partial results prevent retry to preserve data integrity.

## TUI Architecture

```mermaid
graph TD
    subgraph BubbleTea["Bubble Tea v2"]
        init["Init()"] --> loop["Update/View Loop"]
        loop --> key["KeyPressMsg"]
        loop --> agent_msg["agentMsg (channel)"]
        loop --> resize["WindowSizeMsg"]
    end

    key -->|Enter| submit["submit()"]
    key -->|"/cmd"| slash["handleSlashCommand()"]
    submit --> goroutine["Agent goroutine"]
    goroutine -->|"agentTextMsg<br/>agentToolCallMsg<br/>agentToolResultMsg<br/>agentDoneMsg"| agent_msg

    agent_msg --> render["View()"]
    render --> messages["renderMessages()"]
    render --> status["renderStatusBar()"]
    render --> input["renderInput()"]

    messages --> markdown["Glamour<br/>Markdown Render"]

    style BubbleTea fill:#1a2a1a,color:#fff
```

**Slash commands**: `/help`, `/clear`, `/model`, `/session`, `/branch`, `/compact`, `/commit`, `/agents`, `/exit`

**Keyboard**: Enter (submit), Ctrl+C/Esc (quit), Up/Down (history), PgUp/PgDown (scroll), Enter/Esc (commit confirm/cancel)

## Memory System (Planned)

Persistent memory compression system inspired by [claude-mem](https://github.com/thedotmack/claude-mem), implemented natively in Go.

```mermaid
graph TD
    subgraph Capture["Observation Capture"]
        after_cb["AfterToolCallback"] -->|"enqueue"| queue["Buffered Channel"]
        queue --> bg["Background Goroutine"]
    end

    subgraph Compress["AI Compression"]
        bg --> spawner["Subagent Spawner"]
        spawner --> compressor["memory-compressor<br/>(smol model)"]
        compressor -->|"structured observation"| db
    end

    subgraph Store["SQLite Storage (~/.pi-go/memory/)"]
        db["claude-mem.db<br/>WAL mode"]
        db --- sessions_t["sessions"]
        db --- obs_t["observations<br/>+ FTS5"]
        db --- sum_t["session_summaries<br/>+ FTS5"]
    end

    subgraph Retrieve["Context & Search"]
        start["SessionStart"] -->|"inject context"| instruction["System Instruction"]
        search_tool["mem-search tool"] --> db
        timeline_tool["mem-timeline tool"] --> db
        get_obs_tool["mem-get tool"] --> db
    end

    style Capture fill:#1a2a1a,color:#fff
    style Compress fill:#2a1a2a,color:#fff
    style Store fill:#1a1a2a,color:#fff
    style Retrieve fill:#1a2a3a,color:#fff
```

**Core Components:**
- **Observation Capture**: `AfterToolCallback` enqueues tool usage to a buffered channel (non-blocking)
- **AI Compression**: Background goroutine spawns `memory-compressor` subagent (smol model) to extract structured observations
- **SQLite Storage**: `modernc.org/sqlite` (pure Go, no CGO) with FTS5 full-text search
- **Context Injection**: Recent observations injected into system instruction at session start
- **Search Tools**: Native `mem-search`, `mem-timeline`, `mem-get` tools registered in `CoreTools()`

**Data Model:**
| Table | Key Fields |
|-------|------------|
| sessions | id, session_id, project, started_at, status |
| observations | id, session_id, project, title, type, text, source_files, created_at |
| session_summaries | id, session_id, project, request, investigated, learned, completed, next_steps |

**3-Layer Search Workflow:**
1. `mem-search(query)` — compact index with IDs (~50-100 tokens/result)
2. `mem-timeline(anchor=ID)` — chronological context around results
3. `mem-get(ids=[...])` — full details for filtered IDs (~500-1000 tokens/result)

See `specs/claude-mem/` for the full design specification.
