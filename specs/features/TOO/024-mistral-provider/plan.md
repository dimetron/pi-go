# Implementation Plan: Mistral Provider (features/TOO/024-mistral-provider)

Vertical slices. Each slice compiles and passes tests independently. Run slices
in order unless marked parallel-safe; when parallel-safe, a Coordinator may batch
them in one `subagent` call up to the tool's concurrency.

Gates (run after each slice):
- **build:** `go build ./...`
- **test:** `go test ./...` (unit), `go vet ./...`
- **lint:** `golangci-lint run ./...` (final)

Reference docs:
- Chat: https://docs.mistral.ai/api/endpoint/chat#operation-chat_completion_v1_chat_completions_post
- Models: https://docs.mistral.ai/api/endpoint/models#operation-list_models_v1_models_get
- Reasoning/thinking chunk shape:
  https://docs.mistral.ai/studio-api/conversations/reasoning

---

## Slice 1: Routing fix — `mistral/` prefix strip + config auto-detect

**Files:**
- `internal/provider/provider.go`
- `internal/provider/provider_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`

**What to implement:**
1. In `provider.Resolve` (provider.go ~349), add a `mistral/` prefix check BEFORE
   the `modelPrefixes` map loop, mirroring the `azure/` block (lines 362-363):
   ```go
   // Detect mistral/ prefix → native Mistral provider.
   // The prefix is stripped; the remainder is the Mistral model name.
   if strings.HasPrefix(strings.ToLower(modelName), "mistral/") {
       return Info{Provider: "mistral", Model: modelName[len("mistral/"):]}, nil
   }
   ```
   This is the only strip needed: `magistral/...` names route through the map
   loop (prefix "magistral" → mistral) and do NOT carry a strip issue, but for
   consistency also handle `magistral/` if trivially safe (optional; keep the
   change minimal — `mistral/` is the reported bug).
2. In `internal/config/config.go` `modelPrefixes` (lines 219-225), add:
   ```go
   "mistral":   "mistral",
   "magistral": "mistral",
   ```
   This makes `autoDetectProvider("mistral/...")` and
   `autoDetectProvider("mistral-...")` return "mistral" instead of falling back
   to `DefaultProvider` ("openai"). It also fixes role resolution
   (config.go:246-251) for Mistral models.

**Tests to add:**
- `provider_test.go`: `TestResolveMistralPrefixStripped` — `Resolve("mistral/codestral-2508")`
  → Provider "mistral", Model "codestral-2508"; `Resolve("mistral/mistral-small-latest")`
  → Model "mistral-small-latest"; case-insensitive `Resolve("MISTRAL/large")`.
  Assert bare names still work (`Resolve("mistral-large-latest")` unchanged).
- `config_test.go`: extend the auto-detect table with `mistral-large-latest` →
  "mistral" and `mistral/codestral-2508` → "mistral". Assert no DefaultProvider
  fallback for these.

**Verification:** `go test ./internal/provider ./internal/config ./internal/cli`

**Dependencies:** none.

**Parallel-safe:** no (first slice).

---

## Slice 2: NewMistral signature + NewLLM wiring + test call-site updates

**Files:**
- `internal/provider/mistral.go`
- `internal/provider/provider.go`
- `internal/provider/mistral_test.go`
- `internal/provider/mistral_e2e_test.go`

**What to implement:**
1. Change `NewMistral` signature (mistral.go:27) to accept `thinkingLevel`
   before `llmOpts`, mirroring `NewOpenRouter`:
   ```go
   func NewMistral(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, llmOpts *LLMOptions) (model.LLM, error)
   ```
2. Add fields to `mistralModel`:
   ```go
   type mistralModel struct {
       modelName      string
       client         openai.Client
       thinkingLevel  string
       promptCacheKey string // uuid.NewString() per instance (xAI precedent, xai.go:62)
   }
   ```
   Generate the key in the constructor:
   ```go
   promptCacheKey: uuid.NewString(),
   ```
   (`github.com/google/uuid` is already a dependency — go.mod:20.)
