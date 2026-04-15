# Web Search APIs: Ollama vs Anthropic vs OpenAI vs Mistral

**Context:** Comprehensive comparison from a pi-go integration perspective. Covers API mechanics, architecture,
retrieval quality, developer ergonomics, and Go SDK availability per provider.

**Date:** 2026-04-13
**Author:** Research session for Dimetron / pi-go

---

## TL;DR

| Dimension                   | **Ollama**                                                                                                  | **Anthropic**                                                         | **OpenAI**                                                                                             | **Mistral**                                                |
|-----------------------------|-------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|------------------------------------------------------------|
| **Endpoint shape**          | Standalone REST (`/api/web_search`, `/api/web_fetch`)                                                       | Server-tool inside `/v1/messages`                                     | Server-tool inside `/v1/responses` (also `web_search_preview`)                                         | Built-in *connector* on Agents API (stateful)              |
| **Search backend**          | Ollama-managed (undisclosed)                                                                                | Brave Search (per Simon Willison + community)                         | Bing-class index (and Bing on Azure)                                                                   | Mistral-managed + premium news provider                    |
| **Pricing**                 | Free tier; Cloud subscription `$20`/mo Pro, `$100`/mo Max — no per-search fee                               | `$10 / 1000` searches + token costs                                   | Tiered with the search-capable models; `web_search` follows model rate-limits                          | `$30 / 1000` standard, `$50 / 1000` premium news           |
| **Citations**               | Returns `title/url/content` snippets — you wire citations yourself                                          | Inline encrypted citation blocks, automatic                           | Inline citations + `sources` field with all consulted URLs                                             | Reference chunks interleaved with text in `message.output` |
| **Domain allow/block**      | ❌ Not exposed                                                                                               | ✅ `allowed_domains` / `blocked_domains`, `user_location`              | ✅ `filters.allowed_domains` (up to 100, Responses API only)                                            | ❌ Not exposed                                              |
| **Model coupling**          | Decoupled — call from any model/code                                                                        | Tightly coupled — Claude orchestrates the loop                        | Tightly coupled — Responses API model orchestrates; or use `gpt-*-search-*` models in Chat Completions | Coupled to Mistral Agents (stateful conversation)          |
| **Standalone search call?** | ✅ Yes — pure search                                                                                         | ❌ No, model decides                                                   | ❌ No, model decides (Responses)                                                                        | ❌ No, agent decides                                        |
| **Official Go SDK**         | ⚠️ `github.com/ollama/ollama/api` exists but **no `WebSearch`/`WebFetch` methods yet** — call REST directly | ✅ `github.com/anthropics/anthropic-sdk-go` (full server-tool support) | ✅ `github.com/openai/openai-go` (Responses API + tools)                                                | ❌ No official Go SDK; community wrappers only              |

**For pi-go:** Anthropic and OpenAI are the only providers with first-class Go SDKs *and* server-side
search-orchestration. Ollama is best as a **standalone search primitive** that pi-go's own agent loop drives. Mistral is
currently the worst Go citizen of the four.

---

## 1. Ollama — Standalone REST primitive

### Architecture

Two flat endpoints, decoupled from any model:

- `POST /api/web_search` — query → `[{title, url, content}]`
- `POST /api/web_fetch` — url → `{title, content, links}`

The model loop is **your responsibility**. Ollama's docs explicitly recommend ~32k context for search agents because
results can be very large. They also ship a Python MCP server so any MCP-aware client (Claude Code, Codex, Goose, Cline)
can call it.

### Pricing

Free tier with "generous" limits per Ollama account. Cloud Pro `$20`/mo, Max `$100`/mo lift caps. **No per-search fee
** — this is the cheapest of the four for high-volume agent loops.

### Go integration (pi-go-relevant)

There is no `WebSearch` method on `github.com/ollama/ollama/api` yet — the official Python and JS libraries got it
first. For Go you call the REST endpoint directly. Pattern from a community Go example:

