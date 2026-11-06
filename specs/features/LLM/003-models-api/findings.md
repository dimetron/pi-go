# Findings

Each finding is a claim with the command that proves it. Nothing here is inferred from
reading code alone; every number came from a run on 2026-11-06.

Severity key: **P0** silently produces a wrong answer, **P1** wrong output a user may
notice, **P2** cosmetic or latent.

---

## F1 — agentgateway: pi-go overwrites live context windows with a stale local table

**Severity: P0**
`internal/provider/list_models.go:194-203`

```go
func listAgentGatewayModels(...) ([]ModelInfo, error) {
	models, err := listBearerModels(ctx, opts, "agentgateway", "agentgateway")
	if err != nil { return nil, err }
	for i := range models {
		models[i].ContextWindow = ContextWindowSizeFor("agentgateway", models[i].ID)
	}
	return models, nil
}
```

`listBearerModels` decodes only `id` and `owned_by` (`:226-227`), so the loop above is
described in its own doc comment as "enrichment". Since gateway v1.7.0 the response
already contains `context_window`, and this loop discards it.

### Proof

```bash
KEY=$(rg -o 'agw_sk_\w+' ~/agentgateway/config.yaml | head -1)
curl -s http://localhost:4000/v1/models -H "Authorization: Bearer $KEY" > /tmp/gw.json
python3 -c "
import json; d=json.load(open('/tmp/gw.json'))['data']
print(sum(1 for m in d if m.get('context_window')), 'of', len(d), 'carry context_window')"
# 241 of 833
```

Then compare each live value against what pi-go computes
(`ContextWindowSizeFor`), for the 241 entries that carry one:

```
live-values=241  agree=109  over=46  under=24  pi-go-blanked=62
```

Breakdown by provider namespace:

| namespace | agree | over | under | blank |
|---|---|---|---|---|
| anthropic | 11 | 4 | 1 | 1 |
| gemini | 17 | 15 | 0 | 4 |
| mistral | 3 | 0 | 6 | 28 |
| openai | 18 | 4 | 8 | 16 |
| other | 53 | 23 | 9 | 12 |
| xai | 7 | 0 | 0 | 1 |

### Why "over" is the dangerous direction

`ContextWindowSizeFor` feeds `internal/ctxwindow.Resolve`, which feeds
`AutoCompactConfig.Decide` (`internal/session/compaction.go:123-140`):

```go
func (c AutoCompactConfig) Decide(bodyTokens, windowSize int64) CompactionAction {
	if !c.Enabled || windowSize <= 0 || bodyTokens <= 0 {
		return CompactionNone
	}
```

- **over** → compaction fires too late → the request overflows the real window mid-turn.
- **blank (0)** → the `windowSize <= 0` guard returns `CompactionNone` unconditionally →
  auto-compaction **never runs** for that model.
- **under** → compaction fires early → wasted capacity, no correctness loss.

46 models overflow and 62 never compact. The over-estimates, worst ratio first:

| model | live | pi-go | factor |
|---|---|---|---|
| `gemini-embedding-001` | 2048 | 1048576 | 512.0× |
| `gemini-2.5-flash-preview-tts` | 8192 | 1048576 | 128.0× |
| `gemini-3.1-flash-tts-preview` | 8192 | 1048576 | 128.0× |
| `gemini-2.5-pro-preview-tts` | 8192 | 1048576 | 128.0× |
| `gemini-3.5-live-translate-preview` | 16384 | 1048576 | 64.0× |
| `gemini/gemini-2.5-flash-image` | 32768 | 1048576 | 32.0× |
| `gemini/gemini-3-pro-image` | 65536 | 1048576 | 16.0× |
| `anthropic/claude-opus-4-5-20251101` | 200000 | 1000000 | 5.0× |
| `anthropic/claude-sonnet-4-5-20250929` | 200000 | 1000000 | 5.0× |
| `openai/gpt-5.1` | 400000 | 1050000 | 2.6× |
| `openai/gpt-5.2` | 400000 | 1050000 | 2.6× |
| `openai/gpt-5.3-codex` | 400000 | 1050000 | 2.6× |

