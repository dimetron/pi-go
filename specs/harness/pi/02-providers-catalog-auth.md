# 02 — Providers, Model Catalog, Auth & Config

## Side-by-side summary

| | pi-go | pi |
|---|---|---|
| Providers | **10** | **42** |
| Dispatch | `switch` on provider | per-model `api` field |
| Native Ollama | ✓ | ✗ |
| Catalog sources | union of 3, no schema | generated + sha256 drift gate |
| OAuth flows | 1 | 9 |
| Prompt caching | Anthropic only | Anthropic + emulated + Mistral |
| 1h cache accounting | ✗ | ✓ |
| Credential refresh | ✗ | ✓ |
| Rate limiting | ✓ proactive | ✗ |
| Virtual models | ✗ | ✓ |
| Config top-level keys | ~27 (+~50 nested) | ~60 |

## 2.1 Provider inventory

### pi-go — a 10-arm switch

`internal/provider/provider.go:725-776` is a single switch. There is **no
registry**; every provider is a hardcoded `case`.

| Provider | Constructor | Default base URL | SDK |
|---|---|---|---|
| `ollama` | `NewOllama` (`:744`) | `localhost:11434` | **native** `ollama/api` |
| `gemini` | `NewGemini` | `generativelanguage.googleapis.com` | **native** `genai` |
| `openai` | `NewOpenAI` | `api.openai.com` | **native** `openai-go/v3` |
| `azure` | `NewAzureOpenAI` | from env | **native** `openai-go/v3` |
| `anthropic` | `NewAnthropic` | `api.anthropic.com` | **native** `anthropic-sdk-go` |
| `mistral` | `NewMistral` | `api.mistral.ai/v1` | `openai-go/v3` |
| `openrouter` | `NewOpenRouter` | `openrouter.ai/api/v1` | `openai-go/v3` |
| `xai` | `NewXAI` | `api.x.ai/v1` | `openai-go/v3` |
| `opencode` | `NewOpenCode` | `opencode.ai/zen/go/v1` | **delegating** |
| `agentgateway` | `NewAgentGateway` | `localhost:4000` | **delegating** |

**Every provider is an SDK wrapper.** No raw-HTTP provider exists. `mistral`,
`openrouter`, `xai`, and `azure` all wrap `openai-go` because they are
OpenAI-wire-compatible, with SDK gaps patched via `SetExtraFields` and raw-JSON
re-parsing (`mistral.go:101`, `openai_completions.go:393`).

**Compositing is per route, not per model:**

```go
// internal/provider/opencode.go:69-79
family, ok := opencodeGoModelCatalog[modelName]
switch family {
case "chat", "responses":
  return NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
case "messages":
  return NewAnthropic(ctx, modelName, apiKey, opencodeAnthropicBaseURL(baseURL), thinkingLevel, opts)
```

```go
// internal/provider/agentgateway.go:52-59
if GatewayRoutesToGemini(modelName) { return newAgentGatewayGemini(...) }
if routesToOllama(modelName)         { opts.UseLegacyMaxTokens = true }
llm, err := NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
```

`agentgateway` needs a fake key because the genai SDK refuses to build keyless
(`agentGatewayPlaceholderKey`, `agentgateway.go:19`).

**Key env-var mapping is one function** (`provider.go:795-806`):
`anthropic→ANTHROPIC_API_KEY`, `openai→OPENAI_API_KEY`,
`azure→AZUREOPENAI_API_KEY`, `gemini→GEMINI_API_KEY`, default =
`strings.ToUpper(name)+"_API_KEY"`. Explicitly documented as
single-source-of-truth.

**`openai-codex` is not a `NewLLM` case.** It is reached through `NewOpenAI` +
`BackendName` sniffing (`provider.go:177-189`): if the key is a codex OAuth token
(`auth.IsCodexOAuthToken`), the backend is `openai-codex-chatgpt`. Both
`openai_codex.go` comments cite *"pi-mono's openai-codex-responses provider"* —
codex is deliberately modelled as an OpenAI **mode**, not a provider.

### pi — per-model wire API

`builtinProviders()` (`packages/ai/src/providers/all.ts:136-181`) returns
**42** factories. `KnownProvider` (`types.ts:39-81`) has the same 42 members.

Ten wire APIs (`types.ts:17-27`):

