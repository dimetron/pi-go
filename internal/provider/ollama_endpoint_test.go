package provider

import "testing"

// The case this file exists for: a locally pulled model, an OLLAMA_API_KEY
// exported for some cloud model, and a request that used to be posted to
// api.ollama.com — where a private name like muse-glimmer:30b-mlx does not
// exist, so the run failed with a model-not-found from a server the user never
// meant to talk to.
//
// The key is read in exactly one direction: its absence keeps a cloud-tagged
// model on the local daemon, which proxies cloud models on the user's
// `ollama signin` identity. Its presence never promotes an untagged model.
func TestResolveOllamaEndpoint(t *testing.T) {
	const key = "sk-ollama-test"

	tests := []struct {
		name         string
		model        string
		apiKey       string
		baseURL      string
		forceLocal   bool
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
			// Without a credential api.ollama.com answers 401 before it looks
			// at the model, so the cloud is not an option — the local daemon
			// is, and it serves cloud models on the signed-in identity.
			name:         "cloud tag without a key falls back to the local daemon",
			model:        "minimax-m3:cloud",
			wantEndpoint: ollamaLocalURL,
		},
		{
			name:         "sized cloud tag without a key falls back to the local daemon",
			model:        "deepseek-v4-flash:0731-cloud",
			wantEndpoint: ollamaLocalURL,
		},
		{
			// ollama/ is the one way to name the destination outright, so a
			// tag on the name cannot overrule it.
			name:         "ollama/ prefix keeps a cloud-tagged model local",
			model:        "deepseek-v4-flash:0731-cloud",
			apiKey:       key,
			forceLocal:   true,
			wantEndpoint: ollamaLocalURL,
		},
		{
			name:         "explicit host still wins over the ollama/ prefix",
			model:        "deepseek-v4-flash:0731-cloud",
			apiKey:       key,
			baseURL:      "http://gpu-box.lan:11434",
			forceLocal:   true,
			wantEndpoint: "http://gpu-box.lan:11434",
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
			r := OllamaRouting{
				Model:      tt.model,
				BaseURL:    tt.baseURL,
				APIKey:     tt.apiKey,
				ForceLocal: tt.forceLocal,
			}
			if endpoint := ResolveOllamaEndpoint(r); endpoint != tt.wantEndpoint {
				t.Errorf("ResolveOllamaEndpoint(%+v) = %q, want %q", r, endpoint, tt.wantEndpoint)
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
		if !IsOllamaCloudModel(m) {
			t.Errorf("IsOllamaCloudModel(%q) = false, want true", m)
		}
	}
	for _, m := range local {
		if IsOllamaCloudModel(m) {
			t.Errorf("IsOllamaCloudModel(%q) = true, want false", m)
		}
	}
}

// NewOllama is the real entry point, so pin that it honors the routing rather
// than only testing the helper underneath it.
func TestNewOllamaAcceptsLocalTaggedModel(t *testing.T) {
	llm, err := NewOllama(t.Context(), OllamaRouting{Model: "muse-glimmer:30b-mlx", APIKey: "sk-ollama-test", BaseURL: ""}, "none", nil)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}
	if got := llm.Name(); got != "muse-glimmer:30b-mlx" {
		t.Errorf("Name() = %q, want the model name unchanged", got)
	}
}

// Telling the two apart is what keeps a missing OLLAMA_API_KEY from being
// reported as an unreachable daemon: CheckOllama is only meaningful against a
// host someone runs themselves.
func TestIsOllamaCloudEndpoint(t *testing.T) {
	cloud := []string{
		ollamaCloudURL,
		"https://api.ollama.com/",
		"https://ollama.com",
		"api.ollama.com", // no scheme, as OLLAMA_HOST is often written
		"HTTPS://API.OLLAMA.COM",
	}
	local := []string{
		ollamaLocalURL,
		// Unparseable: the guard returns false so a malformed OLLAMA_HOST is
		// treated as a host someone runs, keeping the reachability check.
		"http://[::1",
		"http://127.0.0.1:11434",
		"http://[::1]:11434",
		"http://gpu-box.lan:11434",
		"https://ollama.internal.example.com",
		// A proxy that merely mentions the cloud host is not the cloud host.
		"https://ollama-proxy.example.com/api.ollama.com",
		"",
	}

	for _, u := range cloud {
		if !IsOllamaCloudEndpoint(u) {
			t.Errorf("IsOllamaCloudEndpoint(%q) = false, want true", u)
		}
	}
	for _, u := range local {
		if IsOllamaCloudEndpoint(u) {
			t.Errorf("IsOllamaCloudEndpoint(%q) = true, want false", u)
		}
	}
}