3. Update `NewLLM` switch case "mistral" (provider.go:516):
   ```go
   case "mistral":
       return NewMistral(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
   ```
4. Update ALL existing `NewMistral(...)` call sites in `mistral_test.go` (11)
   and `mistral_e2e_test.go` (8) to pass `""` for the new thinkingLevel argument
   (e.g. `NewMistral(ctx, model, key, url, "", nil)`).
   Do NOT add new behavior in this slice — the goal is the signature change
   landing with zero regressions.

**Verification:** `go test ./internal/provider` — all existing Mistral tests pass
with the new signature.

**Dependencies:** Slice 1.

**Parallel-safe:** no (touches mistral.go/provider.go shared with 3, 4).

---

## Slice 3: Reasoning effort + prompt mode + prompt cache injection

**Files:**
- `internal/provider/mistral.go`
- `internal/provider/mistral_test.go`

**What to implement:**
1. In `mistralModel.GenerateContent` (mistral.go:56-90), after building `params`
   and before the stream/non-stream split, inject Mistral-specific fields:
   ```go
   // Prompt caching: Mistral bills cached input tokens at 10% and keys the
   // cache on prompt_cache_key. One key per model instance ≈ one pi session
   // (xAI precedent, xai.go:59-62).
   params.SetExtraFields(map[string]any{"prompt_cache_key": m.promptCacheKey})

   if effort := mistralReasoningEffort(modelName, m.thinkingLevel); effort != "" {
       // openai-go has no field for Mistral's reasoning_effort / prompt_mode;
       // inject directly (openrouter.go:102-108 pattern).
       params.SetExtraFields(map[string]any{"reasoning_effort": effort})
   } else if m.thinkingLevel != "" && m.thinkingLevel != "none" && mistralUsesPromptMode(modelName) {
       params.SetExtraFields(map[string]any{"prompt_mode": "reasoning"})
   }
   ```
2. Add helpers (unit-tested):
   ```go
   // mistralUsesReasoningEffort reports whether a model id takes
   // reasoning_effort instead of prompt_mode (reference TS:
   // mistral-small-2603, mistral-small-latest, mistral-medium-3.5).
   func mistralUsesReasoningEffort(modelName string) bool {
       switch modelName {
       case "mistral-small-2603", "mistral-small-latest", "mistral-medium-3.5":
           return true
       default:
           return false
       }
   }

   // mistralUsesPromptMode reports whether a model is a reasoning model that
   // controls thinking via prompt_mode:"reasoning" (magistral family).
   func mistralUsesPromptMode(modelName string) bool {
       return strings.HasPrefix(strings.ToLower(modelName), "magistral")
   }

   // mistralReasoningEffort maps pi's thinking level onto Mistral's
   // reasoning_effort. Empty/unknown returns "" (omit → model default).
   // Mapping: "none"→"none", "low"→"low", "medium"→"medium",
   // "high"→"high", "max"→"xhigh". Only called for
   // mistralUsesReasoningEffort models.
   func mistralReasoningEffort(level string) string {
       switch strings.ToLower(strings.TrimSpace(level)) {
       case "none":
           return "none"
       case "low":
           return "low"
       case "medium":
           return "medium"
       case "high":
           return "high"
       case "max", "xhigh":
           return "xhigh"
       default:
           return ""
       }
   }
   ```
   (Signature note: `mistralReasoningEffort(level string)` — model gating happens
   at the call site via `mistralUsesReasoningEffort`. Do NOT pass modelName into
   it; keep the two concerns separate.)

**Tests to add** (httptest request-body capture — assert on `r.Body` JSON):
- `mistral-small-latest` + level "high" → body has `reasoning_effort: "high"`,
  no `prompt_mode`.
- `mistral-medium-3.5` + level "medium" → `reasoning_effort: "medium"`.
- `magistral-medium-latest` + level "high" → `prompt_mode: "reasoning"`, no
  `reasoning_effort`.
- `magistral-medium-latest` + level "none" → neither field present.
- Any model + level "" → no reasoning fields.
- All requests → body has `prompt_cache_key` matching a regex for a UUID; two
  calls from the same instance share the same key.