```
openai-completions   mistral-conversations   openai-responses
azure-openai-responses   openai-codex-responses   anthropic-messages
bedrock-converse-stream   google-generative-ai   google-vertex   pi-messages
```

Because the API is a property of the **model**, one provider can serve several
wire protocols. That is why `minimax`/`minimax-cn` run Anthropic-wire on an
OpenAI-shaped host list, and why `openai-codex` is both a provider and a distinct
`api` id.

Third-party SDKs: `@anthropic-ai/sdk 0.129.0`, `openai 7.19.0`,
`@google/genai 2.21.0`, `@aws-sdk/client-bedrock-runtime 3.1127.0`.
**Everything else is raw `fetch`** — 12,827 LOC in `api/`, with
`openai-completions.ts` (1,726) implementing the wire for ~20 providers.

Full list: `amazon-bedrock`, `ant-ling`, `anthropic`, `azure-openai-responses`,
`baseten`, `cerebras`, `cloudflare-ai-gateway`, `cloudflare-workers-ai`,
`deepseek`, `fireworks`, `github-copilot`, `google`, `google-vertex`, `groq`,
`huggingface`, `kimi-coding`, `meta`, `minimax`, `minimax-cn`, `mistral`,
`moonshotai`, `moonshotai-cn`, `nvidia`, `openai`, `openai-codex`, `opencode`,
`opencode-go`, `openrouter`, `qwen-token-plan` ×3, `radius`, `together`,
`typesafe`, `vercel-ai-gateway`, `xai`, `xiaomi` ×4, `zai`, `zai-coding-cn`.

Grouping is by **data shard**: 42 `.models.ts` files each import one
gitignored `data/<provider>.json` and flatten it. `models.generated.ts` holds
three parallel 42-keyed catalogs — `MODELS` (`:47`), `IMAGE_MODELS` (`:135`),
`CLASSIFIER_MODELS` (`:223`).

`radius` has **no static catalog** — fully dynamic from `radius-config.ts`.
pi-go's switch-based `NewLLM` structurally cannot express a provider with no
static catalog.

### Review — providers

**pi-go's 10 cover the majors.** Anthropic, OpenAI, Gemini, Ollama, Mistral,
OpenRouter, xAI, Azure, plus two gateways. That is a defensible set — Groq,
Cerebras, Together, Fireworks, and DeepSeek are all OpenAI-compatible and
reachable through `openrouter` or `agentgateway` today.

**The gap that matters is the data model, not the count.** pi-go dispatches on
`info.Provider`; pi dispatches on `model.api`. The consequence is concrete:
adding `deepseek` to pi-go means a new `case` that constructs an OpenAI-compatible
client with a different base URL — trivial. Adding `google-vertex` means ADC
auth and regional endpoints — not expressible as a base URL. Adding
`amazon-bedrock` means the AWS credential chain and `bedrock-converse-stream` —
not expressible with an OpenAI-compatible client at all.

**Recommendation**: do not grow the switch. Introduce a provider registry where
each entry declares `{name, api, baseURL, authKind, catalogShard}` and `api`
resolves to one of a small number of wire implementations. That makes the 30
missing OpenAI-compatible providers a data change and leaves Bedrock/Vertex as
genuinely new work. This is the same move that makes `opencode`'s per-model
family switch (`opencodeGoModelCatalog`) general rather than special.

## 2.2 Prompt caching

### pi-go — Anthropic only

`cache_apply.go` is 15 lines and exposes only a flag check. Its own header
(`cache_apply.go:4-6`) states: *"Today only the Anthropic provider honors it;
the openai, gemini, mistral, xai, ollama, and azure builders ignore the option
entirely."*

`anthropic_caching.go:38-60` stamps **3 `cache_control: ephemeral` markers** —
last tool, last system block, last cacheable message block (walking backwards
past thinking blocks, which return nil from `GetCacheControl()`). Anthropic
allows 4; pi-go uses 3. The comment at `:13-14` records that as a deliberate
choice.

Cache read accounting is Anthropic-only (`anthropic.go:590-591`, `:660-663`).

Mistral is the one exception, via `SetExtraFields` since openai-go has no field:

```go
// internal/provider/mistral.go:130
extra := map[string]any{"prompt_cache_key": promptCacheKey}
```

xAI sets a `prompt_cache_key` **header** (`xai.go:25`). OpenAI, Azure, Gemini,
and Ollama get no explicit cache control at all.

