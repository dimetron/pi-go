package subagent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover the stash round trip Create performs so that
// `git worktree add` sees a clean tree. Between the `stash push -u` and the
// matching restore, the user's uncommitted work exists *only* as a stash
// entry, so every exit from that window has to put it back or say loudly that
// it could not.
//
// Every test here runs against a throwaway repo from initTestRepo (t.TempDir),
// never the checkout the test binary itself lives in.

// stashGit runs a git command in dir and fails the test if it errors.
func stashGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeStashFile writes name under repo with the given content.
func writeStashFile(t *testing.T, repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// wantStashFile asserts that name exists under repo with exactly content.
func wantStashFile(t *testing.T, repo, name, content string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(repo, name))
	if err != nil {
		t.Fatalf("%s not restored: %v", name, err)
	}
	if string(got) != content {
		t.Errorf("%s = %q, want %q", name, got, content)
	}
}

// dirtyRepo returns a repo holding a committed tracked.txt that has since been
// modified in the working tree, plus an untracked file. Both halves matter:
// Create stashes with `-u`, so a restore that only covers tracked changes
// would still lose the untracked file.
func dirtyRepo(t *testing.T) string {
	t.Helper()
	repo := initTestRepo(t)
	writeStashFile(t, repo, "tracked.txt", "committed\n")
	stashGit(t, repo, "add", "tracked.txt")
	stashGit(t, repo, "commit", "-m", "add tracked.txt")

	writeStashFile(t, repo, "tracked.txt", "user edit\n")
	writeStashFile(t, repo, "untracked.txt", "user note\n")
	return repo
}

// blockedName is a worktree name that survives sanitizeWorktreeName but that
// git rejects as a branch name (refs may not end in ".lock"). It makes
// `git worktree add -b` fail *after* Create has already taken the stash, which
// is the only way to reach the recovery path under test.
const blockedName = "blocked.lock"

// TestSanitizeWorktreeName_KeepsBlockedName pins the assumption the failure
// injection rests on. If the sanitizer ever starts rewriting "blocked.lock",
// `git worktree add` would succeed and the tests below would silently stop
// exercising the recovery path — this fails loudly instead.
func TestSanitizeWorktreeName_KeepsBlockedName(t *testing.T) {
	if got := sanitizeWorktreeName(blockedName); got != blockedName {
		t.Fatalf("sanitizeWorktreeName(%q) = %q; the worktree-add failure injection no longer works", blockedName, got)
	}
}

// TestCreate_RestoresStashWhenWorktreeAddFails is the regression test for the
// data-loss bug: the failure path used to run `git stash drop`, which deletes
// the entry without applying it, so the user's uncommitted work survived only
// as a dangling commit until gc.
func TestCreate_RestoresStashWhenWorktreeAddFails(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	_, err := mgr.Create("agent-restore", blockedName)
	if err == nil {
		t.Fatal("Create succeeded; expected git worktree add to reject the branch name")
	}
	if !strings.Contains(err.Error(), "git worktree add") {
		t.Errorf("error %q does not report the worktree add failure", err)
	}
	if strings.Contains(err.Error(), "could NOT be restored") {
		t.Fatalf("restore itself failed: %v", err)
	}

	wantStashFile(t, repo, "tracked.txt", "user edit\n")
	wantStashFile(t, repo, "untracked.txt", "user note\n")

	// A successful restore must also consume the entry: leaving it behind
	// would strand an orphan in the stash list that nothing ever pops.
	if n := stashCount(t, repo); n != 0 {
		t.Errorf("stash count = %d, want 0 (entry should be dropped after a successful apply)", n)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active() = %d, want 0 after a failed Create", mgr.Active())
	}
}

