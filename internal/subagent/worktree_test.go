package subagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temporary git repo with an initial commit and returns its path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "config", "tag.gpgsign", "false"},
		// Git for Windows defaults core.autocrlf to true, which would rewrite
		// every LF these tests write into CRLF on checkout and break the
		// byte-for-byte content assertions.
		{"git", "config", "core.autocrlf", "false"},
		{"git", "commit", "--allow-empty", "-m", "initial commit"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v: %s", args, err, out)
		}
	}
	return dir
}

// initTestRepoWithIgnores is initTestRepo plus the ignore rules that hide spec
// artifacts in the real repo: the global **/specs/ pattern and the project's
// .pi-go/ rule. These are what let a plain `git add -A` silently stage nothing
// for planner output, and they must be present for CommitAll's regression guard
// to reproduce the loss in any environment, not just one whose ~/.gitignore
// happens to match. Installed via core.excludesFile so the test stays hermetic
// (independent of the machine's own global ignore file).
func initTestRepoWithIgnores(t *testing.T) string {
	t.Helper()
	dir := initTestRepo(t)

	ignore := filepath.Join(t.TempDir(), "global-gitignore")
	content := "**/specs/\n.pi-go/\n"
	if err := os.WriteFile(ignore, []byte(content), 0o644); err != nil {
		t.Fatalf("writing ignore file: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "config", "core.excludesFile", ignore).CombinedOutput(); err != nil {
		t.Fatalf("setting core.excludesFile: %v: %s", err, out)
	}
	return dir
}

func TestWorktree_CreateAndCleanup(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Create worktree.
	path, err := mgr.Create("agent-abc12345")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify path exists and is a directory.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("worktree path not found: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("worktree path is not a directory")
	}

	// Verify it's under .pi-go/tasks/.
	relPath, _ := filepath.Rel(repo, path)
	if !strings.HasPrefix(relPath, filepath.Join(".pi-go", "tasks")) {
		t.Errorf("path %s not under .pi-go/tasks/", relPath)
	}

	// Verify active count.
	if mgr.Active() != 1 {
		t.Errorf("Active() = %d, want 1", mgr.Active())
	}

	// Cleanup.
	if err := mgr.Cleanup("agent-abc12345"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Verify path removed.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree path still exists after cleanup")
	}

	// Verify active count.
	if mgr.Active() != 0 {
		t.Errorf("Active() = %d, want 0", mgr.Active())
	}
}

func TestWorktree_BranchNaming(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	_, err := mgr.Create("myagent-1234abcd-extra")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("myagent-1234abcd-extra") }()

	// Verify branch exists with correct name (shortID uses last 12 chars).
	expectedBranch := "pi-agent-34abcd-extra"
	cmd := exec.Command("git", "branch", "--list", expectedBranch)
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	branches := strings.TrimSpace(string(out))
	if branches == "" {
		cmd2 := exec.Command("git", "branch")
		cmd2.Dir = repo
		out2, _ := cmd2.CombinedOutput()
		if !strings.Contains(string(out2), expectedBranch) {
			t.Errorf("expected branch %s in:\n%s", expectedBranch, out2)
		}
	}
}

func TestWorktree_CreateWithRequestedName(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	path, err := mgr.Create("agent-abc12345", "features/TOO/004-acp-subagent")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-abc12345") }()

	relPath, _ := filepath.Rel(repo, path)
	expectedPath := filepath.Join(".pi-go", "tasks", "features-too-004-acp-subagent")
	if relPath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", relPath, expectedPath)
	}

	cmd := exec.Command("git", "branch", "--list", "features-too-004-acp-subagent")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) == "" {
		t.Fatalf("expected branch %q to exist; got %q", "features-too-004-acp-subagent", string(out))
	}
}

