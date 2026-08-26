# Requirements

## Questions & Answers

### Q1 (scope): What does "mistral-provider" mean given a Mistral provider already exists?

**Answer (user):** Use the documented Mistral Chat Completion API
(https://docs.mistral.ai/api/endpoint/chat#operation-chat_completion_v1_chat_completions_post)
as the reference for the provider.

**Implication:** the task is to make `internal/provider/mistral.go` implement the
documented `/v1/chat/completions` API properly. Today it routes through the
openai-go SDK's chat completions against Mistral's OpenAI-compatible endpoint,
which cannot express Mistral-specific request fields (reasoning_effort,
prompt_cache_key, response_format, parallel_tool_calls, ...). The documented API
is the spec of record.

### Q1b (models endpoint): The Models API is also in scope.

**Answer (user):** add https://docs.mistral.ai/api/endpoint/models for mistral.

**Implication:** the model-listing path (`pi model list mistral`) should follow the
documented `GET /v1/models` API. Today `listMistralModels` uses the generic
`listBearerModels` helper, which parses only the OpenAI-style `{"data":[{"id",
"owned_by"}]}` envelope. The documented response is a list of model cards with
`capabilities` (completion_chat, completion_fim, function_calling, fine_tuning,
vision, classification), `max_context_length`, `aliases`, `root`, `created`,
`archived`, and fine-tuned (`ft:...`) variants. The spec must decide how much of
the card surface to consume.

### Q2 + Q3 (approach + models endpoint): keep openai SDK, fix model validation, enrich model list.

**Answer (user):** keep openai sdk but fix model validation: unknown openai model
"mistral/codestral-2508" show model list with more fields if possible

**Implication:**
- **Implementation approach (Q2):** keep the openai-go SDK as the transport for
  Mistral chat (no direct HTTP client, no third-party SDK). Any Mistral-specific
  request fields that openai-go cannot express would use the raw-JSON injection
  pattern (as openrouter.go does) — if in scope.
- **Concrete bug (Q3):** `pi` rejects `mistral/codestral-2508` with
  `unknown openai model "mistral/codestral-2508"`. Root-cause chain (confirmed by
  reading code):
  1. `internal/config`'s `autoDetectProvider` has **no `mistral`/`magistral`
     prefixes**, so a `mistral/...` (or bare `mistral-...`) model resolves to
     `""` and falls back to `DefaultProvider` = `"openai"` (config.go:250,
     config.go:212). Verified: `mistral/codestral-2508` → "openai".
  2. Callers that resolve the role's provider first (`buildSwitchedLLM` via
     `cfg.ResolveRole`, `cli.go:1766` commit path) pass that wrong "openai"
     provider into `resolveSwitchedModel` / `ResolveWithBaseURL`, which then
     **overwrites** the correct auto-detected provider (`info.Provider =
     providerName`, interactive.go:1032 / cli.go:315).
  3. Even with the right provider, `provider.Resolve` maps the `mistral` prefix
     to the mistral provider but does **not strip** the `mistral/` prefix from
     `info.Model` (unlike `azure/`, `ollama/`, `opencode/`, `openrouter/`, and
     `openai/`, which all strip). So the literal `mistral/codestral-2508` id is
     sent to `ValidateModel`, where prefix-checking against
     `KnownModels["mistral"]` (contains `codestral`, `mistral-*`, ...) fails.
  4. `ValidateModel` (provider.go:315) then reports
     `unknown openai model "mistral/codestral-2508"` — the provider in the error
     is whatever `info.Provider` got overwritten to.
- **Model list (Q3b):** `pi model list mistral` should show more fields "if
  possible" — parse the documented model-card shape (capabilities,
  max_context_length, aliases, owned_by) instead of only the OpenAI envelope
  (id, owned_by). Today `listMistralModels` reuses the generic
  `listBearerModels`, which discards capabilities/context length, and
  `provider.ModelInfo` only carries `ID` + `OwnedBy`.

### Q4 (mistral/ prefix semantics): Auto-detect + strip.

**Answer (user):** A

**Implication:** `mistral/` is a routing prefix like `azure/` and `ollama/`:
- `Resolve("mistral/codestral-2508")` → `Info{Provider: "mistral", Model:
  "codestral-2508"}` (prefix stripped, case-insensitive detection).
- The bare model id is then validated against `KnownModels["mistral"]`.
  Validation is prefix-based, so `codestral-2508` already passes via the
  existing `codestral` entry (model_catalog.go:104) — no catalog change strictly
  required for this example, though dated variants may be added if desired.