The extremes are the tell: an 8K TTS model and a 2K embedding model are both reported
as 1M, because the local table has one entry per *prefix* (`gemini-2.5-flash`,
`gemini-embedding`) and no way to express that the TTS/image/embedding variants are a
different product with a different limit. For chat models the damage is 2.6×–5×; for
audio and embedding models it is 16×–512×.

models.dev agrees with the API on every row above (`gemini-2.5-flash-image` →
`limit.context: 32768`), so this is not a case of two sources disagreeing about an
ambiguous number — the table is simply wrong.

### Root cause of the wrong table values

The `gpt-5.4`–`gpt-5.6` entries in `context-windows.json` hold **the pricing-tier
boundary, not the context window**:

| model | pi-go | models.dev `limit.context` | models.dev `cost.tiers[0].tier.size` |
|---|---|---|---|
| gpt-5.6-luna | 272000 | 1050000 | **272000** |
| gpt-5.6-sol | 272000 | 1050000 | **272000** |
| gpt-5.6-terra | 272000 | 1050000 | **272000** |
| gpt-5.5 | 272000 | 1050000 | **272000** |
| gpt-5.4 | 272000 | 1050000 | **272000** |

272000 appears in `modelsdev-pricing.json` only as `context_over` — the threshold above
which the expensive rate applies. Someone read the tier boundary and recorded it as the
window. That is a 3.86× under-report in the *table*, which the live gateway then
overrides in the other direction. Both are wrong; they are wrong differently.

### Fix

Trust the response. Use the live `context_window`, fall back to
`max_input_tokens`, and only then to the local table. Never overwrite a live value.

---

## F2 — models with no context-window entry silently disable auto-compaction

**Severity: P0**
`internal/provider/modeldata/context-windows.json`, consumed at
`internal/ctxwindow/ctxwindow.go:27-45`

`claude-haiku-5-5` is the newest Anthropic Haiku, is served at 1M tokens, is in
`models-anthropic.json`, and has **no entry** in `context-windows.json`:

```bash
rg -n "haiku-5-5" internal/provider/modeldata/context-windows.json   # no output
rg -n "gpt-6.1-sol|gpt-daybreak" internal/provider/modeldata/context-windows.json  # no output
```

`longestPrefixSize` finds nothing, `ContextWindowSize("claude-haiku-5-5")` returns 0,
`Resolve` returns 0, and `Decide` short-circuits to `CompactionNone`.

Confirmed:

```go
ContextWindowSizeFor("anthropic", "claude-haiku-5-5")  // 0
```

Live API for the same model:

```bash
curl -s "https://api.anthropic.com/v1/models/claude-haiku-5-5" \
  -H "x-api-key: $ANTHROPIC_API_KEY" -H "anthropic-version: 2023-06-01" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["max_input_tokens"])'
# 1000000
```

Same for `gpt-6.1-sol` and `gpt-daybreak-{blue,red}-latest`, which are live on OpenAI
and absent from the table.

**This is structural, not a missed entry.** The table is hand-maintained; the model
lists grow every few weeks (see `research/upstream.md`). Any new model lands in the
"compaction silently off" state until someone remembers to add it.

**The failure is invisible**: nothing warns, the table still parses, and sessions just
never compact until they hit a provider error.

---

## F2a — the listing and the compaction path are separate; fixing one does not fix the other

**Severity: P0 (scoping correction — found while implementing slice 1)**
`internal/ctxwindow/ctxwindow.go:27-45`

`ctxwindow.Resolve` does **not** read the model listing. It calls
`provider.ContextWindowSizeFor`, which reads the embedded table:

```go
func Resolve(ctx context.Context, cfg config.Config, info provider.Info, baseURL string) int64 {
	ctxWindowSize := provider.ContextWindowSizeFor(info.Provider, info.Model)
	if info.Ollama { ...daemon query... }
	if info.Provider == "openrouter" { ...live query... }
	...
}
```

So there are two independent consumers of the same wrong numbers:

| consumer | source | fixed by slice 1? |
|---|---|---|
| `pi model list` (display, `-o json`) | the model listing | **yes** |
| `ctxwindow.Resolve` → `Decide` (auto-compaction) | the embedded table | **no** |

