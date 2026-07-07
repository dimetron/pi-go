package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dimetron/pi-go/internal/config"
)

// DefaultPoolSize is the default maximum number of concurrent subagents.
const DefaultPoolSize = 5

// recentTaskTTL is how long a completed subagent result is kept before being evicted.
const recentTaskTTL = 30 * time.Minute

// recentTask tracks a recently completed subagent result for deduplication.
type recentTask struct {
	CompletedAt time.Time
	Summary     string // short summary of the result (first line or truncated)
	Status      string // "completed", "failed"
}

// Orchestrator composes Pool, Spawner, and WorktreeManager to manage subagent lifecycle.
type Orchestrator struct {
	pool     *Pool
	spawner  *Spawner
	worktree *WorktreeManager
	cfg      *config.Config
	repoRoot string                 // git repo root; used as default WorkDir for subagents
	registry map[string]AgentConfig // agent name → config (from discovery)
	agents   map[string]*agentState
	mu       sync.Mutex
	closed   bool // set by Shutdown to reject new Spawn calls

	// recentTasks tracks completed subagent research tasks for deduplication.
	// Key is a normalized version of the task prompt.
	recentTasks   map[string]recentTask
	recentTasksMu sync.RWMutex
	pruneTicker   *time.Ticker // periodic cleanup of expired recentTasks
	pruneTickerMu sync.Mutex

	// LLM provider settings passed to child subagents
	BaseURL  string   // LLM API base URL
	Insecure bool     // Skip TLS verification
	Headers  []string // Extra HTTP headers

	// ACP event log — when set, events from ACP subagents (claude, gemini) are
	// appended as JSONL to this path.
	acpLogPath string
	acpLogMu   sync.Mutex
}

// acpAgentNames is the set of bundled agent names that are ACP subprocess
// adapters; their event streams are tee'd to the session's acp.jsonl when an
// ACP log path is configured on the orchestrator.
var acpAgentNames = map[string]struct{}{
	"claude":  {},
	"gemini":  {},
	"cursor":  {},
	"copilot": {},
}

// isACPAgent reports whether the named agent is an ACP subprocess adapter.
func isACPAgent(name string) bool {
	_, ok := acpAgentNames[name]
	return ok
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
	Status      string // "running", "completed", "failed", "canceled", "killed"
}

// NewOrchestrator creates an Orchestrator from config.
// repoRoot is the git repository root (empty string disables worktree support).
// agentConfigs are the discovered agent definitions (from DiscoverAgents + bundled).
func NewOrchestrator(cfg *config.Config, repoRoot string, agentConfigs []AgentConfig) *Orchestrator {
	var wm *WorktreeManager
	if repoRoot != "" {
		wm = NewWorktreeManager(repoRoot)
	}

	registry := make(map[string]AgentConfig, len(agentConfigs))
	for _, ac := range agentConfigs {
		registry[ac.Name] = ac
	}

	return &Orchestrator{
		pool:        NewPool(DefaultPoolSize),
		spawner:     NewSpawner(""),
		worktree:    wm,
		cfg:         cfg,
		repoRoot:    repoRoot,
		registry:    registry,
		agents:      make(map[string]*agentState),
		recentTasks: make(map[string]recentTask),
	}
}

// SetProviderOptions configures the LLM provider settings that will be passed
// to child subagents. Call this after NewOrchestrator to propagate CLI flags.
func (o *Orchestrator) SetProviderOptions(baseURL string, insecure bool, headers []string) {
	o.BaseURL = baseURL
	o.Insecure = insecure
	o.Headers = headers
}

// SetACPLogPath configures where ACP subagent events (claude, gemini) are
// captured as JSONL. Pass an empty string to disable.
func (o *Orchestrator) SetACPLogPath(path string) {
	o.acpLogMu.Lock()
	defer o.acpLogMu.Unlock()
	o.acpLogPath = path
}

