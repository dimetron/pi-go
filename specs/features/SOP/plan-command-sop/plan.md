# Implementation Plan: /plan and /run Commands with PDD SOP Workflow

## Checklist

- [ ] Step 1: PDD SOP Loader Package
- [ ] Step 2: /plan Command — Skeleton Creation & Kebab-case
- [ ] Step 3: /plan Command — System Instruction Injection & Agent Kickoff
- [ ] Step 4: /run Command — PROMPT.md Reader & Gate Parser
- [ ] Step 5: /run Command — Subagent Spawn & Streaming
- [ ] Step 6: /run Command — Gate Validation & Auto-merge
- [ ] Step 7: /run Command — Retry Logic on Gate Failure
- [ ] Step 8: TUI Registration & Autocomplete
- [ ] Step 9: Integration & E2E Testing

---

## Step 1: PDD SOP Loader Package

**Objective:** Create the `internal/sop/` package that loads the PDD SOP instruction with embedded default and file override support.

**Implementation guidance:**
- Create `internal/sop/pdd_default.go`:
  - Embed the full PDD SOP text as a Go constant `DefaultPDDSOP`
  - The SOP should include all PDD phases (skeleton, requirements, research, design, plan, PROMPT.md generation)
  - Include the PROMPT.md template with the `## Gates` section format
  - Include instructions to discover project build/test commands during planning
- Create `internal/sop/sop.go`:
  - `LoadPDD(workDir string) (string, error)` function
  - Resolution order: `{workDir}/.pi-go/sops/pdd.md` → `~/.pi-go/sops/pdd.md` → `DefaultPDDSOP`
  - Use `os.ReadFile` for overrides, return embedded default as fallback
  - Log which source was used (project override, global override, or embedded)

**Test requirements:**
- `TestLoadPDD_EmbeddedDefault` — no override files exist, returns embedded constant
- `TestLoadPDD_ProjectOverride` — project-level file takes precedence over global and embedded
- `TestLoadPDD_GlobalOverride` — global file used when no project file exists
- `TestLoadPDD_ProjectOverGlobal` — project file wins over global file
- `TestLoadPDD_UnreadableFile` — falls back to next source on read error

**Integration notes:** No dependencies on other new code. Foundation for Step 3.

**Demo:** Unit tests pass. `LoadPDD("/some/project")` returns the embedded SOP when no overrides exist.

---

## Step 2: /plan Command — Skeleton Creation & Kebab-case

**Objective:** Implement the `/plan` command's initial phase: parse input, derive task name, create spec directory skeleton.

**Implementation guidance:**
- Create `internal/tui/plan.go`:
  - `toKebabCase(idea string) string`:
    - Lowercase, replace spaces and special chars with hyphens
    - Collapse consecutive hyphens, trim leading/trailing hyphens
    - Truncate to reasonable length (e.g., 50 chars)
  - `createSpecSkeleton(workDir, taskName, roughIdea string) (string, error)`:
    - Target: `{workDir}/specs/{taskName}/`
    - Check directory doesn't already exist (return error if it does)
    - Create directory structure: `specs/{taskName}/`, `specs/{taskName}/research/`
    - Write `rough-idea.md` with the rough idea text
    - Write empty `requirements.md` with Q&A header
    - Return the spec directory path
  - `handlePlanCommand(parts []string) (tea.Model, tea.Cmd)`:
    - Extract rough idea from parts (everything after "/plan")
    - Validate non-empty input
    - Call `toKebabCase` and `createSpecSkeleton`
    - Show confirmation message in TUI with created path
    - (Agent kickoff deferred to Step 3)

**Test requirements:**
- `TestToKebabCase_Simple` — "add rate limiting" → "add-rate-limiting"
- `TestToKebabCase_SpecialChars` — "build a REST API!" → "build-a-rest-api"
- `TestToKebabCase_MixedCase` — "Add JWT Auth" → "add-jwt-auth"
- `TestToKebabCase_ExtraSpaces` — "  too   many  spaces  " → "too-many-spaces"
- `TestToKebabCase_Truncation` — long string truncated to 50 chars at word boundary
- `TestCreateSpecSkeleton_Success` — creates all expected files and directories
- `TestCreateSpecSkeleton_AlreadyExists` — returns error when directory exists
- `TestCreateSpecSkeleton_RoughIdeaContent` — rough-idea.md contains the input text

