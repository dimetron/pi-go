# Code Review - 010-codex-direct-mode-app-server-subagent

## Summary of Findings

The following issues were identified during automated code review for the `codex-direct-mode-app-server-subagent` feature. All items are prioritized as **P2 (Priority 2 - High)**.

### 1. Filter Codex items to the active thread
**Location:** `internal/codex/session.go:365-365`
**Description:** When Codex launches a collab/subagent thread, its `item/started` and `item/completed` notifications carry that child `threadId`. The current implementation ignores `p.ThreadID`/`p.TurnID` and passes every item to `handleItem`, which causes child `agentMessage` content to be streamed as parent text and appended to `RunResult.Result`.
**Action Required:** Apply the existing child-thread guard for `turn/completed` notifications to include item-level notifications as well.

### 2. Complete sessions when start returns a terminal turn
**Location:** `internal/codex/session.go:173-174`
**Description:** If `turn/start` returns a response with an already `completed`, `failed`, or `interrupted` status, the code currently only records the ID and waits for a subsequent `turn/completed` notification. Since the app-server protocol allows terminal statuses in the start response, this causes the `Wait` call to hang until the outer timeout.
**Action Required:** Update the logic to recognize terminal statuses in the initial start response and return the result immediately if no further notifications are expected.

### 3. Start Codex in a killable process group
**Location:** `internal/codex/client.go:98-98`
**Description:** For turns involving shell commands, cancellation or timeouts only terminate the primary `codex app-server` process. Because this command is not started in its own process group and `cmd.Cancel` is not overridden, any long-running child processes spawned by the server will be orphaned.
**Action Required:** Ensure the process starts in a new process group and implement a custom `Cancel` logic to ensure all children are terminated correctly.

### 4. Ask git for repo-root-relative ignored paths
**Location:** `internal/palace/miner.go:277-290`
**Description:** When mining a subdirectory of a repository, `git -C realDir ls-files ...` emits paths relative to that directory unless `--full-name` is used. The current logic joins these to the repo root, resulting in strings like `../ignored.go`. This causes the `isGitIgnoredSet` check to fail for ignored files within subdirectories.
**Action Required:** Use the `--full-name` flag or adjust path joining to correctly handle relative paths returned by `git -C`.
