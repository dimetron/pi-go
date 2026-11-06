# Plan

Ordered slices. Each one ends in a state that builds and whose tests pass, so a slice
can be reverted alone. Requirements are referenced as `R<n>` from `requirements.md`,
findings as `F<n>` from `findings.md`.

Do **not** start with the type change. Slice 1 is the highest-severity defect and is
fixable in about ten lines; doing it first means the worst wrong answer stops being
produced while the rest of the work proceeds.

---

## Slice 1 — stop overwriting the gateway's own context window (F1, R1)

The 46 over-estimates and 62 blanked windows. Smallest possible change.

1. Extend the `listBearerModels` decode struct with `context_window`,
   `max_input_tokens`, `max_tokens`, `name` — or give agentgateway its own decode
   (preferred; see slice 4).
2. Delete the backfill loop in `listAgentGatewayModels` (`list_models.go:194-203`).
3. Add `windowFor` (design.md) and use it.

**Verify**

```bash
# with the gateway running
pi model list agentgateway -o json \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(len(d["models"]))'
# compare against the live endpoint for every model that reports a window — the
# regression test below is the durable version of this check
```

**Tests**

- `TestListAgentGatewayModelsKeepsServedContextWindow` — stub server returns
  `{"data":[{"id":"x","context_window":400000,"max_input_tokens":272000}]}`; assert
  `ContextWindow == 272000` and that no local lookup ran. Reintroducing the backfill
  must make it fail.
- `TestListAgentGatewayModelsNoWindowStaysZero` — stub returns an entry with no
  window; assert `ContextWindow == 0`, not a table value. This is the "virtual model"
  case: `pi-fast` must not be assigned 1000000.
- `TestWindowForPrecedence` — table of (maxInput, contextWindow, table) → expected,
  covering all three branches.

**Done when** `grep -n ContextWindowSizeFor internal/provider/list_models.go` returns
nothing.

> **Implemented 2026-11-06.** Listing now decodes the served fields and the backfill is
> deleted. Measured against a live v1.7.0-alpha.1: `agree=241 over=0 under=0 blank=0`
> (was `agree=109 over=46 under=24 blank=62`). Two guards were confirmed to fail when
> the backfill is reintroduced, then the code restored.

---

## Slice 1b — make the compaction path read the served window too (F2a)

Slice 1 fixes the listing. It does **not** fix auto-compaction, because
`ctxwindow.Resolve` never sees the listing — it calls `ContextWindowSizeFor`, which
reads the embedded table (`findings.md` F2a). Without this slice, a session on
`agentgateway/gpt-5.6-luna` still budgets against 272000 instead of 922000.

Follow the shape Ollama and OpenRouter already use: a live lookup in `Resolve`, keyed
on `Info` (which has Provider, Model, BaseURL — but **no API key**).

Options, cheapest first:

1. **A live query helper called from `Resolve` — recommended.** Mirror
   `OpenRouterContextWindowSize` exactly (`openrouter.go:215-280`): a
   `AgentGatewayContextWindowSize(ctx, apiKey, baseURL, model)` that fetches
   `/v1/models`, caches `id → max_input_tokens` per base URL for an hour, and returns
   `0` on any failure so the caller falls back.

   Call it from `Resolve` beside the existing Ollama and OpenRouter branches, reading
   the key with `config.APIKeys()["agentgateway"]` — `ctxwindow` already imports
   `config` (it takes `cfg config.Config`), so this adds no new dependency edge and
   needs no change to `provider.Info`. The environment read is the same one the CLI
   already does.

2. **Thread the key into `provider.Info`.** Add `APIKey string`. Cleanest
   conceptually, but there are 14 non-test `provider.Info{}` construction sites across
   `pimodels`, `internal/cli` and `internal/acp` — more surface than a 0-window bug
   warrants.

3. **Fix F3 in the table and stop there.** Correct `272000 → 922000` for the
   `gpt-5.4`–`gpt-5.6` family and add the missing models. Keeps one source of truth
   but leaves the structural drift: the next gateway model is wrong again until
   someone edits the table.

**Recommendation: option 1.** It is the pattern already proven twice in this codebase,
it needs no struct change, and unlike a cache populated only by the listing command it
works on a cold process — which matters because the first turn of the first session
after a restart is where a too-large prompt does its damage.

Option 3 remains worth doing regardless (slice 6), as the offline answer.

**Verify**

```bash
# the number a session would budget against, before and after
go test ./internal/ctxwindow/ -run TestResolveAgentGatewayUsesServedWindow -v
```

**Tests**

- `TestResolveAgentGatewayUsesServedWindow` — stub gateway reporting
  `max_input_tokens: 922000`; assert `Resolve` returns 922000, not the table's 272000.
- `TestResolveFallsBackToTableWhenGatewayUnreachable` — closed server; assert the
  table value, so an offline machine still compacts.

**Done when** `ctxwindow.Resolve("agentgateway", "gpt-5.6-luna")` returns 922000
against a live gateway.

