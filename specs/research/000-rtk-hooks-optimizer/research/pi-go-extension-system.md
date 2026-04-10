# pi-go Extension System Research

## Current Architecture

### Extension Components (`internal/extension/`)

| Component | File | Purpose |
|-----------|------|---------|
| Hooks | `hooks.go` | Shell-command before/after tool hooks |
| Skills | `skills.go` | Loadable prompt extensions via SKILL.md |
| MCP | `mcp.go` | External tool providers via subprocess |
| Skill Template | `skill_template.go` | Template for `/skill-create` |

### Hooks System (Current)

```go
type HookConfig struct {
    Event   string   // "before_tool" or "after_tool"
    Command string   // shell command to execute
    Tools   []string // optional tool filter
    Timeout int      // seconds (default 10)
}
```

- Shell-based only — runs `sh -c <command>` with JSON on stdin
- Cannot modify args or results (fire-and-forget)
- Non-fatal failures (logged, execution continues)
- Configured in `config.json` under `hooks` array

### Agent Construction Flow (`internal/cli/cli.go`)

```
CLI Startup
├── Load Config (global + project config.json)
├── Build Core Tools (registry.go → sandbox-wrapped)
├── Load Extensions
│   ├── Hooks → BuildBefore/AfterToolCallbacks()
│   ├── Skills → LoadSkills(global, project dirs)
│   ├── MCP → BuildMCPToolsets()
│   └── LSP → AfterToolCallback (hardcoded)
├── Create Agent (agent.go)
│   ├── LLM agent with tools + toolsets
│   ├── Before/after callbacks
│   ├── Session service
│   └── System instruction (with skill listings)
└── Start TUI or RPC mode
```

### Tool Call Data Flow

```
LLM Response (tool_calls)
    → BeforeToolCallback chain
    → Tool.Run(ctx, args)
    → AfterToolCallback chain (hooks + LSP)
    → Append Event to Session (JSONL)
    → LLM Continuation (with tool results)
    → Loop until done
```

### Key Agent Methods

- `agent.New(cfg Config)` — creates LLM agent with all tools/callbacks
- `agent.RebuildWithInstruction(instruction)` — dynamic system prompt update (preserves tools/callbacks)
- `agent.Run(ctx, sessionID, content)` — execute agent with user message

### TUI Slash Commands (`internal/tui/tui.go`)

| Command | Handler |
|---------|---------|
| `/help` | inline |
| `/clear` | inline |
| `/model` | `formatModelInfo()` |
| `/session` | inline |
| `/branch` | `handleBranchCommand()` |
| `/compact` | `handleCompactCommand()` |
| `/agents` | `handleAgentsCommand()` |
| `/history` | `handleHistoryCommand()` |
| `/commit` | `handleCommitCommand()` |
| `/plan` | `handlePlanCommand()` |
| `/run` | `handleRunCommand()` |
| `/login` | `handleLoginCommand()` |
| `/skill-create` | `handleSkillCreateCommand()` |
| `/skill-load` | `handleSkillLoadCommand()` |
| `/skill-list` | `handleSkillListCommand()` |
| `/<skill>` | `handleSkillCommand()` |

### Configuration (`internal/config/config.go`)

```go
type Config struct {
    Roles         map[string]RoleConfig
    DefaultModel  string
    DefaultProvider string
    ThinkingLevel string
    Theme         string
    Tools         map[string]any
    MCP           *MCPConfig
    Hooks         []HookConfig
}
```

Loading: defaults → `~/.pi-go/config.json` → `.pi-go/config.json`

### Session Management (`internal/session/`)

- File-based: `~/.pi-go/sessions/<id>/`
- `meta.json` + `events.jsonl` + `branches.json`
- In-memory cache with disk persistence
- Branch system for conversation forking
- No session lifecycle hooks (create/destroy events not exposed)

## Integration Points for RTK Optimizer

| Feature | Integration Point | Approach |
|---------|------------------|----------|
| Command rewriting | `BeforeToolCallback` | Modify bash args in-place (ADK supports this) |
| Output compaction | `AfterToolCallback` | Modify result map (ADK supports this) |
| Config TUI (`/rtk`) | TUI slash command | Add handler like existing commands |
| Metrics tracking | Callback state | Accumulate in Go struct, expose via `/rtk stats` |
| Session events | CLI startup + session service | Custom wiring (ADK has no session hooks) |
| System prompt injection | `agent.RebuildWithInstruction()` | Already available |

## Key Files

- `internal/extension/hooks.go` — extend with Go-native hooks
- `internal/cli/cli.go` — agent construction, hook wiring
- `internal/agent/agent.go` — agent config, rebuild
- `internal/tui/tui.go` — slash command registration
- `internal/config/config.go` — config structure
- `internal/tools/bash.go` — bash tool (rewrite target)
- `internal/session/store.go` — session lifecycle
