# Design: OpenCode Go provider

## Current State

pi-go's `internal/provider` package exposes a `model.LLM` factory `NewLLM` that switches on
`info.Provider`. Existing providers: ollama, gemini, openai (chat completions + responses +
codex backend), azure, anthropic, mistral. Configuration lives in `internal/config`:
`APIKeys()`, `BaseURLs()`, `ResolveBaseURLs()`, `autoDetectProvider`. The CLI (`internal/cli`)
resolves a role → provider → key/baseURL and calls `NewLLM`. Model listing is centralized in
`provider.ListModels` and exposed via `pi model list`.

OpenCode Go is not yet integrated. It is an HTTP LLM provider at
`https://opencode.ai/zen/go/v1` using `OPENCODE_API_KEY`, serving models across three endpoint
families (OpenAI chat/completions, OpenAI responses, Anthropic messages).

## Desired End State

A new `opencode` provider lets users select any OpenCode Go model via the `opencode/<model>`
prefix (e.g. `opencode/kimi-k3`, `opencode/gpt-5.6-luna`, `opencode/minimax-m3`). It routes
each model to its native endpoint family automatically and reuses pi-go's existing
OpenAI/Anthropic request, streaming, tool, and multi-turn handling.

## Architecture Overview

```
cli / config
   │  provider=opencode, model=<bare id>, key, baseURL
   ▼
provider.NewLLM ── case "opencode" ──► NewOpenCode(model, key, baseURL, thinking, opts)
                                             │
                          model→endpoint catalog (hardcoded)
                                             │
        ┌────────────────────────────────────┼────────────────────────────┐
        ▼ chat/completions                   ▼ responses                  ▼ messages
   NewOpenAI(..., base=…/v1)           NewOpenAI(..., base=…/v1)    NewAnthropic(..., base=…/zen/go)
        │                                   │                            │
        └────────────── model.LLM ──────────┴──────────── model.LLM ─────┘
                             │
                             ▼
              guardrail-wrap / agent loop (unchanged)
```

`NewOpenCode` returns the appropriate delegate `model.LLM` directly (a delegating wrapper type
is not required, because routing is decided once at construction time based on the model ID).
The `opencode/` prefix is stripped in `Resolve`/`autoDetectProvider` so delegates receive the
bare model ID (e.g. `kimi-k3`), matching how `ollama/` and `azure/` prefixes are handled.

## Components & Interfaces

### New file: `internal/provider/opencode.go`

```go
package provider

// opencodeDefaultBaseURL is the OpenCode Go API base (OpenAI-family paths append /v1; the
// Anthropic SDK already includes v1/ in its request paths, so it gets the parent URL).
const opencodeDefaultBaseURL = "https://opencode.ai/zen/go/v1"

// opencodeGoEndpointFamily is the per-model endpoint routing catalog.
// Values: "chat" (OpenAI chat/completions), "responses" (OpenAI responses), "messages" (Anthropic).
var opencodeGoModelCatalog = map[string]string{
    "grok-4.5":          "chat",
    "glm-5.2":           "chat",
    "glm-5.1":           "chat",
    "glm-5":             "chat",
    "kimi-k3":           "chat",
    "kimi-k2.7-code":    "chat",
    "kimi-k2.6":         "chat",
    "kimi-k2.5":         "chat",
    "deepseek-v4-pro":   "chat",
    "deepseek-v4-flash": "chat",
    "mimo-v2-pro":       "chat",
    "mimo-v2-omni":      "chat",
    "mimo-v2.5-pro":     "chat",
    "mimo-v2.5":         "chat",
    "hy3":               "chat",
    "hy3-preview":       "chat",
    "gpt-5.6-luna":      "responses",
    "minimax-m3":        "messages",
    "minimax-m2.7":      "messages",
    "minimax-m2.5":      "messages",
    "qwen3.8-max":       "messages",
    "qwen3.7-max":       "messages",
    "qwen3.7-plus":      "messages",
    "qwen3.6-plus":      "messages",
    "qwen3.5-plus":      "messages",
}

// NewOpenCode creates an OpenCode Go model.LLM, routing the model to its endpoint family.
func NewOpenCode(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error)
```

Behavior of `NewOpenCode`:
1. Default `baseURL` to `opencodeDefaultBaseURL` when empty.
2. Look up `opencodeGoModelCatalog[modelName]` to select the family.
   - Unknown model → `fmt.Errorf("unknown OpenCode Go model %q", modelName)` (validation).
3. **chat / responses** → `return NewOpenAI(ctx, modelName, apiKey, baseURL, opts)`.
   - `NewOpenAI` with base `https://opencode.ai/zen/go/v1` → SDK hits `.../v1/chat/completions`
     and `.../v1/responses`; `option.WithAPIKey` sets `Authorization: Bearer`.
   - `modelNeedsResponses("gpt-5.6-luna")` already returns true → the openai model routes it to
     responses; all catalog "chat" models return false → routed to chat/completions. This
     matches the catalog, so `NewOpenAI`'s internal routing can be relied upon. (A future
     catalog model that needs responses but isn't in the openai responses list would require
     forcing the endpoint; not needed today.)
   - Note: `NewOpenAI`'s `codexBackend` detection is disabled because a non-empty baseURL is
     always passed (`useCodexBackend := baseURL == "" && ...`).
4. **messages** → `return NewAnthropic(ctx, modelName, apiKey, opencodeAnthropicBaseURL(baseURL), thinkingLevel, opts)`.
   - The Anthropic SDK appends `v1/messages`, so pass the **parent** URL
     `https://opencode.ai/zen/go` (strip the trailing `/v1`).
   - `option.WithAPIKey` on the Anthropic SDK sets `x-api-key` (verified). `isAnthropicOAuthToken`
     is false for an OpenCode Go key, so plain `WithAPIKey` is used.

