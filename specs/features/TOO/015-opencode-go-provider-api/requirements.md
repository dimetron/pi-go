# Requirements

## Task Scope

Add **OpenCode Go** (https://opencode.ai/go) as an LLM provider in pi-go. OpenCode Go is a
low-cost subscription provider for curated open coding models, accessed over HTTPS via
`https://opencode.ai/zen/go/v1/...` endpoints using an `OPENCODE_API_KEY`.

## Questions & Answers

**Q1 — What is the intended deliverable?**
A: Only the API provider — integrate OpenCode Go's HTTP API as a pi-go LLM provider backend.
   Not a Go client library for the opencode server, and not an external-agent spawner.

**Q2 — How should OpenCode Go be implemented?**
A: A single `opencode` provider that dispatches each model to its native endpoint family
   based on a model→endpoint map. Some models use the OpenAI `responses` API, some use
   OpenAI `chat/completions`, and some use the Anthropic `messages` API.

**Q3 — Which endpoint families are in scope?**
A: All three — OpenAI `chat/completions`, OpenAI `responses`, and Anthropic `messages`.
   Include the Anthropic `messages` family in the initial implementation.

**Q4 — How are API keys and config wired?**
A: Support both `OPENCODE_API_KEY` env var AND a config option in `internal/config/config.go`
   (similar to the existing `baseURLs` map). The default endpoint is a fixed constant
   `https://opencode.ai/zen/go/v1`, overridable via `OPENCODE_BASE_URL` as an optional rewrite
   (e.g., if we proxy LLM calls through agentgateway). API key is loaded from `.pi-go/.env`
   (the existing dotenv mechanism in `internal/config/config.go`).

**Q5 — Model→endpoint routing map?**
A: Both: a hardcoded default catalog (maintained alongside the doc) AND a runtime refresh
   from `https://opencode.ai/zen/go/v1/models`.

**Q6 — Model prefix?**
A: `opencode/` only (consistent with pi-go's short provider prefixes like `ollama/`, `azure/`).
   e.g. `opencode/kimi-k3`, `opencode/gpt-5.6-luna`.

---

## Requirements Summary

1. **New provider `opencode`** in `internal/provider` that accesses OpenCode Go over HTTPS
   at base URL `https://opencode.ai/zen/go/v1`, overridable via `OPENCODE_BASE_URL`.
2. **Per-model endpoint routing**: a single `opencode` provider that dispatches each model to
   its native endpoint family based on a hardcoded model→endpoint catalog (from the OpenCode Go
   doc). Three endpoint families:
   - OpenAI `chat/completions` (Bearer auth)
   - OpenAI `responses` (Bearer auth)
   - Anthropic `messages` (`x-api-key` auth)
3. **Model prefix** `opencode/` — stripped before sending to the endpoint; the remainder is the
   model ID (e.g. `opencode/kimi-k3` → `kimi-k3`).
4. **Keys/config**: `OPENCODE_API_KEY` env var (loaded from `.pi-go/.env` via existing dotenv
   mechanism) plus a config option in `internal/config/config.go`; base URL default
   `https://opencode.ai/zen/go/v1` with `OPENCODE_BASE_URL` override.
5. **Model listing**: `pi model list opencode` supported; fetched at runtime from
   `https://opencode.ai/zen/go/v1/models` (Bearer auth). The hardcoded routing catalog drives
   endpoint selection; runtime `/models` provides the available-model list.
6. **Integration points** in pi-go:
   - `provider.Resolve` / `autoDetectProvider`: recognize `opencode/` prefix → provider "opencode".
   - `provider.NewLLM`: case "opencode" → new `NewOpenCode` constructor.
   - `config.APIKeys()`: add `"opencode": {"OPENCODE_API_KEY"}`.
   - `config.BaseURLs()`: add `"opencode": "OPENCODE_BASE_URL"`.
   - `provider.ListModels`: case "opencode".
   - `provider.providerDefaultBaseURL`: case "opencode".
   - CLI `model list` allProviders list and validation lists.

**Open questions (to resolve in research/design):**
- How the `opencode` provider reuses existing openai responses/completions and anthropic
  code paths vs. writing a thin dispatcher that calls into `NewOpenAI`/`NewAnthropic` with the
  Go base URL and API key, given the differing auth header requirements (Bearer vs x-api-key).
- Whether `ValidateModel` needs an opencode catalog entry.

**API probe (2025-XX-XX):**
- `GET https://opencode.ai/zen/go/v1/models` with `Authorization: Bearer $OPENCODE_API_KEY`
  works; returns `{"object":"list","data":[{"id":"...","object":"model","owned_by":"opencode"},...]}`.
- 25 models listed: minimax-m3/m2.7/m2.5, kimi-k3/k2.7-code/k2.6/k2.5, glm-5.2/5.1/5,
  deepseek-v4-pro/flash, qwen3.7-max/3.8-max/3.7-plus/3.6-plus/3.5-plus, mimo-v2-pro/v2-omni/v2.5-pro/v2.5,
  hy3/hy3-preview, gpt-5.6-luna, grok-4.5.
- NOTE: `/models` returns only model IDs — it does NOT expose which endpoint family
  (chat/completions vs responses vs messages) a model uses. The endpoint routing map must
  therefore be a hardcoded catalog (from the doc), with runtime fetch only providing the
  list of available models.

**Endpoint auth probe (2025-XX-XX):**
- OpenAI `chat/completions` (kimi-k3): works with `Authorization: Bearer <key>`.
- OpenAI `responses` (gpt-5.6-luna): works with `Authorization: Bearer <key>`.
- Anthropic `messages` (minimax-m3): requires **`x-api-key: <key>`** header (NOT
  `Authorization: Bearer`). Returns `{"type":"error",...,"message":"Missing API key."}`
  with Bearer. Works with `x-api-key`.
- `deepseek-v4-flash` returned a regional availability error
  (`RegionError` — latest version hosted in China requires opt-in); not a client bug.