Slice 1 was written as if deleting the `listAgentGatewayModels` backfill fixed F1.
It fixes the *listing* half only. Verified after implementing slice 1: the listing
went from `agree=109 over=46 under=24 blank=62` to `agree=241 over=0 under=0 blank=0`,
but a session on `agentgateway/gpt-5.6-luna` still resolves
`ContextWindowSizeFor → 272000`, so compaction still fires 3.86× too late.

**Consequence:** F1 splits into two fixes, and the compaction half needs the same
treatment Ollama and OpenRouter already get — a live query in `Resolve`, because
`Info` carries `Provider`, `Model` and `BaseURL` but no API key
(`provider.go:191-205`). Options are recorded in `plan.md` slice 1b.

---

## F3 — Claude 4.5 models report 5× their real window

**Severity: P0**
`internal/provider/modeldata/context-windows.json:10,16`

```json
"claude-opus-4-5": 1000000,
"claude-sonnet-4-5": 1000000,
```

Live:

| model | pi-go | API `max_input_tokens` | verdict |
|---|---|---|---|
| `claude-opus-4-5-20251101` | 1000000 | **200000** | 5× over |
| `claude-sonnet-4-5-20250929` | 1000000 | **200000** | 5× over |
| `claude-haiku-4-5-20251001` | 200000 | 200000 | ok |
| `claude-sonnet-4-6` | 200000 | **1000000** | 5× **under** |
| `claude-opus-4-6` | 1000000 | 1000000 | ok |
| `claude-haiku-5-5` | **0** | 1000000 | F2 |

`claude-sonnet-4-6` is the mirror image: 200K reported against 1M served, because the
prefix `claude-sonnet-4` (→ 200000) wins over the exact key `claude-sonnet-4-6` being
absent. `longestPrefixSize` picks the longest *prefix* it has, and it has no entry for
4-6.

Requesting the 1M beta does not reconcile it:

```bash
curl -s ".../v1/models/claude-opus-4-5-20251101" \
  -H "anthropic-beta: context-1m-2025-08-07" ... | jq .max_input_tokens
# 200000
```

pi-go does not send that header in any case (`anthropic.go:20,52` — the only beta
header is on the OAuth path).

---

## F4 — Gemini reads only the first page

**Severity: P1**
`internal/provider/list_models.go:265-313`

`listGeminiModels` issues one `GET /v1beta/models` with no `pageSize` and no
`pageToken` loop, then decodes `models`. Gemini default-pages at 50.

```bash
python3 - <<'EOF'
import json,os,urllib.request
key=os.environ['GEMINI_API_KEY']; allm=[]; tok=None; pages=0
while True:
    u=f"https://generativelanguage.googleapis.com/v1beta/models?key={key}"+(f"&pageToken={tok}" if tok else "")
    x=json.load(urllib.request.urlopen(u,timeout=20)); pages+=1
    allm+=x.get('models',[]); tok=x.get('nextPageToken')
    print(f"page {pages}: {len(x.get('models',[]))} models, next={'yes' if tok else 'no'}")
    if not tok: break
print("TOTAL", len(allm))
EOF
# page 1: 50 models, next=yes
# page 2: 11 models, next=no
# TOTAL 61
```

pi-go returns 46 (misses 11 by pagination, filters 4 more). Confirmed end to end:

```bash
pi model list gemini -o json | jq '.models | length'   # 46
```

All 11 pagination-missing models would have survived
`enrichAndFilterModels` — they are not filtered, simply absent:

```
aqa, gemini-2.5-flash-native-audio-{latest,preview-09-2025,preview-12-2025},
gemini-3.1-flash-live-preview, gemini-3.5-live-translate-preview,
gemini-3.5-transcribe-live, gemini-3.8-live, gemini-3.8-live-extended-thinking,
gemini-robotics-er-2-streaming-preview, lyria-realtime-exp
```

Gemini also returns `inputTokenLimit` and `outputTokenLimit` per model, both discarded.

---

## F5 — Anthropic pagination is unhandled and will truncate

**Severity: P1 (latent today, guaranteed later)**
`internal/provider/list_models.go:241-262`

No `limit`, no `after_id` loop, `has_more` ignored (`ModelInfo` has no field for it).

Measured today: the account has 14 models, so `has_more` is `false` and nothing is
lost. The endpoint *is* paginated — `limit=3` returns 3 and `has_more: true`:

