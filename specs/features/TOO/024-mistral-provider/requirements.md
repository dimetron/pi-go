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

### Q2: pending

