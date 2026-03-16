package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCommit is a test helper that commits in a temp repo with proper env.
func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "-m", msg)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}
}

func TestGitFileDiff_UnstagedChange(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Create and commit a file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "main.go").Run()
	gitCommit(t, dir, "initial")

	// Modify the file (unstaged)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	out, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{File: "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out.File != "main.go" {
		t.Errorf("expected file 'main.go', got %q", out.File)
	}
	if out.Diff == "" {
		t.Error("expected non-empty diff")
	}
	if out.LinesAdded < 1 {
		t.Errorf("expected at least 1 line added, got %d", out.LinesAdded)
	}
}

func TestGitFileDiff_StagedChange(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Create and commit a file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "main.go").Run()
	gitCommit(t, dir, "initial")

	// Modify and stage
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "main.go").Run()

	// Unstaged diff should be empty (changes are staged)
	unstaged, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{File: "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if unstaged.Diff != "" {
		t.Errorf("expected empty unstaged diff, got %q", unstaged.Diff)
	}

	// Staged diff should show changes
	staged, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{File: "main.go", Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Diff == "" {
		t.Error("expected non-empty staged diff")
	}
	if staged.LinesAdded < 1 {
		t.Errorf("expected at least 1 line added in staged diff, got %d", staged.LinesAdded)
	}
}

func TestGitFileDiff_NoChange(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "main.go").Run()
	gitCommit(t, dir, "initial")

	// No modifications — diff should be empty
	out, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{File: "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Diff != "" {
		t.Errorf("expected empty diff for unchanged file, got %q", out.Diff)
	}
	if out.LinesAdded != 0 || out.LinesRemoved != 0 {
		t.Errorf("expected 0 added/removed, got +%d -%d", out.LinesAdded, out.LinesRemoved)
	}
}

func TestGitFileDiff_BinaryFile(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Create and commit a binary file
	binaryContent := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}
	os.WriteFile(filepath.Join(dir, "image.bin"), binaryContent, 0o644)
	exec.Command("git", "-C", dir, "add", "image.bin").Run()
	gitCommit(t, dir, "add binary")

	// Modify binary file
	os.WriteFile(filepath.Join(dir, "image.bin"), []byte{0x00, 0x01, 0x03, 0xFF}, 0o644)

	out, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{File: "image.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Binary {
		t.Error("expected Binary=true for binary file")
	}
}

func TestGitFileDiff_EmptyFile(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// File field is required
	_, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{})
	if err == nil {
		t.Error("expected error for empty file field")
	}
}

func TestGitFileDiff_NotARepo(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := gitFileDiffHandler(sb, nil, GitFileDiffInput{File: "main.go"})
	if err == nil {
		t.Error("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' error, got: %v", err)
	}
}

// --- git-hunk tests ---

func TestGitHunk_SingleHunk(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "main.go").Run()
	gitCommit(t, dir, "initial")

	// Add lines at the end — single hunk
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	out, err := gitHunkHandler(sb, nil, GitHunkInput{File: "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalHunks != 1 {
		t.Errorf("expected 1 hunk, got %d", out.TotalHunks)
	}
	if len(out.Hunks) != 1 {
		t.Fatalf("expected 1 hunk in slice, got %d", len(out.Hunks))
	}
	if !strings.HasPrefix(out.Hunks[0].Header, "@@") {
		t.Errorf("expected hunk header starting with @@, got %q", out.Hunks[0].Header)
	}
	if out.Hunks[0].Added < 1 {
		t.Errorf("expected at least 1 added line, got %d", out.Hunks[0].Added)
	}
}

func TestGitHunk_MultipleHunks(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Create a file with many lines so changes in different parts create separate hunks
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "// line "+string(rune('A'+i)))
	}
	original := strings.Join(lines, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(original), 0o644)
	exec.Command("git", "-C", dir, "add", "big.go").Run()
	gitCommit(t, dir, "initial big file")

	// Modify lines near the top and near the bottom (separated enough for 2 hunks)
	lines[1] = "// MODIFIED TOP"
	lines[28] = "// MODIFIED BOTTOM"
	modified := strings.Join(lines, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(modified), 0o644)

	out, err := gitHunkHandler(sb, nil, GitHunkInput{File: "big.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalHunks < 2 {
		t.Errorf("expected at least 2 hunks, got %d", out.TotalHunks)
	}
}

func TestGitHunk_LineCounting(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "count.txt"), []byte("aaa\nbbb\nccc\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "count.txt").Run()
	gitCommit(t, dir, "initial")

	// Replace bbb with xxx and yyy (1 removed, 2 added)
	os.WriteFile(filepath.Join(dir, "count.txt"), []byte("aaa\nxxx\nyyy\nccc\n"), 0o644)

	out, err := gitHunkHandler(sb, nil, GitHunkInput{File: "count.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalHunks != 1 {
		t.Fatalf("expected 1 hunk, got %d", out.TotalHunks)
	}
	h := out.Hunks[0]
	if h.Removed != 1 {
		t.Errorf("expected 1 removed line, got %d", h.Removed)
	}
	if h.Added != 2 {
		t.Errorf("expected 2 added lines, got %d", h.Added)
	}
}

func TestGitHunk_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("clean\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "clean.txt").Run()
	gitCommit(t, dir, "initial")

	out, err := gitHunkHandler(sb, nil, GitHunkInput{File: "clean.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalHunks != 0 {
		t.Errorf("expected 0 hunks for unchanged file, got %d", out.TotalHunks)
	}
}

func TestParseHunks(t *testing.T) {
	diff := `diff --git a/file.go b/file.go
index abc1234..def5678 100644
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main

+func hello() {}
 func main() {}
@@ -10,3 +11,2 @@
 // end
-// removed line
 // final
`
	hunks := parseHunks(diff)
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}

	if hunks[0].Added != 1 {
		t.Errorf("hunk 0: expected 1 added, got %d", hunks[0].Added)
	}
	if hunks[0].Removed != 0 {
		t.Errorf("hunk 0: expected 0 removed, got %d", hunks[0].Removed)
	}

	if hunks[1].Added != 0 {
		t.Errorf("hunk 1: expected 0 added, got %d", hunks[1].Added)
	}
	if hunks[1].Removed != 1 {
		t.Errorf("hunk 1: expected 1 removed, got %d", hunks[1].Removed)
	}
}
