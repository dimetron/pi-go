# Research: Existing tests for plan finalization and agent-done

## `finishPlanWorktree` tests in `internal/tui/plan_test.go`

Four tests exercise it (lines 329-575):

### `TestFinishPlanWorktree_CopiesSpecIntoInvokingCheckout` (329-389)
- Setup: `repo := initRunTestRepo(t)`; `orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)`; `t.Cleanup(orch.Shutdown)`. Creates a real worktree via `orch.Worktree().Create(agentID, "pdd-"+taskName)` with `agentID = "plan-"+taskName`, `taskName = "features/TOO/001-demo"`. Seeds `specs/<task>/` with `PROMPT.md`, `requirements.md`, `research/notes.md` — **PROMPT.md exists**.
- Model: `&model{cfg: Config{WorkDir: repo, Orchestrator: orch}, planWorktreeAgentID, planWorktreePath, planTaskName, planBackupBranch: "specs/"+taskName, planWorktree: orch.Worktree()}`.
- Assertions: after `m.finishPlanWorktree()`, each spec file exists at `repo/specs/<task>/`; `m.planWorktree` nil; `m.planWorktreePath` cleared.

### `TestFinishPlanWorktree_NoWorktreeIsNoOp` (393-398)
- Bare `&model{}`. Asserts `finishPlanWorktree()` returns nil.

### `TestFinishPlanWorktree_KeepsWorktreeUntilPromptExists` (403-443)
- Same repo/orch pattern, worktree created, but only `requirements.md` written — **PROMPT.md does NOT exist**.
- Asserts: returns nil; `m.planWorktree` NOT nil; no spec copied into invoking checkout.

### `TestFinishPlanWorktree_MergeFailureStillCopiesSpec` (523-575)
- Same repo/orch pattern, worktree created, `PROMPT.md` written — **PROMPT.md exists**. Pre-seeds an untracked `PROMPT.md` ("stale") in the invoking checkout so the merge aborts.
- Asserts: returns non-nil error; the worktree's copy overwrote the stale one.

**How PROMPT.md existence is simulated:** purely by writing (or not writing) the
file at `wtPath/specs/<task>/PROMPT.md` before calling `finishPlanWorktree`.

## `handleAgentDone` tests

There is **no dedicated test** for `handleAgentDone`'s plan-worktree branch. The
`finishPlanWorktree` call inside `handleAgentDone` (agent_loop.go:1626) is **not
covered** by any existing test — no test sets `m.mode == "plan"` with a worktree
when calling `handleAgentDone`/`Update(agentDoneMsg)`.

Existing `handleAgentDone` tests (all without a plan worktree):
- `agent_loop_error_e2e_test.go:89-109` — `TestHandleAgentDone_RendersErrorStyled`: `m.handleAgentDone(agentDoneMsg{err: context.DeadlineExceeded})`, asserts Cmd nil, 1 message, `isError` true, content contains error, `kindOf(&got) == blockError`.
- `agent_event_test.go:1454,1510,1561` — lifecycle-hook tests calling `m.handleAgentDone(agentDoneMsg{})`.
- `agent_event_test.go:1580-1613` — `TestAgentDoneMsg_ErrorDoesNotFireUserInputHook`: `m.handleAgentDone(agentDoneMsg{err: errors.New("request canceled")})`.
- `agent_event_test.go:1356,1379` — `TestAgentDoneMsg` / `TestAgentDoneMsg_WithError` via `m.Update(...)`.
- `tui_test.go:447-466`, `tui_update_test.go:11-37,39-70`, `coverage_boost_test.go:2347-2354` — `m.Update(agentDoneMsg{...})` tests.

## Test helpers for a real git worktree

- **`initRunTestRepo(t *testing.T) string`** — `internal/tui/run_backup_test.go:17-40`. Runs `git init`, sets user.email/name, disables gpgsign, sets `core.autocrlf=false`, makes an empty initial commit. Returns the repo dir path.
- **`subagent.NewOrchestrator(cfg *config.Config, repoRoot string, agentConfigs []AgentConfig) *Orchestrator`** — `internal/subagent/orchestrator.go:106`. Tests call `orch.Worktree().Create(agentID, branchName)` and `orch.Shutdown()` via `t.Cleanup`.
- **`newHandlerModel()`** — `internal/tui/agent_loop_handlers_test.go:12-16`. Builds a minimal `&model{width:100, height:30}` with a buffered `agentCh`.

## How `handleAgentDone` surfaces errors

`internal/tui/agent_loop.go:1631-1643`:
- `msg.err != nil` → `m.face.SetMood(MoodSad)`, `m.chatModel.AppendError("Error: ...")`, append `traceEntry{kind:"error"}` to `TraceLog`.
- `msg.err == nil` → `m.face.SetMood(MoodHappy)`.
- `AppendError` — `internal/tui/chat.go:300-307`: appends `message{role:"assistant", content:text, isError:true}`.
- `SetMood` — `internal/tui/face.go:118`; moods at `face.go:14-15`.
- `TraceLog` — `ChatModel.TraceLog []traceEntry` at `chat.go:275`; `traceEntry` at `chat.go:257-262`.
