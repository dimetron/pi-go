# Provider batch APIs — what they actually offer

All figures retrieved 2026-08-14 from primary vendor documentation. Secondary
sources (blog posts, pricing aggregators) disagreed with the vendor docs in two
places and were discarded; both disagreements are recorded below.

## Anthropic — Message Batches API

Source: <https://platform.claude.com/docs/en/build-with-claude/batch-processing>

| Property        | Value |
|-----------------|-------|
| Discount        | "All usage is charged at 50% of the standard API prices." |
| Turnaround      | "most batches completing within 1 hour"; results readable when all requests finish **or after 24 hours, whichever comes first** |
| Expiry          | "Batches expire if processing does not complete within 24 hours." Expired requests are **not billed**. |
| Size limit      | 100,000 requests **or** 256 MB, whichever comes first |
| Result retention| 29 days from creation |
| Streaming       | **Not supported** — `stream: true` is a validation error |
| Tool use        | Supported, including server tools |
| Prompt caching  | Supported. Docs recommend the **1-hour cache duration** because batches can exceed the 5-minute default TTL. |
| `max_tokens: 0` | Rejected (cache pre-warming does not work inside a batch) |
| Also rejected   | `speed` (Fast mode), `store`/`previous_thread_event_id` (Threads), `cache_hint`/`context_hint` |

Batch prices relevant to pi-go's roles:

| Model          | Batch input  | Batch output  |
|----------------|--------------|---------------|
| Claude Opus 5  | $2.50 / MTok | $12.50 / MTok |
| Claude Sonnet 5| $1.00 / MTok | $5.00 / MTok  |
| Claude Haiku 4.5 | $0.50 / MTok | $2.50 / MTok |

Go SDK: `github.com/anthropics/anthropic-sdk-go v1.57.0` (already in `go.mod`)
ships `messagebatch.go` with `MessageBatchService.New/Get/List/Cancel/Delete`
and `ResultsStreaming`. **No new dependency is required.**

## OpenAI — Batch API

Source: <https://developers.openai.com/api/docs/guides/batch>

| Property         | Value |
|------------------|-------|
| Discount         | "50% cost discount compared to synchronous APIs" |
| Completion window| `24h` is the **only** option |
| Size limit       | 50,000 requests per batch; input file ≤ 200 MB |
| Result retention | 30 days |
| Queue limit      | Per-model cap on prompt tokens queued for batch, shown on the Platform Settings page — **account-specific, not documented as a fixed number** |
| Endpoints        | Chat Completions, Responses, Embeddings, Completions, Moderations, Image Generation/Edits, Video Generation |

Prices for the configured default role
(<https://developers.openai.com/api/docs/pricing>):

| Model         | Std input | Std cached | Std output | Batch input | Batch cached | Batch output |
|---------------|-----------|------------|------------|-------------|--------------|--------------|
| gpt-5.6-luna  | $0.20     | $0.02      | $1.20      | $0.10       | $0.01        | $0.60        |
| gpt-5.6-terra | $2.00     | $0.20      | $12.00     | $1.00       | $0.10        | $6.00        |
| gpt-5.6-sol   | $5.00     | $0.50      | $30.00     | $2.50       | $0.25        | $15.00       |

Go SDK: `github.com/openai/openai-go/v3 v3.50.0` ships `batch.go` with
`BatchService.New/Get/List/Cancel`. **Already vendored.**

Note the interaction that undercuts the batch case: luna's cached-input price is
already 90% below its uncached price. Batching a cached request saves $0.01/MTok.

## Google Gemini — Batch API

Source: <https://ai.google.dev/gemini-api/docs/batch-api>

| Property         | Value |
|------------------|-------|
| Discount         | "50% of the standard interactive API cost for the equivalent model" |
| Turnaround       | Target 24 h; jobs expire after 48 h |
| Size limit       | Inline requests ≤ 20 MB; file-based input ≤ 2 GB |
| Result retention | 6 weeks |

**Correction to the vendor docs:** the Gemini batch page shows Python,
JavaScript and REST examples only and does not mention Go. It is nonetheless
supported — `google.golang.org/genai v1.66.0` (already in `go.mod`) ships
`batches.go` with `Batches.Get/Cancel/List/Delete/All` and internal
`create`/`createEmbeddings`. The docs are incomplete, not the SDK.

## xAI — Batch API

Source: <https://docs.x.ai/developers/advanced-api-usage/batch-api>

| Property          | Value |
|-------------------|-------|
| Discount          | The batch page does **not** state a percentage; it defers to the pricing page |
| Turnaround        | "Most batch requests complete within 24 hours… Completion time is best effort and not guaranteed." |
| Model exclusions  | **"`grok-4.6` and `grok-4.5` are not currently supported for Batch API requests and will be rejected."** |
| Rate limits       | 2 batch creations/sec/team; 1000 add-batch-request calls per 30 s |
| Size limits       | 25 MB per request payload; file uploads ≤ 200 MB, ≤ 50,000 requests |

**Discarded secondary sources.** Web search returned three mutually
contradictory claims about the xAI discount — "20% off, only on Grok 4.3 and the
Grok 4.20 variants", "all token costs cut in half", and "20–50%". None is
attributable to xAI. The vendor's own batch page states no percentage. This spec
therefore treats the xAI discount as **unknown** rather than picking a number,
and the flagship models pi-go would actually use are excluded from batch anyway.

## Ollama

No batch API. Local inference; the marginal token cost is electricity. Batch
economics do not apply.

## Cross-provider summary

| Provider  | Batch discount | Max latency | Models pi-go uses eligible? | Go SDK vendored |
|-----------|----------------|-------------|-----------------------------|-----------------|
| Anthropic | 50%            | 24 h        | Yes (all)                   | Yes             |
| OpenAI    | 50%            | 24 h        | Yes (all)                   | Yes             |
| Gemini    | 50%            | 24 h (48 h expiry) | Yes                  | Yes (undocumented) |
| xAI       | Unstated       | 24 h best-effort | **No** — 4.5/4.6 rejected | n/a — no batch client in `xai.go` |
| Ollama    | n/a            | n/a         | n/a                         | n/a             |

Three of five providers offer a clean 50% at a 24-hour worst case, and the SDK
support is already on disk. **The blocker is never the API. It is finding work
in pi-go that can tolerate a 24-hour result.**
