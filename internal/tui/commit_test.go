package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitCommand_NoLLM(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
		cfg:      Config{GenerateCommitMsg: nil},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd when no LLM configured")
	}
	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "not available") {
		t.Errorf("expected 'not available' message, got %q", mm.messages[0].content)
	}
}

func TestCommitCommand_NotARepo(t *testing.T) {
	dir := t.TempDir() // not a git repo

	m := &model{
		messages: make([]message, 0),
		cfg: Config{
			WorkDir:           dir,
			GenerateCommitMsg: func(ctx context.Context, diffs string) (string, error) { return "", nil },
		},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd for non-repo")
	}
	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "Not a git repository") {
		t.Errorf("expected 'Not a git repository' message, got %q", mm.messages[0].content)
	}
}

func TestCommitCommand_NoStagedChanges(t *testing.T) {
	dir := setupGitRepo(t)

	m := &model{
		messages: make([]message, 0),
		cfg: Config{
			WorkDir:           dir,
			GenerateCommitMsg: func(ctx context.Context, diffs string) (string, error) { return "", nil },
		},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd when no staged changes")
	}
	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "No staged changes") {
		t.Errorf("expected 'No staged changes' message, got %q", mm.messages[0].content)
	}
}

func TestCommitCommand_GeneratesConventionalFormat(t *testing.T) {
	dir := setupGitRepo(t)

	// Create and stage a file.
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	gitExec(t, dir, "add", "main.go")

	expectedMsg := "feat(main): add entry point"

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	m := &model{
		ctx:      ctx,
		messages: make([]message, 0),
		cfg: Config{
			WorkDir: dir,
			GenerateCommitMsg: func(ctx context.Context, diffs string) (string, error) {
				// Verify diffs contain the file content.
				if !strings.Contains(diffs, "main.go") {
					t.Errorf("expected diffs to contain 'main.go', got %q", diffs)
				}
				return expectedMsg, nil
			},
		},
	}

	newM, cmd := m.handleCommitCommand()
	mm := newM.(*model)

	if cmd == nil {
		t.Fatal("expected non-nil cmd for async LLM call")
	}
	if mm.commit == nil || mm.commit.phase != "generating" {
		t.Error("expected commit state phase 'generating'")
	}

	// Execute the cmd to simulate the LLM call.
	result := cmd()
	genMsg, ok := result.(commitGeneratedMsg)
	if !ok {
		t.Fatalf("expected commitGeneratedMsg, got %T", result)
	}
	if genMsg.err != nil {
		t.Fatalf("unexpected error: %v", genMsg.err)
	}
	if genMsg.message != expectedMsg {
		t.Errorf("expected %q, got %q", expectedMsg, genMsg.message)
	}

	// Process the generated message.
	newM2, _ := mm.handleCommitGenerated(genMsg)
	mm2 := newM2.(*model)

	if mm2.commit == nil || mm2.commit.phase != "confirming" {
		t.Error("expected commit state phase 'confirming'")
	}
	if mm2.commit.message != expectedMsg {
		t.Errorf("expected commit message %q, got %q", expectedMsg, mm2.commit.message)
	}
}

