package exec_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/sop/exec"
)

// script drives the stages: routes[stage] is the route each activation of that
// stage returns, consumed one per activation so a stage can answer differently
// the second time round the loop. A stage with no entry takes the forward path.
type script struct {
	routes  map[string][]string
	visited []string
}

func (s *script) run(_ context.Context, req exec.StageRequest) (exec.StageOutcome, error) {
	id := req.Stage.ID
	if req.Review != nil {
		id += ".review"
	}
	s.visited = append(s.visited, id)

	route := ""
	if queued := s.routes[id]; len(queued) > 0 {
		route = queued[0]
		s.routes[id] = queued[1:]
	}
	return exec.StageOutcome{Route: route, Output: id}, nil
}

// execute runs the named embedded SOP end to end through a real ADK runner and
// returns the stages that were activated, in order.
func execute(t *testing.T, name string, s *script) ([]string, error) {
	t.Helper()

	def, err := sop.LoadEmbeddedDefinition(name)
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition: %v", err)
	}
	ag, _, err := exec.Agent(def, exec.NewFactory(exec.RunnerFunc(s.run)))
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}

	r, err := runner.New(runner.Config{
		AppName:           "pi-sop-test",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}

	var runErr error
	for _, err := range r.Run(context.Background(), "u", "sess-"+name,
		genai.NewContentFromText("start", genai.RoleUser), adkagent.RunConfig{}) {
		if err != nil {
			runErr = err
			break
		}
	}
	return s.visited, runErr
}

// The happy path: the graph walks the forward edges and the verifier's PASS
// routes to merge. This is the proof that a compiled SOP executes at all.
func TestRunSOPExecutesHappyPath(t *testing.T) {
	s := &script{routes: map[string][]string{"verify": {"PASS"}}}
	visited, err := execute(t, "run", s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []string{"validate_spec", "slices", "gates", "verify", "merge", "summary"}
	if strings.Join(visited, ",") != strings.Join(want, ",") {
		t.Errorf("visited = %v, want %v", visited, want)
	}
}

// The regression this whole design turned on: because the engine fires every
// unconditional edge regardless of routing, a FAIL used to schedule the repair
// stage AND the forward successor. A failing `slices` must reach `repair` and
// must NOT reach `gates`.
func TestFailRouteDoesNotAlsoTakeForwardEdge(t *testing.T) {
	s := &script{routes: map[string][]string{"slices": {"FAIL"}}}
	visited, err := execute(t, "run", s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !contains(visited, "repair") {
		t.Errorf("FAIL did not reach repair: %v", visited)
	}
	if contains(visited, "gates") {
		t.Errorf("FAIL also took the forward edge to gates: %v", visited)
	}
}

// A gate failure repairs, re-verifies, and merges — the loop the prose SOP
// could only ask for.
func TestGateFailureRepairsThenMerges(t *testing.T) {
	s := &script{routes: map[string][]string{
		"gates":  {"FAIL"},
		"repair": {sop.RecheckSignal},
		"verify": {"PASS"},
	}}
	visited, err := execute(t, "run", s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []string{"validate_spec", "slices", "gates", "repair", "verify", "merge", "summary"}
	if strings.Join(visited, ",") != strings.Join(want, ",") {
		t.Errorf("visited = %v, want %v", visited, want)
	}
}

// Nothing in the engine bounds a conditional cycle, so the factory must. A
// verifier that never passes has to stop the run rather than spin.
func TestCycleBudgetStopsANonConvergingLoop(t *testing.T) {
	always := func(route string) []string {
		out := make([]string, 40)
		for i := range out {
			out[i] = route
		}
		return out
	}
	s := &script{routes: map[string][]string{
		"gates":  always("FAIL"),
		"repair": always(sop.RecheckSignal),
		"verify": always("FAIL"),
	}}

	visited, err := execute(t, "run", s)
	if err == nil {
		t.Fatalf("a non-converging loop completed successfully: %v", visited)
	}

	var budget *exec.CycleBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("error = %v, want CycleBudgetError", err)
	}
	if budget.Max != 10 {
		t.Errorf("max = %d, want 10 (repair's max_cycles)", budget.Max)
	}

	repairs := 0
	for _, v := range visited {
		if v == "repair" {
			repairs++
		}
	}
	if repairs > 11 { // 10 allowed activations, the 11th is refused
		t.Errorf("repair ran %d times despite max_cycles: 10", repairs)
	}
}

// The plan SOP routes through four review checkpoints; plan.review FAIL sends
// the graph back to `plan`, PASS advances to `prompt`.
func TestPlanSOPRoutesThroughReview(t *testing.T) {
	s := &script{routes: map[string][]string{
		"plan.review": {"FAIL", "PASS"},
	}}
	visited, err := execute(t, "plan", s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, want := range []string{"clarify", "clarify.review", "research", "design", "plan", "prompt", "manifest"} {
		if !contains(visited, want) {
			t.Errorf("stage %q never ran: %v", want, visited)
		}
	}
	if n := count(visited, "plan"); n != 2 {
		t.Errorf("plan ran %d times, want 2 (one FAIL round trip): %v", n, visited)
	}
}

func contains(xs []string, want string) bool { return count(xs, want) > 0 }

func count(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}