---

## Slice 2 — make a missing window visible (F2, R2)

Slice 1 fixes the values that exist. This makes the ones that do not exist obvious.

1. `-o` validation (R7) — reject anything but `table`/`json`, before the provider loop.
2. `printProviderModels`: print `?` for `ContextWindow == 0`, and after the table emit
   `N models have no known context window; auto-compaction is disabled for them.`

**Verify**

```bash
pi model list anthropic -o yaml   # exits non-zero, names valid formats
pi model list anthropic           # '?' appears for any windowless model
```

**Tests**

- `TestModelListRejectsUnknownOutputFormat` — table over `yaml`, `table`, `json`, ``,
  `potato`; assert error only for the unknown ones.
- `TestPrintProviderModelsMarksUnknownContext` — golden output with a `0`-window row.
- `TestPrintProviderModelsWarnsAboutUnknownWindows` — the summary line appears exactly
  when at least one row has no window.

**Done when** a windowless model is distinguishable from a 0-token model in the table
and the operator is told compaction is off for it.

---

## Slice 3 — paginate Gemini (F4, R4, R10)

Independent of everything above; the endpoint is a different code path.

1. Loop on `nextPageToken`, `pageSize=1000`, capped at ~10 pages (R10).
2. Decode `inputTokenLimit` → `ContextWindow`/`MaxInputTokens`,
   `outputTokenLimit` → `MaxTokens`, `displayName` → `DisplayName`.
3. Route the request through `fetchJSON` for the uniform error shape (R8), or keep the
   inline path but give it the same error format — Gemini needs query-param auth, which
   `fetchJSON` does not support, so extending `fetchJSON` to take an auth mode is the
   cleaner option.

**Verify**

```bash
pi model list gemini -o json | jq '.models | length'   # 61, was 46
```

**Tests**

- `TestListGeminiModelsFollowsNextPageToken` — two-page stub, assert both pages'
  models present.
- `TestListGeminiModelsStopsAtPageCap` — stub that always returns a token; assert the
  call returns after the cap with the models collected so far, no error.
- `TestListGeminiModelsDecodesTokenLimits` — `inputTokenLimit`/`outputTokenLimit`
  land in `ContextWindow`/`MaxTokens`.

**Done when** the live count is 61 and a cursor loop cannot hang.

---

## Slice 4 — extend `ModelInfo` and fill it per provider (F3, F6, R5, R6)

The widest change. Split by provider so each commit is reviewable.

1. Add the fields from design.md. All `omitempty`.
2. Anthropic: `display_name`, `max_input_tokens`, `max_tokens`, `lifecycle`,
   `retires_at`, capability tree; stop using `type` as `OwnedBy`.
3. Mistral: add `reasoning`; add `name`, `deprecation`.
4. OpenAI: `shutdown_date` → `RetiresAt` + `Lifecycle: "deprecated"`.
5. OpenRouter: decode `context_length` in the listing.
6. agentgateway: `architecture`, `capabilities`, `supported_parameters` → structured
   flags; `name` → `DisplayName`.

**Verify**

```bash
make test-unit                                              # snapshots still parse (R6)
pi model list anthropic -o json | jq '.models[0]'            # new fields present
pi model list mistral -o json | jq '[.models[].capabilities[]] | unique'  # "reasoning"
```

**Tests**

- Per-provider golden test: marshal the provider's **real** response shape into a stub,
  unmarshal into `ModelInfo`, assert every field survives. Do not hand-write the
  fixture map — a fixture written by hand encodes the contract you assumed, which is
  the thing under test.
- `TestModelInfoJSONRoundTrips` — marshal → unmarshal → marshal is stable, and a
  pre-change snapshot file still parses (R6).

**Done when** `make fetch-models` produces a sensible diff and the checked-in
snapshots still load unchanged.

---

## Slice 5 — Anthropic pagination and lifecycle (F5, R4)

1. `?limit=1000`, follow `has_more` by `after_id=last_id`, capped.
2. Send `lifecycle%5B%5D=active` and `lifecycle%5B%5D=deprecated` explicitly, so the
   result does not depend on a server default.
3. Do not request `retired` — pi-go should not offer models that no longer answer.

**Verify**

```bash
curl -s "https://api.anthropic.com/v1/models?limit=3" -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" | jq '{n:(.data|length), has_more}'   # {"n":3,"has_more":true}
pi model list anthropic -o json | jq '.models | length'                     # 14
```

The bracket encoding is load-bearing — `?lifecycle=deprecated` returns
`the parameter name is 'lifecycle[]'`.

**Tests**

- `TestListAnthropicModelsFollowsHasMore` — two-page stub via `last_id`.
- `TestListAnthropicModelsSendsBracketLifecycle` — assert the query string contains
  `lifecycle%5B%5D=` and not `lifecycle=`.
- `TestListAnthropicModelsStopsAtPageCap`.

---