### pi — three implementers plus a TTL tier

Anthropic is primary (`anthropic-messages.ts:1130-1204`): same 3 breakpoints
(system `:1173`/`:1180`, messages `:1189`, tools `:1204` gated on
`compat.supportsCacheControlOnTools`). pi adds a **`ttl`** field:

```ts
// packages/ai/src/api/anthropic-messages.ts:91
cacheControl: { type: "ephemeral", ...(ttl && { ttl }) },
```

which is what `Usage.cacheWrite1h` (types.ts:433) bills at 2× — Anthropic's
1h cache-write tier.

**Second implementer**: `openai-completions` in Anthropic-emulation mode.
`compat.cacheControlFormat === "anthropic"` (`openai-completions.ts:1073`) routes
to `applyAnthropicCacheControl`, so OpenAI-wire providers that speak Anthropic
dialect get real markers.

**Third**: Mistral via `promptCacheKey`, with a length clamp OpenAI does not
have:

```ts
// packages/ai/src/api/openai-prompt-cache.ts
export const OPENAI_PROMPT_CACHE_KEY_MAX_LENGTH = 64;
```

Cache retention is declarative per-model: `promptCache?:
Partial<Record<CacheRetention, number>>` (`types.ts:116`).

### Review — prompt caching

pi-go's implementation is correct and deliberately scoped — Anthropic is where
caching matters most and the provider is the one where the markers are
provider-specific. The 3-vs-4 breakpoint choice is a real cost on large tool
sets, and is worth revisiting now that pi-go has 29 tools.

pi's `ttl` + `cacheWrite1h` accounting is a genuine gap: a user on Anthropic's
1h cache tier gets **wrong cost numbers** in pi-go, because there is no 1h tier
in the pricing table at all. That is a correctness bug in spend reporting, not
a missing feature.

The `compat.cacheControlFormat` trick is worth noting but not copying yet —
pi-go's provider set does not have a provider that speaks Anthropic dialect on
an OpenAI host.

## 2.3 Thinking / reasoning levels

### pi-go — two plumbing paths, deliberately split

`provider.go:750-756`:

- **Positional arg** (Anthropic, Mistral, OpenRouter, xAI, OpenCode):
  `NewAnthropic(ctx, model, key, url, thinkingLevel, opts)`
- **`opts` field** (OpenAI-compatible): `fillThinkingLevel(opts, thinkingLevel)`
  at `provider.go:741` → `opts.ThinkingLevel` (`:706`), because the routes that
  forward opts (`agentgateway`, `opencode`) would each need a parameter threaded
  just to pass it on.

**OpenAI effort clamping is per-model-generation**
(`openai_reasoning.go:87-113`), encoding a measured compatibility matrix across
four predicates:

| Predicate | Line | Encodes |
|---|---|---|
| `supportsMax` | `:122` | "max" only on gpt-5.6/gpt-6; gpt-5.4/5.5 **reject** it |
| `oaiSupportsReasoningOff` | `:137` | `none` on gpt-5.1+, never on o-series or gpt-6 |
| `oaiSupportsTopEffort` | `:156` | xhigh/max on gpt-5.4+ only |
| `oaiThinkingLevelForModel` | `:180` | hardcodes `high` for `gpt-5.6-luna` |

The `supportsMax` comment (`:118-121`) is worth quoting: *"gpt-5.4 and gpt-5.5
accept xhigh but reject max, so treating the two as one tier would 400 those
models."*

**Thought signature** (Gemini 3) rides a non-standard OpenAI-wire sibling and must
be re-parsed from raw JSON because openai-go lacks the field:

```go
// internal/provider/openai_thought_signature.go:29-32
const (
  oaiExtraContentField     = "extra_content"
  oaiExtraContentVendor    = "google"
  oaiThoughtSignatureField = "thought_signature"
)
```

An unreadable signature decodes as `nil` — dropped, not fatal (`:48-52`).

**Reasoning tokens** ride one ADK field, `ThoughtsTokenCount`, surfaced in OTel
(`hooks.go:433-434`) and the TUI breakdown. They are **not separately costed**.

### pi — declarative map + nearest-neighbour clamp

7 levels (`types.ts:84-86`): `off, minimal, low, medium, high, xhigh, max`.
`ThinkingLevelMap = Partial<Record<ModelThinkingLevel, string | null>>` —
`string` = override to send, **`null` = unsupported**, `undefined` = pass through.

