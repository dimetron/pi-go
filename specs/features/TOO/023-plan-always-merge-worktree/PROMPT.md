# Plan Always Merge Worktree

## Objective
Fix a robustness gap in `/plan`: when the planning agent errors on the final turn
after producing `PROMPT.md`, the completed spec is never merged into the current
CWD branch. This change makes `handleAgentDone` call `finishPlanWorktree` whenever
in plan mode with an active worktree, regardless of whether the agent turn errored.
`finishPlanWorktree` already gates the merge on `PROMPT.md` existing, so partial
plans still won't merge. All changes are confined to `internal/tui/`.

## Key Requirements
1. **Always finalize in plan mode** — `finishPlanWorktree` runs whenever
   `m.mode == "plan" && m.planWorktree != nil`, even if `msg.err != nil`.
2. **Merge gated on PROMPT.md** — `finishPlanWorktree` (unchanged) still only
   merges when `PROMPT.md` exists; partial plans keep the worktree for the next turn.
3. **Surface errors** — the agent's error is still surfaced even when the merge
   succeeds; a finalize error is wrapped as `"finalize PDD worktree: %w"` and
   surfaced (as today).
4. **TUI-only** — no changes to `internal/subagent/` or any non-TUI package.

## Acceptance Criteria
### Merge behavior
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

## Implementation Slices
1. **Remove the `msg.err == nil` guard** — in `handleAgentDone` (`agent_loop.go`,
   around line 1626), change the guard from `if msg.err == nil && m.mode == "plan"
   && m.planWorktree != nil {` to `if m.mode == "plan" && m.planWorktree != nil {`.
   Do NOT change the body (finalize error still wrapped and written into the local
   `msg.err`). Do NOT change `finishPlanWorktree` in `plan.go`. Files:
   `internal/tui/agent_loop.go`. Verify: `go build ./internal/tui/...` and
   `go test ./internal/tui/...`. Parallel-safe: no.
2. **Add tests** — add 4 tests driving `handleAgentDone` with a real plan worktree,
   reusing the `initRunTestRepo(t)` + `subagent.NewOrchestrator(&config.Config{},
   repo, nil)` + `orch.Worktree().Create(agentID, "pdd-"+taskName)` pattern from
   `plan_test.go`:
   - `TestHandleAgentDone_PlanWorktree_MergesOnError` — `PROMPT.md` present,
     `m.mode = "plan"`, `handleAgentDone(agentDoneMsg{err: errors.New("boom")})`;
     assert spec merged into invoking checkout AND error surfaced (`isError` true,
     contains "boom").
   - `TestHandleAgentDone_PlanWorktree_KeepsOnIncomplete` — `PROMPT.md` absent,
     `m.mode = "plan"`, `handleAgentDone(agentDoneMsg{err: errors.New("boom")})`;
     assert `m.planWorktree != nil` (retained) AND error surfaced.
   - `TestHandleAgentDone_PlanWorktree_NoErrorStillMerges` — `PROMPT.md` present,
     `m.mode = "plan"`, `handleAgentDone(agentDoneMsg{})`; assert spec merged
     (regression guard).
   - `TestHandleAgentDone_NotPlanMode_NoFinalize` — `m.mode = "chat"` with a
     worktree, `handleAgentDone(agentDoneMsg{err: errors.New("boom")})`; assert
     worktree retained AND error surfaced.
   Files: `internal/tui/plan_test.go` (or a new `plan_merge_test.go`). Verify:
   `go test ./internal/tui/...` and `go vet ./internal/tui/...`. Parallel-safe: no.

## Execution Model
Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the
**Coordinator**; it delegates rather than implements.

- **Workers**: one `worker` subagent per slice. No slices are parallel-safe, so run
  them one at a time, in order.
- **Verifier**: after the last slice, a `code-reviewer` subagent checks the Done
  Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to
  10 cycles total.

## Done Criteria
The Verifier checks these against the diff, not against the checklist. Each must
be objectively checkable by reading code or running a command.
- [ ] `handleAgentDone` calls `finishPlanWorktree` when `m.mode == "plan" &&
      m.planWorktree != nil`, with no `msg.err == nil` guard — see
      `TestHandleAgentDone_PlanWorktree_MergesOnError`.
- [ ] `finishPlanWorktree` in `plan.go` is unchanged (still gates the merge on
      `PROMPT.md` existing).
- [ ] `TestHandleAgentDone_PlanWorktree_MergesOnError` asserts the spec is merged
      into the invoking checkout AND the agent error is surfaced.
- [ ] `TestHandleAgentDone_PlanWorktree_KeepsOnIncomplete` asserts the worktree is
      retained when `PROMPT.md` is absent AND the error is surfaced.
- [ ] `TestHandleAgentDone_PlanWorktree_NoErrorStillMerges` asserts the spec is
      merged on a successful turn (regression guard).
- [ ] `TestHandleAgentDone_NotPlanMode_NoFinalize` asserts no finalize when not in
      plan mode.
- [ ] `go test ./internal/tui/...` passes.
- [ ] No slice is left as a stub, TODO, or panic("not implemented").

## Gates
- **build**: `go build ./internal/tui/...`
- **test**: `go test ./internal/tui/...`
- **vet**: `go vet ./internal/tui/...`
- **lint**: `golangci-lint run ./internal/tui/...`

## Reference
- Design: `specs/features/TOO/023-plan-always-merge-worktree/design.md`
- Outline: `specs/features/TOO/023-plan-always-merge-worktree/outline.md`
- Plan: `specs/features/TOO/023-plan-always-merge-worktree/plan.md`
- Requirements: `specs/features/TOO/023-plan-always-merge-worktree/requirements.md`
- Research: `specs/features/TOO/023-plan-always-merge-worktree/research/`

## Constraints
- All error handling and code changes are confined to the TUI layer
  (`internal/tui/`); no changes to `internal/subagent/` or other packages.
- `finishPlanWorktree` must still run only in plan mode with an active worktree.
- The merge must still be gated on `PROMPT.md` existing (a partial plan must not
  merge).
- The agent's error must still be surfaced to the user even when the merge succeeds.
- If `finishPlanWorktree` itself errors, that error must be surfaced (as today).
