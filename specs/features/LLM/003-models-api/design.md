# Design

## Current state

```
                     ┌──────────────────────────────────────────┐
 provider APIs ─────▶│ internal/provider/list_models.go         │
                     │   switch provider → list<X>Models()      │
                     │   each flattens into ModelInfo{5 fields} │
                     └──────────────────┬───────────────────────┘
                                        │  drops everything else
                                        ▼
                     internal/cli/model.go
                       enrichAndFilterModels  (release date, filter)
                       printProviderModels    (table)  /  -o json
                                        │
                                        ▼
                     XDG cache + modeldata/models-<provider>.json
                     (via make fetch-models)

  separate, parallel path — never sees the API response:
                     internal/provider/modeldata/context-windows.json
                                        │
                     ContextWindowSizeFor ─────▶ ctxwindow.Resolve
                                                      │
                                                      ▼
                                          AutoCompactConfig.Decide
```

The two halves never meet. `listAgentGatewayModels` is the one place they are wired
together, and it wires them in the wrong direction (`list_models.go:200`).

`ModelInfo` (`list_models.go:16-22`) is the bottleneck: it has five fields, and every
provider's richer response is flattened into them by hand.

## Desired end state

```
 provider APIs ─────▶ adapter per provider ─────▶ ModelInfo (extended)
                      (retains every field          │
                       the provider sends)          ├──▶ CLI table / -o json
                                                    │
                                                    └──▶ ContextWindowSizeFor
                                                             │
                                        ┌────────────────────┴──────────────────┐
                                        ▼                                       ▼
                             provider reported it → use it        provider silent → embedded table
```

One precedence rule, applied in one place.

## Type changes

`ModelInfo` gains fields; existing keys keep their meaning (R6).

```go
type ModelInfo struct {
	// Existing — meanings unchanged.
	ID            string   `json:"id"`
	OwnedBy       string   `json:"owned_by,omitempty"`
	ReleaseDate   string   `json:"release_date,omitempty"`

	// Renamed in spirit only: this is the value that drives compaction, and it
	// is now whatever the provider said when the provider says anything.
	ContextWindow int64 `json:"context_window,omitempty"`

	// New. All omitempty so existing snapshots keep parsing (R6).
	MaxInputTokens int64    `json:"max_input_tokens,omitempty"`
	MaxTokens      int64    `json:"max_tokens,omitempty"`
	DisplayName    string   `json:"display_name,omitempty"`
	Lifecycle      string   `json:"lifecycle,omitempty"`       // active|deprecated|retired
	RetiresAt      string   `json:"retires_at,omitempty"`      // RFC3339 or YYYY-MM-DD
	Capabilities   []string `json:"capabilities,omitempty"`    // still a display list

	// Structured capability data, when the provider sends it. Kept separate
	// from Capabilities so the table column keeps working unchanged and so a
	// caller can distinguish "provider said false" from "provider said nothing".
	Reasoning        *bool `json:"reasoning,omitempty"`
	ToolCall         *bool `json:"tool_call,omitempty"`
	StructuredOutput *bool `json:"structured_output,omitempty"`
	Temperature      *bool `json:"temperature,omitempty"`
	Vision           *bool `json:"vision,omitempty"`
}
```

`*bool` for the four structured flags is deliberate: the agentgateway contract is
"omit when targets disagree", which is a third state distinct from `false`. Collapsing
it to `bool` would re-introduce F1's guessing.

**Why not a nested `Capabilities` struct?** `Capabilities []string` is consumed at
`model.go:295` as a joined list. A nested struct would be cleaner but requires changing
that call site and every snapshot that carries a capability array. Not worth it in this
change; the structured pointers carry the information that matters.

## Per-provider adapters

Each provider keeps its own decode struct — they genuinely differ — but each fills the
same `ModelInfo` fields and none of them overwrite a field another source set.

### agentgateway — the main fix (F1, F8)

```go
func listAgentGatewayModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	// Decode the full entry, not just id/owned_by.
	// ContextWindow: context_window || context_length || max_input_tokens
	// No local-table substitution. Delete the backfill loop.
}
```

Deleting the loop satisfies R1 and R3 at once. The `agentgateway` block in
`context-windows.json` (29 entries) then becomes dead for listing purposes — but it is
still reachable via `ContextWindowSizeFor` for the validation path, so leave the data
and correct its obviously-wrong values rather than removing it (R9).

Note the three-way window spelling. For one model the gateway may emit all three with
different values:

```json
{"id":"openai/gpt-5.6-luna",
 "context_window":1050000,      // total context, input + output
 "context_length":1050000,      // duplicate of the above
 "max_input_tokens":922000,     // the actual prompt ceiling
 "max_tokens":128000}           // output reservation
```

**The compaction window is the input ceiling, not the total.** 1050000 is
`922000 + 128000`; a prompt of 1050000 tokens cannot be sent, because 128000 is held
in reserve for the response. Using the total would let `Decide` fire at 90%
(945000) — past the real limit of 922000 — and overflow. So `max_input_tokens` wins
when present.

This also means the Context column shows the number of tokens a user can actually
fill, which is the useful figure.

**This is not a rare case.** 52 of the 241 agentgateway entries that report a window
report `max_input_tokens` strictly below `context_window`:

