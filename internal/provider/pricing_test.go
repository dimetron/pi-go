package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCostForExactMatch(t *testing.T) {
	withTempCacheDir(t)
	m, ok := CostFor("openai", "gpt-5.6-sol")
	if !ok {
		t.Fatal("CostFor(openai, gpt-5.6-sol) not found")
	}
	if m.Input != 4 || m.Output != 20 {
		t.Errorf("gpt-5.6-sol rates = %+v, want input 4 output 20", m)
	}
	if len(m.Tiers) != 1 || m.Tiers[0].ContextOver != 272000 {
		t.Errorf("gpt-5.6-sol tiers = %+v, want one 272k tier", m.Tiers)
	}
}

func TestCostForPrefixMatch(t *testing.T) {
	withTempCacheDir(t)
	// A dated model ID resolves against its base entry.
	m, ok := CostFor("anthropic", "claude-opus-4-7-20260101")
	if !ok {
		t.Fatal("CostFor(anthropic, claude-opus-4-7-20260101) not found")
	}
	if m.Input != 5 || m.Output != 25 {
		t.Errorf("claude-opus-4-7 rates = %+v, want input 5 output 25", m)
	}
}

func TestCostForUnknownProviderOrModel(t *testing.T) {
	withTempCacheDir(t)
	if _, ok := CostFor("nonexistent", "x"); ok {
		t.Error("CostFor(nonexistent, x) should not be found")
	}
	if _, ok := CostFor("openai", "definitely-not-a-model"); ok {
		t.Error("CostFor(openai, definitely-not-a-model) should not be found")
	}
}

func TestCostForCaseInsensitive(t *testing.T) {
	withTempCacheDir(t)
	m, ok := CostFor("OpenAI", "GPT-5.6-SOL")
	if !ok {
		t.Fatal("CostFor(OpenAI, GPT-5.6-SOL) not found")
	}
	if m.Input != 4 {
		t.Errorf("rates = %+v, want input 4", m)
	}
}

func TestParseModelsDevPricing(t *testing.T) {
	body := `{
		"openai": {
			"models": {
				"gpt-4o": {
					"cost": {"input": 2.5, "output": 10, "cache_read": 1.25}
				},
				"gpt-5.6-sol": {
					"cost": {
						"input": 4, "output": 20, "cache_read": 0.4, "cache_write": 5,
						"tiers": [{"input": 8, "output": 30, "tier": {"type": "context", "size": 272000}}]
					}
				},
				"no-cost-model": {"id": "x", "modalities": {"output": ["text"]}}
			}
		},
		"anthropic": {
			"models": {
				"claude-opus-4-7": {"cost": {"input": 5, "output": 25, "cache_read": 0.5, "cache_write": 6.25}}
			}
		},
		"unsupported-provider": {
			"models": {"foo": {"cost": {"input": 1, "output": 1}}}
		}
	}`
	s, err := parseModelsDevPricing([]byte(body))
	if err != nil {
		t.Fatalf("parseModelsDevPricing: %v", err)
	}
	if s.Source != "models.dev" {
		t.Errorf("source = %q, want models.dev", s.Source)
	}
	if _, ok := s.Providers["openai"]; !ok {
		t.Fatal("openai provider missing")
	}
	if _, ok := s.Providers["gemini"]; ok {
		t.Error("gemini should be absent (no google source in body)")
	}
	// Unsupported providers are dropped.
	if _, ok := s.Providers["unsupported-provider"]; ok {
		t.Error("unsupported-provider should be dropped")
	}
	// Models without cost are dropped.
	if _, ok := s.Providers["openai"]["no-cost-model"]; ok {
		t.Error("no-cost-model should be dropped")
	}
	// Tier parsed.
	sol := s.Providers["openai"]["gpt-5.6-sol"]
	if len(sol.Tiers) != 1 || sol.Tiers[0].ContextOver != 272000 || sol.Tiers[0].Input != 8 {
		t.Errorf("gpt-5.6-sol tiers = %+v", sol.Tiers)
	}
}

func TestParseModelsDevPricingEmpty(t *testing.T) {
	if _, err := parseModelsDevPricing([]byte(`{"openai": {"models": {}}}`)); err == nil {
		t.Error("expected error for no supported priced models")
	}
}

func TestParseModelsDevPricingReleaseAndDeprecated(t *testing.T) {
	body := `{
		"openai": {
			"models": {
				"gpt-5.5": {
					"release_date": "2026-04-23",
					"cost": {"input": 5, "output": 30}
				},
				"gpt-image-1": {
					"release_date": "2025-04-24",
					"status": "deprecated"
				}
			}
		}
	}`
	s, err := parseModelsDevPricing([]byte(body))
	if err != nil {
		t.Fatalf("parseModelsDevPricing: %v", err)
	}
	// Priced model carries its release date and is not deprecated.
	gpt := s.Providers["openai"]["gpt-5.5"]
	if gpt.ReleaseDate != "2026-04-23" {
		t.Errorf("gpt-5.5 release_date = %q, want 2026-04-23", gpt.ReleaseDate)
	}
	if gpt.Deprecated {
		t.Error("gpt-5.5 should not be deprecated")
	}
	// Deprecated no-cost model is kept, with no price and the flag set.
	img := s.Providers["openai"]["gpt-image-1"]
	if !img.Deprecated {
		t.Error("gpt-image-1 should be deprecated")
	}
	if img.ReleaseDate != "2025-04-24" {
		t.Errorf("gpt-image-1 release_date = %q, want 2025-04-24", img.ReleaseDate)
	}
	if img.hasPrice() {
		t.Error("gpt-image-1 should have no price")
	}
}