- Table test for `mistralReasoningEffort` mapping ("" and unknown → "").

**Verification:** `go test ./internal/provider`

**Dependencies:** Slice 2.

**Parallel-safe:** no (same mistral.go as Slice 4).

---

## Slice 4: Mistral thinking extraction (stream + non-stream hooks)

**Files:**
- `internal/provider/mistral.go`
- `internal/provider/mistral_test.go`

**What to implement:**
1. Add two raw-JSON extraction hooks (mirroring `openrouterDeltaThinking` /
   `openrouterMessageReasoning` in openrouter.go). Mistral reasoning models emit
   thinking in `delta.content` / `message.content` as a JSON **array** of chunks
   (`[{"type":"thinking","thinking":[{"type":"text","text":"..."}]},
   {"type":"text","text":"..."}]`); the openai-go SDK's `Content` field is a
   plain `string`, so the array must be recovered from raw JSON:
   ```go
   // mistralDeltaThinking extracts thinking text from one streaming chunk's
   // raw JSON. Parses choices[].delta.content: a plain string (answer) or an
   // array of chunks whose type == "thinking" carry thinking[].text.
   // Returns the concatenated thinking text ("" when none).
   func mistralDeltaThinking(rawChunk string) string

   // mistralMessageThinking extracts thinking from a non-streaming
   // completion's raw JSON: choices[0].message.content array, same chunk
   // shape. Prepend-only (no partials) like openrouterMessageReasoning.
   func mistralMessageThinking(rawResponse string) string
   ```
   Implementation notes:
   - Unmarshal into `struct { Choices []struct { Delta struct {
     Content json.RawMessage `json:"content"` } `json:"delta"` } `json:"choices"` }`
     (streaming) and the `message` variant for non-streaming.
   - If `Content` is a JSON string (first byte `"`), return "" (answer text).
   - If it's an array, iterate; for each element with `"type":"thinking"`,
     read `"thinking"` (array of `{"type":"text","text":...}`) and concat texts.
   - Malformed JSON → return "" (best-effort; the existing
     `oaiRunStreamingExtract` already yields the text parts separately).
2. Wire the hooks — switch the two call sites in GenerateContent from the plain
   runners to the Extract variants (openai_completions.go:304, 375):
   ```go
   if stream {
       retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
           oaiRunStreamingExtract(ctx, &m.client, params, y, mistralDeltaThinking)
       })
   } else {
       oaiRunNonStreamingExtract(ctx, &m.client, params, yield, mistralMessageThinking)
   }
   ```

**Tests to add:**
- `mistralDeltaThinking` unit tests with synthetic chunk JSON:
  - array with a thinking chunk + text chunk → returns thinking text only.
  - plain string content → "".
  - multiple thinking chunks in one array → concatenated.
  - chunk straddling (thinking text split across two chunks) → both yielded
    (test via a full streaming run, not just the hook).
  - malformed JSON → "".
- `mistralMessageThinking` unit tests: array content → thinking text; string
  content → "".
- Streaming integration test: httptest server emits thinking chunks then answer
  chunks; assert responses contain thinking-role partials
  (`Content.Role == "thinking"`) and the final answer text is intact.

**Verification:** `go test ./internal/provider`

**Dependencies:** Slice 3.

**Parallel-safe:** no (same mistral.go as Slice 3).

---

## Slice 5: Mistral model-list enrichment (card parser + richer print)

**Files:**
- `internal/provider/list_models.go`
- `internal/provider/list_models_test.go`
- `internal/cli/model.go`
- `internal/cli/model_test.go`

**What to implement:**
1. Extend `ModelInfo` (list_models.go:16-19):
   ```go
   type ModelInfo struct {
       ID            string   `json:"id"`
       OwnedBy       string   `json:"owned_by,omitempty"`
       ContextWindow int64    `json:"-"` // max_context_length, when known
       Capabilities  []string `json:"-"` // e.g. completion_chat, vision
   }
   ```
