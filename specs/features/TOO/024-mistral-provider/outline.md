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
| 5 | Model-list enrichment + `-o json` (card parser, richer mistral-only print, JSON output mode) | `internal/provider/list_models.go`, `internal/cli/model.go`, `list_models_test.go`, `model_test.go` | yes (after 1) |
| 6 | Refreshable catalog: CatalogFor/RefreshCatalog + XDG cache | `internal/provider/catalog.go` (new), `catalog_test.go` (new) | yes (after 5) |
| 7 | Wire catalog into ValidateModel (validation-miss refresh) | `internal/provider/provider.go`, `catalog.go`, `provider_test.go` | no (depends on 6) |
| 8 | Makefile `fetch-models` (shells out to `pi model list -o json`) + embedded JSON regeneration | `Makefile`, `scripts/fetch-models.sh`, `internal/provider/modeldata/*.json`, `modeldata/README.md` | yes (after 5/6; repo data) |
| 9 | E2E tests (reasoning + cache, gated) | `internal/provider/mistral_e2e_test.go` | yes (after 4) |

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
    ContextWindow int64    `json:"context_window,omitempty"` // max_context_length from card
    Capabilities  []string `json:"capabilities,omitempty"` // e.g. completion_chat, vision
}
func listMistralModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) // card parse

// internal/cli/model.go
func printProviderModels(providerName string, models []provider.ModelInfo) // extra columns only for mistral
// NEW: `pi model list [-o json]` — JSON mode emits {"provider","fetched_at","models":[...]}
// per provider; this is the fetch-to-repo mechanism (make fetch-models shells out to it).

// internal/provider/catalog.go (new)
func CatalogFor(provider string) []string                          // XDG cache ?? embedded
func RefreshCatalog(ctx context.Context, providerName string, opts ListModelsOptions) error

// internal/provider/provider.go
func ValidateModel(info Info) error // consults CatalogFor; refresh on miss when key present
```

## Order of Changes & Testing

1. **Slice 1** — routing fix. Verify: `go test ./internal/provider ./internal/config`.
2. **Slice 2** — NewMistral signature + NewLLM wiring. Verify: `go test ./internal/provider`.
3. **Slice 3** — reasoning/cache injection. Verify: `go test ./internal/provider`.
4. **Slice 4** — thinking extraction hooks. Verify: `go test ./internal/provider`.
5. **Slice 5** — model-list enrichment + `-o json`. Verify: `go test ./internal/provider ./internal/cli`. (parallel-safe after 1)
6. **Slice 6** — catalog manager + XDG cache. Verify: `go test ./internal/provider`. (parallel-safe after 5)
7. **Slice 7** — ValidateModel integration. Verify: `go test ./internal/provider ./internal/cli`.
8. **Slice 8** — Makefile `fetch-models` + embedded JSON. Verify: `make fetch-models`, `go build ./...`. (parallel-safe after 6)
9. **Slice 9** — e2e tests. Verify: `go test -tags e2e ./internal/provider -run Mistral`, `make test`. (parallel-safe after 4)

Gates throughout: `go vet ./...`, `golangci-lint run ./...`, `go test ./...`.
