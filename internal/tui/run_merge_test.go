package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// newMergeTestOrch builds an orchestrator over a real git repo so the merge
// sequence can be driven end-to-end. Merging is the step that decides whether
// an agent's work survives, so it is worth exercising against real git rather
// than a stand-in.
func newMergeTestOrch(t *testing.T) (*subagent.Orchestrator, string) {
	t.Helper()
	repo := initRunTestRepo(t)
	orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
	t.Cleanup(orch.Shutdown)
	return orch, repo
}

// seedWorktree creates a worktree for agentID and writes a file into it,
// standing in for the work an agent leaves behind uncommitted.
func seedWorktree(t *testing.T, orch *subagent.Orchestrator, agentID, file, content string) string {
	t.Helper()
	wt, err := orch.Worktree().Create(agentID)
	if err != nil {
		t.Fatalf("creating worktree for %s: %v", agentID, err)
	}
	path := filepath.Join(wt, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt
}

// The agent never commits — mergeRunTargets must commit for it, or Cleanup
// force-removes the only copy of the work.
func TestMergeRunTargets_CommitsAndMergesUncommittedWork(t *testing.T) {
	orch, repo := newMergeTestOrch(t)
	const agentID = "task-merge-1"
	seedWorktree(t, orch, agentID, "feature.txt", "implemented\n")

	msg := mergeRunTargets(orch.Worktree(), []mergeTarget{
		{agentID: agentID, backup: runBackupBranchName("spec", "")},
	}, "spec")

	if msg.err != nil {
		t.Fatalf("merge failed: %v (output %s)", msg.err, msg.output)
	}

	got, err := os.ReadFile(filepath.Join(repo, "feature.txt"))
	if err != nil {
		t.Fatalf("the agent's file did not reach the main repo: %v", err)
	}
	if string(got) != "implemented\n" {
		t.Errorf("content = %q, want %q", got, "implemented\n")
	}
}

// Both halves of a parallel run must land, not just the primary.
func TestMergeRunTargets_MergesEveryParallelWorktree(t *testing.T) {
	orch, repo := newMergeTestOrch(t)
	seedWorktree(t, orch, "task-p1", "part1.txt", "one\n")
	seedWorktree(t, orch, "task-p2", "part2.txt", "two\n")

	msg := mergeRunTargets(orch.Worktree(), []mergeTarget{
		{agentID: "task-p1", backup: runBackupBranchName("spec", "part-1")},
		{agentID: "task-p2", backup: runBackupBranchName("spec", "part-2")},
	}, "spec")

	if msg.err != nil {
		t.Fatalf("merge failed: %v (output %s)", msg.err, msg.output)
	}
	for _, f := range []string{"part1.txt", "part2.txt"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("%s did not reach the main repo: %v", f, err)
		}
	}
}

// A worktree carried through a collapsed parallel retry must still merge —
// this is the data-loss path #128 fixed, proven against real git.
func TestMergeRunTargets_CarriedWorktreeStillLands(t *testing.T) {
	orch, repo := newMergeTestOrch(t)
	seedWorktree(t, orch, "task-owner", "owner.txt", "owner\n")
	seedWorktree(t, orch, "task-carried", "carried.txt", "carried\n")

	rs := &runState{
		specName:        "spec",
		worktreeAgentID: "task-owner",
		parallel: []*parallelAgent{
			{agentID: "task-owner"},
			{agentID: "task-carried"},
		},
	}
	rs.collapseParallel()

	msg := mergeRunTargets(orch.Worktree(), rs.mergeTargets(), rs.specName)

	if msg.err != nil {
		t.Fatalf("merge failed: %v (output %s)", msg.err, msg.output)
	}
	if _, err := os.Stat(filepath.Join(repo, "carried.txt")); err != nil {
		t.Errorf("the carried worktree's work was lost: %v", err)
	}
}

// The backup ref must end up on the committed work, not the base commit it was
// created at — otherwise the backup restores an empty tree.
func TestMergeRunTargets_BackupBranchMovesOntoTheWork(t *testing.T) {
	orch, _ := newMergeTestOrch(t)
	const agentID = "task-backup-move"
	seedWorktree(t, orch, agentID, "work.txt", "work\n")

	backup := runBackupBranchName("spec", "")
	if err := orch.Worktree().CreateBackupBranch(agentID, backup); err != nil {
		t.Fatalf("pre-creating the backup branch: %v", err)
	}

	msg := mergeRunTargets(orch.Worktree(), []mergeTarget{
		{agentID: agentID, backup: backup},
	}, "spec")

	if msg.err != nil {
		t.Fatalf("merge failed: %v (output %s)", msg.err, msg.output)
	}
}

// An unknown agent has no worktree; the failure must name it rather than
// merging nothing and reporting success.
func TestMergeRunTargets_UnknownAgentFailsAndIsNamed(t *testing.T) {
	orch, _ := newMergeTestOrch(t)

	msg := mergeRunTargets(orch.Worktree(), []mergeTarget{
		{agentID: "task-does-not-exist", backup: runBackupBranchName("spec", "")},
	}, "spec")

	if msg.err == nil {
		t.Fatal("merging an agent with no worktree should fail")
	}
	if msg.failedAgentID != "task-does-not-exist" {
		t.Errorf("failedAgentID = %q, want the unknown agent named", msg.failedAgentID)
	}
}

