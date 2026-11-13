package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/provider"
)

// fixture builds a fetcher that returns the given models per provider and
// fails for every other one, so a test exercises the same code path a real run
// does without a network or an API key.
func fixture(byProvider map[string][]provider.ModelInfo) liveFetcher {
	return func(name string) ([]provider.ModelInfo, error) {
		models, ok := byProvider[name]
		if !ok {
			return nil, errors.New("no key for " + name)
		}
		return models, nil
	}
}

// TestSectionsMatchFetchedCounts is the guard that stops the document and the
// catalog drifting: the count in the overview table and the number of rows in
// the section must both equal what the fetcher returned.
//
// This is the defect the hand-maintained document had — it claimed 11 Anthropic
// models while the catalog held 15 — so the assertion is on the number the
// reader sees, not on an internal variable.
func TestSectionsMatchFetchedCounts(t *testing.T) {
	models := []provider.ModelInfo{
		{ID: "model-a", ReleaseDate: "2026-01-02"},
		{ID: "model-b", ReleaseDate: "2026-01-01"},
		{ID: "model-c"},
	}
	doc, err := generate(t.TempDir(), fixture(map[string][]provider.ModelInfo{
		"anthropic": models,
	}))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(doc, "| [Anthropic](#anthropic) | 3 models |") {
		t.Errorf("overview row does not report 3 models:\n%s", doc)
	}
	if !strings.Contains(doc, "## Anthropic — 3 models") {
		t.Errorf("section heading does not report 3 models:\n%s", doc)
	}
	for _, m := range models {
		if !strings.Contains(doc, "| "+m.ID+" |") {
			t.Errorf("row for %q missing:\n%s", m.ID, doc)
		}
	}
}

