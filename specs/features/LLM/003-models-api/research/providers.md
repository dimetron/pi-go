# Provider models endpoints — measured 2026-11-06

Everything below was produced by calling the live endpoint on this machine, not read
from documentation. Where a documented shape and the live shape disagree, the live
shape is recorded and the disagreement noted.

Reproduce any row with the command shown.

---

## agentgateway (local, `v1.7.0-alpha.1` via Docker)

```bash
curl -s http://localhost:4000/v1/models -H "Authorization: Bearer $AGENTGATEWAY_API_KEY"
```

`833` entries. `241` carry `context_window`. Fields actually present across the set:

```
id, object, created, owned_by, name, context_window, context_length,
max_input_tokens, max_tokens, architecture{input_modalities,output_modalities},
capabilities{reasoning,structured_outputs,tools,temperature}, supported_parameters
```

Worked example — the richest entry the endpoint returns:

```json
{"id":"claude-opus-5","object":"model","created":1784851200,"owned_by":"anthropic",
 "name":"Claude Opus 5","context_window":1000000,"context_length":1000000,
 "max_input_tokens":1000000,"max_tokens":128000,
 "architecture":{"input_modalities":["text","image","file"],"output_modalities":["text"]},
 "capabilities":{"reasoning":true,"structured_outputs":true,"tools":true,"temperature":false},
 "supported_parameters":["max_tokens","reasoning","reasoning_effort","response_format",
                          "structured_outputs","tool_choice","tools"]}
```

`owned_by` values observed: `anthropic, gcp.gemini, mistral, ollama, openai, opencode,
openrouter, prismml, virtual, xai`.

### Virtual models are marked, not described

Failover/virtual models carry `owned_by: "virtual"` and nothing else:

```json
{"id":"pi-default","object":"model","created":1793925527,"owned_by":"virtual"}
{"id":"pi-fast","object":"model","created":1793925527,"owned_by":"virtual"}
```

This is deliberate upstream behaviour — `ModelInfo::combine`
(`tmp/agentgateway/crates/agentgateway/src/llm/model_list.rs:55-125`) intersects the
targets of a virtual model so that what it advertises holds for *every* target: the
smallest limits, only capabilities all targets agree on, only shared modalities and
effort levels. When targets disagree, the field is omitted rather than guessed.
`owned_by: "virtual"` is the marker; the absent fields are the honest answer.

**Consequence for pi-go:** substituting a local number for `pi-fast`
(`context-windows.json` claims `1000000`) is exactly the guess upstream refused to
make.

### `preferred_endpoints` is not in the running build