```ts
// packages/ai/src/models.ts:1216-1226
export function getSupportedThinkingLevels(model) {
  if (!model.reasoning) return ["off"];
  return EXTENDED_THINKING_LEVELS.filter((level) => {
    const mapped = model.thinkingLevelMap?.[level];
    if (mapped === null) return false;
    if (level === "xhigh" || level === "max") return mapped !== undefined;  // opt-in only
    return true;
  });
}
```

`clampThinkingLevel` (`:1228-1247`) walks **up** to find a supported level, then
back down — never fails hard.

The map is **generated from models.dev**
(`scripts/models-dev-reasoning-options.ts:15-28`), with verified effort values
superseding models.dev where it omits metadata.

Per-API interpretation diverges sharply:
`anthropic-messages.ts:907-926` switches between effort strings and
`budget_tokens` on `compat.forceAdaptiveThinking` (`:945-961`);
`openai-completions.ts:876-931` has **four** dialect branches (zai
`thinking: {type:"enabled"}`, baseten, two `enable_thinking` variants).

Signatures round-trip via `ToolCall.thoughtSignature` (`types.ts:422`) and are
**dropped on model switch** (`transform-messages.ts:131-133`) — a correctness
detail pi-go also needs and does not have.

**Per-model thinking defaults**: `settings.modelThinkingLevels:
Record<"provider/modelId", ThinkingLevel>` (`settings-manager.ts:138`). No
pi-go equivalent.

### Review — thinking levels

Both sides encode measured per-model incompatibilities. pi-go's are hand-written
Go predicates; pi's are generated from models.dev. Both are right about the
problem.

pi-go's `gpt-5.6-luna → high` hardcode (`:180`) is a pragmatic workaround for a
model that does not declare a default. pi's generated map would give the same
result more principledly, and pi-go's `make fetch-models` pipeline could carry a
`thinkingLevelMap` field per model the same way.

**One concrete correctness gap**: pi-go does not drop thought signatures on
model switch. Switching from Gemini 3 to an OpenAI model mid-session leaves a
`thought_signature` on a tool call the new model has never seen. pi handles this
at `transform-messages.ts:131-133`; pi-go should too.

## 2.4 Model catalog

### pi-go — union of three sources, no schema

```go
// internal/provider/catalog.go:53-62
func CatalogFor(provider string) []string {
  ids := KnownModels[provider]
  if embedded, ok := loadEmbeddedCatalogIDs(provider); ok { ids = append(ids, embedded...) }
  if cached, ok := loadCatalogIDs(cachePath(provider)); ok { ids = append(ids, cached...) }
  return uniqueSorted(ids)
}
```

The comment at `catalog.go:44-52` records the bug this fixed: the cache **used
to be returned alone**, so a short refresh silently dropped everything the
embedded snapshot listed.

Three layers:
1. Hard-coded `KnownModels` (`model_catalog.go:66-151`) — curated only for
   gemini/mistral/xai/openrouter; anthropic+openai loaded from JSON
2. Embedded `embed.FS` `modeldata/*.json` (11 files), provenance pinned to blob
   SHAs in `modeldata/README.md:5-8`
3. XDG runtime cache `os.UserCacheDir()/pi-go/models/<provider>.json`, atomic
   temp+rename (`catalog.go:24-35`)

**Validation is structural only.** The equivalents are `uniqueSorted`
(`model_catalog.go:269`) and `TestEmbeddedCatalogPresentForEveryProvider`. Ollama
is deliberately exempt. `modeldata/README.md:61-63` documents a **manual step**:
"check for `skip` lines" after regeneration.

**Pricing**: models.dev refresh (`pricing.go:19`) cached at
`modelsdev-pricing.json`. Ollama Cloud pricing is **scraped** from
ollama.com/pricing by `make fetch-ollama-pricing` — no API exists, no runtime
refresh (`modeldata/README.md:55-66`). `ollama_pricing.go:59-70` exposes
off-peak and peak (weekday 12:00–18:00 UTC) rates.

### pi — generated artifact with cryptographic drift detection

Pipeline: `models.dev` + `OpenRouter /models` →
`scripts/generate-models.ts` (3,680 LOC) → `providers/data/<provider>.json`
(**gitignored**) → `*.models.ts` shards → `models.generated.ts`.

