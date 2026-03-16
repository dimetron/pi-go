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
}

// agentState tracks the runtime state of a subagent.
type agentState struct {
	ID         string
	Type       string
	Prompt     string
	StartedAt  time.Time
	FinishedAt time.Time // set when status changes from "running"
	Process    *Process
	Worktree   bool   // whether a worktree was created
	Status     string // "running", "completed", "failed", "cancelled"
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
func (o *Orchestrator) Spawn(ctx context.Context, input AgentInput) (<-chan Event, string, error) {
	// Validate agent type.
	if err := ValidateType(input.Type); err != nil {
		return nil, "", err
	}

	typeDef := AgentTypes[input.Type]

	// Resolve model for this agent type's role.
	model, _, err := o.cfg.ResolveRole(typeDef.Role)
	if err != nil {
		return nil, "", fmt.Errorf("resolving role %q for agent type %q: %w", typeDef.Role, input.Type, err)
	}

	// Acquire a pool slot.
	if err := o.pool.Acquire(ctx); err != nil {
		return nil, "", fmt.Errorf("acquiring pool slot: %w", err)
	}

	// Generate agent ID.
	agentID := fmt.Sprintf("%s-%d", input.Type, time.Now().UnixNano())

	// Determine if worktree is needed.
	useWorktree := typeDef.Worktree
	if input.Worktree != nil {
		useWorktree = *input.Worktree
	}

	workDir := ""
	if useWorktree && o.worktree != nil {
		wtPath, err := o.worktree.Create(agentID)
		if err != nil {
			o.pool.Release()
			return nil, "", fmt.Errorf("creating worktree: %w", err)
		}
		workDir = wtPath
	}

	// Spawn the process.
	proc, err := o.spawner.Spawn(ctx, SpawnOpts{
		AgentID: agentID,
		Model:   model,
		WorkDir: workDir,
		Prompt:  input.Prompt,
	})
	if err != nil {
		if useWorktree && o.worktree != nil {
			_ = o.worktree.Cleanup(agentID)
		}
		o.pool.Release()
		return nil, "", fmt.Errorf("spawning agent: %w", err)
	}

	state := &agentState{
		ID:        agentID,
		Type:      input.Type,
		Prompt:    input.Prompt,
		StartedAt: time.Now(),
		Process:   proc,
		Worktree:  useWorktree && o.worktree != nil,
		Status:    "running",
	}

	o.mu.Lock()
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

		// Cleanup worktree if needed.
		if state.Worktree && o.worktree != nil {
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

// Shutdown cancels all running agents and cleans up worktrees.
func (o *Orchestrator) Shutdown() {
	o.mu.Lock()
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