**Integration notes:** No dependency on SOP loader yet. Pure file operations.

**Demo:** `/plan add rate limiting to API` creates `specs/add-rate-limiting-to-api/` with rough-idea.md and requirements.md.

---

## Step 3: /plan Command — System Instruction Injection & Agent Kickoff

**Objective:** Wire up the PDD SOP as a system instruction and kick off the interactive agent conversation.

**Implementation guidance:**
- Update `handlePlanCommand` in `internal/tui/plan.go`:
  - After skeleton creation, call `sop.LoadPDD(workDir)` to get the SOP text
  - Construct augmented system instruction:
    ```
    {PDD SOP text}

    ## Current Task
    - Task name: {taskName}
    - Spec directory: specs/{taskName}/
    - Rough idea: {roughIdea}

    ## Instructions
    The spec skeleton has been created. Begin the PDD process starting with Step 2 (Initial Process Planning).
    Artifacts should be written to specs/{taskName}/ using the write and edit tools.
    IMPORTANT: You must NEVER modify any source code. Only read code for research. All file writes must go to specs/* only.
    ```
  - Clear the current conversation (like `/clear`)
  - Set the augmented system instruction on the agent
  - Send the rough idea as the first user message
  - Return to the normal agent loop (the LLM will follow the SOP from here)
- Need to expose a method on `agent.Agent` or `Config` to update the system instruction mid-session. Check existing API — may need to add `Agent.SetSystemInstruction(string)` or reconstruct the agent.

**Test requirements:**
- `TestHandlePlanCommand_ValidInput` — skeleton created, agent receives SOP instruction
- `TestHandlePlanCommand_NoInput` — shows usage error
- `TestHandlePlanCommand_SOPOverride` — custom SOP file used when present
- `TestHandlePlanCommand_ExistingSpec` — shows error, doesn't clear conversation

**Integration notes:** Depends on Steps 1-2. Requires understanding how the agent's system instruction can be updated. May need a small addition to `internal/agent/` or `internal/cli/` to support instruction override.

**Demo:** `/plan build a rate limiter` creates skeleton, clears chat, and the LLM begins the PDD flow by asking "How would you like to start: requirements clarification, research, or provide additional context?"

---

## Step 4: /run Command — PROMPT.md Reader & Gate Parser

**Objective:** Implement PROMPT.md reading and `## Gates` section parsing for the `/run` command.

**Implementation guidance:**
- Create `internal/tui/run.go`:
  - `Gate` struct: `Name string`, `Command string`
  - `parseGates(promptMD string) []Gate`:
    - Find the `## Gates` section in markdown content
    - Parse lines matching pattern: `- **name**: \`command\`` or `- name: \`command\``
    - Stop at next `##` heading or end of file
    - Return empty slice if no Gates section found
  - `readPromptMD(workDir, specName string) (string, error)`:
    - Read `{workDir}/specs/{specName}/PROMPT.md`
    - Return content or error with helpful message
  - `listAvailableSpecs(workDir string) ([]string, error)`:
    - Scan `{workDir}/specs/` for directories containing `PROMPT.md`
    - Return sorted list of spec names
  - `runState` struct (as defined in design)

**Test requirements:**
- `TestParseGates_Standard` — parses `- **build**: \`go build ./...\`` format
- `TestParseGates_Multiple` — parses multiple gate entries
- `TestParseGates_NoSection` — returns empty slice
- `TestParseGates_Malformed` — skips unparseable lines gracefully
- `TestParseGates_StopsAtNextHeading` — doesn't bleed into next section
- `TestReadPromptMD_Success` — reads existing file
- `TestReadPromptMD_NotFound` — helpful error message
- `TestListAvailableSpecs` — finds specs with PROMPT.md

**Integration notes:** No subagent dependency. Pure parsing and file I/O.

**Demo:** Unit tests pass. `parseGates()` correctly extracts gate commands from sample PROMPT.md content.

---

## Step 5: /run Command — Subagent Spawn & Streaming

**Objective:** Wire `/run` to spawn a task subagent and stream events to the TUI.