// TestCreate_RestoreIgnoresDecoyStashEntry pins the by-message lookup. The
// restore used to hardcode stash@{0}, so any stash pushed between Create's push
// and its restore — by the user, an editor, or another agent — would be applied
// instead, restoring someone else's work over the tree and leaving the user's
// own entry behind.
func TestCreate_RestoreIgnoresDecoyStashEntry(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	// Simulate an unrelated stash landing while Create is mid-flight: it is
	// pushed after Create's own entry, so it becomes stash@{0} and Create's
	// entry moves to stash@{1}.
	mgr.gitRunner = func(dir string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "add" {
			writeStashFile(t, repo, "decoy.txt", "not the user's work\n")
			if out, err := runGit(repo, "stash", "push", "-u", "-m", "decoy-entry"); err != nil {
				t.Fatalf("pushing decoy stash: %v: %s", err, out)
			}
		}
		return runGit(dir, args...)
	}

	if _, err := mgr.Create("agent-decoy", blockedName); err == nil {
		t.Fatal("Create succeeded; expected git worktree add to fail")
	}

	wantStashFile(t, repo, "tracked.txt", "user edit\n")
	wantStashFile(t, repo, "untracked.txt", "user note\n")

	// The decoy must be untouched: still stashed, and its file still absent
	// from the working tree.
	if _, err := os.Stat(filepath.Join(repo, "decoy.txt")); err == nil {
		t.Error("decoy.txt was applied to the working tree; the wrong stash entry was restored")
	}
	if n := stashCount(t, repo); n != 1 {
		t.Errorf("stash count = %d, want 1 (only the decoy should remain)", n)
	}
	if _, found := mgr.findStashByMessage("decoy-entry"); !found {
		t.Error("decoy entry is gone from the stash list")
	}
}

// TestCreate_RestoreFailureSurfacesBothCauses covers the worst case: the
// worktree add failed *and* the stash could not be put back. The entry has to
// survive, and the returned error has to name both the original failure and
// the stash the user must recover by hand.
func TestCreate_RestoreFailureSurfacesBothCauses(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	// Recreate the untracked file after Create stashed it away. `git stash
	// apply` refuses to overwrite an existing untracked file, so the restore
	// fails for a reason git itself produces rather than a faked error.
	mgr.gitRunner = func(dir string, args ...string) (string, error) {
		out, err := runGit(dir, args...)
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "add" {
			writeStashFile(t, repo, "untracked.txt", "in the way\n")
		}
		return out, err
	}

	_, err := mgr.Create("agent-blocked", blockedName)
	if err == nil {
		t.Fatal("Create succeeded; expected git worktree add to fail")
	}

	// Neither cause may be swallowed.
	if !strings.Contains(err.Error(), "git worktree add") {
		t.Errorf("error %q lost the worktree add cause", err)
	}
	if !strings.Contains(err.Error(), "could NOT be restored") {
		t.Errorf("error %q does not report that the restore failed", err)
	}
	if !strings.Contains(err.Error(), "git stash apply") {
		t.Errorf("error %q lost the stash apply cause", err)
	}
	if !strings.Contains(err.Error(), "pi-go-worktree-agent-blocked-") {
		t.Errorf("error %q does not name the stash entry the user has to recover", err)
	}

	// Both causes are wrapped, not just formatted: the worktree add failure is
	// an *exec.ExitError and must still be reachable through the chain.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("errors.As could not reach the git exit error in %v", err)
	}

	// The entry must still be there — that is the user's only copy.
	if n := stashCount(t, repo); n != 1 {
		t.Fatalf("stash count = %d, want 1 (a failed restore must not drop the entry)", n)
	}
	if list := stashGit(t, repo, "stash", "list"); !strings.Contains(list, "pi-go-worktree-agent-blocked-") {
		t.Errorf("stash list %q does not hold Create's entry", list)
	}
}

// TestCreate_KeepsStashOnSuccess covers the success path: the entry survives
// Create so MergeBack or Cleanup can deal with it, and it is recorded by the
// message they look it up with.
func TestCreate_KeepsStashOnSuccess(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-ok"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-ok") }()

	if n := stashCount(t, repo); n != 1 {
		t.Fatalf("stash count = %d, want 1 (stash must survive a successful Create)", n)
	}

	mgr.mu.Lock()
	info := mgr.active["agent-ok"]
	mgr.mu.Unlock()
	if info.StashMsg == "" {
		t.Fatal("StashMsg not recorded after a successful Create")
	}
	if _, found := mgr.findStashByMessage(info.StashMsg); !found {
		t.Errorf("recorded StashMsg %q does not match any stash entry", info.StashMsg)
	}

	// The tree is clean, which is the point of stashing in the first place.
	if _, err := os.Stat(filepath.Join(repo, "untracked.txt")); err == nil {
		t.Error("untracked.txt still present; stash push did not clean the tree")
	}
}

