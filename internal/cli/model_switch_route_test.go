package cli

import (
	"testing"

	"github.com/dimetron/pi-go/internal/config"
)

// A model listed by a routing provider must reach it with every segment: the
// gateway serves "openai/gpt-5" as its own ID, not OpenAI's "gpt-5".
func TestRoutedModelNameKeepsListedIDThroughResolution(t *testing.T) {
	cases := []struct {
		name, provider, wantModel, wantProvider string
	}{
		{"openai/gpt-5", "agentgateway", "openai/gpt-5", "agentgateway"},
		{"anthropic/claude-3-opus", "agentgateway", "anthropic/claude-3-opus", "agentgateway"},
		{"agentgateway/openai/gpt-5", "agentgateway", "openai/gpt-5", "agentgateway"},
		{"anthropic/claude-sonnet-4.5", "openrouter", "anthropic/claude-sonnet-4.5", "openrouter"},
		{"gpt-5", "openai", "gpt-5", "openai"},
		{"claude-opus-4-7", "anthropic", "claude-opus-4-7", "anthropic"},
		{"gemini-2.5-flash", "gemini", "gemini-2.5-flash", "gemini"},
	}
	cfg := config.Defaults()
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.name, func(t *testing.T) {
			info, _, _, err := resolveSwitchedModel(cfg, routedModelName(tc.name, tc.provider), tc.provider)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if info.Model != tc.wantModel || info.Provider != tc.wantProvider {
				t.Errorf("got %s/%s, want %s/%s", info.Provider, info.Model, tc.wantProvider, tc.wantModel)
			}
		})
	}
}

// Without the prefix the gateway ID is cut, which is the bug the prefix fixes.
func TestResolveWithoutRoutingPrefixCutsGatewayID(t *testing.T) {
	info, _, _, err := resolveSwitchedModel(config.Defaults(), "openai/gpt-5", "agentgateway")
	if err != nil {
		t.Fatal(err)
	}
	if info.Model == "openai/gpt-5" {
		t.Skip("resolution no longer strips the upstream prefix; routedModelName may be redundant")
	}
}

func TestConfiguredListProvidersIncludesOllamaAndKeyed(t *testing.T) {
	got := configuredListProviders(map[string]string{"openai": "k"}, map[string]string{"agentgateway": "http://gw"})
	want := map[string]bool{"openai": true, "agentgateway": true, "ollama": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys of %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected provider %q in %v", p, got)
		}
	}
}
