package cli

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/testenv"
)

// The bug this pins: OLLAMA_API_KEY exported for a :cloud model used to be read
// as a destination, so a locally pulled name like qwen3.8:27b-mlx was posted to
// api.ollama.com and came back "model not found" from a server the user never
// meant to talk to. Routing follows the model's tag; the key is a credential.
func TestBuildRootRuntime_OllamaKeyDoesNotForceCloud(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		wantURL string
	}{
		{
			name:    "mlx tag stays on the local daemon",
			model:   "ollama/qwen3.8:27b-mlx",
			wantURL: "http://localhost:11434",
		},
		{
			name:    "cloud tag still routes to the cloud",
			model:   "ollama/deepseek-v4-flash:0731-cloud",
			wantURL: "https://api.ollama.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalFlags(t)
			testenv.SetHome(t, t.TempDir())
			// Set, as it must be for any cloud model — and irrelevant to where
			// a local model goes.
			t.Setenv("OLLAMA_API_KEY", "sk-ollama-test")
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
