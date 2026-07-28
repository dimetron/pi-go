package provider

import (
	"slices"
	"testing"
)

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
		{"anthropic", "claude-opus-5"},
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

func TestCompatibilityModelAliases(t *testing.T) {
	want := map[string][]string{
		"anthropic": {
			"claude-3-opus-20240229", "claude-3-opus-latest",
			"claude-3-sonnet-20240229", "claude-3-sonnet-latest",
			"claude-3-haiku-20240307", "claude-3-haiku-latest",
			"claude-3-5-sonnet-20240620", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-latest",
			"claude-3-5-haiku-latest", "claude-3-7-sonnet-20250219", "claude-3-7-sonnet-latest",
			"claude-opus-4-0", "claude-opus-4-1-20250805", "claude-opus-5", "claude-opus-4-5-20251101",
			"claude-sonnet-4-0", "claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-4-7",
			"claude-haiku-4-5", "claude-haiku-4-7", "claude-haiku-4-5-20251001",
		},
		"openai": {
			"gpt-5.5-codex", "gpt-5.4-codex", "gpt-5.3-codex", "gpt-5.3-codex-spark",
			"gpt-5.2-codex", "gpt-5.1-codex", "gpt-5.1-codex-max",
		},
	}
	for provider, aliases := range want {
		if !slices.Equal(compatibilityModelAliases[provider], aliases) {
			t.Errorf("%s compatibility aliases = %q, want %q", provider, compatibilityModelAliases[provider], aliases)
		}
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
		{"claude-opus-5", 1_000_000},
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
