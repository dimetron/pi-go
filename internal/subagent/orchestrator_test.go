package subagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
)

func testConfig() *config.Config {
	cfg := config.Defaults()
	// Add roles for all agent types.
	cfg.Roles["smol"] = config.RoleConfig{Model: "claude-haiku"}
	cfg.Roles["slow"] = config.RoleConfig{Model: "claude-opus"}
	cfg.Roles["plan"] = config.RoleConfig{Model: "claude-sonnet"}
	return &cfg
}

func TestOrchestrator_NewOrchestrator(t *testing.T) {
	cfg := testConfig()

	// With repo root.
	orch := NewOrchestrator(cfg, "/tmp/fake-repo", nil)
	if orch.pool == nil {
		t.Fatal("pool should not be nil")
	}
	if orch.spawner == nil {
		t.Fatal("spawner should not be nil")
	}
	if orch.worktree == nil {
		t.Fatal("worktree should not be nil with repoRoot")
	}
	if orch.pool.Size() != DefaultPoolSize {
		t.Errorf("pool size = %d, want %d", orch.pool.Size(), DefaultPoolSize)
	}

	// Without repo root.
	orch2 := NewOrchestrator(cfg, "", nil)
	if orch2.worktree != nil {
		t.Fatal("worktree should be nil without repoRoot")
	}
}

func TestOrchestrator_SpawnInvalidType(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:   "nonexistent",
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid agent type")
	}
}

func TestOrchestrator_SpawnRoleResolution(t *testing.T) {
	// Config with no roles at all — should fail on role resolution.
	cfg := config.Config{} // empty, no roles
	orch := NewOrchestrator(&cfg, "", nil)

	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:   "explore",
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for missing roles")
	}
	// The error should be about role resolution.
	if !strings.Contains(err.Error(), "resolving role") {
		t.Errorf("expected role resolution error, got: %v", err)
	}
}

func TestOrchestrator_ListEmpty(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	agents := orch.List()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestOrchestrator_CancelNotFound(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	err := orch.Cancel("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestIsACPAgent(t *testing.T) {
	for _, name := range []string{"claude", "gemini", "cursor"} {
		if !isACPAgent(name) {
			t.Errorf("isACPAgent(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "explore", "task", "plan"} {
		if isACPAgent(name) {
			t.Errorf("isACPAgent(%q) = true, want false", name)
		}
	}
}

func TestOrchestrator_ACPLogPathWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acp.jsonl")

	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	orch.SetACPLogPath(path)

	orch.writeACPEvent("claude-123", "claude", Event{Type: "text_delta", Content: "hi"})
	orch.writeACPEvent("gemini-456", "gemini", Event{Type: "tool_call", Content: "read"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading acp.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], `"agent":"claude"`) || !strings.Contains(lines[0], `"agent_id":"claude-123"`) {
		t.Errorf("line 0 missing claude metadata: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"agent":"gemini"`) || !strings.Contains(lines[1], `"type":"tool_call"`) {
		t.Errorf("line 1 missing gemini/tool_call metadata: %s", lines[1])
	}
}

func TestOrchestrator_ACPLogPathDisabled(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	// No SetACPLogPath call — should be a no-op.
	orch.writeACPEvent("claude-0", "claude", Event{Type: "text_delta", Content: "noop"})
}

func TestOrchestrator_Shutdown(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// Shutdown on empty orchestrator should not panic.
	orch.Shutdown()
}

func TestOrchestrator_ShutdownWithTimeout(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// Test ShutdownWithTimeout with various timeouts.
	tests := []time.Duration{
		0, // immediate
		10 * time.Millisecond,
		100 * time.Millisecond,
		5 * time.Second,
	}

	for _, timeout := range tests {
		t.Run(timeout.String(), func(t *testing.T) {
			// ShutdownWithTimeout should not panic.
			orch.ShutdownWithTimeout(timeout)
		})
	}
}

func TestOrchestrator_ConcurrencyLimit(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// Verify pool is properly initialized.
	if orch.pool.Available() != DefaultPoolSize {
		t.Errorf("available = %d, want %d", orch.pool.Available(), DefaultPoolSize)
	}

	// Acquire all slots.
	for i := 0; i < DefaultPoolSize; i++ {
		if err := orch.pool.Acquire(context.Background()); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}

	// Next acquire should block and timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := orch.pool.Acquire(ctx)
	if err == nil {
		t.Fatal("expected timeout error when pool is full")
	}

	// Release all.
	for i := 0; i < DefaultPoolSize; i++ {
		orch.pool.Release()
	}
}

func TestOrchestrator_SpawnExploreNoWorktree(t *testing.T) {
	cfg := testConfig()
	repo := initTestRepo(t)
	orch := NewOrchestrator(cfg, repo, nil)
	defer orch.Shutdown()

	// Use a binary that won't be found — we just want to verify no worktree is created.
	orch.spawner.PiBinary = "/nonexistent/pi"

	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:   "explore",
		Prompt: "test explore",
	})

	// Should fail because binary doesn't exist.
	if err == nil {
		t.Fatal("expected error for missing binary")
	}

	// Verify no worktree was created (explore doesn't use worktree).
	if orch.worktree.Active() != 0 {
		t.Errorf("expected 0 active worktrees, got %d", orch.worktree.Active())
	}

	// Pool slot should have been released on error.
	if orch.pool.Available() != DefaultPoolSize {
		t.Errorf("pool available = %d, want %d (slot should be released)", orch.pool.Available(), DefaultPoolSize)
	}
}

func TestOrchestrator_SpawnTaskWithWorktree(t *testing.T) {
	cfg := testConfig()
	repo := initTestRepo(t)
	orch := NewOrchestrator(cfg, repo, nil)
	defer orch.Shutdown()

	// Use a binary that won't be found.
	orch.spawner.PiBinary = "/nonexistent/pi"

	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:   "task",
		Prompt: "test task",
	})

	// Should fail because binary doesn't exist, but worktree should have been created and cleaned up.
	if err == nil {
		t.Fatal("expected error for missing binary")
	}

	// Worktree should be cleaned up on spawn failure.
	if orch.worktree.Active() != 0 {
		t.Errorf("expected 0 active worktrees after failure, got %d", orch.worktree.Active())
	}
}

