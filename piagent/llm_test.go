package piagent

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

func TestProviderEnvVar(t *testing.T) {
	tests := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"azure":     "AZURE_OPENAI_API_KEY",
		"gemini":    "GEMINI_API_KEY",
		"xai":       "XAI_API_KEY",
	}
	for in, want := range tests {
		if got := providerEnvVar(in); got != want {
			t.Errorf("providerEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeylessProvider(t *testing.T) {
	tests := []struct {
		info provider.Info
		want bool
	}{
		{provider.Info{Provider: "gemini"}, true},
		{provider.Info{Provider: "ollama"}, true},
		{provider.Info{Provider: "azure"}, true},
		{provider.Info{Provider: "custom", Ollama: true}, true},
		{provider.Info{Provider: "anthropic"}, false},
		{provider.Info{Provider: "openai"}, false},
	}
	for _, tt := range tests {
		if got := keylessProvider(tt.info); got != tt.want {
			t.Errorf("keylessProvider(%+v) = %v, want %v", tt.info, got, tt.want)
		}
	}
}

func TestResolveModelNeedsAModel(t *testing.T) {
	isolate(t)
	_, _, _, err := resolveModel(config.Config{}, defaultOptions())
	if err == nil {
		t.Fatal("resolveModel succeeded with no configured role and no override")
	}
	if !strings.Contains(err.Error(), "resolving model role") {
		t.Errorf("error = %v, want it to name the missing role", err)
	}
}

func TestResolveModelExplicitModelNeedsNoRole(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	o.modelName = "claude-sonnet-5"
	o.apiKey = "test-key"

	info, apiKey, _, err := resolveModel(config.Config{}, o)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if info.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", info.Provider)
	}
	if apiKey != "test-key" {
		t.Errorf("apiKey = %q, want the injected key", apiKey)
	}
}

func TestResolveModelRequiresACredential(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	o.modelName = "claude-sonnet-5"

	_, _, _, err := resolveModel(config.Config{}, o)
	if err == nil {
		t.Fatal("resolveModel succeeded with no API key")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error = %v, want it to name the environment variable", err)
	}
}

func TestResolveModelUsesTheConfiguredRole(t *testing.T) {
	isolate(t)
	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "claude-sonnet-5", Provider: "anthropic"},
		},
	}
	o := defaultOptions()
	o.apiKey = "k"

	info, _, _, err := resolveModel(cfg, o)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if info.Model == "" {
		t.Error("resolveModel returned an empty model name")
	}
}

func TestResolveModelCustomBaseURLNeedsNoCredential(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	o.modelName = "claude-sonnet-5"
	o.baseURL = "http://gateway.invalid/v1"

	info, apiKey, baseURL, err := resolveModel(config.Config{}, o)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if apiKey != "" {
		t.Errorf("apiKey = %q, want empty", apiKey)
	}
	if baseURL != "http://gateway.invalid/v1" {
		t.Errorf("baseURL = %q", baseURL)
	}
	if !info.Custom {
		t.Error("info.Custom = false, want true for an explicit endpoint")
	}
}

func TestResolveModelRejectsAnUnknownModel(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	o.modelName = "definitely-not-a-model"

	if _, _, _, err := resolveModel(config.Config{}, o); err == nil {
		t.Fatal("resolveModel accepted an unresolvable model name")
	}
}

func TestContextWindow(t *testing.T) {
	info := provider.Info{Provider: "anthropic", Model: "claude-sonnet-5"}

	if got := contextWindow(t.Context(), config.Config{ContextWindow: 4242}, info, ""); got != 4242 {
		t.Errorf("contextWindow with an explicit config value = %d, want 4242", got)
	}
	if got := contextWindow(t.Context(), config.Config{}, info, ""); got <= 0 {
		t.Errorf("contextWindow = %d, want a positive size from the catalog", got)
	}
}

func TestBuildLLMPassesThroughAnInjectedModel(t *testing.T) {
	isolate(t)
	fake := &fakeLLM{name: "injected"}

	o := defaultOptions()
	o.llm = fake
	llm, info, err := buildLLM(t.Context(), config.Config{}, o)
	if err != nil {
		t.Fatalf("buildLLM: %v", err)
	}
	if llm != fake {
		t.Error("buildLLM wrapped an injected model; it should be passed through unmetered")
	}
	if info.Model != "injected" {
		t.Errorf("info.Model = %q, want the injected model's name", info.Model)
	}
}

func TestBuildLLMFromConfiguration(t *testing.T) {
	isolate(t)
	o := defaultOptions()
	o.modelName = "claude-sonnet-5"
	o.apiKey = "test-key"

	llm, info, err := buildLLM(t.Context(), config.Config{MaxDailyTokens: 1000}, o)
	if err != nil {
		t.Fatalf("buildLLM: %v", err)
	}
	if llm == nil {
		t.Fatal("buildLLM returned a nil model")
	}
	if info.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", info.Provider)
	}
	if llm.Name() == "" {
		t.Error("the guardrail wrapper lost the model name")
	}
}

func TestBuildLLMReportsResolutionFailures(t *testing.T) {
	isolate(t)
	if _, _, err := buildLLM(t.Context(), config.Config{}, defaultOptions()); err == nil {
		t.Fatal("buildLLM succeeded with nothing configured")
	}
}
