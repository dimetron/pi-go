package server

import "testing"

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestKagentModelFromEnv(t *testing.T) {
	cfg := func(model string) string {
		return `{"runtime":{"name":"x"},"agent":{"instruction":"hi","model":` + model + `}}`
	}
	tests := []struct {
		name       string
		env        map[string]string
		want       KagentModel
		wantOK     bool
		wantReason bool
	}{
		{name: "absent config", env: nil},
		{name: "malformed config", env: map[string]string{kagentConfigEnv: `{not json`}},
		{name: "no agent.model", env: map[string]string{kagentConfigEnv: `{"agent":{"instruction":"x"}}`}},
		{name: "empty model name", env: map[string]string{kagentConfigEnv: cfg(`{"model":"","ollama":{}}`)}},
		{
			// A bare catalog name is what api.ollama.com serves; :cloud is the
			// local daemon's proxy tag, so it is not added when the endpoint is
			// named explicitly.
			name:   "ollama catalog model, empty host",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"deepseek-v4.1-flash","ollama":{"options":{"num_ctx":"64000"}},"headers":{"A":"KAGENT_ENV[X]"}}`)},
			want:   KagentModel{Model: "ollama/deepseek-v4.1-flash", BaseURL: "https://api.ollama.com"},
			wantOK: true,
		},
		{
			name:   "ollama already cloud-tagged",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"glm-5.2:cloud","ollama":{}}`)},
			want:   KagentModel{Model: "ollama/glm-5.2:cloud", BaseURL: "https://api.ollama.com"},
			wantOK: true,
		},
		{
			name:   "ollama bare catalog name that is also priced",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"glm-5.2","ollama":{}}`)},
			want:   KagentModel{Model: "ollama/glm-5.2", BaseURL: "https://api.ollama.com"},
			wantOK: true,
		},
		{
			name:   "ollama -cloud tag",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"deepseek-v4-flash:0731-cloud","ollama":{}}`)},
			want:   KagentModel{Model: "ollama/deepseek-v4-flash:0731-cloud", BaseURL: "https://api.ollama.com"},
			wantOK: true,
		},
		{
			name:   "ollama ollama.com host",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gpt-oss:120b","ollama":{"host":"https://ollama.com"}}`)},
			want:   KagentModel{Model: "ollama/gpt-oss:120b", BaseURL: "https://api.ollama.com"},
			wantOK: true,
		},
		{
			// kagent's rule: with no host and a name that is neither a catalog
			// entry nor cloud-tagged, the model goes to the daemon. Sending it
			// to api.ollama.com 404s.
			name:   "ollama local tagged model with no host stays local",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"qwen3:8b","ollama":{}}`)},
			want:   KagentModel{Model: "ollama/qwen3:8b"},
			wantOK: true,
		},
		{
			name:   "ollama local uncataloged model with no host stays local",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"llama3.3","ollama":{}}`)},
			want:   KagentModel{Model: "ollama/llama3.3"},
			wantOK: true,
		},
		{
			name:   "ollama host in JSON",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"llama3.3","ollama":{"host":"http://10.0.0.5:11434"}}`)},
			want:   KagentModel{Model: "ollama/llama3.3", BaseURL: "http://10.0.0.5:11434"},
			wantOK: true,
		},
		{
			name: "ollama host from OLLAMA_API_BASE",
			env: map[string]string{
				kagentConfigEnv:     cfg(`{"model":"llama3.3","ollama":{}}`),
				kagentOllamaBaseEnv: "http://host.docker.internal:11434",
			},
			want:   KagentModel{Model: "ollama/llama3.3", BaseURL: "http://host.docker.internal:11434"},
			wantOK: true,
		},
		{
			name:   "openai with base_url",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gpt-6-luna","openai":{"base_url":"https://llm.example/v1"}}`)},
			want:   KagentModel{Model: "openai/gpt-6-luna", BaseURL: "https://llm.example/v1"},
			wantOK: true,
		},
		{
			name:   "openai default endpoint",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gpt-6-luna","openai":{}}`)},
			want:   KagentModel{Model: "openai/gpt-6-luna"},
			wantOK: true,
		},
		{
			name:   "anthropic with base_url",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"claude-sonnet-5","anthropic":{"base_url":"http://proxy:8080"}}`)},
			want:   KagentModel{Model: "anthropic/claude-sonnet-5", BaseURL: "http://proxy:8080"},
			wantOK: true,
		},
		{
			name:   "mistral",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"mistral-large-4","mistral":{}}`)},
			want:   KagentModel{Model: "mistral/mistral-large-4"},
			wantOK: true,
		},
		{
			// Resolves, but pi-go's catalog does not know it, so the session
			// build would reject it. Falling back to the default is the whole
			// point of this function returning a reason.
			name:       "mistral model outside pi-go's catalog falls back",
			env:        map[string]string{kagentConfigEnv: cfg(`{"model":"mistral-large","mistral":{}}`)},
			wantReason: true,
		},
		{
			name:   "gemini",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gemini-4-argon","gemini":{}}`)},
			want:   KagentModel{Model: "gemini-4-argon"},
			wantOK: true,
		},
		{
			// ADKGemini.base_url is compiled, and a proxy endpoint also marks
			// the model custom, which is what skips catalog validation.
			name:   "gemini with base_url",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gemini-4-argon","gemini":{"base_url":"https://gemini-proxy.example"}}`)},
			want:   KagentModel{Model: "gemini-4-argon", BaseURL: "https://gemini-proxy.example"},
			wantOK: true,
		},
		{
			name:       "bedrock is unmappable",
			env:        map[string]string{kagentConfigEnv: cfg(`{"model":"anthropic.claude","bedrock":{"region":"us-east-1"}}`)},
			wantReason: true,
		},
		{
			name:       "no provider object",
			env:        map[string]string{kagentConfigEnv: cfg(`{"model":"x"}`)},
			wantReason: true,
		},
		{
			name:       "model that pi-go cannot route",
			env:        map[string]string{kagentConfigEnv: cfg(`{"model":"mystery","gemini":{}}`)},
			wantReason: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, reason := KagentModelFromEnv(envOf(tt.env))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (reason %q)", ok, tt.wantOK, reason)
			}
			if got != tt.want {
				t.Errorf("model = %+v, want %+v", got, tt.want)
			}
			if (reason != "") != tt.wantReason {
				t.Errorf("reason = %q, wantReason %v", reason, tt.wantReason)
			}
		})
	}
}

