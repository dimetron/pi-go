package provider

import (
	"testing"
)

func TestBackendName(t *testing.T) {
	codexToken := authTestJWT(t)
	tests := []struct {
		name    string
		info    Info
		key     string
		baseURL string
		want    string
	}{
		{name: "codex chatgpt", info: Info{Provider: "openai"}, key: codexToken, want: "openai-codex-chatgpt"},
		{name: "platform openai", info: Info{Provider: "openai"}, key: "sk-test", want: "openai-platform"},
		{name: "custom provider", info: Info{Provider: "openai"}, key: "sk-test", baseURL: "https://gateway.example/v1", want: "openai-custom"},
		{name: "ollama", info: Info{Provider: "ollama"}, baseURL: "http://127.0.0.1:11434", want: "ollama-custom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BackendName(tt.info, tt.key, tt.baseURL); got != tt.want {
				t.Fatalf("BackendName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func authTestJWT(t *testing.T) string {
	t.Helper()
	return "eyJhbGciOiJSUzI1NiJ9.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiMSJ9fQ.signature"
}
