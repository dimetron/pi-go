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
			name:   "ollama cloud, empty host",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"deepseek-v4.1-flash","ollama":{"options":{"num_ctx":"64000"}},"headers":{"A":"KAGENT_ENV[X]"}}`)},
			want:   KagentModel{Model: "deepseek-v4.1-flash:cloud"},
			wantOK: true,
		},
		{
			name:   "ollama already cloud-tagged",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"glm-5.2:cloud","ollama":{}}`)},
			want:   KagentModel{Model: "glm-5.2:cloud"},
			wantOK: true,
		},
		{
			name:   "ollama -cloud tag",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"deepseek-v4-flash:0731-cloud","ollama":{}}`)},
			want:   KagentModel{Model: "deepseek-v4-flash:0731-cloud"},
			wantOK: true,
		},
		{
			name:   "ollama ollama.com host",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gpt-oss","ollama":{"host":"https://ollama.com"}}`)},
			want:   KagentModel{Model: "gpt-oss:cloud"},
			wantOK: true,
		},
		{
			name:   "ollama tagged non-cloud model on cloud names the endpoint",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"qwen3:8b","ollama":{}}`)},
			want:   KagentModel{Model: "ollama/qwen3:8b", BaseURL: "https://api.ollama.com"},
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
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"mistral-large","mistral":{}}`)},
			want:   KagentModel{Model: "mistral/mistral-large"},
			wantOK: true,
		},
		{
			name:   "gemini",
			env:    map[string]string{kagentConfigEnv: cfg(`{"model":"gemini-3-pro","gemini":{}}`)},
			want:   KagentModel{Model: "gemini-3-pro"},
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