// writeACPEvent appends a single event entry to the configured acp.jsonl.
// Each entry wraps the underlying Event with agent metadata and a timestamp
// so a single file can hold events from multiple ACP subagent runs.
func (o *Orchestrator) writeACPEvent(agentID, agentType string, ev Event) {
	o.acpLogMu.Lock()
	path := o.acpLogPath
	if path == "" {
		o.acpLogMu.Unlock()
		return
	}
	entry := struct {
		Timestamp time.Time `json:"timestamp"`
		AgentID   string    `json:"agent_id"`
		Agent     string    `json:"agent"`
		Event     Event     `json:"event"`
	}{time.Now(), agentID, agentType, ev}
	data, err := json.Marshal(entry)
	if err != nil {
		o.acpLogMu.Unlock()
		return
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		o.acpLogMu.Unlock()
		return
	}
	_, _ = f.Write(data)
	_ = f.Close()
	o.acpLogMu.Unlock()
}

// ensurePruneLoop starts the periodic cleanup goroutine if not already running.
// It's called lazily on first Spawn to avoid starting goroutines in tests.
func (o *Orchestrator) ensurePruneLoop() {
	o.pruneTickerMu.Lock()
	defer o.pruneTickerMu.Unlock()
	if o.pruneTicker == nil {
		o.pruneTicker = time.NewTicker(recentTaskTTL)
		go func() {
			for range o.pruneTicker.C {
				o.pruneRecentTasks()
			}
		}()
	}
}

// RegisterAgents replaces the agent registry with the given configs.
func (o *Orchestrator) RegisterAgents(configs []AgentConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.registry = make(map[string]AgentConfig, len(configs))
	for _, ac := range configs {
		o.registry[ac.Name] = ac
	}
}

// AgentNames returns the names of all registered agents.
func (o *Orchestrator) AgentNames() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	names := make([]string, 0, len(o.registry))
	for name := range o.registry {
		names = append(names, name)
	}
	return names
}

// normalizeTaskKey produces a stable lowercase key from a task prompt for deduplication.
func normalizeTaskKey(prompt string) string {
	// Normalize: lowercase, collapse whitespace, truncate to 200 chars.
	// This captures semantic similarity without triggering on minor differences.
	norm := prompt
	norm = strings.ToLower(norm)
	norm = strings.Join(strings.Fields(norm), " ")
	if len(norm) > 200 {
		norm = norm[:200]
	}
	return norm
}

// RecentTaskResult holds the result of a recently completed subagent task.
type RecentTaskResult struct {
	CompletedAt time.Time
	Summary     string
	Status      string
}

// pruneRecentTasks removes expired entries from recentTasks map.
// Called periodically or on shutdown to prevent unbounded growth.
func (o *Orchestrator) pruneRecentTasks() {
	cutoff := time.Now().Add(-recentTaskTTL)
	o.recentTasksMu.Lock()
	defer o.recentTasksMu.Unlock()
	for k, v := range o.recentTasks {
		if v.CompletedAt.Before(cutoff) {
			delete(o.recentTasks, k)
		}
	}
}

// FindRecentTask checks if a task matching the given prompt was completed recently.
// Returns the result if found and not expired, nil otherwise.
func (o *Orchestrator) FindRecentTask(prompt string) *RecentTaskResult {
	o.recentTasksMu.RLock()
	defer o.recentTasksMu.RUnlock()

	key := normalizeTaskKey(prompt)
	task, ok := o.recentTasks[key]
	if !ok {
		return nil
	}
	if time.Since(task.CompletedAt) > recentTaskTTL {
		return nil
	}
	return &RecentTaskResult{
		CompletedAt: task.CompletedAt,
		Summary:     task.Summary,
		Status:      task.Status,
	}
}

// RecordTask marks a completed subagent task for deduplication.
func (o *Orchestrator) RecordTask(prompt string, summary, status string) {
	o.recentTasksMu.Lock()
	defer o.recentTasksMu.Unlock()

	key := normalizeTaskKey(prompt)
	// Truncate summary to first line, max 200 chars.
	summary = strings.TrimSpace(summary)
	if len(summary) > 200 {
		summary = summary[:200]
	}
	o.recentTasks[key] = recentTask{
		CompletedAt: time.Now(),
		Summary:     summary,
		Status:      status,
	}
	// Note: pruneRecentTasks should be called periodically or on shutdown
}

// LookupAgent returns the AgentConfig for the given name, or an error if not found.
func (o *Orchestrator) LookupAgent(name string) (AgentConfig, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ac, ok := o.registry[name]
	if !ok {
		names := make([]string, 0, len(o.registry))
		for n := range o.registry {
			names = append(names, n)
		}
		return AgentConfig{}, fmt.Errorf("unknown agent %q; available: %v", name, names)
	}
	return ac, nil
}

