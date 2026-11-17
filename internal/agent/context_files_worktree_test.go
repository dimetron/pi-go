package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
}

// A worktree nested in its primary checkout must load the repository's rules
// once, from the worktree — not a second copy from the checkout above it.
// The fixture is a real `git worktree add`, so the .git file and commondir
// are whatever git actually writes.
func TestLoadInstructionWorktreeLoadsRepoRulesOnce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Resolve symlinks so the paths git records match the ones walked.
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(parent, "repo")
	writeTestFile(t, filepath.Join(parent, "AGENTS.md"), "# Workspace Rules\n- above the repo")
	writeTestFile(t, filepath.Join(primary, "AGENTS.md"), "# Repo Rules\n- PRIMARY-COPY")

	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{
			"-c", "user.name=t", "-c", "user.email=t@example.com",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=",
		}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(primary, "init", "-q")
	git(primary, "add", "AGENTS.md")
	git(primary, "commit", "-q", "-m", "init")
	worktree := filepath.Join(primary, ".worktrees", "topic")
	git(primary, "worktree", "add", "-q", "-b", "topic", worktree)
	writeTestFile(t, filepath.Join(worktree, "AGENTS.md"), "# Repo Rules\n- WORKTREE-COPY")

	cwd := filepath.Join(worktree, "internal", "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	result := loadInstructionFrom("Base instruction.", cwd, "")

	if !strings.Contains(result, "WORKTREE-COPY") {
		t.Errorf("worktree rules missing, got %q", result)
	}
	if strings.Contains(result, "PRIMARY-COPY") {
		t.Errorf("primary checkout's rules loaded on top of the worktree's, got %q", result)
	}
	if got := strings.Count(result, "# Repo Rules"); got != 1 {
		t.Errorf("repo rules loaded %d times, want 1", got)
	}
	// Rules above the repository are a different scope and still apply.
	if !strings.Contains(result, "Workspace Rules") {
		t.Errorf("rules above the repository were dropped, got %q", result)
	}

	// From the primary checkout nothing changes.
	result = loadInstructionFrom("Base instruction.", primary, "")
	if !strings.Contains(result, "PRIMARY-COPY") || !strings.Contains(result, "Workspace Rules") {
		t.Errorf("primary checkout lost its rules, got %q", result)
	}
}

// The same layout written by hand, so the rule is exercised where git is not
// installed, including a relative commondir as git writes it.
func TestLoadInstructionWorktreeSyntheticLayout(t *testing.T) {
	primary := t.TempDir()
	worktree := filepath.Join(primary, ".worktrees", "topic")
	private := filepath.Join(primary, ".git", "worktrees", "topic")
	writeTestFile(t, filepath.Join(primary, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTestFile(t, filepath.Join(private, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(worktree, ".git"), "gitdir: "+private+"\n")
	writeTestFile(t, filepath.Join(primary, "AGENTS.md"), "PRIMARY-COPY")
	writeTestFile(t, filepath.Join(worktree, "AGENTS.md"), "WORKTREE-COPY")

	result := loadInstructionFrom("Base instruction.", worktree, "")

	if !strings.Contains(result, "WORKTREE-COPY") || strings.Contains(result, "PRIMARY-COPY") {
		t.Errorf("want only the worktree's rules, got %q", result)
	}
}

// A nested checkout of a *different* repository — a submodule, a vendored
// clone — keeps inheriting the enclosing repository's rules.
func TestLoadInstructionNestedForeignRepoKeepsParentRules(t *testing.T) {
	outer := t.TempDir()
	writeTestFile(t, filepath.Join(outer, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTestFile(t, filepath.Join(outer, "AGENTS.md"), "OUTER-RULES")

	for name, setup := range map[string]func(t *testing.T, inner string){
		"clone": func(t *testing.T, inner string) {
			writeTestFile(t, filepath.Join(inner, ".git", "HEAD"), "ref: refs/heads/main\n")
		},
		"submodule": func(t *testing.T, inner string) {
			mod := filepath.Join(outer, ".git", "modules", "sub")
			writeTestFile(t, filepath.Join(mod, "HEAD"), "ref: refs/heads/main\n")
			writeTestFile(t, filepath.Join(inner, ".git"), "gitdir: ../../.git/modules/sub\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := filepath.Join(outer, "third_party", name)
			setup(t, inner)
			writeTestFile(t, filepath.Join(inner, "AGENTS.md"), "INNER-RULES")

			result := loadInstructionFrom("Base instruction.", inner, "")

			if !strings.Contains(result, "INNER-RULES") || !strings.Contains(result, "OUTER-RULES") {
				t.Errorf("want both inner and outer rules, got %q", result)
			}
		})
	}
}

func TestGitCommonDir(t *testing.T) {
	dir := t.TempDir()
	if got := gitCommonDir(dir); got != "" {
		t.Errorf("gitCommonDir(no .git) = %q, want empty", got)
	}
	writeTestFile(t, filepath.Join(dir, ".git"), "not a gitdir pointer\n")
	if got := gitCommonDir(dir); got != "" {
		t.Errorf("gitCommonDir(malformed .git file) = %q, want empty", got)
	}
}
