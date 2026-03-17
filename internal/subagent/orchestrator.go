package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dimetron/pi-go/internal/config"
)

// DefaultPoolSize is the default maximum number of concurrent subagents.
const DefaultPoolSize = 5

// Orchestrator composes Pool, Spawner, and WorktreeManager to manage subagent lifecycle.
type Orchestrator struct {
	pool     *Pool
	spawner  *Spawner
	worktree *WorktreeManager
	cfg      *config.Config
	agents   map[string]*agentState
	mu       sync.Mutex
	closed   bool // set by Shutdown to reject new Spawn calls
}

// agentState tracks the runtime state of a subagent.
type agentState struct {
	ID          string
	Type        string
	Prompt      string
	StartedAt   time.Time
	FinishedAt  time.Time // set when status changes from "running"
	Process     *Process
	Worktree    bool   // whether a worktree was created
	SkipCleanup bool   // don't auto-cleanup worktree on completion (for gate validation)
	Status      string // "running", "completed", "failed", "cancelled"
}

// NewOrchestrator creates an Orchestrator from config.
// repoRoot is the git repository root (empty string disables worktree support).
func NewOrchestrator(cfg *config.Config, repoRoot string) *Orchestrator {
	var wm *WorktreeManager
	if repoRoot != "" {
		wm = NewWorktreeManager(repoRoot)
	}
	return &Orchestrator{
		pool:     NewPool(DefaultPoolSize),
		spawner:  NewSpawner(""),
		worktree: wm,
		cfg:      cfg,
		agents:   make(map[string]*agentState),
	}
}

// Spawn starts a new subagent and returns an event channel.
// It acquires a pool slot, optionally creates a worktree, and spawns the pi process.
func (o *Orchestrator) Spawn(ctx context.Context, input SpawnInput) (<-chan Event, string, error) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil, "", fmt.Errorf("orchestrator is shut down")
	}
	o.mu.Unlock()

	// Validate agent config.
	agent := input.Agent
	if agent.Name == "" {
		return nil, "", fmt.Errorf("agent config must have a name")
	}

	// Resolve model for this agent's role.
	model, _, err := o.cfg.ResolveRole(agent.Role)
	if err != nil {
		return nil, "", fmt.Errorf("resolving role %q for agent %q: %w", agent.Role, agent.Name, err)
	}

	// Acquire a pool slot.
	if err := o.pool.Acquire(ctx); err != nil {
		return nil, "", fmt.Errorf("acquiring pool slot: %w", err)
	}

	// Generate agent ID.
	agentID := fmt.Sprintf("%s-%d", agent.Name, time.Now().UnixNano())

	// Determine if worktree is needed.
	useWorktree := agent.Worktree
	if input.Worktree != nil {
		useWorktree = *input.Worktree
	}

	workDir := ""
	if input.WorkDir != "" {
		// Use provided working directory (e.g. existing worktree for retry).
		workDir = input.WorkDir
		useWorktree = false // Don't create a new worktree or mark for cleanup.
	} else if useWorktree && o.worktree != nil {
		wtPath, err := o.worktree.Create(agentID)
		if err != nil {
			o.pool.Release()
			return nil, "", fmt.Errorf("creating worktree: %w", err)
		}
		workDir = wtPath
	}

	// Spawn the process.
	proc, err := o.spawner.Spawn(ctx, SpawnOpts{
		AgentID:     agentID,
		Model:       model,
		WorkDir:     workDir,
		Prompt:      input.Prompt,
		Instruction: agent.Instruction,
	})
	if err != nil {
		if useWorktree && o.worktree != nil {
			_ = o.worktree.Cleanup(agentID)
		}
		o.pool.Release()
		return nil, "", fmt.Errorf("spawning agent: %w", err)
	}

	state := &agentState{
		ID:          agentID,
		Type:        agent.Name,
		Prompt:      input.Prompt,
		StartedAt:   time.Now(),
		Process:     proc,
		Worktree:    useWorktree && o.worktree != nil,
		SkipCleanup: input.SkipCleanup,
		Status:      "running",
	}

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		// Orchestrator shut down while we were setting up — clean up and bail.
		proc.Cancel()
		if useWorktree && o.worktree != nil {
			_ = o.worktree.Cleanup(agentID)
		}
		o.pool.Release()
		return nil, "", fmt.Errorf("orchestrator is shut down")
	}
	o.agents[agentID] = state
	o.mu.Unlock()

	// Create a forwarding channel that handles cleanup on completion.
	events := make(chan Event, 64)
	go func() {
		defer close(events)
		defer o.pool.Release()

		for ev := range proc.Events() {
			events <- ev
		}

		// Process done — update state.
		_, waitErr := proc.Wait()

		o.mu.Lock()
		if state.Status == "running" {
			if waitErr != nil {
				state.Status = "failed"
			} else {
				state.Status = "completed"
			}
			state.FinishedAt = time.Now()
		}
		o.mu.Unlock()

		// Cleanup worktree if needed (skip if caller will handle it, e.g. for gate validation).
		if state.Worktree && o.worktree != nil && !state.SkipCleanup {
			_ = o.worktree.Cleanup(agentID)
		}
	}()

	return events, agentID, nil
}

// List returns the status of all tracked agents.
func (o *Orchestrator) List() []AgentStatus {
	o.mu.Lock()
	defer o.mu.Unlock()

	statuses := make([]AgentStatus, 0, len(o.agents))
	for _, s := range o.agents {
		dur := ""
		if s.Status != "running" && !s.FinishedAt.IsZero() {
			dur = s.FinishedAt.Sub(s.StartedAt).Truncate(time.Millisecond).String()
		}
		statuses = append(statuses, AgentStatus{
			AgentID:   s.ID,
			Type:      s.Type,
			Status:    s.Status,
			Prompt:    s.Prompt,
			StartedAt: s.StartedAt,
			Duration:  dur,
		})
	}
	return statuses
}

// Cancel cancels a running agent by ID.
func (o *Orchestrator) Cancel(agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	state, ok := o.agents[agentID]
	if !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	if state.Status != "running" {
		return fmt.Errorf("agent %q is not running (status: %s)", agentID, state.Status)
	}

	state.Process.Cancel()
	state.Status = "cancelled"
	state.FinishedAt = time.Now()

	return nil
}

// Worktree returns the WorktreeManager (may be nil if worktrees are disabled).
func (o *Orchestrator) Worktree() *WorktreeManager {
	return o.worktree
}

// Shutdown cancels all running agents and cleans up worktrees.
func (o *Orchestrator) Shutdown() {
	o.mu.Lock()
	o.closed = true
	for _, state := range o.agents {
		if state.Status == "running" {
			state.Process.Cancel()
			state.Status = "cancelled"
			state.FinishedAt = time.Now()
		}
	}
	o.mu.Unlock()

	if o.worktree != nil {
		_ = o.worktree.CleanupAll()
	}
}

// SpawnWithInput is the legacy method that accepts AgentInput for backward compatibility.
// It converts the input to SpawnInput and calls Spawn.
// Deprecated: Use Spawn with SpawnInput directly.
func (o *Orchestrator) SpawnWithInput(ctx context.Context, input AgentInput) (<-chan Event, string, error) {
	spawnInput, err := input.ToSpawnInput()
	if err != nil {
		return nil, "", err
	}
	return o.Spawn(ctx, spawnInput)
}
