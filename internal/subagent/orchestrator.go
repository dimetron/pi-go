package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/session"
)

// DefaultPoolSize is the default maximum number of concurrent subagents.
//
// Three rather than five, because the budget is per process and a spawned
// coordinator builds its own pool: with the Coordinator/Worker SOP the totals
// multiply with nesting. Measured over six hours of runs, 39% of sessions died
// on the provider's per-minute token limit at a peak of eight agents in
// flight. Raise it with PI_SUBAGENT_CONCURRENCY where the account has headroom
// — see ConcurrencyFromEnv.
const DefaultPoolSize = 3

// recentTaskTTL is how long a completed subagent result is kept before being evicted.
const recentTaskTTL = 30 * time.Minute

// maxCompletedAgents is the maximum number of completed/failed agent states
// retained in the in-memory agents map. Once this limit is exceeded, the
// oldest completed entries are evicted.
const maxCompletedAgents = 50

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
		pool:        NewPool(ConcurrencyFromEnv()),
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

// SetPiBinary overrides the binary used to spawn pi subagents.
//
// NewSpawner("") defaults to os.Executable(), which in a test harness is the
// test binary itself — spawning that would re-exec the test instead of a real
// pi worker. The eval harness resolves the real binary and points the
// orchestrator at it here.
func (o *Orchestrator) SetPiBinary(path string) {
	o.spawner.PiBinary = path
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
	maxRetries := clampRetries(input.MaxRetries)

	var lastErr error
	var finalAgentID string
	var finalEvents <-chan Event

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// The first try is attempt 0, so the last one is attempt == maxRetries
		// and the count reported in errors is attempt+1.
		lastAttempt := attempt == maxRetries

		events, agentID, err := o.Spawn(ctx, input)
		switch {
		case err != nil && lastAttempt:
			return nil, "", fmt.Errorf("spawn failed after %d attempts: %w", attempt+1, err)
		case err != nil:
			// On spawn error, retry if we have attempts left.
			lastErr = err
			continue
		}

		finalAgentID = agentID
		finalEvents = events

		// If no retries configured, return immediately — the caller gets the
		// stream undrained.
		if maxRetries == 0 {
			return finalEvents, finalAgentID, nil
		}

		// Wait for the subagent to reach a terminal event and check status.
		switch outcome := o.awaitAttemptOutcome(events, agentID); {
		case outcome == attemptHealthy, outcome == attemptSilent && lastAttempt:
			return finalEvents, finalAgentID, nil
		case lastAttempt:
			return nil, agentID, fmt.Errorf("subagent %s crashed after %d attempts", agentID, attempt+1)
		}
		// Crashed or silent with attempts left: re-spawn.
	}

	return finalEvents, finalAgentID, lastErr
}

// clampRetries pins a requested retry budget to the supported range: never
// negative, never more than three re-spawns.
func clampRetries(maxRetries int) int {
	if maxRetries < 0 {
		return 0
	}
	if maxRetries > 3 {
		return 3
	}
	return maxRetries
}

// attemptOutcome is how one SpawnWithRetry attempt ended, as observed on the
// agent's event stream.
type attemptOutcome int

const (
	// attemptHealthy: a terminal event arrived and the agent was not in a
	// crashed state — the stream belongs to the caller now.
	attemptHealthy attemptOutcome = iota
	// attemptCrashed: a terminal event arrived while the agent was tracked as
	// "failed" or "killed".
	attemptCrashed
	// attemptSilent: the stream closed without ever producing a terminal event.
	attemptSilent
)

// awaitAttemptOutcome consumes events until the first terminal one
// ("message_end" or "error") and reports how the attempt ended. Events before
// that are dropped, which is why the maxRetries==0 shortcut in SpawnWithRetry
// skips this entirely.
func (o *Orchestrator) awaitAttemptOutcome(events <-chan Event, agentID string) attemptOutcome {
	for ev := range events {
		if ev.Type != "message_end" && ev.Type != "error" {
			continue
		}
		if o.agentCrashed(agentID) {
			return attemptCrashed
		}
		return attemptHealthy
	}
	return attemptSilent
}

