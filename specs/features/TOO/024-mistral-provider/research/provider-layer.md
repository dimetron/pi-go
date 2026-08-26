# Research: LLM Provider Layer (internal/provider)

Repo: `/Users/dimetron/p6s/pi-dev/pi-go` — module `github.com/dimetron/pi-go`, go 1.27.0.
openai-go SDK: `github.com/openai/openai-go/v3 v3.52.0`.

## thinkingLevel → reasoning_effort mapping (existing providers)

**openrouter.go:130-145** — `openrouterReasoningEffort(level string) string`:
- `"low"|"medium"|"high"` → identical; `"max"` (case-insensitive) → `"max"`;
  empty / `"none"` / unknown → `""` (leave model default; "none" rejected by some
  providers). Does NOT recognize `"xhigh"`.
- Consumed at openrouter.go:102-108: `if effort != "" → params.SetExtraFields(...)`.
- Level stored verbatim on `openrouterModel.thinkingLevel` (openrouter.go:31).

**xai.go `xaiReasoningEffort(level string) shared.ReasoningEffort`** (xai.go:194-207),
returns openai-go `shared.ReasoningEffort` constants ("low","medium","high","xhigh"):
- `"none","low"` → `"low"` (Grok has no off switch), `"medium"` → `"medium"`,
  `"high"` → `"high"`, `"max","xhigh"` → `"xhigh"`, unknown → `""` (omitted).
- Exact-match switch (no trim/lower). Assigned at xai.go:85.
- Used at xai.go:141-143, gated by `xaiModelReasons(modelName)` which excludes
  names containing `"non-reasoning"`.

Asymmetry: OpenRouter lowercases/trims; xAI exact-matches. OpenRouter maps
max→"max" only; xAI maps none+low→low and max+xhigh→xhigh.

## 2. SetExtraFields raw-JSON injection (openrouter.go:102-108)

```go
if effort := openrouterReasoningEffort(m.thinkingLevel); effort != "" {
    // The SDK has no field for OpenRouter's unified `reasoning` object...
    params.SetExtraFields(map[string]any{
        "reasoning": map[string]string{"effort": effort},
    })
}
```
`params` is `openai.ChatCompletionNewParams`. `SetExtraFields` is the openai-go SDK
method; value type `map[string]any`.

## 3. xAI per-instance cache key (xai.go)

- `const xaiConversationHeader = "x-grok-conv-id"` (xai.go:27).
- `option.WithHeader(xaiConversationHeader, uuid.NewString())` in NewXAI (xai.go:62).
- Comment xai.go:59-61: "One id per model instance, which is one id per pi
  session: every turn of a conversation shares a prefix, and that is exactly the
  scope xAI's cache is keyed on."
- Extra headers applied after, so explicit --header overrides (xai.go:64-69).

## 4. Current mistral.go

Signature (mistral.go:27) — **no thinkingLevel parameter**:
```go
func NewMistral(_ context.Context, modelName, apiKey, baseURL string, llmOpts *LLMOptions) (model.LLM, error)
```
Struct `mistralModel{modelName string; client openai.Client}` — no reasoning field.

GenerateContent (mistral.go:56-90):
- `messages, systemInstruction := oaiContentsToMessages(req.Contents, req.Config)`
- `params := openai.ChatCompletionNewParams{Model: modelName, Messages: messages}`;
  modelName falls back to `m.modelName` when `req.Model == ""`.
- System instruction prepended as `openai.SystemMessage(systemInstruction)`.
- Tools: `params.Tools = oaiGenaiToolsToOpenAI(req.Config.Tools)` + `ToolChoice =
  {OfAuto: openai.String("auto")}` when tools present.
- Streaming (82-85): `retryStream(ctx, streamRetryConfig(), yield, func(y){ oaiRunStreaming(ctx, &m.client, params, y) })` — plain runner, no Extract variant.
- Non-streaming (86-88): `oaiRunNonStreaming(ctx, &m.client, params, yield)`.
- `mistralFinishReasonToGenai` delegates to `oaiFinishReasonToGenai`.

