# Mistral Provider: routing fix, reasoning/caching, model list, refreshable catalog

## Objective

Make Mistral a first-class provider in pi-go. Fix the `mistral/codestral-2508`
routing/validation bug (`unknown openai model`), wire Mistral-specific chat
fields (`reasoning_effort`/`prompt_mode`/`prompt_cache_key`) through the
openai-go SDK, surface Mistral thinking blocks as thinking-role partials, enrich
`pi model list mistral` (card fields + `-o json` output), and make the
per-provider model catalog refreshable (embedded JSON fallback + XDG cache +
`make fetch-models`).

## Key Requirements

1. **Routing fix** — `mistral/` prefix auto-detects and strips (like `azure/`);
   `internal/config` `autoDetectProvider` gains `mistral`/`magistral` prefixes so
   Mistral role models no longer fall back to `DefaultProvider: "openai"`.
2. **Mistral chat fields** — `NewMistral` gains a `thinkingLevel` parameter;
   inject `reasoning_effort` (mistral-small-2603/latest, mistral-medium-3.5) or
   `prompt_mode: "reasoning"` (magistral-*) and `prompt_cache_key` (per-instance
   UUID) via `params.SetExtraFields`.
3. **Thinking output** — Mistral reasoning models emit thinking in
   `delta.content` as a JSON array; recover it from `chunk.RawJSON()` via
   `mistralDeltaThinking`/`mistralMessageThinking` hooks wired into
   `oaiRunStreamingExtract`/`oaiRunNonStreamingExtract`.
4. **Model list** — dedicated Mistral card parser (capabilities,
   max_context_length); richer mistral-only human print; new `-o json` mode
   emitting one JSON document per provider per line.
5. **Refreshable catalog** — `CatalogFor`/`RefreshCatalog` with XDG cache
   (`os.UserCacheDir()/pi-go/models/<provider>.json`); `ValidateModel` refreshes
   once on miss when an API key is present; `make fetch-models` shells out to
   `pi model list <provider> -o json` and writes `modeldata/models-<provider>.json`.

## Acceptance Criteria

### Routing & validation
- Given `pi --model mistral/codestral-2508`, when the model resolves, then no
  `unknown openai model` error is raised and `info.Model == "codestral-2508"`.
- Given a config role with `model: mistral-large-latest` and no explicit
  provider, when `ResolveRole` runs, then provider == "mistral".
- Given `Resolve("mistral/mistral-small-latest")`, then
  `Provider == "mistral"` and `Model == "mistral-small-latest"`.

### Mistral chat fields
- Given a Mistral model + thinkingLevel "high", when GenerateContent builds
  params, then the JSON body contains `reasoning_effort: "high"` (for
  mistral-small-2603/latest, mistral-medium-3.5) or `prompt_mode: "reasoning"`
  (for magistral-*) — asserted via httptest request-body capture.
- Given thinkingLevel "none", then no reasoning_effort/prompt_mode fields are
  present.
- Given one model instance, then every request carries the same non-empty
  `prompt_cache_key`.
- Given a streaming response with thinking blocks (`delta.content` array with
  type:thinking), then thinking text is yielded as `Content{Role: "thinking"}`
  partials and the final answer text is intact.

### Mistral model list
- Given `pi model list mistral` with a server returning the documented card,
  then output shows id plus context window and chat/vision flags.
- Given `pi model list mistral -o json`, then stdout is one JSON document with
  `"provider":"mistral"`, `fetched_at`, and models including
  `context_window`/`capabilities`.
- Given `pi model list openai`, then human output is unchanged.

### Refreshable catalog
- Given an XDG cache containing a provider catalog, when ValidateModel runs
  against an ID from that catalog, then it passes even if the ID is not in the
  embedded snapshot.
- Given no API key, when ValidateModel misses, then it does NOT call the network
  and returns the embedded-list error.
- Given `make fetch-models`, then `internal/provider/modeldata/*.json` files are
  (re)generated from live provider APIs, and `go build ./...` still succeeds.

## Implementation Slices

1. **Routing fix** — `mistral/` strip in `provider.Resolve` + `mistral`/`magistral`
   in config `modelPrefixes`; tests for both. Files: `internal/provider/provider.go`,
   `internal/provider/provider_test.go`, `internal/config/config.go`,
   `internal/config/config_test.go`. Verify: `go test ./internal/provider ./internal/config ./internal/cli`. Parallel-safe: no.
2. **NewMistral signature** — add `thinkingLevel` param, `promptCacheKey`
   (uuid.NewString()) field, wire `NewLLM` case, update all 19 test call sites.
   Files: `internal/provider/mistral.go`, `internal/provider/provider.go`,
   `internal/provider/mistral_test.go`, `internal/provider/mistral_e2e_test.go`.
   Verify: `go test ./internal/provider`. Parallel-safe: no.
3. **Reasoning/cache injection** — `mistralReasoningEffort`,
   `mistralUsesReasoningEffort`, `mistralUsesPromptMode` helpers +
   `params.SetExtraFields` for `reasoning_effort`/`prompt_mode`/`prompt_cache_key`;
   request-body capture tests. Files: `internal/provider/mistral.go`,
   `internal/provider/mistral_test.go`. Verify: `go test ./internal/provider`. Parallel-safe: no.
4. **Thinking extraction** — `mistralDeltaThinking`/`mistralMessageThinking`
   raw-JSON hooks; switch to `oaiRunStreamingExtract`/`oaiRunNonStreamingExtract`;
   unit + streaming integration tests. Files: `internal/provider/mistral.go`,
   `internal/provider/mistral_test.go`. Verify: `go test ./internal/provider`. Parallel-safe: no.
