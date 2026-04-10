# Implementation Plan: Enhance pi-go with oh-my-pi Features

## Checklist

- [x] Step 1: Model Roles — Config & Resolution
- [x] Step 2: Model Roles — CLI & Provider Integration
- [x] Step 3: Git Tools — git-overview
- [x] Step 4: Git Tools — git-file-diff & git-hunk
- [x] Step 5: Git Tools — /commit Slash Command
- [x] Step 6: Subagent — Concurrency Pool & Types
- [x] Step 7: Subagent — Process Spawner & Streaming
- [x] Step 8: Subagent — Worktree Manager
- [x] Step 9: Subagent — Orchestrator & Agent Tool
- [x] Step 10: LSP — Protocol & Client
- [x] Step 11: LSP — Manager & Language Registry
- [x] Step 12: LSP — Hook Integration (format-on-write, diagnostics-on-edit)
- [x] Step 13: LSP — Explicit ADK Tools
- [x] Step 14: Integration & E2E Testing

---

## Step 1: Model Roles — Config & Resolution

**Objective:** Replace `DefaultModel` with role-based model routing in the config system.

**Implementation guidance:**
- Update `Config` struct in `internal/config/config.go`:
  - Remove `DefaultModel string`
  - Add `Roles map[string]RoleConfig`
  - Add `RoleConfig` struct with `Model` and `Provider` fields
- Update `Defaults()` to set `Roles["default"]` instead of `DefaultModel`
- Implement `ResolveRole(role string) (model, provider string, err error)`:
  - Look up role in map → fall back to "default" → return `ErrNoDefaultRole`
  - Auto-detect provider from model prefix if not specified in role config
- Update config merging logic to deep-merge roles map
- Update `Load()` to handle both old (`defaultModel`) and new (`roles`) formats for migration

**Test requirements:**
- `TestResolveRole_ExactMatch` — role exists, returns correct model+provider
- `TestResolveRole_FallbackToDefault` — unknown role falls back to default
- `TestResolveRole_NoDefault` — no default role returns error
- `TestResolveRole_AutoDetectProvider` — empty provider resolved from model prefix
- `TestConfigMerge_RolesOverride` — project roles override global roles

**Integration notes:** No other packages depend on this yet. Pure config change.

**Demo:** Unit tests pass. `config.Load()` correctly parses roles from JSON.

---

## Step 2: Model Roles — CLI & Provider Integration

**Objective:** Wire role resolution into CLI flags, provider selection, and agent startup.

**Implementation guidance:**
- Add CLI flags in `internal/cli/cli.go`:
  - `--smol`, `--slow`, `--plan` flags that set the active role for the session
  - `--model` flag now sets the `default` role at runtime (override)
- Update agent creation in `cli.go`:
  - Replace direct model name usage with `config.ResolveRole("default")`
  - Pass resolved model+provider to provider factory
- Update `/model` slash command in `internal/tui/tui.go`:
  - Show all configured roles with their model mappings
- Update provider resolution in `internal/provider/provider.go`:
  - Accept provider name from role config instead of only auto-detecting

**Test requirements:**
- `TestCLI_SmolFlag` — `--smol` resolves to smol role model
- `TestCLI_ModelFlagOverridesDefault` — `--model X` overrides default role
- `TestProviderFromRoleConfig` — explicit provider in role config used

**Integration notes:** This is the first point where the new config flows through the system. Existing `--model` flag behavior preserved.

**Demo:** Run `pi --smol` and verify it uses the smol role model. Run `pi` with no flags and verify default role used. `/model` shows all roles.

---

## Step 3: Git Tools — git-overview

**Objective:** Add the first git inspection tool, establishing the pattern for git tools.

**Implementation guidance:**
- Create `internal/tools/git_overview.go`:
  - `GitOverviewInput` struct with optional `include_staged`, `include_unstaged`, `include_untracked` booleans
  - `GitOverviewOutput` struct with branch, recent_commits, staged/unstaged/untracked file lists, upstream, ahead_behind
  - Handler runs git commands via `exec.Command` in sandbox directory:
    - `git rev-parse --abbrev-ref HEAD` for branch
    - `git log --oneline -10` for recent commits
    - `git status --porcelain` for file lists (parse porcelain output)
    - `git rev-list --left-right --count @{upstream}...HEAD` for ahead/behind
  - Handle non-git-repo case with clear error message
