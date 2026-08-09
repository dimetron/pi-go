# Topic 2 — 73% of tokens run on routes reporting zero cache

## Research

`UsageMetadata.CachedContentTokenCount`, aggregated by model:

| model | LLM calls | prompt tokens | cached | hit rate |
|---|---|---|---|---|
| `minimax-m3:cloud` | 14,088 | 1,215,502,810 | 0 | **0.0%** |
| `minimax-m2.7:cloud` | 7,913 | 326,041,328 | 0 | **0.0%** |
| `deepseek-v4-flash:0731-cloud` | 2,597 | 273,278,416 | 0 | **0.0%** |
| `glm-5.2:cloud` | 3,805 | 183,000,068 | 0 | **0.0%** |
| `gpt-5.5` | 3,257 | 181,579,943 | 1,098,752 | **0.6%** |
| `deepseek-v4-flash:cloud` | 758 | 57,796,170 | 0 | **0.0%** |
| `minimax-m3` | 1,074 | 88,062,037 | 87,849,843 | 99.8% |
| `deepseek-v4-flash` | 434 | 63,356,042 | 62,257,024 | 98.3% |
| `gpt-5.6-luna` | 796 | 57,949,768 | 47,508,480 | 82.0% |
| `gemini-3.5-flash` | 552 | 26,686,684 | 22,716,159 | 85.1% |

The same model on two routes reports 99.8% and 0.0%. That is not a caching
difference, it is a **reporting** difference: `CachedContentTokenCount` is
populated only in three providers —

- `internal/provider/anthropic.go:532,663,808` — `CacheReadInputTokens`
- `internal/provider/openai_completions.go:226,253,322` — `PromptTokensDetails.CachedTokens`
- `internal/provider/openai_responses.go:378,463,538` — `InputTokensDetails.CachedTokens`

`internal/provider/ollama.go` never sets it. Everything routed through Ollama
reports zero. Confirmed: in `ollamaRunStreaming` (`ollama.go:508-514`) and
`ollamaRunNonStreaming` (`ollama.go:566-572`) the `usage` struct sets only
`PromptTokenCount` and `CandidatesTokenCount`; `CachedContentTokenCount` is left
at its zero value.

## Why this matters beyond reporting

`Tracker.AddWithCache` (`internal/guardrail/model_wrapper.go:49`) feeds
`CachePrefixTokens()` and `BodyTokens()` (`internal/guardrail/guardrail.go:354,363`),
and `BodyTokens()` is exactly what auto-compaction decides on:

```go
// internal/cli/autocompact_hook.go:52-55
body   := deps.Tracker.BodyTokens()
window := deps.Tracker.ContextWindowSize()
if deps.Cfg.Decide(body, window) == pisession.CompactionNone { return nil }
```

**Correction (verified against `guardrail.go:137-144`):** `CachedContentTokenCount`
does *not* feed `cachePrefixTokens`. `AddWithCache` sets the prefix baseline from
the first request's `inputTokens` (`if t.cachePrefixTokens == 0 { t.cachePrefixTokens = int64(inputTokens) }`),
independent of `cachedTokens`. So zero cache reporting does **not** make
`BodyTokens()` degenerate to the full prompt — the compaction threshold itself is
not corrupted by the cache gap.

The real consequences of zero cache reporting are narrower but still real:
- The **context gauge** and **cost reporting** understate cache savings, so the
  201:1 headline and per-model hit rates are wrong for the 73% of Ollama traffic.
- The **shed gating** in topic 3 cannot observe cache behaviour on those routes,
  which is the actual reason the cache fix must precede topic 3.

## Recommendations

1. **Populate `CachedContentTokenCount` on the Ollama path — but not by
   subtraction.** The Ollama `ChatResponse` `Metrics` struct exposes only
   `PromptEvalCount` and `EvalCount` (verified in the vendored
   `ollama@v0.31.2/api/types.go`); it exposes **no separate total-prompt count**
   from which cache reads could be derived. In the current adapter
   `PromptTokenCount` is itself populated from `PromptEvalCount`
   (`ollama.go:510-513, 568-571`), so a "total − fresh" difference would always
   be zero. Populating the field therefore requires a new source of cache-hit
   data — e.g. the Ollama server's `prompt_cache_hit_count`/`prompt_cache_eval_count`
   metrics if the API exposes them, or a provider-side estimate. This is a
   real, non-trivial change, not a one-line fix.
2. **Log explicitly when a route reports nothing.** Add a guardrail-level log
   when `CachedContentTokenCount == 0` on a route that is expected to cache, so
   a silent reporting regression is visible rather than invisible.
3. **Investigate `gpt-5.5` at 0.6% over 181 M tokens separately.** OpenAI caches
   prefixes automatically, so either the prefix is being invalidated or
   `PromptTokensDetails` is not surviving the response path on that model. This
   is a distinct anomaly from the Ollama gap.

## Expected impact

Corrects the context gauge and cost reporting for the ~73% of traffic that
currently reports zero cache, and — critically — unblocks the **shed gating** in
topic 3, which needs observed cache behaviour to decide whether shedding is
profitable. It does **not** change the compaction threshold itself (see the
correction above).

## Risk

Low-to-medium. The change is additive (populate a field that is currently zero),
but it is not a one-line fix: it requires a new source of cache-hit data on the
Ollama path (see recommendation 1). The behavioural consequence — shed gating
now has real cache numbers — is intended, but must be sequenced before topic 3.
