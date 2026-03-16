package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a temp directory, initializes a git repo, and returns the path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	return dir
}

func TestGitOverview_BasicRepo(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Create a file and commit
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "main.go").Run()
	cmd := exec.Command("git", "-C", dir, "commit", "-m", "initial commit")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	cmd.Run()

	// Modify a file (unstaged change)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	// Create an untracked file
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme\n"), 0o644)

	out, err := gitOverviewHandler(sb, nil, GitOverviewInput{})
	if err != nil {
		t.Fatal(err)
	}

	// Branch
	if out.Branch == "" {
		t.Error("expected non-empty branch")
	}

	// Recent commits
	if len(out.RecentCommits) != 1 {
		t.Errorf("expected 1 commit, got %d", len(out.RecentCommits))
	}
	if len(out.RecentCommits) > 0 && !strings.Contains(out.RecentCommits[0], "initial commit") {
		t.Errorf("expected commit message 'initial commit', got %q", out.RecentCommits[0])
	}

	// Unstaged
	if len(out.UnstagedFiles) != 1 {
		t.Errorf("expected 1 unstaged file, got %d: %v", len(out.UnstagedFiles), out.UnstagedFiles)
	}

	// Untracked
	if len(out.UntrackedFiles) != 1 {
		t.Errorf("expected 1 untracked file, got %d: %v", len(out.UntrackedFiles), out.UntrackedFiles)
	}
}

func TestGitOverview_NotARepo(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := gitOverviewHandler(sb, nil, GitOverviewInput{})
	if err == nil {
		t.Error("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' error, got: %v", err)
	}
}

func TestGitOverview_EmptyRepo(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	out, err := gitOverviewHandler(sb, nil, GitOverviewInput{})
	if err != nil {
		t.Fatal(err)
	}

	// Empty repo should have no commits
	if len(out.RecentCommits) != 0 {
		t.Errorf("expected 0 commits in empty repo, got %d", len(out.RecentCommits))
	}
}

func TestGitOverview_PorcelainParsing(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		staged    int
		unstaged  int
		untracked int
	}{
		{
			name:      "staged new file",
			input:     "A  newfile.go\n",
			staged:    1,
			unstaged:  0,
			untracked: 0,
		},
		{
			name:      "modified not staged",
			input:     " M file.go\n",
			staged:    0,
			unstaged:  1,
			untracked: 0,
		},
		{
			name:      "staged and modified",
			input:     "MM file.go\n",
			staged:    1,
			unstaged:  1,
			untracked: 0,
		},
		{
			name:      "untracked",
			input:     "?? unknown.txt\n",
			staged:    0,
			unstaged:  0,
			untracked: 1,
		},
		{
			name:      "mixed status",
			input:     "M  staged.go\n M unstaged.go\n?? new.txt\nA  added.go\n",
			staged:    2,
			unstaged:  1,
			untracked: 1,
		},
		{
			name:      "rename",
			input:     "R  old.go -> new.go\n",
			staged:    1,
			unstaged:  0,
			untracked: 0,
		},
		{
			name:      "empty output",
			input:     "",
			staged:    0,
			unstaged:  0,
			untracked: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			staged, unstaged, untracked := parsePorcelain(tc.input)
			if len(staged) != tc.staged {
				t.Errorf("staged: got %d, want %d (%v)", len(staged), tc.staged, staged)
			}
			if len(unstaged) != tc.unstaged {
				t.Errorf("unstaged: got %d, want %d (%v)", len(unstaged), tc.unstaged, unstaged)
			}
			if len(untracked) != tc.untracked {
				t.Errorf("untracked: got %d, want %d (%v)", len(untracked), tc.untracked, untracked)
			}
		})
	}
}

func TestGitOverview_StagedFiles(t *testing.T) {
	dir := initGitRepo(t)
	sb := testSandbox(t, dir)

	// Initial commit to get out of empty-repo state
	os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "init.txt").Run()
	cmd := exec.Command("git", "-C", dir, "commit", "-m", "init")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	cmd.Run()

	// Stage a new file
	os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package staged\n"), 0o644)
	exec.Command("git", "-C", dir, "add", "staged.go").Run()

	out, err := gitOverviewHandler(sb, nil, GitOverviewInput{})
	if err != nil {
		t.Fatal(err)
	}

	if len(out.StagedFiles) != 1 {
		t.Errorf("expected 1 staged file, got %d: %v", len(out.StagedFiles), out.StagedFiles)
	}

	// Test filtering: exclude staged
	f := false
	out2, err := gitOverviewHandler(sb, nil, GitOverviewInput{IncludeStaged: &f})
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.StagedFiles) != 0 {
		t.Errorf("expected 0 staged files with include_staged=false, got %d", len(out2.StagedFiles))
	}
}
