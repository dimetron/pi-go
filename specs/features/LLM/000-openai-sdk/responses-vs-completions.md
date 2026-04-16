# OpenAI Responses vs Chat Completions — pi-go reference

**Context:** pi-go integration research. Covers wire format, Go SDK (`openai-go/v3`), Azure's v1 GA vs dated
`api-version` surfaces, Codex model routing.
**Date:** 2026-04-16

---

## TL;DR

Two endpoints coexist. **Chat Completions (`/v1/chat/completions`) is stateless and legacy-stable**; *
*Responses (`/v1/responses`) is stateful, agent-native, and the only way to reach Codex models**. Azure ships two
parallel API surfaces — dated `api-version=…-preview` and the GA `/openai/v1/` path — and Codex family models on Azure
are Responses-only there too. For pi-go: target Responses as the primary wire, keep Chat Completions as a compatibility
shim for Ollama / local backends.

---

## Model → endpoint matrix

GPT-5.3-Codex, GPT-5-Codex, gpt-5.1-codex-mini, and gpt-5.1-codex-max are **Responses-only**. Attempting Chat
Completions against them returns errors like:

> `OperationNotSupported: The chatCompletion operation does not work with the specified model, gpt-5.1-codex-mini` (
> Azure)
> `This model is only supported in v1/responses and not in v1/chat/completions` (OpenAI)

| Model family                                                                     | Chat Completions | Responses | Notes                                                                                  |
|----------------------------------------------------------------------------------|------------------|-----------|----------------------------------------------------------------------------------------|
| gpt-5, gpt-5.1, gpt-5.2, gpt-5.4                                                 | ✅                | ✅         | Tool calling not supported in Chat Completions with `reasoning: none` starting GPT-5.4 |
| gpt-5-codex, gpt-5.1-codex-mini, gpt-5.1-codex-max, gpt-5.2-codex, gpt-5.3-codex | ❌                | ✅         | Responses-only. All reasoning efforts supported.                                       |
| gpt-4.1, gpt-4o family                                                           | ✅                | ✅         | No reasoning tokens.                                                                   |
| codex-mini-latest                                                                | ✅                | ✅         | Older codex-tuned model, dual-endpoint.                                                |

---

## Wire format

### Chat Completions (stateless)

```bash
curl https://api.openai.com/v1/chat/completions \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hello"}
    ]
  }'
```

Response envelope: `choices[0].message.content`. You own the history — every turn replays the full `messages` array.

### Responses (stateful by default)

```bash
curl https://api.openai.com/v1/responses \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d '{
    "model": "gpt-5-codex",
    "instructions": "You are a helpful assistant.",
    "input": "Hello",
    "reasoning": {"effort": "medium"}
  }'
```

Response envelope: `output[]` array of typed items (`reasoning`, `message`, `function_call`, `web_search_call`, etc.),
plus an `output_text` convenience field in SDKs. Turn 2 sends `"input": "...", "previous_response_id": "resp_abc"` —
OpenAI stores the chain.

### Key shape differences that bite during porting

- **`instructions` separated from `input`** at top level (no more system-message-in-messages hack).
- **Function/tool calling schema is different.** Chat Completions uses externally-tagged polymorphism (functions wrapped
  in `{"type": "function", "function": {...}}`), Responses uses internally-tagged flat form. In Chat Completions
  functions are non-strict by default; in Responses they are strict by default.

  ```jsonc
  // Chat Completions — externally tagged
  { "tools": [{ "type": "function", "function": { "name": "get_weather", "parameters": {...} } }] }

  // Responses — internally tagged, flat
  { "tools": [{ "type": "function", "name": "get_weather", "parameters": {...} }] }
  ```

- **Structured outputs:** use `text.format` in Responses, not `response_format`.
- **Reasoning tokens:** Responses supports `reasoning.effort` and returns encrypted reasoning items you can thread into
  the next turn. For ZDR orgs, `store=false` is auto-enforced; `encrypted_content` is decrypted in-memory for the next
  request, never persisted.
- **Built-in tools are Responses-only:** web search, file search, code interpreter, computer use, remote MCP.

---

## Go SDK (`github.com/openai/openai-go/v3`)

Note the `/v3` in the module path. v2 is missing the current Responses surface.

### Basic Responses call

```go
package main

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

func main() {
	client := openai.NewClient(
		option.WithAPIKey("..."), // or OPENAI_API_KEY env
	)

	resp, err := client.Responses.New(context.Background(), responses.ResponseNewParams{
		Model: "gpt-5-codex",
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("Refactor this function for clarity..."),
		},
		Instructions: openai.String("You are a senior Go reviewer."),
		Reasoning: &responses.ReasoningParam{
			Effort: responses.ReasoningEffortHigh,
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.OutputText()) // convenience aggregator
}
```

