package pimodels_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDeps are packages pimodels must never reach.
//
// The point of this package is that model construction is separable from
// everything else pi-go does. That separation is only real if the import graph
// enforces it: a single convenience that reaches into the agent, the tool set
// or the CLI would make every future change to those a potential breaking
// change here, which is exactly what a public package cannot afford.
var forbiddenDeps = []string{
	"github.com/dimetron/pi-go/internal/agent",
	"github.com/dimetron/pi-go/internal/tools",
	"github.com/dimetron/pi-go/internal/cli",
	"github.com/dimetron/pi-go/internal/tui",
	"github.com/dimetron/pi-go/internal/subagent",
	"github.com/dimetron/pi-go/internal/extension",
	"github.com/dimetron/pi-go/internal/palace",
	"github.com/dimetron/pi-go/internal/memory",
	"github.com/dimetron/pi-go/internal/session",
}

// TestPimodelsStaysIsolated fails if the package acquires a dependency on the
// agent side of pi-go. It is deliberately a build-graph assertion rather than a
// convention in a doc comment, because a doc comment does not fail CI.
func TestPimodelsStaysIsolated(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/dimetron/pi-go/pimodels").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	deps := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}

	for _, forbidden := range forbiddenDeps {
		if deps[forbidden] {
			t.Errorf("pimodels imports %s — model construction must stay separable from the agent; "+
				"if a caller needs both, they compose in the caller, not here", forbidden)
		}
	}
}

// TestPimodelsDependsOnADKModel pins the other half of the contract: the
// package returns the ADK interface an agent already consumes, so the two
// halves meet at a type neither of them owns.
func TestPimodelsDependsOnADKModel(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/dimetron/pi-go/pimodels").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	if !strings.Contains(string(out), "google.golang.org/adk/v2/model") {
		t.Fatal("pimodels no longer depends on ADK's model package; " +
			"Model must stay an alias for model.LLM or embedders cannot pass it to an agent")
	}
}
