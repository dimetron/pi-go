package cli

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/testenv"
)

// The bug this pins: OLLAMA_API_KEY exported for a :cloud model used to be read
// as a destination, so a locally pulled name like qwen3.8:27b-mlx was posted to
// api.ollama.com and came back "model not found" from a server the user never
// meant to talk to. A key never promotes a model to the cloud.
//
// The second bug this pins: ollama/ is documented as "Ollama, local", but a
// cloud-looking tag on the name used to override the prefix, so
// ollama/deepseek-v4-flash:0731-cloud went to api.ollama.com — and answered 401
// whenever no key was set, though the local daemon serves that model.
//
// Every case here exports a key, because buildRootRuntime health-checks a local
// daemon only when none is set and CI has no daemon to answer. The no-key
// fallback is pinned where the decision is actually made, in
// provider.TestResolveOllamaEndpoint.
func TestBuildRootRuntime_OllamaKeyDoesNotForceCloud(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		apiKey  string
		wantURL string
	}{
		{
			name:    "mlx tag stays on the local daemon",
			model:   "ollama/qwen3.8:27b-mlx",
			apiKey:  "sk-ollama-test",
			wantURL: "http://localhost:11434",
		},
		{
			name:    "ollama/ prefix keeps a cloud-tagged model local",
			model:   "ollama/deepseek-v4-flash:0731-cloud",
			apiKey:  "sk-ollama-test",
			wantURL: "http://localhost:11434",
		},
		{
			name:    "an unprefixed cloud tag with a key routes to the cloud",
			model:   "deepseek-v4-flash:0731-cloud",
			apiKey:  "sk-ollama-test",
			wantURL: "https://api.ollama.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalFlags(t)
			testenv.SetHome(t, t.TempDir())
			t.Setenv("OLLAMA_API_KEY", tt.apiKey)
			t.Setenv("OLLAMA_HOST", "")

			flagModel = tt.model
			flagMode = "print"

			rt, err := buildRootRuntime(context.Background(), []string{"hi"})
			if err != nil {
				t.Fatalf("buildRootRuntime: %v", err)
			}
			if rt.info.BaseURL != tt.wantURL {
				t.Errorf("endpoint = %q, want %q", rt.info.BaseURL, tt.wantURL)
			}
		})
	}
}

// An explicit OLLAMA_HOST is how someone points at another machine, a
// container, or an authenticated proxy, so it outranks the tag either way.
func TestBuildRootRuntime_OllamaHostWinsOverTag(t *testing.T) {
	resetGlobalFlags(t)
	testenv.SetHome(t, t.TempDir())
	t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
	t.Setenv("OLLAMA_HOST", "http://gpu-box.lan:11434")

	flagModel = "ollama/deepseek-v4-flash:0731-cloud"
	flagMode = "print"

	rt, err := buildRootRuntime(context.Background(), []string{"hi"})
	if err != nil {
		t.Fatalf("buildRootRuntime: %v", err)
	}
	if want := "http://gpu-box.lan:11434"; rt.info.BaseURL != want {
		t.Errorf("endpoint = %q, want %q", rt.info.BaseURL, want)
	}
}