```go
package ollamasearch

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "time"
)

const (
    searchURL = "https://ollama.com/api/web_search"
    fetchURL  = "https://ollama.com/api/web_fetch"
)

type SearchRequest struct {
    Query      string `json:"query"`
    MaxResults int    `json:"max_results,omitempty"` // 1..10, default 5
}

type SearchResult struct {
    Title   string `json:"title"`
    URL     string `json:"url"`
    Content string `json:"content"`
}

type SearchResponse struct {
    Results []SearchResult `json:"results"`
}

type Client struct {
    http   *http.Client
    apiKey string
}

func New(apiKey string) *Client {
    return &Client{
        http:   &http.Client{Timeout: 30 * time.Second},
        apiKey: apiKey,
    }
}

func (c *Client) Search(ctx context.Context, q string, maxResults int) (*SearchResponse, error) {
    body, _ := json.Marshal(SearchRequest{Query: q, MaxResults: maxResults})
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("build request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+c.apiKey)
    req.Header.Set("Content-Type", "application/json")

    resp, err := c.http.Do(req)
    if err != nil {
        return nil, fmt.Errorf("do request: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode/100 != 2 {
        return nil, fmt.Errorf("ollama search: status %d", resp.StatusCode)
    }

    var out SearchResponse
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, fmt.Errorf("decode: %w", err)
    }
    return &out, nil
}

func main() {
    c := New(os.Getenv("OLLAMA_API_KEY"))
    res, err := c.Search(context.Background(), "kagent CNCF sandbox status", 5)
    if err != nil {
        panic(err)
    }
    for _, r := range res.Results {
        fmt.Printf("%s\n%s\n\n", r.Title, r.URL)
    }
}
```

### Strengths / weaknesses for pi-go

- ✅ Cleanest fit: pi-go owns ranking, dedup, freshness; Ollama is just a primitive. Pairs well with chromem-go for
  re-ranking results before stuffing into context.
- ✅ Cheapest at scale; no token tax on the search call itself.
- ✅ License: results are yours to use (confirmed by Ollama maintainers on HN).
- ❌ No domain allow/block list, no location hints — you'd have to filter post-hoc.
- ❌ Result snippets are short; for serious extraction you'll do a follow-up `web_fetch`.
- ❌ No retrieval transparency; you don't know the underlying engine.

---

## 2. Anthropic — Server-side tool with encrypted citations

### Architecture

The model orchestrates a search loop server-side. You hand Claude a tool definition; Claude decides if/when to search,
performs N searches (capped by `max_uses`), and returns inline citations. The search results are encrypted blobs in the
response — the dev never sees the raw search snippets, but does get human-readable citations.

Backend: Brave Search (per Simon Willison's analysis; not officially documented). Available on the Anthropic API
directly and on Vertex AI for Claude 4+ models.

```json
{
  "type": "web_search_20250305",
  "name": "web_search",
  "max_uses": 5,
  "allowed_domains": ["docs.anthropic.com"],
  "blocked_domains": ["spam.example.com"],
  "user_location": {
    "type": "approximate",
    "city": "Prague", "region": "Prague",
    "country": "CZ", "timezone": "Europe/Prague"
  }
}
```

A newer `web_search_20260209` version is also referenced in the docs and works with the same shape. Companion tool: *
*`web_fetch`** — Claude can fetch and analyze any URL.

### Pricing

`$10` per 1,000 searches **plus** the token cost of the additional sampling calls Claude makes during the loop.
Encryption is included; no extra step on the dev side.

### Go integration (pi-go-relevant)

Official SDK: `github.com/anthropics/anthropic-sdk-go`. Server tools are first-class.

```go
package main

import (
    "context"
    "fmt"

    "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"
)

func main() {
    client := anthropic.NewClient(
        option.WithAPIKey(""), // reads ANTHROPIC_API_KEY
    )

    msg, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
        Model:     anthropic.F(anthropic.ModelClaudeOpus4_6),
        MaxTokens: anthropic.F(int64(2048)),
        Messages: anthropic.F([]anthropic.MessageParam{
            anthropic.NewUserMessage(
                anthropic.NewTextBlock("Latest CNCF Sandbox status of KAgent and Kata Containers integration?"),
            ),
        }),
        Tools: anthropic.F([]anthropic.ToolUnionUnionParam{
            anthropic.WebSearchTool20250305Param{
                Type:    anthropic.F(anthropic.WebSearchTool20250305TypeWebSearch20250305),
                Name:    anthropic.F("web_search"),
                MaxUses: anthropic.F(int64(5)),
            },
        }),
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(msg.Content)
}
```

(Exact symbol names track the SDK version; the SDK is generated from the OpenAPI spec, so `WebSearchTool20250305Param`
and any newer `web_search_20260209` variant follow the same pattern.)

### Strengths / weaknesses for pi-go

- ✅ Best **citation experience** of the four: encrypted citations are stable across multi-turn, model handles "should I
  search again?" autonomously.
- ✅ Domain allow/block + location hints — useful for compliance-sensitive telco use cases.
- ✅ First-class Go SDK with full type safety.
- ❌ Most expensive at $10/1k *plus* sampling tokens.
- ❌ Loop is opaque — you can't easily intercept individual search queries to memoize or rewrite.
- ❌ Coupled to Claude — can't swap model.

---

## 3. OpenAI — Responses API with `web_search` / `web_search_preview`

### Architecture

Two paths:

1. **Responses API** with `tools: [{ "type": "web_search" }]` — modern, recommended. Model orchestrates the search
   loop (non-reasoning fast path, reasoning agentic path, or Deep Research).
2. **Chat Completions API** with the dedicated search-capable models: `gpt-5-search-api`, `gpt-4o-search-preview`,
   `gpt-4o-mini-search-preview` — search is fused into the model, no tool definition needed.

`web_search_preview` is the legacy name; current docs recommend `web_search`. A `web_search_2025_08_26` versioned
variant adds domain filtering.

Backend: OpenAI's index (Bing-class on Azure variant). Three modes: non-reasoning (fast lookup), agentic (reasoning
model decides when to keep searching), and Deep Research (multi-step investigation).