func TestOrchestrator_SpawnTaskWithNamedWorktree(t *testing.T) {
	cfg := testConfig()
	repo := initTestRepo(t)
	orch := NewOrchestrator(cfg, repo, nil)
	defer orch.Shutdown()

	orch.spawner.PiBinary = "/nonexistent/pi"

	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:         "task",
		Prompt:       "test task",
		WorktreeName: "my-feature",
	})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}

	if _, statErr := os.Stat(filepath.Join(repo, ".pi-go", "tasks", "my-feature")); !os.IsNotExist(statErr) {
		t.Fatalf("expected named worktree path to be cleaned up, stat err=%v", statErr)
	}

	cmd := exec.Command("git", "branch", "--list", "my-feature")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected named worktree branch to be cleaned up, got %q", string(out))
	}
}

func TestOrchestrator_WorktreeOverride(t *testing.T) {
	cfg := testConfig()
	repo := initTestRepo(t)
	orch := NewOrchestrator(cfg, repo, nil)
	defer orch.Shutdown()

	orch.spawner.PiBinary = "/nonexistent/pi"

	// Override worktree=false for a task type (which normally uses worktree).
	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:     "task",
		Prompt:   "test no worktree override",
		Worktree: new(false),
	})

	if err == nil {
		t.Fatal("expected error for missing binary")
	}

	// No worktree should have been created because of the override.
	if orch.worktree.Active() != 0 {
		t.Errorf("expected 0 active worktrees with override, got %d", orch.worktree.Active())
	}
}

func TestOrchestrator_SpawnWithTimeout(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	// Use a binary that won't be found — we just want to verify timeout is passed through.
	orch.spawner.PiBinary = "/nonexistent/pi"

	// Spawn with explicit timeout (5000ms = 5 seconds).
	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent: AgentConfig{
			Name:    "explore",
			Role:    "smol",
			Timeout: 5000, // 5 second timeout
		},
		Prompt: "test",
	})

	// Should fail because binary doesn't exist.
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestOrchestrator_SpawnWithEnv(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	// Use a binary that won't be found — we just want to verify env is passed through.
	orch.spawner.PiBinary = "/nonexistent/pi"

	// Spawn with custom env.
	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent: AgentConfig{
			Name:    "explore",
			Role:    "smol",
			Timeout: 5000,
		},
		Prompt: "test",
		Env:    []string{"TEST_VAR=value", "ANOTHER=test"},
	})

	// Should fail because binary doesn't exist.
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestOrchestrator_RegisterAgents(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	configs := []AgentConfig{
		{Name: "alpha", Role: "smol"},
		{Name: "beta", Role: "slow"},
	}
	orch.RegisterAgents(configs)

	names := orch.AgentNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 agent names, got %d", len(names))
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["alpha"] || !nameSet["beta"] {
		t.Errorf("unexpected agent names: %v", names)
	}
}

