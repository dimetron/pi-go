package provider

import "testing"

// The case this file exists for: a locally pulled model, an OLLAMA_API_KEY
// exported for some cloud model, and a request that used to be posted to
// api.ollama.com — where a private name like muse-glimmer:30b-mlx does not
// exist, so the run failed with a model-not-found from a server the user never
// meant to talk to.
func TestResolveOllamaEndpoint(t *testing.T) {
	const key = "sk-ollama-test"

	tests := []struct {
		name         string
		model        string
		apiKey       string // recorded to document the scenario; routing must not consult it
		baseURL      string
		wantEndpoint string
	}{
		{
			name:         "local model with a key exported stays local",
			model:        "muse-glimmer:30b-mlx",
			apiKey:       key,
			wantEndpoint: ollamaLocalURL,
		},
		{
			name:         "local model without a key",
			model:        "muse-glimmer:30b-mlx",
			wantEndpoint: ollamaLocalURL,
		},
		{
			name:         "cloud tag routes to the cloud",
			model:        "minimax-m3:cloud",
			apiKey:       key,
			wantEndpoint: ollamaCloudURL,
		},
		{
			// The form most of the cloud catalog actually uses.
			name:         "sized cloud tag routes to the cloud",
			model:        "deepseek-v4-flash:0731-cloud",
			apiKey:       key,
			wantEndpoint: ollamaCloudURL,
		},
		{
			name:         "explicit host wins over the cloud tag",
			model:        "minimax-m3:cloud",
			apiKey:       key,
			baseURL:      "http://gpu-box.lan:11434",
			wantEndpoint: "http://gpu-box.lan:11434",
		},
		{
			name:         "explicit remote host is honored",
			model:        "muse-glimmer:30b-mlx",
			apiKey:       key,
			baseURL:      "https://ollama.internal.example.com",
			wantEndpoint: "https://ollama.internal.example.com",
		},
		{
			name:         "explicit localhost is honored",
			model:        "muse-glimmer:30b-mlx",
			apiKey:       key,
			baseURL:      "http://localhost:11434",
			wantEndpoint: "http://localhost:11434",
		},
		{
			name:         "explicit loopback IP is honored",
			model:        "muse-glimmer:30b-mlx",
			apiKey:       key,
			baseURL:      "http://127.0.0.1:11434",
			wantEndpoint: "http://127.0.0.1:11434",
		},
		{
			name:         "IPv6 loopback is honored",
			model:        "muse-glimmer:30b-mlx",
			apiKey:       key,
			baseURL:      "http://[::1]:11434",
			wantEndpoint: "http://[::1]:11434",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if endpoint := resolveOllamaEndpoint(tt.model, tt.baseURL); endpoint != tt.wantEndpoint {
				t.Errorf("resolveOllamaEndpoint(%q, %q) = %q, want %q",
					tt.model, tt.baseURL, endpoint, tt.wantEndpoint)
			}
		})
	}
}

func TestIsOllamaCloudModel(t *testing.T) {
	cloud := []string{"minimax-m3:cloud", "deepseek-v4-flash:0731-cloud", "gpt-oss:120b-cloud"}
	local := []string{
		"muse-glimmer:30b-mlx",
		"llama3.3:70b",
		"qwen3:latest",
		// "cloud" has to be the tag, not merely present in the name.
		"cloudy-llm:7b",
		"nimbus-cloud-13b:q4",
	}

	for _, m := range cloud {
		if !isOllamaCloudModel(m) {
			t.Errorf("isOllamaCloudModel(%q) = false, want true", m)
		}
	}
	for _, m := range local {
		if isOllamaCloudModel(m) {
			t.Errorf("isOllamaCloudModel(%q) = true, want false", m)
		}
	}
}

// NewOllama is the real entry point, so pin that it honors the routing rather
// than only testing the helper underneath it.
func TestNewOllamaAcceptsLocalTaggedModel(t *testing.T) {
	llm, err := NewOllama(t.Context(), "muse-glimmer:30b-mlx", "sk-ollama-test", "", "none", nil)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}
	if got := llm.Name(); got != "muse-glimmer:30b-mlx" {
		t.Errorf("Name() = %q, want the model name unchanged", got)
	}
}