- Register `newGitOverviewTool` in `CoreTools()` in `internal/tools/registry.go`
- Update expected tool count in `internal/tools/tools_test.go`

**Test requirements:**
- `TestGitOverview_BasicRepo` — create temp git repo, add files, verify output
- `TestGitOverview_NotARepo` — run outside git repo, verify error
- `TestGitOverview_EmptyRepo` — no commits yet, verify graceful handling
- `TestGitOverview_PorcelainParsing` — verify staged/unstaged/untracked classification

**Integration notes:** First new tool. Establishes exec.Command pattern for git operations that git-diff and git-hunk will follow.

**Demo:** In a git repo with changes, LLM can call `git-overview` and see branch, commits, and changed files.

---

## Step 4: Git Tools — git-file-diff & git-hunk

**Objective:** Add file-level diff and hunk-level inspection tools.

**Implementation guidance:**
- Create `internal/tools/git_diff.go`:
  - `GitFileDiffInput` with `file` (required) and `staged` (optional bool)
  - `GitFileDiffOutput` with file, diff (unified), stats
  - Runs `git diff [--cached] -- <file>`
  - Truncate output to 100KB
  - Report binary files without content
- Create `internal/tools/git_hunk.go`:
  - `GitHunkInput` with `file` (required)
  - `GitHunkOutput` with file and `[]Hunk` array
  - `Hunk` struct: header, content, lines (added/removed counts)
  - Parse unified diff output splitting on `^@@` lines
  - Count `+`/`-` lines per hunk
- Register both in `CoreTools()`
- Update tool count in tests

**Test requirements:**
- `TestGitFileDiff_UnstagedChange` — modify file, verify diff output
- `TestGitFileDiff_StagedChange` — stage file, verify `--cached` diff
- `TestGitFileDiff_NoChange` — file unchanged, verify empty diff
- `TestGitFileDiff_BinaryFile` — binary file, verify binary notice
- `TestGitHunk_SingleHunk` — one change region
- `TestGitHunk_MultipleHunks` — changes in separate parts of file
- `TestGitHunk_LineCounting` — verify added/removed counts

**Integration notes:** These tools plus git-overview give the LLM full git awareness. Used by the reviewer subagent type.

**Demo:** LLM can inspect specific file diffs and individual hunks to understand changes before committing.

---

## Step 5: Git Tools — /commit Slash Command

**Objective:** Add interactive `/commit` command that uses the commit role model to generate conventional commit messages.

**Implementation guidance:**
- Add `/commit` handler in `internal/tui/tui.go` slash command dispatch:
  1. Run git-overview internally to check for changes
  2. If no staged changes, prompt user to stage or auto-stage all
  3. Run git-file-diff for each staged file, collect diffs
  4. Compose prompt with diffs asking for conventional commit message
  5. Send to LLM using `commit` role model (resolve via config)
  6. Display proposed commit message to user
  7. On user confirmation (enter), run `git commit -m "<message>"`
  8. On rejection, allow editing or regeneration
- Create a lightweight LLM call helper that uses the commit role:
  - Single-turn, no session, no tools
  - System instruction: "Generate a conventional commit message (type(scope): description) for the following changes."
- Handle edge cases: no changes, not a repo, commit fails

**Test requirements:**
- `TestCommitCommand_GeneratesConventionalFormat` — verify type(scope): format
- `TestCommitCommand_NoChanges` — verify helpful error message
- `TestCommitCommand_NotARepo` — verify error

**Integration notes:** First slash command that uses model roles. Demonstrates the commit role workflow. Requires Steps 1-4 completed.

**Demo:** User types `/commit`, sees generated message like `feat(tools): add git inspection tools`, confirms, commit is created.

---

## Step 6: Subagent — Concurrency Pool & Types

**Objective:** Create the foundational subagent package with concurrency control and agent type definitions.

**Implementation guidance:**
- Create `internal/subagent/` package
- Create `internal/subagent/pool.go`:
  - `Pool` struct with buffered channel semaphore
  - `NewPool(maxConcurrent int) *Pool`
  - `Acquire(ctx context.Context) error` — blocks until slot available or context cancelled
  - `Release()` — returns slot to pool
