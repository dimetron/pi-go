# Research: Model Catalog & Validation

## 1. KnownModels / loadKnownModels (model_catalog.go)

- `provider.go:217` — `var KnownModels = mustLoadKnownModels()` (package init, panics on error).
- `loadKnownModels()` (model_catalog.go:66-139) builds `map[string][]string`:
  - Hard-coded lists: `anthropic: nil`, `openai: nil`, `gemini` (13), `mistral` (11), `xai` (7).
  - anthropic/openai then filled from embedded llm-prices JSON via
    `loadLLMPricesModelIDs` (line 125-131), filtered by `shouldIncludeKnownModel`.
  - `compatibilityModelAliases` appended (anthropic 23, openai 7).
  - Final `uniqueSorted` (lowercase/trim/dedupe/sort).

**Mistral exact list (model_catalog.go:91-107):**
`mistral-large-2512, mistral-large-latest, mistral-medium-2603, mistral-medium-latest,
mistral-small-2603, mistral-small-latest, magistral-medium-latest,
magistral-small-latest, codestral, pixtral, ministral`

## 2. modeldata embedding

- `//go:embed modeldata/*.json` → `modelCatalogFS embed.FS` (model_catalog.go:11-12).
- Files: `llm-prices-openai.json` (42 models), `llm-prices-anthropic.json` (17),
  `context-windows.json`, `README.md` (not embedded).
- llm-prices shape: `{"vendor","models":[{"id","name","price_history":[...]}]}`.
- context-windows.json: `map[provider]map[prefix]int64` tokens — providers
  anthropic(28), azure(31), gemini(10), local(3), mistral(9), openai(28),
  opencode(1), xai(7). Mistral bucket missing `pixtral` and `ministral`.
- README: vendored from github.com/simonw/llm-prices with pinned blob SHAs;
  update process manual (refresh → review → go test ./internal/provider).

## 3. ValidateModel (provider.go:301-317)

1. `if info.Ollama || info.Custom { return nil }`.
2. `known, ok := KnownModels[info.Provider]; if !ok { return nil }`.
3. `strings.HasPrefix(strings.ToLower(info.Model), prefix)` — **prefix-based**.
4. Else: `fmt.Errorf("unknown %s model %q; known models: %s", info.Provider, info.Model, strings.Join(known, ", "))`.

`Info` struct (provider.go:165-179): Provider, Model, Ollama, LocalOllama, Custom, BaseURL.

## 4. list_models.go

- `ModelInfo{ID string; OwnedBy string json:"owned_by,omitempty"}` (list_models.go:16-19).
- `ListModelsOptions{APIKey, BaseURL, Insecure}`.
- `listBearerModels` envelope: `{"data":[{"id","owned_by"}]}` (lines 136-141).
  Endpoint: baseURL (or `providerDefaultBaseURL`) trimmed + `/v1/models` unless
  base already ends `/v1` (then `/models`).
- `listMistralModels` = `listBearerModels(ctx, opts, "mistral", "Mistral")` (lines 102-105).
- `providerDefaultBaseURL("mistral") = "https://api.mistral.ai"`.
- `fetchJSON(ctx, method, url, opts ListModelsOptions, providerName string, dst any) error`
  (line 229): 30s timeout; bearer auth for openai/mistral/xai/openrouter;
  non-200 → `"API returned %d: %s"` truncated to 200 chars.
- Other listers: anthropic `{"data":[{"id","type"}]}` (OwnedBy=Type); gemini
  `/v1beta/models` with `{"models":[{"name","displayName"}]}`.

## 5. Context windows

- `var contextWindowSizes = mustLoadContextWindowSizes()` (flat, excludes azure).
- `var contextWindowSizesByProvider = mustLoadContextWindowSizesByProvider()` (per-vendor, keeps azure).
- `ContextWindowSize(modelName)` → `longestPrefixSize`; 0 = unknown.
- `ContextWindowSizeFor(providerName, modelName)`: provider bucket first, fallback
  to vendor-agnostic (so Azure deployments named after OpenAI models still resolve).
- `longestPrefixSize` strips `ollama/`/`azure/` prefixes, keeps **longest** matching prefix.
- `AzureDeployments()` returns sorted `[]ModelWindow` from azure bucket.

## 6. cli/model.go display

- `printProviderModels(providerName, []ModelInfo)`: sorts by ID; header
  `"%s (%d models):\n"`; per model `"  %-45s  %s\n"` (ID, OwnedBy) or `"  %s\n"`
  when OwnedBy empty; trailing blank line.
- `allProviders` (model.go:60): anthropic, openai, gemini, mistral, xai, ollama, openrouter.
- `runModelListCapture` test harness; `TestRunModelList_Mistral` asserts output
  contains `mistral-large` from `{"data":[{"id":"mistral-large","owned_by":"mistral"}]}`.