func TestOrchestrator_LookupAgent(t *testing.T) {
	cfg := testConfig()
	configs := []AgentConfig{
		{Name: "explorer", Role: "smol", Description: "Explore the codebase"},
	}
	orch := NewOrchestrator(cfg, "", configs)

	t.Run("found", func(t *testing.T) {
		ac, err := orch.LookupAgent("explorer")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ac.Name != "explorer" {
			t.Errorf("name = %q, want explorer", ac.Name)
		}
		if ac.Description != "Explore the codebase" {
			t.Errorf("description = %q", ac.Description)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := orch.LookupAgent("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
		if !strings.Contains(err.Error(), "unknown agent") {
			t.Errorf("expected 'unknown agent' in error, got: %v", err)
		}
	})
}

func TestOrchestrator_WorktreeAccessor(t *testing.T) {
	cfg := testConfig()

	t.Run("with repo root", func(t *testing.T) {
		orch := NewOrchestrator(cfg, "/tmp/fake-repo", nil)
		if orch.Worktree() == nil {
			t.Error("expected non-nil Worktree() with repo root")
		}
	})

	t.Run("without repo root", func(t *testing.T) {
		orch := NewOrchestrator(cfg, "", nil)
		if orch.Worktree() != nil {
			t.Error("expected nil Worktree() without repo root")
		}
	})
}

func TestOrchestrator_SpawnAfterShutdown(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	orch.Shutdown()

	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:  AgentConfig{Name: "test", Role: "smol"},
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error after shutdown")
	}
	if !strings.Contains(err.Error(), "shut down") {
		t.Errorf("expected shutdown error, got: %v", err)
	}
}

func TestOrchestrator_SpawnEmptyAgentName(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	defer orch.Shutdown()

	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:  AgentConfig{Name: "", Role: "smol"},
		Prompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for empty agent name")
	}
}

