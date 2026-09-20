package subagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The `base` field exists so a reviewer can read a change that is already
// committed. It travels through two hops before it reaches a worktree:
//
//	AgentInput.WorktreeBase -> ToSpawnInput -> SpawnInput.WorktreeBase
//	                                              -> resolveWorktreeUsage -> CreateAt
//
// The worktree test in complexity_subagent_test.go starts at the second hop, so
// it cannot see a field dropped by the first — and one was: ToSpawnInput built
// its SpawnInput without copying WorktreeBase, so every caller of the subagent
// tool's `base` got an empty base, no worktree, and a reviewer reading nothing.
// These tests pin both hops, because a guard on either one alone passes while
// the other is broken.
func TestToSpawnInputCarriesWorktreeBase(t *testing.T) {
	const base = "786cb47^"

	// code-reviewer declares `worktree: false`, which is the case the field
	// exists for: a base must be what creates the worktree, not the agent's
	// own default.
	in := AgentInput{Type: "code-reviewer", Prompt: "review", WorktreeBase: base}

	spawn, err := in.ToSpawnInput()
	if err != nil {
		t.Fatalf("ToSpawnInput: %v", err)
	}
	if spawn.WorktreeBase != base {
		t.Fatalf("WorktreeBase dropped by ToSpawnInput: AgentInput=%q -> SpawnInput=%q",
			base, spawn.WorktreeBase)
	}
	// The field is only useful if it also forces a worktree; carrying it while
	// resolveWorktreeUsage ignores it would still leave the reviewer with an
	// empty diff.
	if !resolveWorktreeUsage(spawn.Agent.Worktree, spawn.Worktree, spawn.WorkDir, spawn.WorktreeBase) {
		t.Error("a carried base did not force a worktree for an agent declaring worktree: false")
	}
}

// The end-to-end shape the tool actually takes: AgentInput -> SpawnInput ->
// worktree, then read the diff exactly as the review tools do. A worktree that
// merely branched at base passes a "does the file exist" check while still
// showing an empty `git diff`, so the assertion is on the diff.
func TestSubagentToolBaseReachesWorktreeDiff(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)
	o := &Orchestrator{repoRoot: repo, worktree: mgr}
	t.Cleanup(func() { _ = mgr.CleanupAll() })

	for _, content := range []string{"old", "new"} {
		if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "f.txt"}, {"commit", "-m", content}} {
			cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
	}

	// Enter at the conversion hop, the way internal/tools/subagent.go does.
	spawn, err := AgentInput{
		Type:         "code-reviewer",
		Prompt:       "review",
		WorktreeBase: "HEAD~1",
	}.ToSpawnInput()
	if err != nil {
		t.Fatalf("ToSpawnInput: %v", err)
	}

	useWorktree := resolveWorktreeUsage(spawn.Agent.Worktree, spawn.Worktree, spawn.WorkDir, spawn.WorktreeBase)
	workDir, err := o.resolveWorkDir("wd-e2e0123456", spawn, useWorktree)
	if err != nil {
		t.Fatalf("resolveWorkDir: %v", err)
	}
	if workDir == repo {
		t.Fatal("base did not produce a worktree: reviewer would run in the primary checkout")
	}

	// git diff is the only view the review tools have.
	out, err := exec.Command("git", "-C", workDir, "diff").CombinedOutput()
	if err != nil {
		t.Fatalf("git diff in worktree: %v: %s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("git diff is empty: a reviewer of this change would read nothing")
	}
}
