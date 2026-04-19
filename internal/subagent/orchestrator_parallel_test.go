package subagent

import (
	"context"
	"sync"
	"testing"
	"time"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
)

// TestOrchestrator_ParallelSpawn_PiAndACPAgents drives the full mixed spawn
// path: a pi-backed worker plus the three ACP-backed adapters (claude,
// gemini, cursor) launched in parallel. Each subagent simulates ~250ms of
// work; the test asserts that all four are concurrently in the "running"
// state mid-flight, then that all four complete and the events stream is
// terminated cleanly. This covers the dispatchACP wiring end-to-end without
// requiring the real claude-agent-acp / gemini / cursor-agent binaries.
func TestOrchestrator_ParallelSpawn_PiAndACPAgents(t *testing.T) {
	const workDuration = 250 * time.Millisecond

	// 1. Mocked pi binary that sleeps then emits a single message.
	piScript := `
sleep 0.25
echo '{"type":"message_start","agent":"worker","role":"model"}'
echo '{"type":"text_delta","delta":"pi-worker done"}'
echo '{"type":"message_end"}'
`
	piBinary := mockPiScript(t, piScript)

	// 2. Track every fake ACP session we create so we can finish them
	//    after the same artificial delay.
	var (
		sessMu     sync.Mutex
		sessions   []*fakeACPSession
		sessAgents []string
	)
	prevStart := startACPSessionFn
	startACPSessionFn = func(_ context.Context, agentName, _ string, _ SpawnOpts) (acpSession, error) {
		s := newFakeACPSession()
		sessMu.Lock()
		sessions = append(sessions, s)
		sessAgents = append(sessAgents, agentName)
		sessMu.Unlock()
		// Finish the session asynchronously to mimic a real agent that
		// streams an answer after some thinking time.
		time.AfterFunc(workDuration, func() {
			s.events <- sharedacp.Event{
				Type:      sharedacp.EventTypeMessage,
				Content:   agentName + " ok",
				SessionID: agentName + "-sess",
			}
			s.finish(sharedacp.RunResult{
				Status:    sharedacp.StatusSuccess,
				Result:    agentName + " ok",
				SessionID: agentName + "-sess",
			})
		})
		return s, nil
	}
	t.Cleanup(func() { startACPSessionFn = prevStart })

	// 3. Build orchestrator with our mocked pi spawner.
	cfg := testConfig()
	orch := NewOrchestrator(cfg, "", nil)
	orch.spawner = NewSpawner(piBinary)

	// 4. Spawn worker (pi) + claude + gemini + cursor in parallel.
	agents := []AgentConfig{
		{Name: "worker", Role: "smol"},
		{Name: "claude", Role: "smol"},
		{Name: "gemini", Role: "smol"},
		{Name: "cursor", Role: "smol"},
	}

	type spawnResult struct {
		name    string
		agentID string
		events  <-chan Event
		err     error
	}
	results := make(chan spawnResult, len(agents))

	var startWG sync.WaitGroup
	startWG.Add(len(agents))
	for _, a := range agents {
		go func(a AgentConfig) {
			defer startWG.Done()
			ev, id, err := orch.Spawn(context.Background(), SpawnInput{
				Agent:  a,
				Prompt: "echo hello",
			})
			results <- spawnResult{name: a.Name, agentID: id, events: ev, err: err}
		}(a)
	}
	startWG.Wait()
	close(results)

	spawned := make(map[string]spawnResult, len(agents))
	for r := range results {
		if r.err != nil {
			t.Fatalf("spawn %q failed: %v", r.name, r.err)
		}
		spawned[r.name] = r
	}
	if len(spawned) != len(agents) {
		t.Fatalf("spawned %d agents, want %d", len(spawned), len(agents))
	}

	// 5. While work is in-flight, all four should be reported running.
	//    Give the orchestrator a brief moment to register state.
	deadline := time.Now().Add(workDuration / 2)
	var runningSnapshot []AgentStatus
	for time.Now().Before(deadline) {
		runningSnapshot = orch.List()
		runningCount := 0
		for _, s := range runningSnapshot {
			if s.Status == "running" {
				runningCount++
			}
		}
		if runningCount == len(agents) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	runningByType := map[string]bool{}
	for _, s := range runningSnapshot {
		if s.Status == "running" {
			runningByType[s.Type] = true
		}
	}
	for _, a := range agents {
		if !runningByType[a.Name] {
			t.Errorf("agent %q was not running concurrently with peers; snapshot=%+v",
				a.Name, runningSnapshot)
		}
	}

	// 6. Drain each agent's event channel and confirm message_end arrived.
	for name, r := range spawned {
		var sawEnd bool
		for ev := range r.events {
			if ev.Type == "message_end" {
				sawEnd = true
			}
		}
		if !sawEnd {
			t.Errorf("agent %q stream closed without message_end", name)
		}
	}

	// 7. After draining, every agent should be marked completed.
	finalByType := map[string]string{}
	for _, s := range orch.List() {
		finalByType[s.Type] = s.Status
	}
	for _, a := range agents {
		if got := finalByType[a.Name]; got != "completed" {
			t.Errorf("agent %q final status = %q, want completed", a.Name, got)
		}
	}

	// 8. Sanity: the ACP runner constructor was invoked once per ACP agent.
	sessMu.Lock()
	gotAgents := append([]string(nil), sessAgents...)
	sessMu.Unlock()
	want := map[string]bool{"claude": true, "gemini": true, "cursor": true}
	for _, name := range gotAgents {
		delete(want, name)
	}
	if len(want) > 0 {
		t.Errorf("ACP runner not invoked for: %v (saw %v)", want, gotAgents)
	}
}