2. Replace `listMistralModels` (list_models.go:102-105) with a dedicated card
   parser (do NOT use `listBearerModels` for mistral):
   ```go
   // listMistralModels fetches GET <base>/v1/models and parses the documented
   // Mistral model-card shape:
   // {"data":[{"id","owned_by","max_context_length","capabilities":{
   //   "completion_chat":bool,"completion_fim":bool,"function_calling":bool,
   //   "fine_tuning":bool,"vision":bool,"classification":bool}}]}
   // Only completion_chat-capable models are returned (like the reference
   // TS generator's tool_call filter). Context length and capabilities are
   // copied through for display.
   func listMistralModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error)
   ```
   Reuse the endpoint construction from `listBearerModels` (trim trailing `/`,
   `/v1/models` unless base ends in `/v1`) and `fetchJSON`.
3. CLI display (`internal/cli/model.go` `printProviderModels`, lines 173-188):
   when `providerName == "mistral"` and a model has `ContextWindow > 0`, print
   the extra columns; otherwise keep the existing layout byte-for-byte:
   ```go
   if providerName == "mistral" && m.ContextWindow > 0 {
       fmt.Printf("  %-45s  %-10s  %s\n", m.ID, humanTokens(m.ContextWindow), strings.Join(m.Capabilities, ","))
   } else if m.OwnedBy != "" {
       fmt.Printf("  %-45s  %s\n", m.ID, m.OwnedBy)
   } else {
       fmt.Printf("  %s\n", m.ID)
   }
   ```

**Tests:**
- `list_models_test.go`: update `TestListMistralModels` to serve the documented
  card shape (`max_context_length`, `capabilities`); assert ContextWindow and
  Capabilities populate; assert a non-completion_chat model is filtered out.
- `cli/model_test.go`: `TestRunModelList_Mistral` — server returns card data;
  assert output contains the context window and capability text.
- Regression: existing `TestRunModelList_OpenAI`-style tests assert unchanged
  output for other providers.

**Verification:** `go test ./internal/provider ./internal/cli`

**Dependencies:** Slice 1 (not 2-4; no file overlap).

**Parallel-safe:** yes (after Slice 1; shares no files with Slices 2-4).

---

## Slice 6: Refreshable catalog manager (CatalogFor / RefreshCatalog / FetchCatalogToRepo)

**Files:**
- `internal/provider/catalog.go` (new)
- `internal/provider/catalog_test.go` (new)

**What to implement:**
1. New file `catalog.go` with the cache + refresh machinery:
   ```go
   // modelsCacheDir returns os.UserCacheDir()/pi-go/models. Falls back to
   // "" when UserCacheDir errors (then caching is disabled, embedded only).
   func modelsCacheDir() string

   // cachePath returns the per-provider cache file path.
   func cachePath(provider string) string // <modelsCacheDir>/<provider>.json

   // CatalogFor returns known model prefixes for a provider: XDG cache first
   // (if present), else the embedded snapshot (KnownModels[provider]).
   func CatalogFor(provider string) []string

   // RefreshCatalog fetches live models for a provider (via ListModels) and
   // persists them to the XDG cache. Writes atomically (temp file + rename).
   // Returns the fetched []ModelInfo. On fetch error returns the error
   // (caller falls back to cache/embedded).
   func RefreshCatalog(ctx context.Context, providerName string, opts ListModelsOptions) ([]ModelInfo, error)

   // FetchCatalogToRepo fetches provider catalogs and writes them into
   // internal/provider/modeldata/ as <provider>.json — the `make fetch-models`
   // backend. Missing keys skip that provider (no error, logged note).
   func FetchCatalogToRepo(ctx context.Context, providers []string) error
   ```
2. Cache file JSON shape (same as the repo file, minus git metadata):
   ```go
   type catalogFile struct {
       Provider   string      `json:"provider"`
       FetchedAt  string      `json:"fetched_at"`
       Models     []ModelInfo `json:"models"`
   }
   ```
   On load, `CatalogFor` returns `models[].ID` (lowercased, like
   `uniqueSorted` in model_catalog.go). `ModelInfo.ID` is authoritative; use
   `owned_by`/`max_context_length`/capabilities only for display.
