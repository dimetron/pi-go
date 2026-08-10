package subagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorktreeArtifacts drops the files a planner or task agent would leave in
// its worktree: written with the write tool, never committed.
func writeWorktreeArtifacts(t *testing.T, wtPath, specName string, names ...string) string {
	t.Helper()
	specDir := filepath.Join(wtPath, "specs", specName)
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(specDir, name), []byte("# "+name+"\n\nreal work\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return specDir
}

// TestCommitAll_PreservesUncommittedWork is the regression guard for the way
// /plan and /run lost everything they produced.
//
// Both flows end in CreateBackupBranch, MergeBack and Cleanup, and all three
// are defined in terms of commits — but neither the PDD SOP nor the bundled
// task agent ever commits. So the backup ref pointed at a commit holding none
// of the work, the merge took nothing, and `worktree remove --force` deleted
// the only copy.
func TestCommitAll_PreservesUncommittedWork(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	const agentID = "plan-features/001-demo"
	const specName = "features/001-demo"
	wtPath, err := mgr.Create(agentID, "pdd-demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeWorktreeArtifacts(t, wtPath, specName, "requirements.md", "design.md", "plan.md", "PROMPT.md")

	committed, err := mgr.CommitAll(agentID, "PDD plan: "+specName)
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if !committed {
		t.Fatal("CommitAll reported nothing to commit, but four files were written")
	}

	backupBranch := "specs/" + specName
	if err := mgr.CreateBackupBranch(agentID, backupBranch); err != nil {
		t.Fatalf("CreateBackupBranch: %v", err)
	}
	if _, err := mgr.MergeBack(agentID); err != nil {
		t.Fatalf("MergeBack: %v", err)
	}
	if err := mgr.Cleanup(agentID); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// The work must have reached the invoking checkout...
	for _, name := range []string{"requirements.md", "design.md", "plan.md", "PROMPT.md"} {
		path := filepath.Join(repo, "specs", specName, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s did not reach the invoking checkout: %v", name, err)
		}
	}

	// ...and must still be reachable from the backup ref, which is what
	// survives the branch delete in Cleanup.
	out, err := mgr.git("show", backupBranch+":specs/"+specName+"/PROMPT.md")
	if err != nil {
		t.Fatalf("PROMPT.md is not on the backup branch: %v (%s)", err, out)
	}
	if !strings.Contains(out, "real work") {
		t.Errorf("backup branch holds unexpected content: %q", out)
	}
}

// TestCommitAll_AbandonedPlanSurvivesShutdown covers the case that loses most
// work: planning is abandoned before PROMPT.md exists, so /plan never merges,
// and shutdown force-removes the worktree and force-deletes its branch. The
// backup ref has to keep the artifacts alive on its own.
func TestCommitAll_AbandonedPlanSurvivesShutdown(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	const agentID = "plan-features/002-abandoned"
	const specName = "features/002-abandoned"
	wtPath, err := mgr.Create(agentID, "pdd-abandoned")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// No PROMPT.md — the planner got as far as design.md and stopped.
	writeWorktreeArtifacts(t, wtPath, specName, "requirements.md", "design.md")

	if _, err := mgr.CommitAll(agentID, "PDD plan: "+specName); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	backupBranch := "specs/" + specName
	if err := mgr.CreateBackupBranch(agentID, backupBranch); err != nil {
		t.Fatalf("CreateBackupBranch: %v", err)
	}

	// The user quits: Orchestrator.Shutdown -> CleanupAll.
	if err := mgr.CleanupAll(); err != nil {
		t.Fatalf("CleanupAll: %v", err)
	}

	for _, name := range []string{"requirements.md", "design.md"} {
		out, err := mgr.git("show", backupBranch+":specs/"+specName+"/"+name)
		if err != nil {
			t.Errorf("%s was lost when the session ended: %v (%s)", name, err, out)
		}
	}
}

// TestCommitAll_NothingToCommit keeps the no-op path honest: an agent that
// produced nothing must not create an empty commit.
func TestCommitAll_NothingToCommit(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	const agentID = "task-empty"
	if _, err := mgr.Create(agentID); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := mgr.git("rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	committed, err := mgr.CommitAll(agentID, "should not happen")
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if committed {
		t.Error("CommitAll committed against a clean worktree")
	}

	after, err := mgr.git("rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if before != after {
		t.Errorf("HEAD moved from %s to %s with nothing to commit", before, after)
	}
}

// TestCommitAll_UnknownAgent covers the lookup failure.
func TestCommitAll_UnknownAgent(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	_, err := mgr.CommitAll("never-created", "msg")
	if err == nil {
		t.Fatal("expected an error for an agent with no worktree")
	}
	if !strings.Contains(err.Error(), "never-created") {
		t.Errorf("error %q does not name the unknown agent", err)
	}
}
