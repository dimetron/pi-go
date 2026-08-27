package gitroot

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo makes dir a repository with one commit so worktrees can be added.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q", "-b", "main")
	run(t, dir, "git", "config", "user.email", "t@example.com")
	run(t, dir, "git", "config", "user.name", "t")
	run(t, dir, "git", "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "init")
	return dir
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// resolve normalizes macOS /var -> /private/var symlinking.
func resolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return r
}

func TestDetectPlainCheckout(t *testing.T) {
	repo := initRepo(t)
	got := Detect(t.Context(), repo)
	if got == "" {
		t.Fatal("Detect() = empty, want the repository root")
	}
	if resolve(t, got) != resolve(t, repo) {
		t.Errorf("Detect() = %q, want %q", got, repo)
	}
}

func TestDetectFromSubdirectory(t *testing.T) {
	repo := initRepo(t)
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if resolve(t, Detect(t.Context(), sub)) != resolve(t, repo) {
		t.Errorf("Detect(subdir) did not return the repository root")
	}
}

// TestDetectFromLinkedWorktree is the regression this package exists for:
// --show-toplevel returns the worktree, Detect must return the main checkout.
func TestDetectFromLinkedWorktree(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "side", wt)

	top := Toplevel(t.Context(), wt)
	if resolve(t, top) != resolve(t, wt) {
		t.Fatalf("Toplevel(worktree) = %q, want the worktree %q", top, wt)
	}

	got := Detect(t.Context(), wt)
	if resolve(t, got) != resolve(t, repo) {
		t.Errorf("Detect(worktree) = %q, want the main checkout %q", got, repo)
	}
	if resolve(t, got) == resolve(t, wt) {
		t.Error("Detect(worktree) returned the worktree — sandbox would be rooted too narrowly")
	}
}

func TestDetectNonRepo(t *testing.T) {
	dir := t.TempDir()
	// A temp dir may sit inside no repository; if the harness places it inside
	// one, Detect legitimately finds that one. Only assert the non-repo case.
	if got := Detect(t.Context(), dir); got != "" {
		if _, err := os.Stat(filepath.Join(got, ".git")); err != nil {
			t.Errorf("Detect(non-repo) = %q, which has no .git", got)
		}
	}
}

func TestDetectMissingDir(t *testing.T) {
	if got := Detect(t.Context(), filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("Detect(missing dir) = %q, want empty", got)
	}
}