3. Atomic write helper: write to `<path>.tmp`, `os.Rename` over the target;
   `MkdirAll(cacheDir, 0o755)` first.
4. Keep `KnownModels` (embedded) untouched in this slice — it remains the
   fallback source for `CatalogFor`.

**Tests:**
- Temp cache dir via `t.Setenv("XDG_CACHE_HOME", t.TempDir())` (os.UserCacheDir
  honors XDG_CACHE_HOME on Unix):
  - `RefreshCatalog` with an httptest server writes the cache file; `CatalogFor`
    returns the fetched IDs.
  - `CatalogFor` with no cache file returns the embedded snapshot.
  - Cache file with a model ID not in the embedded snapshot is returned by
    `CatalogFor`.
  - Atomic write: cache file valid JSON after refresh; a pre-existing cache
    with an invalid file falls back to embedded (no panic).
- `FetchCatalogToRepo`: with `MISTRAL_API_KEY`/etc unset, skips providers and
  writes nothing (no error).

**Verification:** `go test ./internal/provider`

**Dependencies:** Slice 1 (ModelInfo already extended by Slice 5 — if Slices
5 and 6 run in parallel, Slice 6 must use `ID`/`OwnedBy` only and NOT reference
the new fields; safer: depend on Slice 5).

**Parallel-safe:** yes (after Slice 5; new file only).

---

## Slice 7: Wire catalog into ValidateModel (validation-miss refresh)

**Files:**
- `internal/provider/provider.go`
- `internal/provider/catalog.go`
- `internal/provider/provider_test.go`

**What to implement:**
1. In `ValidateModel` (provider.go:301-317), replace the static lookup with the
   runtime catalog and add a one-shot refresh on miss:
   ```go
   func ValidateModel(info Info) error {
       if info.Ollama || info.Custom {
           return nil
       }
       known := CatalogFor(info.Provider)
       if len(known) == 0 {
           return nil // unknown provider, skip validation
       }
       lower := strings.ToLower(info.Model)
       if matchPrefix(known, lower) {
           return nil
       }
       // Validation miss: refresh once when an API key is available, then
       // re-check against the fresh catalog. Network errors are non-fatal.
       if key := apiKeyForProvider(info.Provider); key != "" {
           if _, err := RefreshCatalog(context.Background(), info.Provider, ListModelsOptions{APIKey: key}); err == nil {
               if matchPrefix(CatalogFor(info.Provider), lower) {
                   return nil
               }
           }
       }
       return fmt.Errorf("unknown %s model %q; known models: %s",
           info.Provider, info.Model, strings.Join(known, ", "))
   }
   ```
2. Add `matchPrefix(prefixes []string, lower string) bool` helper (extract the
   loop body from the current ValidateModel) and `apiKeyForProvider(p string)
   string` that reads `config.APIKeys()[p]` — BUT avoid importing
   `internal/config` into `internal/provider` (import cycle risk: config does
   not import provider, verify). If config imports provider, instead read the
   env var directly:
   ```go
   // apiKeyForProvider returns the API key for a provider from the
   // conventional env var, without importing internal/config.
   func apiKeyForProvider(p string) string {
       switch p {
       case "mistral":
           return os.Getenv("MISTRAL_API_KEY")
       // ... add the other providers that support /v1/models
       }
       return ""
   }
   ```
   (Confirm the import direction in research before choosing; default to the
   env-var helper to keep the provider package self-contained.)
3. Guard against recursion: `RefreshCatalog` must not call `ValidateModel`.
   It calls `ListModels` only.

**Tests:**
- `provider_test.go`:
  - A model not in the embedded list passes when the XDG cache contains it.
  - A model not in either list and no key → error mentions provider and model;
    no network call (assert via a keyless env, no httptest server hit).
  - A model not in either list WITH `MISTRAL_API_KEY` set and an httptest
    server returning the model → passes after refresh.
  - `ValidateModel` with an unknown provider → nil (unchanged).