Reused helpers: `oaiContentsToMessages`, `oaiGenaiToolsToOpenAI`, `oaiRunStreaming`,
`oaiRunNonStreaming`, `oaiFinishReasonToGenai`.

## 5. Reasoning extraction hooks (openai_completions.go)

- `oaiRunStreamingExtract(ctx, client, params, yield, extractThinking func(rawChunk string) string)`
  (line 304); `oaiRunStreaming` passes nil hook.
- Inside chunk loop (321-332): `think := extractThinking(chunk.RawJSON()); if think != ""`
  → accumulate `state.thinking`, yield `Partial:true, TurnComplete:false,
  Content:&genai.Content{Role:"thinking", Parts:[{Text: think}]}` (TUI renders 💭).
  `state.thinking` re-emitted as turn content only when no text/tool calls
  (buildOaiFinalResponse, lines 251-259).
- `oaiRunNonStreamingExtract(ctx, client, params, yield, extractThinking func(rawResponse string) string)`
  (line 375); non-streaming hook prepends thinking text before content (lines 388-392).
- OpenRouter hooks: `openrouterDeltaThinking` (parses chunk.RawJSON()
  `choices[].delta.reasoning` string or `reasoning_details[].text`) and
  `openrouterMessageReasoning` (parses completion.RawJSON() `choices[].message.reasoning`).
- Only OpenRouter wires the Extract variants (openrouter.go:112,115).

## 6. NewLLM switch (provider.go:493-529) — who gets thinkingLevel

| Provider | Line | thinkingLevel passed? |
|---|---|---|
| ollama | 499-504 | ✅ |
| gemini | 506 | ❌ |
| openai | 508 | ❌ |
| azure | 512 | ❌ (empty literal) |
| anthropic | 514 | ✅ |
| **mistral** | **516** | **❌ constructor lacks param** |
| openrouter | 518 | ✅ |
| xai | 519-523 | ✅ (forces XAI tools) |
| opencode | 525 | ✅ |

5 of 9 providers receive thinkingLevel; mistral does not.

## Notable tests
- `openrouterReasoningEffort` table tests: openrouter_test.go:721-722.
- `xaiReasoningEffort` table tests: xai_test.go:140-141; xai_e2e_test.go:242 logs wire value.

## 7. Mistral thinking chunks vs openai-go SDK (verified against docs + SDK source)

Mistral's official reasoning docs (docs.mistral.ai/studio-api/conversations/reasoning):
- With `reasoning_effort: "high"`, `message.content` is a **list of chunks**:
  `ThinkChunk` (`type:"thinking"`, `thinking` field is a list of `TextChunk`s) and
  `TextChunk` (`type:"text"`). With `"none"` it's a plain string.
- When streaming, `delta.content` changes shape across the response:
  1. Thinking phase: `delta.content` is a list containing a `ThinkChunk`.
  2. Transition: a single list with a closing `ThinkChunk` + first `TextChunk`.
  3. Answer phase: `delta.content` is a plain string.
- `reasoning_effort` adjustable on `mistral-small-latest` and `mistral-medium-3-5`;
  other reasoning models (magistral) use `prompt_mode: "reasoning"`.

SDK reality: `ChatCompletionChunkChoiceDelta.Content` is **`string`**
(chatcompletion.go:1125 in openai-go v3.52.0), so a `delta.content` array cannot
be represented by the SDK field. The chunk's `RawJSON()` is still available
(via `chunk.RawJSON()`, the same mechanism openrouter.go uses to recover
`delta.reasoning`). Therefore:
- A Mistral thinking-extract hook (like `openrouterDeltaThinking`) can parse
  `choices[].delta.content` from raw chunk JSON, extract `type:"thinking"`
  chunks' nested `thinking[].text`, and yield them as thinking-role partials via
  the existing `oaiRunStreamingExtract` hook. Same for non-streaming via
  `oaiRunNonStreamingExtract` (`choices[].message.content` array).
- Without such a hook, Mistral thinking text is dropped (SDK cannot unmarshal an
  array into `Content string`).