`--strict` turns every soft failure into a hard error (11 sites in
`generate-models.ts`). Hard-coded overrides abound where the feed is wrong:
`MODELS_DEV_OPENAI_UNSUPPORTED_MODEL_IDS = new Set(["gpt-5.6"])` (`:360`), Kimi
zero-cost (`:340`), Baseten GLM-5.2 text-only despite models.dev claiming image
input (`:1565`), Vertex cache-rate corrections (`:1641`).

**Drift detection is cryptographic and gates the offline build:**

```
packages/ai/package.json:64-65
"build":         "npm run generate-models && npm run build:offline",
"build:offline": "npm run check:model-data && tsc -p tsconfig.build.json && ..."
```

`check:model-data` → `validateGeneratedModelData` (`model-data.ts:299-302`)
enforces:
- **schema version** `MODEL_DATA_SCHEMA_VERSION = 6` (`:5`)
- **`structureHash`** — sha256 of the normalized provider→model→api map
  (`:122-130`) vs manifest (`:240-242`)
- per-file sha256 for every `data/*.json` (`:257-259`)
- field-level validation per model (`:143-206`): id/provider/api agreement,
  `reasoning` boolean, finite `contextWindow`/`maxTokens`, modality lists, all
  cost fields finite
- `type:id` key identity (`:275-277`) — a model cannot drift between chat/image/classifier
- shard ↔ aggregator agreement via `assertExactModelIds` (`:41`)

So `build:offline` is reproducible offline and a stale shard **fails the
build**.

### Review — model catalog

pi-go's union-of-three is more forgiving and survives a partial fetch better.
That is a genuine advantage for a CLI that must work offline on a plane. pi's
replace-on-regenerate model would lose the embedded snapshot if the fetch fails,
which is why pi needed `build:offline` + sha256 to make it safe.

The gap is **drift detection**. pi-go's guarantee is "the file exists"; pi's is
"the file is internally consistent, matches the manifest hash, and every field
is well-formed." A cheap improvement: have `make fetch-models` fail if any
provider's output is empty, the same way `--strict` does in pi. That catches the
class of bug where a fetch returns `{}` and silently produces a catalog with zero
models.

pi-go's Ollama pricing scrape is **worse than pi's approach**: pi carries the
same knowledge as a generation-time override in code, which is reviewable; pi-go
has a shell script and a README instruction to run it when Ollama changes a
price. Both are unsatisfiable without an API; pi's is at least versioned with the
code that uses it.

## 2.5 Auth

### pi-go — one provider, three mechanisms

`internal/auth/` is 1,463 LOC but **`Providers()` returns exactly one entry**
(`auth.go:135-170`): `codex`, `EnvVar: OPENAI_API_KEY`.

| Flow | Provider | Evidence |
|---|---|---|
| OAuth device auth | codex | `auth.go:160-166` |
| PKCE (preferred=false) | codex | `auth.go:185` |
| Manual paste | codex | `StartManualCodeFlow :273` |
| API key | all 10 | via `APIKeyEnvVar` |

Device auth is preferred **because it needs no localhost listener** — explicitly
so it works in a dev container, Codespace, or over SSH (`auth.go:159-161`).
`codex_device.go` is a bespoke 15-min/5s poll.

**Token storage: plaintext** `~/.pi-go/.env`, dir `0700`, file `0600`
(`auth.go:884-910`). `IdentifyKey` (`:848-873`) classifies structurally:
`sk-`/`sk_` → API key; 3-segment JWT with `https://api.openai.com/auth` payload →
codex OAuth. pi-go implements a TLS preflight to `auth.openai.com` before OAuth
(`:952-955`).

There is **no keychain**.

### pi — nine flows, injectable store

`resolveProviderAuth` (`auth/resolve.ts:35-44`) is a 6-step precedence chain:

```
1. signal.throwIfAborted()
2. overlay env if overrides.env
3. IF overrides.apiKey AND provider.auth.apiKey → resolveApiKey() → RETURN   :50
4. stored = await credentials.read(provider.id)                              :63
5. IF stored:
     oauth → resolveStoredOAuth(...)                                        :65-73
     api_key → resolveApiKey(...)                                           :74-77
     ELSE → return undefined   (hard-fail, no silent fallback)              :78
6. ambient: resolveApiKey(ctx, envApiKey, ...)                              :80-84
```