- Create `internal/subagent/types.go`:
  - `AgentTypeDef` struct: Role, Worktree bool, Instruction string, Tools []string
  - `AgentTypes` map with all 6 types and their configurations
  - `ValidateType(typeName string) error`
  - `AgentInput` and `AgentOutput` structs
  - `AgentStatus` struct for listing agents
  - `Event` struct for streaming

**Test requirements:**
- `TestPool_AcquireRelease` — basic acquire/release cycle
- `TestPool_BlocksAtLimit` — blocks when all slots taken
- `TestPool_ContextCancellation` — unblocks on context cancel
- `TestPool_ConcurrentAccess` — multiple goroutines acquire/release safely
- `TestAgentTypes_AllDefined` — all 6 types exist with valid configs
- `TestAgentTypes_RoleMappings` — each type maps to a valid role name
- `TestValidateType_Invalid` — unknown type returns error

**Integration notes:** No dependencies on other new code. Pure foundation.

**Demo:** Unit tests pass. Pool correctly limits concurrency.

---

## Step 7: Subagent — Process Spawner & Streaming

**Objective:** Implement spawning `pi` subprocesses and streaming their JSON output back.

**Implementation guidance:**
- Create `internal/subagent/spawner.go`:
  - `Spawner` struct holding config reference
  - `Spawn(ctx context.Context, opts SpawnOpts) (*Process, error)`:
    - Resolve model from role via config
    - Build command: `pi --mode json --model <model> [--workdir <path>]`
    - Start process with `exec.Command`, capture stdout pipe
    - Send prompt via stdin (or RPC socket)
    - Return `*Process` handle
  - `Process` struct:
    - `Events() <-chan Event` — channel of parsed JSONL events
    - `Wait() error` — wait for process exit
    - `Cancel()` — kill process
    - Internal goroutine reads stdout line-by-line, parses JSON events, sends to channel
  - Event parsing: unmarshal each line as JSON event (text_delta, tool_call, tool_result, message_end, error)
  - Accumulate text_delta events into final result string
  - Handle process crash: capture stderr, send error event

**Test requirements:**
- `TestSpawner_BasicExecution` — spawn pi process, verify events received
- `TestSpawner_StreamingEvents` — verify text_delta events arrive in order
- `TestSpawner_ProcessCrash` — verify error handling on crash
- `TestSpawner_Cancel` — verify process killed on cancel
- `TestSpawner_ContextTimeout` — verify cleanup on context timeout

**Integration notes:** Requires `pi` binary available in PATH. Integration tests may need to build the binary first.

**Demo:** Spawn a subagent process, observe streaming events, collect final result.

---

## Step 8: Subagent — Worktree Manager

**Objective:** Implement git worktree creation and cleanup for isolated subagent execution.

**Implementation guidance:**
- Create `internal/subagent/worktree.go`:
  - `WorktreeManager` struct with repoRoot, active map, mutex
  - `NewWorktreeManager(repoRoot string) *WorktreeManager`
  - `Create(agentID string) (path string, err error)`:
    - Branch name: `pi-agent-<agentID[:8]>`
    - Worktree path: `<repoRoot>/.pi-go/worktrees/<agentID[:8]>/`
    - Run `git worktree add -b <branch> <path> HEAD`
    - Track in active map
  - `Cleanup(agentID string) error`:
    - Run `git worktree remove <path>`
    - Run `git branch -D <branch>`
    - Remove from active map
    - Best-effort: log errors but don't fail
  - `MergeBack(agentID string) error`:
    - Run `git merge --no-ff <branch>` from main worktree
    - Return merge output (including conflicts if any)
  - `CleanupAll() error` — cleanup all active worktrees (for shutdown)

**Test requirements:**
- `TestWorktree_CreateAndCleanup` — create worktree, verify path exists, cleanup, verify removed
- `TestWorktree_BranchNaming` — verify branch name format
- `TestWorktree_MergeBack` — create worktree, make change, merge back
- `TestWorktree_MergeConflict` — conflicting changes, verify error reported
- `TestWorktree_CleanupAll` — multiple worktrees cleaned up on shutdown
- `TestWorktree_NotARepo` — graceful error outside git repo

**Integration notes:** Uses real git operations in temp directories. `.pi-go/worktrees/` directory added to `.gitignore`.

**Demo:** Create worktree, ls the directory, verify it's a separate working copy, cleanup.

---

## Step 9: Subagent — Orchestrator & Agent Tool

