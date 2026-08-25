# Outline: Plan Always Merge Worktree

## Overview

Make `/plan` always merge the planning worktree into the current CWD branch once
`PROMPT.md` exists, even if the agent errored on the final turn. The fix is a
one-line change to the guard in `handleAgentDone`; `finishPlanWorktree` already
gates the merge on `PROMPT.md` existing. All changes are confined to
`internal/tui/`.

## Phases / Slices

1. **Remove the `msg.err == nil` guard** — in `handleAgentDone` (`agent_loop.go`),
   change the guard so `finishPlanWorktree` runs whenever in plan mode with an
   active worktree, regardless of `msg.err`. Verify: package compiles.
2. **Add tests** — 4 tests covering merge-on-error, keep-on-incomplete,
   no-error-still-merges, and not-plan-mode. Verify: new tests pass.

## Order of Changes & Testing

Slices 1 → 2, strictly sequential. Slice 1 compiles and passes existing tests;
Slice 2 depends on Slice 1.

## Key Type Signatures

No new types or exported functions. The only change is the guard condition in
`handleAgentDone`:

```go
// Before:
if msg.err == nil && m.mode == "plan" && m.planWorktree != nil {
// After:
if m.mode == "plan" && m.planWorktree != nil {
```

## Parallel-Safety

Slice 1 touches `agent_loop.go`; Slice 2 touches test files. They share no files,
but Slice 2 depends on Slice 1's behavior, so run strictly in order.