func TestWorktree_DuplicateCreate(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	_, err := mgr.Create("dup-agent")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("dup-agent") }()

	_, err = mgr.Create("dup-agent")
	if err == nil {
		t.Fatal("expected error on duplicate Create")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWorktree_MergeBack(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	wtPath, err := mgr.Create("merge-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("merge-test") }()

	// Make a change in the worktree.
	testFile := filepath.Join(wtPath, "new-file.txt")
	if err := os.WriteFile(testFile, []byte("hello from worktree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "new-file.txt"},
		{"git", "commit", "-m", "add new file from worktree"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wtPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s in worktree failed: %v: %s", args, err, out)
		}
	}

	// Merge back.
	out, err := mgr.MergeBack("merge-test")
	if err != nil {
		t.Fatalf("MergeBack: %v\noutput: %s", err, out)
	}

	// Verify the file exists in the main repo now.
	mainFile := filepath.Join(repo, "new-file.txt")
	content, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatalf("merged file not found: %v", err)
	}
	if string(content) != "hello from worktree\n" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestWorktree_MergeConflict(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Create a file in main and commit.
	conflictFile := filepath.Join(repo, "conflict.txt")
	if err := os.WriteFile(conflictFile, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("write conflict file: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "conflict.txt"},
		{"git", "commit", "-m", "add conflict file"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v: %s", args, err, out)
		}
	}

	// Create worktree.
	wtPath, err := mgr.Create("conflict-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("conflict-test") }()

	// Modify file in worktree.
	wtFile := filepath.Join(wtPath, "conflict.txt")
	if err := os.WriteFile(wtFile, []byte("worktree version\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "conflict.txt"},
		{"git", "commit", "-m", "worktree change"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wtPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s in worktree failed: %v: %s", args, err, out)
		}
	}

	// Modify same file in main.
	if err := os.WriteFile(conflictFile, []byte("main version\n"), 0o644); err != nil {
		t.Fatalf("write main file: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "conflict.txt"},
		{"git", "commit", "-m", "main change"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v: %s", args, err, out)
		}
	}

	// MergeBack should fail with conflict and abort the merge so the
	// main repo is left clean.
	_, err = mgr.MergeBack("conflict-test")
	if err == nil {
		t.Fatal("expected merge conflict error")
	}
	if !strings.Contains(err.Error(), "merge conflict") && !strings.Contains(err.Error(), "merge failed") {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify the main repo was left clean (MergeBack aborts on conflict).
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "conflict.txt") {
		t.Errorf("main repo left dirty after merge abort:\n%s", out)
	}
}

func TestWorktree_CleanupAll(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Create multiple worktrees.
	for _, id := range []string{"agent-aaa", "agent-bbb", "agent-ccc"} {
		if _, err := mgr.Create(id); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	if mgr.Active() != 3 {
		t.Fatalf("Active() = %d, want 3", mgr.Active())
	}

	// Cleanup all.
	if err := mgr.CleanupAll(); err != nil {
		t.Fatalf("CleanupAll: %v", err)
	}

	if mgr.Active() != 0 {
		t.Errorf("Active() = %d after CleanupAll, want 0", mgr.Active())
	}
}

// stashCount returns the number of entries in `git stash list`.
func stashCount(t *testing.T, repo string) int {
	t.Helper()
	cmd := exec.Command("git", "stash", "list")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git stash list: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(out)), "\n"))
}

func TestWorktree_CreateStashesUncommitted(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Add an untracked file in the main repo (simulates uncommitted changes).
	uncommitted := filepath.Join(repo, "uncommitted.txt")
	if err := os.WriteFile(uncommitted, []byte("uncommitted content\n"), 0o644); err != nil {
		t.Fatalf("write uncommitted file: %v", err)
	}

	before := stashCount(t, repo)

	// Create worktree — this should stash the uncommitted change.
	wtPath, err := mgr.Create("stash-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("stash-test") }()

	// Verify worktree was created.
	info, err := os.Stat(wtPath)
	if err != nil {
		t.Fatalf("worktree path not found: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("worktree path is not a directory")
	}

	// Verify main repo is clean after stash (uncommitted.txt should be gone).
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "uncommitted.txt") {
		t.Errorf("uncommitted.txt still present in main repo:\n%s", out)
	}

	// Verify exactly one new stash entry was created (regardless of message
	// text — this is the locale/version-independent check).
	after := stashCount(t, repo)
	if after != before+1 {
		t.Errorf("stash count = %d, want %d (one new entry)", after, before+1)
	}
}

// TestWorktree_MergeBackPopsStash verifies that a successful MergeBack
// applies and drops the stash entry that Create created, restoring the
// user's uncommitted changes to the main repo.
func TestWorktree_MergeBackPopsStash(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Add an uncommitted change in main.
	uncommitted := filepath.Join(repo, "uncommitted.txt")
	if err := os.WriteFile(uncommitted, []byte("preserved work\n"), 0o644); err != nil {
		t.Fatalf("write uncommitted file: %v", err)
	}

	// Create worktree (stashes the change).
	wtPath, err := mgr.Create("stash-merge-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	afterCreate := stashCount(t, repo)
	if afterCreate != 1 {
		t.Fatalf("stash count after Create = %d, want 1", afterCreate)
	}

	// Make a change in the worktree and commit.
	testFile := filepath.Join(wtPath, "new.txt")
	if err := os.WriteFile(testFile, []byte("from worktree\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "new.txt"},
		{"git", "commit", "-m", "add file from worktree"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wtPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s in worktree failed: %v: %s", args, err, out)
		}
	}

	// Merge back — should apply the stash and drop it.
	if _, err := mgr.MergeBack("stash-merge-test"); err != nil {
		t.Fatalf("MergeBack: %v", err)
	}

	// Verify stash list is now empty.
	afterMerge := stashCount(t, repo)
	if afterMerge != 0 {
		t.Errorf("stash count after MergeBack = %d, want 0 (stash should be popped and dropped)", afterMerge)
	}

	// Verify the user's uncommitted change is back in the main repo.
	if _, err := os.Stat(uncommitted); os.IsNotExist(err) {
		t.Error("uncommitted.txt not restored to main repo after MergeBack")
	}
	if content, err := os.ReadFile(uncommitted); err == nil {
		if string(content) != "preserved work\n" {
			t.Errorf("restored content = %q, want %q", content, "preserved work\n")
		}
	}

	// Verify the worktree's commit was merged in.
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); os.IsNotExist(err) {
		t.Error("worktree commit not present in main repo after merge")
	}
}

func TestWorktree_NotARepo(t *testing.T) {
	dir := t.TempDir() // Not a git repo.
	mgr := NewWorktreeManager(dir)

	_, err := mgr.Create("no-repo-agent")
	if err == nil {
		t.Fatal("expected error for non-repo directory")
	}
}

func TestWorktree_PathFor(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// No worktree yet.
	if p := mgr.PathFor("nope"); p != "" {
		t.Errorf("PathFor unknown = %q, want empty", p)
	}

	path, err := mgr.Create("pathfor-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("pathfor-test") }()

	if got := mgr.PathFor("pathfor-test"); got != path {
		t.Errorf("PathFor = %q, want %q", got, path)
	}
}

func TestWorktree_CleanupNonexistent(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	err := mgr.Cleanup("nonexistent")
	if err == nil {
		t.Fatal("expected error for cleanup of nonexistent worktree")
	}
	if !strings.Contains(err.Error(), "no worktree found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWorktree_MergeBack_RecoversMissingActiveEntry(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	wtPath, err := mgr.Create("merge-recover-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("merge-recover-test") }()

	testFile := filepath.Join(wtPath, "recovered-file.txt")
	if err := os.WriteFile(testFile, []byte("hello from recovered worktree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "recovered-file.txt"},
		{"git", "commit", "-m", "add recovered file from worktree"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wtPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s in worktree failed: %v: %s", args, err, out)
		}
	}

	mgr.mu.Lock()
	delete(mgr.active, "merge-recover-test")
	mgr.mu.Unlock()

	out, err := mgr.MergeBack("merge-recover-test")
	if err != nil {
		t.Fatalf("MergeBack with recovered metadata: %v\noutput: %s", err, out)
	}

	mainFile := filepath.Join(repo, "recovered-file.txt")
	content, err := os.ReadFile(mainFile)
	if err != nil {
		t.Fatalf("merged file not found: %v", err)
	}
	if string(content) != "hello from recovered worktree\n" {
		t.Errorf("unexpected content: %q", content)
	}
}

// TestWorktree_CreateWithExistingBranch verifies that Create reuses a branch
// that already exists in the repo (e.g. left over from a previous /run that
// did not clean up). This prevents the
// "fatal: a branch named X already exists" error when re-running a spec.
func TestWorktree_CreateWithExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Simulate a leftover branch from a prior run.
	cmd := exec.Command("git", "branch", "my-spec")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create leftover branch: %v: %s", err, out)
	}

	// Create should reuse the existing branch instead of failing.
	path, err := mgr.Create("agent-existing-branch", "my-spec")
	if err != nil {
		t.Fatalf("Create with existing branch: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-existing-branch") }()

	// Verify the worktree was attached to the leftover branch.
	cmd = exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "branch refs/heads/my-spec") {
		t.Errorf("expected worktree on branch my-spec, got:\n%s", out)
	}
	if !listingNamesPath(string(out), path) {
		t.Errorf("expected worktree at %s in list:\n%s", path, out)
	}
}

// listingNamesPath reports whether a `git worktree list --porcelain` listing
// names path.
//
// git prints worktree paths with forward slashes, and on Windows it prints the
// long form of a directory that t.TempDir hands back in 8.3 short form
// (C:\Users\RUNNER~1\... vs C:/Users/runneradmin/...), so a plain substring
// match is not enough there.
func listingNamesPath(listing, path string) bool {
	if strings.Contains(listing, path) || strings.Contains(listing, filepath.ToSlash(path)) {
		return true
	}
	long, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return strings.Contains(listing, filepath.ToSlash(long))
}

// TestWorktree_CreateWithExistingBranchAndWorktree verifies that Create
// re-attaches an existing worktree+branch pair without re-running git.
func TestWorktree_CreateWithExistingBranchAndWorktree(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// First create — sets up branch + worktree on disk.
	path1, err := mgr.Create("agent-first", "shared-spec")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Forget the active entry to simulate a fresh process re-attaching
	// after the previous run exited without Cleanup.
	mgr.mu.Lock()
	delete(mgr.active, "agent-first")
	mgr.mu.Unlock()

	// Second create with the same name should reuse, not error.
	path2, err := mgr.Create("agent-second", "shared-spec")
	if err != nil {
		t.Fatalf("re-attach Create: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-second") }()

	if path1 != path2 {
		t.Errorf("expected same worktree path, got %q vs %q", path1, path2)
	}
	if mgr.PathFor("agent-second") != path2 {
		t.Errorf("PathFor returned %q, want %q", mgr.PathFor("agent-second"), path2)
	}
}

// TestWorktree_CreateRejectsBranchOnDifferentWorktree verifies that Create
// refuses to reuse a branch that is currently checked out at a path other
// than the one we would have used.
func TestWorktree_CreateRejectsBranchOnDifferentWorktree(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Manually create a worktree at an unexpected location.
	customPath := filepath.Join(t.TempDir(), "custom-wt")
	cmd := exec.Command("git", "worktree", "add", "-b", "busy-spec", customPath, "HEAD")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed custom worktree: %v: %s", err, out)
	}
	defer func() {
		c := exec.Command("git", "worktree", "remove", "--force", customPath)
		c.Dir = repo
		_ = c.Run()
	}()

	// Attempting to reuse the branch from a different location should error.
	_, err := mgr.Create("agent-blocked", "busy-spec")
	if err == nil {
		t.Fatal("expected error when branch is checked out elsewhere")
	}
	if !strings.Contains(err.Error(), "already checked out") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestWorktree_CreatePrunesStaleDir verifies that a leftover worktree
// directory (without a matching branch attachment) is cleaned up before
// `git worktree add` runs, preventing "fatal: <path> already exists".
func TestWorktree_CreatePrunesStaleDir(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Pre-create the directory that Create would normally choose.
	wtPath := filepath.Join(repo, ".pi-go", "tasks", "stale-spec")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatalf("pre-create stale dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "garbage.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	path, err := mgr.Create("agent-stale", "stale-spec")
	if err != nil {
		t.Fatalf("Create over stale dir: %v", err)
	}
	defer func() { _ = mgr.Cleanup("agent-stale") }()

	if path != wtPath {
		t.Errorf("path = %q, want %q", path, wtPath)
	}
}

// TestWorktree_BranchExistsHelper sanity-checks the branchExists helper.
func TestWorktree_BranchExistsHelper(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	if mgr.branchExists("does-not-exist") {
		t.Error("branchExists returned true for missing branch")
	}

	cmd := exec.Command("git", "branch", "real-branch")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v: %s", err, out)
	}

	if !mgr.branchExists("real-branch") {
		t.Error("branchExists returned false for existing branch")
	}
	if mgr.branchExists("") {
		t.Error("branchExists returned true for empty branch name")
	}
}

// TestWorktree_RecoverRejectsOrphanedBranch verifies that recoverWorktreeInfo
// refuses to operate when the branch is not attached to any worktree, so
// MergeBack does not silently re-attach a branch the user already cleaned up.
func TestWorktree_RecoverRejectsOrphanedBranch(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// Create a worktree, then manually remove the worktree (and prune)
	// leaving the branch orphaned.
	path, err := mgr.Create("orphan-recover-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree remove: %v: %s", err, out)
	}
	_, _ = mgr.git("worktree", "prune")
	_ = mgr.Cleanup("orphan-recover-test") // remove from active map

	// Recover should now fail because the branch is orphaned (no worktree
	// has it checked out).
	_, err = mgr.recoverWorktreeInfo("orphan-recover-test")
	if err == nil {
		t.Fatal("expected recover to refuse an orphaned branch")
	}
	if !strings.Contains(err.Error(), "orphaned") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestWorktree_FindStashByMessage exercises the stash lookup helper that
// MergeBack uses to identify the entry to pop.
func TestWorktree_FindStashByMessage(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	// No stash present.
	if _, _, found := mgr.findStashByMessage("anything"); found {
		t.Error("findStashByMessage returned true with empty stash list")
	}

	// Create a stash with a unique message.
	if err := os.WriteFile(filepath.Join(repo, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("git", "stash", "push", "-u", "-m", "unique-msg-12345")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stash push: %v: %s", err, out)
	}

	ref, _, found := mgr.findStashByMessage("unique-msg-12345")
	if !found {
		t.Fatal("findStashByMessage did not find existing entry")
	}
	if !strings.HasPrefix(ref, "stash@{") {
		t.Errorf("unexpected ref: %q", ref)
	}

	// Wrong message — not found.
	if _, _, found := mgr.findStashByMessage("wrong-msg"); found {
		t.Error("findStashByMessage returned true for non-matching message")
	}
	// Empty message — not found.
	if _, _, found := mgr.findStashByMessage(""); found {
		t.Error("findStashByMessage returned true for empty message")
	}
}

// commitFile writes name into the repo and commits it, returning the new sha.
func commitFile(t *testing.T, repo, name, content string) string {
	t.Helper()
	full := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-m", "add " + name}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// readFileAt reads a file out of a worktree.
func readFileAt(t *testing.T, wt, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wt, name))
	if err != nil {
		t.Fatalf("reading %s in %s: %v", name, wt, err)
	}
	return string(b)
}

// diffIn returns the unified diff a reviewer's git-file-diff/git-hunk tools
// would see inside the worktree: plain `git diff`, no refs.
func diffIn(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "diff").CombinedOutput()
	if err != nil {
		t.Fatalf("git diff in %s: %v: %s", dir, err, out)
	}
	return string(out)
}

// TestWorktree_CreateAtShowsTheChangeAsAPendingDiff is the point of CreateAt.
//
// The property is that `git diff` — the only view the review tools have —
// reports the change. A worktree merely branched at base checks base out with a
// clean index and shows an empty diff, which is the failure this feature exists
// to remove, so asserting file content instead of the diff is what let a broken
// implementation pass. This test fails on that variant.
func TestWorktree_CreateAtShowsTheChangeAsAPendingDiff(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	base := commitFile(t, repo, "f.txt", "old")
	commitFile(t, repo, "f.txt", "new")
	commitFile(t, repo, "added.txt", "brand new")

	wt, err := mgr.CreateAt("agent-diff001", base)
	if err != nil {
		t.Fatalf("CreateAt: %v", err)
	}

	diff := diffIn(t, wt)
	if diff == "" {
		t.Fatal("git diff is empty: a reviewer of this worktree would read nothing")
	}
	for _, want := range []string{"f.txt", "added.txt", "-old", "+new", "+brand new"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff does not mention %q:\n%s", want, diff)
		}
	}
}

// TestWorktree_CreateAtHoldsThePostChangeContent pins the other half of the
// contract: the agent reads the code as it is after the change, so prose and
// file reads describe the new state while the diff shows what moved.
func TestWorktree_CreateAtHoldsThePostChangeContent(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	base := commitFile(t, repo, "f.txt", "old")
	commitFile(t, repo, "f.txt", "new")

	wt, err := mgr.CreateAt("agent-content", base)
	if err != nil {
		t.Fatalf("CreateAt: %v", err)
	}
	if got := readFileAt(t, wt, "f.txt"); got != "new" {
		t.Errorf("content = %q, want %q (post-change content)", got, "new")
	}
}

// TestWorktree_CreateStillShowsNothing keeps the default honest: without a base
// there is no committed change to show, so the tree must stay clean. This fails
// if the staging step leaked into the no-base path.
func TestWorktree_CreateStillShowsNothing(t *testing.T) {
	repo := initTestRepo(t)

	commitFile(t, repo, "f.txt", "old")
	commitFile(t, repo, "f.txt", "new")

	for _, tc := range []struct {
		name string
		make func(m *WorktreeManager, id string) (string, error)
	}{
		{"Create", func(m *WorktreeManager, id string) (string, error) { return m.Create(id) }},
		{"CreateAt_empty_base", func(m *WorktreeManager, id string) (string, error) { return m.CreateAt(id, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewWorktreeManager(repo)
			wt, err := tc.make(m, "agent-"+tc.name)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if diff := diffIn(t, wt); diff != "" {
				t.Errorf("expected a clean tree, got a diff:\n%s", diff)
			}
			// Content still tracks HEAD, which is the pre-existing behavior.
			if got := readFileAt(t, wt, "f.txt"); got != "new" {
				t.Errorf("content = %q, want %q", got, "new")
			}
		})
	}
}

// TestWorktree_CreateAtRejectsUnknownRef checks the failure is caught before any
// git state changes: no worktree, no branch, and nothing left for Cleanup.
func TestWorktree_CreateAtRejectsUnknownRef(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	if _, err := mgr.CreateAt("agent-badref", "no-such-ref-anywhere"); err == nil {
		t.Fatal("expected an error for an unknown base ref")
	} else if !strings.Contains(err.Error(), "no-such-ref-anywhere") {
		t.Errorf("error = %v, want it to name the bad ref", err)
	}

	if err := mgr.Cleanup("agent-badref"); err == nil {
		t.Error("expected Cleanup to find no worktree for the rejected spawn")
	}
}

// TestWorktree_CreateAtAcceptsRevisionSyntax covers a relative rev, which is how
// a caller names a PR's base commit without knowing its sha.
func TestWorktree_CreateAtAcceptsRevisionSyntax(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	commitFile(t, repo, "f.txt", "first")
	commitFile(t, repo, "f.txt", "second")

	wt, err := mgr.CreateAt("agent-rev001", "HEAD~1")
	if err != nil {
		t.Fatalf("CreateAt(HEAD~1): %v", err)
	}
	diff := diffIn(t, wt)
	if !strings.Contains(diff, "+second") || !strings.Contains(diff, "-first") {
		t.Errorf("HEAD~1 diff does not show the last commit:\n%s", diff)
	}
}

// TestWorktree_CreateAtGivesABranchNotDetachedHEAD keeps the CommitAll →
// MergeBack → Cleanup path working for base-branched worktrees: a detached HEAD
// would leave MergeBack merging whatever the repo root currently has.
func TestWorktree_CreateAtGivesABranchNotDetachedHEAD(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	base := commitFile(t, repo, "f.txt", "old")
	commitFile(t, repo, "f.txt", "new")

	wt, err := mgr.CreateAt("agent-branch1", base)
	if err != nil {
		t.Fatalf("CreateAt: %v", err)
	}

	out, err := exec.Command("git", "-C", wt, "symbolic-ref", "--quiet", "HEAD").Output()
	if err != nil {
		t.Fatalf("worktree HEAD is not on a branch: %v", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("expected a branch name, got an empty symbolic-ref")
	}
}

// TestWorktree_CreateAtIsIdempotentOnRerun: a second spawn against the same base
// must not double-apply the staging step, which would turn the pending diff into
// a committed-looking state or fail outright.
func TestWorktree_CreateAtKeepsTheDiffAfterCleanupAll(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	base := commitFile(t, repo, "f.txt", "old")
	commitFile(t, repo, "f.txt", "new")

	if _, err := mgr.CreateAt("agent-twice001", base); err != nil {
		t.Fatalf("CreateAt: %v", err)
	}
	if err := mgr.CleanupAll(); err != nil {
		t.Fatalf("CleanupAll: %v", err)
	}

	// A fresh manager on the same repo must still produce the diff, which fails
	// if the first run left global git state behind (a dirty index in the main
	// worktree, a leftover branch).
	mgr2 := NewWorktreeManager(repo)
	wt, err := mgr2.CreateAt("agent-twice002", base)
	if err != nil {
		t.Fatalf("second CreateAt: %v", err)
	}
	if diff := diffIn(t, wt); diff == "" {
		t.Error("second run produced an empty diff")
	}
}