- `internal/config`'s `autoDetectProvider` must gain `mistral` and `magistral`
  prefixes so config-resolved roles no longer fall back to `DefaultProvider`.
- Callers that force a resolved role provider must not overwrite a correct
  auto-detected provider for a `mistral/...` name (or must auto-detect with the
  same rules).

### Q5 (model list enrichment): Mistral-only richer display.

**Answer (user):** B

**Implication:** add a dedicated Mistral model-card parse + print path:
- `pi model list mistral` parses the documented `GET /v1/models` card shape
  (capabilities, max_context_length, aliases, owned_by, root) instead of the
  generic OpenAI envelope.
- Output shows context window and (at least) chat/vision capabilities for Mistral
  models, without changing the print layout for other providers.
- Implementation may add optional fields to `provider.ModelInfo` (e.g.,
  `ContextWindow int64`, `Capabilities []string`) as long as other providers'
  output stays unchanged, or use a separate Mistral-only struct + printer.

### Q6 (chat-side Mistral-specific fields): wire in reasoning effort + prompt caching.

**Answer (user):** A

**Implication:** wire the Mistral-specific chat fields using the openrouter-style
raw-JSON injection (`params.SetExtraFields`):
- **reasoning_effort / prompt_mode:** `NewMistral` currently takes no
  `thinkingLevel` (provider.go:516 calls `NewMistral(ctx, info.Model, apiKey,
  baseURL, opts)`), unlike NewOpenRouter/NewXAI/NewAnthropic. Add the parameter
  and map pi's thinking level ("none"|"low"|"medium"|"high"|"max") onto
  Mistral's `reasoning_effort` ("none"|"minimal"|"low"|"medium"|"high"|"xhigh")
  and `prompt_mode: "reasoning"` where applicable.
- **prompt_cache_key:** Mistral bills cached tokens at 10% of input price and
  keys cache on this string. The session ID is not plumbed into the provider
  layer; the xAI precedent (xai.go:59-62) is a per-instance UUID generated in the
  constructor — "one id per model instance, which is one id per pi session".
  Apply the same pattern for Mistral's `prompt_cache_key`, injected via
  `params.SetExtraFields`.
- Response side: check whether Mistral reasoning models emit thinking content on
  chunks (delta.reasoning / reasoning_content) and surface it as thinking-role
  partials like openrouter does — research item for Phase 3.

### Q7 (other documented chat fields): pending — see Q8 follow-up.

### Q8 (catalog): load and save per provider; embed JSON; pull fresh when API key available.

**Answer (user):** model catalog - load and save now per provider embed json - when API KEY available pull fresh

**Implication:** the model catalog becomes refreshable instead of a compile-time
snapshot:
- **Embedded JSON stays** as the offline fallback (today: `modeldata/llm-prices-*.json`
  for openai/anthropic, `model_catalog.go` hard-coded maps for gemini/mistral/xai,
  plus `modeldata/context-windows.json`).
- **Pull fresh:** when a provider API key is available, fetch `GET /v1/models`
  (the documented Mistral Models endpoint, and the equivalent for other
  providers) and use the live list for validation.
- **Load and save:** the fetched catalog is persisted per provider (JSON on disk)
  and loaded on startup, so a key-less restart still validates against the last
  known-good list instead of only the embedded snapshot.
- Open design questions (Phase 3 research + Phase 4 design):
  1. Where the per-provider cache lives (config dir vs cache dir — pi-go has
     `~/.pi-go` config and a session base dir; pick the established pattern).
  2. When a refresh is triggered (startup, validation miss, `pi model list`,
     TTL) and how staleness is handled.
  3. Whether this replaces `KnownModels`' init-time loading with a
     lazily-refreshed lookup, and how `ValidateModel` uses it.
  4. Whether this applies to all providers or starts with Mistral (the task's
     focus) — the user said "per provider", so design for the general mechanism,
     land it for Mistral first.
  5. How capabilities/context-window from the live card merge into validation
     and the `pi model list` display (ties into Q5).

### Q7 (chat fields scope) + Q8b (tests): pending

**User input so far:**
- Q7 (chat fields scope): user selected (a) for Q6 — reasoning + cache. Remaining
  documented chat fields (response_format, parallel_tool_calls, random_seed,
  safe_prompt, service_tier, guardrails) are NOT requested; keep the openai-go
  defaults for everything else unless research shows a gap that breaks tool use.
- Q8b (tests): not yet answered — the existing e2e suite is `//go:build e2e`
  gated with `MISTRAL_API_KEY` skip. Plan should add httptest unit coverage for
  the new injection/catalog logic and extend e2e for reasoning + caching when
  key is present (mirroring `mistral_e2e_test.go` style).
