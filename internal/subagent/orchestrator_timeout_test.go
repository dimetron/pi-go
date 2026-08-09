package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
)

// captureSpawnOpts installs an ACP session constructor that records the
// SpawnOpts it is handed and then immediately completes the session. It
// returns a getter for the captured opts, which is safe to call once the
// event stream has drained.
func captureSpawnOpts(t *testing.T) func() SpawnOpts {
	t.Helper()

	var (
		mu     sync.Mutex
		opts   SpawnOpts
		called bool
	)

	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, agentName, _ string, o SpawnOpts) (acpSession, error) {
		mu.Lock()
		opts, called = o, true
		mu.Unlock()

		s := newFakeACPSession()
		go s.finish(sharedacp.RunResult{
			Status:    sharedacp.StatusSuccess,
			Result:    agentName + " ok",
			SessionID: agentName + "-sess",
		})
		return s, nil
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	return func() SpawnOpts {
		mu.Lock()
		defer mu.Unlock()
		if !called {
			t.Fatal("ACP session was never started, so no SpawnOpts were captured")
		}
		return opts
	}
}

// TestSpawn_ResolvesTimeout pins the precedence rule the spawn path applies:
// an explicit per-spawn timeout wins over the one declared by the agent
// definition, and anything else falls back to the agent's own value. This is
// what lets a caller such as `/run` give a task agent an hour without editing
// the bundled agent's frontmatter.
func TestSpawn_ResolvesTimeout(t *testing.T) {
	const hourMS = int(time.Hour / time.Millisecond)

	tests := []struct {
		name         string
		agentTimeout int
		inputTimeout int
		want         int
	}{
		{"explicit timeout overrides the agent definition", 5_000, hourMS, hourMS},
		{"unset timeout falls back to the agent definition", 5_000, 0, 5_000},
		{"negative timeout is ignored", 5_000, -1, 5_000},
		{"explicit timeout applies when the agent declares none", 0, 60_000, 60_000},
		{"neither set leaves the spawner on its defaults", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOpts := captureSpawnOpts(t)

			orch := NewOrchestrator(testConfig(), "", nil)
			t.Cleanup(orch.Shutdown)

			events, _, err := orch.Spawn(context.Background(), SpawnInput{
				Agent:   AgentConfig{Name: "claude", Role: "smol", Timeout: tt.agentTimeout},
				Prompt:  "go",
				Timeout: tt.inputTimeout,
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			for range events { //nolint:revive // drain the stream so the agent finishes
			}

			if got := gotOpts().Timeout; got != tt.want {
				t.Errorf("SpawnOpts.Timeout = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestSpawn_InputTimeoutIsEnforced is the behavioral half of the rule above:
// the overriding value is not merely passed along, it is the deadline the
// subagent actually dies on. The agent definition asks for 30s; the spawn asks
// for 300ms; the agent must be stopped on the shorter one.
func TestSpawn_InputTimeoutIsEnforced(t *testing.T) {
	// Inactivity must not be what fires — the fake agent talks constantly, so
	// only the absolute cap can stop it.
	t.Setenv("PI_SUBAGENT_INACTIVITY_MS", "20000")

	orch := NewOrchestrator(testConfig(), "", nil)
	t.Cleanup(orch.Shutdown)
	orch.spawner = NewSpawner(mockPiScript(t, `
while true; do
  printf '{"type":"text_delta","delta":"x"}\n'
  sleep 0.05
done
`))

	start := time.Now()
	events, agentID, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:   AgentConfig{Name: "worker", Role: "smol", Timeout: 30_000},
		Prompt:  "go",
		Timeout: 300,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	for range events { //nolint:revive // drain until the agent is killed
	}
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Errorf("agent ran for %v — the 30s agent timeout fired, not the 300ms override", elapsed)
	}

	// The orchestrator must report the kill as a timeout rather than as a
	// generic signal death: the spawner SIGKILLs the group, so without the
	// explicit ErrSubagentTimeout check this would surface as "killed".
	st, ok := orch.Get(agentID)
	if !ok {
		t.Fatalf("agent %s not tracked after completion", agentID)
	}
	if st.Status != "timeout" {
		t.Errorf("status = %q, want %q", st.Status, "timeout")
	}
}

// TestSpawnWithInput_CarriesTimeout covers the legacy AgentInput path, which
// has to forward the timeout through ToSpawnInput or the override is silently
// dropped for every caller still on the old API — including `/run`.
func TestSpawnWithInput_CarriesTimeout(t *testing.T) {
	in := AgentInput{
		Type:    "claude",
		Prompt:  "go",
		Timeout: 90_000,
	}

	spawnIn, err := in.ToSpawnInput()
	if err != nil {
		t.Fatalf("ToSpawnInput: %v", err)
	}
	if spawnIn.Timeout != in.Timeout {
		t.Fatalf("SpawnInput.Timeout = %d, want %d", spawnIn.Timeout, in.Timeout)
	}

	gotOpts := captureSpawnOpts(t)
	orch := NewOrchestrator(testConfig(), "", nil)
	t.Cleanup(orch.Shutdown)

	events, _, err := orch.SpawnWithInput(context.Background(), in)
	if err != nil {
		t.Fatalf("SpawnWithInput: %v", err)
	}
	for range events { //nolint:revive // drain the stream so the agent finishes
	}

	if got := gotOpts().Timeout; got != in.Timeout {
		t.Errorf("SpawnOpts.Timeout = %d, want %d", got, in.Timeout)
	}
}

// TestSpawn_ShutdownDuringSetup covers the race the Spawn path guards against:
// the orchestrator is shut down after the process has been launched but before
// its state is registered. The half-started process must be canceled and its
// pool slot returned, not leaked.
func TestSpawn_ShutdownDuringSetup(t *testing.T) {
	orch := NewOrchestrator(testConfig(), "", nil)
	before := orch.pool.Available()

	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, agentName, _ string, _ SpawnOpts) (acpSession, error) {
		// Shut down while Spawn is still wiring the process up.
		orch.Shutdown()

		s := newFakeACPSession()
		go s.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: agentName + " ok"})
		return s, nil
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:  AgentConfig{Name: "claude", Role: "smol"},
		Prompt: "go",
	})
	if err == nil {
		t.Fatal("Spawn succeeded against a shut-down orchestrator")
	}
	if !strings.Contains(err.Error(), "shut down") {
		t.Errorf("error %q does not say the orchestrator is shut down", err)
	}
	if got := orch.pool.Available(); got != before {
		t.Errorf("pool slots available = %d, want %d — a slot was leaked", got, before)
	}
}

// TestSpawn_LaunchFailureCleansUpWorktree checks the other unwind path: the
// process never starts, so the worktree created for it and the pool slot held
// for it both have to go back.
func TestSpawn_LaunchFailureCleansUpWorktree(t *testing.T) {
	repo := initTestRepo(t)
	orch := NewOrchestrator(testConfig(), repo, nil)
	t.Cleanup(orch.Shutdown)
	before := orch.pool.Available()

	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, _, _ string, _ SpawnOpts) (acpSession, error) {
		return nil, fmt.Errorf("adapter binary not found")
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	useWorktree := true
	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:    AgentConfig{Name: "claude", Role: "smol"},
		Prompt:   "go",
		Worktree: &useWorktree,
	})
	if err == nil {
		t.Fatal("Spawn succeeded despite the adapter failing to launch")
	}
	if !strings.Contains(err.Error(), "adapter binary not found") {
		t.Errorf("error %q loses the underlying launch failure", err)
	}
	if got := orch.pool.Available(); got != before {
		t.Errorf("pool slots available = %d, want %d — a slot was leaked", got, before)
	}
	if len(orch.List()) != 0 {
		t.Errorf("failed spawn left %d agents tracked, want 0", len(orch.List()))
	}
}