```bash
curl -s "https://api.anthropic.com/v1/models?limit=3" -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" | jq '{n: (.data|length), has_more}'
# {"n": 3, "has_more": true}
```

The SDK documents the default as 20 (`model.go:446`) but the live API returned all 14
when unpaginated, so the effective default here is larger. Either way the code depends
on an unspecified default and would truncate on a larger account or workspace.

Same code discards `display_name`, `max_input_tokens`, `max_tokens`, `lifecycle`,
`line`, `deprecated_at`, `retires_at` and the whole `capabilities` tree — and sets
`OwnedBy` to the record `type`, which is always the literal `"model"`. That is why
`pi model list anthropic` shows `owned_by: "model"` for every row.

---

## F6 — Provider metadata is discarded across the board

**Severity: P1**

`ModelInfo` (`list_models.go:16-22`) has five fields:

```go
type ModelInfo struct {
	ID            string   `json:"id"`
	OwnedBy       string   `json:"owned_by,omitempty"`
	ContextWindow int64    `json:"context_window,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	ReleaseDate   string   `json:"release_date,omitempty"`
}
```

What each provider sends that this cannot hold:

| provider | dropped |
|---|---|
| agentgateway | `name`, `max_input_tokens`, `max_tokens`, `architecture`, 4 capability flags, `supported_parameters` |
| anthropic | `display_name`, `max_input_tokens`, `max_tokens`, `lifecycle`, `line`, `retires_at`, full capability tree |
| openai | `shutdown_date` (48 of 98 models) |
| gemini | `displayName`, `inputTokenLimit`, `outputTokenLimit`, `supportedGenerationMethods` |
| mistral | `name`, `reasoning` capability, `deprecation`, `deprecation_replacement_model`, 13 other fields |

Specific consequence — Mistral reasoning:

```json
{"id":"mistral-medium-latest","capabilities":{"completion_chat":true,
 "function_calling":true,"reasoning":true,"vision":true}}
```

`listMistralModels:113-174` copies only `completion_chat, completion_fim,
function_calling, fine_tuning, vision, classification`. `reasoning: true` is dropped, so
a reasoning model looks identical to a non-reasoning one in the Notes column.

---

## F7 — `-o yaml` is accepted and ignored

**Severity: P2**
`internal/cli/model.go:69,134`

The flag declares `Output format: "json" emits one JSON document per provider`, and the
only branch is:

```go
if flagModelListOutput == "json" { ... }
```

Anything else falls through to `printProviderModels`, the human table.

```bash
pi model list agentgateway -o yaml | head -3
# agentgateway (833 models):
# | Model | Release | Price | Context | Notes |
# |---|---|---|---|---|
```

`-o yaml`, `-o table`, `-o potato` all render the table and exit 0. A caller parsing
for YAML gets a markdown table with an exit code claiming success.

---

## F8 — the agentgateway table duplicates data the gateway already serves

**Severity: P1 (maintainability; the cause of F1)**

`context-windows.json` carries 29 hand-curated `agentgateway` entries. Upstream
regenerates its catalog weekly (`research/upstream.md`). 132 of 241 comparable live
values disagree, and the table holds `pi-fast: 1000000` for a model the gateway
deliberately describes as `{"owned_by":"virtual"}` with no limits at all.

Every future gateway model needs a manual table edit to get a correct window, and F1
means the edit will then *override* a correct served value with it.

---

## Not defects (checked, correct)

Recorded so a future reader does not re-investigate:

- **`listBearerModels` not paginating** — correct. OpenAI's `List` takes no params and
  `GetNextPage` is a stub (`tmp/openai-go/model.go:57-73`,
  `packages/pagination/pagination.go:39-41`). F4/F5 are Gemini/Anthropic only.
- **`/v1/v1/models` guard** (`list_models.go:213-224`) — correct, and load-bearing for
  `XAI_BASE_URL` values that already end in `/v1`.
- **Ollama taking its window from the daemon** — correct, and the model F2 should
  follow for providers that report.
- **`lifecycle[]` bracket encoding** — pi-go does not send it yet; when it does it
  must use `lifecycle%5B%5D=`, not `lifecycle=`.
