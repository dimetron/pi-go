# Models API — cross-provider model listing

> Status: **research + plan, not implemented**
> Researched: 2026-11-06
> Upstream references: `tmp/agentgateway` @ `ae6db441` (v1.7.0-98, 2026-11-05),
> `tmp/openai-go` @ `5d4ac524` (v3.96.0), `tmp/anthropic-sdk-go` @ `b98ff33` (v2.7.0)

## Why this exists

`pi model list` is the single entry point for "what can I run", and it feeds three
things that are not cosmetic: model validation (`provider.ValidateModel`), the
context window that drives auto-compaction (`internal/ctxwindow`), and cost
estimation (`provider.CostFor`). Every one of those reads numbers pi-go keeps in
`internal/provider/modeldata/`, not numbers the provider reports.

Those numbers have drifted badly, and the listing code cannot see the fields that
would let it stop drifting. Measured against live APIs on 2026-11-06:

| Defect | Impact | Evidence |
|---|---|---|
| `agentgateway` overwrites the live `context_window` with a stale local table | 46 models over-estimate (overflow), 62 blanked (compaction silently off), 24 under-estimate | `list_models.go:200` |
| `claude-opus-4-5` / `claude-sonnet-4-5` reported as 1M but the API serves 200K | 5× over-estimate → request overflows the real window | `context-windows.json:10,16` |
| `claude-haiku-5-5` and `gpt-6.1-sol` have no context-window entry at all | `windowSize=0` → `Decide` returns `CompactionNone`, auto-compaction never fires | `compaction.go:124` |
| `gemini` reads only page 1 of a paginated response | 11 of 61 live models invisible; all 11 would survive pi-go's filter | `list_models.go:265` |
| Provider-supplied metadata is discarded everywhere | `display_name`, `max_input_tokens`, `max_tokens`, capabilities, lifecycle, `shutdown_date` all dropped | `list_models.go:226-227` |
| Mistral `reasoning` capability dropped, 13 other fields dropped | reasoning-capable models look non-reasoning | `list_models.go:113` |
| `-o yaml` silently renders the table | flag accepted, format ignored | `model.go:134` |

The common root cause: **`ModelInfo` is a 5-field struct built for a table**
(`list_models.go:16-22`), and each provider's richer response is flattened into it
by hand, field by field. The `agentgateway` case is the sharpest illustration — the
gateway already returns `context_window`, `context_length`, `max_input_tokens`,
`max_tokens`, `architecture`, `capabilities` and `supported_parameters` (241 of 833
entries carry values), and `listAgentGatewayModels` throws all of it away to
substitute a local lookup that is wrong for 132 of those 241.

## Documents

| File | Contents |
|---|---|
| [research/providers.md](research/providers.md) | What each provider's models endpoint actually returns, verified live |
| [research/upstream.md](research/upstream.md) | What changed in the three upstream repos, and what it means for pi-go |
| [findings.md](findings.md) | Every defect, with the command that proves it |
| [requirements.md](requirements.md) | What the new behaviour must satisfy |
| [design.md](design.md) | Type changes, per-provider adapters, precedence rules |
| [plan.md](plan.md) | Ordered, verifiable implementation slices |

## Guiding constraint

**Provider-reported data beats local tables, and a local table that disagrees with a
live response is a bug, not a fallback.** Today the ordering is inverted for
`agentgateway`, which is why a working gateway makes the listing worse. The design
inverts it and keeps the local table for the two cases where it is genuinely the
only source: OpenAI (which reports no window at all) and offline/validation paths.

## Out of scope

- Adding new providers.
- The `/v1/models/{id}` single-model endpoint (useful, but nothing needs it yet).
- Streaming or incremental listing.
- Changing the agentgateway *config* under `hack/agentgateway/` — except where
  `update-prices.sh`-adjacent data is affected, noted in the plan.