The header comment (`resolve.ts:27-33`) states: *"No silent env fallback after a
failed refresh."*

**Refresh is double-checked-locked** inside the store's only write path
(`resolve.ts:113-126`):

```ts
const minimumValidityMs = Math.max(DEFAULT_OAUTH_MINIMUM_VALIDITY_MS, minOAuthValidityMs ?? 0);
post = await credentials.modify(providerId, async (current) => {
  if (current?.type !== "oauth") return undefined;      // logged out meanwhile
  if (!expiresSoon(current)) return undefined;          // another process refreshed
  return await oauth.refresh(current, refreshSignal);
});
if (post?.type !== "oauth") return undefined;            // logged out meanwhile
```

This prevents both expiry and **double-rotation of a rotated token**.

Nine flows, lazy-loaded via a variable specifier so bundlers never pull Node
builtins into browser builds (`auth/oauth/load.ts:6-14`):

| Flow | Mechanism |
|---|---|
| anthropic | PKCE + local server **or** manual paste (298 LOC) |
| github-copilot | device_code (507) |
| openai-chatgpt | PKCE + **dynamic client registration** (310) |
| openai-codex | device_code (435) |
| kimi-coding | device_code (296) |
| openrouter | PKCE → yields a long-lived API key, `refresh: ""` (169) |
| xai | device_code (239) |
| meta | device_code (208) |
| radius | device_code, gateway-configured (321) |

**Storage is app-injected, never assumed.** `CredentialStore`
(`auth/types.ts:64-91`) has `read/list/modify/delete`; the default impl is
**in-memory** (`credential-store.ts:10-13`) with the comment *"Apps inject
persistent stores."* There is no keychain in `pi-ai` either — coding-agent
supplies `AuthStorage`.

Env var map is explicit (`env-api-keys.ts:84-127`) with three special cases:
`github-copilot→COPILOT_GITHUB_TOKEN`, `huggingface→HF_TOKEN`,
`google→GEMINI_API_KEY`, `google-vertex→GOOGLE_CLOUD_API_KEY`.

### Review — auth

**Neither side uses a keychain.** pi-go writes plaintext to `.env` with 0600;
pi defines an interface and ships in-memory. pi-go's plaintext default is the
more exposed of the two, but pi's design makes injection the norm so the
responsibility is visible where it is incurred.

pi-go's device-code-first choice for codex is **better** than pi's PKCE-first
choice for anthropic in one respect: it needs no localhost listener, so it works
in containers and over SSH. pi has the same capability via `device-code.ts` and
uses it for 5 of 9 flows; anthropic gets PKCE because Anthropic's console does not
offer a device flow. That is a provider constraint, not a design gap.

The **credential refresh gap is real and matters for pi-go's codex users**. A
stored codex token that expires simply stops working with no re-login prompt, and
pi-go has no `expiresSoon` check anywhere. Adding one — read the JWT `exp`,
warn at 5 minutes, re-run the device flow — is small and valuable.

**1 vs 9 OAuth flows** is the headline, but the composition matters more than
the count: pi's flows are shared across providers by mechanism
(`device-code.ts` 98 LOC serves 5 flows; `pkce.ts` 34 LOC serves 3). pi-go has
`codex_device.go` bespoke. The right shape is pi's — extract the two mechanisms,
then a provider is a small table entry.

## 2.6 Rate limiting, retry, guardrails

### pi-go

**Quota unit: input tokens per minute, scoped per `provider|model|baseURL`**
(`ratelimit.go:406-408`). Named once in `NewLLM` (`provider.go:732-735`) because
the quota is **per account, not per client** — two clients on the same budget
must share a bucket.

Defaults are deliberately sparse — **only Gemini** (`defaults.go:44-50`) at 1.8M
input tok/min. `isGeminiTarget` (`:62-73`) matches on *model* not provider,
because `agentgateway` forwards `gemini-*` to generativelanguage while pi-go
believes it talks to `localhost:4000`. Resellers excluded.

**Retry classification is substring matching, and `ServerDelay` outranks both
lists:**

```go
// internal/retry/retry.go:149-163
if _, ok := ServerDelay(err); ok { return false }   // a supplied window means it clears
return containsAny(strings.ToLower(err.Error()), terminalPatterns)
```

