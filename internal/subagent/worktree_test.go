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

	// Verify it's under .pi-go/worktrees/.
	relPath, _ := filepath.Rel(repo, path)
	if !strings.HasPrefix(relPath, filepath.Join(".pi-go", "worktrees")) {
		t.Errorf("path %s not under .pi-go/worktrees/", relPath)
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
	defer mgr.Cleanup("myagent-1234abcd-extra")

	// Verify branch exists with correct name.
	cmd := exec.Command("git", "branch", "--list", "pi-agent-myagent-")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	branches := strings.TrimSpace(string(out))
	if branches == "" {
		// Check with the actual shortID prefix.
		cmd2 := exec.Command("git", "branch")
		cmd2.Dir = repo
		out2, _ := cmd2.CombinedOutput()
		if !strings.Contains(string(out2), "pi-agent-myagent-") {
			t.Errorf("expected branch pi-agent-myagent- in:\n%s", out2)
		}
	}
}

func TestWorktree_DuplicateCreate(t *testing.T) {
	repo := initTestRepo(t)
	mgr := NewWorktreeManager(repo)

	_, err := mgr.Create("dup-agent")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	defer mgr.Cleanup("dup-agent")

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
	defer mgr.Cleanup("merge-test")

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
	os.WriteFile(conflictFile, []byte("original\n"), 0o644)
	for _, args := range [][]string{
		{"git", "add", "conflict.txt"},
		{"git", "commit", "-m", "add conflict file"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		cmd.CombinedOutput()
	}

	// Create worktree.
	wtPath, err := mgr.Create("conflict-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Cleanup("conflict-test")

	// Modify file in worktree.
	wtFile := filepath.Join(wtPath, "conflict.txt")
	os.WriteFile(wtFile, []byte("worktree version\n"), 0o644)
	for _, args := range [][]string{
		{"git", "add", "conflict.txt"},
		{"git", "commit", "-m", "worktree change"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wtPath
		cmd.CombinedOutput()
	}

	// Modify same file in main.
	os.WriteFile(conflictFile, []byte("main version\n"), 0o644)
	for _, args := range [][]string{
		{"git", "add", "conflict.txt"},
		{"git", "commit", "-m", "main change"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		cmd.CombinedOutput()
	}

	// MergeBack should fail with conflict.
	_, err = mgr.MergeBack("conflict-test")
	if err == nil {
		t.Fatal("expected merge conflict error")
	}
	if !strings.Contains(err.Error(), "merge failed") {
		t.Errorf("unexpected error: %v", err)
	}

	// Abort the merge so cleanup works.
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = repo
	cmd.CombinedOutput()
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
	defer mgr.Cleanup("pathfor-test")

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
