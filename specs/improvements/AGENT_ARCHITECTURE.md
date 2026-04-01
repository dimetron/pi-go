# Agent Harness & Multi-Agent Orchestration Architecture

> Detailed architecture reference for the agent execution harness, multi-agent coordination,
> and swarm orchestration system in Claude Code. Derived from source analysis of the
> March 31 2026 snapshot (~512K lines TypeScript).

---

## Table of Contents

1. [System Overview](#system-overview)
2. [Agent Tool — The Spawn Primitive](#agent-tool--the-spawn-primitive)
3. [Agent Definitions & Loading](#agent-definitions--loading)
4. [Agent Execution Engine](#agent-execution-engine)
5. [Fork Subagent — Cache-Optimized Parallel Spawning](#fork-subagent--cache-optimized-parallel-spawning)
6. [Coordinator Mode — Three-Tier Orchestration](#coordinator-mode--three-tier-orchestration)
7. [Team Swarm System](#team-swarm-system)
8. [Inter-Agent Messaging](#inter-agent-messaging)
9. [Task Management System](#task-management-system)
10. [Permission System (Multi-Agent)](#permission-system-multi-agent)
11. [Agent Isolation Modes](#agent-isolation-modes)
12. [Agent State & Bootstrap](#agent-state--bootstrap)
13. [Query Pipeline & LLM Loop](#query-pipeline--llm-loop)
14. [Execution Backends (tmux / iTerm2 / In-Process)](#execution-backends)
15. [Agent Memory & Persistence](#agent-memory--persistence)
16. [Key Architectural Patterns](#key-architectural-patterns)
17. [File Manifest](#file-manifest)

---

## 1. System Overview

Claude Code supports **four distinct multi-agent patterns**, layered on top of a single
agent execution primitive (`AgentTool`):

```
┌─────────────────────────────────────────────────────────────────┐
│                         User Session                            │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                    Main REPL Loop                         │  │
│  │  QueryEngine → LLM → Tool Execution → Permission Gate    │  │
│  └──────────┬────────────────────┬───────────────────────────┘  │
│             │                    │                               │
│    ┌────────▼────────┐  ┌───────▼────────────┐                  │
│    │   AgentTool      │  │   TeamCreateTool   │                  │
│    │  (single agent)  │  │  (swarm of agents) │                  │
│    └────────┬─────────┘  └───────┬────────────┘                  │
│             │                    │                               │
│  ┌──────────▼────────────────────▼──────────────────────────┐   │
│  │              Agent Execution Harness                      │   │
│  │                                                           │   │
│  │  ┌─────────┐ ┌──────────┐ ┌───────────┐ ┌────────────┐  │   │
│  │  │  Sync   │ │  Async   │ │   Fork    │ │Coordinator │  │   │
│  │  │ (inline)│ │(background│ │(cache-opt)│ │  (3-tier)  │  │   │
│  │  └─────────┘ └──────────┘ └───────────┘ └────────────┘  │   │
│  │                                                           │   │
│  │  ┌──────────────────────────────────────────────────┐    │   │
│  │  │           Execution Backends                      │    │   │
│  │  │  In-Process │ tmux Pane │ iTerm2 Pane │ Remote   │    │   │
│  │  └──────────────────────────────────────────────────┘    │   │
│  └───────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Cross-Cutting: Permissions · Mailbox · Tasks · Memory     │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

**The four patterns:**

| Pattern | Entry Tool | Execution | Communication | Use Case |
|---------|-----------|-----------|---------------|----------|
| **Sync Agent** | `AgentTool` | Inline in parent turn | Return value | Explore, Plan, quick tasks |
| **Async Agent** | `AgentTool` (background) | Detached task | `SendMessage` / notifications | Long-running research |
| **Fork Subagent** | `AgentTool` (implicit) | Parallel async | Cache-shared prefixes | Parallel independent tasks |
| **Coordinator** | Feature flag | 3-tier hierarchy | Task notifications + XML | Complex multi-step projects |
| **Team Swarm** | `TeamCreateTool` | tmux/iTerm2/in-process | File-based mailbox | Collaborative multi-agent |

---

## 2. Agent Tool — The Spawn Primitive

**File:** `src/tools/AgentTool/AgentTool.tsx` (3,772 lines)

The `AgentTool` is the single entry point for all agent spawning. Every other pattern
(fork, coordinator, swarm) builds on top of it.

### Input Schema

```typescript
// Base schema (always available)
{
  description: string          // 3-5 word task summary
  prompt: string               // Full task instructions
  subagent_type?: string       // Agent type selector
  model?: 'sonnet' | 'opus' | 'haiku'  // Model override
  run_in_background?: boolean  // Force async execution
}

// Extended schema (when agent swarms enabled)
{
  ...base,
  name?: string                // SendMessage-addressable name
  team_name?: string           // Team context association
  mode?: PermissionMode        // 'plan' | 'default' | 'auto' | ...
  isolation?: 'worktree' | 'remote'  // Execution isolation
}
```

### Spawn Lifecycle

```
AgentTool.call()
  │
  ├── 1. VALIDATE
  │   ├── Check team_name eligibility (isAgentSwarmsEnabled)
  │   ├── Check teammate constraints (no nested teams, no bg from teams)
  │   └── Validate subagent_type against available definitions
  │
  ├── 2. SELECT AGENT
  │   ├── Resolve subagent_type → AgentDefinition
  │   ├── If subagent_type omitted + fork enabled → implicit fork
  │   ├── Filter by MCP server requirements
  │   └── Apply allowedAgentTypes restrictions
  │
  ├── 3. MCP VERIFICATION
  │   ├── Wait for pending MCP servers (up to 30s)
  │   └── Verify required servers have tools available
  │
  ├── 4. BUILD CONTEXT
  │   ├── Call agent.getSystemPrompt({toolUseContext})
  │   ├── Inject environment details
  │   ├── Create user message with prompt
  │   └── Set color via setAgentColor()
  │
  ├── 5. DETERMINE EXECUTION MODE
  │   ├── run_in_background: true → ASYNC
  │   ├── agent.background: true → ASYNC
  │   ├── coordinatorMode → ASYNC
  │   ├── forkSubagentEnabled → ASYNC (all spawns)
  │   └── Default → SYNC
  │
  ├── 6. SETUP ISOLATION
  │   ├── worktree → createAgentWorktree(slug)
  │   ├── remote → teleportToRemote() [ant-only]
  │   └── none → inherit parent cwd
  │
  ├── 7a. ASYNC PATH                    7b. SYNC PATH
  │   ├── registerAsyncAgent()           ├── registerAgentForeground()
  │   ├── Store name → agentId           ├── Start auto-background timer (120s)
  │   ├── Fire-and-forget lifecycle      ├── Stream messages from runAgent()
  │   └── Return async_launched          ├── Race: messages vs background signal
  │                                      ├── On background → transition to async
  │                                      └── Return completed + result
  │
  └── 8. CLEANUP
      ├── Check worktree for changes
      ├── Clean if unchanged, keep if dirty
      ├── Clear invoked skills & dump state
      └── Disconnect agent-specific MCP clients
```

---

## 3. Agent Definitions & Loading

**File:** `src/tools/AgentTool/loadAgentsDir.ts` (756 lines)

### Agent Definition Types

```typescript
type BaseAgentDefinition = {
  agentType: string                    // Unique identifier
  whenToUse: string                    // Description for LLM selection
  tools?: string[]                     // Allowed tools (undefined = '*')
  disallowedTools?: string[]           // Blocked tools
  skills?: string[]                    // Preloaded slash commands
  mcpServers?: AgentMcpServerSpec[]    // Required MCP servers
  hooks?: HooksSettings                // Session-scoped hooks
  color?: AgentColorName               // Terminal color
  model?: string                       // Model override or 'inherit'
  effort?: EffortValue                 // 0-4
  permissionMode?: PermissionMode      // 'auto' | 'acceptEdits' | 'plan' | 'bubble'
  maxTurns?: number                    // Turn limit
  background?: boolean                 // Force background execution
  isolation?: 'worktree' | 'remote'    // Execution isolation
  memory?: AgentMemoryScope            // 'user' | 'project' | 'local'
}

// Three concrete subtypes:
type BuiltInAgentDefinition    // Dynamic prompts via getSystemPrompt() callback
type CustomAgentDefinition     // User/project agents from markdown files
type PluginAgentDefinition     // Plugin-provided agents
```

### Built-In Agents

| Agent | File | Tools | Purpose |
|-------|------|-------|---------|
| `general-purpose` | `built-in/generalPurposeAgent.ts` | `*` | Multi-tool general agent |
| `Explore` | `built-in/exploreAgent.ts` | Read-only | Fast codebase exploration |
| `Plan` | `built-in/planAgent.ts` | Read-only | Architecture & planning |
| `verification` | `built-in/verificationAgent.ts` | All except Agent | Testing & verification |
| `claude-code-guide` | `built-in/claudeCodeGuideAgent.ts` | Read-only + web | Documentation guide |
| `statusline-setup` | `built-in/statuslineSetup.ts` | Read + Edit | Terminal status config |

### Loading Pipeline

```
getAgentDefinitionsWithOverrides()
  │
  ├── 1. Load built-in agents          (lowest priority)
  ├── 2. Load plugin agents
  ├── 3. Load custom agents from markdown:
  │      ├── ~/.claude/agents/*.md      (user scope)
  │      ├── .claude/agents/*.md        (project scope)
  │      └── CLI flag agents
  ├── 4. Load managed/policy agents     (highest priority)
  │
  ├── 5. Merge all sources
  │      └── getActiveAgentsFromList(): dedup by agentType,
  │          last source wins (higher priority overwrites lower)
  │
  ├── 6. Initialize colors for active agents
  └── 7. Check memory snapshots (if auto-memory enabled)
```

### Custom Agent Markdown Format

```markdown
---
name: my-researcher
description: Deep research agent for API documentation
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
hooks:
  preToolCall:
    - command: "echo Starting tool..."
---

You are a research agent specialized in API documentation...
```

---

## 4. Agent Execution Engine

**File:** `src/tools/AgentTool/runAgent.ts` (10,499 lines)

The execution engine is an **async generator** that yields messages as they stream
from the LLM. It wraps the full query pipeline with agent-specific context.

### Execution Flow

```
runAgent(config)
  │
  ├── Create QueryEngine with:
  │   ├── Agent's system prompt
  │   ├── Agent's tool pool (filtered by definition)
  │   ├── Agent's permission mode
  │   ├── Initial messages (user prompt or forked context)
  │   ├── Model override (if specified)
  │   └── Max turns limit
  │
  ├── For each LLM turn:
  │   ├── Yield streaming events to parent
  │   ├── Execute tool calls via permission gate
  │   ├── Track token usage
  │   └── Check abort signal / max turns
  │
  └── On completion:
      ├── Yield final assistant message
      └── Return terminal status
```

### Finalization

**File:** `src/tools/AgentTool/agentToolUtils.ts` (687 lines)

```typescript
finalizeAgentTool() → AgentToolResult {
  agentId: string
  agentType: string
  content: TextBlock[]          // Last text from agent
  totalDurationMs: number
  totalTokens: number
  totalToolUseCount: number
  usage: UsageBlock
}
```

---

## 5. Fork Subagent — Cache-Optimized Parallel Spawning

**File:** `src/tools/AgentTool/forkSubagent.ts` (211 lines)

Fork is a specialized spawn mode that enables **prompt-cache-optimized parallel execution**.
When enabled, omitting `subagent_type` triggers an implicit fork instead of selecting
a built-in agent.

### How Fork Works

```
Parent makes N tool_use calls (without subagent_type)
  │
  ├── buildForkedMessages() for each child:
  │   │
  │   ├── 1. Clone parent's FULL assistant message
  │   │      (all tool_use blocks, thinking, text — byte-identical)
  │   │
  │   ├── 2. Build tool_result blocks for ALL tool_use blocks
  │   │      Each gets identical placeholder: "Fork started — processing in background"
  │   │      (byte-identical across all children)
  │   │
  │   └── 3. Append per-child directive text block
  │          (only this differs between children)
  │
  └── Result: [assistantMessage, userMessage(tool_results + directive)]
              ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
              Byte-identical prefix across all children
              → Maximizes Anthropic API prompt cache hits
```

### Fork Agent Definition

```typescript
const FORK_AGENT = {
  agentType: 'fork',
  tools: ['*'],                    // Inherits parent's exact tool pool
  permissionMode: 'bubble',        // Surfaces permission prompts to parent terminal
  model: 'inherit',                // Keeps parent's model
  maxTurns: 200,
}
```

### Recursion Guard

```typescript
isInForkChild(): boolean
  // Scans conversation history for <fork_boilerplate> tag
  // Returns true → prevents fork-of-fork infinite recursion
```

---

## 6. Coordinator Mode — Three-Tier Orchestration

**File:** `src/coordinator/coordinatorMode.ts`

Coordinator mode is a feature-flagged (`COORDINATOR_MODE`) orchestration pattern that
creates a **three-tier hierarchy**: Coordinator → Workers → Tools.

### Architecture

```
┌──────────────────────────────────────────────┐
│              COORDINATOR (Leader)              │
│                                                │
│  Understands conversation context              │
│  Synthesizes findings before delegating        │
│  Directs workers with self-contained prompts   │
│  Reports results to user                       │
└──────────┬───────────────────────────────────┘
           │ spawns via AgentTool (always async)
           │
    ┌──────┼──────────────────────┐
    │      │                      │
    ▼      ▼                      ▼
┌────────┐ ┌────────┐       ┌────────┐
│Worker 1│ │Worker 2│  ...  │Worker N│
│        │ │        │       │        │
│Research│ │Implement       │Verify  │
└───┬────┘ └───┬────┘       └───┬────┘
    │          │                │
    ▼          ▼                ▼
  Tools      Tools            Tools
  (scoped)   (scoped)         (scoped)
```

### Worker Task Notification Format

Workers report results back as XML blocks injected into the coordinator's conversation:

```xml
<task-notification>
  <task-id>a_abc123</task-id>
  <status>completed</status>
  <summary>Found 3 API endpoints matching the pattern</summary>
  <result>Detailed findings...</result>
  <usage>input_tokens=1234 output_tokens=567</usage>
</task-notification>
```

### Coordinator System Prompt (Key Sections)

The 368-line system prompt encodes orchestration philosophy:

1. **Parallelism as superpower** — Launch independent workers concurrently
2. **Synthesis requirement** — Coordinator must understand findings before directing follow-up (never delegate understanding)
3. **Continue vs. spawn decision** — Context overlap → continue same worker; fresh task → spawn new
4. **Self-contained prompts** — Workers can't see conversation; include file paths, line numbers, done criteria
5. **Verification philosophy** — Prove code works, don't rubber-stamp; run tests with feature enabled
6. **Worker failure handling** — Continue same worker with error context rather than abandoning

### Worker Tool Restrictions

Workers see a filtered tool set:

```typescript
// Excluded from workers:
TeamCreateTool       // No nested teams
TeamDeleteTool       // Only coordinator manages teams
SendMessageTool      // Workers communicate via task results
SyntheticOutputTool  // Internal use only
```

### Scratchpad Directory

When enabled, coordinator and workers share a writable scratchpad directory for
intermediate artifacts. All workers can read/write without permission prompts.

---

## 7. Team Swarm System

**File:** `src/tools/TeamCreateTool/TeamCreateTool.ts`, `src/utils/swarm/`

Teams are a higher-level orchestration pattern where multiple agents run as
**persistent, named teammates** with direct messaging, shared task lists,
and visual layout in the terminal.

### Team Lifecycle

```
TeamCreateTool.call({ team_name, description })
  │
  ├── 1. Generate unique team name (collision check + word slug)
  │
  ├── 2. Create team file: ~/.claude/teams/{teamName}/config.json
  │      {
  │        name, description, createdAt,
  │        leadAgentId: "team-lead@{teamName}",
  │        members: [leader],
  │        teamAllowedPaths: []
  │      }
  │
  ├── 3. Create task list: ~/.claude/tasks/{teamName}/
  │
  ├── 4. Register in AppState:
  │      appState.teamContext = {
  │        teamName, teamFilePath, leadAgentId,
  │        isLeader: true,
  │        teammates: { [leadId]: { name, color, ... } }
  │      }
  │
  └── 5. Return { team_name, team_file_path, lead_agent_id }

  Then leader spawns teammates via AgentTool with team_name param:
  │
  AgentTool.call({ prompt, team_name, name: "researcher" })
    │
    ├── Backend detection → tmux / iTerm2 / in-process
    ├── Create pane or in-process context
    ├── Register member in team file
    ├── Teammate initializes with team context
    └── Communication via mailbox + SendMessage
```

### Team File Structure

```typescript
type TeamFile = {
  name: string
  description?: string
  createdAt: number
  leadAgentId: string              // "team-lead@my-team"
  leadSessionId?: string
  hiddenPaneIds?: string[]
  teamAllowedPaths?: TeamAllowedPath[]
  members: Array<{
    agentId: string                // "researcher@my-team"
    name: string                   // "researcher"
    agentType?: string
    model?: string
    prompt?: string
    color?: AgentColorName
    planModeRequired?: boolean
    joinedAt: number
    tmuxPaneId: string             // "" for in-process
    cwd: string
    worktreePath?: string
    sessionId?: string
    subscriptions: string[]
    backendType?: BackendType      // 'tmux' | 'iterm2' | 'in-process'
    isActive?: boolean
    mode?: PermissionMode
  }>
}
```

### Team Deletion

```
TeamDeleteTool.call({ team_name })
  │
  ├── Verify no active non-lead members remain
  ├── cleanupTeamDirectories() → remove team + task dirs
  ├── Destroy git worktrees for each member
  ├── Kill orphaned tmux/iTerm2 panes
  ├── Clear AppState teamContext
  └── Clear teammate colors
```

---

## 8. Inter-Agent Messaging

**File:** `src/tools/SendMessageTool/SendMessageTool.ts`, `src/utils/teammateMailbox.ts`

### Message Types

```typescript
// Plain text message
{ to: "researcher", message: "Check the auth module", summary: "Review auth" }

// Broadcast to all teammates
{ to: "*", message: "Switching to implementation phase" }

// Structured messages (control flow)
{ to: "researcher", message: { type: 'shutdown_request', reason: 'Task complete' } }
{ to: "team-lead", message: { type: 'shutdown_response', request_id: '...', approve: true } }
{ to: "implementer", message: { type: 'plan_approval_response', request_id: '...', approve: true } }
```

### Routing Architecture

```
SendMessageTool.call({ to, message })
  │
  ├── to: "name" → Single teammate
  │   └── writeToMailbox(recipientName, message, teamName)
  │
  ├── to: "*" → Broadcast
  │   ├── Read team file for member list
  │   └── writeToMailbox() to each (except sender)
  │
  ├── to: "uds:..." → Unix domain socket (bridge)
  │
  └── Structured messages:
      ├── shutdown_request → Queue in recipient mailbox
      ├── shutdown_response (approve) → abortController.abort()
      ├── shutdown_response (reject) → Forward reason to leader
      ├── plan_approval_response (approve) → Unblock teammate
      └── plan_approval_response (reject) → Forward feedback
```

### Mailbox System

```
Storage: ~/.claude/teams/{teamName}/inboxes/{agentName}.json

┌──────────────────────────────────────┐
│ [                                     │
│   {                                   │
│     "from": "team-lead",             │
│     "text": "Check auth module",     │
│     "timestamp": "2026-03-31T...",   │
│     "read": false,                   │
│     "color": "blue",                 │
│     "summary": "Review auth"        │
│   },                                  │
│   ...                                 │
│ ]                                     │
└──────────────────────────────────────┘

Operations:
  writeToMailbox(name, msg, team)  → append with file lock
  readMailbox(name, team)          → read all messages
  getInboxPath(name, team)         → file path resolution
```

### Auto-Resume

If `SendMessage` targets a stopped agent, the system automatically resumes it:
1. Read transcript from disk
2. Restore file state cache
3. Restart async lifecycle with same agent context

---

## 9. Task Management System

**Files:** `src/Task.ts`, `src/utils/tasks.ts`, `src/tools/Task*Tool/`

### Task Types

```typescript
type TaskType =
  | 'local_bash'            // Shell command task
  | 'local_agent'           // AgentTool async task
  | 'remote_agent'          // Remote CCR task
  | 'in_process_teammate'   // In-process swarm member
  | 'local_workflow'        // Workflow execution
  | 'monitor_mcp'           // Monitor tool task
  | 'dream'                 // Background dream task

type TaskStatus = 'pending' | 'running' | 'completed' | 'failed' | 'killed'
```

### Task ID Generation

```typescript
generateTaskId(type) → string
  // Prefix by type:
  //   't' = in_process_teammate
  //   'a' = local_agent
  //   'r' = remote_agent
  //   'b' = local_bash
  //   'w' = local_workflow
  //   'm' = monitor_mcp
  //   'd' = dream
  // + unique suffix
```

### Task Tools

| Tool | Purpose | Key Behavior |
|------|---------|-------------|
| `TaskCreateTool` | Create tasks | Auto-expands task list UI; runs `taskCreatedHooks` |
| `TaskListTool` | List all tasks | Filters completed blockers from `blockedBy`; read-only |
| `TaskUpdateTool` | Update task state | Auto-assigns owner on `in_progress`; runs `taskCompletedHooks`; notifies new owner via mailbox |
| `TaskGetTool` | Get single task | Returns full task details |

### Task Storage

```
~/.claude/tasks/{taskListId}/
  └── tasks/
      ├── {taskId_1}.json
      ├── {taskId_2}.json
      └── ...
```

### In-Process Teammate Task State

```typescript
type InProcessTeammateTaskState = TaskStateBase & {
  type: 'in_process_teammate'
  identity: {
    agentId: string             // "researcher@my-team"
    agentName: string
    teamName: string
    color?: string
    planModeRequired: boolean
    parentSessionId: string
  }
  prompt: string
  abortController?: AbortController
  awaitingPlanApproval: boolean
  permissionMode: PermissionMode
  messages?: Message[]           // UI mirror (capped at 50)
  pendingUserMessages: string[]
  isIdle: boolean
  shutdownRequested: boolean
}
```

---

## 10. Permission System (Multi-Agent)

**Files:** `src/hooks/toolPermission/handlers/`

Three distinct permission dispatch paths optimize for different agent execution contexts:

### Permission Handler Dispatch

```
Tool invocation arrives
  │
  ├── Is swarm worker? ──yes──→ swarmWorkerHandler
  │                               │
  │                               ├── Try classifier (sequential)
  │                               ├── If allows → done
  │                               └── Else:
  │                                   ├── Create permission request
  │                                   ├── Send to leader's mailbox
  │                                   ├── Show pendingWorkerRequest UI
  │                                   └── Wait for leader response
  │
  ├── Is coordinator? ──yes──→ coordinatorHandler
  │                              │
  │                              ├── Run hooks (sequential)
  │                              ├── Run classifier (sequential)
  │                              ├── If either resolves → done
  │                              └── Else → fall through to interactive
  │
  └── Default ──────────────→ interactiveHandler
                                │
                                ├── Push ToolUseConfirm to UI queue
                                ├── Run hooks + classifier (async, background)
                                ├── RACE: user interaction vs. background checks
                                ├── User clicks → cancels background
                                └── Background resolves → auto-approve/deny
```

### Swarm Permission Request Flow

```
Worker                          Leader
  │                               │
  ├── createPermissionRequest()   │
  ├── registerCallback(reqId)     │
  ├── sendViaMailbox(request) ──→ │
  │   [waiting...]                ├── Read from mailbox
  │                               ├── Show in permission UI
  │                               ├── User approves/denies
  │                               ├── sendResponseViaMailbox() ──→ │
  │   [callback fires]            │                                │
  ├── Apply decision              │
  └── Continue execution          │
```

### Permission Request Schema

```typescript
type SwarmPermissionRequest = {
  id: string                       // "perm-{timestamp}-{random}"
  workerId: string
  workerName: string
  workerColor?: string
  teamName: string
  toolName: string
  toolUseId: string
  description: string
  input: Record<string, unknown>
  permissionSuggestions: unknown[]
  status: 'pending' | 'approved' | 'rejected'
  resolvedBy?: 'worker' | 'leader'
  feedback?: string
  updatedInput?: Record<string, unknown>
  permissionUpdates?: PermissionUpdate[]
  createdAt: number
}
```

### In-Process Permission Bridge

For in-process teammates, a bridge allows direct access to the leader's UI:

```typescript
// Leader registers its UI queue
registerLeaderToolUseConfirmQueue(setter)

// Teammate uses leader's UI directly (no mailbox needed)
const queue = getLeaderToolUseConfirmQueue()
if (queue) {
  // Show permission dialog in leader's terminal with worker badge
  queue.push(toolUseConfirm)
} else {
  // Fall back to mailbox system
  sendPermissionRequestViaMailbox(request)
}
```

---

## 11. Agent Isolation Modes

### No Isolation (Default)

Agent inherits parent's working directory. All file operations share the same filesystem.

### Git Worktree Isolation

```
AgentTool.call({ isolation: 'worktree' })
  │
  ├── Create: createAgentWorktree("agent-{id[:8]}")
  │   └── git worktree add /tmp/.../agent-abc12345
  │       Returns: { worktreePath, worktreeBranch, headCommit, gitRoot }
  │
  ├── Execute: runWithCwdOverride(worktreePath, agentFn)
  │   └── getCwd() inside agent returns worktree path
  │       All file ops, bash, tools use worktree root
  │
  └── Cleanup: cleanupWorktreeIfNeeded()
      ├── hasWorktreeChanges(path, headCommit)?
      │   ├── No changes → removeAgentWorktree(path, branch, root)
      │   └── Has changes → keep worktree, return path in metadata
      └── Hook-based worktrees always kept
```

### Remote Isolation (Ant-Only)

```
AgentTool.call({ isolation: 'remote' })
  │
  ├── teleportToRemote() → delegates to CCR (Cloud Code Runner)
  ├── Registers RemoteAgentTask
  ├── Always async execution
  └── Returns session URL for monitoring
```

---

## 12. Agent State & Bootstrap

### Bootstrap State

**File:** `src/bootstrap/state.ts` (427 lines)

```typescript
// Process-level singleton state
const STATE = {
  // Session identity
  sessionId: string
  parentSessionId?: string           // For plan → implementation lineage

  // Project identity
  projectRoot: string                // Stable — NOT updated by worktree entry
  originalCwd: string
  cwd: string

  // Agent colors
  agentColorMap: Map<string, AgentColorName>
  agentColorIndex: number

  // Telemetry, counters, flags...
}
```

### AppState Team Context

**File:** `src/state/AppStateStore.ts`

```typescript
type AppState = {
  // Team coordination
  teamContext?: {
    teamName: string
    teamFilePath: string
    leadAgentId: string
    selfAgentId?: string
    selfAgentName: string
    isLeader: boolean
    teammates: { [id: string]: TeammateInfo }
  }

  // Agent name registry (for SendMessage routing)
  agentNameRegistry: Map<string, AgentId>

  // Inbox (pending messages from teammates)
  inbox: {
    messages: Array<{
      id: string
      from: string
      text: string
      status: 'pending' | 'processing' | 'processed'
      color?: string
      summary?: string
    }>
  }

  // Tasks (mutable, holds AbortControllers)
  tasks: { [taskId: string]: TaskState }

  // Permission context
  toolPermissionContext: ToolPermissionContext

  // Worker sandbox permissions
  workerSandboxPermissions: { queue: SandboxRequest[] }
}
```

### Teammate Identity Detection

**File:** `src/utils/teammate.ts` (293 lines)

Three-layer priority resolution:

```
getAgentId() / getTeamName() / isTeammate()
  │
  ├── 1. AsyncLocalStorage (in-process teammates)
  │      └── getTeammateContext() → per-async-scope identity
  │
  ├── 2. dynamicTeamContext (tmux teammates via CLI args)
  │      └── setDynamicTeamContext({ agentId, name, teamName, ... })
  │
  └── 3. AppState.teamContext (leaders)
         └── getAppState().teamContext.selfAgentId
```

---

## 13. Query Pipeline & LLM Loop

**File:** `src/query.ts`

The query pipeline is the core execution loop shared by all agents (main session,
sub-agents, teammates).

### Query Loop State

```typescript
type State = {
  messages: Message[]
  toolUseContext: ToolUseContext
  autoCompactTracking?: AutoCompactTrackingState
  maxOutputTokensRecoveryCount: number
  hasAttemptedReactiveCompact: boolean
  pendingToolUseSummary?: Promise<ToolUseSummaryMessage | null>
  stopHookActive?: boolean
  turnCount: number
  transition?: Continue           // Why loop continued
}
```

### Iteration Sequence

```
query(params) → AsyncGenerator<StreamEvent | Message, Terminal>
  │
  while (true) {
  │
  ├── 1. Snapshot query tracking (chainId, depth++)
  ├── 2. Apply tool result budget
  ├── 3. Run snip compact (if enabled)
  ├── 4. Run microcompact (if needed)
  ├── 5. Prefetch skills + memory
  │
  ├── 6. callModel(messages, systemPrompt, tools)
  │      └── Stream response tokens
  │
  ├── 7. runTools(toolCalls)
  │      └── Permission gate → execute → collect results
  │
  ├── 8. Handle stop hooks
  │      ├── Main agent: Stop hooks
  │      └── Teammates: TaskCompleted + TeammateIdle hooks
  │
  └── 9. Decision:
         ├── continue (max_output_tokens recovery, autocompact, etc.)
         └── stop_reason: 'end_turn' → return terminal
  }
```

### Injected Dependencies

```typescript
type QueryDeps = {
  callModel: typeof queryModelWithStreaming
  microcompact: typeof microcompactMessages
  autocompact: typeof autoCompactIfNeeded
  uuid: () => string
}
```

Production uses real implementations; tests inject mocks without per-module spyOn.

---

## 14. Execution Backends

**Files:** `src/utils/swarm/backends/`

### Backend Detection Priority

```
detectAndGetBackend()
  │
  ├── Inside tmux? → TmuxBackend (native)
  │
  ├── In iTerm2?
  │   ├── it2 CLI available? → ITermBackend (native)
  │   ├── tmux available? → TmuxBackend (with it2 setup prompt)
  │   └── Neither? → ERROR with install instructions
  │
  ├── tmux available? → TmuxBackend (external session)
  │
  └── No tmux? → ERROR with platform-specific install instructions

isInProcessEnabled()
  │
  ├── Non-interactive (-p flag)? → Always in-process
  ├── Mode override 'in-process'? → In-process
  ├── Mode override 'tmux'? → Pane backend
  └── Mode 'auto' (default):
      ├── Prior spawn failed? → In-process (fallback)
      ├── Inside tmux/iTerm2? → Pane backend
      └── Otherwise → In-process
```

### Backend Comparison

| Feature | tmux | iTerm2 | In-Process |
|---------|------|--------|------------|
| Process isolation | Separate process | Separate process | Same Node.js process |
| Pane management | Native splits | Native splits | Virtual (AppState UI) |
| Hide/show panes | Yes | No | N/A |
| Color borders | Yes | Yes | Via AppState |
| Communication | File mailbox | File mailbox | Leader permission bridge + mailbox |
| Termination | kill-pane | kill session | AbortController.abort() |
| Context isolation | Process boundary | Process boundary | AsyncLocalStorage |
| Shared resources | None | None | API client, MCP connections |

### tmux Backend Layout

```
Inside tmux:                      External session:
┌─────────┬──────────────┐        ┌──────────────────────┐
│         │  Teammate 1  │        │     Teammate 1       │
│ Leader  ├──────────────┤        ├──────────────────────┤
│  (30%)  │  Teammate 2  │        │     Teammate 2       │
│         ├──────────────┤        ├──────────────────────┤
│         │  Teammate 3  │        │     Teammate 3       │
└─────────┴──────────────┘        └──────────────────────┘
   claude-swarm session              Equal-sized panes
```

### In-Process Spawn Flow

```
spawnInProcessTeammate(config, context)
  │
  ├── Generate agentId: formatAgentId(name, teamName) → "name@team"
  ├── Generate taskId: generateTaskId('in_process_teammate')
  ├── Create independent AbortController (survives leader interrupts)
  ├── Create TeammateContext via AsyncLocalStorage
  │
  ├── Register InProcessTeammateTaskState in AppState:
  │   {
  │     type: 'in_process_teammate',
  │     status: 'running',
  │     identity: { agentId, agentName, teamName, color, ... },
  │     prompt, model, abortController,
  │     permissionMode: planModeRequired ? 'plan' : 'default',
  │     messages: [],
  │     pendingUserMessages: [],
  │     isIdle: false,
  │   }
  │
  ├── Register cleanup handler (abort on graceful shutdown)
  └── Return { agentId, taskId, abortController, teammateContext }
```

### Pane Backend Executor

Wraps any `PaneBackend` into a `TeammateExecutor` interface:

```typescript
interface TeammateExecutor {
  spawn(config): Promise<TeammateSpawnResult>
  sendMessage(agentId, message): Promise<void>
  terminate(agentId, reason): Promise<void>    // Graceful
  kill(agentId): Promise<void>                 // Force
  isActive(agentId): boolean
}
```

CLI arguments propagated to teammate process:
```
--agent-id <id>
--agent-name <name>
--team-name <team>
--agent-color <color>
--parent-session-id <sessionId>
--plan-mode-required       (if applicable)
+ inherited CLI flags (model, settings, plugins, chrome)
+ inherited env vars (API provider, proxy, config dir)
```

---

## 15. Agent Memory & Persistence

**Files:** `src/tools/AgentTool/agentMemory.ts`, `agentMemorySnapshot.ts`

### Memory Scopes

```typescript
type AgentMemoryScope = 'user' | 'project' | 'local'

// Storage locations:
'user'    → ~/.claude/agent-memory/{agentType}/
'project' → .claude/agent-memory/{agentType}/
'local'   → .claude/agent-memory/{agentType}/  (project-local)
```

### Memory Integration

When an agent has `memory` defined:

1. **At definition load:** Inject Write/Edit/Read tools into agent's tool pool
2. **At spawn:** Call `loadAgentMemoryPrompt()` → append memory instructions to system prompt
3. **During execution:** Agent can read/write its memory directory
4. **Snapshots:** `checkAgentMemorySnapshot()` auto-initializes from project if enabled

### Agent Color System

**File:** `src/tools/AgentTool/agentColorManager.ts`

```typescript
type AgentColorName = 'red' | 'blue' | 'green' | 'yellow' |
                      'purple' | 'orange' | 'pink' | 'cyan'

// Color → Theme mapping
'red'    → 'red_FOR_SUBAGENTS_ONLY'
'blue'   → 'blue_FOR_SUBAGENTS_ONLY'
// ... etc

// Special: 'general-purpose' agent → no color (undefined)

setAgentColor(agentType, color)  // Store in global map
getAgentColor(agentType)         // Lookup from global map
```

---

## 16. Key Architectural Patterns

### Pattern 1: Resolve-Once Guard (Race Condition Protection)

```typescript
// Used in permission handlers to prevent multiple resolutions
const { resolve, isResolved, claim } = createResolveOnce(resolve)

function onAllow() {
  if (!claim()) return   // Atomic check-and-mark
  resolveOnce(decision)  // Only first caller wins
}
```

User interaction, hooks, and classifier all race to resolve — only the first wins.

### Pattern 2: Byte-Identical Prefix for Cache Sharing (Fork)

All fork children share byte-identical API request prefixes. Only the per-child
directive text differs. This maximizes Anthropic's prompt cache hit rate across
parallel agents.

### Pattern 3: AsyncLocalStorage for In-Process Isolation

```typescript
// Each in-process teammate runs in its own async context
runWithTeammateContext(teammateContext, async () => {
  // getTeammateColor(), getTeamName(), getAgentId()
  // all resolve from AsyncLocalStorage — no parameter threading
})
```

### Pattern 4: Feature Gate Snapshot

```typescript
// Query config snapshots gates ONCE at entry
type QueryConfig = {
  gates: {
    streamingToolExecution: boolean
    fastModeEnabled: boolean
    // ...
  }
}
// Prevents model seeing gate value changes mid-conversation
```

### Pattern 5: Dual Communication Channels

- **In-Process:** Leader UI dialog + permission bridge (synchronous, immediate)
- **Pane-Based:** File mailbox with lockfiles (asynchronous, persistent)
- Both converge to same `SwarmPermissionRequest` schema

### Pattern 6: Lazy Schema with Dead Code Elimination

```typescript
const inputSchema = lazySchema(() => {
  const schema = feature('KAIROS') ? fullInputSchema() : baseInputSchema()
  return isBackgroundTasksDisabled
    ? schema.omit({ run_in_background: true })
    : schema
})
// Feature flags + conditional fields eliminated at Bun bundle time
```

### Pattern 7: Stable Project Root

```typescript
// projectRoot set ONLY at startup, never updated by worktree entry
setProjectRoot(cwd)         // Called once
getProjectRoot()            // Always returns original

// Skills, history, and memory stay anchored to original project
// while agent operates in throwaway worktree
```

### Pattern 8: Teammate Mode Snapshot

```typescript
// Captured once at startup, frozen for session lifetime
captureTeammateModeSnapshot()
getTeammateModeFromSnapshot()  // Returns captured mode

// Runtime config changes don't take effect until next session
// CLI args override config (highest precedence)
```

---

## 17. File Manifest

### Core Agent Infrastructure

| File | Lines | Purpose |
|------|-------|---------|
| `src/tools/AgentTool/AgentTool.tsx` | 3,772 | Main spawn primitive |
| `src/tools/AgentTool/runAgent.ts` | 10,499 | Agent execution engine |
| `src/tools/AgentTool/loadAgentsDir.ts` | 756 | Agent definition loading & parsing |
| `src/tools/AgentTool/agentToolUtils.ts` | 687 | Tool resolution & finalization |
| `src/tools/AgentTool/forkSubagent.ts` | 211 | Fork subagent (cache-optimized) |
| `src/tools/AgentTool/agentMemory.ts` | 178 | Persistent agent memory |
| `src/tools/AgentTool/agentMemorySnapshot.ts` | 198 | Memory snapshots |
| `src/tools/AgentTool/agentColorManager.ts` | 67 | Color assignment |
| `src/tools/AgentTool/agentDisplay.ts` | 105 | Display utilities |
| `src/tools/AgentTool/resumeAgent.ts` | — | Resume background agents |
| `src/tools/AgentTool/builtInAgents.ts` | — | Built-in agent registry |
| `src/tools/AgentTool/prompt.ts` | — | Tool prompt generation |
| `src/tools/AgentTool/UI.tsx` | — | React UI components |

### Coordinator

| File | Lines | Purpose |
|------|-------|---------|
| `src/coordinator/coordinatorMode.ts` | ~500 | Coordinator mode + 368-line system prompt |

### Swarm Infrastructure

| File | Lines | Purpose |
|------|-------|---------|
| `src/utils/swarm/constants.ts` | — | Session names, socket names, env vars |
| `src/utils/swarm/spawnUtils.ts` | — | CLI flags & env var propagation |
| `src/utils/swarm/spawnInProcess.ts` | — | In-process teammate spawning |
| `src/utils/swarm/inProcessRunner.ts` | 1,000+ | In-process teammate execution loop |
| `src/utils/swarm/teamHelpers.ts` | — | Team file CRUD, cleanup, worktree mgmt |
| `src/utils/swarm/permissionSync.ts` | 600+ | Permission request/response protocol |
| `src/utils/swarm/leaderPermissionBridge.ts` | — | In-process permission UI bridge |
| `src/utils/swarm/reconnection.ts` | — | Team context initialization & resume |
| `src/utils/swarm/teammateInit.ts` | — | Teammate hooks & team-wide permissions |
| `src/utils/swarm/teammatePromptAddendum.ts` | — | System prompt additions for teammates |
| `src/utils/swarm/teammateLayoutManager.ts` | — | Pane layout & color management |

### Execution Backends

| File | Lines | Purpose |
|------|-------|---------|
| `src/utils/swarm/backends/registry.ts` | — | Backend detection & caching |
| `src/utils/swarm/backends/detection.ts` | — | tmux/iTerm2 environment detection |
| `src/utils/swarm/backends/types.ts` | — | PaneBackend & TeammateExecutor interfaces |
| `src/utils/swarm/backends/TmuxBackend.ts` | — | tmux pane management |
| `src/utils/swarm/backends/ITermBackend.ts` | — | iTerm2 native split panes |
| `src/utils/swarm/backends/InProcessBackend.ts` | — | Same-process execution |
| `src/utils/swarm/backends/PaneBackendExecutor.ts` | — | PaneBackend → TeammateExecutor adapter |
| `src/utils/swarm/backends/teammateModeSnapshot.ts` | — | Mode capture at startup |

### Team & Task Tools

| File | Lines | Purpose |
|------|-------|---------|
| `src/tools/TeamCreateTool/TeamCreateTool.ts` | — | Team creation |
| `src/tools/TeamDeleteTool/TeamDeleteTool.ts` | — | Team deletion & cleanup |
| `src/tools/SendMessageTool/SendMessageTool.ts` | — | Inter-agent messaging |
| `src/tools/TaskCreateTool/TaskCreateTool.ts` | — | Task creation |
| `src/tools/TaskUpdateTool/TaskUpdateTool.ts` | — | Task updates + hooks |
| `src/tools/TaskListTool/TaskListTool.ts` | — | Task listing |
| `src/tools/TaskGetTool/TaskGetTool.ts` | — | Task retrieval |

### State & Bootstrap

| File | Lines | Purpose |
|------|-------|---------|
| `src/bootstrap/state.ts` | 427 | Global session state singleton |
| `src/state/AppStateStore.ts` | 570 | Full AppState type + defaults |
| `src/state/store.ts` | 35 | Generic pub/sub store |
| `src/state/onChangeAppState.ts` | 150+ | State change hooks (CCR sync) |
| `src/state/selectors.ts` | 77 | Pure state derivations |

### Permission Handlers

| File | Lines | Purpose |
|------|-------|---------|
| `src/hooks/toolPermission/PermissionContext.ts` | 389 | Permission context factory |
| `src/hooks/toolPermission/handlers/interactiveHandler.ts` | 400+ | Main agent (racing model) |
| `src/hooks/toolPermission/handlers/swarmWorkerHandler.ts` | 150+ | Worker → leader delegation |
| `src/hooks/toolPermission/handlers/coordinatorHandler.ts` | 65 | Coordinator (sequential) |

### Query Pipeline

| File | Lines | Purpose |
|------|-------|---------|
| `src/query.ts` | 900+ | Core query loop |
| `src/query/config.ts` | 47 | Gate snapshot |
| `src/query/deps.ts` | 41 | Injected I/O dependencies |
| `src/query/stopHooks.ts` | 473 | Post-query hook execution |
| `src/QueryEngine.ts` | ~46,000 | Full LLM query engine |

### Utilities

| File | Lines | Purpose |
|------|-------|---------|
| `src/utils/teammate.ts` | 293 | Teammate identity detection |
| `src/utils/teammateMailbox.ts` | — | File-based mailbox system |
| `src/utils/teammateContext.ts` | — | AsyncLocalStorage context |
| `src/utils/agentSwarmsEnabled.ts` | 45 | Swarms feature gate |
| `src/utils/worktreeModeEnabled.ts` | 11 | Worktree mode (always on) |
| `src/constants/tools.ts` | — | Tool availability rules |
| `src/tools.ts` | 390 | Tool registry & conditional loading |

---

## Tool Availability Matrix

| Tool | Main Agent | Sync Sub-Agent | Async Sub-Agent | Coordinator Worker | Team Teammate |
|------|-----------|---------------|----------------|-------------------|--------------|
| BashTool | Yes | Yes | Yes | Yes | Yes |
| FileRead/Write/Edit | Yes | Yes | Yes | Yes | Yes |
| Glob/Grep | Yes | Yes | Yes | Yes | Yes |
| AgentTool | Yes | No* | No* | No* | No* |
| TeamCreateTool | Yes | No | No | No | No |
| TeamDeleteTool | Yes | No | No | No | No |
| SendMessageTool | Yes | No | No | No | Yes |
| TaskCreate/Update/List | Yes | No | No | No | Yes |
| AskUserQuestion | Yes | No | No | No | No |
| EnterPlanMode | Yes | No | No | No | No |
| SkillTool | Yes | Yes | Yes | Yes | Yes |
| WebFetch/Search | Yes | Yes | Yes | Yes | Yes |

*AgentTool available for ant users (`USER_TYPE=ant`)