**Implementation guidance:**
- Add to `internal/tui/run.go`:
  - `handleRunCommand(parts []string) (tea.Model, tea.Cmd)`:
    - Validate input: requires spec-name argument
    - Read PROMPT.md, parse gates
    - Initialize `runState` on the TUI model
    - Construct augmented prompt for the agent:
      ```
      {PROMPT.md content}

      ## Execution Instructions
      - Follow the plan in specs/{specName}/plan.md step by step
      - After completing each step, update the plan.md checklist: change `- [ ] Step N:` to `- [x] Step N:`
      - Run tests after each step to verify correctness
      - Work in the current directory (worktree)
      ```
    - Call `Orchestrator.Spawn()` with type="task" and the augmented prompt
    - Store agentID in runState
    - Return tea.Cmd that starts consuming the events channel
  - New message types for /run streaming:
    - `runAgentEventMsg` — wraps subagent events for TUI dispatch
    - `runAgentDoneMsg` — signals agent completion
  - Update `Update()` in tui.go to handle run message types:
    - `runAgentEventMsg`: display text_delta, tool_call, tool_result in chat (similar to normal agent messages)
    - `runAgentDoneMsg`: transition to gate validation phase

**Test requirements:**
- `TestHandleRunCommand_ValidSpec` — orchestrator.Spawn called with correct type and prompt
- `TestHandleRunCommand_MissingSpec` — error with list of available specs
- `TestHandleRunCommand_NoArgs` — shows usage message
- `TestHandleRunCommand_StreamingEvents` — events forwarded to TUI messages

**Integration notes:** Depends on Step 4 (parsing) and existing Orchestrator. The subagent instruction already includes implementation guidance from the agent type definition — the PROMPT.md content and execution instructions are passed as the prompt.

**Demo:** `/run plan-command-sop` spawns a task agent. TUI shows streaming output as the agent works.

---

## Step 6: /run Command — Gate Validation & Auto-merge

**Objective:** After the subagent completes, run gate commands and auto-merge on success.

**Implementation guidance:**
- Add to `internal/tui/run.go`:
  - `runGatesCmd(ctx context.Context, worktreePath string, gates []Gate) tea.Cmd`:
    - Returns a tea.Cmd that runs each gate command sequentially in the worktree
    - Uses `exec.Command` with `Dir` set to worktree path
    - Captures stdout+stderr for each gate
    - Returns `runGateResultMsg` with pass/fail and output
  - `mergeWorktreeCmd(ctx context.Context, orch *subagent.Orchestrator, agentID string) tea.Cmd`:
    - Calls `orch.Worktree().MergeBack(agentID)` (or equivalent)
    - Returns `runMergeResultMsg` with output or error
  - Handle `runGateResultMsg` in `Update()`:
    - If passed: show gate results, trigger merge
    - If failed: trigger retry logic (Step 7)
  - Handle `runMergeResultMsg` in `Update()`:
    - If success: show merge confirmation, clean up worktree, set phase="done"
    - If conflict: show conflict details, leave worktree, set phase="failed"
  - If no gates defined (empty gates list): skip directly to merge

**Test requirements:**
- `TestRunGates_AllPass` — both build and test gates pass, returns passed=true
- `TestRunGates_BuildFails` — build gate fails, stops early, returns output
- `TestRunGates_TestFails` — build passes, test fails, returns output
- `TestRunGates_NoGates` — empty gates, returns passed=true
- `TestMergeWorktree_Success` — merge completes, worktree cleaned up
- `TestMergeWorktree_Conflict` — merge conflict reported

**Integration notes:** Depends on Step 5. Uses existing WorktreeManager.MergeBack(). Gate commands run as real shell commands in the worktree directory.

**Demo:** After agent completes, gates run automatically. On success, changes appear in main branch.

---

## Step 7: /run Command — Retry Logic on Gate Failure

**Objective:** When gates fail, re-spawn the agent with failure context to fix the issues. Max 3 retries.