func TestResolveWorktreeUsage(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name          string
		agentDefault  bool
		inputOverride *bool
		workDir       string
		want          bool
	}{
		{"default true, no override", true, nil, "", true},
		{"default false, no override", false, nil, "", false},
		{"override true", false, boolPtr(true), "", true},
		{"override false", true, boolPtr(false), "", false},
		{"workDir provided, ignores all", true, boolPtr(true), "/tmp/wt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveWorktreeUsage(tt.agentDefault, tt.inputOverride, tt.workDir)
			if got != tt.want {
				t.Errorf("resolveWorktreeUsage = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeTaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello world"},
		{"  extra   spaces  ", "extra spaces"},
		{"ALLCAPS", "allcaps"},
		{"", ""},
		{strings.Repeat("a", 250), strings.Repeat("a", 200)},
	}
	for _, tt := range tests {
		got := normalizeTaskKey(tt.input)
		if got != tt.want {
			t.Errorf("normalizeTaskKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestOrchestrator_RecordAndFindTask(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// No task initially.
	if r := orch.FindRecentTask("do something"); r != nil {
		t.Fatal("expected nil for unrecorded task")
	}

	// Record a task.
	orch.RecordTask("do something", "it worked", "completed")

	// Should find it now.
	r := orch.FindRecentTask("do something")
	if r == nil {
		t.Fatal("expected to find recorded task")
	}
	if r.Summary != "it worked" {
		t.Errorf("summary = %q, want %q", r.Summary, "it worked")
	}
	if r.Status != "completed" {
		t.Errorf("status = %q, want %q", r.Status, "completed")
	}

	// Normalized key lookup should also work.
	r2 := orch.FindRecentTask("  Do   Something  ")
	if r2 == nil {
		t.Fatal("expected normalized lookup to find task")
	}

	// Record with long summary — should be truncated.
	orch.RecordTask("long task", strings.Repeat("x", 300), "completed")
	r3 := orch.FindRecentTask("long task")
	if r3 == nil {
		t.Fatal("expected to find long task")
	}
	if len(r3.Summary) > 200 {
		t.Errorf("summary should be truncated, got len %d", len(r3.Summary))
	}
}

func TestOrchestrator_FindRecentTask_Expired(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// Manually insert an expired task.
	key := normalizeTaskKey("expired task")
	orch.recentTasksMu.Lock()
	orch.recentTasks[key] = recentTask{
		CompletedAt: time.Now().Add(-2 * recentTaskTTL),
		Summary:     "old result",
		Status:      "completed",
	}
	orch.recentTasksMu.Unlock()

	if r := orch.FindRecentTask("expired task"); r != nil {
		t.Error("expected nil for expired task")
	}
}

func TestOrchestrator_ListWithAgents(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	now := time.Now()
	// Add mock agents directly.
	orch.mu.Lock()
	orch.agents["a1"] = &agentState{
		ID: "a1", Type: "explore", Prompt: "test",
		Status: "running", StartedAt: now,
	}
	orch.agents["a2"] = &agentState{
		ID: "a2", Type: "task", Prompt: "build",
		Status: "completed", StartedAt: now.Add(-time.Second), FinishedAt: now,
	}
	orch.mu.Unlock()

	statuses := orch.List()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(statuses))
	}

	// Check that the completed agent has a duration.
	for _, s := range statuses {
		if s.Status == "completed" && s.Duration == "" {
			t.Error("completed agent should have a duration")
		}
		if s.Status == "running" && s.Duration != "" {
			t.Error("running agent should not have a duration")
		}
	}
}

func TestOrchestrator_CancelRunningAgent(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	orch.mu.Lock()
	orch.agents["run1"] = &agentState{
		ID:        "run1",
		Type:      "explore",
		Prompt:    "test",
		Status:    "running",
		StartedAt: time.Now(),
		Process:   &Process{cancel: cancel, done: make(chan struct{})},
	}
	orch.mu.Unlock()

	if err := orch.Cancel("run1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Verify context was canceled.
	select {
	case <-ctx.Done():
		// ok
	default:
		t.Error("expected context to be canceled")
	}

	// Try canceling again — should fail.
	if err := orch.Cancel("run1"); err == nil {
		t.Error("expected error canceling non-running agent")
	}
}

func TestIsKilledBySignal(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"generic error", fmt.Errorf("something went wrong"), false},
		{"error ending with killed", fmt.Errorf("process killed"), true},
		{"short error", fmt.Errorf("fail"), false},
		{"error not ending with killed", fmt.Errorf("signal: terminated"), false},
		{"exactly six chars killed", fmt.Errorf("killed"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKilledBySignal(tt.err)
			if got != tt.want {
				t.Errorf("isKilledBySignal(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestValidateAgentForCancel(t *testing.T) {
	tests := []struct {
		name    string
		state   *agentState
		wantErr string
	}{
		{"nil state", nil, "not found"},
		{"completed", &agentState{Status: "completed"}, "not running"},
		{"canceled", &agentState{Status: "canceled"}, "not running"},
		{"running", &agentState{Status: "running"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAgentForCancel(tt.state, "test-agent")
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q should contain %q", err, tt.wantErr)
				}
			}
		})
	}
}

// TestGet_ReturnsSeededStatus verifies that Get returns the recorded
// status of a seeded agent and reports (zero, false) for unknown IDs.
func TestGet_ReturnsSeededStatus(t *testing.T) {
	o := NewOrchestrator(&config.Config{}, "", nil)

	if _, ok := o.Get("nope"); ok {
		t.Fatal("Get on unknown agent should return ok=false")
	}

	if !o.SetStatusForTest("task-1", "completed") {
		t.Fatal("SetStatusForTest should succeed on a fresh orchestrator")
	}
	st, ok := o.Get("task-1")
	if !ok {
		t.Fatal("Get should find seeded agent")
	}
	if st.Status != "completed" {
		t.Errorf("status = %q, want completed", st.Status)
	}
	if st.AgentID != "task-1" {
		t.Errorf("agentID = %q, want task-1", st.AgentID)
	}
	if st.Duration == "" {
		t.Error("Duration should be populated for finished agents")
	}

	if !o.SetStatusForTest("task-2", "failed") {
		t.Fatal("SetStatusForTest should succeed")
	}
	st2, _ := o.Get("task-2")
	if st2.Status != "failed" {
		t.Errorf("status = %q, want failed", st2.Status)
	}
}
