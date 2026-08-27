package tui

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/exec"
)

// runEmbeddedSOP executes a SOP through a real ADK runner and returns every
// event it emitted, plus the compiled graph the sidebar draws.
func runEmbeddedSOP(t *testing.T, name string, routes map[string][]string) ([]*session.Event, *sop.Compiled) {
	t.Helper()

	def, err := sop.LoadEmbeddedDefinition(name)
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition: %v", err)
	}

	run := func(_ context.Context, req exec.StageRequest) (exec.StageOutcome, error) {
		id := req.Stage.ID
		if req.Review != nil {
			id += ".review"
		}
		route := ""
		if queued := routes[id]; len(queued) > 0 {
			route = queued[0]
			routes[id] = queued[1:]
		}
		return exec.StageOutcome{Route: route, Output: id}, nil
	}

	ag, compiled, err := exec.Agent(def, exec.NewFactory(exec.RunnerFunc(run)))
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}

	r, err := runner.New(runner.Config{
		AppName:           "pi-tui-test",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}

	var events []*session.Event
	for ev, err := range r.Run(context.Background(), "u", "sess-"+name,
		genai.NewContentFromText("start", genai.RoleUser), adkagent.RunConfig{}) {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		events = append(events, ev)
	}
	return events, compiled
}

// The whole point of the feature: what the sidebar draws is what the engine did.
// This drives a real workflow and renders the real diagram from its events.
func TestSidebarGraphFollowsRealExecution(t *testing.T) {
	events, compiled := runEmbeddedSOP(t, "run", map[string][]string{"verify": {"PASS"}})

	tracker := newStageTracker()
	var atGates string

	for _, ev := range events {
		tracker.observe(ev)
		// Snapshot the frame the user would see the moment gates begins.
		if stage, ok := exec.StageStarted(ev); ok && stage == "gates" {
			atGates = plain(sidebarGraphLines(
				compiled.Order, compiled.GraphEdges(), tracker.statuses(), 20, testSidebarStyles()))
		}
	}
	tracker.finish()

	if atGates == "" {
		t.Fatal("gates never started; the workflow did not run as expected")
	}
	for _, want := range []string{"✔ validate_spec", "✔ slices", "▶ gates", "○ verify", "○ merge"} {
		if !strings.Contains(atGates, want) {
			t.Errorf("mid-run frame missing %q:\n%s", want, atGates)
		}
	}

	final := plain(sidebarGraphLines(
		compiled.Order, compiled.GraphEdges(), tracker.statuses(), 20, testSidebarStyles()))
	for _, want := range []string{"✔ validate_spec", "✔ slices", "✔ gates", "✔ verify", "✔ merge", "✔ summary"} {
		if !strings.Contains(final, want) {
			t.Errorf("final frame missing %q:\n%s", want, final)
		}
	}
	// repair never ran, so it must still read as not started.
	if !strings.Contains(final, "○ repair") {
		t.Errorf("repair should be inactive on a clean run:\n%s", final)
	}
}

// A stage the graph loops back to reads as running again, not as still finished.
func TestStageTrackerReMarksALoopedStage(t *testing.T) {
	events, compiled := runEmbeddedSOP(t, "run", map[string][]string{
		"gates":  {"FAIL"},
		"repair": {sop.RecheckSignal},
		"verify": {"PASS"},
	})

	tracker := newStageTracker()
	for _, ev := range events {
		tracker.observe(ev)
	}
	tracker.finish()

	got := tracker.statuses()
	if got["repair"] != stageCompleted {
		t.Errorf("repair status = %v, want completed", got["repair"])
	}
	if got["verify"] != stageCompleted {
		t.Errorf("verify status = %v, want completed after the recheck", got["verify"])
	}

	final := plain(sidebarGraphLines(compiled.Order, compiled.GraphEdges(), got, 20, testSidebarStyles()))
	if !strings.Contains(final, "✔ repair") {
		t.Errorf("repair ran but is not marked complete:\n%s", final)
	}
}

// A failing run marks the stage that was running, not the whole graph.
func TestStageTrackerMarksTheFailingStage(t *testing.T) {
	tracker := newStageTracker()
	tracker.status["validate_spec"] = stageCompleted
	tracker.current = "slices"
	tracker.fail()

	if got := tracker.statuses()["slices"]; got != stageFailed {
		t.Errorf("slices status = %v, want failed", got)
	}
	if got := tracker.statuses()["validate_spec"]; got != stageCompleted {
		t.Errorf("a completed stage was disturbed by a later failure: %v", got)
	}
}
