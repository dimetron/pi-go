# Implementation Plan: Skills Subagents

## Checklist

- [ ] Step 1: Agent definition parsing and discovery
- [ ] Step 2: Bundled agent markdown files
- [ ] Step 3: Environment filtering and timeout management
- [ ] Step 4: Enhanced orchestrator and spawner
- [ ] Step 5: Subagent tool with single mode
- [ ] Step 6: Parallel execution mode
- [ ] Step 7: Chain execution mode
- [ ] Step 8: TUI pipeline visualization
- [ ] Step 9: Slash commands and CLI wiring
- [ ] Step 10: Integration testing and polish

---

## Step 1: Agent Definition Parsing and Discovery

**Objective:** Create the agent discovery system that loads markdown-defined agents from bundled, user, and project directories with priority override.

**Implementation guidance:**
- Create `internal/subagent/agents.go` with types: `AgentScope`, `AgentConfig`, `AgentDiscoveryResult`
- Implement `ParseAgentFile()` — parse YAML frontmatter (name, description, tools, role, worktree, timeout) and markdown body as system prompt
- Reuse or adapt the existing frontmatter parsing from `internal/extension/skill.go` if applicable
- Implement `LoadAgentsFromDir()` — read all `.md` files from a directory, call `ParseAgentFile` on each
- Implement `DiscoverAgents(cwd, scope)` — load from bundled + user + project dirs, apply priority override by name (project > user > bundled)
- `findNearestProjectAgentsDir()` — walk up from cwd looking for `.pi-go/agents/`

**Test requirements:**
- Test `ParseAgentFile` with valid frontmatter, missing fields, empty body
- Test `LoadAgentsFromDir` with mixed valid/invalid files, empty directory, missing directory
- Test `DiscoverAgents` priority: project overrides user overrides bundled
- Test scope filtering: user-only, project-only, both

**Integration notes:**
- No other component depends on this yet — pure data layer
- Agent format should match what `step 2` produces

**Demo:** Run tests showing agent files parsed from temp directories with correct priority override.

---

## Step 2: Bundled Agent Markdown Files

**Objective:** Convert the 6 existing hardcoded agent types to markdown files, add code-reviewer and spec-reviewer, embed via `go:embed`.

**Implementation guidance:**
- Create `internal/subagent/bundled/` directory
- Create 8 markdown files converting from `AgentTypes` map in `types.go`:
  - `explore.md` — role: smol, worktree: false, tools: read, grep, find, tree, ls
  - `plan.md` — role: plan, worktree: false, tools: read, grep, find, tree, ls, git-overview
  - `designer.md` — role: slow, worktree: true, tools: read, write, edit, grep, find, tree, ls, bash
  - `task.md` — role: default, worktree: true, tools: read, write, edit, bash, grep, find, tree, ls, git-overview
  - `quick-task.md` — role: smol, worktree: false, tools: read, write, edit, bash, grep, find
  - `worker.md` — role: default, worktree: false, tools: read, write, edit, bash, grep, find (new, from pi-superpowers-plus)
  - `code-reviewer.md` — role: slow, worktree: false, tools: read, grep, find, git-overview, git-file-diff, git-hunk (split from reviewer)
  - `spec-reviewer.md` — role: slow, worktree: false, tools: read, grep, find, git-overview, git-file-diff, git-hunk (split from reviewer)
- Create `embed.go` with `//go:embed *.md` directive
- Implement `LoadBundledAgents()` using `bundled.FS`
- Remove `AgentTypes` map from `types.go` and `ValidateType()` function

**Test requirements:**
- Test `LoadBundledAgents()` returns exactly 8 agents with correct names
- Test each bundled agent has required fields (name, description, role, tools)
- Test embedded FS contains all expected files

**Integration notes:**
- Step 1's `DiscoverAgents` now uses `LoadBundledAgents()` as the bundled source
- Wire together: `DiscoverAgents` calls `LoadBundledAgents()` + `LoadAgentsFromDir()` for user/project

**Demo:** Run `DiscoverAgents(".", "both")` and print all 8 bundled agents with their configs.

---

## Step 3: Environment Filtering and Timeout Management

**Objective:** Create subprocess environment isolation and configurable timeout resolution.

**Implementation guidance:**
- Create `internal/subagent/env.go`:
  - `AllowedPrefixes`: PI_, GO, LC_, XDG_
  - `AllowedExplicit`: PATH, HOME, SHELL, TERM, USER, TMPDIR, EDITOR, API keys, base URLs
  - `BuildSubagentEnv(extra)` — filter `os.Environ()` against allowlist, add passthrough from `PI_SUBAGENT_ENV_PASSTHROUGH`, add extras
  - Return `[]string` (key=value format for `exec.Cmd.Env`)
