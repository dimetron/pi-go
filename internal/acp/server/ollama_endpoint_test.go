package server

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
)

// buildSessionLLM used to read OLLAMA_API_KEY as a destination: a key exported
// for some :cloud model sent every locally pulled model to api.ollama.com,
// which 404s for a name only that machine has. Routing follows the model's tag
// here exactly as it does in the CLI, and the local-daemon health check must
// not run against the cloud endpoint.
func TestBuildSessionLLM_OllamaRoutesByTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
	t.Setenv("OLLAMA_HOST", "")

	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "deepseek-v4-flash:0731-cloud", Provider: "ollama"},
		},
	}

	llm, _, err := buildSessionLLM(context.Background(), RuntimeConfig{}, cfg)
	if err != nil {
		t.Fatalf("buildSessionLLM: %v", err)
	}
	if llm == nil {
		t.Fatal("nil LLM")
	}
	if got := llm.Name(); got != "deepseek-v4-flash:0731-cloud" {
		t.Errorf("Name() = %q, want the model name unchanged", got)
	}
}

// With no key and a local endpoint, the daemon has to answer. Pointing
// OLLAMA_HOST at a closed port makes the failure deterministic rather than
// depending on whether a daemon happens to be running on the test machine.
func TestBuildSessionLLM_OllamaHealthCheckFailureIsReported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")

	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "ollama/qwen3.8:27b-mlx", Provider: "ollama"},
		},
	}

	_, _, err := buildSessionLLM(context.Background(), RuntimeConfig{}, cfg)
	if err == nil {
		t.Fatal("expected an error when the daemon is unreachable")
	}
	if !strings.Contains(err.Error(), "ollama health check") {
		t.Errorf("err = %v, want it to name the health check", err)
	}
}