// A failure must stop the run rather than continuing to merge and clean up
// later worktrees behind a broken one.
func TestMergeRunTargets_StopsAtFirstFailure(t *testing.T) {
	orch, repo := newMergeTestOrch(t)
	seedWorktree(t, orch, "task-good", "good.txt", "good\n")

	msg := mergeRunTargets(orch.Worktree(), []mergeTarget{
		{agentID: "task-missing", backup: runBackupBranchName("spec", "part-1")},
		{agentID: "task-good", backup: runBackupBranchName("spec", "part-2")},
	}, "spec")

	if msg.err == nil {
		t.Fatal("expected the first target's failure to stop the merge")
	}
	if _, err := os.Stat(filepath.Join(repo, "good.txt")); err == nil {
		t.Error("the second target should not have been merged after a failure")
	}
	if orch.Worktree().PathFor("task-good") == "" {
		t.Error("the untouched worktree should still exist for inspection")
	}
}

// Nothing to merge is not an error.
func TestMergeRunTargets_NoTargets(t *testing.T) {
	orch, _ := newMergeTestOrch(t)

	msg := mergeRunTargets(orch.Worktree(), nil, "spec")

	if msg.err != nil {
		t.Errorf("merging nothing should not fail: %v", msg.err)
	}
	if msg.output != "" {
		t.Errorf("output = %q, want empty", msg.output)
	}
}

// A run with no worktree manager reports that plainly instead of panicking.
func TestMergeWorktreeCmd_NoWorktreeManager(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	t.Cleanup(orch.Shutdown)

	m := &model{cfg: Config{Orchestrator: orch}, run: &runState{specName: "spec"}}

	cmd := m.mergeWorktreeCmd()
	if cmd == nil {
		t.Fatal("expected a command even without a worktree manager")
	}
	msg, ok := cmd().(runMergeResultMsg)
	if !ok {
		t.Fatalf("got %T, want runMergeResultMsg", cmd())
	}
	if !strings.Contains(msg.output, "no worktree manager") {
		t.Errorf("output = %q, want it to name the missing worktree manager", msg.output)
	}
}

func TestMergeWorktreeCmd_NoRunState(t *testing.T) {
	m := &model{}
	if cmd := m.mergeWorktreeCmd(); cmd != nil {
		t.Error("expected no command without run state")
	}
}

// The command path must merge for real, not just the extracted function.
func TestMergeWorktreeCmd_EndToEnd(t *testing.T) {
	orch, repo := newMergeTestOrch(t)
	const agentID = "task-cmd-e2e"
	seedWorktree(t, orch, agentID, "cmd.txt", "via cmd\n")

	m := &model{
		cfg: Config{Orchestrator: orch},
		run: &runState{specName: "spec", agentID: agentID, worktreeAgentID: agentID},
	}

	cmd := m.mergeWorktreeCmd()
	if cmd == nil {
		t.Fatal("expected a merge command")
	}
	msg, ok := cmd().(runMergeResultMsg)
	if !ok {
		t.Fatalf("got %T, want runMergeResultMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("merge failed: %v (output %s)", msg.err, msg.output)
	}
	if _, err := os.Stat(filepath.Join(repo, "cmd.txt")); err != nil {
		t.Errorf("work did not reach the main repo: %v", err)
	}
}

// Two agents editing the same file conflict on the second merge. The conflict
// must be reported with the worktree preserved, not swallowed — the work is
// only recoverable from that worktree.
func TestMergeRunTargets_ConflictPreservesTheWorktree(t *testing.T) {
	orch, _ := newMergeTestOrch(t)
	seedWorktree(t, orch, "task-c1", "shared.txt", "from agent one\n")
	seedWorktree(t, orch, "task-c2", "shared.txt", "from agent two\n")

	msg := mergeRunTargets(orch.Worktree(), []mergeTarget{
		{agentID: "task-c1", backup: runBackupBranchName("spec", "part-1")},
		{agentID: "task-c2", backup: runBackupBranchName("spec", "part-2")},
	}, "spec")

	if msg.err == nil {
		t.Fatal("expected the second merge to conflict")
	}
	if msg.failedAgentID != "task-c2" {
		t.Errorf("failedAgentID = %q, want task-c2", msg.failedAgentID)
	}
	if msg.preservedWTPath == "" {
		t.Error("a conflicting merge must report the preserved worktree path")
	}
	if _, err := os.Stat(msg.preservedWTPath); err != nil {
		t.Errorf("the preserved worktree should still exist: %v", err)
	}
	// The first agent's merge already landed, and its output is kept so the
	// report shows what did succeed before the failure.
	if !strings.Contains(msg.output, "task-c1") {
		t.Errorf("output = %q, want the successful merge retained", msg.output)
	}
}
