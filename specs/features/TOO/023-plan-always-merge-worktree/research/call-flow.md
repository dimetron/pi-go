# Research: /plan worktree finalization call flow

## `handleAgentDone` — gating and calling `finishPlanWorktree`

**File:** `internal/tui/agent_loop.go:1620-1661`

```go
func (m *model) handleAgentDone(msg agentDoneMsg) (tea.Model, tea.Cmd) {
    m.running = false
    m.agentCancel = nil
    m.matrix.clear()
    m.statusModel.ActiveTool = ""
    m.statusModel.ActiveTools = nil
    if msg.err == nil && m.mode == "plan" && m.planWorktree != nil {
        if err := m.finishPlanWorktree(); err != nil {
            msg.err = fmt.Errorf("finalize PDD worktree: %w", err)
        }
    }
    if msg.err != nil {
        // MoodSad, AppendError, TraceLog
    } else {
        // MoodHappy
    }
    ...
}
```

**Gating (line 1626):** `finishPlanWorktree` is called only when all three hold:
- `msg.err == nil` (agent turn completed without error), AND
- `m.mode == "plan"`, AND
- `m.planWorktree != nil`.

**Error handling (lines 1627-1629):** a `finishPlanWorktree` error is wrapped as
`"finalize PDD worktree: %w"` and written back into the local `msg.err`, which then
drives the error UI path (MoodSad, AppendError, TraceLog).

**Note:** `msg` is passed **by value**, so the `msg.err` mutation is local to
`handleAgentDone`.

## `finishPlanWorktree` — full logic

**File:** `internal/tui/plan.go:228-282`

1. **Early return (229-231):** if `m.planWorktree == nil`, return nil (no-op).
2. **Commit (237):** `CommitAll(agentID, "PDD plan: <taskName>")`. On error, return.
3. **Backup branch (240):** `CreateBackupBranch(agentID, m.planBackupBranch)` where
   `m.planBackupBranch = "specs/" + taskName`. On error, return.
4. **PROMPT.md gate (246-249):** `promptPath = <worktreePath>/specs/<taskName>/PROMPT.md`.
   If `os.Stat` fails (file missing), return nil **early** — keeps worktree alive
   for next turn. This is the "not finished yet" path.
5. **Merge (253):** `MergeBack(agentID)` merges the worktree branch into the
   invoking branch. On error: copies the spec dir from worktree into
   `<workDir>/specs/<taskName>` with `overwrite=true` (merge-failure fallback,
   lines 257-258), then returns the merge error.
6. **Post-merge copy (269-272):** after a successful merge, copies the spec dir
   again with `overwrite=false` (belt-and-suspenders so `/run` finds PROMPT.md on
   disk). On error, return wrapped error.
7. **Cleanup (274):** `Cleanup(agentID)` removes the temporary worktree/branch. On
   error, return.
8. **State reset (277-280):** on success sets `m.planWorktree = nil` and
   `m.planWorktreePath = ""`.

**Key nuance:** runs at the end of every planning turn. Commit + backup branch act
as an incremental snapshot; the worktree is only torn down once PROMPT.md exists.

## Other call sites

- **Production:** only one — `handleAgentDone` at `agent_loop.go:1627`.
- **Tests:** `plan_test.go` calls it directly at lines 366, 395, 432, 561.

## `agentDoneMsg` type

**File:** `internal/tui/agent_loop.go:406` — `type agentDoneMsg struct{ err error }`.
- Implements `agentMsg()` marker interface (line 440).
- **Dispatch:** `tui.go:831-833` — `case agentDoneMsg:` calls `m.handleAgentDone(msg)`.
- **Producers:** `agent_loop.go:537` (channel close), `:777` (panic), `:783` (nil
  agent), `:820` (run error).