5. **Model list + `-o json`** — `ModelInfo` gains `ContextWindow`/`Capabilities`
   (JSON tags `context_window`/`capabilities`); dedicated `listMistralModels`
   card parser; richer mistral-only print; `--output json` flag emitting one
   JSON document per provider per line. Files: `internal/provider/list_models.go`,
   `internal/provider/list_models_test.go`, `internal/cli/model.go`,
   `internal/cli/model_test.go`. Verify: `go test ./internal/provider ./internal/cli`. Parallel-safe: yes (after 1).
6. **Catalog manager** — `catalog.go` (new): `modelsCacheDir`, `cachePath`,
   `CatalogFor`, `RefreshCatalog` (atomic write); `catalogFile` shape matching
   the CLI JSON output. Files: `internal/provider/catalog.go`,
   `internal/provider/catalog_test.go`. Verify: `go test ./internal/provider`. Parallel-safe: yes (after 5).
7. **ValidateModel integration** — consult `CatalogFor`; on prefix miss with an
   API key present, `RefreshCatalog` once and re-check; env-var key helper
   (no config import). Files: `internal/provider/provider.go`,
   `internal/provider/catalog.go`, `internal/provider/provider_test.go`.
   Verify: `go test ./internal/provider ./internal/cli`. Parallel-safe: no.
8. **Makefile fetch-models** — `scripts/fetch-models.sh` builds local `pi` and
   runs `./pi model list <provider> -o json` per provider, redirecting into
   `modeldata/models-<provider>.json`; `CatalogFor` prefers the embedded
   per-provider file; README update. Files: `Makefile`,
   `scripts/fetch-models.sh`, `internal/provider/modeldata/*.json`,
   `internal/provider/modeldata/README.md`, `internal/provider/catalog.go`.
   Verify: `make fetch-models`, `go build ./...`, `go test ./internal/provider`. Parallel-safe: yes (after 6).
9. **E2E tests** — reasoning streaming (thinking partials), cache-key stability,
   non-streaming thinking; `//go:build e2e`, skip without `MISTRAL_API_KEY`.
   Files: `internal/provider/mistral_e2e_test.go`. Verify:
   `go test -tags e2e ./internal/provider -run Mistral`, `go test ./...`. Parallel-safe: yes (after 4).

## Execution Model

Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the
**Coordinator**; it delegates rather than implements.

- **Workers**: one `worker` subagent per slice (`quick-task` for a single-file
  mechanical change). Slices marked parallel-safe may be batched in one parallel
  `subagent` call, but only up to the concurrency the `subagent` tool reports
  for the running process — beyond that the tasks queue inside one call instead
  of overlapping. All other slices run one at a time, in order.
- **Verifier**: after the last slice, a `code-reviewer` subagent checks the Done
  Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to
  10 cycles total.

## Done Criteria

The Verifier checks these against the diff, not against the checklist. Each must
be objectively checkable by reading code or running a command.
- [ ] `Resolve("mistral/codestral-2508")` returns `{Provider:"mistral", Model:"codestral-2508"}` — see provider_test.go; `config.autoDetectProvider("mistral-large-latest")` returns "mistral" — see config_test.go.
- [ ] `NewMistral` accepts a thinkingLevel parameter and `NewLLM` passes it for the mistral case — see mistral.go/provider.go.
- [ ] Mistral requests carry `reasoning_effort`/`prompt_mode` per model family and a stable per-instance `prompt_cache_key` — asserted by httptest request-body capture in mistral_test.go.
- [ ] Mistral thinking blocks stream as `Content{Role:"thinking"}` partials and are prepended on the non-streaming path — see mistralDeltaThinking/mistralMessageThinking tests.
- [ ] `pi model list mistral` shows context window + capabilities; `pi model list mistral -o json` emits one JSON document per line with provider/fetched_at/models — see model_test.go.
- [ ] `CatalogFor` returns XDG-cache IDs when present, embedded snapshot otherwise; `ValidateModel` refreshes once on miss when a key exists and never calls the network without one — see catalog_test.go/provider_test.go.
- [ ] `make fetch-models` regenerates `internal/provider/modeldata/models-<provider>.json` and `go build ./...` succeeds.
- [ ] No slice is left as a stub, TODO, or panic("not implemented").

## Gates

- **build**: `go build ./...`
- **test**: `go test ./...` (unit); `go test -tags e2e ./internal/provider -run Mistral` (e2e, skips without MISTRAL_API_KEY)
- **vet**: `go vet ./...`; `golangci-lint run ./...`

## Reference

- Design: `specs/features/TOO/024-mistral-provider/design.md`
- Outline: `specs/features/TOO/024-mistral-provider/outline.md`
- Plan: `specs/features/TOO/024-mistral-provider/plan.md`
- Requirements: `specs/features/TOO/024-mistral-provider/requirements.md`
- Research: `specs/features/TOO/024-mistral-provider/research/`

## Constraints

- Keep the openai-go SDK as the Mistral chat transport — no direct HTTP client,
  no third-party Mistral Go SDK.
- Other documented chat fields (response_format, parallel_tool_calls,
  random_seed, safe_prompt, service_tier, guardrails) are out of scope.
- Other providers' `pi model list` human output must stay byte-identical.
- `internal/provider` must not import `internal/config` (use the env-var key
  helper in Slice 7).
- `prompt_cache_key` is a per-instance UUID (xAI precedent) — the session ID is
  not plumbed into the provider layer.
- `make fetch-models` must not fail when a provider key is missing — skip with a
  note.
- Mistral thinking arrives in `delta.content` as a JSON array (NOT
  `delta.reasoning`); the openai-go SDK's `Content` field is a plain string, so
  extraction must go through `chunk.RawJSON()`.