// SpawnWithRetry spawns a subagent with automatic retry on crash (up to maxRetries).
// It monitors the subagent and re-spawns if the subagent crashes with status "failed" or "killed".
// Returns the final events channel, agentID, and error (nil on success).
func (o *Orchestrator) SpawnWithRetry(ctx context.Context, input SpawnInput) (<-chan Event, string, error) {
	maxRetries := input.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries > 3 {
		maxRetries = 3
	}

	var lastErr error
	var finalAgentID string
	var finalEvents <-chan Event

	for attempt := 0; attempt <= maxRetries; attempt++ {
		events, agentID, err := o.Spawn(ctx, input)
		if err != nil {
			lastErr = err
			// On spawn error, retry if we have attempts left.
			if attempt < maxRetries {
				continue
			}
			return nil, "", fmt.Errorf("spawn failed after %d attempts: %w", attempt+1, lastErr)
		}

		finalAgentID = agentID
		finalEvents = events

		// If no retries configured, return immediately.
		if maxRetries == 0 {
			return finalEvents, finalAgentID, nil
		}

		// Wait for the subagent to complete and check status.
		var status string
		for ev := range events {
			// Forward events to caller (consume the channel).
			if ev.Type == "message_end" || ev.Type == "error" {
				// Check agent state.
				o.mu.Lock()
				state := o.agents[agentID]
				if state != nil {
					status = state.Status
				}
				o.mu.Unlock()

				if status == "failed" || status == "killed" {
					// Crash detected — retry if we have attempts left.
					if attempt < maxRetries {
						break
					}
					return nil, agentID, fmt.Errorf("subagent %s crashed after %d attempts", agentID, attempt+1)
				}
				return finalEvents, finalAgentID, nil
			}
		}

		// If we broke out of the loop for retry, continue to next attempt.
		if attempt < maxRetries {
			continue
		}
		return finalEvents, finalAgentID, nil
	}

	return finalEvents, finalAgentID, lastErr
}

// Spawn starts a new subagent and returns an event channel.
// It acquires a pool slot, optionally creates a worktree, and spawns the pi process.
func (o *Orchestrator) Spawn(ctx context.Context, input SpawnInput) (<-chan Event, string, error) {
	// Start the prune loop lazily on first use
	o.ensurePruneLoop()

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
	model, _, _, _, _, err := o.cfg.ResolveRole(agent.Role)
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
	useWorktree := resolveWorktreeUsage(agent.Worktree, input.Worktree, input.WorkDir)

	workDir := o.repoRoot // default to project root so subagents run in the right directory
	if input.WorkDir != "" {
		workDir = input.WorkDir
	} else if useWorktree && o.worktree != nil {
		wtPath, err := o.worktree.Create(agentID, input.WorktreeName)
		if err != nil {
			o.pool.Release()
			return nil, "", fmt.Errorf("creating worktree: %w", err)
		}
		workDir = wtPath
	}

	// Pass repo root to subagent so its sandbox covers the full repo,
	// not just the worktree directory.
	env := input.Env
	if o.worktree != nil {
		env = append(append([]string(nil), env...), "PI_SANDBOX_ROOT="+o.worktree.RepoRoot())
		// Also pass the worktree path so subagent can normalize relative paths
		if workDir != "" {
			env = append(env, "PI_WORKTREE_ROOT="+workDir)
		}
	}

	// Build spawn options shared by the pi spawner and the ACP dispatcher.
	spawnOpts := SpawnOpts{
		AgentID:     agentID,
		Model:       model,
		WorkDir:     workDir,
		Prompt:      input.Prompt,
		Instruction: agent.Instruction,
		Timeout:     agent.Timeout,
		Env:         env,
		BaseURL:     o.BaseURL,
		Insecure:    o.Insecure,
		Headers:     o.Headers,
	}

	// ACP-bundled agents (claude/gemini/cursor) launch their own CLI binary
	// via the ACP adapter; everyone else runs as a child pi --mode json.
	var proc *Process
	if isACPAgent(agent.Name) {
		proc, err = dispatchACP(ctx, spawnOpts, agent.Name)
	} else {
		proc, err = o.spawner.Spawn(ctx, spawnOpts)
	}
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
	logACP := isACPAgent(agent.Name)
	go func() {
		defer close(events)
		defer o.pool.Release()

		for ev := range proc.Events() {
			if logACP {
				o.writeACPEvent(agentID, agent.Name, ev)
			}
			events <- ev
		}

		// Process done — update state.
		_, waitErr := proc.Wait()

		o.mu.Lock()
		if state.Status == "running" {
			// Distinguish killed-by-signal (e.g., timeout, OOM) from actual failures.
			if waitErr != nil && isKilledBySignal(waitErr) {
				state.Status = "killed"
			} else if waitErr != nil {
				state.Status = "failed"
			} else {
				state.Status = "completed"
			}
			state.FinishedAt = time.Now()
		}
		o.mu.Unlock()

		// Worktree cleanup is intentionally NOT done here. Deletion is
		// deferred to the caller (e.g. the TUI /run flow calls
		// wm.Cleanup after a successful merge) or to Shutdown via
		// CleanupAll. Auto-cleaning on process exit raced with the
		// merge step, causing "no worktree found" merge failures.
		_ = state.SkipCleanup // kept for API stability; no longer gated here
	}()

	return events, agentID, nil
}

