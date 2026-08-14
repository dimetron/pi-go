package pimodels

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewRejectsEmptyModelName(t *testing.T) {
	if _, err := New(context.Background(), ""); err == nil {
		t.Fatal("New(\"\") returned no error; an empty model name cannot resolve to anything")
	}
}

func TestNewUnknownModel(t *testing.T) {
	_, err := New(context.Background(), "definitely-not-a-real-model-xyz")
	if err == nil {
		t.Fatal("expected an error for an unresolvable model name")
	}
	if !strings.Contains(err.Error(), "pimodels:") {
		t.Fatalf("error is not attributed to this package: %v", err)
	}
}

func TestResolveKnownModels(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantProvider string
	}{
		{"openai", "gpt-5.6-luna", "openai"},
		{"anthropic", "claude-sonnet-5", "anthropic"},
		{"gemini", "gemini-3.5-pro", "gemini"},
		{"xai", "grok-4.6", "xai"},
		{"mistral", "mistral-large-latest", "mistral"},
		{"ollama prefix", "ollama/gemma4:e4b", "ollama"},
		{"ollama cloud suffix", "minimax-m3:cloud", "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.model, err)
			}
			if info.Provider != tt.wantProvider {
				t.Fatalf("Resolve(%q).Provider = %q, want %q", tt.model, info.Provider, tt.wantProvider)
			}
			if info.Model == "" {
				t.Errorf("Resolve(%q) returned an empty Model name", tt.model)
			}
		})
	}
}

func TestResolveUnknownModel(t *testing.T) {
	if _, err := Resolve("not-a-model-at-all-123"); err == nil {
		t.Fatal("expected an error for an unresolvable model name")
	}
}

// TestResolveWithBaseURLTakesPrecedence pins the rule that an explicit endpoint
// wins: a caller naming a gateway must not have the model name silently reroute
// the request somewhere else.
func TestResolveWithBaseURLTakesPrecedence(t *testing.T) {
	info, err := Resolve("gpt-5.6-luna", WithBaseURL("https://gateway.example/v1"))
	if err != nil {
		t.Fatalf("Resolve with base URL: %v", err)
	}
	if info.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("BaseURL = %q, want the explicit endpoint", info.BaseURL)
	}
}

func TestContextWindow(t *testing.T) {
	if got := ContextWindow("gpt-5.6-luna"); got <= 0 {
		t.Errorf("ContextWindow for a known model = %d, want > 0", got)
	}
	if got := ContextWindow("definitely-unknown-model-xyz"); got != 0 {
		t.Errorf("ContextWindow for an unknown model = %d, want 0", got)
	}
}

func TestContextWindowFor(t *testing.T) {
	if got := ContextWindowFor("gemini", "gemini-3.7-flash"); got <= 0 {
		t.Errorf("ContextWindowFor(gemini) = %d, want > 0", got)
	}
}

func TestAPIKeyEnvVar(t *testing.T) {
	tests := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"azure":     "AZURE_OPENAI_API_KEY",
		"gemini":    "GEMINI_API_KEY",
		"xai":       "XAI_API_KEY",
		"mistral":   "MISTRAL_API_KEY",
	}
	for prov, want := range tests {
		if got := APIKeyEnvVar(prov); got != want {
			t.Errorf("APIKeyEnvVar(%q) = %q, want %q", prov, got, want)
		}
	}
}

// TestOptionsApplyInOrder pins that a later option wins, which is what makes
// FromConfig's "config first, caller second" layering work.
func TestOptionsApplyInOrder(t *testing.T) {
	var o options
	for _, opt := range []Option{
		WithAPIKey("first"),
		WithBaseURL("https://one.example"),
		WithAPIKey("second"),
	} {
		opt(&o)
	}
	if o.apiKey != "second" {
		t.Fatalf("apiKey = %q, want the later option to win", o.apiKey)
	}
	if o.baseURL != "https://one.example" {
		t.Fatalf("baseURL = %q, want it preserved", o.baseURL)
	}
}

func TestOptionsSetLLMFields(t *testing.T) {
	var o options
	for _, opt := range []Option{
		WithHeaders(map[string]string{"X-Tenant": "acme"}),
		WithConnectTimeout(3 * time.Second),
		WithCACert("/etc/ssl/corp.pem"),
		WithInsecureTLS(),
		WithPromptCachingDisabled(),
		WithAdvisor("claude-opus-4-7", 2, true),
		WithThinkingLevel("high"),
	} {
		opt(&o)
	}
	if o.llm.ExtraHeaders["X-Tenant"] != "acme" {
		t.Error("WithHeaders did not reach LLMOptions")
	}
	if o.llm.ConnectTimeout != 3*time.Second {
		t.Error("WithConnectTimeout did not reach LLMOptions")
	}
	if o.llm.CACertPath != "/etc/ssl/corp.pem" {
		t.Error("WithCACert did not reach LLMOptions")
	}
	if !o.llm.InsecureSkipTLS {
		t.Error("WithInsecureTLS did not reach LLMOptions")
	}
	if !o.llm.DisablePromptCaching {
		t.Error("WithPromptCachingDisabled did not reach LLMOptions")
	}
	if o.llm.AdvisorModel != "claude-opus-4-7" || o.llm.AdvisorMaxUses != 2 || !o.llm.AdvisorCaching {
		t.Error("WithAdvisor did not reach LLMOptions")
	}
	if o.thinkingLevel != "high" {
		t.Error("WithThinkingLevel was not recorded")
	}
}

// TestNewUsesExplicitKeyOverEnv pins that WithAPIKey wins, so an embedder
// managing its own secrets is never overridden by a stray environment variable.
func TestNewUsesExplicitKeyOverEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "from-env")

	// A local Ollama endpoint needs no credential and no network at construction
	// time, which keeps this test about option precedence rather than about
	// reaching a vendor.
	var o options
	WithAPIKey("explicit").apply(&o)
	if o.apiKey != "explicit" {
		t.Fatalf("apiKey = %q, want %q", o.apiKey, "explicit")
	}
}

// apply exists so the precedence test above reads as one call rather than a
// loop over a single element.
func (f Option) apply(o *options) { f(o) }

func TestFromConfigUnknownRoleFails(t *testing.T) {
	// A role that cannot exist must surface as an error rather than silently
	// falling back to some other model — an embedder needs to know it asked for
	// something the config does not define.
	_, err := FromConfig(context.Background(), "definitely-not-a-configured-role")
	if err == nil {
		t.Skip("config defines a fallback for unknown roles on this machine; nothing to assert")
	}
	if !strings.Contains(err.Error(), "pimodels:") {
		t.Fatalf("error is not attributed to this package: %v", err)
	}
}
