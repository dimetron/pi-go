package provider

import (
	"slices"
	"strings"
	"testing"
)

// TestAzureWindowsDoNotLeakIntoOpenAI is the whole reason the catalog is
// provider-scoped: 15 of the 28 Azure deployments share a name with an OpenAI
// model but were provisioned with a different window. A flattened table makes
// the winner depend on map iteration order.
func TestAzureWindowsDoNotLeakIntoOpenAI(t *testing.T) {
	cases := []struct {
		model       string
		azureWant   int64
		openaiWant  int64
		description string
	}{
		{"gpt-5.1", 272_000, 1_050_000, "Azure provisions less than OpenAI"},
		{"gpt-5.6-luna", 1_050_000, 272_000, "Azure provisions more than OpenAI"},
		{"gpt-5", 250_000, 400_000, ""},
		{"gpt-5.4", 900_000, 272_000, ""},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			if got := ContextWindowSizeFor("azure", tc.model); got != tc.azureWant {
				t.Errorf("ContextWindowSizeFor(azure, %q) = %d, want %d", tc.model, got, tc.azureWant)
			}
			if got := ContextWindowSizeFor("openai", tc.model); got != tc.openaiWant {
				t.Errorf("ContextWindowSizeFor(openai, %q) = %d, want %d", tc.model, got, tc.openaiWant)
			}
			// The provider-less path must keep answering for OpenAI exactly as
			// it did before Azure existed.
			if got := ContextWindowSize(tc.model); got != tc.openaiWant {
				t.Errorf("ContextWindowSize(%q) = %d, want the OpenAI value %d", tc.model, got, tc.openaiWant)
			}
		})
	}
}

// The azure/ prefix is stripped by Resolve before lookup, but a caller that
// passes the unstripped name must not silently get 0.
func TestContextWindowSizeStripsAzurePrefix(t *testing.T) {
	if got := ContextWindowSizeFor("azure", "azure/gpt-5.6-luna"); got != 1_050_000 {
		t.Errorf("got %d, want 1050000 with the azure/ prefix present", got)
	}
}

// An uncataloged deployment must resolve to something, because 0 disables
// auto-compaction and lets the session grow until the API rejects it.
//
// Azure deployment names are free-form, and suffixing the model name
// ("gpt-5.6-luna-prod") is the common convention, so prefix matching is what
// makes those resolve to the Azure window rather than OpenAI's.
func TestAzureUnknownDeploymentResolves(t *testing.T) {
	cases := map[string]int64{
		"gpt-5.6-luna-prod":    1_050_000, // suffixed name keeps the Azure window
		"gpt-4o-brand-new":     128_000,   // gpt-4o family guard, not the 8K gpt-4
		"gpt-5.1-eastus2":      272_000,
		"totally-unknown-name": 0,
	}
	for deployment, want := range cases {
		got := ContextWindowSizeFor("azure", deployment)
		if got != want {
			t.Errorf("ContextWindowSizeFor(azure, %q) = %d, want %d", deployment, got, want)
		}
	}
}

// The bare "gpt-4" entry is a real 8K deployment, but it is also a prefix of
// every gpt-4o, gpt-4.1, gpt-4-turbo and gpt-4-32k name. Without an entry of
// their own, those resolved to 8K and compacted up to 16x too early.
//
// "gpt-4-turbo-128k" does not cover a deployment named plainly "gpt-4-turbo":
// the catalog key is longer than the name, so it cannot prefix-match it, and
// the lookup fell through to "gpt-4".
func TestAzureGPT4PrefixDoesNotSwallowLongerNames(t *testing.T) {
	cases := map[string]int64{
		"gpt-4":              8_000,
		"gpt-4o-autox":       128_000,
		"gpt-4.1":            1_000_000,
		"gpt-4-turbo":        128_000,
		"gpt-4-turbo-128k":   128_000,
		"gpt-4-turbo-eastus": 128_000,
		"gpt-4-32k":          32_768,
	}
	for model, want := range cases {
		if got := ContextWindowSizeFor("azure", model); got != want {
			t.Errorf("ContextWindowSizeFor(azure, %q) = %d, want %d", model, got, want)
		}
	}
}

func TestContextWindowSizeForUnknownProviderFallsBack(t *testing.T) {
	if got, want := ContextWindowSizeFor("nonesuch", "gpt-5.1"), ContextWindowSize("gpt-5.1"); got != want {
		t.Errorf("unknown provider = %d, want the flat-table value %d", got, want)
	}
}