**Objective:** Wire pool, spawner, and worktree manager together. Register the `agent` ADK tool.

**Implementation guidance:**
- Create `internal/subagent/orchestrator.go`:
  - `Orchestrator` struct composing Pool, Spawner, WorktreeManager, config
  - `NewOrchestrator(cfg *config.Config) *Orchestrator`
  - `Spawn(ctx context.Context, input AgentInput) (<-chan Event, error)`:
    1. Validate agent type
    2. Acquire pool slot
    3. If agent type requires worktree, create one
    4. Spawn pi process (with worktree path if applicable)
    5. Return event channel
    6. On completion: release pool slot, cleanup worktree if needed
  - `List() []AgentStatus` — return status of all agents
  - `Cancel(agentID string) error` — cancel running agent
  - `Shutdown()` — cancel all, cleanup all worktrees
- Create `internal/tools/agent.go`:
  - `AgentInput` struct: type, prompt, worktree override, background
  - Tool handler:
    1. Call orchestrator.Spawn()
    2. Consume event channel, accumulate result
    3. Return final AgentOutput with result text
  - Register `newAgentTool` factory (receives *Orchestrator instead of *Sandbox)
- Update `CoreTools()` signature or create separate `AgentTools()` for tools needing orchestrator
- Update CLI to create orchestrator and pass to agent setup
- Add `/agents` slash command to TUI showing running/completed agents

**Test requirements:**
- `TestOrchestrator_SpawnExplore` — explore agent uses smol role, no worktree
- `TestOrchestrator_SpawnTask` — task agent gets worktree
- `TestOrchestrator_ConcurrencyLimit` — respects pool limit
- `TestOrchestrator_Cancel` — cancels running agent, cleans up
- `TestOrchestrator_Shutdown` — all agents stopped, worktrees cleaned
- `TestAgentTool_Registration` — tool registered with correct schema

**Integration notes:** This is the integration point for Steps 6-8. Requires config roles (Step 1-2) for model resolution. Updates CLI startup flow.

**Demo:** LLM can call `agent` tool with type="explore" and prompt, receives streaming results. `/agents` shows agent status.

---

## Step 10: LSP — Protocol & Client

**Objective:** Implement the LSP JSON-RPC 2.0 client with Content-Length framing.

**Implementation guidance:**
- Create `internal/lsp/protocol.go`:
  - LSP types: `Position`, `Range`, `Location`, `TextDocumentIdentifier`, `TextDocumentPositionParams`
  - `Diagnostic` struct: range, severity, message, source
  - `DocumentSymbol` struct: name, kind, range, children
  - `TextEdit` struct: range, newText
  - `HoverResult` struct: contents (MarkupContent)
  - JSON-RPC types: `Request`, `Response`, `Notification`
- Create `internal/lsp/client.go`:
  - `Client` struct: process cmd, stdin writer, stdout reader, pending requests map, next ID
  - `NewClient(command string, args []string) (*Client, error)`:
    - Start subprocess via `exec.Command`
    - Set up stdin/stdout pipes
    - Start reader goroutine for responses
  - `Request(method string, params any) (json.RawMessage, error)`:
    - Assign ID, encode JSON-RPC request with Content-Length header
    - Write to stdin
    - Wait on response channel (with timeout)
  - `Notify(method string, params any) error`:
    - Encode notification (no ID), write to stdin
  - Reader goroutine:
    - Parse `Content-Length: N\r\n\r\n` header
    - Read N bytes, unmarshal JSON-RPC response
    - Route to pending request by ID
    - Handle server-initiated notifications (e.g., publishDiagnostics)
  - `Close()` — send shutdown/exit, kill process

**Test requirements:**
- `TestClient_ContentLengthFraming` — verify correct header encoding
- `TestClient_RequestResponse` — mock server, send request, receive response
- `TestClient_Notification` — verify no response expected
- `TestClient_ConcurrentRequests` — multiple requests in flight
- `TestClient_ServerCrash` — verify error propagation
- `TestClient_Timeout` — verify request timeout handling

**Integration notes:** Foundation for all LSP operations. No dependency on other new packages.

**Demo:** Unit tests with mock LSP server pass. Content-Length framing works correctly.

---

## Step 11: LSP — Manager & Language Registry

**Objective:** Implement language detection, server lifecycle management, and the built-in language configs.

