# Gap Analysis: pi-go vs. Claude Code Architecture

> Comparative analysis identifying opportunities to improve pi-go based on patterns from Claude Code's agent architecture.
> Reference: [AGENT_ARCHITECTURE.md](./AGENT_ARCHITECTURE.md), [ARCHITECTURE.md](./ARCHITECTURE.md)

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Multi-Agent Orchestration](#multi-agent-orchestration)
3. [Agent System](#agent-system)
4. [Task Management](#task-management)
5. [Inter-Agent Communication](#inter-agent-communication)
6. [Permission System](#permission-system)
7. [Terminal UI](#terminal-ui)
8. [Session & Memory](#session--memory)
9. [Tool System](#tool-system)
10. [Bash Security](#bash-security)
11. [Extensibility](#extensibility)
12. [IDE Integration](#ide-integration)
13. [Commands & Slash Commands](#commands--slash-commands)
14. [Execution Backends](#execution-backends)
15. [Priority Recommendations](#priority-recommendations)

---

## Executive Summary

| Category | Claude Code | pi-go | Gap |
|----------|------------|-------|-----|
| **Multi-Agent Patterns** | 5 patterns (sync, async, fork, coordinator, swarm) | 2 (sync, async) | Medium |
| **Task Management** | Full task system with CRUD | None | High |
| **Inter-Agent Messaging** | File mailbox + SendMessage | None (results only) | High |
| **Agent Definitions** | Markdown-based dynamic loading | Bundled Go code | Medium |
| **Permission Modes** | 6 modes with ML classifier | 2 modes (ask, bypass) | High |
| **IDE Bridge** | Full stdio/WebSocket bridge | None | High |
| **Bash Security** | AST parsing + sandbox | os.Root sandbox only | Medium |
| **Execution Backends** | tmux/iTerm2/in-process | In-process only | High |
| **Memory System** | SQLite + AI compression | SQLite + AI compression | Low |
| **Command System** | 20+ commands | 15+ commands | Low |

---

## Multi-Agent Orchestration

### Current State (pi-go)

pi-go has a basic subagent system in `internal/subagent/`:
- **Sync subagents**: Inline execution, result returned to parent
- **Async subagents**: Background execution via goroutines
- **Pool**: Concurrency limiter (max 5)
- **Worktree isolation**: Git worktrees for isolation
- **Agent types**: explore, plan, designer, reviewer, task, quick_task

### Reference (Claude Code)

Claude Code implements 5 distinct multi-agent patterns:

| Pattern | Entry | Execution | Communication |
|---------|-------|-----------|---------------|
| **Sync Agent** | AgentTool | Inline in parent turn | Return value |
| **Async Agent** | AgentTool (background) | Detached task | SendMessage / notifications |
| **Fork Subagent** | AgentTool (implicit) | Parallel async | Cache-shared prefixes |
| **Coordinator** | Feature flag | 3-tier hierarchy | Task notifications + XML |
| **Team Swarm** | TeamCreateTool | tmux/iTerm2/in-process | File-based mailbox |

### Gaps & Improvements

#### 1. Fork Subagent with Prompt Cache Optimization

**Gap**: Claude Code's fork pattern maximizes Anthropic's prompt cache by sharing byte-identical prefixes across parallel children.

```
Parent assistant message (identical copy)
+ tool_result placeholders (identical)
+ per-child directive (only difference)
```

**Suggestion**: Implement fork with cache-aware message building:
- Clone parent's full assistant message verbatim
- Build identical placeholder tool_results
- Append only per-child directive as difference
- Track fork depth to prevent infinite recursion

#### 2. Coordinator Mode (3-Tier Orchestration)

**Gap**: No coordinator pattern for complex multi-step projects.

**Suggestion**: Implement coordinator with:
- Coordinator (leader) → Workers → Tools hierarchy
- XML-based task notifications from workers
- Synthesis requirement (coordinator understands before delegating)
- Self-contained prompts (workers can't see conversation)

#### 3. Team Swarm System

**Gap**: No persistent named teammates with direct messaging.

**Suggestion**: Implement team system with:
- `TeamCreateTool` / `TeamDeleteTool`
- File-based mailbox at `~/.pi-go/teams/{team}/inboxes/{name}.json`
- Shared task list at `~/.pi-go/tasks/{team}/`
- Visual pane management (tmux/iTerm2 or virtual)

#### 4. Async Agent Resume

**Gap**: No way to resume stopped background agents.

**Suggestion**: Implement resume capability:
- Persist agent state to disk
- Detect stopped agents on SendMessage
- Auto-resume with transcript + file state restoration

---

## Agent System

### Current State (pi-go)

Agents are defined in Go code in `internal/subagent/agents.go`:
```go
type AgentType struct {
    Name        string
    Description string
    ModelRole   string
    Worktree    bool
    Instruction string
    Tools       []string
}
```

### Reference (Claude Code)

Dynamic agent loading from markdown files:
```markdown
---
name: my-researcher
description: Deep research agent
color: blue
model: opus
memory: project
isolation: worktree
effort: 3
permissionMode: auto
maxTurns: 50
skills:
  - mem-search
mcpServers:
  - tavily
disallowedTools:
  - TeamCreateTool
---

You are a research agent specialized in...
```

### Gaps & Improvements

#### 1. Markdown-Based Agent Definitions

**Gap**: Agents are hardcoded in Go, not user-extensible.

**Suggestion**: Implement markdown agent loading:
- Load from `~/.pi-go/agents/*.md` and `.pi-go/agents/*.md`
- Parse YAML frontmatter for metadata
- Render body as system instruction
- Support per-project agent overrides

#### 2. Agent Color System

**Gap**: No visual distinction between subagents in output.

**Suggestion**: Implement color assignment:
- Map agent types to terminal colors (red, blue, green, etc.)
- Assign dynamically from color pool
- Display agent color badge in TUI

#### 3. Agent Memory Scopes

**Gap**: Memory is global, not scoped per agent type.

**Suggestion**: Implement memory scopes:
- `user`: Cross-project agent memory at `~/.pi-go/agent-memory/{type}/`
- `project`: Project-local at `.pi-go/agent-memory/{type}/`
- Inject memory instructions into agent's system prompt

#### 4. Agent Effort/Efficiency Hints

**Gap**: No way to hint expected complexity.

**Suggestion**: Add effort metadata:
- `effort: 0-4` scale
- Map to timeout limits
- Influence model selection

---

## Task Management

### Current State (pi-go)

**No task system exists.**

### Reference (Claude Code)

Full task lifecycle system:

| Tool | Purpose |
|------|---------|
| `TaskCreateTool` | Create tasks, auto-expands task list UI |
| `TaskListTool` | List all tasks with blockedBy filtering |
| `TaskUpdateTool` | Update status, auto-assign owner, run hooks |
| `TaskGetTool` | Get single task details |

### Gaps & Improvements

#### 1. Task CRUD System

**Gap**: No task management for complex projects.

**Suggestion**: Implement task system:
```
~/.pi-go/tasks/{taskListId}/tasks/{taskId}.json
```

Task schema:
```json
{
  "id": "t_abc123",
  "type": "local_agent",
  "status": "pending",
  "description": "Implement auth module",
  "blockedBy": ["t_other"],
  "owner": null,
  "createdAt": 1234567890
}
```

#### 2. Task List UI

**Gap**: No visual task management in TUI.

**Suggestion**: Add sidebar panel:
- Show task list with status
- Click to assign/update
- Blocked tasks visually dimmed

#### 3. Task Hooks

**Gap**: No task lifecycle hooks.

**Suggestion**: Implement hooks:
- `taskCreatedHooks`: Run on task creation
- `taskCompletedHooks`: Run on task completion
- Notify owner via mailbox on assignment

---

## Inter-Agent Communication

### Current State (pi-go)

**No messaging system.** Subagents return results directly.

### Reference (Claude Code)

File-based mailbox with structured messages:

```
~/.pi-go/teams/{team}/inboxes/{agentName}.json
```

Message types:
- Plain text messages
- Structured control messages (shutdown_request, shutdown_response, plan_approval_response)
- Broadcast to all (`to: "*"`)

### Gaps & Improvements

#### 1. SendMessage Tool

**Gap**: No inter-agent messaging capability.

**Suggestion**: Implement SendMessage tool:
```go
type SendMessageInput struct {
    To      string      // agent name or "*"
    Message interface{} // string or control struct
    Summary string      // optional summary for inbox
}
```

#### 2. Mailbox System

**Gap**: No persistent message delivery.

**Suggestion**: Implement mailbox:
```go
type Mailbox struct {
    path string // ~/.pi-go/inboxes/{agentName}.json
}

func (m *Mailbox) Write(from, message string) error
func (m *Mailbox) Read() ([]Message, error)
func (m *Mailbox) Purge() error
```

#### 3. Auto-Resume on Message

**Gap**: Stopped agents can't receive messages.

**Suggestion**: Implement auto-resume:
- If target agent is stopped, read transcript from disk
- Restore file state cache
- Restart agent with same context

---

## Permission System

### Current State (pi-go)

Basic permission system in `internal/agent/agent.go`:
- **Ask mode**: Prompt user for each tool
- **Bypass mode**: Auto-approve all

### Reference (Claude Code)

Six permission modes:

| Mode | Behavior |
|------|----------|
| `default` | Ask user for every tool use |
| `acceptEdits` | Auto-approve file edits, ask for rest |
| `plan` | Approve once per plan, then auto-approve |
| `dontAsk` | Remember decisions for session |
| `bypassPermissions` | Auto-approve everything |
| `auto` | ML classifier decides |

### Gaps & Improvements

#### 1. Permission Modes

**Gap**: Only two modes implemented.

**Suggestion**: Implement remaining modes:
- `acceptEdits`: Auto-approve read, write, edit, git tools
- `plan`: Track approval state per plan phase
- `dontAsk`: Implement decision memory for session

#### 2. Swarm Permission Bridge

**Gap**: No way for worker agents to surface permissions to parent.

**Suggestion**: Implement permission delegation:
- Workers send permission requests to leader via mailbox
- Leader surfaces in leader's UI with worker badge
- User approves/denies, response sent back
- Bridge for in-process teammates to access leader's UI queue

#### 3. Rule-Based Permissions

**Gap**: No declarative allow/deny rules.

**Suggestion**: Implement permission rules:
```json
{
  "permissions": [
    { "tool": "Bash", "condition": "command.startsWith('git ')", "allow": true },
    { "tool": "Bash", "condition": "command.contains('rm -rf')", "deny": true }
  ]
}
```

#### 4. Denial Tracking

**Gap**: No feedback to LLM about denied operations.

**Suggestion**: Implement denial context:
- Track denials in session
- Inject denial context into system prompt
- Help LLM avoid repeated denied patterns

---

## Terminal UI

### Current State (pi-go)

Bubble Tea v2 TUI with:
- Markdown rendering via Glamour
- Message history with scroll
- Status bar with model/cost
- Slash command palette
- Theme support

### Reference (Claude Code)

React + Ink based TUI with richer components:
- Team member panes with color borders
- Inbox/message panel
- Task list sidebar
- Permission prompts with agent attribution
- Progress indicators for long operations

### Gaps & Improvements

#### 1. Team Member Panes

**Gap**: No visual representation of team members.

**Suggestion**: Implement pane layout:
- Virtual panes for in-process teammates
- Color borders per agent
- Message routing visualization

#### 2. Inbox Panel

**Gap**: No inbox for teammate messages.

**Suggestion**: Add inbox panel:
- Show pending messages from teammates
- Click to read full content
- Mark as processed

#### 3. Permission Attribution

**Gap**: Permission prompts don't show which agent requested.

**Suggestion**: Enhance permission UI:
- Show requesting agent name/color
- Display agent badge in permission dialog
- Log permission context for audit

#### 4. Streaming Progress

**Gap**: Basic progress indicators.

**Suggestion**: Enhance progress display:
- Per-agent progress bars
- Token usage per agent
- Elapsed time display

---

## Session & Memory

### Current State (pi-go)

Session management in `internal/session/`:
- JSONL append-only event log
- Branch support
- Compaction on token threshold
- Resume via `--continue` or `--session`

Memory in `internal/memory/`:
- SQLite with FTS5
- AI compression via subagent
- 3-layer search: index → timeline → full

### Reference (Claude Code)

Similar session/memory architecture with enhancements:
- Session metadata includes parent lineage
- File history tracking
- Task outputs persisted
- Agent memory snapshots

### Gaps & Improvements

#### 1. Session Lineage

**Gap**: No parent-child session tracking.

**Suggestion**: Add session lineage:
```json
{
  "sessionId": "abc123",
  "parentSessionId": "xyz789",
  "createdAt": 1234567890
}
```

#### 2. File History

**Gap**: No tracking of files modified.

**Suggestion**: Implement file history:
```json
{
  "path": "src/main.go",
  "action": "edit",
  "timestamp": 1234567890,
  "sessionId": "abc123"
}
```

#### 3. Task Output Persistence

**Gap**: Task outputs not persisted.

**Suggestion**: Persist task outputs:
```
~/.pi-go/sessions/{id}/tasks/{taskId}.json
```

---

## Tool System

### Current State (pi-go)

Core tools in `internal/tools/`:
- read, write, edit, bash, grep, find, ls, tree
- git-overview, git-file-diff, git-hunk
- lsp-diagnostics, lsp-definition, lsp-references, lsp-hover, lsp-symbols
- mem-search, mem-timeline, mem-get
- agent (subagent spawning), restart, screen

### Reference (Claude Code)

Extended toolset:
- TaskCreate/Update/List/Get tools
- SendMessage tool
- TeamCreate/TeamDelete tools
- Notebook tool (Jupyter)
- WebFetch/WebSearch tools
- AskUserQuestion tool

### Gaps & Improvements

#### 1. Task Tools

**Gap**: No task tools.

**Suggestion**: Implement TaskCreate, TaskUpdate, TaskList, TaskGet.

#### 2. Web Tools

**Gap**: No web fetch or search.

**Suggestion**: Implement:
- `web-fetch`: Fetch URL content, convert to text
- `web-search`: Search web, format results

#### 3. AskUserQuestion

**Gap**: No structured user questions.

**Suggestion**: Implement AskUserQuestion:
```go
type AskUserQuestionInput struct {
    Question string
    Options  []string // optional
}
```

#### 4. Notebook Tool

**Gap**: No Jupyter notebook support.

**Suggestion**: Implement notebook tool:
- Read/write notebook cells
- Preserve cell outputs
- Execute code cells

---

## Bash Security

### Current State (pi-go)

Sandbox via `os.Root`:
- Restricts access to working directory tree
- Prevents `..` escape
- No AST parsing of commands

### Reference (Claude Code)

Advanced bash security:
- AST parsing to understand command intent
- Semantic classification: read/write/destructive/network
- Path validation (no escape from working dir)
- Sandbox detection for untrusted commands
- Timeout enforcement (2min default, 10min max)
- Output truncation

### Gaps & Improvements

#### 1. Command AST Parsing

**Gap**: No analysis of command structure.

**Suggestion**: Implement command parser:
- Parse shell command into AST
- Extract operations (git, npm, docker, etc.)
- Detect dangerous patterns (pipe to shell, here-docs with variables)
- Classify intent: read-only vs. write vs. destructive

#### 2. Semantic Classification

**Gap**: No intent classification.

**Suggestion**: Classify commands:
```go
type CommandIntent int
const (
    IntentReadOnly CommandIntent = iota
    IntentWrite
    IntentDestructive
    IntentNetwork
    IntentUnknown
)
```

#### 3. Enhanced Sandbox

**Gap**: Basic sandbox only.

**Suggestion**: Implement OS-level sandbox:
- Use seatbelt (macOS) or seccomp (Linux) for high-risk commands
- Detect sandbox availability
- Fallback to os.Root for unsupported platforms

---

## Extensibility

### Current State (pi-go)

Extension system in `internal/extension/`:
- **Hooks**: Shell commands before/after tool calls
- **Skills**: *.SKILL.md reusable workflows
- **MCP**: External tool servers via subprocess

### Reference (Claude Code)

Full plugin system:
- Plugin loader with lifecycle
- Custom commands, tools, hooks per plugin
- User hooks (preToolUse, postToolUse, sessionStart)
- Feature flags with build-time elimination

### Gaps & Improvements

#### 1. Plugin System

**Gap**: No formal plugin architecture.

**Suggestion**: Implement plugin system:
```
~/.pi-go/plugins/{name}/
├── index.go
├── commands/
├── tools/
└── hooks/
```

#### 2. Feature Flags

**Gap**: No feature flag system.

**Suggestion**: Implement feature flags:
```go
type FeatureFlag struct {
    Name    string
    Enabled bool
}

func Feature(name string) bool
```

#### 3. Custom Hook Events

**Gap**: Limited hook events.

**Suggestion**: Add more hooks:
- `preSessionStart`
- `postMessageReceived`
- `preAgentSpawn`
- `postAgentComplete`

---

## IDE Integration

### Current State (pi-go)

**No IDE bridge.**

### Reference (Claude Code)

Full IDE bridge:
- JSON over stdio/WebSocket protocol
- Permission callbacks to IDE
- File attachment relay
- Status update streaming
- Device pairing (JWT)

### Gaps & Improvements

#### 1. Bridge Protocol

**Gap**: No communication protocol.

**Suggestion**: Implement bridge:
```
IDE Extension ◄────── Bridge Protocol ──────► CLI Agent
  (client)         (JSON messages over          (server)
                    stdio/WebSocket/UDS)
```

#### 2. Permission Delegation

**Gap**: Permissions only work in terminal.

**Suggestion**: Implement IDE permission UI:
- Send permission requests to IDE
- IDE surfaces its own dialog
- Response relayed back to agent

#### 3. Status Streaming

**Gap**: No real-time status to IDE.

**Suggestion**: Implement status updates:
- Progress indicators
- Token usage
- Error notifications

---

## Commands & Slash Commands

### Current State (pi-go)

Commands in `internal/tui/commands.go`:
- /help, /clear, /model, /session, /context, /branch
- /compact, /commit, /agents, /history, /plan, /run
- /login, /skills, /theme, /rtk, /ping, /restart, /exit, /quit

### Reference (Claude Code)

Extended command set:
- /doctor (environment diagnostics)
- /memory (manage persistent memory)
- /resume (resume previous session)
- /cost (show token usage and cost)
- /vim (toggle vim keybindings)
- /diff (show file changes)
- /config (view/edit settings)

### Gaps & Improvements

#### 1. /doctor Command

**Gap**: No environment diagnostics.

**Suggestion**: Implement /doctor:
- Check API key presence
- Verify model access
- Check required tools (git, rg, etc.)
- Report configuration issues

#### 2. /cost Command

**Gap**: No token cost display.

**Suggestion**: Implement /cost:
- Show input/output tokens
- Estimate API cost
- Display per-session and daily totals

#### 3. /vim Toggle

**Gap**: No vim mode toggle.

**Suggestion**: Implement vim keybindings:
- Normal/Insert mode switching
- Vim-style navigation (hjkl, w, b, etc.)
- `/` to search history

#### 4. /config View/Edit

**Gap**: No runtime config inspection.

**Suggestion**: Implement /config:
- Show current settings
- Allow editing of runtime options
- Save changes to config file

---

## Execution Backends

### Current State (pi-go)

**In-process execution only** via goroutines.

### Reference (Claude Code)

Three execution backends:

| Backend | Process Isolation | Communication |
|---------|-------------------|---------------|
| tmux | Separate process | File mailbox |
| iTerm2 | Separate process | File mailbox |
| In-process | Same process | Leader permission bridge |

### Gaps & Improvements

#### 1. tmux Backend

**Gap**: No tmux pane support.

**Suggestion**: Implement tmux backend:
- Detect if running inside tmux
- Create panes for teammates
- Layout management (vertical/horizontal splits)
- Color borders per agent

#### 2. iTerm2 Backend

**Gap**: No iTerm2 native integration.

**Suggestion**: Implement iTerm2 backend:
- Detect iTerm2 availability
- Use it2 CLI for split panes
- Native iTerm2 styling

#### 3. Backend Abstraction

**Gap**: No backend abstraction layer.

**Suggestion**: Define backend interface:
```go
type TeammateExecutor interface {
    Spawn(config SpawnConfig) (TeammateSpawnResult, error)
    SendMessage(agentID string, message Message) error
    Terminate(agentID string) error
    Kill(agentID string) error
    IsActive(agentID string) bool
}
```

---

## Priority Recommendations

### High Priority (Core Experience)

1. **Task Management System**
   - CRUD operations for tasks
   - Task list UI in sidebar
   - BlockedBy relationships

2. **SendMessage Tool + Mailbox**
   - Inter-agent messaging
   - File-based mailbox storage
   - Auto-resume capability

3. **Enhanced Permission Modes**
   - acceptEdits, plan, dontAsk modes
   - Denial tracking for LLM context
   - Swarm permission bridge

4. **/doctor Command**
   - Environment diagnostics
   - Configuration validation
   - Tool availability checks

### Medium Priority (Enhanced Features)

5. **Markdown Agent Definitions**
   - Load agents from ~/.pi-go/agents/*.md
   - Per-project agent overrides
   - Agent color system

6. **Fork Subagent Pattern**
   - Prompt cache optimization
   - Parallel execution with shared prefixes
   - Recursion guard

7. **Bash Security Enhancement**
   - Command AST parsing
   - Intent classification
   - Enhanced sandbox options

8. **IDE Bridge (Basic)**
   - Stdio protocol
   - Permission delegation
   - Status streaming

### Low Priority (Polish)

9. **Web Tools**
   - web-fetch
   - web-search

10. **tmux Backend**
    - Pane layout management
    - Color borders per agent

11. **Plugin System**
    - Plugin loader
    - Custom commands/tools/hooks

12. **Notebook Tool**
    - Jupyter cell editing
    - Output preservation

---

## Implementation Phases

### Phase 1: Foundation (2-3 weeks)
- Task CRUD system
- SendMessage + Mailbox
- Enhanced permission modes
- /doctor command

### Phase 2: Agent System (2-3 weeks)
- Markdown agent loading
- Agent color system
- Fork subagent pattern
- Memory scopes

### Phase 3: Coordination (2-3 weeks)
- Coordinator mode
- Team swarm basics
- Task hooks
- Swarm permission bridge

### Phase 4: Polish (1-2 weeks)
- tmux backend
- IDE bridge
- Web tools
- Plugin system

---

## Conclusion

pi-go has a solid foundation with its ADK-based architecture, subagent system, and memory implementation. The primary gaps are in multi-agent coordination (task management, messaging, team swarms) and permission flexibility. 

The recommendations prioritize improving the core multi-agent experience before adding polish features. Implement in phases, starting with task management and inter-agent messaging as they unlock the most value for complex projects.
