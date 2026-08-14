package piagent_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDeps are packages piagent must never reach.
//
// The point of this package is that the agent is separable from how its model
// was built. That separation is only real if the import graph enforces it: one
// convenience that resolves a provider, looks up a key or picks an endpoint
// would put provider handling on this package's public surface, and every
// future change to providers would become a breaking change here.
//
// pimodels is on the list from the other direction. The two packages are
// siblings that compose in the embedder's code; if either imports the other
// they are one package with extra steps.
var forbiddenDeps = []string{
	"github.com/dimetron/pi-go/internal/provider",
	"github.com/dimetron/pi-go/internal/guardrail",
	"github.com/dimetron/pi-go/pimodels",
}

// deps returns the transitive import set of the piagent package.
func deps(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "github.com/dimetron/pi-go/piagent").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	set := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		set[strings.TrimSpace(line)] = true
	}
	return set
}

// TestPiagentStaysIsolated fails if the package acquires a dependency on
// provider construction. It is deliberately a build-graph assertion rather
// than a convention in a doc comment, because a doc comment does not fail CI.
func TestPiagentStaysIsolated(t *testing.T) {
	got := deps(t)
	for _, forbidden := range forbiddenDeps {
		if got[forbidden] {
			t.Errorf("piagent imports %s — the model arrives through WithModel as an ADK model.LLM; "+
				"if a caller needs both halves, they compose in the caller, not here", forbidden)
		}
	}
}

// TestPiagentDependsOnADKModel pins the other half of the contract: the agent
// consumes the ADK interface the models package returns, so the two halves
// meet at a type neither of them owns.
func TestPiagentDependsOnADKModel(t *testing.T) {
	if !deps(t)["google.golang.org/adk/v2/model"] {
		t.Fatal("piagent no longer depends on ADK's model package; " +
			"WithModel must keep taking model.LLM or embedders cannot pass one in")
	}
}