func TestCommitCommand_ConfirmCommits(t *testing.T) {
	dir := setupGitRepo(t)

	// Create and stage a file.
	writeFile(t, filepath.Join(dir, "hello.txt"), "hello world\n")
	gitExec(t, dir, "add", "hello.txt")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	commitMsg := "feat: add hello"
	m := &model{
		ctx:      ctx,
		messages: make([]message, 0),
		commit:   &commitState{phase: "confirming", message: commitMsg},
		cfg:      Config{WorkDir: dir},
	}

	// Confirm the commit.
	newM, cmd := m.handleCommitConfirm()
	mm := newM.(*model)

	if cmd == nil {
		t.Fatal("expected non-nil cmd for git commit")
	}
	if mm.commit != nil {
		t.Error("expected commit state to be cleared")
	}

	// Execute the git commit.
	result := cmd()
	doneMsg, ok := result.(commitDoneMsg)
	if !ok {
		t.Fatalf("expected commitDoneMsg, got %T", result)
	}
	if doneMsg.err != nil {
		t.Fatalf("commit failed: %v\n%s", doneMsg.err, doneMsg.output)
	}

	// Verify the commit was created.
	log, err := gitCmd(dir, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(log, "feat: add hello") {
		t.Errorf("expected commit message in log, got %q", log)
	}
}

func TestCommitCommand_CancelCommit(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
		commit:   &commitState{phase: "confirming", message: "some message"},
	}

	newM, _ := m.handleCommitCancel()
	mm := newM.(*model)

	if mm.commit != nil {
		t.Error("expected commit state to be nil after cancel")
	}
	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mm.messages))
	}
	if !strings.Contains(mm.messages[0].content, "cancelled") {
		t.Errorf("expected 'cancelled' message, got %q", mm.messages[0].content)
	}
}

func TestCommitCommand_LLMError(t *testing.T) {
	m := &model{
		messages: []message{{role: "assistant", content: "Generating..."}},
		commit:   &commitState{phase: "generating"},
	}

	newM, _ := m.handleCommitGenerated(commitGeneratedMsg{
		err: fmt.Errorf("API rate limit exceeded"),
	})
	mm := newM.(*model)

	if mm.commit != nil {
		t.Error("expected commit state to be nil after error")
	}
	if !strings.Contains(mm.messages[0].content, "Error generating") {
		t.Errorf("expected error message, got %q", mm.messages[0].content)
	}
}

func TestCommitParsePorcelain(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		staged    []string
		unstaged  []string
		untracked []string
	}{
		{
			name:   "staged modification",
			input:  "M  file.go\n",
			staged: []string{"M file.go"},
		},
		{
			name:     "unstaged modification",
			input:    " M file.go\n",
			unstaged: []string{"M file.go"},
		},
		{
			name:      "untracked file",
			input:     "?? newfile.go\n",
			untracked: []string{"newfile.go"},
		},
		{
			name:     "staged and unstaged",
			input:    "MM file.go\n",
			staged:   []string{"M file.go"},
			unstaged: []string{"M file.go"},
		},
		{
			name:   "added file",
			input:  "A  file.go\n",
			staged: []string{"A file.go"},
		},
		{
			name:   "renamed file",
			input:  "R  old.go -> new.go\n",
			staged: []string{"R new.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staged, unstaged, untracked := commitParsePorcelain(tt.input)

			if len(staged) != len(tt.staged) {
				t.Errorf("staged: expected %d, got %d: %v", len(tt.staged), len(staged), staged)
			}
			for i, s := range staged {
				if i < len(tt.staged) && s != tt.staged[i] {
					t.Errorf("staged[%d]: expected %q, got %q", i, tt.staged[i], s)
				}
			}

			if len(unstaged) != len(tt.unstaged) {
				t.Errorf("unstaged: expected %d, got %d: %v", len(tt.unstaged), len(unstaged), unstaged)
			}

			if len(untracked) != len(tt.untracked) {
				t.Errorf("untracked: expected %d, got %d: %v", len(tt.untracked), len(untracked), untracked)
			}
		})
	}
}

func TestHandleSlashCommandHelpContainsCommit(t *testing.T) {
	m := &model{
		input:    "/help",
		messages: make([]message, 0),
	}

	newM, _ := m.handleSlashCommand("/help")
	mm := newM.(*model)

	if !strings.Contains(mm.messages[0].content, "/commit") {
		t.Errorf("expected /help to mention /commit, got %q", mm.messages[0].content)
	}
}

// Test helpers

func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.email", "test@test.com")
	gitExec(t, dir, "config", "user.name", "Test")

	// Create initial commit so we have a HEAD.
	writeFile(t, filepath.Join(dir, "README.md"), "# Test\n")
	gitExec(t, dir, "add", "README.md")
	gitExec(t, dir, "commit", "-m", "initial commit")

	return dir
}

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing file %s: %v", path, err)
	}
}