// TestMergeBack_AppliesOwnStashNotDecoy is the MergeBack half of the by-message
// lookup: the same stash@{0} assumption meant a concurrent stash could be
// applied in place of the one Create took.
func TestMergeBack_AppliesOwnStashNotDecoy(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-merge-decoy"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-merge-decoy") }()

	// Someone else stashes while the agent is running.
	writeStashFile(t, repo, "decoy.txt", "not the user's work\n")
	stashGit(t, repo, "stash", "push", "-u", "-m", "decoy-entry")

	if _, err := mgr.MergeBack("agent-merge-decoy"); err != nil {
		t.Fatalf("MergeBack: %v", err)
	}

	wantStashFile(t, repo, "tracked.txt", "user edit\n")
	wantStashFile(t, repo, "untracked.txt", "user note\n")
	if _, err := os.Stat(filepath.Join(repo, "decoy.txt")); err == nil {
		t.Error("decoy.txt was applied; MergeBack restored the wrong stash entry")
	}
	if n := stashCount(t, repo); n != 1 {
		t.Errorf("stash count = %d, want 1 (only the decoy should remain)", n)
	}
	if _, found := mgr.findStashByMessage("decoy-entry"); !found {
		t.Error("decoy entry is gone from the stash list")
	}
}

// TestPopStashByMessage_MissingEntry checks the explicit not-found path: with
// no matching entry, popStashByMessage reports it rather than falling back to
// whatever sits on top of the stash.
func TestPopStashByMessage_MissingEntry(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	writeStashFile(t, repo, "someone-elses.txt", "unrelated\n")
	stashGit(t, repo, "stash", "push", "-u", "-m", "unrelated-entry")

	err := mgr.popStashByMessage("pi-go-worktree-never-pushed")
	if err == nil {
		t.Fatal("popStashByMessage succeeded with no matching entry")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q does not report a missing entry", err)
	}

	if n := stashCount(t, repo); n != 1 {
		t.Errorf("stash count = %d, want 1 (the unrelated entry must be untouched)", n)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "someone-elses.txt")); statErr == nil {
		t.Error("the unrelated stash entry was applied")
	}
}

// TestPopStashByMessage_ApplyFailureKeepsEntry checks that a failed apply
// leaves the entry in the list, since that is the user's route back to the
// content.
func TestPopStashByMessage_ApplyFailureKeepsEntry(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	writeStashFile(t, repo, "note.txt", "stashed\n")
	stashGit(t, repo, "stash", "push", "-u", "-m", "keep-me")

	// An untracked file at the same path makes `git stash apply` bail out.
	writeStashFile(t, repo, "note.txt", "in the way\n")

	err := mgr.popStashByMessage("keep-me")
	if err == nil {
		t.Fatal("popStashByMessage succeeded despite the blocked apply")
	}
	if !strings.Contains(err.Error(), "git stash apply") {
		t.Errorf("error %q does not report the apply failure", err)
	}
	if n := stashCount(t, repo); n != 1 {
		t.Errorf("stash count = %d, want 1 (a failed apply must not drop the entry)", n)
	}
}

// TestCleanup_RestoresStashWhenMergeBackSkipped is the regression test for the
// path that is reachable without anything going wrong in git: a run that ends
// before MergeBack (a failed spawn, a shutdown mid-setup, a discarded result)
// used to reach `stash drop` and delete the user's uncommitted work.
func TestCleanup_RestoresStashWhenMergeBackSkipped(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-abandoned"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n := stashCount(t, repo); n != 1 {
		t.Fatalf("stash count after Create = %d, want 1", n)
	}

	// No MergeBack — straight to Cleanup, as orchestrator.go does on a spawn
	// failure or a shutdown during setup.
	if err := mgr.Cleanup("agent-abandoned"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	wantStashFile(t, repo, "tracked.txt", "user edit\n")
	wantStashFile(t, repo, "untracked.txt", "user note\n")
	if n := stashCount(t, repo); n != 0 {
		t.Errorf("stash count = %d, want 0 (entry should be dropped only after the apply succeeded)", n)
	}
}

