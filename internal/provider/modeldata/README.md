# Embedded model catalog

This directory vendors a small compile-time snapshot used by `internal/provider`.

Source:
- Repository: https://github.com/simonw/llm-prices
- Data path: `data/openai.json`, `data/anthropic.json`
- Retrieved from the `main` branch during development for this change.
- Blob SHAs: `openai.json` 44ee7f1a4d3dd8403bf36d350e40f3e987903222, `anthropic.json` 8171c463aca64ccc619ec15bf7bc7a5917f2d8ed.

Usage:
- `llm-prices-openai.json` and `llm-prices-anthropic.json` provide known model IDs for validation.
- `context-windows.json` is a pi-go curated overlay. `llm-prices` is pricing-focused and does not reliably provide context-window metadata.
- `models-<provider>.json` are per-provider catalogs regenerated from live
  provider APIs by `make fetch-models` (which shells out to
  `pi model list <provider> -o json`). They are the embedded baseline for
  `CatalogFor`; the runtime pulls fresh catalogs into the XDG cache
  (`os.UserCacheDir()/pi-go/models/<provider>.json`) on a validation miss.

Update process:
1. Refresh the two `llm-prices-*.json` files from upstream.
2. Review new IDs and update `context-windows.json` where official context-window data is known.
3. Run `make fetch-models` to regenerate `models-<provider>.json` from live APIs.
4. Run `go test ./internal/provider`.
