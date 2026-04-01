# Coding Agent Architecture Reference

> High-level architecture for building an interactive coding agent CLI, derived from production patterns in Claude Code. This document is implementation-agnostic and can be used as a blueprint for a new repository.

---

## Table of Contents

1. [System Overview](#system-overview)
2. [Core Architecture](#core-architecture)
3. [Tool System](#tool-system)
4. [Query Engine (LLM Loop)](#query-engine-llm-loop)
5. [Permission System](#permission-system)
6. [Command System](#command-system)
7. [Terminal UI](#terminal-ui)
8. [State Management](#state-management)
9. [IDE Bridge](#ide-bridge)
10. [Multi-Agent Orchestration](#multi-agent-orchestration)
11. [Plugin & Skill Extensibility](#plugin--skill-extensibility)
12. [Session Management](#session-management)
13. [Security Architecture](#security-architecture)
14. [Observability](#observability)
15. [Configuration Layering](#configuration-layering)
16. [Performance Patterns](#performance-patterns)
17. [Technology Stack](#technology-stack)
18. [Implementation Checklist](#implementation-checklist)

---

## System Overview

A coding agent is an interactive CLI that connects a user to an LLM through a REPL (Read-Eval-Print Loop). The LLM can invoke **tools** (file read/write, shell execution, search, web fetch) to perform real-world actions. The agent loops between the LLM and tool execution until the task is complete or the user intervenes.

```
┌─────────────────────────────────────────────────────┐
│                     User (Terminal / IDE)            │
└──────────────────────┬──────────────────────────────┘
                       │ input / approval / denial
                       ▼
┌─────────────────────────────────────────────────────┐
│                  REPL / UI Layer                     │
│          (React/Ink terminal components)             │
└──────────────────────┬──────────────────────────────┘
                       │ messages
                       ▼
┌─────────────────────────────────────────────────────┐
│                  Query Engine                        │
│  ┌───────────┐   ┌───────────┐   ┌──────────────┐  │
│  │  Message   │──▶│  LLM API  │──▶│  Tool Loop   │  │
│  │ Normalizer │   │ (stream)  │   │  (execute +  │  │
│  └───────────┘   └───────────┘   │   re-prompt)  │  │
│                                   └──────────────┘  │
└──────────────────────┬──────────────────────────────┘
                       │ tool calls
                       ▼
┌─────────────────────────────────────────────────────┐
│                  Tool Registry                       │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────────┐  │
│  │ Bash │ │ Read │ │ Edit │ │ Grep │ │ Agent    │  │
│  │ Tool │ │ Tool │ │ Tool │ │ Tool │ │ (sub)    │  │
│  └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘ └────┬─────┘  │
│     │        │        │        │           │        │
│  ┌──▼────────▼────────▼────────▼───────────▼─────┐  │
│  │            Permission Gate                     │  │
│  └────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

---

## Core Architecture

### Directory Layout

```
src/
├── entrypoints/       # Initialization and bootstrap
├── tools/             # Self-contained tool modules
│   ├── BashTool/      # Shell execution (schema, permissions, security, UI)
│   ├── FileReadTool/
│   ├── FileEditTool/
│   ├── FileWriteTool/
│   ├── GlobTool/
│   ├── GrepTool/
│   ├── WebFetchTool/
│   ├── AgentTool/     # Sub-agent spawning
│   └── ...
├── commands/          # Slash commands (/commit, /review, /compact, ...)
├── services/          # External integrations (API, MCP, OAuth, analytics)
├── components/        # Terminal UI components
├── hooks/             # React hooks (permissions, input, state)
├── state/             # Centralized state store
├── utils/             # Shared utilities (shell, git, paths, messages)
├── bridge/            # IDE integration layer
├── skills/            # Reusable workflow definitions
├── plugins/           # Plugin loader and lifecycle
├── tasks/             # Background task management
├── types/             # TypeScript type definitions
├── constants/         # Configuration constants
└── migrations/        # Config schema migrations
```

### Key Principle: Each Tool Is a Self-Contained Module

Every tool encapsulates:
- **Input schema** (Zod validation)
- **Permission model** (what rules govern its use)
- **Security checks** (e.g., bash command analysis, path validation)
- **Execution logic** (the actual work)
- **Progress UI** (streaming feedback to the user)
- **Result formatting** (structured output for the LLM)

---

## Tool System

### Core Tools to Implement

| Tool | Purpose | Key Concerns |
|------|---------|-------------|
| **Bash** | Shell command execution | Sandbox, security analysis, read-only validation, timeout |
| **FileRead** | Read files (text, images, PDFs, notebooks) | Encoding detection, line limits, binary handling |
| **FileEdit** | Partial file modification (string replacement) | Uniqueness check on old_string, indentation preservation |
| **FileWrite** | Create or overwrite files | Require prior read of existing files to prevent data loss |
| **Glob** | File pattern search | Modification-time sorting, respect .gitignore |
| **Grep** | Content search (regex) | Use ripgrep for performance, support context lines, multiple output modes |
| **WebFetch** | Fetch URL content | HTML-to-text conversion, size limits, timeout |
| **WebSearch** | Web search | Rate limiting, result formatting |
| **Agent** | Spawn sub-agents | Context isolation, message passing, worktree isolation |
| **TaskCreate/Update/Get/List** | Task lifecycle | Disk persistence, status tracking |
| **SendMessage** | Inter-agent messaging | Routing by agent name/ID |
| **Notebook** | Jupyter cell editing | Cell type handling, output preservation |
| **LSP** | Language server queries | Protocol bridging, timeout management |
| **MCP** | External MCP server tools | Dynamic tool discovery, permission delegation |

### Tool Registration Pattern

```typescript
interface Tool {
  name: string
  description: string
  inputSchema: ZodSchema          // Zod v4 for runtime validation
  permissionModel: PermissionRule
  isReadOnly: boolean
  execute(input: ToolInput, context: ToolContext): Promise<ToolResult>
  renderProgress?(state: ProgressState): ReactNode  // optional UI
}
```

### Bash Tool Security (Critical)

The Bash tool is the most powerful and dangerous tool. It requires:

1. **AST parsing** of the command to understand intent
2. **Command semantics classification** (read-only, write, destructive, network)
3. **Sandbox detection** (use platform sandbox for untrusted commands)
4. **Path validation** (commands shouldn't escape the working directory)
5. **Timeout enforcement** (default 2 minutes, configurable)
6. **Output truncation** (prevent unbounded output from filling context)
7. **Background execution support** (for long-running commands)

---

## Query Engine (LLM Loop)

The query engine is the core loop that mediates between the LLM and tools.

### Flow

```
1. Normalize messages (system prompt, user input, memory, context)
2. Call LLM API with streaming
3. Parse response for tool_use blocks
4. For each tool call:
   a. Validate input against schema
   b. Check permissions (allow/deny/ask user)
   c. Execute tool
   d. Format result as tool_result message
5. If tool calls were made, append results and go to step 2
6. If no tool calls (text-only response), return to user
7. Handle: stop reasons, token limits, errors, retries
```

### Message Architecture

```
Message Types:
├── SystemMessage        # System prompt + instructions
├── UserMessage          # User input (text + optional attachments)
├── AssistantMessage     # LLM response (text + tool_use blocks)
├── ToolResultMessage    # Tool execution results
├── ProgressMessage      # Streaming progress (not sent to API)
└── TombstoneMessage     # Placeholder for compacted/deleted messages
```

### Context Window Management

- **Auto-compact**: When approaching token limits, summarize older messages
- **Snip-compact**: Replace middle of conversation with summary, keep recent
- **Token counting**: Track input/output tokens per turn and cumulative
- **Attachment filtering**: Strip large attachments from history after N turns

---

## Permission System

### Permission Modes

| Mode | Behavior |
|------|----------|
| `default` | Ask user for every tool use |
| `acceptEdits` | Auto-approve file edits, ask for everything else |
| `plan` | Approve once per plan, then auto-approve |
| `dontAsk` | Remember user decisions for the session |
| `bypassPermissions` | Auto-approve everything (dangerous) |
| `auto` | ML classifier decides (experimental) |

### Permission Rule Structure

```typescript
interface PermissionRule {
  source: 'userSettings' | 'projectSettings' | 'policySettings' | 'cli'
  tool: string                    // tool name or glob pattern
  behavior: 'allow' | 'deny' | 'ask'
  condition?: string              // e.g., path pattern, command pattern
}
```

### Permission Flow

```
Tool invoked
  ├── Check explicit deny rules → Block
  ├── Check explicit allow rules → Execute
  ├── Check permission mode:
  │   ├── bypassPermissions → Execute
  │   ├── acceptEdits (and tool is edit) → Execute
  │   ├── dontAsk (and seen before) → Use remembered decision
  │   └── default/plan → Prompt user
  └── Record decision (for dontAsk mode)
```

### Best Practices

- **Default to ask**: Unknown tools should require explicit approval
- **Track denials**: Record what was denied so the LLM can adjust behavior
- **Layered sources**: Policy > project > user > CLI args
- **Content-specific rules**: "Allow Bash for `git status`" vs "Deny Bash for `rm -rf`"

---

## Command System

Slash commands (`/commit`, `/review`, `/compact`, etc.) are user-facing operations registered in a command registry.

### Command Interface

```typescript
interface Command {
  name: string
  aliases?: string[]
  description: string
  isEnabled?: () => boolean       // feature-gate check
  argParser?: (args: string) => ParsedArgs
  execute(args: ParsedArgs, context: AppContext): Promise<void>
}
```

### Essential Commands

| Command | Purpose |
|---------|---------|
| `/help` | Show available commands |
| `/compact` | Compress conversation context |
| `/commit` | Create git commit from staged changes |
| `/review` | Code review analysis |
| `/diff` | Show file changes |
| `/config` | View/edit settings |
| `/doctor` | Environment diagnostics |
| `/login` / `/logout` | Authentication |
| `/memory` | Manage persistent memory |
| `/resume` | Resume previous session |
| `/cost` | Show token usage and cost |
| `/vim` | Toggle vim keybindings |

---

## Terminal UI

### Technology: React + Ink

Ink renders React components to the terminal using ANSI escape codes. This gives you:
- Declarative UI with components and hooks
- Layout engine (Yoga/Flexbox)
- Text styling and colors
- Scrollable containers
- Focus management
- Keyboard event handling

### Key UI Components

```
App
├── StatusLine              # Session info, model, cost
├── MessageList             # Scrollable message history
│   ├── UserMessage         # User input display
│   ├── AssistantMessage    # LLM response with markdown
│   │   ├── Markdown        # Rich markdown rendering
│   │   └── ToolUseBlock    # Tool call display
│   │       ├── ProgressBar
│   │       └── ResultView
│   └── SystemMessage       # System notifications
├── PermissionPrompt        # Tool approval dialog
├── TextInput               # User input field
└── ErrorOverview           # Error display
```

### Markdown Rendering

Use a markdown parser (remark/unified) to render formatted output:
- Code blocks with syntax highlighting
- Tables, lists, headers
- Links (clickable in supported terminals)
- Inline code and emphasis

---

## State Management

### Central Store Pattern

Use a Zustand-like store with React context:

```typescript
interface AppState {
  messages: Message[]
  permissionContext: PermissionContext
  denials: Denial[]
  settings: Settings
  mcpConnections: MCPConnection[]
  agents: AgentState[]
  tasks: Task[]
  session: SessionInfo
}
```

### Principles

- **Single source of truth**: All state in one store
- **Mutable updates internally**: Use immer or direct mutation in store
- **Immutable snapshots for React**: Components get frozen snapshots
- **Selector memoization**: Derived state via selectors to prevent re-renders
- **Settings reactivity**: Changes to settings propagate immediately

---

## IDE Bridge

Bidirectional communication between CLI and IDE extensions (VS Code, JetBrains).

### Architecture

```
IDE Extension ◄────── Bridge Protocol ──────► CLI Agent
  (client)         (JSON messages over          (server)
                    stdio/WebSocket/UDS)
```

### Bridge Capabilities

- **Full duplex streaming**: Real-time message relay
- **Permission callbacks**: IDE can prompt user for tool approvals
- **File attachments**: Send files between IDE and agent
- **Status updates**: Progress indicators in IDE
- **Device pairing**: JWT-based trust establishment

### Protocol Messages

```
→ IDE to Agent: user_message, file_attachment, permission_response
← Agent to IDE: assistant_message, tool_progress, permission_request, status_update
```

---

## Multi-Agent Orchestration

### Sub-Agent Spawning (AgentTool)

The agent tool creates isolated child processes that:
- Inherit a subset of the parent's context
- Have their own tool registry and permission scope
- Can work in isolated git worktrees
- Communicate results back via SendMessage

### Agent Types

| Type | Purpose | Tools Available |
|------|---------|----------------|
| `general-purpose` | Complex multi-step tasks | All tools |
| `Explore` | Fast codebase search | Read-only tools |
| `Plan` | Architecture design | Read-only + plan tools |
| Custom types | Domain-specific agents | Configurable |

### Team Coordination

```
Coordinator Agent
├── Agent A (feature implementation)
├── Agent B (test writing)
└── Agent C (documentation)
    └── Each has isolated worktree
    └── Results merged by coordinator
```

### Best Practices

- **Context isolation**: Sub-agents don't pollute parent context
- **Parallel execution**: Independent agents run concurrently
- **Worktree isolation**: Each agent gets its own git worktree to prevent conflicts
- **Message passing**: Structured communication via SendMessage, not shared state

---

## Plugin & Skill Extensibility

### Plugin System

Plugins extend the agent with custom commands, tools, and hooks.

```
~/.claude/plugins/
├── my-plugin/
│   ├── index.ts        # Plugin entry point
│   ├── commands/        # Custom commands
│   ├── tools/           # Custom tools
│   └── hooks/           # Event hooks
```

### Skill System

Skills are reusable, shareable workflows (like macros).

```
~/.claude/skills/
├── deploy.md           # Skill definition (prompt template)
├── review-pr.md
└── setup-project.md
```

Skills are invoked via the SkillTool and can:
- Accept arguments
- Chain tool calls
- Be shared across projects
- Be gated by feature flags

### User Hooks

Event-driven shell commands for extensibility:

```json
{
  "hooks": {
    "preToolUse": [{ "command": "lint-check.sh", "tool": "FileEdit" }],
    "postToolUse": [{ "command": "format.sh", "tool": "FileWrite" }],
    "sessionStart": [{ "command": "setup-env.sh" }]
  }
}
```

---

## Session Management

### Session Persistence

```
~/.claude/sessions/
├── <session-id>/
│   ├── transcript.json    # Full message history
│   ├── file-history.json  # Files modified
│   ├── tasks/             # Task outputs
│   └── metadata.json      # Session info (start time, model, cost)
```

### Resume Capability

- Sessions can be resumed with full context
- File history tracks what was modified for rollback awareness
- Task outputs persist across sessions

### Persistent Memory

```
~/.claude/memory/
├── MEMORY.md              # Index of memory files
├── user_role.md           # User profile
├── feedback_testing.md    # Behavioral feedback
├── project_auth.md        # Project context
└── reference_linear.md    # External system pointers
```

Memory types: `user`, `feedback`, `project`, `reference`

---

## Security Architecture

### Defense in Depth

```
Layer 1: Permission Rules     (declarative allow/deny)
Layer 2: User Approval        (interactive prompt)
Layer 3: Tool-Level Security  (bash AST analysis, path validation)
Layer 4: Sandbox Isolation    (OS-level sandboxing for high-risk commands)
Layer 5: Credential Isolation (keychain storage, no env leaks)
```

### Bash Security Pipeline

```
Command string
  → AST parse (bash parser)
  → Classify intent (read/write/destructive/network)
  → Check against deny patterns (rm -rf /, drop database, etc.)
  → Validate paths (within working directory)
  → Decide sandbox (untrusted commands get sandboxed)
  → Execute with timeout and output limits
```

### Credential Management

- **macOS Keychain** / platform-specific secure storage
- **No credentials in environment** unless explicitly safe
- **Token refresh** with exponential backoff
- **OAuth 2.0** for API authentication
- **JWT** for bridge device pairing

### Prompt Injection Defense

- Tool results flagged if they contain suspicious instructions
- System messages clearly demarcated from user/tool content
- MCP server instructions isolated from user context

---

## Observability

### Layers

| Layer | Technology | Purpose |
|-------|-----------|---------|
| **Structured Logging** | Custom logger | Debug, diagnostics, PII-safe logging |
| **Telemetry** | OpenTelemetry + gRPC | Distributed tracing, spans |
| **Analytics** | GrowthBook | Feature flags, A/B experiments |
| **Cost Tracking** | Built-in | Token usage, API cost per session |
| **Startup Profiling** | Checkpoint system | Measure initialization latency |
| **Error Categorization** | Custom classifier | Retryable vs fatal, user-facing messages |

### Error Handling Pattern

```typescript
try {
  result = await callAPI(request)
} catch (error) {
  const category = categorizeError(error)  // retryable, fatal, rate-limit, auth
  if (category === 'retryable') {
    return await withRetry(() => callAPI(request), { maxRetries: 3, backoff: 'exponential' })
  }
  throw new UserFacingError(category, error)
}
```

---

## Configuration Layering

Configuration sources in priority order (highest wins):

```
1. CLI arguments          (--model, --permission-mode)
2. Environment variables  (CLAUDE_MODEL, ANTHROPIC_API_KEY)
3. Project settings       (.claude/settings.json)
4. User settings          (~/.claude/settings.json)
5. Remote managed         (server-pushed org policies)
6. MDM / system policy    (enterprise device management)
7. Defaults               (hardcoded fallbacks)
```

### Settings Schema

```json
{
  "model": "claude-opus-4-6",
  "permissionMode": "default",
  "tools": {
    "allow": ["FileRead", "Glob", "Grep"],
    "deny": []
  },
  "hooks": { ... },
  "mcp": {
    "servers": { ... }
  },
  "theme": "dark",
  "vim": false
}
```

### Config Migrations

When the settings schema changes, migration scripts upgrade old configs:

```typescript
const migrations = [
  { version: 2, migrate: (config) => { /* rename field */ } },
  { version: 3, migrate: (config) => { /* add new defaults */ } },
]
```

---

## Performance Patterns

### 1. Parallel Prefetch at Startup

Fire I/O-bound operations before heavy module evaluation:

```typescript
// Before imports
const mdmPromise = startMdmRead()
const keychainPromise = startKeychainPrefetch()
const preconnectPromise = apiPreconnect()

// Heavy imports happen here...
import { App } from './App'

// Await results when needed
const mdm = await mdmPromise
```

### 2. Lazy Loading via Dynamic Import

Only load heavy modules when their feature is active:

```typescript
const telemetry = feature('TELEMETRY')
  ? await import('./services/telemetry')
  : null
```

### 3. Streaming Responses

Stream LLM output token-by-token to the UI for perceived responsiveness.

### 4. Tool Progress Feedback

Show progress for tools that take >2 seconds:

```typescript
const PROGRESS_THRESHOLD_MS = 2000
if (elapsed > PROGRESS_THRESHOLD_MS) {
  emitProgress({ tool: 'Bash', status: 'running', output: partialOutput })
}
```

### 5. Output Truncation

Prevent unbounded tool output from filling the context window:
- Truncate bash output beyond N lines
- Summarize large file reads
- Limit grep results

---

## Technology Stack

| Category | Recommended | Alternatives |
|----------|-------------|-------------|
| **Runtime** | Bun | Node.js, Deno |
| **Language** | TypeScript (strict) | — |
| **CLI Framework** | Commander.js | yargs, oclif |
| **Terminal UI** | React + Ink | blessed, enquirer, prompts |
| **Schema Validation** | Zod v4 | io-ts, ajv |
| **Code Search** | ripgrep (rg) | grep, ag |
| **LLM API** | Anthropic SDK | OpenAI SDK, custom HTTP |
| **Protocols** | MCP SDK, LSP | — |
| **State Management** | Zustand | Redux, MobX, custom store |
| **Markdown** | remark / unified | marked, markdown-it |
| **Auth** | OAuth 2.0 + JWT | API keys only |
| **Secure Storage** | OS Keychain | encrypted file, vault |
| **Telemetry** | OpenTelemetry | Datadog, custom |
| **Feature Flags** | GrowthBook | LaunchDarkly, custom |
| **Sandbox** | OS-level (seatbelt/seccomp) | Docker, nsjail |

---

## Implementation Checklist

### Phase 1: Core Loop
- [ ] CLI entry point with Commander.js
- [ ] Basic REPL (read input, send to LLM, print response)
- [ ] LLM API integration with streaming
- [ ] Tool loop (parse tool_use, execute, return tool_result)
- [ ] Message history management
- [ ] Token counting and context limits

### Phase 2: Essential Tools
- [ ] FileRead (text, encoding detection)
- [ ] FileWrite (with prior-read requirement)
- [ ] FileEdit (string replacement with uniqueness check)
- [ ] Bash (basic execution with timeout)
- [ ] Glob (file pattern search)
- [ ] Grep (ripgrep wrapper)

### Phase 3: Permission System
- [ ] Permission modes (default, acceptEdits, bypass)
- [ ] User prompt for tool approval
- [ ] Allow/deny rule configuration
- [ ] Denial tracking for LLM feedback

### Phase 4: Terminal UI
- [ ] Ink-based component system
- [ ] Markdown rendering
- [ ] Progress indicators
- [ ] Scrollable message history
- [ ] Permission prompt dialog

### Phase 5: Bash Security
- [ ] Command AST parsing
- [ ] Intent classification (read/write/destructive)
- [ ] Path validation
- [ ] Sandbox integration
- [ ] Output truncation

### Phase 6: Context Management
- [ ] Auto-compact (summarize old messages)
- [ ] Snip-compact (keep recent, summarize middle)
- [ ] Attachment lifecycle
- [ ] System prompt management

### Phase 7: Session & Memory
- [ ] Session persistence (transcript, metadata)
- [ ] Session resume
- [ ] Persistent memory (user, feedback, project, reference)
- [ ] Memory indexing

### Phase 8: Commands
- [ ] Command registry and parser
- [ ] /help, /compact, /cost
- [ ] /commit, /diff, /review (git integration)
- [ ] /config, /doctor

### Phase 9: Sub-Agents
- [ ] AgentTool (spawn child processes)
- [ ] Context isolation
- [ ] Git worktree isolation
- [ ] Inter-agent messaging (SendMessage)
- [ ] Parallel agent execution

### Phase 10: IDE Bridge
- [ ] Bridge protocol (JSON over stdio/WebSocket)
- [ ] Permission callback delegation
- [ ] File attachment relay
- [ ] Status update streaming
- [ ] Device pairing (JWT)

### Phase 11: Extensibility
- [ ] Plugin loader and lifecycle
- [ ] Skill definitions and execution
- [ ] User hooks (pre/post tool, session events)
- [ ] MCP server integration
- [ ] Custom tool registration

### Phase 12: Production Hardening
- [ ] Structured logging (PII-safe)
- [ ] OpenTelemetry tracing
- [ ] Error categorization and retry logic
- [ ] Cost tracking and budget limits
- [ ] Config migrations
- [ ] Feature flags
- [ ] Startup profiling and optimization

---

## Key Design Decisions Summary

| Decision | Rationale |
|----------|-----------|
| React/Ink for TUI | Declarative UI, component reuse, familiar dev model |
| Zod for tool schemas | Runtime validation + TypeScript inference in one place |
| Self-contained tool modules | Each tool owns its schema, permissions, execution, and UI |
| Layered permissions | Enterprise policies override user prefs override defaults |
| Streaming-first | Token-by-token output for perceived responsiveness |
| Context compression | Sustain long sessions without hitting token limits |
| Git worktree isolation | Sub-agents work without conflicts on the same repo |
| Persistent memory | Cross-session learning without retraining |
| Hook system | User extensibility without forking the codebase |
| Feature flags at build time | Dead code elimination for smaller, focused builds |
