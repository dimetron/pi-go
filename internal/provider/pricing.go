package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// modelsDevPricingURL is the models.dev API endpoint that publishes per-model
// token pricing for every provider. It is the single source of truth for the
// embedded snapshot and the runtime refresh. A variable so tests can point it
// at a local server.
var modelsDevPricingURL = "https://models.dev/api.json"

// pricingCacheFile is the on-disk cache file name under the pi-go models cache
// dir. It holds the same shape as the embedded snapshot, so a fresh pull
// replaces the embedded data without a code change.
const pricingCacheFile = "modelsdev-pricing.json"

// pricingSnapshot is the embedded and cached shape of the models.dev pricing
// data. Providers are keyed by pi-go's provider names (openai, anthropic,
// gemini, mistral, xai, azure, openrouter); each model maps to its per-million
// token rates in USD.
type pricingSnapshot struct {
	Source    string                             `json:"source"`
	FetchedAt string                             `json:"fetched_at"`
	Providers map[string]map[string]PricingModel `json:"providers"`
}

// PricingModel holds a model's per-million-token rates in USD. All fields are
// optional; a provider may omit cache rates or reasoning. Tiers carry the
// context-over threshold at which the higher rate applies.
type PricingModel struct {
	Input      float64       `json:"input,omitempty"`
	Output     float64       `json:"output,omitempty"`
	CacheRead  float64       `json:"cache_read,omitempty"`
	CacheWrite float64       `json:"cache_write,omitempty"`
	Tiers      []PricingTier `json:"tiers,omitempty"`
}

// PricingTier is a context-length tier: rates that apply once the prompt
// exceeds contextOver tokens.
type PricingTier struct {
	ContextOver int64   `json:"context_over"`
	Input       float64 `json:"input,omitempty"`
	Output      float64 `json:"output,omitempty"`
	CacheRead   float64 `json:"cache_read,omitempty"`
	CacheWrite  float64 `json:"cache_write,omitempty"`
}

// pricingCacheDir returns the directory that holds the runtime pricing cache,
// reusing the same XDG cache location as the model catalogs. Falls back to ""
// when UserCacheDir errors (then caching is disabled, embedded only).
func pricingCacheDir() string {
	return modelsCacheDir()
}

// pricingCachePath returns the full path to the pricing cache file.
func pricingCachePath() string {
	return filepath.Join(pricingCacheDir(), pricingCacheFile)
}

// loadPricingSnapshot reads a pricing snapshot from the given bytes. ok is
// false when the data is absent or malformed.
func loadPricingSnapshot(b []byte) (pricingSnapshot, bool) {
	var s pricingSnapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return s, false
	}
	if s.Source == "" || len(s.Providers) == 0 {
		return s, false
	}
	return s, true
}

// loadEmbeddedPricing reads the checked-in modeldata/modelsdev-pricing.json
// snapshot. ok is false when the file is absent or malformed.
func loadEmbeddedPricing() (pricingSnapshot, bool) {
	b, err := modelCatalogFS.ReadFile("modeldata/modelsdev-pricing.json")
	if err != nil {
		return pricingSnapshot{}, false
	}
	return loadPricingSnapshot(b)
}

// loadCachedPricing reads the runtime pricing cache from disk. ok is false when
// the file is absent or malformed.
func loadCachedPricing() (pricingSnapshot, bool) {
	b, err := os.ReadFile(pricingCachePath())
	if err != nil {
		return pricingSnapshot{}, false
	}
	return loadPricingSnapshot(b)
}

// pricingFor returns the pricing snapshot to use for cost estimation: the
// runtime cache first (it is freshest), else the embedded snapshot. ok is false
// when neither is available.
func pricingFor() (pricingSnapshot, bool) {
	if s, ok := loadCachedPricing(); ok {
		return s, true
	}
	return loadEmbeddedPricing()
}

// CostFor returns the per-million-token USD rates for a model served by a
// provider, using prefix matching so a dated model ID (gpt-5.6-sol) resolves
// against its base entry (gpt-5.6). ok is false when the provider or model is
// unknown.
func CostFor(providerName, modelName string) (PricingModel, bool) {
	s, ok := pricingFor()
	if !ok {
		return PricingModel{}, false
	}
	models, ok := s.Providers[strings.ToLower(strings.TrimSpace(providerName))]
	if !ok {
		return PricingModel{}, false
	}
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" {
		return PricingModel{}, false
	}
	// Exact match first, then longest prefix match.
	if m, ok := models[lower]; ok {
		return m, true
	}
	best := ""
	for id := range models {
		if strings.HasPrefix(lower, id) && len(id) > len(best) {
			best = id
		}
	}
	if best == "" {
		return PricingModel{}, false
	}
	return models[best], true
}

