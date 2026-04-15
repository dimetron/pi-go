# PI-ARCH: pi-go Internal Architecture

> **Purpose**: Detailed internal architecture reference for pi-go contributors and extending developers.
> **Audience**: Developers who want to understand, modify, or extend pi-go's internals.
> **Companion**: See [ARCHITECTURE.md](./ARCHITECTURE.md) for the high-level overview.

---

## Table of Contents

1. [System Context](#1-system-context)
2. [Package Dependency Graph](#2-package-dependency-graph)
3. [ADK Integration Layer](#3-adk-integration-layer)
4. [Agent Initialization Pipeline](#4-agent-initialization-pipeline)
5. [Tool Execution Flow](#5-tool-execution-flow)
6. [Session & Memory Architecture](#6-session--memory-architecture)
7. [Subagent Orchestration](#7-subagent-orchestration)
8. [TUI Rendering Pipeline](#8-tui-rendering-pipeline)
9. [Extension Points](#9-extension-points)
10. [Data Flow Diagrams](#10-data-flow-diagrams)

---

## 1. System Context

```mermaid
C4Context
    title System Context - pi-go Coding Agent

    Person(user, "Developer", "Uses pi-go for coding tasks")
    System(pi, "pi-go", "Coding agent with tool calling, session persistence, and subagent support")

    System_Ext(cli, "Terminal/Shell", "User's shell environment")
    System_Ext(llm, "LLM Provider", "Anthropic Claude / OpenAI / Gemini / Ollama")
    System_Ext(lsp, "Language Server", "gopls, tsserver, rust-analyzer")
    System_Ext(mcp, "MCP Servers", "External tool providers via MCP protocol")
    System_Ext(git, "Git Repository", "Codebase under version control")

    Rel(user, cli, "interacts with")
    Rel(cli, pi, "invokes")
    Rel(pi, llm, "GenerateContent")
    Rel(pi, lsp, "LSP calls")
    Rel(pi, mcp, "MCP protocol")
    Rel(pi, git, "git commands")
```

### External Boundaries

| Boundary          | Description                                                  |
|-------------------|--------------------------------------------------------------|
| **LLM Providers** | HTTP APIs (Anthropic, OpenAI, Gemini) or local (Ollama)      |
| **File System**   | Restricted to working directory via `os.Root` sandbox        |
| **Git**           | Read/write via subprocess, workspace isolation via worktrees |
| **LSP Servers**   | Stdio-based IPC per language                                 |
| **MCP Protocol**  | Subprocess transport, JSON-RPC 1.0                           |

---

## 2. Package Dependency Graph

```mermaid
flowchart TB
    subgraph entry["cmd/pi"]
        main["main.go"]
    end

    subgraph core["Core Packages"]
        CLI["cli"]
        Agent["agent"]
        Config["config"]
        Provider["provider"]
        Session["session"]
    end

    subgraph tools["Tool System"]
        Tools["tools"]
        Sandbox["sandbox"]
        LSP["lsp"]
        Redact["redact"]
    end

    subgraph agents["Agent System"]
        Subagent["subagent"]
        Palace["palace"]
    end

    subgraph memory["Memory System"]
        Memory["memory"]
        Atif["atif"]
    end

    subgraph ui["User Interface"]
        TUI["tui"]
        RPC["jsonrpc"]
    end

    subgraph ext["Extension System"]
        Extension["extension"]
        Hooks["hooks"]
        Skills["skills"]
        MCP["mcp"]
    end

    subgraph infra["Infrastructure"]
        Guardrail["guardrail"]
        Logger["logger"]
        Auth["auth"]
        Audit["audit"]
        Sop["sop"]
        WebServer["webserver"]
    end

    main --> CLI
    CLI --> Config
    CLI --> Provider
    CLI --> Agent
    CLI --> Session
    CLI --> Tools
    CLI --> TUI
    CLI --> RPC
    CLI --> Subagent
    CLI --> LSP
    CLI --> Guardrail
    CLI --> Auth
    CLI --> Logger
    CLI --> Audit

    Agent --> Provider
    Agent --> Session
    Agent --> Extension

    Tools --> Sandbox
    Tools --> LSP
    Tools --> Redact
    Tools --> Atif

    Subagent --> Config
    Subagent --> Provider
    Subagent --> Agent

    Palace --> Memory

    TUI --> Agent
    TUI --> CLI
    TUI --> Config
    TUI --> LSP
    TUI --> Palace

    Extension --> Tools
    Extension --> MCP
    Extension --> Skills

    Memory --> Config
    Memory --> Subagent

    Guardrail --> Provider
    Guardrail --> CLI

    Logger --> CLI

    Auth --> CLI
    Sop --> Agent
```

### Package Purposes

| Package     | Responsibility                                                 |
|-------------|----------------------------------------------------------------|
| `cmd/pi`    | Entry point, CLI flag parsing                                  |
| `cli`       | Mode routing (interactive/print/json/rpc), deferred init       |
| `agent`     | ADK LLM Agent + Runner setup, retry logic                      |
| `config`    | YAML/JSON config loading, role resolution                      |
| `provider`  | Multi-provider LLM factory (Anthropic, OpenAI, Gemini, Ollama) |
| `session`   | JSONL persistence, branching, compaction                       |
| `tools`     | Tool registration, sandboxing, LSP integration                 |
| `tui`       | Bubble Tea TUI, markdown rendering                             |
| `subagent`  | Pool, spawner, worktree management                             |
| `lsp`       | Language server protocol client                                |
| `extension` | Hooks, skills, MCP integration                                 |
| `guardrail` | Daily token usage tracking                                     |
| `memory`    | SQLite persistence, AI compression                             |
| `palace`    | Memory palace (drawers, knowledge graph, layers)               |
| `atif`      | Agent Trajectory Interchange Format                            |
| `logger`    | Session logging to JSON files                                  |
| `auth`      | OAuth PKCE/device-code login                                   |
| `audit`     | Hidden character scanner                                       |
| `sop`       | Standard Operating Procedures                                  |
| `webserver` | Web-based terminal sharing                                     |
| `jsonrpc`   | Unix socket JSON-RPC 2.0 server                                |

---

## 3. ADK Integration Layer

```mermaid
classDiagram
    class Agent {
        runner *Runner
        sessionService Service
        config Config
        +New(cfg Config) *Agent
        +Run(ctx, sessionID, msg) iter.Seq2[Event, error]
        +RebuildWithInstruction(instruction string)
    }

    class Runner {
        llmAgent *LLMAgent
        sessionService Service
        +Run(ctx, sessionID, content) iter.Seq2[Event, error]
    }

    class LLMAgent {
        model Model
        tools []Tool
        instruction string
        +Run(ctx, req) iter.Seq2[Event, error]
    }

    class Model {
        <<interface>>
        +Name() string
        +GenerateContent(ctx, *LLMRequest, bool) iter.Seq2[*LLMResponse, error]
    }

    class Tool {
        <<interface>>
        +Name() string
        +Description() string
        +Execute(ctx, args) (*tool.Response, error)
    }

    class Toolset {
        <<interface>>
        +Name() string
        +GetTools(ctx) []Tool
    }

    class Service {
        <<interface>>
        +Create(ctx, *CreateRequest) *CreateResponse
        +Get(ctx, *GetRequest) *GetResponse
        +AppendEvent(ctx, Session, *Event) error
    }

    class Event {
        timestamp time.Time
        author string
        content []*Content
        actions []Action
    }

    class BeforeToolCallback {
        +BeforeToolCall(ctx, tool, args) error
    }

    class AfterToolCallback {
        +AfterToolCall(ctx, tool, args, result) error
    }

    Agent --> Runner : creates
    Agent --> LLMAgent : creates
    Runner --> Service : uses
    Runner --> LLMAgent : runs
    LLMAgent --> Model : calls
    LLMAgent --> Tool : invokes
    LLMAgent --> Toolset : uses
    LLMAgent --> BeforeToolCallback : before tool
    LLMAgent --> AfterToolCallback : after tool
    Runner --> Event : yields
```

### Key ADK Interfaces

```go
// model.LLM — LLM provider interface
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error]
}

// tool.Tool — tool interface
type Tool interface {
    Name() string
    Description() string
    Schema() *Schema
    Execute(ctx context.Context, args []byte) (*Response, error)
}

// tool.Toolset — grouped tools
type Toolset interface {
    Name() string
    GetTools(ctx context.Context) ([]Tool, error)
}

// session.Service — session persistence
type Service interface {
    Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error)
    Get(ctx context.Context, req *GetRequest) (*GetResponse, error)
    List(ctx context.Context, req *ListRequest) (*ListResponse, error)
    Delete(ctx context.Context, req *DeleteRequest) error
    AppendEvent(ctx context.Context, curSession Session, event *Event) error
}
```

---

## 4. Agent Initialization Pipeline

```mermaid
sequenceDiagram
    participant Main as cmd/pi/main.go
    participant CLI as cli/cli.go
    participant Config as config.Load()
    participant Provider as provider.NewLLM()
    participant Guardrail as guardrail.WrapModel()
    participant TUI as tui.Init()
    participant Init as deferredInit()
    participant Tools as tools.CoreTools()
    participant LSP as lsp.NewManager()
    participant Memory as memory.OpenDB()
    participant MCP as extension.LoadMCP()
    participant Skills as extension.LoadSkills()
    participant Subagent as subagent.NewOrchestrator()
    participant Agent as agent.New()

    Main->>CLI: Execute()
    CLI->>Config: Load config
    CLI->>Provider: Resolve model + create LLM
    CLI->>Guardrail: Wrap with token tracking
    CLI->>TUI: Init()

    Note over TUI: UI starts immediately with spinner

    par Deferred Init in background goroutine
        TUI->>Init: Start goroutine
        Init->>Tools: Phase 1: Sandbox + CoreTools
        Init->>Tools: Create ScreenTool, RestartTool
    and
        par Parallel (WaitGroup)
            Init->>LSP: Create manager
            Init->>Memory: Open DB, create worker
            Init->>MCP: Launch servers
            Init->>Skills: Load *.SKILL.md files
            Init->>Subagent: Create orchestrator
        end
    end

    Init->>Tools: Register LSP tools
    Init->>Tools: Register Memory tools
    Init->>Tools: Register Agent tools
    Init->>Agent: Build agent with all components

    Init->>TUI: InitEvent{Result: InitResult}
    TUI->>User: Ready to accept input
```

### Initialization Phases

| Phase       | Components                                  | Duration   |
|-------------|---------------------------------------------|------------|
| **Phase 1** | Sandbox, CoreTools, ScreenTool, RestartTool | Sequential |
| **Phase 2** | Git, LSP, Memory, MCP, Skills               | Parallel   |
| **Phase 3** | Orchestrator, Agent builder                 | Sequential |

---

## 5. Tool Execution Flow

```mermaid
flowchart LR
    subgraph LLM["LLM Inference"]
        LLMReq["LLM Request"]
        LLMResp["Response"]
    end

    subgraph ADK["ADK Runner"]
        Runner["Runner.Run()"]
        BeforeCB["BeforeToolCallback"]
        AfterCB["AfterToolCallback"]
    end

    subgraph Tools["Tool System"]
        ToolReg["Tool Registry"]
        Sandbox["os.Root Sandbox"]
        Coerce["Parameter Coercion"]
    end

    subgraph Extensions["Extensions"]
        Hooks["Hooks"]
        LSP["LSP Hooks"]
        Memory["Memory Capture"]
    end

    LLMReq --> Runner
    Runner --> BeforeCB
    BeforeCB --> ToolReg
    ToolReg --> Coerce
    Coerce --> Sandbox
    Sandbox --> AfterCB
    AfterCB --> Hooks
    AfterCB --> LSP
    AfterCB --> Memory
    LSP --> LLMResp
    Hooks --> LLMResp
    Memory --> LLMResp
```

### Tool Lifecycle

```
1. LLM generates tool call request
       ↓
2. Runner invokes BeforeToolCallbacks
   - Extension hooks (before)
       ↓
3. Tool selected from registry
   - CoreTools (read, write, edit, bash, grep, find, ls, tree, git)
   - LSP tools (diagnostics, definition, references, hover, symbols)
   - Memory tools (search, timeline, get)
   - Agent tool (spawn subagent)
       ↓
4. Parameter coercion (string → int, bool, etc.)
       ↓
5. Sandbox enforcement (os.Root restriction)
       ↓
6. Tool execution with timeout
       ↓
7. AfterToolCallbacks
   - Extension hooks (after)
   - LSP format hook
   - Memory observation capture
   - Compactor check
       ↓
8. Result returned to LLM
```

### Sandbox Mechanism

```go
// internal/tools/sandbox.go
func NewSandbox(root string) *Sandbox {
    r, _ := os.Root(root)  // Go 1.24+ restricted filesystem
    return &Sandbox{root: r}
}

func (s *Sandbox) Open(name string) (*os.File, error) {
    return s.root.Open(name)
}
```

---

## 6. Session & Memory Architecture

```mermaid
flowchart TB
    subgraph Storage["~/.pi-go/"]
        Sessions["sessions/"]
        Memory["memory/"]
        Log["log/"]
        Palace["palace/"]
    end

    subgraph Sessions["sessions/<id>/"]
        Meta["meta.json"]
        Events["events.jsonl"]
        Branches["branches/"]
        BranchState["branches.json"]
    end

    subgraph Memory["memory/"]
        DB["claude-mem.db"]
        SessionsT["sessions table"]
        ObsT["observations table"]
        SumT["session_summaries table"]
        FTS["FTS5 indexes"]
    end

    subgraph Palace["palace/"]
        Config["mempalace.yaml"]
        Drawers["drawers/"]
        Graph["knowledge-graph.db"]
        Embed["embeddings/"]
    end

    subgraph Runtime["Runtime"]
        FileService["FileService"]
        Compactor["Compactor"]
        Worker["MemoryWorker"]
    end

    FileService --> Meta
    FileService --> Events
    FileService --> Branches
    FileService --> BranchState

    Compactor --> Events
    Compactor --> Summarizer["AI Summarizer"]

    Worker --> DB
    Worker --> Compressor["memory-compressor"]
```

### Session Storage Format

```
~/.pi-go/sessions/<session-id>/
├── meta.json           # Session metadata
├── events.jsonl        # Append-only event log
└── branches/
    ├── main/
    │   └── events.jsonl
    └── <branch-name>/
        └── events.jsonl
```

### Memory Layer Hierarchy (Palace)

```mermaid
flowchart TB
    subgraph L0["L0: Identity"]
        Identity["Static identity file"]
    end

    subgraph L1["L1: Essential Story"]
        TopDrawers["Top-15 drawers by importance"]
    end

    subgraph L2["L2: On-Demand Recall"]
        Chunks["Context-filtered drawer chunks"]
    end

    subgraph L3["L3: Search"]
        Semantic["Semantic (embedding)"]
        FTS["FTS5 keyword"]
    end

    subgraph Tools["Palace Tools"]
        WakeUp["WakeUp → L0+L1"]
        Recall["Recall → L2"]
        Search["Search → L3"]
    end

    Identity --> WakeUp
    TopDrawers --> WakeUp
    Chunks --> Recall
    Semantic --> Search
    FTS --> Search
```

---

## 7. Subagent Orchestration

```mermaid
flowchart TB
    subgraph Trigger["Trigger"]
        AgentTool["agent tool (LLM)"]
        Direct["Direct spawn"]
    end

    subgraph Orchestrator["Orchestrator"]
        Registry["Agent Registry"]
        Pool["Concurrency Pool (max 5)"]
        Spawner["Spawner"]
        Worktree["WorktreeManager"]
    end

    subgraph Execution["Execution"]
        Process["pi subprocess"]
        JSONL["JSONL events"]
        WorktreeDir[".worktrees/<id>/"]
    end

    subgraph Types["Agent Types"]
        Explore["explore"]
        Plan["plan"]
        Designer["designer"]
        Reviewer["reviewer"]
        Task["task"]
        QuickTask["quick_task"]
    end

    AgentTool --> Orchestrator
    Direct --> Orchestrator

    Orchestrator --> Registry
    Registry --> Pool
    Pool --> Spawner

    Spawner --> Process
    Process --> JSONL

    Registry --> Worktree
    Worktree --> WorktreeDir

    Orchestrator --> Types
```

### Subagent Configuration

```go
// internal/subagent/types.go
type AgentConfig struct {
    Type      string            // "explore", "task", "plan", etc.
    Model     string            // "smol", "default", "slow", "plan"
    Worktree  bool              // Isolated git worktree
    Instruction string          // System instruction override
    Timeout   time.Duration     // Max runtime
    Tools     []string          // Allowed tool names
}
```

### Agent Type Matrix

| Type       | Model   | Worktree | Purpose                 | Timeout |
|------------|---------|----------|-------------------------|---------|
| explore    | smol    | No       | Fast read-only research | 5min    |
| quick_task | smol    | No       | Small focused tasks     | 10min   |
| task       | default | Yes      | Full coding tasks       | 30min   |
| designer   | slow    | Yes      | Code creation           | 30min   |
| reviewer   | slow    | No       | Code review             | 15min   |
| plan       | plan    | No       | Analysis & planning     | 20min   |

---

## 8. TUI Rendering Pipeline

```mermaid
flowchart LR
    subgraph Input["Input Handler"]
        Key["KeyPress"]
        Cmd["SlashCommand"]
    end

    subgraph Update["Bubble Tea Update Loop"]
        Model["model"]
        AgentLoop["Agent Loop"]
        Events["Agent Events"]
    end

    subgraph Render["View Rendering"]
        View["View()"]
        Messages["renderMessages()"]
        Status["renderStatusBar()"]
        Input["renderInput()"]
        Markdown["Glamour Markdown"]
    end

    subgraph Display["Terminal Display"]
        Output["Terminal Output"]
    end

    Key --> Model
    Cmd --> Model
    Model --> AgentLoop
    AgentLoop --> Events
    Events --> View
    View --> Messages
    View --> Status
    View --> Input
    Messages --> Markdown
    Markdown --> Output
```

### TUI Component Hierarchy

```
model (Bubble Tea Model)
├── inputModel (InputModel)
│   ├── textinput
│   ├── completionEngine
│   └── history
├── chatModel (ChatModel)
│   ├── messages []Message
│   ├── markdownRenderer
│   └── scrollOffset
├── statusModel (StatusModel)
│   ├── modelName
│   ├── sessionID
│   └── tokenUsage
└── themeManager (ThemeManager)
    ├── themes map[string]Theme
    └── currentTheme
```

---

## 9. Extension Points

```mermaid
flowchart TB
    subgraph Agent["Agent Config"]
        BeforeCB["BeforeToolCallbacks"]
        AfterCB["AfterToolCallbacks"]
        Toolsets["Toolsets"]
    end

    subgraph Extensions["Extension System"]
        Hooks["Hooks"]
        Skills["Skills"]
        MCP["MCP Servers"]
    end

    subgraph Config["Configuration"]
        HookConfig["hooks[]"]
        SkillPaths["skill directories"]
        MCPConfig["mcp.servers[]"]
    end

    subgraph Hooks["Hooks Extension"]
        BeforeHook["before_tool"]
        AfterHook["after_tool"]
        Shell["Shell command execution"]
    end

    subgraph Skills["Skills Extension"]
        SkillFiles["*.SKILL.md"]
        Frontmatter["YAML frontmatter"]
        Instructions["Markdown instructions"]
    end

    subgraph MCP["MCP Extension"]
        MCPServer["MCP Server"]
        MCPTools["MCP Tools"]
        Transport["Subprocess Transport"]
    end

    Config --> Extensions
    Extensions --> Agent

    Hooks --> BeforeHook
    Hooks --> AfterHook
    BeforeHook --> Shell
    AfterHook --> Shell

    Skills --> SkillFiles
    SkillFiles --> Frontmatter
    SkillFiles --> Instructions

    MCP --> MCPServer
    MCP --> MCPTools
    MCP --> Transport
```

### Hook Configuration Schema

```yaml
# ~/.pi-go/config.json
{
  "hooks": [
    {
      "name": "my-hook",
      "trigger": "after_tool",
      "tool": "write",
      "command": ["echo", "wrote {path}"],
      "env": {"EXTRA": "value"}
    }
  ]
}
```

### Skill File Schema

```markdown
---
name: my-skill
description: What this skill does
trigger: manual  # or "auto" on match
---

# My Skill

Instructions for the agent when this skill is active...
```

### MCP Server Configuration

```yaml
# ~/.pi-go/config.json
{
  "mcp": {
    "servers": [
      {
        "name": "filesystem",
        "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/path"]
      }
    ]
  }
}
```

---

## 10. Data Flow Diagrams

### Complete Request Flow

```mermaid
sequenceDiagram
    participant U as User
    participant T as TUI
    participant A as Agent
    participant R as ADK Runner
    participant LL as LLM
    participant TB as Tool
    participant SB as Sandbox
    participant H as Hooks
    participant LS as LSP
    participant M as Memory
    participant S as Session

    U->>T: "fix the bug"
    T->>A: Run(ctx, sessionID, msg)
    A->>R: runner.Run(content)
    R->>LL: GenerateContent(req)

    alt Text Response
        LL-->>R: text response
    else Tool Call
        LL-->>R: tool_call("read", {path: "foo.go"})
        R->>H: BeforeToolCallbacks
        R->>TB: Tool.Execute(args)
        TB->>SB: Open restricted path
        SB-->>TB: file contents
        TB-->>R: tool result
        R->>H: AfterToolCallbacks
        R->>LS: Format/Diagnostics
        R->>M: Capture observation
        R->>S: AppendEvent
        R->>LL: GenerateContent(with result)
    end

    R-->>A: iter.Seq2[Event, error]
    A-->>T: events stream
    T-->>U: rendered output
```

### Memory Observation Capture Flow

```mermaid
sequenceDiagram
    participant T as Tool
    participant CB as AfterToolCallback
    participant Q as Queue
    participant W as Worker
    participant SA as Subagent
    participant DB as Memory DB
    participant AI as AI Compressor

    T-->>CB: tool result
    CB->>Q: enqueue observation
    Q-->>W: buffered channel
    W->>SA: spawn memory-compressor
    SA->>AI: extract structured observation
    AI-->>SA: observation {type, title, text}
    SA-->>W: structured result
    W->>DB: insert into SQLite
```

---

## Appendix: Key Files Reference

| File                                | Purpose                   |
|-------------------------------------|---------------------------|
| `cmd/pi/main.go`                    | Entry point               |
| `internal/cli/cli.go`               | CLI setup, mode routing   |
| `internal/cli/interactive.go`       | TUI initialization        |
| `internal/agent/agent.go`           | ADK agent creation        |
| `internal/agent/retry.go`           | Exponential backoff retry |
| `internal/agent/instruction.go`     | System prompt             |
| `internal/provider/provider.go`     | LLM factory               |
| `internal/tools/registry.go`        | Tool registration         |
| `internal/tools/sandbox.go`         | Filesystem sandbox        |
| `internal/session/store.go`         | JSONL persistence         |
| `internal/tui/tui.go`               | Bubble Tea model          |
| `internal/tui/chat.go`              | Message rendering         |
| `internal/subagent/orchestrator.go` | Subagent management       |
| `internal/lsp/manager.go`           | LSP client manager        |
| `internal/memory/store.go`          | SQLite memory storage     |
| `internal/palace/palace.go`         | Memory palace             |
| `internal/extension/hooks.go`       | Hook extension            |
| `internal/extension/skills.go`      | Skills extension          |
| `internal/extension/mcp.go`         | MCP integration           |

---

## Appendix: Environment Variables

| Variable             | Purpose                   |
|----------------------|---------------------------|
| `ANTHROPIC_API_KEY`  | Anthropic API key         |
| `ANTHROPIC_BASE_URL` | Custom Anthropic endpoint |
| `OPENAI_API_KEY`     | OpenAI API key            |
| `OPENAI_BASE_URL`    | Custom OpenAI endpoint    |
| `GEMINI_API_KEY`     | Google API key            |
| `GEMINI_BASE_URL`    | Custom Gemini endpoint    |
| `MISTRAL_API_KEY`    | Mistral API key           |
| `PI_GO_CONFIG`       | Config file path override |

---

*Generated from codebase analysis. Last updated: auto-generated.*