**Implementation guidance:**
- Create `internal/lsp/languages.go`:
  - `LanguageConfig` struct: Command, Args, FileExtensions, RootMarkers, InitOptions
  - `DefaultLanguages` map with Go, TypeScript, Python, Rust configs
  - `DetectLanguage(filePath string) string` — match file extension to language name
  - `FindRoot(filePath string, markers []string) string` — walk up to find project root
- Create `internal/lsp/manager.go`:
  - `Manager` struct: config map, servers map (language → *Server), mutex
  - `NewManager(cfg *LSPConfig) *Manager`:
    - Merge default languages with user config
    - Check which language servers are actually installed (`exec.LookPath`)
    - Log warnings for missing servers
  - `ServerFor(filePath string) (*Server, error)`:
    - Detect language from file extension
    - If server running, return it
    - If not, start new server:
      1. Find project root from file path
      2. Create LSP client
      3. Send `initialize` request with rootUri, capabilities
      4. Send `initialized` notification
      5. Cache in servers map
    - Return server or error
  - `Server` struct wrapping `Client` with higher-level methods:
    - `Diagnostics(file string) ([]Diagnostic, error)`
    - `Definition(file string, line, col int) ([]Location, error)`
    - `References(file string, line, col int) ([]Location, error)`
    - `Hover(file string, line, col int) (*HoverResult, error)`
    - `Symbols(file string) ([]DocumentSymbol, error)`
    - `Format(file string) ([]TextEdit, error)`
    - Each method handles `textDocument/didOpen` if file not yet opened
  - `Shutdown()` — send shutdown to all servers, wait, kill

**Test requirements:**
- `TestDetectLanguage` — .go→go, .ts→typescript, .py→python, .rs→rust, .txt→""
- `TestFindRoot_GoMod` — walk up dirs to find go.mod
- `TestManager_ServerLifecycle` — start, reuse, shutdown
- `TestManager_MissingServer` — warning logged, no error
- `TestManager_ConfigOverride` — user config overrides defaults

**Integration notes:** Depends on Step 10 (client). Used by Steps 12 and 13.

**Demo:** Manager detects .go file, starts gopls, sends initialize, can query for diagnostics.

---

## Step 12: LSP — Hook Integration (format-on-write, diagnostics-on-edit)

**Objective:** Automatically format files and report diagnostics after write/edit tool calls.