// TestCleanup_KeepsStashWhenRestoreFails checks the other half of the rule: if
// Cleanup cannot put the work back, the entry stays and the failure is
// reported rather than swallowed.
func TestCleanup_KeepsStashWhenRestoreFails(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-blocked-cleanup"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Something recreates the untracked file while the agent runs, so the
	// restore's `git stash apply` refuses to overwrite it.
	writeStashFile(t, repo, "untracked.txt", "in the way\n")

	err := mgr.Cleanup("agent-blocked-cleanup")
	if err == nil {
		t.Fatal("Cleanup succeeded despite the blocked restore")
	}
	if !strings.Contains(err.Error(), "restore stashed changes") {
		t.Errorf("error %q does not report the failed restore", err)
	}
	if !strings.Contains(err.Error(), "pi-go-worktree-agent-blocked-cleanup-") {
		t.Errorf("error %q does not name the stash entry the user has to recover", err)
	}
	if n := stashCount(t, repo); n != 1 {
		t.Errorf("stash count = %d, want 1 (a failed restore must not drop the entry)", n)
	}
}

// TestCleanup_IgnoresDecoyStashEntry is the Cleanup half of the by-message
// lookup: an unrelated entry sitting at stash@{0} must not be applied or
// dropped.
func TestCleanup_IgnoresDecoyStashEntry(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-cleanup-decoy"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeStashFile(t, repo, "decoy.txt", "not the user's work\n")
	stashGit(t, repo, "stash", "push", "-u", "-m", "decoy-entry")

	if err := mgr.Cleanup("agent-cleanup-decoy"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	wantStashFile(t, repo, "tracked.txt", "user edit\n")
	wantStashFile(t, repo, "untracked.txt", "user note\n")
	if _, err := os.Stat(filepath.Join(repo, "decoy.txt")); err == nil {
		t.Error("decoy.txt was applied; Cleanup restored the wrong stash entry")
	}
	if n := stashCount(t, repo); n != 1 {
		t.Errorf("stash count = %d, want 1 (only the decoy should remain)", n)
	}
	if _, found := mgr.findStashByMessage("decoy-entry"); !found {
		t.Error("decoy entry is gone from the stash list")
	}
}

// TestCleanup_AfterMergeBackIsNoOp covers the ordinary two-step flow. MergeBack
// has already restored and dropped the entry, so Cleanup finds nothing —
// errStashEntryGone is expected there and must not surface as a cleanup error.
func TestCleanup_AfterMergeBackIsNoOp(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-merge-then-clean"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := mgr.MergeBack("agent-merge-then-clean"); err != nil {
		t.Fatalf("MergeBack: %v", err)
	}
	if err := mgr.Cleanup("agent-merge-then-clean"); err != nil {
		t.Fatalf("Cleanup after MergeBack: %v", err)
	}

	wantStashFile(t, repo, "tracked.txt", "user edit\n")
	wantStashFile(t, repo, "untracked.txt", "user note\n")
	if n := stashCount(t, repo); n != 0 {
		t.Errorf("stash count = %d, want 0", n)
	}
}

// TestMergeBack_KeepsStashWhenApplyFails pins the fix to the path whose error
// message used to be impossible to follow: it told the user to recover with
// `git stash show` immediately after dropping the entry that command would
// have shown.
func TestMergeBack_KeepsStashWhenApplyFails(t *testing.T) {
	repo := dirtyRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.Create("agent-merge-blocked"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-merge-blocked") }()

	// Recreate the untracked file the stash holds, so the post-merge apply
	// refuses to overwrite it.
	writeStashFile(t, repo, "untracked.txt", "in the way\n")

	_, err := mgr.MergeBack("agent-merge-blocked")
	if err == nil {
		t.Fatal("MergeBack succeeded despite the blocked stash apply")
	}
	if !strings.Contains(err.Error(), "git stash apply") {
		t.Errorf("error %q does not report the apply failure", err)
	}
	if !strings.Contains(err.Error(), "still stashed as") {
		t.Errorf("error %q does not tell the user the work is still stashed", err)
	}

	// The advice in that error must be followable: the entry is still there.
	if n := stashCount(t, repo); n != 1 {
		t.Fatalf("stash count = %d, want 1 (a failed apply must not drop the entry)", n)
	}
	if list := stashGit(t, repo, "stash", "list"); !strings.Contains(list, "pi-go-worktree-agent-merge-blocked-") {
		t.Errorf("stash list %q no longer holds the entry the error points at", list)
	}
}
