package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// catalogFile is the on-disk shape of a per-provider model catalog, both in the
// XDG cache and in the checked-in modeldata/ files produced by `make
// fetch-models`. It matches the `pi model list <provider> -o json` output so the
// two are interchangeable.
type catalogFile struct {
	Provider  string      `json:"provider"`
	FetchedAt string      `json:"fetched_at"`
	Models    []ModelInfo `json:"models"`
}

// modelsCacheDir returns os.UserCacheDir()/pi-go/models. Falls back to "" when
// UserCacheDir errors (then caching is disabled, embedded only).
func modelsCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "pi-go", "models")
}

// cachePath returns the per-provider cache file path.
func cachePath(provider string) string {
	return filepath.Join(modelsCacheDir(), provider+".json")
}

// CatalogFor returns known model prefixes for a provider: XDG cache first (if
// present), else the embedded modeldata/models-<provider>.json snapshot merged
// with the hard-coded KnownModels list, else the hard-coded list alone.
//
// The merge is a union: the hard-coded list carries curated compatibility
// entries (e.g. bare "codestral", "pixtral", "ministral") that the live
// catalog does not, and the live catalog adds dated models the hard-coded
// list predates. A union is a superset, so nothing that validated before
// stops validating.
func CatalogFor(provider string) []string {
	if ids, ok := loadCatalogIDs(cachePath(provider)); ok {
		return ids
	}
	embedded, ok := loadEmbeddedCatalogIDs(provider)
	if !ok {
		return KnownModels[provider]
	}
	return uniqueSorted(append(embedded, KnownModels[provider]...))
}

// loadEmbeddedCatalogIDs reads the checked-in modeldata/models-<provider>.json
// snapshot (produced by `make fetch-models`) and returns its model IDs
// lowercased and sorted. ok is false when the file is absent.
func loadEmbeddedCatalogIDs(provider string) ([]string, bool) {
	b, err := modelCatalogFS.ReadFile("modeldata/models-" + provider + ".json")
	if err != nil {
		return nil, false
	}
	var cf catalogFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, false
	}
	ids := make([]string, 0, len(cf.Models))
	for _, m := range cf.Models {
		ids = append(ids, strings.ToLower(m.ID))
	}
	return uniqueSorted(ids), true
}

// loadCatalogIDs reads a catalog file and returns its model IDs lowercased and
// sorted. ok is false when the file is missing or unreadable.
func loadCatalogIDs(path string) ([]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var cf catalogFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, false
	}
	ids := make([]string, 0, len(cf.Models))
	for _, m := range cf.Models {
		ids = append(ids, strings.ToLower(m.ID))
	}
	return uniqueSorted(ids), true
}

// RefreshCatalog fetches live models for a provider (via ListModels) and
// persists them to the XDG cache. Writes atomically (temp file + rename).
// Returns the fetched []ModelInfo. On fetch error returns the error (caller
// falls back to cache/embedded).
func RefreshCatalog(ctx context.Context, providerName string, opts ListModelsOptions) ([]ModelInfo, error) {
	models, err := ListModels(ctx, providerName, opts)
	if err != nil {
		return nil, err
	}
	dir := modelsCacheDir()
	if dir == "" {
		return models, nil // caching disabled; still return the fetched list
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return models, fmt.Errorf("creating models cache dir: %w", err)
	}
	cf := catalogFile{
		Provider:  providerName,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Models:    models,
	}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return models, fmt.Errorf("encoding catalog: %w", err)
	}
	path := cachePath(providerName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return models, fmt.Errorf("writing catalog: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return models, fmt.Errorf("renaming catalog: %w", err)
	}
	return models, nil
}