- Verify existing validation tests still pass (they rely on the embedded list;
  with `XDG_CACHE_HOME` unset the embedded list is used).

**Verification:** `go test ./internal/provider ./internal/cli`

**Dependencies:** Slice 6.

**Parallel-safe:** no (touches provider.go shared with Slice 1's file; run
after 1 and 6).

---

## Slice 8: Makefile fetch-models + embedded JSON regeneration

**Files:**
- `Makefile`
- `internal/provider/modeldata/models-<provider>.json` (new per-provider files,
  generated)
- `internal/provider/modeldata/README.md`
- `scripts/fetch-models.sh` (new, small)

**What to implement:**
1. Add a Makefile target:
   ```make
   .PHONY: fetch-models
   fetch-models:            ## Fetch per-provider model catalogs into internal/provider/modeldata/
   	@bash scripts/fetch-models.sh
   ```
2. `scripts/fetch-models.sh` (or a small Go helper via `go run`):
   - For each provider in `anthropic openai gemini mistral xai openrouter`
     (ollama has no cloud list; skip):
     - If the provider's API key env var is set, call the provider's live
       `/v1/models` (reuse `provider.ListModels` via a tiny `go run` program, or
       curl + jq — prefer the Go path to reuse `fetchJSON`/card parsing).
     - Write `internal/provider/modeldata/models-<provider>.json` in the
       `catalogFile` shape with `fetched_at`.
   - Missing keys → skip with a note (do not fail the target).
3. Update `modeldata/README.md`: document the new files, the `make fetch-models`
   workflow, and the "run before opening a PR" rule (requirements Q10).
4. `internal/provider/model_catalog.go` / `catalog.go`: `CatalogFor` should
   prefer the embedded `modeldata/models-<provider>.json` when present
   (checked in), falling back to the hard-coded lists / llm-prices files. Keep
   the existing `KnownModels` behavior for providers without a new file.
   (If this requires touching catalog.go, keep it minimal — a lookup that
   reads `modeldata/models-<provider>.json` from the embed FS, memoized.)

**Tests / verification:**
- `make fetch-models` runs without error (skips providers without keys).
- `go build ./...` succeeds with the regenerated embedded JSON (valid files).
- `go test ./internal/provider` — CatalogFor prefers the new embedded file.

**Dependencies:** Slice 6 (catalogFile shape).

**Parallel-safe:** yes (after Slice 6; touches Makefile/scripts/modeldata JSON —
no Go source overlap with 7).

---

## Slice 9: E2E tests (reasoning + caching, gated)

**Files:**
- `internal/provider/mistral_e2e_test.go`

**What to implement:**
Add `//go:build e2e` tests (skip without `MISTRAL_API_KEY`, mirroring
`testGetMistralAPIKey` at mistral_e2e_test.go:15-21):
1. `TestE2EMistralReasoningStreaming` — `mistral-small-latest` with
   thinkingLevel "high", streaming; assert at least one thinking-role partial
   (`Content.Role == "thinking"`) appears before the answer, and the answer is
   non-empty. (Mistral-small-latest supports reasoning_effort per docs; if the
   account/model doesn't emit thinking, log-and-pass rather than fail — the
   reference TS tests assert payload, not output.)
2. `TestE2EMistralPromptCacheKeyStable` — two sequential non-streaming calls
   from one `NewMistral` instance; assert both succeed and carry the same
   `prompt_cache_key` (capture via a wrapping transport if needed, or simply
   assert both calls succeed — the key is unit-tested in Slice 3).
3. `TestE2EMistralNonStreamingThinking` — non-streaming with reasoning enabled;
   assert the response parts contain the thinking text prepended (if the model
   emits it).

Update existing e2e call sites to the new `NewMistral` signature (Slice 2
already did this — verify none were missed).

**Verification:** `go test -tags e2e ./internal/provider -run Mistral`
(skips without key); `go test ./...` (build tag excludes them).

**Dependencies:** Slices 3-4 (thinking + cache behavior).

**Parallel-safe:** yes (after Slice 4; e2e file only).