**Implementation guidance:**
- Update gate failure handling in `Update()`:
  - When `runGateResultMsg` with `passed=false`:
    - If `runState.retries < runState.maxRetries`:
      - Increment `runState.retries`
      - Construct retry prompt:
        ```
        The previous implementation attempt failed gate validation.

        ## Gate Failures
        {gate output}

        ## Original Task
        {PROMPT.md content}

        ## Instructions
        Fix the issues identified by the gate failures. The failing commands were run in the worktree.
        Continue working in the current directory. Run the failing commands yourself to verify fixes.
        Update plan.md checklist as you complete steps.
        ```
      - Re-spawn agent in the **same worktree** (don't create a new one)
      - Update runState.phase = "retrying"
    - If retries exhausted:
      - Show failure summary with gate output
      - Show worktree path for manual inspection
      - Set runState.phase = "failed"
- Need Orchestrator support for spawning into an existing worktree path (currently creates new ones). May need to add `SpawnInWorktree(ctx, input, worktreePath)` or accept a workdir override in `AgentInput`.

**Test requirements:**
- `TestRetryOnGateFailure_FirstRetry` — retry count increments, new agent spawned with failure context
- `TestRetryOnGateFailure_SecondRetry` — retry prompt includes latest failure
- `TestRetryOnGateFailure_MaxRetries` — after 3 failures, phase set to "failed", worktree preserved
- `TestRetryPrompt_IncludesGateOutput` — failure output included in retry prompt

**Integration notes:** Depends on Steps 5-6. May need a small addition to Orchestrator/Spawner to support spawning into an existing worktree directory rather than creating a new one. Check if `AgentInput` already supports a `Worktree` path override (research shows it has a `Worktree` field).

**Demo:** Agent completes but `go test` fails. Agent is re-spawned with failure context, fixes the issue, gates pass on retry, merge succeeds.

---

## Step 8: TUI Registration & Autocomplete

**Objective:** Register `/plan` and `/run` in the TUI slash command system, including autocomplete and help text.

**Implementation guidance:**
- Update `internal/tui/tui.go`:
  - Add `/plan` and `/run` to `slashCommands` array (line ~1051)
  - Add cases in `handleSlashCommand()` switch block:
    ```go
    case "/plan":
        return m.handlePlanCommand(parts[1:])
    case "/run":
        return m.handleRunCommand(parts[1:])
    ```
  - Update `/help` output to include:
    - `- /plan <idea> — Start a PDD planning session to design and spec a feature`
    - `- /run <spec-name> — Execute a spec's PROMPT.md using an isolated task agent`
- Update `handleRunCommand` to support tab-completion of spec names:
  - When user types `/run ` and hits Tab, autocomplete with available spec directory names
  - Optional: implement as a second-level completion (existing system only completes command names)

**Test requirements:**
- `TestSlashCommands_PlanRegistered` — "/plan" in slashCommands list
- `TestSlashCommands_RunRegistered` — "/run" in slashCommands list
- `TestHelpText_IncludesPlanAndRun` — help output mentions both commands

**Integration notes:** Depends on Steps 2-3 (plan handler) and Steps 4-7 (run handler). This is the wiring step.

**Demo:** Type `/p` → Tab completes to `/plan`. Type `/r` → Tab completes to `/run`. `/help` shows both commands. Both commands dispatch correctly.

---

## Step 9: Integration & E2E Testing

**Objective:** Verify the full `/plan → /run` pipeline works end-to-end. Polish edge cases.

**Implementation guidance:**
- Create `internal/tui/plan_run_e2e_test.go`:
  - **E2E: /plan creates spec skeleton** — invoke /plan, verify directory structure
  - **E2E: /run reads PROMPT.md and spawns agent** — create test PROMPT.md, invoke /run, verify agent spawned
  - **E2E: Gate parsing roundtrip** — /plan generates PROMPT.md with Gates section, /run parses it correctly
  - **E2E: Gate pass → merge** — mock agent completion, gates pass, verify merge called
  - **E2E: Gate fail → retry** — mock gate failure, verify re-spawn with context
- Verify existing tests still pass: `go test ./...`
- Verify build: `go build ./...`
- Edge case review:
  - `/plan` with very long idea text
  - `/run` while another agent is running
  - `/run` with spec that has no plan.md (only PROMPT.md)
  - `/plan` interrupted mid-conversation (can be resumed with `/plan` again? or manual?)
  - Worktree cleanup on TUI exit during `/run`
- Update TUI shutdown to cancel any running /run agents and clean up worktrees

**Test requirements:**
- All E2E tests listed above
- Regression: `go test ./...` passes
- Build: `go build ./...` succeeds
- Vet: `go vet ./...` passes

**Integration notes:** This step ties everything together. All prior steps must be complete.

**Demo:** Full pipeline: user runs `/plan build a rate limiter`, goes through PDD flow, gets PROMPT.md. Then runs `/run build-a-rate-limiter`, watches agent implement, gates pass, changes merged.