// agentCrashed reports whether the tracked agent is in a state worth
// re-spawning. An agent that has already been evicted from the map is not
// treated as crashed.
func (o *Orchestrator) agentCrashed(agentID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.agents[agentID]
	if state == nil {
		return false
	}
	return state.Status == "failed" || state.Status == "killed"
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
	agentID := fmt.Sprintf("%s-%d", agent.Name, uniqueNano())

	// Determine if worktree is needed.
	useWorktree := resolveWorktreeUsage(agent.Worktree, input.Worktree, input.WorkDir)

	workDir, err := o.resolveWorkDir(agentID, input, useWorktree)
	if err != nil {
		o.pool.Release()
		return nil, "", err
	}

	// An explicit spawn timeout overrides the agent definition; otherwise use
	// the timeout declared by the resolved agent.
	timeout := agent.Timeout
	if input.Timeout > 0 {
		timeout = input.Timeout
	}

	// Build spawn options shared by the pi spawner and the ACP dispatcher.
	spawnOpts := SpawnOpts{
		AgentID:     agentID,
		Model:       model,
		WorkDir:     workDir,
		Prompt:      input.Prompt,
		Instruction: agent.Instruction,
		Timeout:     timeout,
		Env:         o.spawnEnv(input.Env, workDir, attributionFor(input, agentID, workDir)),
		BaseURL:     o.BaseURL,
		Insecure:    o.Insecure,
		Headers:     o.Headers,
		LSP:         agent.LSP,
	}

	proc, err := o.dispatchSpawn(ctx, spawnOpts, agent.Name)
	if err != nil {
		o.abandonSpawn(agentID, useWorktree)
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

	if !o.trackAgent(state) {
		// Orchestrator shut down while we were setting up — clean up and bail.
		proc.Cancel()
		o.abandonSpawn(agentID, useWorktree)
		return nil, "", fmt.Errorf("orchestrator is shut down")
	}

	// Create a forwarding channel that handles cleanup on completion.
	events := make(chan Event, 64)
	// Codex subagents are logged to acp.jsonl alongside the ACP ones: the log
	// records external-CLI subagent event streams, and the Event shape is the
	// same regardless of which protocol produced it.
	logACP := isACPAgent(agent.Name) || isCodexAgent(agent.Name)
	go o.forwardAgentEvents(events, proc, state, logACP)

	return events, agentID, nil
}

// resolveWorkDir picks the directory the subagent runs in, creating its
// worktree when one is called for.
func (o *Orchestrator) resolveWorkDir(agentID string, input SpawnInput, useWorktree bool) (string, error) {
	if input.WorkDir != "" {
		return input.WorkDir, nil
	}
	if useWorktree && o.worktree != nil {
		wtPath, err := o.worktree.Create(agentID, input.WorktreeName)
		if err != nil {
			return "", fmt.Errorf("creating worktree: %w", err)
		}
		return wtPath, nil
	}
	return o.repoRoot, nil // default to project root so subagents run in the right directory
}

// spawnEnv extends the caller's env with the roots the subagent needs: the repo
// root so its sandbox covers the full repo and not just the worktree directory,
// and the worktree path so it can normalize relative paths.
// attributionFor fills in the fields the orchestrator knows — the agent's ID,
// its type, and the worktree it was given — over whatever the caller supplied.
// The caller owns the run-level fields (run ID, spec, slice, cycle) because
// only it knows them.
func attributionFor(input SpawnInput, agentID, workDir string) *session.AgentContext {
	var ctx session.AgentContext
	if input.Attribution != nil {
		ctx = *input.Attribution
	}
	if ctx.AgentID == "" {
		ctx.AgentID = agentID
	}
	if ctx.AgentType == "" {
		ctx.AgentType = input.Agent.Name
	}
	if ctx.Worktree == "" {
		ctx.Worktree = workDir
	}
	return &ctx
}

func (o *Orchestrator) spawnEnv(env []string, workDir string, attribution *session.AgentContext) []string {
	out := append([]string(nil), env...)
	if o.worktree != nil {
		out = append(out, "PI_SANDBOX_ROOT="+o.worktree.RepoRoot())
		if workDir != "" {
			out = append(out, "PI_WORKTREE_ROOT="+workDir)
		}
	}
	// Attribution rides the same channel. The child records it on its own
	// session so the run tree is recoverable from meta.json rather than by
	// grouping sessions by working directory and guessing at roles.
	out = append(out, attribution.Env()...)
	return out
}

// dispatchSpawn launches the agent through the right runner. ACP-bundled agents
// (claude/gemini/cursor/copilot) launch their own CLI binary via the ACP
// adapter; codex agents launch `codex app-server` and speak its JSON-RPC
// protocol directly; everyone else runs as a child pi --mode json.
func (o *Orchestrator) dispatchSpawn(ctx context.Context, spawnOpts SpawnOpts, agentName string) (*Process, error) {
	switch {
	case isACPAgent(agentName):
		return dispatchACP(ctx, spawnOpts, agentName)
	case isCodexAgent(agentName):
		return dispatchCodex(ctx, spawnOpts, agentName)
	default:
		return o.spawner.Spawn(ctx, spawnOpts)
	}
}

// abandonSpawn gives back the resources taken for a spawn that never reached
// the running state.
func (o *Orchestrator) abandonSpawn(agentID string, useWorktree bool) {
	if useWorktree && o.worktree != nil {
		_ = o.worktree.Cleanup(agentID)
	}
	o.pool.Release()
}

// trackAgent registers a running agent, reporting false if the orchestrator was
// shut down while the spawn was being set up.
func (o *Orchestrator) trackAgent(state *agentState) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	o.agents[state.ID] = state
	return true
}