```python
# Reference Python — concept maps 1:1 to Go
response = client.responses.create(
    model="gpt-5",
    tools=[{
        "type": "web_search",
        "filters": {"allowed_domains": ["pubmed.ncbi.nlm.nih.gov", "clinicaltrials.gov"]}
    }],
    include=["web_search_call.action.sources"],
    input="latest semaglutide trials"
)
```

`sources` field returns the **complete** list of URLs the model consulted (often >> the citation count) — useful for
audit trails. Real-time third-party feeds appear as `oai-sports`, `oai-weather`, `oai-finance`.

### Pricing

Web search inherits the rate-limit tier of the model. OpenAI does not publish a flat per-search fee in the same way
Anthropic does — you pay model tokens plus the search-tool overhead bundled into the search-capable model pricing.

### Go integration (pi-go-relevant)

Official SDK: `github.com/openai/openai-go`. Responses API and tools are supported.

```go
package main

import (
    "context"
    "fmt"

    "github.com/openai/openai-go"
    "github.com/openai/openai-go/option"
    "github.com/openai/openai-go/responses"
)

func main() {
    client := openai.NewClient(option.WithAPIKey("")) // OPENAI_API_KEY

    resp, err := client.Responses.New(context.TODO(), responses.ResponseNewParams{
        Model: openai.F("gpt-5"),
        Input: responses.ResponseNewParamsInputUnion{
            OfString: openai.F("latest CNCF KAgent release notes"),
        },
        Tools: openai.F([]responses.ToolUnionParam{
            {OfWebSearch: &responses.WebSearchToolParam{
                Type: openai.F(responses.WebSearchToolTypeWebSearch),
            }},
        }),
        Include: openai.F([]responses.ResponseIncludable{
            responses.ResponseIncludableWebSearchCallActionSources,
        }),
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(resp.OutputText())
}
```

(Exact field paths track the SDK version; the openai-go SDK is also OpenAPI-generated.)

### Strengths / weaknesses for pi-go

- ✅ **Best transparency** — `sources` field gives you every URL consulted, not just cited ones.
- ✅ Three-mode design (fast / agentic / Deep Research) lets you trade latency vs. depth at request time.
- ✅ Domain allow-list up to 100 URLs.
- ✅ First-class Go SDK.
- ❌ Tool/model coupling is messier than Anthropic — different models support different web-search variants;
  `gpt-4.1-nano` and `gpt-5` minimal-reasoning don't support it at all.
- ❌ Pricing isn't a clean "per search" line item; harder to budget.

---

## 4. Mistral — Built-in connector on Agents API

### Architecture

Web search is a *connector* on Mistral's stateful **Agents API**, not a tool you bolt onto Chat Completions. You create
an agent (`client.beta.agents.create`) with `tools=[{"type": "web_search"}]` or `web_search_premium` (adds verified news
provider access).

Conversations are persistent and stateful server-side — a fundamentally different model from Anthropic/OpenAI's
request/response shape. Every turn is logged as structured `Entries`, with `tool.execution` and `message.output`
including reference chunks interleaved with text — Mistral's RAG-style citation format.

Benchmark numbers Mistral publishes are striking: SimpleQA goes from **23% → 75%** for Mistral Large and **22% → 82%**
for Mistral Medium with web search enabled.

### Pricing

- `$30 / 1000` standard web search calls
- `$50 / 1000` premium news access
- Plus model tokens (Medium 3: $0.4 in / $2 out per million tokens)

This is the **most expensive per-search** of the four.

### Go integration (pi-go-relevant)

**No official Go SDK.** Mistral ships Python (`mistralai`) and TypeScript clients. For Go you'd:

