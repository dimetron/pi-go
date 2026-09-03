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

  All six providers must be present: `anthropic`, `openai`, `gemini`,
  `mistral`, `xai`, `openrouter`. `TestEmbeddedCatalogPresentForEveryProvider`
  enforces this, because `make fetch-models` skips a provider whose API key is
  missing and would otherwise leave the gap silent. `openrouter` is the
  load-bearing one: it has no hard-coded `KnownModels` entry, so without its
  snapshot `CatalogFor` returns nothing and `ValidateModel` stops validating
  OpenRouter models altogether.

  `ollama` is deliberately excluded. Its model list is whatever the local
  daemon has pulled, so a checked-in snapshot would describe one developer's
  machine; `ValidateModel` exempts Ollama for the same reason.

- `modelsdev-pricing.json` is a compact snapshot of
  https://models.dev/api.json, keeping only the providers pi-go supports
  (`openai`, `anthropic`, `gemini`, `mistral`, `xai`, `azure`, `openrouter`)
  and only the per-million-token USD rate fields cost estimation needs. It is
  the embedded baseline for `CostFor`; the `/model-price-refresh` slash command
  pulls a fresh copy from the same endpoint into the XDG cache
  (`os.UserCacheDir()/pi-go/models/modelsdev-pricing.json`) on demand.
  Regenerate the embedded snapshot with `make fetch-modelsdev-pricing`.

Update process:
1. Refresh the two `llm-prices-*.json` files from upstream.
2. Review new IDs and update `context-windows.json` where official context-window data is known.
3. Run `make fetch-models` to regenerate `models-<provider>.json` from live APIs.
   It needs every provider's API key. Keys are read the way the CLI reads them:
   `$HOME/.pi-go/.env` first, then the nearest `.pi-go/.env` walking up from the
   working directory, which wins. A provider whose key is absent is skipped with
   a note rather than failing the target — check the output for `skip` lines.
4. Run `make fetch-modelsdev-pricing` to regenerate `modelsdev-pricing.json`
   from models.dev. It needs no API key.
5. Run `go test ./internal/provider`.