// forwardAgentEvents republishes the process's events on out, then records the
// terminal status and publishes it as a final run_done event.
func (o *Orchestrator) forwardAgentEvents(out chan Event, proc *Process, state *agentState, logACP bool) {
	defer close(out)
	defer o.pool.Release()

	for ev := range proc.Events() {
		if logACP {
			o.writeACPEvent(state.ID, state.Type, ev)
		}
		out <- ev
	}

	// Process done — update state.
	_, waitErr := proc.Wait()

	o.mu.Lock()
	if state.Status == "running" {
		state.Status = terminalStatus(waitErr)
		state.FinishedAt = time.Now()
	}
	finalStatus := state.Status
	o.evictCompletedAgentsLocked()
	o.mu.Unlock()

	// Publish the terminal status before closing the channel. Consumers must
	// use this snapshot rather than querying the evictable status map.
	out <- Event{Type: "run_done", Status: finalStatus}

	// Worktree cleanup is intentionally NOT done here. Deletion is
	// deferred to the caller (e.g. the TUI /run flow calls
	// wm.Cleanup after a successful merge) or to Shutdown via
	// CleanupAll. Auto-cleaning on process exit raced with the
	// merge step, causing "no worktree found" merge failures.
	_ = state.SkipCleanup // kept for API stability; no longer gated here
}

// terminalStatus distinguishes a timeout from a crash from an exit-code
// failure. A timeout is reported first because it also arrives as a signal
// kill — the spawner SIGKILLs the group — and would otherwise be
// indistinguishable from an OOM.
func terminalStatus(waitErr error) string {
	switch {
	case errors.Is(waitErr, ErrSubagentTimeout):
		return "timeout"
	case waitErr != nil && isKilledBySignal(waitErr):
		return "killed"
	case waitErr != nil:
		return "failed"
	default:
		return "completed"
	}
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

// evictCompletedAgentsLocked removes the oldest completed/failed/killed/canceled
// agent states from the in-memory map when the number of non-running entries
// exceeds maxCompletedAgents. Caller must hold o.mu.
func (o *Orchestrator) evictCompletedAgentsLocked() {
	completed := 0
	for _, s := range o.agents {
		if s.Status != "running" {
			completed++
		}
	}
	if completed <= maxCompletedAgents {
		return
	}
	// Evict the oldest finished entries by FinishedAt.
	type entry struct {
		id   string
		time time.Time
	}
	finished := make([]entry, 0, completed)
	for id, s := range o.agents {
		if s.Status != "running" && !s.FinishedAt.IsZero() {
			finished = append(finished, entry{id, s.FinishedAt})
		}
	}
	// Sort by FinishedAt ascending (oldest first).
	sort.Slice(finished, func(i, j int) bool {
		return finished[i].time.Before(finished[j].time)
	})
	toRemove := completed - maxCompletedAgents
	for i := 0; i < toRemove && i < len(finished); i++ {
		delete(o.agents, finished[i].id)
	}
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

// SetStatusForTest is a test-only helper that records a synthetic
// agent state so callers can exercise post-spawn verification paths
// without spawning a real subprocess. The agent is marked "running"
// until SetStatusForTest is called with a final status. Returns false
// if the orchestrator was already shut down. Not for production use.
func (o *Orchestrator) SetStatusForTest(agentID, status string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	now := time.Now()
	o.agents[agentID] = &agentState{
		ID:         agentID,
		Type:       "test",
		StartedAt:  now,
		FinishedAt: now,
		Status:     status,
	}
	return true
}

// Get returns the status of a single tracked agent by ID.
// Returns (AgentStatus{}, false) when the agent is unknown. Useful for
// post-spawn verification (e.g. the /run flow checks exit status before
// moving on to gate validation).
func (o *Orchestrator) Get(agentID string) (AgentStatus, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	state, ok := o.agents[agentID]
	if !ok {
		return AgentStatus{}, false
	}
	dur := ""
	if state.Status != "running" && !state.FinishedAt.IsZero() {
		dur = state.FinishedAt.Sub(state.StartedAt).Truncate(time.Millisecond).String()
	}
	return AgentStatus{
		AgentID:   state.ID,
		Type:      state.Type,
		Status:    state.Status,
		Prompt:    state.Prompt,
		StartedAt: state.StartedAt,
		Duration:  dur,
	}, true
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

// Concurrency reports how many subagents this process may run at once. It is
// the pool size, which is what actually gates a spawn — not maxParallelTasks,
// which only caps how many tasks one call may name. A batch larger than this
// does not run in parallel; it queues, and the call takes proportionally
// longer.
func (o *Orchestrator) Concurrency() int {
	if o == nil || o.pool == nil {
		return 0
	}
	return o.pool.Size()
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

// lastAgentNano is the timestamp the previous agent ID was minted from.
var lastAgentNano atomic.Int64

// uniqueNano returns a strictly increasing nanosecond timestamp for agent IDs.
//
// The ID is "<name>-<nanos>", and the orchestrator keys its agent table on it,
// so two spawns minted from the same clock reading collide and the second
// silently replaces the first. On Linux that needs two spawns inside the same
// nanosecond; on Windows the monotonic clock ticks in 100ns steps -- and
// coarser still on older kernels -- so a retry after an instant crash, as
// SpawnWithRetry performs, reproduced it.
func uniqueNano() int64 {
	for {
		now := time.Now().UnixNano()
		last := lastAgentNano.Load()
		if now <= last {
			now = last + 1
		}
		if lastAgentNano.CompareAndSwap(last, now) {
			return now
		}
	}
}
