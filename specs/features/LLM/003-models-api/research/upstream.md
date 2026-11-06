# Upstream changes and what they mean for pi-go

Three checkouts under `tmp/` were read on 2026-11-06. This document records what
changed, and separates *shipped* changes from *unreleased* ones — a distinction that
matters because a recommendation built on unreleased code is a recommendation pi-go
cannot act on yet.

| Repo | HEAD | Version | Released? |
|---|---|---|---|
| `tmp/agentgateway` | `ae6db441` | `v1.7.0-98-gae6db441` | partially — see below |
| `tmp/openai-go` | `5d4ac524` | v3.96.0 | yes |
| `tmp/anthropic-sdk-go` | `b98ff33` | v2.7.0 | yes |

---

## agentgateway

### What pi-go talks to

The running container is `ghcr.io/agentgateway/agentgateway:v1.7.0-alpha.1`
(confirmed via `docker ps`; `agentgateway --version` reports `0.11.1`, an older
binary on PATH that is not the one serving port 4000).

### `catalog: render model information into /v1/models` — `940ce7db` (2026-10-14, #3965)

**Shipped in v1.7.0.** This is the change pi-go has not caught up with: the gateway's
`/v1/models` went from `{id, object, created, owned_by}` to the catalog-backed entry
shown in `providers.md`, gaining `name`, `context_window`, `context_length`,
`max_input_tokens`, `max_tokens`, `architecture`, `capabilities` and
`supported_parameters` for any model the embedded catalog knows.

Verify a running gateway has it:

```bash
curl -s http://localhost:4000/v1/models -H "Authorization: Bearer $KEY" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); \
      print(sum(1 for m in d["data"] if "context_window" in m), "of", len(d["data"]))'
# 241 of 833
```

pi-go's `listAgentGatewayModels` (`list_models.go:194-203`) predates this and
**overwrites** the field with `ContextWindowSizeFor("agentgateway", id)`. See
`findings.md` F1.

### `llm/discovery: advertise preferred invocation protocols` — `134897e1` (2026-11-03, #4272)

**Not released.** `git tag --contains 134897e1` is empty; the change sits 40+ commits
past `v1.7.0` on `main`.

It adds a `preferred_endpoints` array to each entry, telling a client which client-side
paths the gateway will serve without conversion:

```json
{"id":"gpt-5.5","preferred_endpoints":["/chat/completions","/responses"]}
```

Populated by `ModelDiscovery::preferred_endpoints`
(`crates/agentgateway/src/llm/discovery.rs:77-140`), which derives endpoints from the
provider's native chat formats plus catalog-declared `endpoints`, and **intersects**
across a virtual model's targets (`model_list.rs:121-127`) so a failover model
advertises only what every target serves.

**Consequence:** this is the field that would let pi-go stop hard-coding
`GatewayRoutesToGemini` / `routesToOllama` as name-prefix heuristics
(`agentgateway.go:137-168`). It is genuinely useful — and unavailable today. The
finding is recorded as a *future* item, gated on an upstream release, not as work.

Do not build on it yet. A test asserting its presence would fail against every
released gateway.

### `feat(llm): describe failover virtual models in the model list` — `ae4ef22f` (#4313)

At HEAD but **the object is not present in this checkout** (`git cat-file -e` fails —
the clone is missing some recent objects). Read from the commit subject and the
`ModelInfo::combine` implementation, which is present and explains the behaviour:
virtual models are described by the *intersection* of their targets'
capabilities/limits, and `owned_by` is set to `"virtual"`.

Live confirmation:

```json
{"id":"pi-fast","object":"model","created":1793925527,"owned_by":"virtual"}
```

**Consequence:** pi-go must treat `owned_by == "virtual"` plus *absent* limit fields as
an intentional answer, not as missing data to be filled in locally.

### `controller: flip semantics on custom models` — `50cea948` (#4301), `AGW_ENABLE_AGENTGATEWAY_MODELS is now always enabled` — `32a39cb0` (#4280)

Kubernetes-controller changes. pi-go talks to the standalone gateway over HTTP and does
not use the CRDs, so these are informational only. The relevant part is that custom
model sources are now always enabled, which means a gateway may serve models absent
from the embedded catalog — another reason a `0`/absent window must stay `0` rather
than being guessed.

### Model catalog refresh cadence

`catalog/model-catalog.json` is regenerated frequently (7 commits in the 3 weeks
before HEAD: `6f8e1d14`, `2148de9b`, `f8be8ab4`, `f3b9e894`, `a95d3c37`, `53f108cc`,
`bf1032b9`). Each carries a `metadata.generatedAt`. The current file is
`2026-11-05T08:25:31Z` with 22 providers.