// isKilledBySignal returns true if the error indicates the process was killed by a signal
// (e.g., SIGKILL from timeout or OOM), as opposed to an exit code failure.
func isKilledBySignal(err error) bool {
	if err == nil {
		return false
	}
	// exec.Error is returned when cmd.Wait() encounters a signal-terminated process.
	// Check the error using errors.As for proper wrapped error handling.
	if execErr, ok := errors.AsType[*exec.ExitError](err); ok {
		// ExitError with no code typically means killed by signal.
		return !execErr.Success()
	}
	// For other error types, check if the message indicates a signal.
	errStr := err.Error()
	return len(errStr) >= 6 && errStr[len(errStr)-6:] == "killed"
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

// resolveWorktreeUsage determines whether a worktree should be created for a spawn.
func resolveWorktreeUsage(agentDefault bool, inputOverride *bool, workDir string) bool {
	if workDir != "" {
		return false // explicit work dir — no worktree
	}
	if inputOverride != nil {
		return *inputOverride
	}
	return agentDefault
}

// validateAgentForCancel checks whether an agent can be canceled.
func validateAgentForCancel(state *agentState, agentID string) error {
	if state == nil {
		return fmt.Errorf("agent %q not found", agentID)
	}
	if state.Status != "running" {
		return fmt.Errorf("agent %q is not running (status: %s)", agentID, state.Status)
	}
	return nil
}

// Cancel cancels a running agent by ID.
func (o *Orchestrator) Cancel(agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	state := o.agents[agentID]
	if err := validateAgentForCancel(state, agentID); err != nil {
		return err
	}

	state.Process.Cancel()
	state.Status = "canceled"
	state.FinishedAt = time.Now()

	return nil
}

// Worktree returns the WorktreeManager (may be nil if worktrees are disabled).
func (o *Orchestrator) Worktree() *WorktreeManager {
	return o.worktree
}

// Shutdown cancels all running agents and cleans up worktrees.
// For production use, prefer ShutdownWithTimeout.
func (o *Orchestrator) Shutdown() {
	o.ShutdownWithTimeout(5 * time.Second)
}

// ShutdownWithTimeout gracefully cancels all running agents with the given timeout,
// then forces cleanup of worktrees. The timeout applies to the entire shutdown
// operation, not individual agents.
func (o *Orchestrator) ShutdownWithTimeout(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Stop the prune ticker if it was started and do final cleanup
	o.pruneTickerMu.Lock()
	if o.pruneTicker != nil {
		o.pruneTicker.Stop()
	}
	o.pruneTickerMu.Unlock()
	o.pruneRecentTasks()

	// First: graceful cancellation of running agents
	var hadRunning bool
	o.mu.Lock()
	o.closed = true
	for _, state := range o.agents {
		if state.Status == "running" {
			state.Process.Cancel()
			state.Status = "canceled"
			state.FinishedAt = time.Now()
			hadRunning = true
		}
	}
	o.mu.Unlock()

	// Only wait for the graceful timeout if we actually canceled something.
	if hadRunning {
		<-ctx.Done()
	}

	// Force cleanup of worktrees
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
