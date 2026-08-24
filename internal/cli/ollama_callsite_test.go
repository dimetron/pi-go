package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/testenv"
)

// The endpoint decision has to be the same one in every place that builds an
// Ollama client, not just in the main run path. These two callers each used to
// decide for themselves — buildSwitchedLLM read the API key as a destination,
// buildCommitMsgFunc hard-coded localhost — so both are pinned here against a
// :cloud model, which is the direction each of them used to get wrong.

func TestBuildSwitchedLLM_OllamaRoutesByTag(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
	t.Setenv("OLLAMA_HOST", "")

	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""

	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "deepseek-v4-flash:0731-cloud", Provider: "ollama"},
		},
	}

	// A cloud-tagged model must reach the cloud endpoint without a health
	// check against a local daemon that need not be running.
	llm, modelName, providerName, err := buildSwitchedLLM(
		context.Background(), cfg, guardrail.New(0), "deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatalf("buildSwitchedLLM: %v", err)
	}
	if llm == nil {
		t.Fatal("nil LLM")
	}
	if providerName != "ollama" {
		t.Errorf("provider = %q, want ollama", providerName)
	}
	if modelName != "deepseek-v4-flash:0731-cloud" {
		t.Errorf("model = %q, want the name unchanged", modelName)
	}
}

// buildCommitMsgFunc used to default every Ollama model to localhost, so a
// :cloud commit model was sent to a daemon that does not serve it. It returns
// nil rather than an error when the model cannot be reached, so the assertion
// is that a cloud tag yields a usable callback with no local daemon present.
func TestBuildCommitMsgFunc_OllamaCloudTagSkipsLocalDaemon(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
	t.Setenv("OLLAMA_HOST", "")

	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""

	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"commit": {Model: "deepseek-v4-flash:0731-cloud", Provider: "ollama"},
		},
	}

	if fn := buildCommitMsgFunc(context.Background(), cfg); fn == nil {
		t.Fatal("buildCommitMsgFunc returned nil for a cloud-tagged model; " +
			"the local-daemon health check should not run against api.ollama.com")
	}
}

// ping resolved its own endpoint too, and defaulted every Ollama model to
// localhost — so `pi ping` against a :cloud model probed a daemon that does
// not serve it. It now asks the same resolver as everything else.
func TestResolvePingTarget_OllamaRoutesByTag(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
	t.Setenv("OLLAMA_HOST", "")

	origURL, origModel := flagURL, flagModel
	t.Cleanup(func() { flagURL, flagModel = origURL, origModel })
	flagURL, flagModel = "", ""

	tests := []struct {
		name, model, want string
	}{
		{"cloud tag reaches the cloud", "deepseek-v4-flash:0731-cloud", "https://api.ollama.com"},
		{"local tag stays local", "ollama/qwen3.8:27b-mlx", "http://localhost:11434"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Roles: map[string]config.RoleConfig{
					"default": {Model: tt.model, Provider: "ollama"},
				},
			}
			target, err := resolvePingTarget(cfg)
			if err != nil {
				t.Fatalf("resolvePingTarget: %v", err)
			}
			if target.baseURL != tt.want {
				t.Errorf("baseURL = %q, want %q", target.baseURL, tt.want)
			}
		})
	}
}

// With no key and a local endpoint the daemon has to actually answer, and the
// failure has to say so. Pointing OLLAMA_HOST at a closed port makes that
// deterministic without depending on whether a daemon happens to be running.
func TestBuildRootRuntime_OllamaHealthCheckFailureIsReported(t *testing.T) {
	resetGlobalFlags(t)
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")

	flagModel = "ollama/qwen3.8:27b-mlx"
	flagMode = "print"

	_, err := buildRootRuntime(context.Background(), []string{"hi"})
	if err == nil {
		t.Fatal("expected an error when the daemon is unreachable")
	}
	if !strings.Contains(err.Error(), "ollama health check") {
		t.Errorf("err = %v, want it to name the health check", err)
	}
}

// buildCommitMsgFunc reports the same condition by returning nil, which is how
// /commit degrades to no generated message rather than failing the session.
func TestBuildCommitMsgFunc_OllamaUnreachableDaemonYieldsNil(t *testing.T) {
	resetGlobalFlags(t)
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")

	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"commit": {Model: "ollama/qwen3.8:27b-mlx", Provider: "ollama"},
		},
	}
	if fn := buildCommitMsgFunc(context.Background(), cfg); fn != nil {
		t.Error("expected nil when the local daemon is unreachable")
	}
}

// The model name buildSwitchedLLM returns is what the TUI stores and feeds
// back into the next resolve, so it has to survive a round trip. Resolve
// strips the ollama/ prefix from info.Model, and that prefix is the only part
// of the spelling that pins a cloud-tagged model to the local daemon — hand
// back the bare name and the next switch sends it to api.ollama.com instead.
func TestBuildSwitchedLLM_OllamaPrefixSurvivesRoundTrip(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
	t.Setenv("OLLAMA_HOST", "")

	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""

	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "ollama/deepseek-v4-flash:0731-cloud", Provider: "ollama"},
		},
	}

	const requested = "ollama/deepseek-v4-flash:0731-cloud"
	_, modelName, _, err := buildSwitchedLLM(context.Background(), cfg, guardrail.New(0), requested)
	if err != nil {
		t.Fatalf("buildSwitchedLLM: %v", err)
	}
	if modelName != requested {
		t.Fatalf("model = %q, want %q — the prefix must survive to be re-resolved", modelName, requested)
	}

	// The returned name, resolved again, must still route to the daemon.
	info, err := provider.Resolve(modelName)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", modelName, err)
	}
	got := provider.ResolveOllamaEndpoint(provider.OllamaRouting{
		Model:      info.Model,
		APIKey:     "sk-ollama-test",
		ForceLocal: info.LocalOllama,
	})
	if provider.IsOllamaCloudEndpoint(got) {
		t.Errorf("re-resolved endpoint = %q, want the local daemon", got)
	}
}