func TestModelReleaseDate(t *testing.T) {
	withTempCacheDir(t)
	if got := ModelReleaseDate("openai", "gpt-5.5"); got != "2026-04-23" {
		t.Errorf("ModelReleaseDate(openai, gpt-5.5) = %q, want 2026-04-23", got)
	}
	// Unknown model has no date.
	if got := ModelReleaseDate("openai", "definitely-not-a-model"); got != "" {
		t.Errorf("ModelReleaseDate(openai, unknown) = %q, want empty", got)
	}
}

func TestShouldFilterModel(t *testing.T) {
	withTempCacheDir(t)
	ref := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	// gpt-image-1 is deprecated, released 2025-04-24 (>1y before ref), no price.
	if !ShouldFilterModel("openai", "gpt-image-1", ref) {
		t.Error("gpt-image-1 should be filtered (deprecated, old, unpriced)")
	}
	// A priced deprecated model is not filtered.
	if ShouldFilterModel("openai", "gpt-5.5", ref) {
		t.Error("gpt-5.5 should not be filtered (it is priced)")
	}
	// A recent deprecated model is not filtered.
	if ShouldFilterModel("openai", "gpt-image-1", time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("gpt-image-1 should not be filtered when reference is within a year of release")
	}
	// Unknown model is not filtered.
	if ShouldFilterModel("openai", "definitely-not-a-model", ref) {
		t.Error("unknown model should not be filtered")
	}
}

func TestRefreshPricingFetches(t *testing.T) {
	withTempCacheDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"openai": {"models": {"gpt-4o": {"cost": {"input": 2.5, "output": 10}}}}}`))
	}))
	defer srv.Close()
	oldURL := modelsDevPricingURL
	modelsDevPricingURL = srv.URL
	defer func() { modelsDevPricingURL = oldURL }()

	if err := RefreshPricing(context.Background()); err != nil {
		t.Fatalf("RefreshPricing: %v", err)
	}
	// Cache file written with fresh fetched_at.
	b, err := os.ReadFile(pricingCachePath())
	if err != nil {
		t.Fatalf("cache file: %v", err)
	}
	var got pricingSnapshot
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("cache file not valid JSON: %v", err)
	}
	ft, _ := time.Parse(time.RFC3339, got.FetchedAt)
	if time.Since(ft) > time.Minute {
		t.Errorf("fetched_at = %s, want recent", got.FetchedAt)
	}
	if _, ok := got.Providers["openai"]["gpt-4o"]; !ok {
		t.Errorf("cache missing gpt-4o: %+v", got.Providers)
	}
}

func TestRefreshPricingFetchError(t *testing.T) {
	withTempCacheDir(t)
	// Server returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	oldURL := modelsDevPricingURL
	modelsDevPricingURL = srv.URL
	defer func() { modelsDevPricingURL = oldURL }()

	if err := RefreshPricing(context.Background()); err == nil {
		t.Fatal("expected error from failed fetch")
	}
	// No cache file written on failure.
	if _, err := os.Stat(pricingCachePath()); !os.IsNotExist(err) {
		t.Errorf("cache file should not exist after failed fetch, stat err = %v", err)
	}
}

func TestPricingCachePathUsesModelsCacheDir(t *testing.T) {
	dir := withTempCacheDir(t)
	if got := pricingCachePath(); got != filepath.Join(dir, pricingCacheFile) {
		t.Errorf("pricingCachePath() = %q, want %q", got, filepath.Join(dir, pricingCacheFile))
	}
}

func TestModelTextOutput(t *testing.T) {
	withTempCacheDir(t)

	// A chat model emits text.
	text, ok := ModelTextOutput("openai", "gpt-4o")
	if !ok {
		t.Fatal("ModelTextOutput(openai, gpt-4o) not found")
	}
	if !text {
		t.Error("gpt-4o should report text output")
	}

	// An image generator carries no text_output flag, so it reports false —
	// which is what keeps it out of the model listing.
	text, ok = ModelTextOutput("openai", "gpt-image-1")
	if !ok {
		t.Fatal("ModelTextOutput(openai, gpt-image-1) not found")
	}
	if text {
		t.Error("gpt-image-1 should not report text output")
	}

	// An unknown model is not classified either way, so callers keep it.
	if _, ok := ModelTextOutput("openai", "definitely-not-a-model"); ok {
		t.Error("unknown model should report ok=false")
	}
	if _, ok := ModelTextOutput("nonexistent", "x"); ok {
		t.Error("unknown provider should report ok=false")
	}
}