// Longest-prefix resolution has to survive names that are prefixes of others.
func TestAzureLongestPrefixWins(t *testing.T) {
	cases := map[string]int64{
		"gpt-5":              250_000,
		"gpt-5-chat":         128_000,
		"gpt-5-mini":         250_000,
		"gpt-5.1":            272_000,
		"gpt-5.1-codex":      272_000,
		"gpt-5.1-codex-mini": 272_000,
		"o1":                 200_000,
		"o1-mini":            128_000,
		"o3-mini":            200_000,
		"gpt-4":              8_000,
		"gpt-4o-mini":        128_000,
	}
	for model, want := range cases {
		if got := ContextWindowSizeFor("azure", model); got != want {
			t.Errorf("ContextWindowSizeFor(azure, %q) = %d, want %d", model, got, want)
		}
	}
}

func TestAzureDeployments(t *testing.T) {
	got := AzureDeployments()
	// Every key in the azure block of context-windows.json: the deployments
	// themselves plus the family guards ("gpt-4o", "gpt-4-turbo", "gpt-4-32k")
	// that keep the 8K "gpt-4" entry from swallowing longer names.
	if len(got) != 31 {
		t.Errorf("AzureDeployments() returned %d entries, want 31", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name >= got[i].Name {
			t.Fatalf("not sorted: %q before %q", got[i-1].Name, got[i].Name)
		}
	}
	for _, d := range got {
		if d.ContextWindow <= 0 {
			t.Errorf("deployment %q has window %d", d.Name, d.ContextWindow)
		}
	}
}

// The credential check comes first, because it is the candidate whose 200
// means something; the legacy deployment route is the wider-served fallback.
func TestAzureProbePathsNativeResource(t *testing.T) {
	got := AzureProbePaths("gpt-5.6-luna", "2025-04-01-preview", "https://my-res.openai.azure.com")
	want := []string{
		"/openai/models?api-version=2025-04-01-preview",
		"/openai/deployments/gpt-5.6-luna?api-version=2025-04-01-preview",
	}
	if !slices.Equal(got, want) {
		t.Errorf("AzureProbePaths = %q, want %q", got, want)
	}
}

// Without a deployment name there is nothing the second candidate could ask
// about, so it must not be emitted as a bare /openai/deployments listing.
func TestAzureProbePathsNoDeployment(t *testing.T) {
	got := AzureProbePaths("", "v1", "https://my-res.openai.azure.com")
	if len(got) != 1 || got[0] != "/openai/models?api-version=v1" {
		t.Errorf("AzureProbePaths with no deployment = %q, want just the models route", got)
	}
}

// A compat gateway must not get deployment paths or api-version — the same
// carve-out NewAzureOpenAI makes for real traffic. One candidate only: there
// is no Azure-shaped route to fall back to.
func TestAzureProbePathsCompatProxy(t *testing.T) {
	got := AzureProbePaths("gpt-5.6-luna", "", "https://gw.corp.internal/openai/v1")
	if len(got) != 1 || got[0] != "/models" {
		t.Errorf("AzureProbePaths on a compat proxy = %q, want [/models]", got)
	}
	for _, p := range got {
		if strings.Contains(p, "api-version") {
			t.Errorf("compat proxy probe %q must not carry api-version", p)
		}
	}
}

func TestAzureProbePathsDefaultsAPIVersion(t *testing.T) {
	t.Setenv("OPENAI_API_VERSION", "")
	got := AzureProbePaths("dep", "", "https://my-res.openai.azure.com")
	for _, p := range got {
		if !strings.Contains(p, DefaultAzureAPIVersion) {
			t.Errorf("AzureProbePaths entry %q lacks the default api-version %q", p, DefaultAzureAPIVersion)
		}
	}
}

func TestAzureProbePathsEscapesDeployment(t *testing.T) {
	got := AzureProbePaths("my dep/weird", "v1", "https://my-res.openai.azure.com")
	for _, p := range got {
		if strings.Contains(p, " ") {
			t.Errorf("AzureProbePaths entry %q, want the deployment percent-escaped", p)
		}
	}
}

// Ping and the real client must agree on which env var supplies the key.
// config.APIKeys already reads all three, so this is not a new capability —
// it is the fallback chain living in one place instead of two that can drift.
func TestAzureAPIKeyFallbackChain(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	t.Setenv("AZUREOPENAI_API_KEY", "")
	t.Setenv("AZURE_API_KEY", "third")
	if got := AzureAPIKey(""); got != "third" {
		t.Errorf("AzureAPIKey() = %q, want the AZURE_API_KEY fallback", got)
	}
	if got := AzureAPIKey("explicit"); got != "explicit" {
		t.Errorf("AzureAPIKey(explicit) = %q, want the explicit value to win", got)
	}
}

func TestAzureEndpointFallsBackToEnv(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://from-env.openai.azure.com")
	if got := AzureEndpoint(""); got != "https://from-env.openai.azure.com" {
		t.Errorf("AzureEndpoint() = %q, want the env value", got)
	}
	if got := AzureEndpoint("https://explicit"); got != "https://explicit" {
		t.Errorf("AzureEndpoint(explicit) = %q, want the explicit value", got)
	}
}