### Multi-turn with `previous_response_id`

```go
// Turn 1
r1, _ := client.Responses.New(ctx, responses.ResponseNewParams{
Model: "gpt-5-codex",
Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("Plan the refactor.")},
})

// Turn 2 — only send the new user turn
r2, _ := client.Responses.New(ctx, responses.ResponseNewParams{
Model:              "gpt-5-codex",
PreviousResponseID: openai.String(r1.ID),
Input:              responses.ResponseNewParamsInputUnion{OfString: openai.String("Now apply it.")},
})
```

Statelessness escape hatch: `Store: openai.Bool(false)` plus pass `encrypted_content` yourself.

### Streaming

```go
stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
Model: "gpt-5-codex",
Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("Stream me a haiku")},
})
for stream.Next() {
evt := stream.Current()
// evt is a tagged union: response.output_text.delta,
// response.function_call_arguments.delta, response.reasoning.delta,
// response.completed, etc.
if delta, ok := evt.AsResponseOutputTextDelta(); ok {
fmt.Print(delta.Delta)
}
}
if err := stream.Err(); err != nil { /* ... */ }
```

Semantic events (typed, not raw SSE chunks) are a real ergonomics win over Chat Completions'
`choices[0].delta.content` — useful when interleaving reasoning deltas, tool-call argument deltas, and text deltas in
one stream.

---

## Azure — two-surface reality

### Surface 1: Legacy dated `api-version`

URL:

```
https://{resource}.openai.azure.com/openai/deployments/{deployment-name}/responses?api-version=2025-04-01-preview
```

- Model is encoded as the **deployment name in the path**, not in the body.
- Each monthly preview `api-version` is its own SKU of the surface.
- Current preview exposing Responses: `2025-04-01-preview`.

### Surface 2: v1 API (GA)

URL:

```
https://{resource}.openai.azure.com/openai/v1/responses
```

- No `api-version` query for GA features; use `?api-version=preview` for preview features gated in v1.
- Same shape as upstream OpenAI — just swap `base_url`.
- Preview features opt-in via headers (e.g. `aoai-evals: preview`) instead of version swaps.
- **Responses is GA on v1 as of late 2025 / early 2026.**

### Side-by-side curl

```bash
# Legacy dated api-version (model = deployment name in path)
curl "https://my-aoai.openai.azure.com/openai/deployments/gpt-5-codex-dep/responses?api-version=2025-04-01-preview" \
  -H "api-key: $AZURE_OPENAI_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "input": "Write a Go HTTP handler.",
    "reasoning": {"effort": "high"}
  }'

# v1 GA surface (model = deployment name in body, just like OpenAI)
curl "https://my-aoai.openai.azure.com/openai/v1/responses" \
  -H "api-key: $AZURE_OPENAI_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5-codex-dep",
    "input": "Write a Go HTTP handler.",
    "reasoning": {"effort": "high"}
  }'
```

### Go against Azure — two client options

**Option A — `openai-go/v3/azure` (preferred for code-portability across OpenAI and Azure)**

```go
import (
"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
"github.com/openai/openai-go/v3"
"github.com/openai/openai-go/v3/azure"
"github.com/openai/openai-go/v3/option"
"github.com/openai/openai-go/v3/responses"
)

cred, _ := azidentity.NewDefaultAzureCredential(nil)

client := openai.NewClient(
// Legacy dated api-version path:
azure.WithEndpoint("https://my-aoai.openai.azure.com", "2025-04-01-preview"),
// OR v1 GA path — omit api-version:
// option.WithBaseURL("https://my-aoai.openai.azure.com/openai/v1/"),
azure.WithTokenCredential(cred),
)

resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
Model: "gpt-5-codex-dep", // Azure deployment name, not the OpenAI model id
Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("...")},
})
```

Auth scope for `NewDefaultAzureCredential`: `https://cognitiveservices.azure.com/.default`.

**Option B — `github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai`**

Microsoft's own SDK. Includes `Example_responsesApiFunctionCalling` and `Example_responsesApiReasoning` examples.
Heavier-weight but fully integrated with `azure-sdk-for-go` (retries, telemetry, credential types). Reach for this in
KAgent where you're already Azure-SDK-heavy; for pi-go, `openai-go/v3` is lighter.

### Azure api-version timeline

| api-version          | Status                | Notes                                                             |
|----------------------|-----------------------|-------------------------------------------------------------------|
| `2024-10-21`         | Last legacy GA        | Chat Completions only                                             |
| `2025-04-01-preview` | Preview               | First to expose `/responses`, Assistants, evals, Realtime preview |
| `preview` (literal)  | Rolling preview on v1 | Use as `?api-version=preview` on v1 URL for preview features      |
| _(omitted)_ on v1    | GA                    | Responses, Chat Completions, embeddings, images, audio — all GA   |

