# Outline: Mistral Provider (features/TOO/024-mistral-provider)

High-level slices for the implementation plan. Each slice compiles and tests
independently; order is dependency-driven.

## Slice Map

| # | Slice | Files | Parallel-safe |
|---|-------|-------|---------------|
| 1 | Routing fix: `mistral/` prefix strip + config auto-detect | `internal/provider/provider.go`, `internal/config/config.go`, their `_test.go` | no (first) |
| 2 | Mistral chat: thinkingLevel param + reasoning/cache injection | `internal/provider/mistral.go`, `internal/provider/provider.go` (NewLLM case), `mistral_test.go` | no (depends on 1 for clean tests) |
| 3 | Mistral thinking extraction (stream + non-stream hooks) | `internal/provider/mistral.go`, `mistral_test.go` | no (same file as 2; merge with 2) |
| 4 | Mistral model-list enrichment (dedicated parser + richer print) | `internal/provider/list_models.go`, `internal/cli/model.go`, `list_models_test.go`, `model_test.go` | yes (after 1) |
| 5 | Refreshable catalog: CatalogFor/RefreshCatalog + XDG cache | `internal/provider/catalog.go` (new), `model_catalog.go`, `catalog_test.go` (new) | no (depends on 1 for validation integration) |
| 6 | Wire catalog into ValidateModel (validation-miss refresh) | `internal/provider/provider.go`, `catalog.go` | no (depends on 5) |
| 7 | Makefile `fetch-models` + embedded JSON regeneration | `Makefile`, `internal/provider/modeldata/*.json`, `scripts/` (if needed) | yes (after 5; repo data) |
| 8 | E2E tests (reasoning + cache, gated) | `internal/provider/mistral_e2e_test.go` | yes (after 2/3) |

## Key Type Signatures (new/changed)

```go
// internal/provider/mistral.go
func NewMistral(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, llmOpts *LLMOptions) (model.LLM, error)

type mistralModel struct {
    modelName     string
    client        openai.Client
    thinkingLevel string
    promptCacheKey string // uuid.NewString() per instance (xAI precedent)
}

func mistralReasoningEffort(modelName, level string) string // "" = omit
func mistralUsesReasoningEffort(modelName string) bool      // small-2603/latest, medium-3.5
func mistralDeltaThinking(rawChunk string) string           // streaming hook
func mistralMessageThinking(rawResponse string) string      // non-streaming hook

// internal/provider/provider.go
func Resolve(modelName string) (Info, error) // adds mistral/ strip
// NewLLM case "mistral": NewMistral(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)

// internal/config/config.go
var modelPrefixes = map[string]string{ /* + "mistral":"mistral", "magistral":"mistral" */ }

// internal/provider/list_models.go
type ModelInfo struct {
    ID            string
    OwnedBy       string
    ContextWindow int64    `json:"-"` // max_context_length from card
    Capabilities  []string `json:"-"` // e.g. completion_chat, vision
}
func listMistralModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) // card parse

// internal/cli/model.go
func printProviderModels(providerName string, models []provider.ModelInfo) // extra columns only for mistral

// internal/provider/catalog.go (new)
func CatalogFor(provider string) []string                          // XDG cache ?? embedded
func RefreshCatalog(ctx context.Context, providerName string, opts ListModelsOptions) error
func FetchCatalogToRepo(ctx context.Context, providers []string) error // used by make fetch-models

// internal/provider/provider.go
func ValidateModel(info Info) error // consults CatalogFor; refresh on miss when key present
```

## Order of Changes & Testing

1. **Slice 1** — routing fix. Verify: `go test ./internal/provider ./internal/config` (new tests: mistral/ strip, config auto-detect).
2. **Slices 2+3 (merged)** — Mistral chat fields + thinking hooks. Verify: `go test ./internal/provider` (request-body capture tests, thinking extraction tests).
3. **Slice 4** — model list. Verify: `go test ./internal/provider ./internal/cli`.
4. **Slice 5** — catalog manager + XDG cache. Verify: `go test ./internal/provider` (temp XDG dir tests).
5. **Slice 6** — ValidateModel integration. Verify: `go test ./internal/provider ./internal/cli`.
6. **Slice 7** — Makefile fetch-models + regenerated JSON. Verify: `make fetch-models` (optional keys), `go build ./...`.
7. **Slice 8** — e2e tests. Verify: `go test -tags e2e ./internal/provider/ -run Mistral` (skips without key) and `make test`.

Gates throughout: `go vet ./...`, `golangci-lint run ./...`, `go test ./...`.