```go
// internal/retry/retry.go:165-183
// This beats the terminal patterns because Gemini's per-minute token limit
// reuses that prose verbatim ("You exceeded your current quota") while
// still telling us exactly when its window reopens.
if _, ok := ServerDelay(err); ok { return true }
if containsAny(msg, terminalPatterns) { return false }   // Terminal wins on a tie
```

Terminal: quota exhaustion, auth/plan prose. Transient: 429/500/502/503/504,
connection reset/refused, `deadline exceeded`, `EADDRNOTAVAIL` in both macOS and
Linux spellings, HTTP/2 `stream error:`, `unexpected EOF`. `DefaultConfig`: 5
retries, 1s initial, **60s max**.

`internal/guardrail` tracks a daily budget (default 50M tokens) with
`FreshInputTokens` (`:40`) and `CacheHitRate` (`:51`) handling the Anthropic 0.1×
discount, plus context-window tracking.

### pi

**No proactive rate limiting at all.** Cost falls entirely on reactive retry.
pi's retry policy is nested and settings-driven (`settings-manager.ts:46-52`):
agent-level `{enabled, maxRetries: 3, baseDelayMs: 2000, maxAgentDelayMs: 60000}`
plus provider-level `{timeoutMs, maxRetries, maxRetryDelayMs}`.

Durable has `DEFAULT_RETRY_POLICY` (`harness/agent.ts:18-22`) with the same
values, and — importantly — the backoff deadline is **durable**: on retry it
commits `{phase:"retry", ..., until}`, so a crash during the sleep resumes the
same attempt rather than restarting the wait (`compaction.ts:180-207`).

### Review — rate limiting & retry

**pi-go is ahead here and pi should know it.** The `ServerDelay`-outranks-both
design is genuinely subtle and correct — it handles the real case where Gemini's
per-minute limit uses the same prose as a terminal quota error but carries a
retry window. The per-`provider|model|baseURL` scope is right (quota is per
account). And pi-go's split — proactive admission control plus reactive
classification from one shared classifier — is more robust than either layer
alone.

pi's **durable retry deadline** is a nice idea with no direct pi-go analogue,
though it depends on the durable runtime pi-go does not have.

pi's settings-driven nested retry config is more user-controllable than
pi-go's constants; pi-go exposes no `retry` block in `config.json`. That is a
real gap in user-facing flexibility, cheap to close.

## 2.7 Config schema

### pi-go — ~27 top-level + ~50 nested

`Config` (`config.go:120-183`): `roles`, `defaultModel` (deprecated),
`defaultProvider`, `thinkingLevel`, `theme`, `baseURLs`, `extraHeaders`,
`insecureSkipTLS`, `maxOutputTokens`, `caCertPath`, `disableSystemCAs`,
`traceHTTP`, `planAutoFix`, `tools`, `mcp`, `hooks`, `maxDailyTokens`,
`contextWindow`, `rateLimits`, `compactor`, `autoCompact`, `memory`, `palace`,
`a2a`, `llms`.

Nested: `RoleConfig` (`:27`), `MemoryConfig` (`:39`), `PalaceConfig` (`:186`),
`CompactorConfig` (`:204`), `AutoCompactConfig` (`:215`), `MCPConfig` (`:229`),
`RateLimitConfig` (`:105`), `HookConfig` (`:19`), `A2AConfig` (`:257`),
`LLMSConfig` (`:275`).

Two design details worth copying:

**Pointer fields to distinguish unset from disabled.** `RateLimitConfig` uses
`*int` for both limits "so 'unset' ≠ 'explicitly unlimited'". `planAutoFix` is
`*bool` defaulting to on.

**`json:"-"` on derived fields.** `reroutedLLMS` (`:176`) and `inferredLLMS`
(`:182`) are excluded because `Save` marshals the whole `Config` — an inference
drawn from a project's `mcp.json` would otherwise be written into the global
file by an unrelated save.

**The `role` concept.** `RoleConfig` is a named task slot carrying more than a
model:

```go
type RoleConfig struct {
  Model          string `json:"model"`
  Provider       string `json:"provider,omitempty"`
  AdvisorModel   string `json:"advisorModel,omitempty"`
  AdvisorMaxUses int    `json:"advisorMaxUses,omitempty"`
  AdvisorCaching bool   `json:"advisorCaching,omitempty"`
}
```

