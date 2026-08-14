package provider

import "testing"

func TestAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		// The four that do not follow the pattern, and are the reason this
		// mapping exists rather than a bare strings.ToUpper.
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"azure", "AZURE_OPENAI_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		// Everything else derives from the provider name.
		{"xai", "XAI_API_KEY"},
		{"mistral", "MISTRAL_API_KEY"},
		{"ollama", "OLLAMA_API_KEY"},
		{"opencode", "OPENCODE_API_KEY"},
		{"", "_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			if got := APIKeyEnvVar(tt.provider); got != tt.want {
				t.Fatalf("APIKeyEnvVar(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestAPIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-value")
	if got := APIKeyFromEnv("openai"); got != "sk-test-value" {
		t.Fatalf("APIKeyFromEnv(openai) = %q, want the value from the environment", got)
	}
}

// TestAPIKeyFromEnvUnset pins that an absent key is an empty string rather than
// an error: providers that need no credential — a local Ollama — must keep
// working, and it is the provider constructor's job to decide, not this helper's.
func TestAPIKeyFromEnvUnset(t *testing.T) {
	t.Setenv("MADEUPPROVIDER_API_KEY", "")
	if got := APIKeyFromEnv("madeupprovider"); got != "" {
		t.Fatalf("APIKeyFromEnv for an unset variable = %q, want empty", got)
	}
}

// TestAPIKeyEnvVarMatchesFromEnv keeps the two in step: a caller that reports a
// missing credential by name must name the variable the lookup actually reads.
func TestAPIKeyEnvVarMatchesFromEnv(t *testing.T) {
	for _, p := range []string{"anthropic", "openai", "azure", "gemini", "xai"} {
		t.Setenv(APIKeyEnvVar(p), "value-for-"+p)
		if got := APIKeyFromEnv(p); got != "value-for-"+p {
			t.Errorf("provider %q: APIKeyFromEnv read a different variable than APIKeyEnvVar names", p)
		}
	}
}