// TestEvictCompletedAgents_DropsOldestFirst covers the bookkeeping that keeps
// a long session's agent map from growing without bound: once finished agents
// exceed the cap, the oldest are dropped and running ones are never touched.
func TestEvictCompletedAgents_DropsOldestFirst(t *testing.T) {
	orch := NewOrchestrator(testConfig(), "", nil)

	const extra = 5
	base := time.Now().Add(-time.Hour)

	// Finished agents, oldest first: finished-0 is the oldest.
	for i := range maxCompletedAgents + extra {
		id := fmt.Sprintf("finished-%02d", i)
		orch.agents[id] = &agentState{
			ID:         id,
			Type:       "worker",
			Status:     "completed",
			StartedAt:  base,
			FinishedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	// Plus one still running, which must survive regardless of age.
	orch.agents["running-0"] = &agentState{
		ID:        "running-0",
		Type:      "worker",
		Status:    "running",
		StartedAt: base,
	}

	orch.mu.Lock()
	orch.evictCompletedAgentsLocked()
	orch.mu.Unlock()

	if _, ok := orch.agents["running-0"]; !ok {
		t.Error("a running agent was evicted")
	}
	if got := len(orch.agents); got != maxCompletedAgents+1 {
		t.Errorf("tracked agents = %d, want %d", got, maxCompletedAgents+1)
	}
	for i := range extra {
		id := fmt.Sprintf("finished-%02d", i)
		if _, ok := orch.agents[id]; ok {
			t.Errorf("%s is the %d-oldest finished agent and should have been evicted", id, i+1)
		}
	}
	newest := fmt.Sprintf("finished-%02d", maxCompletedAgents+extra-1)
	if _, ok := orch.agents[newest]; !ok {
		t.Errorf("%s is the newest finished agent and should have been kept", newest)
	}
}

// TestEvictCompletedAgents_UnderCapKeepsEverything guards the early return:
// eviction must be a no-op until the cap is actually exceeded.
func TestEvictCompletedAgents_UnderCapKeepsEverything(t *testing.T) {
	orch := NewOrchestrator(testConfig(), "", nil)

	for i := range maxCompletedAgents {
		id := fmt.Sprintf("finished-%02d", i)
		orch.agents[id] = &agentState{ID: id, Type: "worker", Status: "completed", FinishedAt: time.Now()}
	}

	orch.mu.Lock()
	orch.evictCompletedAgentsLocked()
	orch.mu.Unlock()

	if got := len(orch.agents); got != maxCompletedAgents {
		t.Errorf("tracked agents = %d, want %d — evicted below the cap", got, maxCompletedAgents)
	}
}

// TestSpawn_RejectedAfterShutdown is the plain case: once shut down, the
// orchestrator refuses new work outright.
func TestSpawn_RejectedAfterShutdown(t *testing.T) {
	orch := NewOrchestrator(testConfig(), "", nil)
	orch.Shutdown()

	_, _, err := orch.Spawn(context.Background(), SpawnInput{
		Agent:  AgentConfig{Name: "worker", Role: "smol"},
		Prompt: "go",
	})
	if err == nil {
		t.Fatal("Spawn succeeded after Shutdown")
	}
	if !strings.Contains(err.Error(), "shut down") {
		t.Errorf("error %q does not say the orchestrator is shut down", err)
	}
}
