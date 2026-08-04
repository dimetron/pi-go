# Outline: OpenCode Go provider

## Slices

1. **Catalog + constructor** — `internal/provider/opencode.go`:
   - `opencodeDefaultBaseURL`, `opencodeGoModelCatalog`, `opencodeAnthropicBaseURL`, `NewOpenCode`.
   - Routes to `NewOpenAI` (chat/responses) or `NewAnthropic` (messages).
2. **Provider registry** — `internal/provider/provider.go`:
   - `modelPrefixes` += `opencode`, `Resolve` handles `opencode/`, `NewLLM` case `opencode`.
3. **Config** — `internal/config/config.go`:
   - `APIKeys()` += `opencode:{OPENCODE_API_KEY}`; `BaseURLs()` += `opencode:OPENCODE_BASE_URL`;
     `autoDetectProvider` handles `opencode/`.
4. **Model listing** — `internal/provider/list_models.go`:
   - `ListModels` case `opencode`, `listOpenCodeModels`, `fetchJSON` Bearer case,
     `providerDefaultBaseURL` case.
5. **CLI** — `internal/cli/model.go`:
   - `allProviders` += `opencode`, provider switch case.
6. **Tests** — provider unit tests, config tests, list tests, CLI test, guarded E2E.

## Order & Testing

Each slice compiles and passes its own tests before moving on:
1. Constructor + catalog (unit tests: routing, baseURL strip, unknown model).
2. Registry (NewLLM dispatch + Resolve prefix tests).
3. Config (APIKeys/BaseURLs/autoDetect tests).
4. Listing (listOpenCodeModels, fetchJSON auth, defaultBaseURL).
5. CLI (`pi model list opencode`).
6. E2E (guarded by OPENCODE_API_KEY).

## Key Signatures

```go
// internal/provider/opencode.go
func NewOpenCode(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error)
func opencodeAnthropicBaseURL(baseURL string) string
const opencodeDefaultBaseURL = "https://opencode.ai/zen/go/v1"
var opencodeGoModelCatalog = map[string]string{ ... } // model -> "chat"|"responses"|"messages"

// internal/provider/list_models.go
func listOpenCodeModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error)
```
