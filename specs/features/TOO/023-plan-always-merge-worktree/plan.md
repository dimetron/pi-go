# Plan: Plan Always Merge Worktree

## Context

Make `/plan` always merge the planning worktree into the current CWD branch once
`PROMPT.md` exists, even if the agent errored on the final turn. The fix is a
one-line change to the guard in `handleAgentDone`; `finishPlanWorktree` already
gates the merge on `PROMPT.md` existing. All changes are confined to
`internal/tui/`. See `design.md` for full detail.

## Slices

### Slice 1: Remove the `msg.err == nil` guard

**What to implement:**
- In `internal/tui/agent_loop.go`, in `handleAgentDone` (around line 1626), change
  the guard so `finishPlanWorktree` runs whenever in plan mode with an active
  worktree, regardless of `msg.err`:
  ```go
  // Before:
  if msg.err == nil && m.mode == "plan" && m.planWorktree != nil {
  // After:
  if m.mode == "plan" && m.planWorktree != nil {
  ```
- Do NOT change the body: the `finishPlanWorktree` error is still wrapped as
  `"finalize PDD worktree: %w"` and written into the local `msg.err`, which then
  drives the error UI path.
- Do NOT change `finishPlanWorktree` in `plan.go` — its `PROMPT.md` gate already
  ensures a partial plan doesn't merge.

**Verification checkpoint:** `go build ./internal/tui/...` compiles; existing
tests still pass (`go test ./internal/tui/...`).

**Dependencies:** none.

**Parallel-safe:** no (touches `agent_loop.go`).

### Slice 2: Add tests

**What to implement:**
- In `internal/tui/plan_test.go` (or a new `plan_merge_test.go`), add tests that
  drive `handleAgentDone` with a real plan worktree. Reuse the
  `initRunTestRepo(t)` + `subagent.NewOrchestrator(&config.Config{}, repo, nil)` +
  `orch.Worktree().Create(agentID, "pdd-"+taskName)` pattern from the existing
  `TestFinishPlanWorktree_*` tests.
  - `TestHandleAgentDone_PlanWorktree_MergesOnError` — worktree with `PROMPT.md`
    present, `m.mode = "plan"`, call `m.handleAgentDone(agentDoneMsg{err: errors.New("boom")})`,
    assert the spec is merged into the invoking checkout (files exist at
    `repo/specs/<task>/`) AND the error is surfaced (a message with `isError`
    true contains "boom").
  - `TestHandleAgentDone_PlanWorktree_KeepsOnIncomplete` — worktree with
    `PROMPT.md` absent, `m.mode = "plan"`, call `m.handleAgentDone(agentDoneMsg{err: errors.New("boom")})`,
    assert the worktree is retained (`m.planWorktree != nil`) AND the error is
    surfaced.
  - `TestHandleAgentDone_PlanWorktree_NoErrorStillMerges` — worktree with
    `PROMPT.md` present, `m.mode = "plan"`, call `m.handleAgentDone(agentDoneMsg{})`,
    assert the spec is merged (regression guard for existing behavior).
  - `TestHandleAgentDone_NotPlanMode_NoFinalize` — `m.mode = "chat"` with a
    worktree, call `m.handleAgentDone(agentDoneMsg{err: errors.New("boom")})`,
    assert `finishPlanWorktree` is not invoked (worktree retained, error surfaced).

**Verification checkpoint:** `go test ./internal/tui/...` passes; `go vet
./internal/tui/...` clean.

**Dependencies:** Slice 1.

**Parallel-safe:** no (touches test files).

## Final Verification

After all slices:
- `go build ./...`
- `go test ./internal/tui/...`
- `go vet ./internal/tui/...`
- `golangci-lint run ./internal/tui/...`