```
openai/gpt-5        400000 -> 272000
openai/gpt-5-mini   400000 -> 272000
openai/gpt-5.1      400000 -> 272000
openai/gpt-5.2      400000 -> 272000
```

Using the total for those 52 would compact late and overflow — the same class of bug
as F1, arrived at by a different route. The Anthropic and Gemini endpoints report a
single limit each (`max_input_tokens` / `inputTokenLimit`), so they are unaffected.

### anthropic (F3, F5, F6)

Decode `display_name`, `max_input_tokens`, `max_tokens`, `lifecycle`, `retires_at`,
`created_at`, and the capability tree. Set `OwnedBy` from `line` when present (or leave
it empty) rather than from `type`, which is always `"model"`.

Paginate: `?limit=1000` then follow `has_more` via `after_id=last_id`, bounded (R10).
`limit=1000` is the documented maximum and collapses today's 14 models to one request.
Use `lifecycle%5B%5D=active` plus `lifecycle%5B%5D=deprecated` explicitly, so behaviour
does not depend on the server's default set.

### gemini (F4, F6)

Paginate on `nextPageToken`, bounded. Decode `displayName`, `inputTokenLimit`,
`outputTokenLimit`, `supportedGenerationMethods`. Map `thinking: true` → `Reasoning`.

Request `pageSize=1000` (the documented max) so the 61-model account is one page.

### mistral (F6)

Add `reasoning` to the copied capability set — it is the one dropped flag with a
behavioural consequence. Add `name` → `DisplayName`, `deprecation` → `Lifecycle`
plus `deprecation_replacement_model` → a note. Leave `max_context_length` as
`ContextWindow` (already correct).

### openai (F6)

Add `shutdown_date` → `RetiresAt`, and derive `Lifecycle: "deprecated"` when it is set.
Keep using the embedded table for `ContextWindow` — OpenAI does not report one (R3
step 4).

### xai, openrouter, ollama

xAI: no change beyond `RetiresAt` availability (it sends none today).
OpenRouter: `context_length` is in the listing and should be decoded there rather than
only in the separate runtime query (`openrouter.go:224`), so `-o json` carries it.
Ollama: no change — already asks the daemon.

## Precedence rule, in one place

```go
// windowFor picks the compaction window for one listed model.
// Order matters: a provider that reports a window is authoritative, and the
// local table is only a fallback for the providers that report nothing.
//
// max_input_tokens outranks context_window when both are present. They are not
// the same quantity: context_window is input+output, so a prompt may never fill
// it — the output reservation is taken out of it. Compacting against the total
// fires late and overflows on exactly the long turns it exists to protect.
func windowFor(providerName string, m ModelInfo) int64 {
	switch {
	case m.MaxInputTokens > 0:
		return m.MaxInputTokens        // the real prompt ceiling
	case m.ContextWindow > 0:
		return m.ContextWindow         // provider reported only the total
	default:
		return ContextWindowSizeFor(providerName, m.ID)  // table fallback (R3)
	}
}
```

`ContextWindowSizeFor`'s doc comment gains a line stating it is a fallback for
providers that do not report a window (R11).

## CLI changes

### `-o` validation (R7)

```go
switch flagModelListOutput {
case "", "table": // human table
case "json":      // JSONL, one document per provider
default:
	return fmt.Errorf("unknown output format %q; valid: table, json", flagModelListOutput)
}
```

Validate before the provider loop so a typo fails immediately rather than after 833
models are fetched.

### Table: show unknown windows (R2)

`printProviderModels` currently prints an empty Context cell when `ContextWindow == 0`
(`model.go:292-297`). Print `?` and, after the table, a one-line note:

```
3 models have no known context window; auto-compaction is disabled for them.
```

This turns F2's silent failure into a visible one without changing any number.

### Table: use the real capabilities

The Notes column for `mistral|agentgateway|ollama` joins `Capabilities`
(`model.go:295`). With `reasoning` now present for Mistral, the column becomes accurate
for free.

## What does not change

- `ContextWindowSizeFor` / `ContextWindowSize` / `longestPrefixSize` signatures.
- `catalogFile` shape and the XDG cache path.
- `RefreshCatalog` atomic-write behaviour.
- `ValidateModel` and its refresh-on-miss path.
- `cfg.ContextWindow` override precedence (top of R3, already correct).
- Ollama's `/api/show` path.

## Risks

| Risk | Mitigation |
|---|---|
| A provider starts returning a *wrong* window, and pi-go now trusts it | The table remains as fallback; a divergence is visible in `-o json`; `research/` records the current agreement. Preferring the served value is still right — a provider knows its own limits better than a weekly snapshot. |
| Snapshot churn from new JSON fields | All new fields `omitempty`; `make fetch-models` output is sorted and date-pinned, so a diff appears once per real change (R6). |
| Pagination loops on a broken endpoint | Bounded page count (R10), plus the existing 30 s per-request timeout. |
| `has_more`/`nextPageToken` naming differs between providers | Each adapter keeps its own decode struct; there is no shared pagination abstraction to get wrong. |
| Deleting the agentgateway backfill changes `pi model show` output for users who liked the old numbers | The old numbers were wrong for 132 of 241 models. Note the change in the PR body. |
