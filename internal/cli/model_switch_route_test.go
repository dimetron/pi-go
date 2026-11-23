package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/tui"
)

// A model listed by a routing provider must reach it with every segment: the
// gateway serves "openai/gpt-5" as its own ID, not OpenAI's "gpt-5".
func TestRoutedModelNameKeepsListedIDThroughResolution(t *testing.T) {
	cases := []struct {
		name, provider, wantModel, wantProvider string
	}{
		{"openai/gpt-5", "agentgateway", "openai/gpt-5", "agentgateway"},
		{"anthropic/claude-3-opus", "agentgateway", "anthropic/claude-3-opus", "agentgateway"},
		{"anthropic/claude-sonnet-4.5", "openrouter", "anthropic/claude-sonnet-4.5", "openrouter"},
		// OpenRouter's own namespace is part of the ID, not a routing prefix.
		{"openrouter/auto", "openrouter", "openrouter/auto", "openrouter"},
		// Names detection does not know are still the named provider's.
		{"gemma-4-31b-it", "gemini", "gemma-4-31b-it", "gemini"},
		{"llama3", "ollama", "llama3", "ollama"},
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

// An Ollama Cloud model listed under ollama must not be pinned to the local
// daemon: routedModelName leaves ollama unprefixed for exactly this.
func TestRoutedModelNameKeepsOllamaCloudOffLocalhost(t *testing.T) {
	name := routedModelName("minimax-m3:cloud", "ollama")
	if name != "minimax-m3:cloud" {
		t.Fatalf("routedModelName = %q", name)
	}
	info, _, _, err := resolveSwitchedModel(config.Defaults(), name, "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if info.LocalOllama {
		t.Errorf("cloud model resolved as local: %+v", info)
	}
}

// A switched model is saved by switchedModelName and resolved again on
// restart with the saved provider pinned; the ID must survive the trip.
func TestSwitchedModelNameRoundTrips(t *testing.T) {
	cfg := config.Defaults()
	for _, tc := range []struct{ id, provider string }{
		{"openai/gpt-5", "openrouter"},
		{"openrouter/auto", "openrouter"},
		{"openai/gpt-5", "agentgateway"},
	} {
		first, _, _, err := resolveSwitchedModel(cfg, routedModelName(tc.id, tc.provider), tc.provider)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		saved := switchedModelName(first)
		again, _, _, err := resolveSwitchedModel(cfg, saved, tc.provider)
		if err != nil {
			t.Fatalf("%s: restart resolve of %q: %v", tc.id, saved, err)
		}
		if again.Model != tc.id || again.Provider != tc.provider {
			t.Errorf("%s on %s: restart got %s/%s via saved %q", tc.id, tc.provider, again.Provider, again.Model, saved)
		}
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

// gatewayStub serves a /v1/models payload in agentgateway's shape and points
// flagURL at it for the test.
func gatewayStub(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := flagURL
	flagURL = srv.URL
	t.Cleanup(func() { flagURL = orig })
}

func TestListProviderModelsSortsNewestFirst(t *testing.T) {
	gatewayStub(t, `{"data":[{"id":"zeta/b"},{"id":"alpha/a","context_window":1000}]}`)
	models, err := listProviderModels(context.Background(), config.Defaults(), "agentgateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "alpha/a" || models[1].ID != "zeta/b" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ContextWindow != 1000 {
		t.Errorf("context window dropped: %+v", models[0])
	}
}

func TestListProviderModelsReportsProviderError(t *testing.T) {
	gatewayStub(t, `not json`)
	if _, err := listProviderModels(context.Background(), config.Defaults(), "agentgateway"); err == nil {
		t.Fatal("expected an error for a malformed listing")
	}
}

func TestApplyModelSwitchingWiresEveryHook(t *testing.T) {
	gatewayStub(t, `{"data":[{"id":"openai/gpt-5"}]}`)
	var tc tui.Config
	applyModelSwitching(&tc, config.Defaults(), guardrail.New(0))
	if tc.ModelSwitcher == nil || tc.ProviderModelSwitcher == nil || tc.ModelLister == nil || tc.ModelProviders == nil {
		t.Fatalf("hooks not all set: %+v", tc)
	}

	models, err := tc.ModelLister(context.Background(), "agentgateway")
	if err != nil || len(models) != 1 || models[0].ID != "openai/gpt-5" {
		t.Fatalf("lister = %+v, %v", models, err)
	}
	// flagURL counts as configuration for every provider, so all are offered.
	if provs := tc.ModelProviders(); !slices.Contains(provs, "agentgateway") {
		t.Errorf("providers = %v", provs)
	}
	// A custom endpoint accepts any name, and building the LLM makes no call,
	// so both switchers can be driven end to end against the stub.
	if _, name, _, err := tc.ModelSwitcher(context.Background(), "agentgateway/openai/gpt-5"); err != nil || name != "agentgateway/openai/gpt-5" {
		t.Errorf("ModelSwitcher = %q, %v", name, err)
	}
	_, name, prov, err := tc.ProviderModelSwitcher(context.Background(), "openai/gpt-5", "agentgateway")
	if err != nil || prov != "agentgateway" || name != "agentgateway/openai/gpt-5" {
		t.Errorf("ProviderModelSwitcher = %q on %q, %v", name, prov, err)
	}
}

// With no provider named, an unknown name is still a resolution error.
func TestResolveSwitchedModelUnknownWithoutProvider(t *testing.T) {
	if _, _, _, err := resolveSwitchedModel(config.Defaults(), "gemma-4-31b-it", ""); err == nil {
		t.Fatal("expected a resolution error without a provider")
	}
}
