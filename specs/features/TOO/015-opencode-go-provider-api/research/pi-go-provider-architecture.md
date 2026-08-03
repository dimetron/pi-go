# Research: pi-go provider architecture

## Provider registry (internal/provider/provider.go)

- `Info{Provider, Model, Ollama, Custom}` — describes provider + model.
- `modelPrefixes` map routes model prefixes to providers:
  `claude→anthropic`, `gpt→openai`, `gpt-5→openai`, `gemini→gemini`, `mistral→mistral`.
  (Note: config.go has its own smaller copy — see below.)
- `Resolve(modelName)` returns `Info`. It special-cases `ollama/`, `azure/`, `:cloud`,
  `-cloud`, then falls back to `modelPrefixes`. Unknown models error.
- `ResolveWithBaseURL(modelName, baseURL)` routes unrecognized models to a custom
  OpenAI-compatible endpoint when a baseURL is given.
- `NewLLM(ctx, info, apiKey, baseURL, thinkingLevel, opts)` switches on `info.Provider`
  to construct the `model.LLM`:
  - `ollama`, `gemini`, `openai`, `azure`, `anthropic`, `mistral`.
  - default → `unsupported provider` error.
- `ValidateModel(info)` checks model name against `KnownModels` catalog. Returns nil if
  `info.Ollama || info.Custom` (dynamic). Unknown provider → nil (skip validation).
- `KnownModels`/`ContextWindowSize` are loaded from embedded modeldata JSON snapshots.

### To add `opencode` provider
1. Add `"opencode"` handling in `Resolve` / `ResolveWithBaseURL` (or via a `modelPrefixes`
   entry) so `opencode/<id>` → `Info{Provider:"opencode", Model:"<id>"}`.
2. Add `case "opencode":` in `NewLLM` → `NewOpenCode(...)`.
3. `ValidateModel`: decide whether opencode models are validated against the hardcoded
   catalog or treated as dynamic.

## config package (internal/config/config.go)

- `RoleConfig{Model, Provider, AdvisorModel, ...}`; `Config.Roles` map; `DefaultProvider`.
- `modelPrefixes` (local copy): `claude→anthropic`, `gpt→openai`, `gpt-5→openai`,
  `gemini→gemini`. Used by `autoDetectProvider`.
- `APIKeys()` returns map[provider]key from env vars. Providers: anthropic, openai, azure,
  gemini, mistral, ollama. Add `"opencode": {"OPENCODE_API_KEY"}`.
- `BaseURLs()` returns map[provider]baseURL from env. Add `"opencode": "OPENCODE_BASE_URL"`.
- `ResolveBaseURLs()` merges config `BaseURLs` map + env (env wins).
- Dotenv: `.pi-go/.env` is loaded via `loadEnvFileFrom` (used for MCP var substitution) and
  `loadDotEnv()` in cli (sets os env). `OPENCODE_API_KEY` already present in `.pi-go/.env`.
- `Config.BaseURLs map[string]string` (JSON `baseURLs`) supports per-provider base URL
  overrides in config.json, merged with env.

### To add `opencode`
1. `APIKeys()`: add `"opencode": {"OPENCODE_API_KEY"}`.
2. `BaseURLs()`: add `"opencode": "OPENCODE_BASE_URL"`.
3. `autoDetectProvider`: add `opencode/` prefix → `"opencode"`.
4. `Config.BaseURLs` (config.json `baseURLs`) already supports arbitrary providers via the
   map — no schema change needed; `ResolveBaseURLs` picks it up automatically.

## CLI wiring (internal/cli)

- `cli.go`: resolves role → provider, then `baseURL = baseURLs[providerName]` (from
  `cfg.ResolveBaseURLs()`), calls `provider.ResolveWithBaseURL`, overrides
  `info.Provider`, then `keys := config.APIKeys(); apiKey := keys[info.Provider]`.
  Rejects with `no API key found for provider ... (set <envVar>)` via `providerEnvVar(p)`.
  `providerEnvVar` defaults to `strings.ToUpper(p) + "_API_KEY"` → for opencode that yields
  `OPENCODE_API_KEY` automatically. Then calls `provider.NewLLM`.
- `loadDotEnv()` loads `.pi-go/.env` before resolving keys (so `OPENCODE_API_KEY` from the
  dotenv file is available).
- `model.go`: `pi model list [provider]`. `allProviders = ["anthropic","openai","gemini",
  "mistral","ollama"]`. The switch validates provider names. `pi model list opencode`
  needs `"opencode"` added to `allProviders` and the switch, plus `ListModels` support.
- `ping.go`, `acp/server/runtime.go`: also resolve keys/baseURLs per provider.

## Model listing (internal/provider/list_models.go)

- `ListModels(ctx, providerName, opts)` switches on provider:
  anthropic, openai, gemini, mistral, ollama. Add `case "opencode"`.
- `fetchJSON(ctx, method, url, opts, providerName, dst)` sets auth header by provider:
  - `anthropic` → `x-api-key` + `anthropic-version: 2023-06-01`.
  - `openai`/`mistral` → `Authorization: Bearer`.
  - opencode should use `Authorization: Bearer` (matches OpenAI family for `/models`).
- `providerDefaultBaseURL(p)` returns default base per provider. Add `case "opencode":
  return "https://opencode.ai/zen/go/v1"`.

## model.LLM interface (google.golang.org/adk/v2/model)

```go
type LLM interface {
    Name() string
    GenerateContent(ctx, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error]
}
```
`LLMRequest{Model string, Contents []*genai.Content, Config *genai.GenerateContentConfig}`.
`LLMResponse` provides a `Content *genai.Content` candidate.

## Dispatch design considerations

The `opencode` provider must route each model to one of three backend implementations:
OpenAI chat/completions, OpenAI responses, or Anthropic messages.

pi-go already has `NewOpenAI(modelName, apiKey, baseURL, opts)` and
`NewAnthropic(modelName, apiKey, baseURL, thinkingLevel, opts)` constructors returning
`model.LLM`. These handle tool calls, streaming, and multi-turn. Two viable designs:

1. **Delegating wrapper**: `NewOpenCode` builds a per-model delegate by calling
   `NewOpenAI` (for completions/responses models) or `NewAnthropic` (for messages models)
   with the Go base URL and key, then wraps it in a thin `opencodeModel` that forwards
   `GenerateContent`. Routing is by model→endpoint catalog. This reuses all existing
   request/streaming/tool logic with minimal new code.
   - OpenAI family: pass base URL `https://opencode.ai/zen/go/v1`, `option.WithAPIKey`
     (Bearer). `modelNeedsResponses`/`endpointMode` currently decides responses vs chat for
     openai by hardcoded model lists — need to ensure `gpt-5.6-luna` maps to responses and
     completions models map to chat. Either reuse the openai model's own routing or force it.
   - Anthropic family: pass base URL `https://opencode.ai/zen/go`, `option.WithAPIKey`
     (x-api-key). 
2. **Standalone dispatcher**: reimplement three request paths. Duplicates tool/streaming
   logic. Not recommended.

Design 1 is clearly simpler and lower-risk. The key subtlety is the differing base URL
suffixes (`/v1` vs no `/v1`) and that `NewOpenAI`'s `normalizeOpenAIBaseURL` and the codex
back-end detection must not interfere.
