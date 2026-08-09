package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// initRunTestRepo builds a throwaway git repo with one commit, which is the
// minimum a WorktreeManager needs to create worktrees and branches.
func initRunTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "config", "tag.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "-m", "initial commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

func TestRunBackupBranchName(t *testing.T) {
	tests := []struct {
		name     string
		specName string
		suffix   string
		want     string
	}{
		{"plain spec", "my-spec", "", "run/my-spec"},
		{"nested spec flattens to dashes", "area/my-spec", "", "run/area-my-spec"},
		{"suffix becomes a branch segment", "my-spec", "part-1", "run/my-spec/part-1"},
		{"surrounding slashes are trimmed", "/my-spec/", "", "run/my-spec"},
		{"nested spec with suffix", "area/my-spec", "part-2", "run/area-my-spec/part-2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runBackupBranchName(tt.specName, tt.suffix); got != tt.want {
				t.Errorf("runBackupBranchName(%q, %q) = %q, want %q", tt.specName, tt.suffix, got, tt.want)
			}
		})
	}
}

// TestRunBackupBranchName_DistinctPerAgent guards the property the parallel run
// depends on: two agents working the same spec must not be handed the same
// backup branch, or the second `git branch -f` silently overwrites the first.
func TestRunBackupBranchName_DistinctPerAgent(t *testing.T) {
	first := runBackupBranchName("my-spec", "part-1")
	second := runBackupBranchName("my-spec", "part-2")
	if first == second {
		t.Errorf("both parallel agents got backup branch %q", first)
	}
}

func TestCreateRunBackupBranch_NoOrchestrator(t *testing.T) {
	m := &model{chatModel: ChatModel{Messages: make([]message, 0)}}

	err := m.createRunBackupBranch("task-1", "run/my-spec")
	if err == nil {
		t.Fatal("expected an error with no orchestrator")
	}
	if !strings.Contains(err.Error(), "no worktree manager") {
		t.Errorf("error %q does not name the missing worktree manager", err)
	}
}

func TestCreateRunBackupBranch_NoWorktreeManager(t *testing.T) {
	// An orchestrator built without a repo root has no worktree manager.
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	t.Cleanup(orch.Shutdown)

	m := &model{cfg: Config{Orchestrator: orch}}

	err := m.createRunBackupBranch("task-1", "run/my-spec")
	if err == nil {
		t.Fatal("expected an error with no worktree manager")
	}
	if !strings.Contains(err.Error(), "no worktree manager") {
		t.Errorf("error %q does not name the missing worktree manager", err)
	}
}

func TestCreateRunBackupBranch_CreatesBranch(t *testing.T) {
	repo := initRunTestRepo(t)
	orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
	t.Cleanup(orch.Shutdown)

	const agentID = "task-backup-1"
	if _, err := orch.Worktree().Create(agentID); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}

	m := &model{cfg: Config{Orchestrator: orch}}

	branch := runBackupBranchName("my-spec", "")
	if err := m.createRunBackupBranch(agentID, branch); err != nil {
		t.Fatalf("createRunBackupBranch: %v", err)
	}

	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("backup branch %q was not created: %v: %s", branch, err, out)
	}
}

func TestCreateRunBackupBranch_UnknownAgent(t *testing.T) {
	repo := initRunTestRepo(t)
	orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
	t.Cleanup(orch.Shutdown)

	m := &model{cfg: Config{Orchestrator: orch}}

	err := m.createRunBackupBranch("never-spawned", "run/my-spec")
	if err == nil {
		t.Fatal("expected an error for an agent with no worktree")
	}
	if !strings.Contains(err.Error(), "never-spawned") {
		t.Errorf("error %q does not name the unknown agent", err)
	}
}

// writeRunSpec lays out specs/<name>/{PROMPT.md,plan.md} under workDir.
func writeRunSpec(t *testing.T, workDir, specName, prompt, plan string) {
	t.Helper()
	dir := filepath.Join(workDir, "specs", specName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PROMPT.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan != "" {
		if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runTestModel returns a model wired to an orchestrator that cannot resolve a
// model for the task role, so every spawn fails fast instead of launching a
// real subagent.
func runTestModel(t *testing.T, workDir string) *model {
	t.Helper()
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	t.Cleanup(orch.Shutdown)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return &model{
		ctx:       ctx,
		cancel:    cancel,
		cfg:       Config{WorkDir: workDir, Orchestrator: orch},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
}

func lastMessage(t *testing.T, m *model) string {
	t.Helper()
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("no messages were appended")
	}
	return m.chatModel.Messages[len(m.chatModel.Messages)-1].content
}

// TestHandleRunCommand_SpawnFailure checks the single-agent path reports a
// failed spawn rather than leaving the model in a half-started run state.
func TestHandleRunCommand_SpawnFailure(t *testing.T) {
	workDir := t.TempDir()
	writeRunSpec(t, workDir, "my-spec", "# My Spec\n\n## Gates\n\n- **build**: `go build ./...`\n", "")

	m := runTestModel(t, workDir)
	m.handleRunCommand([]string{"my-spec"})

	if got := lastMessage(t, m); !strings.Contains(got, "Failed to spawn task agent") {
		t.Errorf("message %q does not report the spawn failure", got)
	}
	if m.run != nil {
		t.Error("a run state was recorded despite the spawn failing")
	}
	if m.running {
		t.Error("model was left in the running state despite the spawn failing")
	}
}

// TestHandleRunCommand_ParallelSpawnFailure drives the --parallel branch, which
// splits the plan checklist across two agents. The first spawn fails, so the
// error must name agent 1 and no run state may be recorded.
func TestHandleRunCommand_ParallelSpawnFailure(t *testing.T) {
	workDir := t.TempDir()
	plan := `# Plan

- [ ] Step 1: first slice
- [ ] Step 2: second slice
- [ ] Step 3: third slice
- [ ] Step 4: fourth slice
`
	writeRunSpec(t, workDir, "my-spec", "# My Spec\n", plan)

	m := runTestModel(t, workDir)
	m.handleRunCommand([]string{"my-spec", "--parallel"})

	got := lastMessage(t, m)
	if !strings.Contains(got, "Failed to spawn agent 1") {
		t.Errorf("message %q does not report which parallel agent failed", got)
	}
	if m.run != nil {
		t.Error("a run state was recorded despite the spawn failing")
	}
}

// TestHandleRunCommand_ParallelFallsBackToSingleAgent guards the guard: with
// fewer than two checklist steps there is nothing to split, so --parallel must
// take the single-agent path instead of spawning two agents over one slice.
func TestHandleRunCommand_ParallelFallsBackToSingleAgent(t *testing.T) {
	workDir := t.TempDir()
	writeRunSpec(t, workDir, "my-spec", "# My Spec\n", "# Plan\n\n- [ ] Step 1: the only slice\n")

	m := runTestModel(t, workDir)
	m.handleRunCommand([]string{"my-spec", "--parallel"})

	if got := lastMessage(t, m); !strings.Contains(got, "Failed to spawn task agent") {
		t.Errorf("message %q suggests the parallel path ran with a single slice", got)
	}
}