`ResolveRole` (`:310-337`) falls back requested → `"default"` → `ErrNoDefaultRole`,
then infers provider by prefix (`autoDetectProvider :339-378`). A role lets
auxiliary work (compaction, advisor, subagents) run on a cheaper model, with the
budget and caching policy travelling with the binding.

### pi — ~60 top-level keys

`Settings` (`settings-manager.ts:133-193`), two scopes (`:267`), project
overrides global, `deepMergeSettings` (`:246`).

Beyond the overlap, pi has heavy **TUI/terminal/UX** surface pi-go lacks
entirely: `tuiMode` (`fullscreen`/`regular`), `fullscreenExitOutput`,
`fullscreenScrollbar`, `fullscreenCopyOnSelect`, `fullscreenWheelScrollLines`,
`terminal.{showImages, imageWidthCells, clearOnShrink, showTerminalProgress,
hyperlinks, images, trueColor}`, `markdown.mermaid`, `codemode.{mode,
inlineBudget}`, `editorPaddingX`, `outputPad`, `autocompleteMaxVisible`,
`showHardwareCursor`, `images.autoResize`.

And **trust/security**: `defaultProjectTrust` (`ask`/`always`/`never`, global
only), plus `--approve`/`--no-approve` CLI flags.

And **identity**: `deviceId` (stable UUID), `trackingId`, `enableInstallTelemetry`
(default true), `enableAnalytics` (default **false**).

And **models**: `modelThinkingLevels` (per-model defaults),
`scoped-models`/`enabledModels` patterns for Ctrl+P cycling, `thinkingBudgets`.

`defaultTools` has custom merge semantics (`:214-231`): plain names **replace**,
`+name`/`-name` **append** modifiers, merged across settings layers.

### Review — config

pi-go's config is **operationally deeper** and pi's is **interface deeper**. pi-go
has `rateLimits`, `autoCompact.shed/summarizePercent`, `planAutoFix`,
`memory.compressor`, `palace.*`, `llms.sources`, `hooks[]`, `maxDailyTokens`,
`caCertPath`/`disableSystemCAs`, `traceHTTP` — none of which pi has. pi has
`defaultProjectTrust`, terminal image settings, codemode, per-model thinking
levels, and identity settings — none of which pi-go has.

**Two specific gaps in pi-go worth closing:**

1. **No project trust setting.** pi's `defaultProjectTrust` is the one control
   that makes a hostile repo's `.pi/` files safe. pi-go has no equivalent, which
   matters more given pi-go also has `internal/plugin` installing from git.
2. **No retry config block.** pi-go's retry constants are compiled in; pi exposes
   `retry.{maxRetries, baseDelayMs, maxAgentDelayMs, provider.*}`.

**pi-go's `role` concept is the better abstraction** for the problem pi solves
with `modelThinkingLevels` + `compaction.modelOverrides`. A role binds a model to
a task slot with a budget; pi's approach requires separate per-model and
per-purpose maps. Adopting roles for compaction summaries (`memory.CompressionRole`
already does this at `config.go:44`) would cover more cases with less config.

## Review summary — providers, catalog, auth, config

**pi-go leads on**: proactive rate limiting with account-correct scoping, the
`ServerDelay` retry refinement, daily token budget enforcement, reasoning-token
cost hygiene (partially), device-code-first OAuth (container-friendly), the
pointer-field unset/disabled distinction, `json:"-"` on derived config, and the
role abstraction.

**pi leads on**: provider breadth (42 vs 10), per-model wire-API dispatch, native
Bedrock/Vertex, image and classifier model catalogs, cache TTL with 1h-write
accounting, 9 OAuth flows across 3 shared mechanisms, credential refresh with
double-checked rotation, constrained/strict schema sanitization, virtual models
for routing, and cryptographic catalog drift detection.

**pi-go's single biggest structural limitation here** is that `NewLLM` dispatches
on provider name. Every addition is a code change; every provider that is not
OpenAI-compatible is a genuinely new wire implementation. Moving to a per-model
`api` table converts the 30 OpenAI-compatible providers into data and leaves
Bedrock/Vertex as the only real code work. That change should precede adding
any new provider.

**pi-go's correctness bugs to fix** (small, concrete):

- No 1h cache-write accounting → wrong cost on Anthropic's 1h tier
- Thought signatures not dropped on model switch → malformed requests
- `make fetch-models` can silently produce an empty catalog → add `--strict`