// The Ollama mapping has to agree with kagent's own routing rule, because the
// two disagreeing is the bug this file exists to fix: kagent reports one model
// and the harness silently ran another. kagent's rule (models.OllamaReachesCloud
// in kagent/go/adk/pkg/models/ollama.go) is: a host that is not ollama.com means
// a daemon; otherwise the model decides, via the cloud catalog or a cloud tag.
//
// This is a table rather than a call into kagent — the rule is transcribed, and
// the point of the test is that a change to the mapping has to be deliberate.
func TestOllamaKagentModel_MatchesKagentRoutingRule(t *testing.T) {
	cloud := "https://api.ollama.com"
	tests := []struct {
		model, host string
		want        KagentModel
	}{
		// Catalog entries and cloud tags reach the cloud API, named explicitly.
		{"glm-5.2", "", KagentModel{Model: "ollama/glm-5.2", BaseURL: cloud}},
		{"deepseek-v4.1-flash", "", KagentModel{Model: "ollama/deepseek-v4.1-flash", BaseURL: cloud}},
		{"glm-5.2:cloud", "", KagentModel{Model: "ollama/glm-5.2:cloud", BaseURL: cloud}},
		{"deepseek-v4-flash:0731-cloud", "", KagentModel{Model: "ollama/deepseek-v4-flash:0731-cloud", BaseURL: cloud}},
		{"gpt-oss:120b", "https://ollama.com", KagentModel{Model: "ollama/gpt-oss:120b", BaseURL: cloud}},
		{"glm-5.2", "api.ollama.com", KagentModel{Model: "ollama/glm-5.2", BaseURL: cloud}},
		// Everything else stays on the daemon, as kagent routes it. Before this
		// was fixed, qwen3:8b went to api.ollama.com and llama3.3 became
		// "llama3.3:cloud", a name the daemon does not have.
		{"qwen3:8b", "", KagentModel{Model: "ollama/qwen3:8b"}},
		{"llama3.3", "", KagentModel{Model: "ollama/llama3.3"}},
		{"qwen3.8:27b-mlx", "", KagentModel{Model: "ollama/qwen3.8:27b-mlx"}},
		// An explicit daemon host wins over the name, cloud or not.
		{"qwen3:8b", "http://10.0.0.5:11434", KagentModel{Model: "ollama/qwen3:8b", BaseURL: "http://10.0.0.5:11434"}},
		{"glm-5.2", "http://host.docker.internal:11434", KagentModel{Model: "ollama/glm-5.2", BaseURL: "http://host.docker.internal:11434"}},
		// The repo's own render-manifests.sh default: a cloud-tagged model on a
		// daemon host. The host is the operator's choice and wins.
		{"deepseek-v4-flash:0731-cloud", "http://host.docker.internal:11434", KagentModel{Model: "ollama/deepseek-v4-flash:0731-cloud", BaseURL: "http://host.docker.internal:11434"}},
	}
	for _, tt := range tests {
		t.Run(tt.model+"@"+tt.host, func(t *testing.T) {
			if got := ollamaKagentModel(tt.model, tt.host); got != tt.want {
				t.Errorf("ollamaKagentModel(%q, %q) = %+v, want %+v", tt.model, tt.host, got, tt.want)
			}
		})
	}
}
