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

### Q5: pending