- Create `internal/subagent/timeout.go`:
  - Constants: `DefaultInactivityTimeout = 120s`, `DefaultAbsoluteTimeout = 10min`
  - `GetAbsoluteTimeout(agentTimeoutMs int)` — agent → env → default
  - `GetInactivityTimeout()` — env → default
  - Read `PI_SUBAGENT_TIMEOUT_MS` and `PI_SUBAGENT_INACTIVITY_MS`

**Test requirements:**
- Test env filtering: only allowed vars pass through
- Test prefix matching (PI_FOO passes, SECRET_FOO doesn't)
- Test API keys are included
- Test passthrough parsing from comma-separated string
- Test timeout resolution chain: agent > env > default
- Test invalid env values fall back to defaults

**Integration notes:**
- These are pure utility functions, no dependencies on other new code
- Used by spawner in Step 4

**Demo:** Run tests showing filtered env and timeout resolution.

---

## Step 4: Enhanced Orchestrator and Spawner

**Objective:** Modify orchestrator to accept `AgentConfig` instead of hardcoded types, add timeout management and env filtering to spawner.

**Implementation guidance:**
- Modify `internal/subagent/orchestrator.go`:
  - Add `OrchestratorOption` pattern: `WithPoolSize(int)`, `WithInactivityTimeout(duration)`, `WithAbsoluteTimeout(duration)`
  - Read `PI_SUBAGENT_CONCURRENCY` env var for default pool size (default: 6)
  - Change `Spawn()` to accept `SpawnInput{Agent: AgentConfig, Prompt, Cwd, Worktree, SkipCleanup}`
  - Resolve model via `cfg.ResolveRole(agent.Role)` instead of `AgentTypes[type].Role`
  - Worktree decision: `input.Worktree` override > `agent.Worktree` default
- Modify `internal/subagent/spawner.go`:
  - Update `SpawnOpts` to include `Tools []string`, `InactivityTimeout`, `AbsoluteTimeout`, `Env []string`
  - Add `--tools` flag to subprocess command when agent has tool whitelist
  - Implement inactivity timer: reset on each stdout line, kill on expiry
  - Implement absolute timer: kill after max duration regardless of activity
  - Set `cmd.Env` to filtered environment from `BuildSubagentEnv()`
  - SIGTERM first, SIGKILL after 5s fallback (matching pi-superpowers-plus)
- Modify `internal/subagent/pool.go`:
  - `NewPool` reads `PI_SUBAGENT_CONCURRENCY` or uses provided size

**Test requirements:**
- Test orchestrator with `AgentConfig` spawn (not type string)
- Test pool size from env var
- Test inactivity timeout fires after 120s silence (use mock process)
- Test absolute timeout fires regardless of activity
- Test environment is filtered in spawned process
- Test worktree override logic
- Test existing orchestrator tests still pass (adapted for new API)

**Integration notes:**
- This is the critical refactor — changes the interface between tool and orchestrator
- All existing tests in `orchestrator_test.go`, `spawner_test.go`, `pool_test.go` need updating
- The old `AgentInput.Type` field is removed; callers must provide `AgentConfig`

**Demo:** Spawn a subagent using an `AgentConfig` parsed from markdown, observe filtered env and timeout behavior.

---

## Step 5: Subagent Tool with Single Mode

**Objective:** Create the `subagent` tool replacing the `agent` tool, implementing single mode first.

**Implementation guidance:**
- Create `internal/tools/subagent.go`:
  - Define `SubagentInput`, `SubagentOutput`, `AgentResult`, `SubagentEvent` types
  - Define `SubagentEventCallback` type
  - Implement `NewSubagentTool(orch, onEvent)` — creates ADK FunctionTool named "subagent"
  - Implement `subagentHandler()`:
    - Detect mode from input (single if `agent` + `task` provided)
    - Call `DiscoverAgents(cwd, scope)` to get available agents
    - Validate agent name exists
    - For single mode: spawn agent, stream events via callback, accumulate result, return `SubagentOutput`
    - Emit `SubagentEvent` with `PipelineID`, `Mode: "single"`, `Step: 1`, `Total: 1`
  - Implement `SubagentTools(orch, onEvent)` factory function
- Delete `internal/tools/agent.go`
- Update tool description to document all three modes and list available agents dynamically

**Test requirements:**
- Test tool creation and registration (name is "subagent")
- Test single mode: valid agent → spawn succeeds
- Test unknown agent → error with available agents list
- Test mode detection: single vs no-mode error
- Test event callback receives SubagentEvent with pipeline metadata

**Integration notes:**
- Wire into CLI in place of old `AgentTools()` — defer to Step 9 for full wiring
- Old `agent` tool references in system instructions need updating
- The tool needs access to cwd for agent discovery

**Demo:** Call subagent tool with `{agent: "explore", task: "find main.go"}`, see events stream and result return.

---

## Step 6: Parallel Execution Mode

**Objective:** Add parallel mode to the subagent tool — concurrent agent dispatch with aggregated results.

**Implementation guidance:**
- In `subagentHandler()`, add parallel mode detection: `input.Tasks != nil && len > 0`
- Validate: max 8 tasks, all agent names valid
- Use `errgroup.Group` with context for concurrent execution:
  ```go
  g, ctx := errgroup.WithContext(ctx)
  results := make([]AgentResult, len(input.Tasks))
  for i, task := range input.Tasks {
      g.Go(func() error {
          events, id, err := orch.Spawn(ctx, SpawnInput{...})
          // consume events, forward to callback with Step=i+1
          results[i] = collectResult(events)
          return nil
      })
  }
  g.Wait()
  ```
- Each parallel agent gets `SubagentEvent` with `Mode: "parallel"`, `Step: index+1`, `Total: len(tasks)`
- Generate shared `PipelineID` for all agents in the batch
- Aggregate results: count successes/failures, build summary string
- All agents run to completion regardless of individual failures

**Test requirements:**
- Test 3 parallel tasks execute and return aggregated results
- Test one failing agent doesn't cancel others
- Test max 8 limit enforced
- Test events have correct pipeline metadata (same PipelineID, different Steps)
- Test pool limits respected (6 concurrent max)
- Test empty tasks array returns error

**Integration notes:**
- Depends on Step 5 for tool infrastructure
- TUI rendering for parallel handled in Step 8

**Demo:** Call `subagent({tasks: [{agent: "explore", task: "..."}, {agent: "explore", task: "..."}]})`, see concurrent execution and aggregated result.

---

## Step 7: Chain Execution Mode

**Objective:** Add chain mode — sequential execution with `{previous}` and `{previous_json}` output piping.

**Implementation guidance:**
- In `subagentHandler()`, add chain mode detection: `input.Chain != nil && len > 0`
- Validate all agent names before starting any execution
- Execute sequentially:
  ```go
  var previousText, previousJSON string
  for i, step := range input.Chain {
      task := step.Task
      task = strings.ReplaceAll(task, "{previous}", previousText)
      task = strings.ReplaceAll(task, "{previous_json}", previousJSON)
      events, id, err := orch.Spawn(ctx, SpawnInput{...})
      result := collectResult(events)
      previousText = result.Result
      // Try to parse as JSON for {previous_json}
      if json.Valid([]byte(previousText)) {
          previousJSON = previousText
      } else {
          previousJSON = fmt.Sprintf("%q", previousText) // escape as JSON string
      }
      if result.Status == "failed" {
          // Stop chain, return completed + failed steps
          break
      }
  }
  ```
- Each chain step gets `SubagentEvent` with `Mode: "chain"`, `Step: i+1`, `Total: len(chain)`
- Shared `PipelineID` for all steps
- Stop on first failure, don't execute remaining steps

**Test requirements:**
- Test 3-step chain executes sequentially
- Test `{previous}` replacement with prior output
- Test `{previous_json}` replacement with JSON output
- Test `{previous_json}` with non-JSON prior output (escaped string)
- Test chain stops on first failure, remaining steps not executed
- Test events have sequential step numbers
- Test empty chain returns error

**Integration notes:**
- Depends on Steps 5-6 for tool infrastructure
- Chain and parallel share the same `SubagentOutput` format

**Demo:** Call `subagent({chain: [{agent: "explore", task: "find tests"}, {agent: "worker", task: "analyze: {previous}"}]})`, see sequential execution with output piping.

---

## Step 8: TUI Pipeline Visualization

**Objective:** Render box-drawing state machine visualization for parallel and chain executions.

**Implementation guidance:**
- Create `internal/tui/pipeline.go`:
  - Define `PipelineView`, `AgentBlock`, `AgentState` types
  - Implement `RenderPipeline(view, width)` dispatcher
  - Implement `renderPipelineBoxes()` for width ≥ 100:
    - Outer container box with mode label
    - Inner agent boxes side-by-side (parallel) or with `────▶` arrows (chain)
    - State icon + color per agent: ○ pending (gray), ▶ running (orange), ✓ done (green), ✗ failed (red)
    - Current tool name and elapsed time in each box
    - Progress line at bottom
  - Implement `renderPipelineStacked()` for width ≥ 60:
    - Same info but one box per line
    - Arrows between boxes for chain mode
  - Implement `renderPipelineCompact()` for width < 60:
    - Inline: `[✓ impl] [▶ spec-rev] [○ code-rev]  chain 2/3`
  - Use lipgloss styling for colors matching existing TUI palette
- Modify `internal/tui/tui.go`:
  - Add `pipelines map[string]*PipelineView` to model
  - Route `SubagentEvent` to pipeline view:
    - On "spawn": create/update AgentBlock, set Running
    - On "tool_call": update CurrentTool
    - On "done": set Done/Failed, clear CurrentTool
    - On "error": set Failed
  - In message rendering: if message is a parallel/chain subagent result, render pipeline view instead of standard event stream
  - Single mode: keep current rendering (no pipeline)

**Test requirements:**
- Test box rendering at widths 120, 80, 50
- Test parallel layout: 1, 3, 8 agents
- Test chain layout with arrows
- Test state transitions: pending → running → done
- Test failed state rendering
- Test compact mode output
- Test pipeline event routing in TUI model
- Test long agent names truncation

**Integration notes:**
- Depends on Steps 5-7 for SubagentEvent with pipeline metadata
- Pipeline view updates in-place during execution (TUI re-renders on each event)

**Demo:** Run a parallel subagent call in the TUI, see live-updating box visualization with state colors.

---

## Step 9: Slash Commands and CLI Wiring

**Objective:** Wire everything together — CLI creates subagent tool, TUI handles new commands.

**Implementation guidance:**
- Modify `internal/cli/cli.go`:
  - Replace `tools.AgentTools(orch, agentEventCB)` with `tools.SubagentTools(orch, subagentEventCB)`
  - Create orchestrator with env-based pool size: `subagent.NewOrchestrator(cfg, repoRoot, subagent.WithPoolSize(poolSize))`
  - Update `SubagentEventCallback` to forward `SubagentEvent` to TUI channel
  - Update agent event channel type from `AgentSubEvent` to `SubagentEvent`
- Modify `internal/tui/tui.go`:
  - `/agents` command: call `DiscoverAgents(cwd, "both")`, render table of name, description, source, tools, role
  - `/agents status` command: call `orch.List()`, render running agents with ID, type, elapsed, status
  - Update help text to document new commands and remove old `agent` references
  - Update system instruction to describe `subagent` tool instead of `agent`
- Modify `internal/agent/agent.go`:
  - Update system instruction: replace `agent` tool docs with `subagent` tool docs
  - Document single/parallel/chain modes and available agent names

**Test requirements:**
- Test CLI creates subagent tool (not agent tool)
- Test `/agents` lists all discovered agents
- Test `/agents status` shows running agents
- Test system instruction contains subagent documentation
- Test event channel carries SubagentEvent type

**Integration notes:**
- This is the final wiring step — everything connects here
- Must update any system prompt text that references the old `agent` tool

**Demo:** Full end-to-end: start pi-go, type `/agents` to see definitions, run a parallel subagent call, see pipeline visualization, type `/agents status` to see running agents.

---

## Step 10: Integration Testing and Polish

**Objective:** End-to-end testing, edge case handling, documentation.

**Implementation guidance:**
- Run full test suite, fix any regressions from the refactor
- Add integration test: spawn real subagent process, verify event stream and result
- Test edge cases:
  - Agent with no tools field (all tools available)
  - Agent with empty system prompt
  - Chain with single step (degenerates to single)
  - Parallel with single task (degenerates to single)
  - Very long agent names in pipeline boxes
  - Concurrent parallel + chain calls
  - Worktree conflicts in parallel mode (multiple agents with worktree=true)
- Verify all 25 acceptance criteria from design.md
- Update any existing tests that referenced old `agent` tool
- Clean up dead code: remove old `AgentTypes` map references, `ValidateType()`, `agent.go`

**Test requirements:**
- All existing tests pass (adapted for new API)
- All 25 acceptance criteria verified
- No regressions in TUI rendering
- Edge cases covered

**Integration notes:**
- Final step — everything should be working
- May need to iterate on pipeline visualization based on actual terminal rendering

**Demo:** Full demonstration of all features: agent discovery, single/parallel/chain modes, pipeline visualization, slash commands, timeout behavior, env filtering.
