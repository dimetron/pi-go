# Requirements

Numbered so `plan.md` can reference them. Each is stated as an observable outcome, not
an implementation.

---

## R1 — A provider-reported context window wins over any local table

When a provider's models response carries a window, that value is what pi-go uses.
No local lookup may overwrite it.

**Acceptance:** for the 241 entries in the live agentgateway listing that carry
`context_window`, `pi model list agentgateway -o json` reports that exact number.

**Why:** `findings.md` F1 — 46 over-estimates and 62 blanked windows, all of them
caused by overwriting a served value.

---

## R2 — An absent window stays absent, and absence is visible

A model whose window no source reports must report `0` — or better, omit the field —
and must **not** be silently assigned a plausible number.

`Decide` already treats `0` correctly (`compaction.go:124`). The defect is that
nothing tells the operator that compaction is disabled.

**Acceptance:** `pi model list <provider>` marks models with no known window in the
Context column (e.g. `—` or `?`) rather than printing a number or an empty cell, and
the count is summarised.

**Why:** F2 — `claude-haiku-5-5` and `gpt-6.1-sol` currently reach `windowSize = 0`
with no signal anywhere.

---

## R3 — Local tables are the floor, never the override

Where a provider does not report a value (OpenAI, xAI) the embedded table supplies it.
Where it does, the table is not consulted.

Precedence, highest first:

1. explicit user config (`cfg.ContextWindow`) — unchanged, already correct
2. the provider's models response
3. the runtime query (`/api/show` for Ollama, OpenRouter `/models`) — unchanged
4. the embedded `modeldata/` table
5. nothing (`0`)

**Acceptance:** each step is covered by a test that fails if a lower-precedence source
overrides a higher one. The agentgateway case (step 2 vs step 4) is currently inverted.

---

## R4 — Paginate every paginated endpoint to exhaustion

Gemini (`nextPageToken`) and Anthropic (`has_more` / `last_id`) must be followed until
the provider stops returning a cursor.

**Acceptance:** `ListModels(ctx, "gemini", …)` against an account with 61 models returns
61, not 50. A loop bound prevents runaway (see R10).

**Why:** F4 (11 models invisible), F5 (latent truncation).

---

## R5 — `ModelInfo` carries what providers actually send

The struct must be able to hold, at minimum:

- `context_window` — the window used for compaction
- `max_input_tokens` — when distinct from the window
- `max_tokens` / output limit
- display name
- capability set (as structured data, not a display-only string slice)
- lifecycle / deprecation signal, with the retirement date when known

Existing JSON keys must keep their meanings so `modeldata/models-*.json` and the XDG
cache keep parsing without migration.

**Acceptance:** a golden test per provider asserts each field that provider sends
survives into `ModelInfo`, built by marshalling the provider's *real* response shape.

**Why:** F6 — every provider's metadata is currently dropped.

---

## R6 — Additive-only JSON, so existing snapshots keep working

`make fetch-models` regenerates `modeldata/models-*.json` from `pi model list -o json`.
New fields must be `omitempty` and old files must still parse.

**Acceptance:** `TestEmbeddedCatalogPresentForEveryProvider` and the snapshot
round-trip tests pass without regenerating the files.

---

## R7 — `-o` rejects what it does not implement

`-o yaml`, `-o table`, and anything else must either be implemented or rejected with a
non-zero exit and a message naming the valid values.

**Acceptance:** `pi model list anthropic -o yaml` exits non-zero with a usage error.

**Why:** F7 — currently accepted, ignored, exit 0.

---

## R8 — Failures name the provider and the endpoint

Every list error must include the provider and the URL, and the CLI must not report
success when a provider failed.

**Acceptance:** a 500 from a stubbed endpoint produces an error containing both the
provider name and the path; the process exit code is non-zero.

**Current state:** mostly satisfied (`fetchJSON` wraps with provider and status), but
`listGeminiModels` duplicates the logic inline (`:265-313`) and should share it so the
guarantee is uniform.

---

## R9 — No new required network call

Model listing must keep working with only an API key and no catalog refresh. The
embedded table remains the offline answer.

**Acceptance:** with the network stubbed to fail, `provider.CatalogFor` and
`ContextWindowSizeFor` still return their embedded values.

---

## R10 — Bounded work

A paginating loop must have a hard cap on pages (and therefore requests), so a
misbehaving endpoint cannot hang a listing. The existing 30 s per-request timeout
(`defaultListTimeout`) stays.

**Acceptance:** a stub that always returns a next-page cursor terminates with the cap
and returns what it collected, without erroring the whole listing.

---

## R11 — Document the divergence

Where pi-go's number and a provider's number disagree, the listing should not silently
pick one. `research/` and `findings.md` record the current divergences; the code should
carry a comment pointing at the precedence rule so the next person does not re-add a
local override.

**Acceptance:** `listAgentGatewayModels` no longer exists, and
`ContextWindowSizeFor`'s doc comment states that it is a fallback for providers that do
not report a window.

---

## Non-requirements

- **No new providers.** Eight is enough for this change.
- **No `/v1/models/{id}` single-model fetch.** Nothing needs it; it is a second code
  path to keep correct.
- **No dependency on agentgateway `preferred_endpoints`.** It is unreleased upstream
  (`research/upstream.md`); building on it would fail against every shipped gateway.
- **No change to `hack/agentgateway/config.yaml`.** The gateway's own configuration is
  not pi-go's to fix; the fix is to stop copying its data.