Helper:
```go
// opencodeAnthropicBaseURL strips the trailing /v1 so the Anthropic SDK's own v1/messages path lands on the right URL.
func opencodeAnthropicBaseURL(baseURL string) string {
    return strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
}
```

### `internal/provider/provider.go` changes
- `modelPrefixes`: add `"opencode": "opencode"`.
- `Resolve`: handle `opencode/` prefix → `Info{Provider: "opencode", Model: stripped, Custom: false}`.
  (Add explicit prefix check before/alongside `modelPrefixes` loop, mirroring `ollama/`/`azure/`.)
- `NewLLM`: add `case "opencode": return NewOpenCode(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)`.

### `internal/config/config.go` changes
- `APIKeys()`: add `"opencode": {"OPENCODE_API_KEY"}`.
- `BaseURLs()`: add `"opencode": "OPENCODE_BASE_URL"`.
- `autoDetectProvider`: add `opencode/` prefix → `"opencode"`.
- `Config.BaseURLs` (config.json `baseURLs`) already supports arbitrary providers; `ResolveBaseURLs`
  picks up an opencode entry automatically — no schema change.

### `internal/provider/list_models.go` changes
- `ListModels`: add `case "opencode": return listOpenCodeModels(ctx, opts)`.
- New `listOpenCodeModels`: GET `{base}/v1/models` with `Authorization: Bearer`, decode
  `{"data":[{"id","object","owned_by"}]}` (mirrors `listOpenAIModels`).
- `fetchJSON`: add `case "opencode":` → Bearer auth (matches OpenAI family).
- `providerDefaultBaseURL`: add `case "opencode": return opencodeDefaultBaseURL`.

### `internal/cli/model.go` changes
- `allProviders`: add `"opencode"`.
- The provider-name switch in `runModelList`: add `case "opencode"`.

## Data Model

No persistent data model changes. The hardcoded `opencodeGoModelCatalog` map is the source of
truth for endpoint routing; the runtime `GET /v1/models` supplies the available-model list for
`pi model list opencode`.

## Patterns Followed

- **Delegate reuse**: mirrors how `NewLLM` already delegates to per-provider constructors;
  OpenCode Go reuses `NewOpenAI`/`NewAnthropic` rather than duplicating request logic.
- **Prefix stripping**: mirrors `ollama/` and `azure/` handling in `Resolve`.
- **Config**: mirrors existing env-var key/baseURL maps in `config.APIKeys()`/`BaseURLs()`.
- **Model listing**: mirrors `listOpenAIModels` + `fetchJSON` provider auth switch.
- **Validation**: unknown provider models error at construction, matching other providers.

## Error Handling Strategy

- Missing API key: handled upstream in CLI (`providerEnvVar` returns `OPENCODE_API_KEY`).
- Unknown model ID → clear error from `NewOpenCode` listing valid catalog models.
- Network/API errors propagate from the underlying OpenAI/Anthropic SDKs (already surfaced
  with body capture in the openai `errorBodyLoggingTransport`).

## Acceptance Criteria

### Provider construction & routing
- Given `NewLLM` with provider `opencode` and model `kimi-k3`, when called, then it returns an
  OpenAI chat-completions-backed `model.LLM`.
- Given model `gpt-5.6-luna`, then it returns an OpenAI responses-backed `model.LLM`.
- Given model `minimax-m3`, then it returns an Anthropic messages-backed `model.LLM`.
- Given an unknown `opencode/<id>`, then `NewOpenCode` returns an error.

### Model routing & generation
- Given a request for `opencode/kimi-k3`, when `GenerateContent` runs, then a chat/completions
  request is sent to `https://opencode.ai/zen/go/v1/chat/completions` with `Authorization: Bearer`.
- Given a request for `opencode/minimax-m3`, when `GenerateContent` runs, then a messages request
  is sent to `https://opencode.ai/zen/go/v1/messages` with `x-api-key`.
- Given a request with tools, when streamed, then tool calls round-trip through the delegate.

### Config & listing
- Given `OPENCODE_API_KEY` is set, then `config.APIKeys()["opencode"]` returns it.
- Given `OPENCODE_BASE_URL` is set, then `config.BaseURLs()["opencode"]` returns it (and wins
  over config.json `baseURLs.opencode`).
- Given `pi model list opencode`, then it lists models fetched from the runtime `/models`
  endpoint.
- Given `pi model list` (no arg) with an opencode key configured, then opencode is included.

## Testing Strategy

- **Unit tests** (`internal/provider/opencode_test.go`):
  - Catalog routing: each family maps to the right constructor (inspect the returned concrete
    type via type assertion to `*openaiModel` / `*anthropicModel`, and their base URL / model name).
  - `opencodeAnthropicBaseURL` strips `/v1` correctly.
  - Unknown model error.
- **Config tests** (`internal/config/config_test.go`): `APIKeys`/`BaseURLs` return opencode entries.
- **ListModels tests**: `listOpenCodeModels` decodes the `/models` payload shape; `fetchJSON`
  sends Bearer auth; `providerDefaultBaseURL("opencode")` returns the default.
- **CLI test**: `runModelList` accepts `opencode`.
- **Live E2E (manual/guarded)**: a chat model and a messages model against the real API, guarded
  by `OPENCODE_API_KEY` presence (skip if unset), mirroring existing `mistral_e2e_test.go`.