// RefreshPricing fetches a fresh pricing snapshot from models.dev and persists
// it to the XDG cache, regardless of the current snapshot's age. It is the
// force-refresh path used by the /model-price-refresh command.
func RefreshPricing(ctx context.Context) error {
	s, err := fetchModelsDevPricing(ctx)
	if err != nil {
		return err
	}
	return writePricingCache(s)
}

// fetchModelsDevPricing downloads and parses the models.dev pricing API into a
// snapshot keyed by pi-go's provider names.
func fetchModelsDevPricing(ctx context.Context) (pricingSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevPricingURL, nil)
	if err != nil {
		return pricingSnapshot{}, fmt.Errorf("building models.dev request: %w", err)
	}
	req.Header.Set("User-Agent", "pi-go")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return pricingSnapshot{}, fmt.Errorf("fetching models.dev pricing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pricingSnapshot{}, fmt.Errorf("models.dev pricing returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return pricingSnapshot{}, fmt.Errorf("reading models.dev pricing: %w", err)
	}
	return parseModelsDevPricing(body)
}

// parseModelsDevPricing converts the raw models.dev API body into the compact
// snapshot shape, keeping only the providers pi-go supports and only the rate
// fields cost estimation needs.
func parseModelsDevPricing(body []byte) (pricingSnapshot, error) {
	var api map[string]struct {
		Models map[string]struct {
			Cost *struct {
				Input      json.Number `json:"input"`
				Output     json.Number `json:"output"`
				CacheRead  json.Number `json:"cache_read"`
				CacheWrite json.Number `json:"cache_write"`
				Tiers      []struct {
					Input      json.Number `json:"input"`
					Output     json.Number `json:"output"`
					CacheRead  json.Number `json:"cache_read"`
					CacheWrite json.Number `json:"cache_write"`
					Tier       struct {
						Type string `json:"type"`
						Size int64  `json:"size"`
					} `json:"tier"`
				} `json:"tiers"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &api); err != nil {
		return pricingSnapshot{}, fmt.Errorf("parsing models.dev pricing: %w", err)
	}

	// models.dev source id -> pi-go provider name.
	sourceToProvider := map[string]string{
		"openai":     "openai",
		"anthropic":  "anthropic",
		"google":     "gemini",
		"mistral":    "mistral",
		"xai":        "xai",
		"azure":      "azure",
		"openrouter": "openrouter",
	}

	s := pricingSnapshot{
		Source:    "models.dev",
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Providers: map[string]map[string]PricingModel{},
	}
	for srcID, provider := range sourceToProvider {
		src, ok := api[srcID]
		if !ok {
			continue
		}
		models := map[string]PricingModel{}
		for modelID, m := range src.Models {
			if m.Cost == nil {
				continue
			}
			pm := PricingModel{
				Input:      num(m.Cost.Input),
				Output:     num(m.Cost.Output),
				CacheRead:  num(m.Cost.CacheRead),
				CacheWrite: num(m.Cost.CacheWrite),
			}
			for _, t := range m.Cost.Tiers {
				if t.Tier.Type != "context" || t.Tier.Size <= 0 {
					continue
				}
				pm.Tiers = append(pm.Tiers, PricingTier{
					ContextOver: t.Tier.Size,
					Input:       num(t.Input),
					Output:      num(t.Output),
					CacheRead:   num(t.CacheRead),
					CacheWrite:  num(t.CacheWrite),
				})
			}
			if pm.Input == 0 && pm.Output == 0 && pm.CacheRead == 0 && pm.CacheWrite == 0 && len(pm.Tiers) == 0 {
				continue
			}
			models[modelID] = pm
		}
		if len(models) > 0 {
			s.Providers[provider] = models
		}
	}
	if len(s.Providers) == 0 {
		return pricingSnapshot{}, fmt.Errorf("models.dev returned no supported priced models")
	}
	return s, nil
}

// num converts a json.Number to a float64, returning 0 for absent or invalid
// values.
func num(n json.Number) float64 {
	if n == "" {
		return 0
	}
	f, err := n.Float64()
	if err != nil {
		return 0
	}
	return f
}

// writePricingCache persists a pricing snapshot to the XDG cache atomically
// (temp file + rename), mirroring RefreshCatalog's write pattern.
func writePricingCache(s pricingSnapshot) error {
	dir := pricingCacheDir()
	if dir == "" {
		return nil // caching disabled; the embedded snapshot still serves
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating pricing cache dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding pricing snapshot: %w", err)
	}
	path := pricingCachePath()
	tmp, err := os.CreateTemp(dir, "modelsdev-pricing.*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating pricing temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing pricing cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing pricing cache: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("writing pricing cache: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming pricing cache: %w", err)
	}
	return nil
}
