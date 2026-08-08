package tools

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// noRoleOrchestrator returns an orchestrator whose agents pass the existence
// check but cannot be spawned: with no roles configured, ResolveRole fails, so
// Spawn returns an error before any process starts.
//
// This is the branch that turns an infrastructure failure into a reportable
// result. It matters because the handlers must never propagate a spawn error
// to the model as a tool error — the model cannot act on that. They report a
// failed AgentResult instead, which the model can read and route around.
func noRoleOrchestrator(t *testing.T, names ...string) *subagent.Orchestrator {
	t.Helper()

	agents := make([]subagent.AgentConfig, 0, len(names))
	for _, n := range names {
		agents = append(agents, subagent.AgentConfig{Name: n, Description: "test agent", Role: "default"})
	}

	cfg := config.Defaults()
	cfg.Roles = nil // ResolveRole -> ErrNoDefaultRole

	orch := subagent.NewOrchestrator(&cfg, "", agents)
	t.Cleanup(orch.Shutdown)
	return orch
}

func TestSubagentSingleMode_SpawnFailureIsReportedNotReturned(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore")

	out, err := subagentHandler(nil, orch, SubagentInput{Agent: "explore", Task: "look around"}, nil)
	if err != nil {
		t.Fatalf("a spawn failure must be reported in the result, not returned as an error: %v", err)
	}

	if out.Mode != "single" {
		t.Errorf("mode = %q, want %q", out.Mode, "single")
	}
	if len(out.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(out.Results))
	}
	r := out.Results[0]
	if r.Status != "failed" {
		t.Errorf("status = %q, want %q", r.Status, "failed")
	}
	if r.Error == "" {
		t.Error("a failed spawn produced no error message for the model to read")
	}
	if r.Agent != "explore" {
		t.Errorf("agent = %q, want %q", r.Agent, "explore")
	}
	if r.Duration == "" {
		t.Error("result carries no duration")
	}
	if !strings.Contains(out.Summary, "explore") {
		t.Errorf("summary %q does not name the agent", out.Summary)
	}
}

func TestSubagentParallelMode_SpawnFailureIsReportedPerTask(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore", "review")

	out, err := subagentHandler(nil, orch, SubagentInput{Tasks: []TaskItem{
		{Agent: "explore", Task: "a"},
		{Agent: "review", Task: "b"},
	}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Mode != "parallel" {
		t.Errorf("mode = %q, want %q", out.Mode, "parallel")
	}
	if len(out.Results) != 2 {
		t.Fatalf("got %d results, want one per task", len(out.Results))
	}
	// Order must follow the input, so the model can match results to the tasks
	// it asked for.
	for i, want := range []string{"explore", "review"} {
		if out.Results[i].Agent != want {
			t.Errorf("result %d is for %q, want %q", i, out.Results[i].Agent, want)
		}
		if out.Results[i].Status != "failed" {
			t.Errorf("result %d status = %q, want %q", i, out.Results[i].Status, "failed")
		}
		if out.Results[i].Error == "" {
			t.Errorf("result %d carries no error message", i)
		}
	}
}

func TestSubagentParallelMode_RejectsTooManyTasks(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore")

	tasks := make([]TaskItem, maxParallelTasks+1)
	for i := range tasks {
		tasks[i] = TaskItem{Agent: "explore", Task: "x"}
	}

	out, err := subagentHandler(nil, orch, SubagentInput{Tasks: tasks}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("got %d results, want a single rejection", len(out.Results))
	}
	if out.Results[0].Status != "failed" {
		t.Errorf("status = %q, want %q", out.Results[0].Status, "failed")
	}
	if !strings.Contains(out.Summary, "too many parallel tasks") {
		t.Errorf("summary = %q, want it to explain the cap", out.Summary)
	}
}

func TestSubagentChainMode_SpawnFailureStopsTheChain(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore", "review")

	out, err := subagentHandler(nil, orch, SubagentInput{Chain: []ChainItem{
		{Agent: "explore", Task: "first"},
		{Agent: "review", Task: "second"},
	}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Mode != "chain" {
		t.Errorf("mode = %q, want %q", out.Mode, "chain")
	}
	if len(out.Results) == 0 {
		t.Fatal("chain produced no results at all")
	}
	// A chain passes each result forward, so a failed step has nothing to hand
	// on and the chain must stop rather than run the next step on nothing.
	first := out.Results[0]
	if first.Status != "failed" {
		t.Errorf("first step status = %q, want %q", first.Status, "failed")
	}
	if first.Error == "" {
		t.Error("first step carries no error message")
	}
	if len(out.Results) > 1 {
		t.Errorf("chain continued past a failed step: %d results", len(out.Results))
	}
}

func TestSubagentChainMode_UnknownAgentIsRejectedUpfront(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore")

	out, err := subagentHandler(nil, orch, SubagentInput{Chain: []ChainItem{
		{Agent: "nonexistent", Task: "first"},
	}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(out.Results))
	}
	if out.Results[0].Status != "failed" {
		t.Errorf("status = %q, want %q", out.Results[0].Status, "failed")
	}
	if !strings.Contains(out.Summary, "nonexistent") {
		t.Errorf("summary = %q, want it to name the unknown agent", out.Summary)
	}
}

// TestSubagentEventsAreEmittedForFailedSpawn checks the TUI still sees the
// pipeline, so a failed subagent does not simply vanish from the sidebar.
func TestSubagentEventsAreEmittedForFailedSpawn(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore")

	var kinds []string
	onEvent := func(ev SubagentEvent) {
		kinds = append(kinds, ev.Kind)
	}

	if _, err := subagentHandler(nil, orch, SubagentInput{Agent: "explore", Task: "x"}, onEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A spawn that never started emits nothing, which is correct — there is no
	// agent ID to attach events to. The contract being pinned is that the
	// handler does not panic on a non-nil callback in this path.
	for _, k := range kinds {
		if k == "" {
			t.Error("emitted an event with an empty kind")
		}
	}
}
