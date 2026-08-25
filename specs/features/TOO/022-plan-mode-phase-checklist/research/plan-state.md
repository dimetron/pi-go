# Research: Plan-mode state and spec existence checks

## Plan-session fields on the model

The `model` struct is defined in `internal/tui/tui.go:25`. Plan-related fields at `tui.go:55-61`:
```go
planWorktreeAgentID string
planWorktreePath    string
planBackupBranch    string
planTaskName        string
planWorktree        *subagent.WorktreeManager
```
The `mode` field is at `tui.go:65`.

Set in two places:
- **`startPlanWorktree`** (`plan.go:201-218`) sets all five plan fields at lines 212-216.
- **`startPlanSession`** (`plan.go:383-473`) sets `m.mode = "plan"` (line 470) and `m.running = true` (line 471).

Cleared on completion:
- **`finishPlanWorktree`** (`plan.go:228-282`) sets `m.planWorktree = nil` (line 277) and `m.planWorktreePath = ""` (line 280).

## Spec directory derivation

The spec directory is `filepath.Join(m.planWorktreePath, "specs", m.planTaskName)`.
- `m.planWorktreePath` is the worktree root; `m.planTaskName` is the task name (e.g. `features/TOO/022-...`).
- This is the same path used in `finishPlanWorktree` (`plan.go:246`): `promptPath := filepath.Join(m.planWorktreePath, "specs", m.planTaskName, "PROMPT.md")`.

## Existing file/dir existence checks

No single dedicated helper. Inline `os.Stat` calls:
- `createSpecSkeleton` (`plan.go:142`) — `os.Stat(specDir)` to detect existing spec dir.
- `finishPlanWorktree` (`plan.go:246-249`) — `os.Stat(promptPath)` to detect PROMPT.md.
- `listAvailableSpecs` (`run.go:1803-1834`) — `os.Stat(specsDir)` and per-spec `os.Stat(promptPath)`.
- `findExistingSpec` / `nextSpecNumber` — use `os.ReadDir`, not `os.Stat`.

## Sidebar section rendering contract

- Each section renderer returns `nil` when its data is absent (e.g. `sidebarGitLines` returns nil when `GitBranch == ""`, `sidebar.go:188`; `sidebarMCPLines` returns nil when `len(in.MCPTools) == 0`, `sidebar.go:387`).
- `RenderSidebar` (`sidebar.go:118-131`) appends each section's lines; a nil section contributes nothing.
- This is the mechanism the presence/absence tests rely on.
