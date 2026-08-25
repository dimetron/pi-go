# Requirements

## Purpose

Fix a robustness gap in `/plan`: when the planning agent errors on the final turn
after producing `PROMPT.md`, the completed spec is never merged into the current
CWD branch. The spec survives only on the backup branch, not in the user's working
tree. This spec makes `/plan` always merge the worktree into the current branch
once the plan is complete (`PROMPT.md` exists), regardless of whether the final
agent turn errored.

## Questions & Answers

### Q1: What's the desired behavior when /plan completes?
**A:** Always merge the worktree into the current CWD branch once `PROMPT.md`
exists — even if the agent errored on a later turn. The spec is complete, so it
should land in the working tree.

### Q2: What happens to the agent's error message when PROMPT.md exists but the agent errored?
**A:** Keep the error — merge the spec, but still surface the agent error to the
user. The plan landed, but the session had a problem.

## Scope

- **In scope:** `internal/tui/agent_loop.go` and `internal/tui/plan.go` (and tests).
  Change `handleAgentDone` so `finishPlanWorktree` runs even when `msg.err != nil`
  (in plan mode with an active worktree). `finishPlanWorktree` already gates the
  merge on `PROMPT.md` existing, so a partial/abandoned plan still won't merge.
- **Out of scope:** No change to `finishPlanWorktree`'s merge logic itself. No
  change to the `.pi-go/tasks/` vs `.worktrees/` location question. No change to
  `/run`. **All error handling stays in the TUI layer only** — no changes to
  `internal/subagent/worktree.go` or any non-TUI package.

## Constraints

- `finishPlanWorktree` must still run only in plan mode with an active worktree.
- The merge must still be gated on `PROMPT.md` existing (a partial plan must not
  merge).
- The agent's error must still be surfaced to the user even when the merge succeeds.
- If `finishPlanWorktree` itself errors, that error must be surfaced (as today).
- All error handling and any code changes are confined to the TUI layer
  (`internal/tui/`); no changes to `internal/subagent/` or other packages.

## Acceptance Criteria

- Given a completed plan (`PROMPT.md` exists) and a successful agent turn, when
  the turn ends, then the worktree is merged into the current branch (as today).
- Given a completed plan (`PROMPT.md` exists) and an agent turn that errored, when
  the turn ends, then the worktree is STILL merged into the current branch, AND the
  agent error is surfaced to the user.
- Given an incomplete plan (`PROMPT.md` missing) and an agent turn that errored,
  when the turn ends, then the worktree is NOT merged (kept for the next turn), AND
  the agent error is surfaced.
- Given `finishPlanWorktree` errors during the merge, when the turn ends, then the
  merge error is surfaced to the user.
