# Implementation Plan: OpenCode Go provider

Gates (discovered during research):
- **build**: `go build ./...`
- **test**: `go test ./internal/provider/... ./internal/config/... ./internal/cli/...`
- **vet**: `go vet ./...`
- **lint**: `golangci-lint run ./...`

## Vertical Slices

### Slice 1: Catalog + constructor (`internal/provider/opencode.go`)

- [ ] Create `internal/provider/opencode.go` with:
  - `const opencodeDefaultBaseURL = "https://opencode.ai/zen/go/v1"`
  - `var opencodeGoModelCatalog = map[string]string{...}` mapping each OpenCode Go model ID to
    `"chat"`, `"responses"`, or `"messages"` (full catalog from design.md).
  - `func opencodeAnthropicBaseURL(baseURL string) string` — strips trailing `/v1`.
  - `func NewOpenCode(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error)`:
    - default baseURL to `opencodeDefaultBaseURL` when empty
    - look up catalog family by `modelName`; unknown → error
    - "chat"/"responses" → `NewOpenAI(ctx, modelName, apiKey, baseURL, opts)`
    - "messages" → `NewAnthropic(ctx, modelName, apiKey, opencodeAnthropicBaseURL(baseURL), thinkingLevel, opts)`

- [ ] Add `internal/provider/opencode_test.go` unit tests:
  - routing: `kimi-k3` → `*openaiModel` (chat), `gpt-5.6-luna` → `*openaiModel`, `minimax-m3` → `*anthropicModel`
  - `opencodeAnthropicBaseURL("https://opencode.ai/zen/go/v1") == "https://opencode.ai/zen/go"`
  - unknown model → error
  - Verify the openai delegate's base URL is `.../v1` and anthropic delegate base is `.../zen/go`.

**Verify:** `go test ./internal/provider/ -run OpenCode` && `go build ./...`

---

### Slice 2: Provider registry (`internal/provider/provider.go`)

- [ ] Add `"opencode": "opencode"` to `modelPrefixes`.
- [ ] Add explicit `opencode/` prefix handling in `Resolve` (mirror `ollama/`/`azure/`):
  return `Info{Provider: "opencode", Model: stripped, Custom: false}`.
- [ ] Add `case "opencode":` in `NewLLM` → `NewOpenCode(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)`.

- [ ] Add tests:
  - `Resolve("opencode/kimi-k3")` → `Info{Provider:"opencode", Model:"kimi-k3"}`
  - `NewLLM` dispatches `opencode` provider (via the Slice-1 routing tests / a provider test).

**Verify:** `go test ./internal/provider/ -run 'OpenCode|Resolve'` && `go build ./...`

---

### Slice 3: Config (`internal/config/config.go`)

- [ ] `APIKeys()`: add `"opencode": {"OPENCODE_API_KEY"}`.
- [ ] `BaseURLs()`: add `"opencode": "OPENCODE_BASE_URL"`.
- [ ] `autoDetectProvider`: add `opencode/` prefix → `"opencode"`.
- [ ] Confirm `Config.BaseURLs` (config.json `baseURLs`) supports `opencode` via `ResolveBaseURLs` (no schema change needed; add a test).

- [ ] Add `internal/config/config_test.go` tests:
  - `APIKeys()["opencode"]` reads `OPENCODE_API_KEY`.
  - `BaseURLs()["opencode"]` reads `OPENCODE_BASE_URL`.
  - `autoDetectProvider("opencode/kimi-k3") == "opencode"`.
  - `ResolveBaseURLs` env wins over config `baseURLs.opencode`.

**Verify:** `go test ./internal/config/...` && `go build ./...`

---

### Slice 4: Model listing (`internal/provider/list_models.go`)

- [ ] `ListModels`: add `case "opencode": return listOpenCodeModels(ctx, opts)`.
- [ ] Add `listOpenCodeModels`: GET `{base}/v1/models` with Bearer auth, decode
  `{"data":[{"id","object","owned_by"}]}` (mirror `listOpenAIModels`).
- [ ] `fetchJSON`: add `case "opencode":` → `Authorization: Bearer <key>`.
- [ ] `providerDefaultBaseURL`: add `case "opencode": return opencodeDefaultBaseURL`.

- [ ] Add tests:
  - `listOpenCodeModels` decodes the `/models` payload shape.
  - `fetchJSON` sets Bearer auth for `opencode`.
  - `providerDefaultBaseURL("opencode")` returns the default.

**Verify:** `go test ./internal/provider/ -run 'ListModels|OpenCode'` && `go build ./...`

---

### Slice 5: CLI model list (`internal/cli/model.go`)

- [ ] `allProviders`: add `"opencode"`.
- [ ] Provider-name switch in `runModelList`: add `case "opencode": providers = []string{"opencode"}`.

- [ ] Add test: `runModelList` accepts `opencode` as an argument (and `allProviders` includes it).

**Verify:** `go test ./internal/cli/ -run ModelList` && `go build ./...`

---

### Slice 6: End-to-end + full gates

- [ ] Add a guarded E2E test (mirroring `mistral_e2e_test.go`) that:
  - skips when `OPENCODE_API_KEY` is unset
  - runs a chat model (e.g. `kimi-k3`) and a messages model (e.g. `minimax-m3`) through
    `NewLLM`/`NewOpenCode` and asserts a non-empty response.

- [ ] Run full gates: `go build ./... && go vet ./... && go test ./...` (optionally `golangci-lint run ./...`).

**Verify:** full `go test ./...` (or `make test`) passes; `golangci-lint run ./...` clean.
