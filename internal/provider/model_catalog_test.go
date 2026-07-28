package provider

import "testing"

func TestEmbeddedModelCatalog(t *testing.T) {
	models, err := loadKnownModels()
	if err != nil {
		t.Fatalf("loadKnownModels() error: %v", err)
	}

	tests := []struct {
		provider string
		model    string
	}{
		{"openai", "gpt-4o"},
		{"openai", "gpt-5.6-sol"},
		{"openai", "gpt-5.2"},
		{"openai", "gpt-5.3-codex-spark"},
		{"anthropic", "claude-mythos-5"},
		{"anthropic", "claude-opus-4-8"},
		{"anthropic", "claude-3-5-sonnet-20241022"},
		{"gemini", "gemini-3.6-flash"},
		{"gemini", "gemini-3.5-flash"},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			if !containsModelID(models[tt.provider], tt.model) {
				t.Fatalf("%s catalog missing %q", tt.provider, tt.model)
			}
		})
	}
}

func TestEmbeddedContextWindows(t *testing.T) {
	sizes, err := loadContextWindowSizes()
	if err != nil {
		t.Fatalf("loadContextWindowSizes() error: %v", err)
	}

	tests := []struct {
		prefix string
		want   int64
	}{
		{"gpt-5.6-sol", 272_000},
		{"gpt-5.2", 1_050_000},
		{"claude-mythos-5", 1_000_000},
		{"claude-3-5-sonnet", 200_000},
		{"gemini-3.6-flash", 1_048_576},
		{"phi4:14b", 16_384},
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			if got := sizes[tt.prefix]; got != tt.want {
				t.Fatalf("context window %q = %d, want %d", tt.prefix, got, tt.want)
			}
		})
	}
}

func containsModelID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