- Realtime Preview is deprecated starting **April 30, 2026** — canary for the broader legacy deprecation pattern.
- Assistants API retired **August 26, 2026** — migrate to Responses or Foundry Agents.

### Regional availability for Codex (EU-focused notes)

Azure rollout lags OpenAI by weeks to months; gated by registration. Registration is required for `gpt-5.4`,
`gpt-5.4-pro`, `gpt-5.3-codex`, `gpt-5.2`, `gpt-5.2-codex`.

Observed patterns from Q&A threads:

- New Codex/GPT-5.x models often land **East US 2** first (Global Standard and Data Zone).
- EU availability usually arrives in **Sweden Central** first, with **West Europe** trailing by weeks-to-months.
- Regional / Provisioned (PTU) deployment types for Codex models often don't exist yet at launch.

**Deployment types to know:**

- **Global Standard** — cheapest, traffic routes worldwide; data at rest stays in your geo but inference may leave.
- **Data Zone Standard** — inference stays in EU (or US) data zone. Pragmatic EU-residency pick.
- **Regional / Provisioned (PTU)** — single region, reserved capacity.

**For Prague / EU residency today:** Codex work on Azure → Sweden Central Data Zone (if registered) or Global Standard
East US 2 fallback. Upstream OpenAI API remains the fastest path for Codex experimentation in pi-go dev.

---

## pi-go recommendation

1. **Primary wire = Responses.** Design the core agent loop around the Responses shape (typed `output[]` items,
   `previous_response_id` or encrypted-content threading, semantic streaming events). Maps cleanly onto
   GoAkt/MemPalace — each `output_item.added` event is an actor message.

2. **Chat Completions = compatibility adapter, not core.** Ollama, LiteLLM, Groq, Mistral, most OSS backends speak Chat
   Completions. Keep a small adapter that flattens Responses params → `messages[]` for those. Don't try to force
   Codex-class models through Chat Completions — won't work.

3. **`store=false` by default.** For a local-first runtime like pi-go, set `store: false` and thread `encrypted_content`
   yourself. You get Responses semantics without OpenAI-side persistence — matches ZDR-sensitive telco customer profile.

4. **Pin `openai-go/v3`.** Module path `github.com/openai/openai-go/v3`. v2 is missing the current Responses surface;
   avoid third-party ports.

5. **Azure abstraction layer.** Wrap client construction so callers pick `{openai, azure-v1, azure-dated}`. Only runtime
   differences: base URL, auth header name (`Authorization: Bearer` vs `api-key`), and whether `model` is a deployment
   name. The `openai-go/v3/azure` package handles most of this.

---

## Gotchas

- **`previous_response_id` doesn't save input tokens** the way you'd expect — billing still covers the re-used context
  on OpenAI's side. Ergonomics win, not a cost win.
- **Model field on Azure = deployment name**, not OpenAI model id. Typos here → confusing 404s.
- **Function schema migration:** dropping the wrapping `"function": { ... }` object is required when moving Chat
  Completions tool defs to Responses. Doing it backwards → `Missing required parameter: 'tools[0].function'`.
- **Codex models don't support all GPT-5.4 parameters.** GPT-5.3-Codex supports low/medium/high/xhigh reasoning effort,
  function calling, structured outputs, streaming, prompt caching — but not the full GPT-5.4 parameter surface. Test per
  model.
- **Assistants API is dead** (retires 2026-08-26). If any code imports it, flag for migration.
- **Continue.dev-style failure mode:** attempting to use `chat/completions` against Azure Codex deployments fails with
  `OperationNotSupported`. Make sure the adapter routes Codex → Responses.

---

## Sources

- OpenAI Responses guide: https://platform.openai.com/docs/guides/responses-vs-chat-completions
- OpenAI migration guide: https://platform.openai.com/docs/guides/migrate-to-responses
- OpenAI GPT-5-Codex model page: https://platform.openai.com/docs/models/gpt-5-codex
- openai-go v3 GitHub: https://github.com/openai/openai-go
- Azure v1 API lifecycle: https://learn.microsoft.com/en-us/azure/foundry/openai/api-version-lifecycle
- Azure Responses REST reference: https://learn.microsoft.com/en-us/azure/foundry/openai/reference-preview
- Azure OpenAI Responses Go examples: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai
- Azure Starter Kit (Go Responses
  examples): https://learn.microsoft.com/en-us/samples/azure-samples/azure-openai-starter/azure-openai-starter/
- Codex endpoint-only bug (litellm): https://github.com/openai/codex/issues/4136
- Continue.dev Azure Codex issue: https://github.com/continuedev/continue/issues/9133