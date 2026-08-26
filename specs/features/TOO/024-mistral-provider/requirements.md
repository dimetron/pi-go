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

### Q2: pending