## Slice 6 — correct the context-window table (F3, F8)

Now that the provider is authoritative, the table only matters for OpenAI, xAI and the
validation path — but it is wrong today and that still matters for those.

1. Fix the Claude 4.5 entries: `claude-opus-4-5` and `claude-sonnet-4-5` are 200000,
   not 1000000. Add exact entries for the dated spellings and for `claude-sonnet-4-6`
   (1000000) so `longestPrefixSize` stops resolving 4-6 to the 4.x 200K prefix.
2. Add `claude-haiku-5-5` (1000000), `gpt-6.1-sol`, `gpt-daybreak-*`.
3. Correct the `gpt-5.4`–`gpt-5.6` family from the tier boundary (272000) to the real
   window (1050000), and record the input ceiling where known.
4. Re-check every `agentgateway` entry against a live gateway and either correct or
   delete it. `pi-fast: 1000000` should go — the gateway says `virtual` with no limits.

**Verify** — a script, not a manual pass:

```bash
# every table entry vs the live source, printing disagreements
make check-context-windows    # new target; see below
```

Add `make check-context-windows`: a Go program under `hack/` that compares
`context-windows.json` against (a) `models.dev` `limit.context` and (b) a live
agentgateway `/v1/models`, and exits non-zero on disagreement. This is the check that
would have caught F1 and F3 before a user did.

**Tests**

- `TestContextWindowsMatchModelsDev` — offline variant. **Requires a new field first:**
  `pricingSnapshot` does not currently capture `limit`
  (`internal/provider/pricing.go:246-290` decodes `release_date`, `modalities`,
  `cost`, `deprecated`, `text_output` — not `limit`). Add
  `Limit struct{ Context, Input, Output int64 }` to that decode and to
  `PricingModel`, then compare `context-windows.json` against it, skipping entries the
  snapshot lacks. Fails on the current tree; that is the point.

  Note the snapshot is a *trimmed* copy of models.dev, regenerated by
  `make fetch-modelsdev-pricing`. Adding `limit` means regenerating it, which is a
  larger diff than slices 1–5 — check whether that is acceptable before writing this
  test, and fall back to the live-only `make check-context-windows` target if not.
- `TestClaude45GenerationIs200K` — the specific regression.
- `TestDatedSpellingResolvesToOwnEntry` — `claude-sonnet-4-6` resolves to 1000000, not
  to the `claude-sonnet-4` prefix.

**Done when** `make check-context-windows` passes against live sources.

---

## Slice 7 — refresh the embedded snapshots

Last, because it depends on all the decoding above.

```bash
make fetch-models
make fetch-modelsdev-pricing
git diff --stat internal/provider/modeldata/
```

Verify the diff contains only expected changes: new fields appearing, no field
disappearing, no model count dropping for a provider whose live count did not drop.

**Do not** run `make check-cve` for this — it opens with `go mod tidy -v` and rewrites
tracked files (see AGENTS.md).

---

## Ordering rationale

| Slice | Severity | Why here |
|---|---|---|
| 1 | P0 | Stops producing a wrong answer; ~10 lines; independent |
| 2 | P0 | Makes the remaining `0` cases visible without changing data |
| 3 | P1 | Self-contained endpoint fix, no dependency on the type change |
| 4 | P1 | Widest change; benefits from 1–3 having settled the shape |
| 5 | P1 | Latent today; cheap once 4 exists |
| 6 | P0 | Needs the live-comparison harness to be trustworthy |
| 7 | — | Artifact of 1–6 |

Slices 1, 2, 3 are independent and can be three parallel branches. Slice 4 touches
every adapter, so it should land after them to avoid conflicting edits in
`list_models.go`.

---

## Verification gates

Every slice, before its commit:

```bash
make build
make test-unit
make vet
make lint
```

Before the PR:

```bash
make test-all
make vulncheck        # AGENTS.md: after review, before merge
```

Two failure classes pass on macOS and fail only on Windows CI (AGENTS.md): an
unclosed directory handle breaking `t.TempDir()` cleanup, and `rg` being absent from
the Windows runner. Neither applies to this change — no temp dirs or `rg` lookups are
added — but the new tests must still be checked on Windows CI before merge.

---

## Open questions

1. **Should `ContextWindow` be renamed to `InputTokenLimit`?** The name is now
   imprecise, since it holds the input ceiling. Renaming touches the JSON key and every
   snapshot. Recommendation: keep the key, document the meaning. A rename is a separate
   change with its own migration.
2. **Should `make check-context-windows` be a CI gate or a local target?** It needs a
   live gateway and API keys, so CI cannot run the full version. Recommendation: local
   target only, plus the offline models.dev variant as the CI gate.
3. **Should the `agentgateway` block in `context-windows.json` be deleted entirely?**
   After slice 1 it is unused for listing, but `ContextWindowSizeFor` still reaches it
   for validation. Recommendation: keep, corrected, and let a later change decide once
   nothing reads it.
