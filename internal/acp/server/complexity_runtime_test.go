package server

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/provider"
)

// checkProviderCredentials is the "may this provider start without an API key"
// rule buildSessionLLM used to inline. One row per escape hatch the original
// condition listed, so a dropped clause shows up as a failure here.
func TestCheckProviderCredentials(t *testing.T) {
	tests := []struct {
		name    string
		info    provider.Info
		apiKey  string
		baseURL string
		wantErr bool
	}{
		{
			name:   "key present",
			info:   provider.Info{Provider: "anthropic"},
			apiKey: "sk-test",
		},
		{
			name:    "no key and no base URL is rejected",
			info:    provider.Info{Provider: "anthropic"},
			wantErr: true,
		},
		{
			name:    "openai without a key is rejected too",
			info:    provider.Info{Provider: "openai"},
			wantErr: true,
		},
		{
			name:    "a custom base URL stands in for the key",
			info:    provider.Info{Provider: "anthropic"},
			baseURL: "http://localhost:8080/v1",
		},
		{
			name: "an Ollama-served model needs no key",
			info: provider.Info{Provider: "anthropic", Ollama: true},
		},
		{
			name: "gemini authenticates through ADC",
			info: provider.Info{Provider: "gemini"},
		},
		{
			name: "ollama provider",
			info: provider.Info{Provider: "ollama"},
		},
		{
			name: "azure authenticates through the deployment URL",
			info: provider.Info{Provider: "azure"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkProviderCredentials(tt.info, tt.apiKey, tt.baseURL)
			if tt.wantErr != (err != nil) {
				t.Fatalf("checkProviderCredentials() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			// The message has to tell the user which key to set.
			if !strings.Contains(err.Error(), tt.info.Provider) {
				t.Errorf("err = %v, want it to name provider %q", err, tt.info.Provider)
			}
			if envVar := providerEnvVar(tt.info.Provider); !strings.Contains(err.Error(), envVar) {
				t.Errorf("err = %v, want it to name %s", err, envVar)
			}
		})
	}
}

// resolveOllamaBaseURL both settles the endpoint and decides whether the local
// daemon has to answer. Non-Ollama providers must pass through untouched.
func TestResolveOllamaBaseURL_NonOllamaPassesThrough(t *testing.T) {
	for _, baseURL := range []string{"", "https://api.anthropic.com"} {
		got, err := resolveOllamaBaseURL(provider.Info{Provider: "anthropic", Model: "claude-sonnet-4"}, "", baseURL)
		if err != nil {
			t.Fatalf("resolveOllamaBaseURL(%q): %v", baseURL, err)
		}
		if got != baseURL {
			t.Errorf("resolveOllamaBaseURL(%q) = %q, want it unchanged", baseURL, got)
		}
	}
}

// A cloud-tagged model with a key and no explicit endpoint resolves to
// ollama.com, and the local-daemon health check must not run against it.
//
// The key matters to the destination here: without one the same model falls
// back to the local daemon, because api.ollama.com answers an unauthenticated
// request with 401 before it looks at the model.
func TestResolveOllamaBaseURL_CloudModelSkipsHealthCheck(t *testing.T) {
	info := provider.Info{Provider: "ollama", Model: "deepseek-v4-flash:0731-cloud", Ollama: true}
	got, err := resolveOllamaBaseURL(info, "sk-ollama-test", "")
	if err != nil {
		t.Fatalf("resolveOllamaBaseURL: %v", err)
	}
	if !strings.Contains(got, "api.ollama.com") {
		t.Fatalf("baseURL = %q, want the ollama cloud endpoint", got)
	}
}

// An explicit endpoint wins over the tag-based default, and a key means the
// caller is authenticating rather than reaching a local daemon.
func TestResolveOllamaBaseURL_APIKeySkipsHealthCheck(t *testing.T) {
	info := provider.Info{Provider: "ollama", Model: "qwen3:8b", Ollama: true}
	// Port 1 has nothing listening: if the health check ran, this would fail.
	got, err := resolveOllamaBaseURL(info, "sk-ollama-test", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("resolveOllamaBaseURL: %v", err)
	}
	if got != "http://127.0.0.1:1" {
		t.Fatalf("baseURL = %q, want the explicit endpoint", got)
	}
}

// With no key and a local endpoint the daemon has to answer. Pointing at a
// closed port makes the failure deterministic rather than depending on whether
// a daemon happens to be running on the test machine.
func TestResolveOllamaBaseURL_LocalHealthCheckFailureIsReported(t *testing.T) {
	info := provider.Info{Provider: "ollama", Model: "qwen3:8b", Ollama: true}
	got, err := resolveOllamaBaseURL(info, "", "http://127.0.0.1:1")
	if err == nil {
		t.Fatalf("resolveOllamaBaseURL = %q, want an error when the daemon is unreachable", got)
	}
	if !strings.Contains(err.Error(), "ollama health check") {
		t.Errorf("err = %v, want it to name the health check", err)
	}
	if got != "" {
		t.Errorf("baseURL = %q, want empty alongside an error", got)
	}
}

// A model with no key pointed at the cloud endpoint by hand is a credential
// problem, not a reachability one, so the health check stays off.
func TestResolveOllamaBaseURL_ExplicitCloudEndpointSkipsHealthCheck(t *testing.T) {
	info := provider.Info{Provider: "ollama", Model: "qwen3:8b", Ollama: true}
	got, err := resolveOllamaBaseURL(info, "", "https://api.ollama.com")
	if err != nil {
		t.Fatalf("resolveOllamaBaseURL: %v", err)
	}
	if !strings.Contains(got, "api.ollama.com") {
		t.Fatalf("baseURL = %q, want the cloud endpoint preserved", got)
	}
}
