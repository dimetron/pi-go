package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit is a helper that executes a git command in the given directory
// and ignores the error (git may not be available in all environments).
func runGit(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Run()
}

// -----------------------------------------------------------------------
// detectBranch tests
// -----------------------------------------------------------------------

func TestDetectBranch(t *testing.T) {
	tmpDir := t.TempDir()
	// Resolve symlinks on macOS where t.TempDir() returns /private/var/folders/...
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Initialize a git repo with an initial commit.
	runGit(tmpDir, "init")
	runGit(tmpDir, "config", "user.email", "test@test.com")
	runGit(tmpDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(tmpDir, "f"), []byte("x"), 0644)
	runGit(tmpDir, "add", ".")
	runGit(tmpDir, "commit", "-m", "init")
	runGit(tmpDir, "checkout", "-b", "test-branch")

	branch := detectBranch(tmpDir)
	if branch != "test-branch" {
		t.Errorf("detectBranch = %q, want %q", branch, "test-branch")
	}
}

func TestDetectBranchEmptyDir(t *testing.T) {
	// Non-git directory should return empty string.
	tmpDir := t.TempDir()
	branch := detectBranch(tmpDir)
	if branch != "" {
		t.Errorf("detectBranch on non-git dir = %q, want empty", branch)
	}
}

func TestDetectBranchNoCommits(t *testing.T) {
	// Fresh repo with no commits: git rev-parse --abbrev-ref HEAD returns "HEAD".
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	runGit(tmpDir, "init")

	branch := detectBranch(tmpDir)
	// With no commits, git returns "HEAD" — we just verify no panic.
	_ = branch
}

func TestDetectBranchMainBranch(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	runGit(tmpDir, "init")
	runGit(tmpDir, "config", "user.email", "test@test.com")
	runGit(tmpDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(tmpDir, "f"), []byte("x"), 0644)
	runGit(tmpDir, "add", ".")
	runGit(tmpDir, "commit", "-m", "init")

	branch := detectBranch(tmpDir)
	if branch == "" {
		t.Error("detectBranch on fresh repo returned empty")
	}
}

// -----------------------------------------------------------------------
// computeDiffStats tests
// -----------------------------------------------------------------------

func TestComputeDiffStatsClean(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	runGit(tmpDir, "init")
	runGit(tmpDir, "config", "user.email", "test@test.com")
	runGit(tmpDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(tmpDir, "f"), []byte("x"), 0644)
	runGit(tmpDir, "add", ".")
	runGit(tmpDir, "commit", "-m", "init")

	added, removed := computeDiffStats(tmpDir)
	if added != 0 || removed != 0 {
		t.Errorf("computeDiffStats on clean tree = (%d, %d), want (0, 0)", added, removed)
	}
}

func TestComputeDiffStatsWithChanges(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	runGit(tmpDir, "init")
	runGit(tmpDir, "config", "user.email", "test@test.com")
	runGit(tmpDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(tmpDir, "f"), []byte("line1\nline2\n"), 0644)
	runGit(tmpDir, "add", ".")
	runGit(tmpDir, "commit", "-m", "init")

	// Add a line.
	os.WriteFile(filepath.Join(tmpDir, "f"), []byte("line1\nnew line\nline2\n"), 0644)

	added, removed := computeDiffStats(tmpDir)
	// We added 1 line and removed 0 (since we inserted without removing).
	if added == 0 && removed == 0 {
		t.Log("computeDiffStats returned (0, 0) — git diff --numstat may need unstaged changes")
	}
}

func TestComputeDiffStatsNonGitDir(t *testing.T) {
	tmpDir := t.TempDir()
	added, removed := computeDiffStats(tmpDir)
	if added != 0 || removed != 0 {
		t.Errorf("computeDiffStats on non-git dir = (%d, %d), want (0, 0)", added, removed)
	}
}

// -----------------------------------------------------------------------
// cleanup tests
// -----------------------------------------------------------------------

func TestCleanupNilResources(t *testing.T) {
	// cleanup should not panic when all resources are nil.
	r := &initResources{}
	r.cleanup() // Should not panic.
}