// TestMissingProviderKeepsExistingSection pins the degrade path: a provider
// whose key is absent must not delete its section, or one missing key would
// strip a provider from the reference entirely.
func TestMissingProviderKeepsExistingSection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "MODELS.md", "# Supported Models\n\nLast updated: 2020-01-01\n\n"+
		"## Anthropic — 11 models\n\nAPI key: `ANTHROPIC_API_KEY`.\n\n"+
		"| Model | Release |\n|---|---|\n| claude-old | 2020-01-01 |\n\n"+
		"## Updating This File\n\nprose\n")

	doc, err := generate(root, func(string) ([]provider.ModelInfo, error) {
		return nil, errors.New("ANTHROPIC_API_KEY not set")
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(doc, "claude-old") {
		t.Errorf("existing Anthropic section was dropped:\n%s", doc)
	}
	if !strings.Contains(doc, "## Anthropic — 11 models") {
		t.Errorf("existing Anthropic count was lost:\n%s", doc)
	}
}

// TestAllProvidersFailIsAnError guards the case where nothing could be
// fetched: writing a document with no provider sections would replace a good
// reference with an empty one.
func TestAllProvidersFailIsAnError(t *testing.T) {
	_, err := generate(t.TempDir(), func(string) ([]provider.ModelInfo, error) {
		return nil, errors.New("no credentials")
	})
	if err == nil {
		t.Fatal("generate succeeded with no provider sections; want an error")
	}
}

// TestEmptyProviderIsUnavailable: an empty model list is a successful request
// that tells us nothing, so it must not render an empty section over a
// populated one.
func TestEmptyProviderIsUnavailable(t *testing.T) {
	_, _, err := liveSection(providers[0], func(string) ([]provider.ModelInfo, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("liveSection accepted an empty model list; want an error")
	}
}

// TestPriceDoesNotRepeatUnit checks the cell format: the column header already
// says "per 1M", so a cell repeating it is noise on every row.
func TestPriceDoesNotRepeatUnit(t *testing.T) {
	got := stripUnit("$2.00/$10.00 per 1M")
	if got != "$2.00/$10.00" {
		t.Errorf("stripUnit = %q, want %q", got, "$2.00/$10.00")
	}
	if got := stripUnit("—"); got != "—" {
		t.Errorf("stripUnit on a dash = %q, want the dash unchanged", got)
	}
}

// TestEmptyColumnsAreDropped pins the layout rule: a column that is empty in
// every row is omitted rather than rendered as a wall of em dashes. Ollama
// reports no release dates, so its Release column must disappear.
func TestEmptyColumnsAreDropped(t *testing.T) {
	section, _, err := liveSection(
		providerSpec{name: "ollama", title: "Ollama", count: "models", hasCtx: true, notes: "Capabilities"},
		fixture(map[string][]provider.ModelInfo{
			"ollama": {{ID: "m:latest", ContextWindow: 512, Capabilities: []string{"embedding"}}},
		}),
	)
	if err != nil {
		t.Fatalf("liveSection: %v", err)
	}
	if strings.Contains(section, "Release") {
		t.Errorf("all-empty Release column was kept:\n%s", section)
	}
	if !strings.Contains(section, "Context") {
		t.Errorf("populated Context column was dropped:\n%s", section)
	}
}

// TestSortIsNewestFirst: the document reads as a list of what is new, so
// models sort by release date descending and undated ones sink to the bottom.
func TestSortIsNewestFirst(t *testing.T) {
	section, _, err := liveSection(
		providerSpec{name: "xai", title: "xAI", count: "models"},
		fixture(map[string][]provider.ModelInfo{
			"xai": {
				{ID: "undated"},
				{ID: "older", ReleaseDate: "2025-01-01"},
				{ID: "newest", ReleaseDate: "2026-06-01"},
			},
		}),
	)
	if err != nil {
		t.Fatalf("liveSection: %v", err)
	}
	newest := strings.Index(section, "| newest |")
	older := strings.Index(section, "| older |")
	undated := strings.Index(section, "| undated |")
	if newest < 0 || older < 0 || undated < 0 {
		t.Fatalf("a row is missing:\n%s", section)
	}
	if !(newest < older && older < undated) {
		t.Errorf("rows are not newest-first: newest=%d older=%d undated=%d\n%s",
			newest, older, undated, section)
	}
}

// TestGatewayRoutesIncludeVirtualModels guards the config parse. The virtual
// models sit beside the routes under `llm`, not nested inside one — reading
// only the routes silently produced 17 of the 25 entries.
func TestGatewayRoutesIncludeVirtualModels(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "hack/agentgateway/config.yaml", `
llm:
  providers:
  - name: anthropic
  models:
  - name: anthropic/*
  - name: gpt-*
  virtualModels:
  - name: pi-fast
  - name: pi-default
`)
	routes, err := gatewayRoutes(root)
	if err != nil {
		t.Fatalf("gatewayRoutes: %v", err)
	}
	want := map[string]bool{"anthropic/*": true, "gpt-*": true, "pi-fast": true, "pi-default": true}
	if len(routes) != len(want) {
		t.Fatalf("routes = %v, want %d entries", routes, len(want))
	}
	for _, r := range routes {
		if !want[r] {
			t.Errorf("unexpected route %q", r)
		}
	}
}

func TestSlugMatchesGitHubAnchor(t *testing.T) {
	for _, tt := range []struct{ title, want string }{
		{"Anthropic", "anthropic"},
		{"Google Gemini", "google-gemini"},
		{"xAI", "xai"},
		{"Agentgateway", "agentgateway"},
	} {
		if got := slug(tt.title); got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

// TestHumanTokensMatchesCLI: the document and `pi model list` must not disagree
// about the same number, so this mirrors internal/cli's formatting.
func TestHumanTokensMatchesCLI(t *testing.T) {
	for _, tt := range []struct {
		in   int64
		want string
	}{
		{512, "512"},
		{262144, "262K"},
		{1_048_576, "1.05M"},
		{1_000_000, "1.00M"},
	} {
		if got := humanTokens(tt.in); got != tt.want {
			t.Errorf("humanTokens(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
