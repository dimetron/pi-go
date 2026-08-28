package provider

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// catalogProviders is the canonical set of providers that must ship an embedded
// modeldata/models-<provider>.json snapshot. Ollama is deliberately absent: its
// model list is whatever the local daemon has pulled, so a checked-in snapshot
// would describe one developer's machine. ValidateModel exempts Ollama for the
// same reason.
var catalogProviders = []string{
	"anthropic",
	"openai",
	"gemini",
	"mistral",
	"xai",
	"openrouter",
}

// TestEmbeddedCatalogPresentForEveryProvider is the guard that a `make
// fetch-models` run which silently skipped a provider (missing API key, failed
// request) cannot land. Before this test, models-anthropic.json and
// models-openai.json were both absent from the tree while the other four were
// committed, and nothing failed.
func TestEmbeddedCatalogPresentForEveryProvider(t *testing.T) {
	for _, p := range catalogProviders {
		t.Run(p, func(t *testing.T) {
			b, err := modelCatalogFS.ReadFile("modeldata/models-" + p + ".json")
			if err != nil {
				t.Fatalf("missing embedded catalog for %s: %v (run `make fetch-models`)", p, err)
			}
			var cf catalogFile
			if err := json.Unmarshal(b, &cf); err != nil {
				t.Fatalf("models-%s.json is not valid JSON: %v", p, err)
			}
			if cf.Provider != p {
				t.Errorf("models-%s.json provider = %q, want %q", p, cf.Provider, p)
			}
			if len(cf.Models) == 0 {
				t.Fatalf("models-%s.json has no models", p)
			}
			seen := make(map[string]struct{}, len(cf.Models))
			for _, m := range cf.Models {
				if strings.TrimSpace(m.ID) == "" {
					t.Errorf("models-%s.json has a model with an empty id", p)
					continue
				}
				if _, dup := seen[m.ID]; dup {
					t.Errorf("models-%s.json has duplicate id %q", p, m.ID)
				}
				seen[m.ID] = struct{}{}
			}
			if !slices.IsSortedFunc(cf.Models, func(a, b ModelInfo) int { return strings.Compare(a.ID, b.ID) }) {
				t.Errorf("models-%s.json models are not sorted by id; `make fetch-models` normalizes them", p)
			}
		})
	}
}

// TestEmbeddedCatalogFeedsCatalogFor pins the reason the snapshots exist: every
// one of them must actually reach CatalogFor. openrouter is the load-bearing
// case — it has no KnownModels entry, so without its snapshot CatalogFor returns
// nothing and ValidateModel skips validation entirely.
func TestEmbeddedCatalogFeedsCatalogFor(t *testing.T) {
	withTempCacheDir(t)
	for _, p := range catalogProviders {
		embedded, ok := loadEmbeddedCatalogIDs(p)
		if !ok || len(embedded) == 0 {
			t.Errorf("loadEmbeddedCatalogIDs(%s) returned nothing", p)
			continue
		}
		ids := CatalogFor(p)
		for _, want := range embedded {
			if !slices.Contains(ids, want) {
				t.Errorf("CatalogFor(%s) missing embedded id %q", p, want)
				break
			}
		}
	}
}

// TestFetchModelsScriptCoversEveryProvider keeps scripts/fetch-models.sh and
// catalogProviders from drifting: a provider added to one and not the other
// yields either an unregenerated snapshot or an unfetched provider.
func TestFetchModelsScriptCoversEveryProvider(t *testing.T) {
	b, err := os.ReadFile("../../scripts/fetch-models.sh")
	if err != nil {
		t.Skipf("fetch-models.sh unreadable: %v", err)
	}
	m := regexp.MustCompile(`(?m)^PROVIDERS="([^"]*)"`).FindSubmatch(b)
	if m == nil {
		t.Fatal(`no PROVIDERS="..." assignment in scripts/fetch-models.sh`)
	}
	got := strings.Fields(string(m[1]))
	want := slices.Clone(catalogProviders)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("fetch-models.sh PROVIDERS = %v, catalogProviders = %v", got, want)
	}
}
