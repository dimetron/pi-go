package subagent

import (
	"context"
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

func TestOrchestrator_Shutdown(t *testing.T) {
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)

	// Shutdown on empty orchestrator should not panic.
	orch.Shutdown()
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

func TestOrchestrator_WorktreeOverride(t *testing.T) {
	cfg := testConfig()
	repo := initTestRepo(t)
	orch := NewOrchestrator(cfg, repo, nil)
	defer orch.Shutdown()

	orch.spawner.PiBinary = "/nonexistent/pi"

	// Override worktree=false for a task type (which normally uses worktree).
	noWorktree := false
	_, _, err := orch.SpawnWithInput(context.Background(), AgentInput{
		Type:     "task",
		Prompt:   "test no worktree override",
		Worktree: &noWorktree,
	})

	if err == nil {
		t.Fatal("expected error for missing binary")
	}

	// No worktree should have been created because of the override.
	if orch.worktree.Active() != 0 {
		t.Errorf("expected 0 active worktrees with override, got %d", orch.worktree.Active())
	}
}
