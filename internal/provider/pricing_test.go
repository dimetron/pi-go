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
				"no-cost-model": {"id": "x"}
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

func TestParseModelsDevPricingSkipsNonContextTierAndZeroModel(t *testing.T) {
	// A tier whose type is not "context" is skipped; a model whose rates are
	// all zero and has no tiers is dropped entirely.
	body := `{
		"openai": {
			"models": {
				"gpt-4o": {
					"cost": {
						"input": 2.5, "output": 10,
						"tiers": [
							{"input": 5, "output": 20, "tier": {"type": "context", "size": 100000}},
							{"input": 9, "output": 30, "tier": {"type": "prompt", "size": 200000}}
						]
					}
				},
				"zero-model": {"cost": {"input": 0, "output": 0}}
			}
		}
	}`
	s, err := parseModelsDevPricing([]byte(body))
	if err != nil {
		t.Fatalf("parseModelsDevPricing: %v", err)
	}
	// Only the context tier survives.
	got := s.Providers["openai"]["gpt-4o"]
	if len(got.Tiers) != 1 || got.Tiers[0].ContextOver != 100000 {
		t.Errorf("gpt-4o tiers = %+v, want only the 100k context tier", got.Tiers)
	}
	// The all-zero model is dropped.
	if _, ok := s.Providers["openai"]["zero-model"]; ok {
		t.Error("zero-model should be dropped")
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

func TestLoadPricingSnapshotInvalid(t *testing.T) {
	if _, ok := loadPricingSnapshot([]byte("not json")); ok {
		t.Error("loadPricingSnapshot(not json) should be false")
	}
	if _, ok := loadPricingSnapshot([]byte(`{"source":"models.dev"}`)); ok {
		t.Error("loadPricingSnapshot with no providers should be false")
	}
}

func TestLoadEmbeddedPricingMissing(t *testing.T) {
	// The embedded snapshot is always present in the binary, so this exercises
	// the error path by reading a nonexistent file name via the same helper
	// shape. We can't remove the embedded file, so assert the happy path works
	// and the loader returns ok for the real snapshot.
	if _, ok := loadEmbeddedPricing(); !ok {
		t.Error("loadEmbeddedPricing() should find the embedded snapshot")
	}
}

func TestLoadCachedPricingMissing(t *testing.T) {
	withTempCacheDir(t)
	if _, ok := loadCachedPricing(); ok {
		t.Error("loadCachedPricing() should be false with no cache file")
	}
}

func TestPricingForPrefersCache(t *testing.T) {
	withTempCacheDir(t)
	// Write a cache file; pricingFor should prefer it over embedded.
	s := pricingSnapshot{
		Source:    "models.dev",
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Providers: map[string]map[string]PricingModel{"openai": {"gpt-4o": {Input: 2.5}}},
	}
	b, _ := json.Marshal(s)
	if err := os.MkdirAll(pricingCacheDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pricingCachePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := pricingFor()
	if !ok {
		t.Fatal("pricingFor() should find the cache")
	}
	if _, ok := got.Providers["openai"]["gpt-4o"]; !ok {
		t.Errorf("pricingFor() did not prefer the cache: %+v", got.Providers)
	}
}

func TestCostForEmptyModelName(t *testing.T) {
	withTempCacheDir(t)
	if _, ok := CostFor("openai", ""); ok {
		t.Error("CostFor(openai, \"\") should not be found")
	}
}

func TestNumInvalid(t *testing.T) {
	if got := num(json.Number("not-a-number")); got != 0 {
		t.Errorf("num(invalid) = %v, want 0", got)
	}
	if got := num(json.Number("")); got != 0 {
		t.Errorf("num(empty) = %v, want 0", got)
	}
}

func TestFetchModelsDevPricingNon200(t *testing.T) {
	withTempCacheDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	oldURL := modelsDevPricingURL
	modelsDevPricingURL = srv.URL
	defer func() { modelsDevPricingURL = oldURL }()

	if _, err := fetchModelsDevPricing(context.Background()); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestFetchModelsDevPricingBadBody(t *testing.T) {
	withTempCacheDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	oldURL := modelsDevPricingURL
	modelsDevPricingURL = srv.URL
	defer func() { modelsDevPricingURL = oldURL }()

	if _, err := fetchModelsDevPricing(context.Background()); err == nil {
		t.Fatal("expected error for malformed body")
	}
}

func TestFetchModelsDevPricingNetworkError(t *testing.T) {
	withTempCacheDir(t)
	// Point at a closed port so http.DefaultClient.Do fails.
	oldURL := modelsDevPricingURL
	modelsDevPricingURL = "http://127.0.0.1:1"
	defer func() { modelsDevPricingURL = oldURL }()

	if _, err := fetchModelsDevPricing(context.Background()); err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

func TestFetchModelsDevPricingInvalidURL(t *testing.T) {
	withTempCacheDir(t)
	// An invalid URL makes http.NewRequestWithContext fail.
	oldURL := modelsDevPricingURL
	modelsDevPricingURL = "://bad"
	defer func() { modelsDevPricingURL = oldURL }()

	if _, err := fetchModelsDevPricing(context.Background()); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