**Implementation guidance:**
- Create `internal/lsp/hooks.go` (or add to `internal/extension/hooks.go`):
  - `BuildLSPAfterToolCallback(mgr *lsp.Manager) llmagent.AfterToolCallback`:
    - Triggered after `write` and `edit` tools
    - Extract `file_path` from tool args
    - Call `mgr.ServerFor(filePath)` — skip if no server for this language
    - **For write tool:**
      1. Notify server: `textDocument/didOpen` or `textDocument/didChange`
      2. Request formatting: `textDocument/formatting`
      3. Apply text edits to the file on disk
      4. Notify server of the formatted content
    - **For both write and edit:**
      1. Request diagnostics (or wait for `publishDiagnostics` notification)
      2. Filter to errors and warnings (skip hints/info)
      3. Format as readable string: `"file.go:10:5: error: undeclared name: foo"`
      4. Append to tool result map: `result["lsp_diagnostics"] = formatted`
    - Handle timeouts: 5s for format, 3s for diagnostics
    - Handle errors silently (log, don't fail the tool call)
- Update agent creation in `internal/cli/cli.go`:
  - Create LSP Manager from config
  - Build LSP callback
  - Append to `AfterToolCallbacks` in agent config
  - Register shutdown hook for LSP manager

**Test requirements:**
- `TestLSPHook_FormatOnWrite` — write .go file → verify formatted
- `TestLSPHook_DiagnosticsOnEdit` — edit .go file with error → verify diagnostics in result
- `TestLSPHook_NoServerForFileType` — write .txt file → no LSP action, no error
- `TestLSPHook_Timeout` — slow server → skip gracefully
- `TestLSPHook_ServerNotInstalled` — no gopls → skip, no error

**Integration notes:** This is where LSP becomes invisible to the LLM — it just works. Diagnostics appear in tool results, so the LLM can self-correct errors. Requires Steps 10-11.

**Demo:** Edit a Go file to introduce a syntax error via the edit tool. The tool result includes LSP diagnostics showing the error. LLM sees and fixes it.

---

## Step 13: LSP — Explicit ADK Tools

**Objective:** Register 5 LSP tools for the LLM to call explicitly when it needs code intelligence.

**Implementation guidance:**
- Create `internal/tools/lsp.go`:
  - `newLSPDiagnosticsTool(mgr *lsp.Manager) (tool.Tool, error)`:
    - Input: `{file: string}`
    - Output: `{diagnostics: [{file, line, column, severity, message, source}]}`
  - `newLSPDefinitionTool(mgr *lsp.Manager) (tool.Tool, error)`:
    - Input: `{file: string, line: int, column: int}`
    - Output: `{locations: [{file, line, column}]}` or `{error: "not found"}`
  - `newLSPReferencesTool(mgr *lsp.Manager) (tool.Tool, error)`:
    - Input: `{file: string, line: int, column: int}`
    - Output: `{references: [{file, line, column}]}`
  - `newLSPHoverTool(mgr *lsp.Manager) (tool.Tool, error)`:
    - Input: `{file: string, line: int, column: int}`
    - Output: `{content: string}` — markdown-formatted type/doc info
  - `newLSPSymbolsTool(mgr *lsp.Manager) (tool.Tool, error)`:
    - Input: `{file: string}`
    - Output: `{symbols: [{name, kind, line, end_line}]}`
  - Create `LSPTools(mgr *lsp.Manager) ([]tool.Tool, error)` factory
  - Each tool: if no LSP server for file type, return helpful message ("no language server configured for .xyz files")
- Update CLI to call `LSPTools()` and add to agent toolset
- Update tool count in tests

**Test requirements:**
- `TestLSPDefinition_GoFunction` — find definition of a Go function
- `TestLSPReferences_GoType` — find references to a Go type
- `TestLSPHover_GoVariable` — hover shows type info
- `TestLSPSymbols_GoFile` — list functions and types
- `TestLSPDiagnostics_SyntaxError` — verify error reported
- `TestLSPTools_NoServer` — graceful message for unsupported language

**Integration notes:** Depends on Steps 10-11. These tools give the LLM explicit code intelligence when needed beyond the automatic hooks. Tool descriptions should explain when to use them vs relying on auto-diagnostics.

**Demo:** LLM calls `lsp-definition` on a function call, gets the file and line of the definition. Calls `lsp-symbols` to understand file structure before editing.

---

## Step 14: Integration & E2E Testing

**Objective:** Verify all features work together end-to-end. Polish, fix edge cases.

**Implementation guidance:**
- Create `internal/agent/e2e_enhanced_test.go`:
  - **E2E: Role switching** — start agent, verify default model. Spawn smol subagent, verify different model used.
  - **E2E: Git commit flow** — create temp repo, make changes, trigger /commit, verify conventional commit created
  - **E2E: Subagent explore** — spawn explore agent, ask about test fixtures, verify reads files and returns answer
  - **E2E: Subagent task with worktree** — spawn task agent, verify worktree created, changes made in isolation, worktree cleaned up
  - **E2E: LSP diagnostics** — edit Go file with error, verify diagnostics in tool result, LLM self-corrects
  - **E2E: Full workflow** — agent uses git-overview to see changes, spawns reviewer subagent, reviewer uses lsp-diagnostics, reports findings
- Update `Makefile` with new test targets:
  - `make test-unit` — unit tests only
  - `make test-integration` — integration tests (needs git, gopls)
  - `make test-e2e` — full E2E tests (needs pi binary, language servers)
- Update `ARCHITECTURE.md` with new packages and feature documentation
- Add `.pi-go/worktrees/` to `.gitignore`
- Verify `pi --help` shows new flags
- Verify config loading with new schema
- Performance check: verify LSP server startup doesn't slow agent launch (on-demand)

**Test requirements:**
- All E2E tests listed above
- Regression: existing tests still pass
- Build verification: `go build ./...` succeeds
- Vet: `go vet ./...` passes

**Integration notes:** This step ties everything together. All prior steps must be complete. Focus on edge cases and error paths.

**Demo:** Full agent session: user asks to review code, agent uses git-overview, spawns reviewer subagent, reviewer uses LSP tools, reports findings with diagnostics. User runs /commit, gets conventional commit message.