**This is the single most important argument for F1's fix.** pi-go maintains a
hand-curated 29-entry `agentgateway` block in `context-windows.json` that is compared
against a catalog upstream refreshes weekly. It cannot keep up, and 132 of 241 live
values disagree. Stop copying the data; read it.

---

## openai-go (v3.96.0)

### `Model` has not changed shape in ~3 months

`model.go:96-117`:

```go
type Model struct {
	ID           string
	Created      int64
	Object       constant.Model
	OwnedBy      string
	ShutdownDate time.Time   // added f8b63fd6, 2026-08-14
}
```

`shutdown_date` is the only addition; `model.go` itself last changed at `6e728809`
(data-residency endpoints). All recent "model" churn in this repo is *constant/enum*
additions — `a47bab2d` adds `gpt-6-rosalind`, `b5e1cc96` adds `gpt-6.1-terra`, etc.

### The list is not paginated

`ModelService.List` (`model.go:57-73`) issues `GET /models` with **no query params** —
there is no `ModelListParams` type in the repo at all. The return type is
`*pagination.Page[Model]` whose `GetNextPage` is hard-coded to `return nil, nil`
(`packages/pagination/pagination.go:39-41`).

**Consequence:** pi-go's `listBearerModels` is correct for OpenAI and xAI. The
pagination finding (F4) applies to **Gemini and Anthropic only**.

### Newer model IDs exist that pi-go's table lacks

`shared/shared.go` lists `gpt-6.1-sol`, `gpt-6.1-terra`, `gpt-6-astra`,
`gpt-6-rosalind`, `gpt-5.6-cyber`, `gpt-daybreak-*`. Live `/v1/models` returned
`gpt-6.1-sol` and `gpt-daybreak-{blue,red}-latest` — all three have **no**
`context-windows.json` entry, so all three get `windowSize = 0` and disable
auto-compaction. This is F2, and it is not a one-off: it is the steady state of a
hand-maintained table tracking an enum that upstream extends every few weeks.

---

## anthropic-sdk-go (v2.7.0)

The richest model object of the three, and the one with the most recent movement.

### `ModelInfo` — `model.go:240-303`

```go
type ModelInfo struct {
	ID             string
	Capabilities   ModelCapabilities
	CreatedAt      time.Time
	DeprecatedAt   time.Time
	DisplayName    string
	Lifecycle      ModelInfoLifecycle   // active | deprecated | retired
	Line           ModelLine            // haiku | sonnet | opus | fable | mythos
	MaxInputTokens int64
	MaxTokens      int64
	RetiresAt      time.Time
	Type           constant.Model
}
```

`MaxInputTokens` / `MaxTokens` are served values — the authority pi-go should be using
instead of `context-windows.json`.

### Three recent API additions pi-go discards

| Commit | Date | Added |
|---|---|---|
| `2b3ea06` | 2026-10-07 | `DeprecatedAt`, `Lifecycle`, `RetiresAt`, `Lifecycle []string` list filter |
| `24b1d56` | 2026-10-01 | `Line` |
| `4e8df93` | — | `between_tools` thinking type |

`65df48f` added API version `2026-10-21`, but the `lifecycle[]` filter works on
`2023-06-01` (verified live), so pi-go does not need to bump
`list_models.go:331`.

### `ModelListParams` — `model.go:437-464`

```go
AfterID     param.Opt[string]
BeforeID    param.Opt[string]
Limit       param.Opt[int64]   // default 20, range 1..1000
WorkspaceID param.Opt[string]  // anthropic-workspace-id header
Lifecycle   []string           // array format: brackets
```

Note the array format is `brackets`, not `repeat` — which is why
`?lifecycle=deprecated` is rejected and `?lifecycle%5B%5D=deprecated` works. A
hand-rolled query string must use the bracket form.

### Beta adds `AllowedFallbackModels`

`BetaModelInfo` (`betamodel.go:290`) carries `allowed_fallback_models` that stable does
not. Not relevant to listing; recorded for completeness.

---

## What this means, in one paragraph

Six of pi-go's eight listed providers already tell it the context window, and
agentgateway tells it the window, output limit, modalities, capability flags and
display name for 241 models. pi-go reads almost none of it and substitutes a
29-entry hand-curated table that is wrong for 132 of those 241 values. Meanwhile
upstream OpenAI is *adding* model IDs faster than that table is updated, so the
`windowSize = 0` case (compaction disabled) is structural, not incidental. The fix is
to make `ModelInfo` carry the fields the providers send and to define precedence —
provider value first, local table only where the provider is silent. Everything else
in this spec follows from that.
