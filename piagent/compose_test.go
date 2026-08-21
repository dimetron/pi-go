package piagent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/piagent"
	"github.com/dimetron/pi-go/pimodels"
)

// This file is the one place the two public packages meet. It lives in the
// external piagent_test package deliberately: an embedder imports both, but
// neither production package imports the other, and the isolation guards in
// each package assert exactly that on the build graph. `go list -deps` does not
// follow test files, so this composition cannot weaken either guard — a fact
// TestComposeDoesNotWeakenIsolation pins rather than assumes.

// TestComposeModelIntoAgent is the quickstart from both READMEs, executed.
// If this breaks, the documented two-line embed is wrong, and no amount of
// per-package coverage would have caught it.
func TestComposeModelIntoAgent(t *testing.T) {
	ctx := context.Background()

	// A local Ollama endpoint resolves and constructs without a credential and
	// without reaching the network, which keeps this about composition rather
	// than about a vendor being up.
	m, err := pimodels.New(ctx, "ollama/gemma4:e4b", "",
		pimodels.WithBaseURL("http://127.0.0.1:11434"))
	if err != nil {
		t.Fatalf("pimodels.New: %v", err)
	}

	a, err := piagent.New(ctx,
		piagent.WithModel(m),
		piagent.WithWorkingDir(t.TempDir()),
		// Off by default already; stated here because a test that silently
		// depended on the user's real stores would be a bad test.
		piagent.WithMemory(false),
		piagent.WithPalace(false),
	)
	if err != nil {
		t.Fatalf("piagent.New with a pimodels model: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if len(a.Tools()) == 0 {
		t.Error("composed agent has no tools; the core tool set did not reach it")
	}
	if a.Model() == "" {
		t.Error("composed agent reports no model name")
	}
}

// TestComposeProviderNamerFlowsThrough pins the structural contract between the
// packages: pimodels attaches the provider to the model, and anything holding
// that model can read it without importing pimodels. This is what let piagent
// delete its own model-name prefix table.
func TestComposeProviderNamerFlowsThrough(t *testing.T) {
	m, err := pimodels.New(context.Background(), "ollama/gemma4:e4b", "",
		pimodels.WithBaseURL("http://127.0.0.1:11434"))
	if err != nil {
		t.Fatalf("pimodels.New: %v", err)
	}

	// Asserted as a bare shape, exactly as a third-party consumer would — never
	// as pimodels.ProviderNamer, which would reverse the dependency direction.
	p, ok := m.(interface{ Provider() string })
	if !ok {
		t.Fatal("a pimodels model no longer answers Provider(); every consumer " +
			"falls back to its own prefix table, which is the duplication this removed")
	}
	if got := p.Provider(); got != "ollama" {
		t.Fatalf("Provider() = %q, want %q", got, "ollama")
	}
}

// TestAgentWithoutModelNamesTheOtherPackage pins the error that carries a user
// from one package to the other. It is the only guidance an embedder gets when
// they call New with no model, so it has to name the fix.
func TestAgentWithoutModelNamesTheOtherPackage(t *testing.T) {
	_, err := piagent.New(context.Background(), piagent.WithWorkingDir(t.TempDir()))
	if err == nil {
		t.Fatal("piagent.New with no model returned no error; the model is required")
	}
	if !errors.Is(err, piagent.ErrNoModel) {
		t.Fatalf("error is not ErrNoModel, so errors.Is cannot match it: %v", err)
	}
	for _, want := range []string{"WithModel", "pimodels"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ErrNoModel message does not mention %q, leaving the embedder without a next step: %v", want, err)
		}
	}
}