1. Use community wrappers (search GitHub for `mistral-go` — quality and maintenance vary), or
2. Hit the REST API directly (`api.mistral.ai`), or
3. Use the OpenAI-compatible endpoint Mistral exposes for chat completions — but **note that the Agents API and
   its `web_search` connector are Mistral-specific and do not have an OpenAI-compatible analog.**

Reference shape (curl-equivalent, Go would follow the standard `net/http` pattern from §1):

```bash
curl https://api.mistral.ai/v1/agents \
  -H "Authorization: Bearer $MISTRAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mistral-medium-latest",
    "name": "Websearch Agent",
    "instructions": "Use web_search for up-to-date information.",
    "tools": [{"type": "web_search"}]
  }'
```

### Strengths / weaknesses for pi-go

- ✅ Stateful conversations remove some bookkeeping if your app is conversational.
- ✅ Best published accuracy lift for grounded QA.
- ✅ Built-in handoffs to other agents (web_search → calculator → logger).
- ❌ **Worst Go story of the four.** No official SDK = manual REST.
- ❌ Most expensive per call.
- ❌ Statefulness is a tax for stateless agent runtimes; pi-go would have to manage agent IDs and conversation lifecycle.

---

## 5. Cross-cutting comparison

### Retrieval quality & freshness

- **Anthropic (Brave)** and **OpenAI (Bing-class)** are the strongest general-purpose indexes; expect best recency on
  news and tech docs.
- **Ollama**'s engine is undisclosed but practical for general queries; ranking quality is "good enough for agents" per
  community feedback.
- **Mistral** doesn't disclose backend; their SimpleQA jump suggests a competent index plus aggressive RAG-style
  chunking.

### Citation models

| Provider  | Citation style                                               |
|-----------|--------------------------------------------------------------|
| Anthropic | Encrypted opaque blobs + human-readable citations, automatic |
| OpenAI    | Inline citations + separate `sources` array                  |
| Mistral   | `tool_reference` chunks interleaved with text                |
| Ollama    | Raw `[{title, url, content}]` — DIY citations                |

### Agent-loop control (matters for pi-go)

- **Maximum control:** Ollama (you own the loop end-to-end)
- **Maximum convenience:** Anthropic / Mistral (model owns the loop)
- **Middle ground:** OpenAI — you can choose non-reasoning fast path *or* let a reasoning model run agentic search

### Compliance / governance

- Domain allow/block lists: **Anthropic** and **OpenAI** only.
- Location hints: **Anthropic** and **OpenAI** only.
- Audit trail of all consulted URLs: **OpenAI**'s `sources` field is the cleanest.
- Result encryption: **Anthropic** is the only one that encrypts.

---

## 6. Recommendation for pi-go

Given pi-go's design (Go-native AI coding agent runtime, MemPalace semantic memory, multi-provider LLM support, manual
DI), the natural mapping is:

1. **Ollama as the default search primitive.** It matches pi-go's "you own the loop" architecture, costs nothing per
   call, and is trivial to wrap in `net/http`. Pair with chromem-go for re-ranking and MemPalace for caching/dedup. The
   fact that there's no official Go method yet is actually a feature — a thin internal client keeps you decoupled.

2. **Anthropic web_search as the "high-quality" tier** for tasks where citation quality and the agentic loop matter (
   deep research, customer-facing answers). Use the official Go SDK; Claude's encrypted citations are the cleanest UX.

3. **OpenAI web_search where you specifically need `sources` audit trails** or want to use Deep Research mode for
   long-horizon tasks. Same model-agnostic Go SDK shape as Anthropic.

4. **Skip Mistral web_search for now.** No Go SDK + most expensive per call + statefulness tax. Revisit if Mistral ships
   an official Go SDK or if a specific telco customer requires EU-hosted inference with web grounding.

### Suggested abstraction in pi-go

```go
package search

import "context"

type Result struct {
	Title   string
	URL     string
	Snippet string
	Score   float64 // post-rerank, optional
}

type Provider interface {
	Search(ctx context.Context, query string, opts ...Option) ([]Result, error)
	Fetch(ctx context.Context, url string) (string, error)
}

// Concrete impls:
//   ollama.Provider     — REST, free, owns-the-loop
//   anthropic.Provider  — wraps SDK server-tool, returns flattened citations
//   openai.Provider     — wraps Responses API, exposes sources separately
```

This gives pi-go the polymorphism it already uses for LLM providers, and makes the "which provider for which query"
decision a runtime config instead of a code change.

---

## Sources

Anthropic web search docs · Ollama web search blog & docs · OpenAI tools/web-search docs · Mistral Agents API docs ·
Simon Willison's Anthropic web search analysis · DataCamp Mistral Agents tutorial · Glukhov.org Ollama Go integration
guide · Vertex AI Anthropic web search docs.