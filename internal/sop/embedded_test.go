package sop

import (
	"strings"
	"testing"
)

// The TUI renders the compiled graph and maps run phases onto stage ids. If a
// SOP edit renames or drops a stage, the sidebar would silently highlight
// nothing — so the embedded id set is pinned here. Updating a SOP means
// updating this test and the renderer together, on purpose.
func TestEmbeddedStageIDsArePinned(t *testing.T) {
	want := map[string][]string{
		"run":  {"validate_spec", "slices", "gates", "verify", "repair", "merge", "summary"},
		"plan": {"clarify", "research", "design", "outline", "plan", "prompt", "manifest"},
	}

	for name, ids := range want {
		t.Run(name, func(t *testing.T) {
			def, err := LoadEmbeddedDefinition(name)
			if err != nil {
				t.Fatalf("LoadEmbeddedDefinition(%q): %v", name, err)
			}
			compiled, err := Compile(def, DescribeFactory{})
			if err != nil {
				t.Fatalf("Compile(%q): %v", name, err)
			}

			var got []string
			for _, id := range compiled.Order {
				if !strings.HasSuffix(id, ".review") {
					got = append(got, id)
				}
			}
			if len(got) != len(ids) {
				t.Fatalf("stage ids = %v, want %v", got, ids)
			}
			for i, id := range ids {
				if got[i] != id {
					t.Errorf("stage %d = %q, want %q", i, got[i], id)
				}
			}
		})
	}
}

// Three of the plan SOP's four checkpoints are human approvals. They are what
// turns "Approve the design?" from a sentence the model may skip into a
// scheduler state, so their kind is pinned rather than left to a later edit.
func TestPlanReviewKinds(t *testing.T) {
	def, err := LoadEmbeddedDefinition("plan")
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition: %v", err)
	}

	want := map[string]string{
		"clarify": "human",
		"design":  "human",
		"outline": "human",
		"plan":    "agent",
	}

	got := map[string]string{}
	for _, s := range def.AllStages() {
		if s.Review != nil {
			got[s.ID] = s.Review.Kind
		}
	}

	if len(got) != len(want) {
		t.Fatalf("stages with reviews = %v, want %v", got, want)
	}
	for id, kind := range want {
		if got[id] != kind {
			t.Errorf("%s review kind = %q, want %q", id, got[id], kind)
		}
	}
}

// The run SOP has no review checkpoints: its verdict rides the verify stage's
// routes instead.
func TestRunSOPHasNoReviews(t *testing.T) {
	def, err := LoadEmbeddedDefinition("run")
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition: %v", err)
	}
	for _, s := range def.AllStages() {
		if s.Review != nil {
			t.Errorf("stage %q unexpectedly declares a review", s.ID)
		}
	}
}

// The engine fires every unconditional edge regardless of routing, so a stage
// that can fail over must not also carry an unconditional forward edge — it
// would schedule the successor and the repair stage at once on a failure.
func TestFailoverStagesHaveNoUnconditionalForwardEdge(t *testing.T) {
	for _, name := range []string{"run", "plan"} {
		t.Run(name, func(t *testing.T) {
			def, err := LoadEmbeddedDefinition(name)
			if err != nil {
				t.Fatalf("LoadEmbeddedDefinition: %v", err)
			}
			compiled, err := Compile(def, DescribeFactory{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			failsOver := map[string]bool{}
			for _, s := range def.AllStages() {
				if failoverTarget(s) != "" {
					failsOver[s.ID] = true
				}
			}

			for _, e := range compiled.Edges {
				if e.Route == nil && failsOver[e.From.Name()] {
					t.Errorf("%s -> %s is unconditional, but %s declares a failover target",
						e.From.Name(), e.To.Name(), e.From.Name())
				}
			}
		})
	}
}