`model_list.rs` at HEAD adds `preferred_endpoints` (`134897e1`, "llm/discovery:
advertise preferred invocation protocols", #4272). The running container is
`v1.7.0-alpha.1`, and the live response has **0** entries with that key.

pi-go must not depend on it. If an upstream release ships it, it is additive and
pi-go can opt in later; see `research/upstream.md` for the version arithmetic.

---

## OpenAI

```bash
curl -s https://api.openai.com/v1/models -H "Authorization: Bearer $OPENAI_API_KEY"
```

`98` entries, no pagination. Fields: `id, object, created, owned_by, shutdown_date`.

```json
{"id":"gpt-5-nano","object":"model","created":...,"owned_by":"system","shutdown_date":"2026-12-11"}
```

`shutdown_date` is present on `48` of `98` models.

**No context window, no capabilities, no token limits.** Confirmed independently in
`tmp/openai-go/model.go:96-117`: the SDK's `Model` struct is exactly
`ID, Created, Object, OwnedBy, ShutdownDate`. `ModelService.List` takes no params and
`pagination.Page.GetNextPage` returns `nil, nil` — OpenAI's model list is not
paginated.

OpenAI is therefore the one provider where a local table is the only source for
windows and capabilities. `shutdown_date` is new signal pi-go can use for
"deprecated, retires on X" and currently discards.

---

## Anthropic

```bash
curl -s https://api.anthropic.com/v1/models -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01"
```

`14` entries with no `limit` param. **`limit=3` returns 3 and `has_more: true`** — the
endpoint is cursor-paginated (`after_id`, `before_id`, `limit` up to 1000,
`lifecycle[]`), and pi-go sends none of it. Today that happens to be harmless
(`has_more: false` at the default), but it is one model away from silently
truncating.

Fields per model:

```json
{"type":"model","id":"claude-haiku-5-5","display_name":"Claude Haiku 5.5",
 "created_at":"2026-10-07T18:00:00Z","line":"haiku",
 "max_input_tokens":1000000,"max_tokens":128000,
 "lifecycle":"active","deprecated_at":null,"retires_at":null,
 "capabilities":{"batch":{"supported":true},"citations":{"supported":true},
   "code_execution":{"supported":true},
   "context_management":{"supported":true,"clear_tool_uses_20250919":{"supported":true},
     "clear_thinking_20251015":{"supported":true},"compact_20260112":{"supported":true}},
   "effort":{"supported":true,"low":{...},"medium":{...},"high":{...},"xhigh":{...},"max":{...}},
   "image_input":{"supported":true},"pdf_input":{"supported":true},
   "server_tools":{"supported":true,"code_execution":{...},"web_search":{...}},
   "structured_outputs":{"supported":true},"thinking":{"supported":true}}}
```

### `max_input_tokens` is the authoritative window

Live, per model:

| model | `max_input_tokens` | `max_tokens` | `lifecycle` |
|---|---|---|---|
| `claude-haiku-5-5` | 1000000 | 128000 | active |
| `claude-sonnet-5-5` | 1000000 | 128000 | active |
| `claude-opus-5-5` | 1000000 | 128000 | active |
| `claude-fable-5-1` | 1000000 | 128000 | active |
| `claude-opus-5` | 1000000 | 128000 | active |
| `claude-sonnet-5` | 1000000 | 128000 | active |
| `claude-fable-5` | 1000000 | 128000 | active |
| `claude-opus-4-8` | 1000000 | 128000 | active |
| `claude-opus-4-7` | 1000000 | 128000 | active |
| `claude-sonnet-4-6` | 1000000 | 128000 | active |
| `claude-opus-4-6` | 1000000 | 128000 | active |
| `claude-opus-4-5-20251101` | **200000** | **64000** | active |
| `claude-haiku-4-5-20251001` | **200000** | **64000** | active |
| `claude-sonnet-4-5-20250929` | **200000** | **64000** | **deprecated**, retires 2026-11-30 |

The 4.5 generation is 200K, not 1M. Requesting `anthropic-beta: context-1m-2025-08-07`
does **not** change what the API reports for `claude-opus-4-5-20251101` (still
`200000`), and pi-go does not send that header anyway — the only beta header it sets
is `anthropicOAuthBetaHeader` on the OAuth path (`anthropic.go:20,52`).

### `lifecycle[]` needs the bracket spelling

`?lifecycle=deprecated` returns
`invalid_request_error: the parameter name is 'lifecycle[]'`. The bracket form works:

```bash
curl "…/v1/models?lifecycle%5B%5D=deprecated&limit=100"   # -> claude-sonnet-4-5-20250929
curl "…/v1/models?lifecycle%5B%5D=retired&limit=100"      # -> []
```

Omitting the parameter returns `active` + `deprecated`; `retired` appears only when
asked for explicitly. `anthropic-version: 2023-06-01` accepts the filter, so pi-go
does not need a version bump to use it.

---

## Gemini

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models?key=$GEMINI_API_KEY"
```

**Paginated, and page 1 is not the whole list.** Default `pageSize` is 50; the
account has 61 models across 2 pages:

```
page 1: 50 models, nextPageToken=yes
page 2: 11 models, nextPageToken=no
TOTAL 61
```

The 11 on page 2 are, in full:

```
aqa
gemini-2.5-flash-native-audio-latest
gemini-2.5-flash-native-audio-preview-09-2025
gemini-2.5-flash-native-audio-preview-12-2025
gemini-3.1-flash-live-preview
gemini-3.5-live-translate-preview
gemini-3.5-transcribe-live
gemini-3.8-live
gemini-3.8-live-extended-thinking
gemini-robotics-er-2-streaming-preview
lyria-realtime-exp
```

pi-go returns 46 — it misses all 11 and then filters 4 more of its own.

Fields per model (note `inputTokenLimit` / `outputTokenLimit`, which pi-go discards):

```json
{"name":"models/gemini-2.5-flash","version":"001","displayName":"Gemini 2.5 Flash",
 "description":"…","inputTokenLimit":1048576,"outputTokenLimit":65536,
 "supportedGenerationMethods":["generateContent","countTokens","createCachedContent","batchGenerateContent"],
 "temperature":1,"topP":0.95,"topK":64,"maxTemperature":2,"thinking":true}
```

`supportedGenerationMethods` is the closest thing to a capability declaration here,
and `inputTokenLimit` is a real context window the endpoint hands over for free.

---

## Mistral

```bash
curl https://api.mistral.ai/v1/models -H "Authorization: Bearer $MISTRAL_API_KEY"
```

`64` entries, not paginated. Fields:

```
id, object, created, owned_by, name, description, type, aliases, max_context_length,
capabilities, default_model_temperature, billing_model_name, deprecation,
deprecation_replacement_model, audio_speech_capabilities, audio_transcription_capabilities
```

pi-go reads `id`, `owned_by`, `max_context_length`, and 6 of the 14 capability flags.

Dropped capability flag that matters:

```json
{"id":"mistral-medium-latest","max_context_length":262144,
 "capabilities":{"completion_chat":true,"function_calling":true,"reasoning":true,"vision":true}}
```

`reasoning: true` is discarded — `listMistralModels` (`list_models.go:113-174`) copies
only `completion_chat, completion_fim, function_calling, fine_tuning, vision,
classification`, so a reasoning model is indistinguishable from a non-reasoning one.

Also dropped: `deprecation` / `deprecation_replacement_model` (a per-model retirement
notice with a suggested replacement — better than pi-go's heuristic), and `name`
(the human display name).

---

## xAI

```bash
curl https://api.x.ai/v1/models -H "Authorization: Bearer $XAI_API_KEY"
```

`13` entries, `{data, object}`, no pagination, no window. Plain OpenAI shape.

---

## OpenRouter

```bash
curl https://openrouter.ai/api/v1/models -H "Authorization: Bearer $OPENROUTER_API_KEY"
```

`232` entries. Top-level `{data, links, total_count}` — `links` and `total_count` are
pagination metadata pi-go ignores, though it currently gets the whole list.

---

## Ollama

Read from `/api/tags` plus `/api/show` per model — already the one provider where
pi-go asks the runtime for the window instead of guessing
(`OllamaContextWindowSize`). No change.

---

## Summary: who reports what

| | window | out limit | display name | capabilities | lifecycle | paginated |
|---|---|---|---|---|---|---|
| agentgateway | ✅ `context_window` (241/833) | ✅ `max_tokens` | ✅ `name` | ✅ 4 flags | ❌ | ❌ |
| openai | ❌ | ❌ | ❌ | ❌ | ✅ `shutdown_date` | ❌ |
| anthropic | ✅ `max_input_tokens` | ✅ `max_tokens` | ✅ `display_name` | ✅ full tree | ✅ `lifecycle` | ✅ cursor |
| gemini | ✅ `inputTokenLimit` | ✅ `outputTokenLimit` | ✅ `displayName` | ⚠️ methods | ❌ | ✅ token |
| mistral | ✅ `max_context_length` | ❌ | ✅ `name` | ✅ 14 flags | ✅ `deprecation` | ❌ |
| xai | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| openrouter | ⚠️ in `context_length` | ⚠️ | ✅ `name` | ✅ | ❌ | ❌ |
| ollama | ✅ `/api/show` | ✅ | ✅ | ⚠️ | ❌ | ❌ |

Six of eight report a context window and pi-go uses none of them. Only OpenAI and xAI
genuinely need a local table.
