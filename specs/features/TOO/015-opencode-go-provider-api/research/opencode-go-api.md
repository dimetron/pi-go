# Research: OpenCode Go API

## What OpenCode Go is

OpenCode Go (https://opencode.ai/go) is a low-cost subscription LLM provider for curated
open coding models. It is accessed over HTTPS at base URL `https://opencode.ai/zen/go/v1`
using an API key (`OPENCODE_API_KEY`). It is *not* a Go client library and not a pi-go
server integration — it is an HTTP LLM provider backend.

## Base URL

`https://opencode.ai/zen/go/v1`

## Endpoint families

Three endpoint families exist under the base URL. They map to different models and use
different auth header conventions.

| Family | Path | Auth header | Models |
|--------|------|-------------|--------|
| OpenAI chat/completions | `/chat/completions` | `Authorization: Bearer <key>` | grok-4.5, glm-5.2/5.1/5, kimi-k3/k2.7-code/k2.6/k2.5, deepseek-v4-pro/flash, mimo-v2-pro/v2-omni/v2.5-pro/v2.5, hy3/hy3-preview |
| OpenAI responses | `/responses` | `Authorization: Bearer <key>` | gpt-5.6-luna |
| Anthropic messages | `/messages` | `x-api-key: <key>` | minimax-m3/m2.7/m2.5, qwen3.8-max, qwen3.7-max/plus, qwen3.6-plus, qwen3.5-plus |

## Live probe results (verified)

- `GET /v1/models` with `Authorization: Bearer <key>` returns
  `{"object":"list","data":[{"id":"...","object":"model","owned_by":"opencode"},...]}`.
  25 model IDs returned (see requirements.md for full list).
- `POST /v1/chat/completions` (kimi-k3) works with Bearer auth.
- `POST /v1/responses` (gpt-5.6-luna) works with Bearer auth.
- `POST /v1/messages` (minimax-m3) **requires `x-api-key` header** (not Bearer).
  Returns `{"type":"error","error":{"type":"AuthError","message":"Missing API key."}}`
  with Bearer. Works with `x-api-key`. `anthropic-version` header is NOT required.
- `deepseek-v4-flash` returned a `RegionError` (latest version hosted in China requires
  explicit opt-in) — this is a model availability issue, not a client bug.

## Key finding: `/models` does not expose endpoint routing

`GET /v1/models` returns only model IDs and `owned_by`. It does **not** indicate which
endpoint family a model uses. Therefore the model→endpoint routing map must be a hardcoded
catalog (sourced from the doc), while the runtime `/models` fetch provides only the list of
available models.

## SDK base URL joining (determines how to pass the Go base URL)

Both the OpenAI and Anthropic SDKs build request URLs with
`cfg.BaseURL.Parse(strings.TrimLeft(req.URL.String(), "/"))` — i.e. they resolve the
relative path against the configured base URL.

- **OpenAI SDK** (openai-go/v3): request paths are `chat/completions` and `responses`
  (no `/v1` prefix). Default base URL is `https://api.openai.com/v1/`.
  To hit `https://opencode.ai/zen/go/v1/chat/completions` and `.../v1/responses`,
  pass base URL **`https://opencode.ai/zen/go/v1`** to the OpenAI SDK.
- **Anthropic SDK** (anthropic-sdk-go): request path is `v1/messages`. Default base URL is
  `https://api.anthropic.com`. To hit `https://opencode.ai/zen/go/v1/messages`,
  pass base URL **`https://opencode.ai/zen/go`** to the Anthropic SDK.

Auth headers: OpenAI SDK `option.WithAPIKey` sets `Authorization: Bearer <key>`.
Anthropic SDK `option.WithAPIKey` sets `x-api-key: <key>`.

## Model list (runtime `/models`)

`pi model list opencode` should call `GET {base}/v1/models` with Bearer auth and decode
`{"data":[{"id","object","owned_by"}]}`. This mirrors `listOpenAIModels` in
`internal/provider/list_models.go`.
