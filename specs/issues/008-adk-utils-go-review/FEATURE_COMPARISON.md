# pi-go vs adk-utils-go — Feature Comparison & Roadmap Recommendations

> **Scope:** Detailed side-by-side feature comparison between `pi-go` (the Go-based AI coding agent in this repo) and
> `adk-utils-go` (`github.com/achetronic/adk-utils-go`, snapshotted at `tmp/adk-utils-go/`). Produces a prioritised list
> of
> feature gaps and concrete recommendations for `pi-go`.
>
> **Date:** 2026-07-30
> **Methodology:** Two parallel `explore` agents produced exhaustive, file:line-anchored inventories of both codebases
> (adk-utils-go's 8 packages, 23 Go files, ~6,200 LOC; pi-go's 23 internal packages). All claims are anchored to actual
> file:line locations — see `Appendix A: Package Map` and `Appendix B: Feature Score Matrix`.
>
> **Status:** WIP — awaiting user prioritisation. See `## Open Questions for the user`.

---

## Executive Summary

| #  | Headline                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
|----|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1  | **pi-go's runner is built without `runner.Config.PluginConfig`** (`internal/agent/agent.go:361-365`). The only hooks are `BeforeModelCallback` / `AfterModelCallback` on the `llmagent` itself (`agent.go:265-269`). adk-utils-go's two flagship plugins (`contextguard`, `langfuse`) require `runner.PluginConfig`. **This is the single most important architectural gap to close before any plugin work.**                                                                                                         |
| 2  | **pi-go has no `artifact.Service` implementation** — only a TUI sidebar stub that returns `nil` (`internal/tui/tui.go:1736-1746`). The `agent.Config` struct lacks any `ArtifactService` field. adk-utils-go ships a complete `artifact/filesystem/artifact.go` (341 LOC) with versioning, user-namespacing, and tests.                                                                                                                                                                                               |
| 3  | **pi-go has Anthropic prompt caching ONLY on the advisor tool beta** (`internal/provider/anthropic.go:184`) — not on the standard tools / system / messages path. adk-utils-go's `genai/anthropic/caching.go` (75 LOC) applies 3 cache-control breakpoints (tools, system, last cacheable block of last message) on every request. **This is a 10× Claude cost reduction waiting to happen.**                                                                                                                         |
| 4  | **pi-go has no Postgres/Redis dependencies and no distributed backends**. All state lives in `~/.pi-go/` (files + SQLite via `modernc.org/sqlite`). adk-utils-go ships `session/redis` (864 LOC) and `memory/postgres` (616 LOC + pgvector). pi-go's `session.Service` and `memory.Store` interfaces make these drop-in alternatives.                                                                                                                                                                                 |
| 5  | **pi-go has no `ADK memory.Service` implementation** — its `internal/memory/` is a custom claude-mem-style observation store, not the ADK `memory.Service` interface. adk-utils-go ships `memory/memorytypes/types.go` (36 LOC) defining `MemoryService` + `ExtendedMemoryService`, plus a full Postgres+pgvector implementation with 3-tier search (vector → text → recent).                                                                                                                                         |
| 6  | **pi-go's OTel infrastructure is raw SDK setup only** (`internal/otel/otel.go:1-253`). It does NOT enrich LLM-call spans with prompt/completion content or token usage. adk-utils-go's `plugin/langfuse/` (700 LOC) wraps the OTLP exporter with an `enrichingExporter` that injects full payloads via the `BeforeModel`/`AfterModel` callback + `enrichedSpan` pattern.                                                                                                                                              |
| 7  | **pi-go's context-window management is manual and dead code** — `internal/session/store.go:859-957` has a `Compact()` method that is never auto-triggered, and the `SimpleSummarizer` is a placeholder. The only auto-running compaction is for tool output, not the conversation (`internal/tools/compactor.go`). adk-utils-go's `plugin/contextguard/` (~1,260 LOC) provides `BeforeModelCallback`-based auto-compaction with two strategies (threshold + sliding window) and a calibrated heuristic token counter. |
| 8  | **pi-go's memory tools are read-only**: `mem-search`, `mem-timeline`, `mem-get` (`internal/tools/mem_search.go:35-167`). adk-utils-go's `tools/memory/toolset.go` (391 LOC) adds `save_to_memory`, `update_memory`, `delete_memory` — the agent can curate its own memory. The `ExtendedMemoryService` interface extension pattern is reusable independently.                                                                                                                                                         |
| 9  | **pi-go has 3rd-party LLM client libraries adk-utils-go can teach us from** — `genai/anthropic/anthropic.go` (958 LOC) and `genai/openai/openai.go` (899 LOC) implement ~15 specific techniques (sanitizeToolID, repairMessageHistory, trimFinalAssistantWhitespace, reasoning_content extraction, tool_call_id 40-char normalization, etc.) that are drop-in improvements.                                                                                                                                           |
| 10 | **pi-go has a model catalog with context windows** (`internal/provider/model_catalog.go`, `modeldata/context-windows.json`) but no `ModelRegistry` interface — the data is global, not injectable. adk-utils-go's `plugin/contextguard/model_registry.go` (20 LOC) defines the interface; a `CrushRegistry` impl wraps catwalk's embedded model DB. **Refactoring pi-go's catalog to implement this interface is a 50-line change with high downstream value.**                                                       |

**Verdict:** pi-go is **well-engineered but feature-thin** in the ADK ecosystem. The top 5 gaps to close — in order —
are (1) `runner.PluginConfig` plumbing, (2) Anthropic prompt caching, (3) filesystem artifact service, (4)
context-window auto-compaction, (5) Langfuse-style span enrichment. After that, the `ExtendedMemoryService` pattern and
memory write tools are quick wins.

---

## Feature Matrix

Legend: ✅ done · 🟡 partial · ⚪ none · 🚫 deliberately skipped · ❓ unclear

### Architectural Foundations (gates everything else)

| Feature                                               |                                             pi-go                                             |                   adk-utils-go                    | Gap          | Priority |
|-------------------------------------------------------|:---------------------------------------------------------------------------------------------:|:-------------------------------------------------:|--------------|:--------:|
| `runner.Config.PluginConfig` set on `runner.New`      | ⚪ **Not set** — `agent.go:361-365` builds runner with only `AppName`/`Agent`/`SessionService` | ✅ All plugins (contextguard, langfuse) require it | foundational |  **H**   |
| `BeforeModelCallback` / `AfterModelCallback` on agent |                     ✅ `agent.go:265-269` — works for in-agent model calls                     |                 ✅ Same; ADK-level                 | none         |    —     |
| `BeforeToolCallback` / `AfterToolCallback` on agent   |                                     ✅ `agent.go:259-263`                                      |                 ✅ Same; ADK-level                 | none         |    —     |
| `agent.Config.PluginConfig` field                     |                           ⚪ **Missing** — only callbacks supported                            |              Required by all plugins              | foundational |  **H**   |
| `runner.Runner` accepts a `MemoryService`             |          ⚪ Field exists on `llmagent.Config` but not plumbed through `agent.Config`           |         ✅ All memory backends require it          | medium       |    M     |
| `agent.Config.ArtifactService` field                  |                      ⚪ **Missing** — no `ArtifactService` field anywhere                      |       Required by `artifact.Service` impls        | medium       |    M     |
| ADK `artifact` import in production code              |                            ⚪ Zero matches in `internal/` + `cmd/`                             |          ✅ Used by `artifact/filesystem`          | high         |  **H**   |

### Artifact Storage

| Feature                                           |                                 pi-go                                  |                   adk-utils-go                   | Gap    |    Priority    |
|---------------------------------------------------|:----------------------------------------------------------------------:|:------------------------------------------------:|--------|:--------------:|
| `artifact.Service` implementation                 | ⚪ None — only a TUI stub `internal/tui/tui.go:1736-1746` returns `nil` |  ✅ `artifact/filesystem/artifact.go` (341 LOC)   | high   |     **H**      |
| Save / Load / List / Delete                       |                                   ⚪                                    |                        ✅                         | high   |     **H**      |
| Versioned storage (monotonic)                     |                                   ⚪                                    |          ✅ `latestVersion()` walks dir           | medium |       M        |
| `user:` namespacing (cross-session per-user docs) |                                   ⚪                                    | ✅ `user:` prefix stored under session key `user` | high   | **H** (UX win) |
| Empty-directory cleanup after Delete              |                                   ⚪                                    |           ✅ `cleanEmptyDirs` walks up            | low    |       L        |
| File-level RWMutex per operation                  |                                   ⚪                                    |                        ✅                         | low    |       L        |
| Tests                                             |                                   ⚪                                    |               ✅ `artifact_test.go`               | high   |     **H**      |

### Context Window Management / Compaction

| Feature                                                               |                          pi-go                          |                                                                           adk-utils-go                                                                           | Gap          | Priority |
|-----------------------------------------------------------------------|:-------------------------------------------------------:|:----------------------------------------------------------------------------------------------------------------------------------------------------------------:|--------------|:--------:|
| Auto-triggered before every LLM call                                  | ⚪ None — runner has no `BeforeModelCallback` registered |                                                    ✅ `plugin/contextguard/` is itself a `BeforeModelCallback`                                                    | foundational |  **H**   |
| Threshold strategy (token-count based)                                |                            ⚪                            |                                                          ✅ `compaction_strategy_threshold.go` (141 LOC)                                                          | foundational |  **H**   |
| Sliding-window strategy (turn-count based)                            |                            ⚪                            |                                                       ✅ `compaction_strategy_sliding_window.go` (145 LOC)                                                        | medium       |    M     |
| Pluggable `ModelRegistry` interface                                   |               ⚪ Global map, not interface               |                                                                  ✅ `model_registry.go` (20 LOC)                                                                  | low          |    L     |
| `CrushRegistry` impl (embedded model DB)                              |                            ⚪                            |                                                               ✅ `model_registry_crush.go` (62 LOC)                                                               | low          |    L     |
| Calibrated heuristic token counter (real vs estimated)                |                 ⚪ Naive `chars/4` only                  |                                          ✅ `compaction_utils.go:217-264` — 3-way max with `defaultCorrectionFactor=2.5`                                          | high         |  **H**   |
| Token estimator covering tools/parts/inline-data                      |                     ⚪ chars/4 only                      | ✅ `compaction_utils.go:477-579` covers Text, FunctionCall args, FunctionResponse, InlineData, ToolCall, ToolResponse, PartMetadata, SystemInstruction, tool defs | high         |  **H**   |
| Tool-chain-aware split (never mid-tool-call)                          |                            ⚪                            |                                                     ✅ `walkBackToPairBoundary` / `walkForwardToPairBoundary`                                                     | high         |  **H**   |
| Todo preservation across compaction                                   |                            ⚪                            |                                                       ✅ `loadTodos` reads `[]TodoItem` from session state                                                        | medium       |    M     |
| Summary injection idempotence                                         |                            ⚪                            |                                                        ✅ Drops existing summary block before re-injecting                                                        | medium       |    M     |
| Continuation prompt injection                                         |                            ⚪                            |                                                                 ✅ `compaction_utils.go:767-794`                                                                  | medium       |    M     |
| Manual `Compact()` on session                                         |          ✅ `internal/session/store.go:859-957`          |                                                                 ⚪ Not applicable (plugin-driven)                                                                 | —            |    —     |
| Tool-output compactor (separate concern)                              |             ✅ `internal/tools/compactor.go`             |                                                                         ⚪ Not applicable                                                                         | —            |    —     |
| Per-agent options (max-tokens override, sliding-window, max-attempts) |                            ⚪                            |                                              ✅ `WithMaxTokens` / `WithSlidingWindow` / `WithMaxCompactionAttempts`                                               | medium       |    M     |
| State persistence keys (`__context_guard_*`)                          |                            ⚪                            |                                                                    ✅ `contextguard.go:53-62`                                                                     | low          |    L     |

### Observability / Tracing

| Feature                                                        |                       pi-go                        |                          adk-utils-go                          | Gap          | Priority |
|----------------------------------------------------------------|:--------------------------------------------------:|:--------------------------------------------------------------:|--------------|:--------:|
| OpenTelemetry SDK initialization                               | ✅ `internal/otel/otel.go:1-253` — gRPC + HTTP OTLP |                             ✅ Same                             | none         |    —     |
| Env-var driven config (`OTEL_*`)                               |  ✅ Reads from `~/.pi-go/.env` (no env pollution)   |                    ⚪ Reads from process env                    | pi-go better |    —     |
| Console exporter                                               |               ⚪ TODO at `otel.go:90`               |                              n/a                               | low          |    L     |
| gRPC + HTTP OTLP exporters                                     |                         ✅                          |                               ✅                                | none         |    —     |
| LLM-call span enrichment (prompt, response, tokens)            |       ⚪ Spans only carry model name as a tag       | ✅ `enrichingExporter` + `enrichedSpan` (`langfuse.go:367-444`) | high         |  **H**   |
| `generate_content` payload capture                             |                         ⚪                          |      ✅ Per `branchKey` FIFO + `enrichedSpan.Attributes()`      | high         |  **H**   |
| Langfuse plugin (per-request context + span enrichment)        |      ⚪ Zero matches for `langfuse` in source       |                 ✅ `plugin/langfuse/` (700 LOC)                 | high         |  **H**   |
| Per-branch isolation (ParallelAgent sub-agent traces)          |                         ⚪                          |              ✅ `branchKey = invocationID:branch`               | high         |  **H**   |
| Trace input/output propagation up the agent stack              |                         ⚪                          |       ✅ `beforeAgent`/`afterModel` capture top-level I/O       | high         |    M     |
| Per-request context injection (`WithUserID`, `WithTags`, etc.) |                         ⚪                          |                    ✅ `context.go` (125 LOC)                    | medium       |    M     |
| Config struct with YAML/JSON tags                              |               ✅ Pi-go uses env vars                |          ✅ `Config{PublicKey, SecretKey, Host, ...}`           | —            |    —     |
| Defensive nil checks (`span.IsRecording()`)                    |                        n/a                         |                          ✅ Everywhere                          | —            |    —     |
| Tool spans (`tool.bash`, `tool.<name>`, etc.)                  |   ✅ `tools/bash.go:63`, `extension/hooks.go:152`   |                              n/a                               | pi-go better |    —     |
| Agent prompt span (`agent.prompt`)                             |           ✅ `tui/agent_loop.go:422-423`            |                              n/a                               | pi-go better |    —     |

### Session Backends

| Feature                                                     |                               pi-go                                |                        adk-utils-go                        | Gap        | Priority |
|-------------------------------------------------------------|:------------------------------------------------------------------:|:----------------------------------------------------------:|------------|:--------:|
| `session.Service` interface boundary                        | ✅ `internal/agent/agent.go:257` — `SessionService session.Service` |                           ✅ Same                           | none       |    —     |
| Filesystem backend (JSONL append-only)                      |             ✅ `internal/session/store.go` (1033+ LOC)              |                       ⚪ Not provided                       | pi-go only |    —     |
| Redis backend                                               |                           ⚪ No Redis dep                           |           ✅ `session/redis/session.go` (864 LOC)           | medium     |    M     |
| `app:` / `user:` / `temp:` state prefix handling            |                           ✅ Implemented                            | ✅ `session.go:499-566` (mirrors `internal/sessionutils.*`) | none       |    —     |
| Live state mirror after `AppendEvent`                       |                                 ✅                                  |                   ✅ `session.go:316-413`                   | none       |    —     |
| Per-tier TTL (`AppStateTTL`, `UserStateTTL`)                |                        ⚪ Single global TTL                         |                     ✅ Separate config                      | low        |    L     |
| Atomic file rewrites (temp + rename)                        |                       ✅ `store.go:999-1030`                        |               ✅ (Redis is naturally atomic)                | none       |    —     |
| `Compact()` on session                                      |                   ✅ `store.go:859-957` (manual)                    |                             ⚪                              | pi-go only |    —     |
| `Archive()` to `archive/yyyy/mm/dd/<id>/`                   |                     ✅ `store.go` Delete method                     |                             ⚪                              | pi-go only |    —     |
| In-memory LRU cache (20 sessions)                           |                       ✅ `maxCachedSessions`                        |                    ⚪ Redis is the cache                    | pi-go only |    —     |
| ATIF trajectory writer integration                          |                         ✅ `internal/atif/`                         |                             ⚪                              | pi-go only |    —     |
| Time-sortable session IDs                                   |                    ✅ `yymmdd-hhmm-xxxxx-xxxxx`                     |                             ⚪                              | pi-go only |    —     |
| Title OSC-injection sanitization                            |                        ✅ `store.go:559-591`                        |                             ⚪                              | pi-go only |    —     |
| Branches (fork/replay)                                      |         ✅ `branches.json` + `branches/<name>/events.jsonl`         |                             ⚪                              | pi-go only |    —     |
| `SetSessionModel` / `SetSessionTitle` / `LastSessionID`     |               ✅ Convenience methods on `FileService`               |                             ⚪                              | pi-go only |    —     |
| `var _ session.Service = (*FileService)(nil)` compile check |                         ✅ `store.go:1033`                          |            ✅ `session/redis/session.go:859-863`            | none       |    —     |

### Memory Systems

| Feature                                                           |                             pi-go                              |                            adk-utils-go                             | Gap             | Priority |
|-------------------------------------------------------------------|:--------------------------------------------------------------:|:-------------------------------------------------------------------:|-----------------|:--------:|
| Memory backend type                                               | 🟡 SQLite observation store (custom, not ADK `memory.Service`) |            ✅ Postgres + pgvector (ADK `memory.Service`)             | high (paradigm) |  **H**   |
| `memory.Service` interface implementation                         |   ⚪ None — `internal/memory/` is a custom `Store` interface    |   ✅ `memory/memorytypes.MemoryService` + `PostgresMemoryService`    | foundational    |  **H**   |
| `ExtendedMemoryService` interface (CRUD with IDs)                 |                               ⚪                                |               ✅ `memorytypes/types.go:24-30` (36 LOC)               | high            |  **H**   |
| `AddSessionToMemory` (session → memory)                           |                               ⚪                                |                            ✅ `memory.go`                            | high            |  **H**   |
| `SearchMemory` (semantic + text fallback)                         |              ⚪ Custom `search.go` uses FTS5 only               |              ✅ 3-tier fallback: vector → text → recent              | high            |  **H**   |
| `SearchWithID` (return IDs for update/delete)                     |                               ⚪                                |                                  ✅                                  | high            |    M     |
| `UpdateMemory` / `DeleteMemory`                                   |                               ⚪                                |                                  ✅                                  | high            |    M     |
| Pluggable `EmbeddingModel` interface                              |    🟡 Has `palace/embedder.go` (MiniLM-L6) for palace only     | ✅ `memory/postgres/embedding.go` defines `EmbeddingModel` interface | medium          |    M     |
| OpenAI-compatible embedding adapter                               | 🟡 Embedded model only (no HTTP client for OpenAI embeddings)  |               ✅ `OpenAICompatibleEmbedding` (128 LOC)               | medium          |    M     |
| Lazy dimension detection                                          |                   ⚪ Hardcoded 384 for MiniLM                   |         ✅ `embedding.go:106-108` — first Embed call sets it         | low             |    L     |
| `vector(N)` schema with `IVFFlat` index                           |                      ⚪ (no pgvector dep)                       |                        ✅ `memory.go:82-145`                         | medium          |    L     |
| `tsvector` + `ts_rank` text fallback                              |                        ⚪ (FTS5 instead)                        |                        ✅ `memory.go:251-315`                        | medium          |    L     |
| `ON CONFLICT` upsert on replay                                    |                        ⚪ (no Postgres)                         |                        ✅ `memory.go:163-176`                        | low             |    L     |
| `vectorToString` manual `[f1,f2,...]` serialization               |                               ⚪                                |           ✅ `memory.go:597-611` (no pgx/pgvector driver)            | low             |    L     |
| Claude-mem observation capture (decision/bugfix/feature/refactor) |           ✅ `memory/types.go` + `memory/compress.go`           |                       ⚪ Not the same paradigm                       | pi-go only      |    —     |
| Subagent `memory-compressor` (smol model)                         |                ✅ `internal/memory/compress.go`                 |                                  ⚪                                  | pi-go only      |    —     |
| `ContextGenerator` with token budget                              |                 ✅ `internal/memory/context.go`                 |                                  ⚪                                  | pi-go only      |    —     |
| Background worker (channel-fed)                                   |                 ✅ `internal/memory/worker.go`                  |                                  ⚪                                  | pi-go only      |    —     |
| PII detection / redaction                                         |                 ✅ `internal/memory/privacy.go`                 |                                  ⚪                                  | pi-go only      |    —     |
| SQLite FTS5 with sync triggers                                    |         ✅ `internal/memory/db.go` (v1, v2 migrations)          |                                  ⚪                                  | pi-go only      |    —     |
| `modernc.org/sqlite` (pure Go, no CGO)                            |                           ✅ `go.mod`                           |                                  ⚪                                  | pi-go only      |    —     |

### Memory Tools (exposed to agent)

| Feature                                                                                         |             pi-go              |                     adk-utils-go                      | Gap        | Priority |
|-------------------------------------------------------------------------------------------------|:------------------------------:|:-----------------------------------------------------:|------------|:--------:|
| `mem-search` / `memory_search` (full-text)                                                      |   ✅ `tools/mem_search.go:35`   |             ✅ `search_memory` (semantic)              | partial    |    M     |
| `mem-timeline` (context around anchor)                                                          |  ✅ `tools/mem_search.go:101`   |                           ⚪                           | pi-go only |    —     |
| `mem-get` (fetch full details by ID)                                                            |  ✅ `tools/mem_search.go:162`   |                           ⚪                           | pi-go only |    —     |
| `memory_get` (analog of `mem-get`)                                                              |               ⚪                |                           ⚪                           | n/a        |    —     |
| `save_to_memory`                                                                                |               ⚪                |                  ✅ `toolset.go:208`                   | high       |  **H**   |
| `update_memory`                                                                                 |               ⚪                |        ✅ `toolset.go:253` (extended interface)        | medium     |    M     |
| `delete_memory`                                                                                 |               ⚪                |        ✅ `toolset.go:294` (extended interface)        | medium     |    M     |
| `singleEntrySession` adapter (wrap one content as session for `AddSessionToMemory`)             |               ⚪                |                ✅ `toolset.go:330-390`                 | medium     |    M     |
| Category tagging (`[category] content` as Text)                                                 |               ⚪                |              ✅ `toolset.go:208, 375-377`              | low        |    L     |
| `jsonschema` struct tags on args                                                                |             ✅ Used             |                        ✅ Used                         | none       |    —     |
| `Toolset.Name()` / `Tools(ctx)`                                                                 | ✅ ADK `tool.Toolset` interface |                        ✅ Same                         | none       |    —     |
| Auto-extended tools (register update/delete only if service implements `ExtendedMemoryService`) |               ⚪                | ✅ `toolset.go:55-59` + `DisableExtendedTools` opt-out | high       |    M     |

### Anthropic LLM Client (techniques)

| Feature                                                                    |                  pi-go (`internal/provider/anthropic.go`)                   |                                                                                                                             adk-utils-go (`genai/anthropic/`)                                                                                                                              | Gap        |         Priority          |
|----------------------------------------------------------------------------|:---------------------------------------------------------------------------:|:------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------:|------------|:-------------------------:|
| Uses official `anthropic-sdk-go`                                           |                                  ✅ v1.57.0                                  |                                                                                                                                           ✅ Same                                                                                                                                           | none       |             —             |
| Public `Config` struct                                                     | ⚪ Only `NewAnthropic(ctx, modelName, apiKey, baseURL, thinkingLevel, opts)` |                                                                                  ✅ `Config{APIKey, ModelName, BaseURL, MaxOutputTokens, ThinkingBudgetTokens, ThinkingEffort, ThinkingMode, HTTPOptions}`                                                                                  | medium     |             M             |
| Public `HTTPOptions{Headers http.Header}` for custom headers               |                                      ⚪                                      |                                                                                                                                             ✅                                                                                                                                              | medium     |             M             |
| Dual thinking API (`enabled` budget vs `adaptive` effort)                  |                          🟡 Single thinking level                           |                                                                                                               ✅ `ThinkingModeEnabled` / `ThinkingModeAdaptive` (auto-deduce)                                                                                                               | high       | **H** (Opus 4.5+ support) |
| Per-request JSON injection (`option.WithJSONSet`)                          |                                      ⚪                                      |                                                                                                                         ✅ `anthropic.go:206-214` for adaptive mode                                                                                                                         | medium     |             M             |
| **Prompt caching** (3 breakpoints)                                         |                         ⚪ Only on advisor tool beta                         |                                                                                                       ✅ `caching.go` (75 LOC) — tools, system, last cacheable block of last message                                                                                                        | high       |       **H** (cost)        |
| `repairMessageHistory` (drop orphaned `tool_use` blocks)                   |                            ✅ `anthropic.go:768`                             |                                                                                                                                  ✅ `anthropic.go:767-814`                                                                                                                                  | partial    |      L (port tests)       |
| `sanitizeToolID` (replace invalid chars with SHA256-derived `toolu_<hex>`) |                            ✅ `anthropic.go:757`                             |                                                                                                                                  ✅ `anthropic.go:756-765`                                                                                                                                  | none       |             —             |
| `trimFinalAssistantWhitespace` (Anthropic 400 on trailing ws)              |                            ✅ `anthropic.go:822`                             |                                                                                                                                  ✅ `anthropic.go:816-843`                                                                                                                                  | none       |             —             |
| Schema `type` lowercase (recursive walk)                                   |                            ✅ `anthropic.go:942`                             |                                                                                                                                  ✅ `anthropic.go:940-958`                                                                                                                                  | none       |             —             |
| Inline-data router (MIME → image/PDF/text document blocks)                 |                            ✅ `anthropic.go:886`                             |                                                                                                                                  ✅ `anthropic.go:886-933`                                                                                                                                  | none       |             —             |
| Usage aggregation (`promptTokens = Input + CacheRead + CacheCreation`)     |                                      ❓                                      |                                                                                                                                  ✅ `anthropic.go:598-609`                                                                                                                                  | medium     |             M             |
| `ErrNoContentInResponse` sentinel                                          |                                      ❓                                      |                                                                                                                                             ✅                                                                                                                                              | low        |             L             |
| Thinking-block skip in cache walker                                        |                                     n/a                                     |                                                                                                                                    ✅ `caching.go:53-60`                                                                                                                                    | high       |           **H**           |
| `MaxOutputTokens` config                                                   |                              🟡 Set internally                              |                                                                                                                                   ✅ Public Config field                                                                                                                                    | low        |             L             |
| OAuth support (`isAnthropicOAuthToken`)                                    |                             ✅ `anthropic.go:98`                             |                                                                                                                                  ⚪ Not surfaced in Config                                                                                                                                  | pi-go only |             —             |
| Advisor tool beta                                                          |                 ✅ `anthropic.go:161-213` (uses BetaService)                 |                                                                                                                                             ⚪                                                                                                                                              | pi-go only |             —             |
| Test breadth                                                               |                       🟡 1 file (`anthropic_test.go`)                       | ✅ 12+ files: `core_test.go`, `tools_test.go`, `caching_test.go`, `thinking_test.go`, `trim_test.go`, `stream_usage_test.go`, `wire_test.go`, `wire_tool_payload_test.go`, `tool_payload_test.go`, `inline_data_test.go`, `semantic_integration_test.go`, `repair_test.go`, `utils_test.go` | high       |  **H** (testing pattern)  |

### OpenAI LLM Client (techniques)

| Feature                                                            |                    pi-go                    |                                 adk-utils-go                                 | Gap        | Priority |
|--------------------------------------------------------------------|:-------------------------------------------:|:----------------------------------------------------------------------------:|------------|:--------:|
| Chat Completions API                                               | ✅ `internal/provider/openai_completions.go` |                          ✅ `genai/openai/openai.go`                          | —          |    —     |
| Responses API                                                      |  ✅ `internal/provider/openai_responses.go`  |                                      ⚪                                       | pi-go only |    —     |
| `StreamOptions.IncludeUsage` opt-in (otherwise no usage in stream) |                      ❓                      | ✅ `openai.go:151` — `params.StreamOptions.IncludeUsage = param.NewOpt(true)` | medium     |    M     |
| `reasoning_content` extraction (DeepSeek/Qwen/etc. raw JSON)       |                      ❓                      |         ✅ `openai.go:166-172` + `extractReasoningContent` (~120 LOC)         | medium     |    M     |
| Tool `tool_call_id` 40-char normalization                          |                      ❓                      |                 ✅ `openai.go:36-37` (`maxToolCallIDLength`)                  | medium     |    M     |
| `response_format` JSON schema strict mode                          |                      ❓                      |                  ✅ `openai.go:321-342` with `Strict: true`                   | medium     |    M     |
| Schema lowercase + ensure `properties` for object types            |                      ❓                      |                            ✅ `openai.go:568-597`                             | low        |    L     |
| `tool_choice` ModeAuto/Any/None mapping                            |                      ❓                      |                            ✅ `openai.go:351-388`                             | low        |    L     |
| Reasoning effort mapping from `genai.ThinkingConfig`               |                      ❓                      |                            ✅ `openai.go:316-319`                             | medium     |    M     |
| `MarshalToolPayload` (null → `{}`)                                 |                      ❓                      |                     ✅ `genai/common/payload.go` (37 LOC)                     | low        |    L     |
| Azure OpenAI                                                       |    ✅ `internal/provider/openai_azure.go`    |                              🟡 Via `base_url`                               | —          |    —     |
| ChatGPT Codex backend                                              |    ✅ `internal/provider/openai_codex.go`    |                                      ⚪                                       | pi-go only |    —     |

### Model Registry / Catalog

| Feature                                                                                       |                 pi-go                 |               adk-utils-go               | Gap        |   Priority    |
|-----------------------------------------------------------------------------------------------|:-------------------------------------:|:----------------------------------------:|------------|:-------------:|
| Embedded model catalog                                                                        | ✅ `modeldata/*.json` via `//go:embed` | ✅ `catwalk/pkg/embedded` (CrushRegistry) | none       |       —       |
| `llm-prices-anthropic.json` / `llm-prices-openai.json`                                        |                   ✅                   |                   n/a                    | pi-go only |       —       |
| `context-windows.json` (prefix → window-size map)                                             |                   ✅                   |                   n/a                    | pi-go only |       —       |
| Hard-coded gemini/mistral lists                                                               |                   ✅                   |                   n/a                    | pi-go only |       —       |
| `compatibilityModelAliases`                                                                   |                   ✅                   |                   n/a                    | pi-go only |       —       |
| `ContextWindowSize(modelName) int64` longest-prefix lookup                                    |          ✅ `provider.go:100`          |     ✅ `CrushRegistry.ContextWindow`      | none       |       —       |
| `ModelRegistry` interface (injectable)                                                        |      ⚪ Global map, not interface      |   ✅ `model_registry.go:8-11` (20 LOC)    | low        | L (quick win) |
| `DefaultMaxTokens(modelID)`                                                                   |                   ❓                   |    ✅ `ModelRegistry.DefaultMaxTokens`    | low        |       L       |
| Multi-provider routing via prefix (`claude`, `gpt`, `gemini`, `mistral`, `ollama/`, `azure/`) |                   ✅                   |                   n/a                    | pi-go only |       —       |
| `:cloud` / `-cloud` suffix routing                                                            |                   ✅                   |                   n/a                    | pi-go only |       —       |
| `pi model list` CLI (5 latest per provider)                                                   |                   ✅                   |                   n/a                    | pi-go only |       —       |
| `LLMOptions{ExtraHeaders, InsecureSkipTLS, AdvisorModel, ...}`                                |          ✅ `provider.go:258`          |                   n/a                    | pi-go only |       —       |

### Compaction (tool output — separate concern)

| Feature                                        |              pi-go              |   adk-utils-go   | Gap        | Priority |
|------------------------------------------------|:-------------------------------:|:----------------:|------------|:--------:|
| `BuildCompactorCallback` (AfterToolCallback)   | ✅ `internal/tools/compactor.go` | ⚪ Not applicable | pi-go only |    —     |
| ANSI strip                                     |                ✅                |       n/a        | —          |    —     |
| Test output aggregation                        |                ✅                |       n/a        | —          |    —     |
| Build error filtering                          |                ✅                |       n/a        | —          |    —     |
| Git output compaction                          |                ✅                |       n/a        | —          |    —     |
| Linter aggregation                             |                ✅                |       n/a        | —          |    —     |
| Smart truncation                               |                ✅                |       n/a        | —          |    —     |
| Pipeline-style config (`CompactorConfig` JSON) |                ✅                |       n/a        | —          |    —     |

### Examples / Demos

| Example                                       | adk-utils-go                                     | Reusable for pi-go?             |
|-----------------------------------------------|--------------------------------------------------|---------------------------------|
| `examples/openai-client/main.go` (99 LOC)     | Ollama via OpenAI compat                         | low — pi-go already has Ollama  |
| `examples/anthropic-client/main.go` (120 LOC) | Env-driven thinking config                       | medium — config patterns        |
| `examples/session-memory/main.go` (183 LOC)   | Redis multi-turn conversation                    | medium — session API demo       |
| `examples/long-term-memory/main.go` (169 LOC) | Postgres + embedding + memory toolset            | high — memory integration shape |
| `examples/full-memory/main.go` (265 LOC)      | Redis + Postgres + memory toolset                | high — full memory stack        |
| `examples/context-guard/main.go` (153 LOC)    | All three guard configs (crush/override/sliding) | high — proves out the plugin    |

---

## Detailed Feature Analysis with Scores

Each feature is scored on:

- **Value** (1–5): How much value does this bring to pi-go?
- **Effort** (1–5): 1 = trivial, 5 = significant refactor
- **Risk** (1–5): 1 = safe, 5 = could break existing behavior
- **Total** = Value × 5 − Effort − Risk (range roughly −10 to +20; higher = better)

### Feature 1: `runner.PluginConfig` Plumbing ⭐ Highest Priority

| Aspect     | Detail                                                                                                   |
|------------|----------------------------------------------------------------------------------------------------------|
| **Source** | n/a — pi-go is *missing* this                                                                            |
| **Target** | `internal/agent/agent.go:241-276` (Config struct) + `agent.go:361-365` (buildRunner)                     |
| **Value**  | 5 — gates every ADK plugin; required by contextguard AND langfuse                                        |
| **Effort** | 1 — two-line passthrough: add `PluginConfig *runner.PluginConfig` to `Config`, set it on `runner.Config` |
| **Risk**   | 1 — purely additive, no breakage                                                                         |
| **Total**  | **+23**                                                                                                  |

**Description.** pi-go's `agent.Config` (`internal/agent/agent.go:241-276`) supports `BeforeModelCallbacks` and
`AfterModelCallbacks` directly on the `llmagent`, but does **not** expose ADK's `runner.Config.PluginConfig`.
adk-utils-go's two flagship plugins (contextguard, langfuse) are both implemented as `runner.PluginConfig` (`Setup()`
returns `runner.PluginConfig`).

**Why it matters.** Without this single field, pi-go cannot adopt ANY ADK-level plugin. With it, every plugin becomes a
1-line wire-up.

**Concrete change:**

```go
// internal/agent/agent.go:241 (add field)
type Config struct {
    // ... existing fields ...
    PluginConfig *runner.PluginConfig  // NEW
}

// internal/agent/agent.go:361 (add to runner.Config)
r, err := runner.New(runner.Config{
    AppName:        AppName,
    Agent:          llmAgent,
    SessionService: sessionSvc,
    PluginConfig:   cfg.PluginConfig,  // NEW
})
```

**Score breakdown.** Value=5 (gates everything). Effort=1 (trivial). Risk=1 (additive). Total=+23.

---

### Feature 2: Filesystem Artifact Service

| Aspect     | Detail                                                                                                           |
|------------|------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/artifact/filesystem/artifact.go:1-341` (+ `artifact_test.go`)                                  |
| **Target** | `internal/agent/agent.go:241-276` (add `ArtifactService`) + new `internal/artifact/filesystem/` package          |
| **Value**  | 5 — fills a hard gap (TUI sidebar stub returns `nil`); enables cross-session per-user docs via `user:` namespace |
| **Effort** | 2 — copy + adapt to pi-go naming; wire to existing `tui/sidebar.go` `ArtifactEntry`                              |
| **Risk**   | 2 — adds an `ArtifactService` field; needs A2A + image-paste integration consideration                           |
| **Total**  | **+21**                                                                                                          |

**Description.** adk-utils-go ships a complete `artifact.Service` impl on the filesystem. Layout:
`{BasePath}/{appName}/{userID}/{sessionID|user}/{fileName}/{version}.json`. The `user:` namespace trick — files prefixed
`user:` are stored under the session key `user`, making them accessible across all sessions for `(appName, userID)` — is
a novel UX feature that pi-go could adopt directly.

**Public API to port:**

```go
type FilesystemService struct { basePath string; mu sync.RWMutex }
type FilesystemServiceConfig struct { BasePath string }
func NewFilesystemService(cfg) (*FilesystemService, error)
// artifact.Service: Save / Load / Delete / List / Versions / GetArtifactVersion
```

**Notable patterns worth lifting:**

- **`user:` namespace** (`artifact.go:23, 61-78`) — high value, novel
- **Monotonic version** (`artifact.go:94-97`) — `latestVersion` scans dir
- **Empty-directory cleanup** (`artifact.go:330-338`) — `cleanEmptyDirs` walks up
- **Sorted version list** (`artifact.go:300-302`) — desc order

**Test coverage** (`artifact_test.go`) — should port with the file.

**Score breakdown.** Value=5 (hard gap). Effort=2. Risk=2. Total=+21.

---

### Feature 3: Anthropic Prompt Caching

| Aspect     | Detail                                                                                            |
|------------|---------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/genai/anthropic/caching.go:1-75` (+ `caching_test.go`)                          |
| **Target** | `internal/provider/anthropic.go` (call `applyCacheControl` after `buildMessageParams`)            |
| **Value**  | 5 — **10× cost reduction on Claude for agentic loops**; pi-go's largest gap in provider cost      |
| **Effort** | 1 — copy 75 LOC file, add one call site in `anthropicModel.GenerateContent`                       |
| **Risk**   | 1 — `cache_control: ephemeral` is purely additive metadata; Anthropic ignores it if not supported |
| **Total**  | **+23**                                                                                           |

**Description.** pi-go's `internal/provider/anthropic.go` only sets `CacheControl` on the **advisor tool beta** (line
184). It does NOT apply general prompt caching to tools, system, or messages. adk-utils-go's `caching.go` is 75 LOC that
applies 3 `cache_control: ephemeral` breakpoints:

1. **Last tool definition** (rarely changes per agent)
2. **Last system block** (edits don't invalidate tools cache)
3. **Last cacheable block of the last message** (turn N's history → turn N+1's prefix)

Anthropic allows max 4 breakpoints; they pick 3. Thinking/redacted-thinking blocks are skipped (`caching.go:53-60`).

**Why it matters.** In agentic loops the full history is re-sent every tool round-trip. Caching delivers ~10% of input
cost on cached prefixes. For long-running coding sessions, this is material.

**Concrete change:**

```go
// internal/provider/anthropic.go (end of buildMessageParams, before antRun*)
if !m.disableCaching {
    applyCacheControl(&params)
}
```

**Test file** (`caching_test.go`, 196 LOC) — should port with the file.

**Score breakdown.** Value=5 (huge cost win). Effort=1 (75 LOC + 1 call). Risk=1. Total=+23.

---

### Feature 4: Context-Window Auto-Compaction (Context Guard)

| Aspect     | Detail                                                                                                                                                              |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/plugin/contextguard/` (~1,260 LOC across 5 files)                                                                                                 |
| **Target** | New `internal/contextguard/` package; requires Feature 1 (PluginConfig plumbing)                                                                                    |
| **Value**  | 5 — **CRITICAL for long-running CLI sessions**; pi-go's `internal/session/store.go:859-957` `Compact()` is never auto-triggered and the summarizer is a placeholder |
| **Effort** | 4 — port 5 files, adapt to pi-go's `Config` field conventions, integrate with `agent.Config.PluginConfig`                                                           |
| **Risk**   | 3 — `BeforeModelCallback` injection changes LLM-call behavior; latency hit per compaction (one extra LLM round-trip); needs feature flag                            |
| **Total**  | **+18**                                                                                                                                                             |

**Description.** The flagship adk-utils-go feature. A `BeforeModelCallback` that compacts conversation history before
every LLM call when it would overflow. Two strategies:

- **`threshold` (token-based)**: triggers when estimated tokens approach `ModelRegistry.ContextWindow(modelID)` minus
  safety buffer (20k for windows ≥200k, 20% for smaller).
- **`sliding_window` (turn-count-based)**: triggers after N turns regardless of token count.

**Public API to port:**

```go
func New(registry ModelRegistry) *Guard
func (g *Guard) Add(agentName string, model model.LLM, opts ...Option) *Guard
type Option func(*agentOptions)
func WithSlidingWindow(maxTurns int) Option
func WithMaxTokens(maxTokens int) Option
func WithMaxCompactionAttempts(n int) Option
func (g *Guard) PluginConfig() runner.PluginConfig
```

**Notable patterns worth lifting:**

- **Calibrated heuristic token counter** (`compaction_utils.go:217-264`):
  ```go
  currentHeuristic = estimateTokens(req)
  realTokens       = loadRealTokens(ctx)        // from AfterModel
  lastHeuristic    = loadLastHeuristic(ctx)     // persisted by BeforeModel
  correction       = min(max(1.0, real/last), 5.0)
  calibrated       = currentHeuristic * correction
  result           = max(realTokens, calibrated)
  if no real tokens → currentHeuristic * 2.5
  ```
- **Token estimator** covering Text, FunctionCall args, FunctionResponse, InlineData, ToolCall, ToolResponse,
  PartMetadata, SystemInstruction, tool defs (`compaction_utils.go:477-579`) — **important fidelity improvement** over
  naive `chars/4` when tools emit large outputs
- **Tool-chain-aware split** (`compaction_utils.go:623-695`) — never splits mid-tool-call
- **Todo preservation** across compaction (`compaction_utils.go:268-310, 418-426`)
- **Summary injection idempotence** (`compaction_utils.go:719-749`) — drops existing summary block before re-injecting
- **Continuation prompt** (`compaction_utils.go:767-794`) — `The user's current request is: \`{text}\`. Continue working
  on this request without asking the user to repeat anything.`

**pi-go integration concerns:**

- Replace `SimpleSummarizer` (currently a placeholder at `internal/session/store.go:865`) with a real LLM-backed
  summarizer. Could reuse the same model as the agent (configurable).
- Wire `ModelRegistry` to the existing `internal/provider/model_catalog.go` (Feature 9 below).
- State persistence keys (`__context_guard_*_{agent}`) need to live in `session.State`, not in tool output.

**Test files to port:** `contextguard_unit_test.go`, `compaction_strategy_singleshot_test.go`,
`compaction_strategy_multiturn_test.go`.

**Score breakdown.** Value=5. Effort=4. Risk=3. Total=+18.

---

### Feature 5: Langfuse Plugin (Architecture Pattern)

| Aspect     | Detail                                                                                                                                                        |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/plugin/langfuse/` (~700 LOC across 3 files)                                                                                                 |
| **Target** | New `internal/observability/langfuse/` package; requires Feature 1 (PluginConfig plumbing)                                                                    |
| **Value**  | 4 — fills the LLM-payload-enrichment gap in `internal/otel/`; high value for debugging/cost-tracking; not 5 because pi-go users may prefer different backends |
| **Effort** | 4 — port 3 files, adapt `enrichingExporter` to pi-go's `otel.go`                                                                                              |
| **Risk**   | 3 — touches the entire OTel pipeline; needs feature flag                                                                                                      |
| **Total**  | **+13**                                                                                                                                                       |

**Description.** adk-utils-go's `plugin/langfuse/langfuse.go` (523 LOC) is a `runner.PluginConfig` that exports OTLP
traces to Langfuse and — critically — **enriches `generate_content` spans with prompt/response payloads** via an
`enrichingExporter` + `enrichedSpan` pattern. This is what gives Langfuse its actual value (seeing prompts, completions,
token counts, costs).

**Architecture (the part worth lifting even if Langfuse itself is not adopted):**

1. `Setup()` (`langfuse.go:73-134`) builds an `otlptracehttp` exporter + `enrichingExporter` wrapper. Returns
   `runner.PluginConfig` + shutdown func.
2. `spanEnricher` (`langfuse.go:169-173`) maintains two maps:
    - `agentSpans map[branchKey][]oteltrace.Span` — per-branch stack of `invoke_agent` spans
    - `pending map[invoke_agent_spanID][]llmCall` — FIFO of pending LLM calls awaiting export
3. **`branchKey`** (`langfuse.go:177-182`): `invocationID:branch`. Sequential flows → `"invocationID"`. ParallelAgent →
   `"invocationID:branch1"` etc. **Critical for parallel sub-agent trace isolation.**
4. `enrichingExporter.ExportSpans` (`langfuse.go:367-400`): for every span, if name starts with `generate_content`, look
   up parent span ID → pop oldest pending `llmCall` → attach attributes.
5. `enrichedSpan` (`langfuse.go:414-444`) wraps `sdktrace.ReadOnlySpan` and overrides only `Attributes()`. **Key
   insight:** the `generate_content` span is created *inside* the model adapter; callbacks can't see it. The exporter
   matches by **parent span ID** = the `invoke_agent` span, which IS visible to callbacks.

**Per-request context injection** (`context.go`, 125 LOC):

```go
WithUserID(ctx, userID)         context.Context
WithTags(ctx, []string)         context.Context
WithTraceMetadata(ctx, map)     context.Context
WithEnvironment(ctx, env)       context.Context
WithRelease(ctx, release)       context.Context
WithTraceName(ctx, name)        context.Context
```

**Score breakdown.** Value=4. Effort=4. Risk=3. Total=+13.

---

### Feature 6: ADK `memory.Service` Interface (foundational)

| Aspect     | Detail                                                                                                                          |
|------------|---------------------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/memory/memorytypes/types.go:1-36`                                                                             |
| **Target** | New `internal/memory/adk/` package defining the interface; optionally port the `PostgresMemoryService`                          |
| **Value**  | 5 — opens the door to ADK-compatible memory; pi-go's custom observation store is fine but doesn't compose with ADK's `llmagent` |
| **Effort** | 1 — port 36 LOC interface, no implementation yet                                                                                |
| **Risk**   | 1 — interface-only, fully backward compatible                                                                                   |
| **Total**  | **+23**                                                                                                                         |

**Description.** pi-go's `internal/memory/` is a **custom claude-mem-style observation store**, not the ADK
`memory.Service` interface. The two don't compose: ADK's `llmagent` accepts a `memory.Service` and uses it for
cross-session context; pi-go's `Store` is observation-only.

**The interface to port (36 LOC, `memorytypes/types.go`):**

```go
type EntryWithID struct {
    ID        int
    Content   *genai.Content
    Author    string
    Timestamp time.Time
}
type MemoryService interface {
    AddSessionToMemory(ctx context.Context, s session.Session) error
    SearchMemory(ctx context.Context, req *memory.SearchRequest) (*memory.SearchResponse, error)
}
type ExtendedMemoryService interface {
    MemoryService
    SearchWithID(ctx context.Context, req *memory.SearchRequest) ([]EntryWithID, error)
    UpdateMemory(ctx context.Context, appName, userID, entryID string, newContent *genai.Content) error
    DeleteMemory(ctx context.Context, appName, userID, entryID string) error
}
```

**Why this is foundational.** The `ExtendedMemoryService` extension pattern is reusable independently of the Postgres
backend. pi-go's `SQLiteStore` could implement it too (with the `EntryWithID`-style update/delete methods). And the
agent's `save_to_memory` / `update_memory` / `delete_memory` tools (Feature 7) need this interface to be useful.

**Score breakdown.** Value=5 (foundational). Effort=1 (interface only). Risk=1. Total=+23.

---

### Feature 7: Memory Toolset (Read + Write)

| Aspect     | Detail                                                                                           |
|------------|--------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/tools/memory/toolset.go:1-391`                                                 |
| **Target** | Extend `internal/tools/mem_search.go` with `mem-save`, `mem-update`, `mem-delete`                |
| **Value**  | 4 — gives the agent agency over its memory; currently only `mem-search`/`mem-timeline`/`mem-get` |
| **Effort** | 2 — port 391 LOC; wire to pi-go's `memory.Store` (or new ADK `memory.Service`)                   |
| **Risk**   | 2 — adds write tools; needs careful permissioning / confirmation UX                              |
| **Total**  | **+16**                                                                                          |

**Description.** adk-utils-go's `tools/memory/toolset.go` exposes:

- `search_memory` — semantic search
- `save_to_memory` — save information for future recall
- `update_memory` — modify existing entries (extended interface only)
- `delete_memory` — remove entries (extended interface only)

**Notable patterns:**

- **Interface-based extension** (`toolset.go:55-59`): if MemoryService implements `ExtendedMemoryService`, automatically
  register `update_memory` + `delete_memory`. Opt-out with `DisableExtendedTools bool`.
- **`singleEntrySession` adapter** (`toolset.go:330-390`): wraps a single content + category into a one-event session so
  `AddSessionToMemory` (which expects a real session interface) accepts it.
- **Category tagging** (`toolset.go:208, 375-377`): saves `[category] content` as Text. Minimal metadata layer.
- **JSON schemas on args** (`toolset.go:126, 208, 253, 294`): `jsonschema` struct tags for proper tool description
  exposure.

**pi-go integration note.** pi-go's `internal/tools/mem_search.go:207-227` `MemoryTools(store memory.Store)` is the
existing factory. Adding 3 more tools means extending this factory. The write tools should be **gated by a config flag
** (`MEMORY_WRITE_TOOLS=true` or similar) for safety.

**Test coverage** is light in the source (no separate `_test.go`), so a port should include a new test file.

**Score breakdown.** Value=4. Effort=2. Risk=2. Total=+16.

---

### Feature 8: Redis Session Backend

| Aspect     | Detail                                                                                                              |
|------------|---------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/session/redis/session.go:1-864` (+ `session_test.go` + `livestate_test.go`)                       |
| **Target** | New `internal/session/redis/` package; `agent.Config.SessionService` already accepts the interface                  |
| **Value**  | 3 — needed for distributed/multi-host deployments; low for single-user local                                        |
| **Effort** | 3 — port 864 LOC; needs `github.com/redis/go-redis/v9` dep; needs connection-string-based factory in `agent.Config` |
| **Risk**   | 3 — adds external infra dep; needs opt-in wiring; can't be default                                                  |
| **Total**  | **+9**                                                                                                              |

**Description.** adk-utils-go's `session/redis/session.go` (864 LOC) is a complete `session.Service` impl backed by
Redis. Uses 5 key spaces:

- `session:{app}:{user}:{sid}` — JSON blob
- `events:{app}:{user}:{sid}` — RPUSH list of events
- `appstate:{app}` — HASH for app-scoped state
- `userstate:{app}:{user}` — HASH for user-scoped state
- `sessions:{app}:{user}` — SET index

**Notable patterns worth stealing (even if not porting Redis itself):**

- **Live state mirror after `AppendEvent`** (`session.go:316-413`): applies the event's StateDelta to both persisted and
  in-memory `redisState`. pi-go's `FileService` should verify this behavior.
- **Prefix-aware state handling** (`session.go:499-566`): explicit replication of the ADK canonical
  `ExtractStateDeltas` / `MergeStates` / `TrimTempDelta` internals. Keys with `temp:` prefix stay in the in-memory map
  but never reach Redis.
- **Per-tier TTL** (`RedisSessionServiceConfig{TTL, AppStateTTL, UserStateTTL}`) — separate TTLs for session, app state,
  user state. pi-go uses a single global TTL.
- **`loadFromRedis` with cached fallback** (`session.go:813-833`): if the cached slice is "filtered" (NumRecentEvents or
  After applied during Get), serve cache without re-fetching.
- **`var _ session.Service = (*RedisSessionService)(nil)`** compile-time check at `session.go:859-863` — pi-go does this
  at `store.go:1033`.

**Score breakdown.** Value=3. Effort=3. Risk=3. Total=+9.

---

### Feature 9: ModelRegistry Interface (refactor)

| Aspect     | Detail                                                                                                              |
|------------|---------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/plugin/contextguard/model_registry.go:1-20` (+ `model_registry_crush.go:1-62`)                    |
| **Target** | New `internal/provider/registry.go`; refactor `internal/provider/provider.go:96-100` to use it                      |
| **Value**  | 3 — unlocks the context-guard plugin (Feature 4) cleanly; reusable for any future context-aware logic               |
| **Effort** | 1 — 20 LOC interface + 50 LOC refactor; pi-go already has `contextWindowSizes` map, just needs an interface wrapper |
| **Risk**   | 1 — purely additive interface; existing call sites continue to work                                                 |
| **Total**  | **+13**                                                                                                             |

**Description.** adk-utils-go defines:

```go
type ModelRegistry interface {
    ContextWindow(modelID string) int
    DefaultMaxTokens(modelID string) int
}
```

Plus a built-in `CrushRegistry` that wraps `charm.land/catwalk/pkg/embedded` — model metadata compiled into the binary (
no network calls). Returns 128k/4096 defaults for unknown models.

**pi-go refactor.**

- `internal/provider/provider.go:96` has `var contextWindowSizes = mustLoadContextWindowSizes()` as a global map.
- `internal/provider/provider.go:100` has `func ContextWindowSize(modelName) int64`.
- Wrap these in a `ModelRegistry` interface that can be passed to the context-guard plugin.
- Default impl reads from `modeldata/context-windows.json` (already embedded).
- Optional `CrushRegistry` impl could be added as an alternative source.

**Score breakdown.** Value=3. Effort=1. Risk=1. Total=+13.

---

### Feature 10: OpenAI-Compatible Embedding Adapter

| Aspect     | Detail                                                                                                                     |
|------------|----------------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/memory/postgres/embedding.go:1-128`                                                                      |
| **Target** | New `internal/embed/openai/` package; useful when extending to Postgres+pgvector memory                                    |
| **Value**  | 3 — enables OpenAI-compatible embedding backends (Ollama, OpenRouter, vLLM, LM Studio) for any future semantic memory work |
| **Effort** | 1 — 128 LOC, plug-and-play                                                                                                 |
| **Risk**   | 1 — pure adapter, no side effects                                                                                          |
| **Total**  | **+13**                                                                                                                    |

**Description.** `OpenAICompatibleEmbedding` (128 LOC) is a `/v1/embeddings` HTTP client for any OpenAI-compatible
provider. Pluggable `HTTPClient` for testing.

**Public API:**

```go
type OpenAICompatibleEmbeddingConfig struct {
    BaseURL, APIKey, Model string
    Dimension              int  // auto-detected on first Embed()
    HTTPClient             *http.Client
}
func NewOpenAICompatibleEmbedding(cfg) *OpenAICompatibleEmbedding
func (e *OpenAICompatibleEmbedding) Embed(ctx, text) ([]float32, error)
func (e *OpenAICompatibleEmbedding) Dimension() int
```

**Notable patterns:**

- **Lazy dimension detection** (`embedding.go:106-108`): if `dim == 0`, the first successful Embed call sets it from the
  response length. Then probes during `NewPostgresMemoryService` (`memory.go:58-64`) if not set.
- **Lives behind an interface** (`memory.go:21-25`): any EmbeddingModel works (no hard dep on openai-go).

**pi-go integration note.** pi-go's `internal/palace/embedder.go` uses an embedded MiniLM-L6 ONNX model. The
OpenAI-compatible adapter is a useful *alternative* for users who want to use a hosted embedding model.

**Score breakdown.** Value=3. Effort=1. Risk=1. Total=+13.

---

### Feature 11: Anthropic Dual Thinking API (enabled + adaptive)

| Aspect     | Detail                                                                                                                |
|------------|-----------------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/genai/anthropic/anthropic.go:46-103, 172-214`                                                       |
| **Target** | `internal/provider/anthropic.go` Config + `NewAnthropic` signature                                                    |
| **Value**  | 4 — required for **Opus 4.5+** (rejects `enabled`); older models reject `adaptive`; currently pi-go only has one mode |
| **Effort** | 2 — add `ThinkingMode` field, add `WithJSONSet` for adaptive mode, update `NewAnthropic`                              |
| **Risk**   | 2 — could change behavior for users currently using extended thinking                                                 |
| **Total**  | **+16**                                                                                                               |

**Description.** adk-utils-go supports both:

- `ThinkingModeEnabled = "enabled"` — classic budget API (`ThinkingBudgetTokens`)
- `ThinkingModeAdaptive = "adaptive"` — effort API, Opus 4.5+ (`ThinkingEffort`)

The `resolveThinkingMode()` function infers from the field set rather than inspecting model name. Anthropic returns HTTP
400 if you use the wrong wire shape — this is a real production trap.

**Key technique.** `option.WithJSONSet("thinking.type", "adaptive")` and
`option.WithJSONSet("output_config.effort", ...)` because the typed SDK union lacks the `adaptive` variant. This is
the "escape the SDK" pattern.

**Public API delta:**

```go
type Config struct {
    // ... existing ...
    ThinkingMode         string  // "enabled" | "adaptive" | "" (auto-deduce)
    ThinkingBudgetTokens int     // classic API
    ThinkingEffort       string  // adaptive API (low/medium/high)
}
```

**Score breakdown.** Value=4 (required for Opus 4.5+). Effort=2. Risk=2. Total=+16.

---

### Feature 12: `StreamOptions.IncludeUsage` for OpenAI

| Aspect     | Detail                                                                        |
|------------|-------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/genai/openai/openai.go:151`                                 |
| **Target** | `internal/provider/openai_completions.go` and `openai_responses.go`           |
| **Value**  | 3 — without this, OpenAI never emits usage in streaming; breaks cost tracking |
| **Effort** | 1 — one-line change per provider                                              |
| **Risk**   | 1 — `IncludeUsage` is standard OpenAI API                                     |
| **Total**  | **+13**                                                                       |

**Description.** OpenAI's API requires `stream_options.include_usage = true` to emit token usage in streaming responses.
Without it, `usage` is `null` in the final chunk.

```go
params.StreamOptions.IncludeUsage = param.NewOpt(true)
```

**pi-go audit needed.** Check whether `internal/provider/openai_completions.go` and `openai_responses.go` already set
this. If not, this is a 1-line fix per file.

**Score breakdown.** Value=3 (cost-tracking enablement). Effort=1. Risk=1. Total=+13.

---

### Feature 13: `MarshalToolPayload` (null → `{}`)

| Aspect     | Detail                                                                                                  |
|------------|---------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/genai/common/payload.go:1-37`                                                         |
| **Target** | `internal/provider/common/` or inline in OpenAI/Mistral providers                                       |
| **Value**  | 2 — strict OpenAI-compatible parsers (Qwen on vLLM/llama.cpp) reject `null` where they expect an object |
| **Effort** | 1 — 37 LOC, drop-in helper                                                                              |
| **Risk**   | 1 — defensive normalization                                                                             |
| **Total**  | **+8**                                                                                                  |

**Description.** Single shared helper:

```go
func MarshalToolPayload(payload any) (json.RawMessage, error) {
    // turn nil/null/empty into "{}"
}
```

**Score breakdown.** Value=2. Effort=1. Risk=1. Total=+8.

---

### Feature 14: `reasoning_content` Extraction (OpenAI-compatible)

| Aspect     | Detail                                                                                                      |
|------------|-------------------------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/genai/openai/openai.go:166-172, 166-186` (~120 LOC)                                       |
| **Target** | `internal/provider/openai_completions.go`                                                                   |
| **Value**  | 2 — only relevant if pi-go talks to DeepSeek / Qwen / reasoning providers; pi-go's Gemini does CoT natively |
| **Effort** | 2 — ~120 LOC raw-JSON extraction                                                                            |
| **Risk**   | 2 — couples to provider-specific JSON shape                                                                 |
| **Total**  | **+6**                                                                                                      |

**Description.** OpenAI-compatible providers stream hidden chain-of-thought in `reasoning_content`, not in the typed
schema. adk-utils-go reads it from raw JSON via `delta.RawJSON()`. Reasoning Part goes before the answer Part to
preserve temporal order.

**Score breakdown.** Value=2. Effort=2. Risk=2. Total=+6.

---

### Feature 15: Tool `tool_call_id` 40-char Normalization (OpenAI)

| Aspect     | Detail                                                                                        |
|------------|-----------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/genai/openai/openai.go:36-37`                                               |
| **Target** | `internal/provider/openai_completions.go`                                                     |
| **Value**  | 2 — OpenAI caps `tool_call_id` at 40 chars; pi-go may generate longer IDs from ADK tool names |
| **Effort** | 1 — ~30 LOC SHA256 hashing                                                                    |
| **Risk**   | 1 — purely defensive                                                                          |
| **Total**  | **+8**                                                                                        |

**Description.** `maxToolCallIDLength = 40`. The adapter maps shorter IDs back to originals via SHA256 hashing.

**Score breakdown.** Value=2. Effort=1. Risk=1. Total=+8.

---

### Feature 16: 3-Tier Search Fallback (vector → text → recent)

| Aspect     | Detail                                                                              |
|------------|-------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/memory/postgres/memory.go:251-315`                                |
| **Target** | `internal/memory/search.go` (extend FTS5 path) or new `internal/memory/adk/`        |
| **Value**  | 4 — improves search quality dramatically; current FTS5-only misses semantic matches |
| **Effort** | 3 — needs an embedding backend (Feature 10) + new search path; substantial work     |
| **Risk**   | 2 — changes existing search behavior; needs fallback path verification              |
| **Total**  | **+15**                                                                             |

**Description.** If vector search returns nothing, fall back to PostgreSQL's `to_tsvector` + `ts_rank`. If still nothing
and query is empty, return most recent 10. This is well-considered and avoids the cold-start problem of pure-vector
search.

**pi-go integration.** pi-go's `internal/memory/search.go` uses FTS5 with a `searchLike` fallback. To adopt the 3-tier
pattern, would need:

1. A `searchByVector()` path (requires Feature 10 embedding adapter + vector index)
2. Keep FTS5 as the "text" middle tier
3. Keep `RecentObservations` as the "recent" bottom tier
4. Merge results with proper ranking

**Score breakdown.** Value=4. Effort=3. Risk=2. Total=+15.

---

### Feature 17: Filesystem Artifact `user:` Namespace

| Aspect     | Detail                                                                                       |
|------------|----------------------------------------------------------------------------------------------|
| **Source** | `tmp/adk-utils-go/artifact/filesystem/artifact.go:23, 61-78`                                 |
| **Target** | The filesystem artifact service from Feature 2                                               |
| **Value**  | 4 — UX feature: per-user docs that span sessions; novel pattern not present in any other OSS |
| **Effort** | 1 — already part of Feature 2                                                                |
| **Risk**   | 1 — covered by Feature 2                                                                     |
| **Total**  | **+4 (bundled with Feature 2)**                                                              |

**Description.** Filenames prefixed `user:` are stored under the session key `user`, making them accessible across all
sessions for `(appName, userID)`. `List` merges both session and user-scoped names.

**Why this is novel.** The ADK artifact service does not natively have this concept. adk-utils-go's `user:` prefix
convention is a clever layered extension.

**Score breakdown.** Value=4. Effort=1. Risk=1. Total=+4 — but this is part of Feature 2, not a separate task.

---

## Score Summary (Sorted by Total)

| Rank | Feature                                           | Value | Effort | Risk |  Total  | Recommended Phase      |
|-----:|---------------------------------------------------|:-----:|:------:|:----:|:-------:|------------------------|
|    1 | **`runner.PluginConfig` plumbing**                |   5   |   1    |  1   | **+23** | Phase 0 (prerequisite) |
|    1 | **Anthropic prompt caching**                      |   5   |   1    |  1   | **+23** | Phase 1                |
|    1 | **ADK `memory.Service` interface**                |   5   |   1    |  1   | **+23** | Phase 1                |
|    4 | Filesystem artifact service (+ `user:` namespace) |   5   |   2    |  2   | **+21** | Phase 1                |
|    5 | Context-window auto-compaction (context guard)    |   5   |   4    |  3   | **+18** | Phase 2                |
|    5 | Anthropic dual thinking API (Opus 4.5+ support)   |   4   |   2    |  2   | **+16** | Phase 2                |
|    5 | Memory toolset (save/update/delete)               |   4   |   2    |  2   | **+16** | Phase 2                |
|    8 | 3-tier search fallback (vector → text → recent)   |   4   |   3    |  2   | **+15** | Phase 3                |
|    9 | Langfuse plugin (architecture)                    |   4   |   4    |  3   | **+13** | Phase 3                |
|    9 | ModelRegistry interface (refactor)                |   3   |   1    |  1   | **+13** | Phase 1                |
|    9 | OpenAI-compatible embedding adapter               |   3   |   1    |  1   | **+13** | Phase 2                |
|    9 | `StreamOptions.IncludeUsage` for OpenAI           |   3   |   1    |  1   | **+13** | Phase 1                |
|   13 | Redis session backend                             |   3   |   3    |  3   | **+9**  | Phase 3 (or never)     |
|   14 | `MarshalToolPayload` (null → `{}`)                |   2   |   1    |  1   | **+8**  | Phase 2                |
|   14 | Tool `tool_call_id` 40-char normalization         |   2   |   1    |  1   | **+8**  | Phase 2                |
|   16 | `reasoning_content` extraction (OpenAI-compat)    |   2   |   2    |  2   | **+6**  | Phase 3 (conditional)  |
|   17 | Filesystem artifact `user:` namespace             |   4   |   1    |  1   | **+4**  | (bundled with #4)      |

---

## Recommended Phased Roadmap

### Phase 0: Foundational Plumbing (1 day, 2 LOC)

**Goal:** Unblock all plugin work.

1. **Add `PluginConfig *runner.PluginConfig` to `agent.Config`** (`internal/agent/agent.go:241-276`).
2. **Pass it to `runner.Config`** (`internal/agent/agent.go:361-365`).
3. Add a `var _ = (*runner.PluginConfig)(nil)` compile-time import in `agent.go` to ensure the import stays.
4. Build, test, no behavior change.

**Why first.** Gates Phase 2 (context guard) and Phase 3 (langfuse). Trivial risk.

### Phase 1: Quick Wins (1-2 weeks)

**Goal:** Capture the highest-leverage, lowest-effort features.

5. **Anthropic prompt caching** — copy `tmp/adk-utils-go/genai/anthropic/caching.go` (75 LOC + 196 LOC test) into
   `internal/provider/anthropic/caching.go`. Add one call site at the end of `buildMessageParams` in
   `internal/provider/anthropic.go`. **Cost win.**
6. **`StreamOptions.IncludeUsage`** for OpenAI — 1-line audit + fix in `internal/provider/openai_completions.go` and
   `openai_responses.go`.
7. **ADK `memory.Service` interface** — copy `tmp/adk-utils-go/memory/memorytypes/types.go` (36 LOC) into new
   `internal/memory/adk/service.go`. No implementation yet, just the interface. **Unblocks Feature 7.**
8. **ModelRegistry interface** — extract `contextWindowSizes` global into a `ModelRegistry` interface (
   `internal/provider/registry.go`, ~70 LOC). Wrap existing map as default impl. **Unblocks Feature 4 (context guard).**
9. **Filesystem artifact service** — copy `tmp/adk-utils-go/artifact/filesystem/artifact.go` (341 LOC + tests) into
   `internal/artifact/filesystem/`. Add `ArtifactService artifact.Service` to `agent.Config`. Wire to existing
   `tui/sidebar.go:ArtifactEntry` stub at `tui.go:1736-1746`. **Hard gap closed.**

### Phase 2: Major Features (3-4 weeks)

**Goal:** Land the most impactful plugins and tools.

10. **Context-window auto-compaction** — port the 5 files of `plugin/contextguard/` (~1,260 LOC + tests) into
    `internal/contextguard/`. Wire to `agent.Config.PluginConfig` (Feature 1). Build a real LLM-backed `Summarizer` to
    replace the placeholder at `internal/session/store.go:865`. **The most important feature in this report.**
11. **Memory toolset (read + write)** — extend `internal/tools/mem_search.go` with `mem-save`, `mem-update`,
    `mem-delete` (3 new function tools, ~150 LOC). Gate behind `MEMORY_WRITE_TOOLS` flag. **New agent capability.**
12. **Anthropic dual thinking API** — add `ThinkingMode` field, `WithJSONSet` for adaptive mode. Required for Opus
    4.5+. ~50 LOC change to `internal/provider/anthropic.go`.
13. **OpenAI-compatible embedding adapter** — port `tmp/adk-utils-go/memory/postgres/embedding.go` (128 LOC) into
    `internal/embed/openai/`. Prerequisite for Feature 14.
14. **Tool `tool_call_id` 40-char normalization** — ~30 LOC, defensive, low risk.
15. **`MarshalToolPayload` (null → `{}`)** — 37 LOC shared helper.

### Phase 3: Optional / Polish (1-2 weeks, optional)

**Goal:** Fill remaining gaps; evaluate based on user demand.

16. **Langfuse plugin** — port the 3 files of `plugin/langfuse/` (~700 LOC + tests) into
    `internal/observability/langfuse/`. The `enrichingExporter` + `enrichedSpan` architecture is worth lifting into
    `internal/otel/` even if Langfuse itself is not adopted.
17. **3-tier search fallback** — extend `internal/memory/search.go` with vector + text + recent tiers. Requires Feature
    13 (embedding adapter).
18. **Redis session backend** — port `tmp/adk-utils-go/session/redis/session.go` (864 LOC). Only if distributed
    deployment becomes a real need.
19. **`reasoning_content` extraction** — only if pi-go starts talking to DeepSeek/Qwen/reasoning providers.

### Phase N: Defer / Reject

- **`anthropic-client` binary** — `tmp/adk-utils-go/anthropic-client` is a compiled ELF binary without source. Not
  useful.
- **Full OpenAI-compatible adapter replacement** — pi-go already has 3 OpenAI backends; wholesale replacement is not
  worth it.
- **Full Redis + Postgres memory rewrite** — pi-go's SQLite observation store is more featureful (claude-mem paradigm)
  than adk-utils-go's `memory.Service`. The interface (Feature 6) is the bridge; the implementations should coexist.

---

## Notable things NOT in adk-utils-go that pi-go may want

For symmetry, these are not present anywhere in adk-utils-go and would be pi-go gaps or stand-out features:

- **No GitHub / Slack / filesystem tools** — adk-utils-go assumes tools are user-provided.
- **No MCP bridge** — but pi-go has one (`features/TOO/000-mcp-support/`).
- **No authorization/auth, RBAC** — adk-utils-go assumes single-tenant.
- **No code execution sandboxing** — but pi-go has `os.Root` (`internal/tools/sandbox.go`).
- **No plan-mode or sub-agent orchestration beyond ADK's built-ins** — but pi-go has rich subagent + SOP work.
- **No multi-tenancy** (beyond app/user prefixes).
- **No rate-limiting, token-budget enforcement, or cost controls** — adk-utils-go's only budget is the model registry.
- **No session fork / branch / replay** — but pi-go has branches (`store.go`).
- **No `memory.Service` automatic compaction** — context-guard compacts the *conversation*, not memory.
- **No streaming-aware UI hooks** — adk-utils-go is library-only, no TUI.
- **No example agent marketplaces** — adk-utils-go has 6 examples but no marketplace.

---

## Open Questions for the User

1. **License compatibility.** adk-utils-go is at `github.com/achetronic/adk-utils-go` — what license? Need to confirm
   before porting code. *(Check `tmp/adk-utils-go/LICENSE`.)*
2. **Codebase ownership.** Are we porting adk-utils-go code into pi-go (creating derivative work) or just lifting the
   techniques? Different implications for attribution.
3. **PluginConfig gating.** Should `runner.PluginConfig` be exposed as a public Config field, or wrapped behind a
   `Plugins []Plugin` abstraction?
4. **Memory Service implementation.** Port `PostgresMemoryService` (heavy), or just the `ExtendedMemoryService`
   interface + make `SQLiteStore` implement it (light)?
5. **Context guard default.** Should auto-compaction be on by default, or opt-in via a config flag?
6. **Anthropic caching default.** On by default, or opt-in?
7. **Langfuse vs OTLP-only.** Should pi-go ship a Langfuse plugin specifically, or just lift the `enrichingExporter`
   architecture into `internal/otel/` to support any LLM-aware backend?
8. **Redis priority.** Is multi-host session support actually needed, or is local-only the right scope?

---

## Appendix A: Package Map

### adk-utils-go (8 packages, ~6,200 LOC + ~7,000 LOC tests)

| Package                | Files         |                           LOC | Description                               |
|------------------------|---------------|------------------------------:|-------------------------------------------|
| `artifact/filesystem/` | 1 + 1 test    |                   341 + tests | Filesystem artifact service               |
| `genai/anthropic/`     | 2 + 13 tests  |            1033 + ~4500 tests | Native Anthropic client + caching + tests |
| `genai/openai/`        | 1 + 9 tests   |             899 + ~2800 tests | OpenAI-compatible client + tests          |
| `genai/common/`        | 1 + 1 test    |                    37 + tests | Shared `MarshalToolPayload` helper        |
| `memory/memorytypes/`  | 1             |                            36 | Interface definitions                     |
| `memory/postgres/`     | 2 + 2 tests   |                   744 + tests | pgvector + tsvector memory service        |
| `plugin/contextguard/` | 5 + 3 tests   |                  1260 + tests | Auto-compaction plugin                    |
| `plugin/langfuse/`     | 3 + 1 test    |                   700 + tests | Langfuse OTel plugin                      |
| `session/redis/`       | 1 + 2 tests   |                   864 + tests | Redis session service                     |
| `tools/memory/`        | 1             |                           391 | Memory CRUD toolset                       |
| `examples/`            | 6             |                           989 | 6 working demos                           |
| **Total**              | **~32 files** | **~7,200 LOC + ~9,800 tests** |                                           |

### pi-go internal/ packages touched in this analysis

| Package                   | Relevant files                                                                  | What's there                                                                  |
|---------------------------|---------------------------------------------------------------------------------|-------------------------------------------------------------------------------|
| `internal/agent/`         | `agent.go:241-369` (Config, buildRunner)                                        | Lacks `PluginConfig` and `ArtifactService` fields                             |
| `internal/artifact/`      | (does not exist)                                                                | n/a — would be new                                                            |
| `internal/contextguard/`  | (does not exist)                                                                | n/a — would be new                                                            |
| `internal/memory/`        | `store.go`, `search.go`, `compress.go`, `context.go`, `worker.go`, `privacy.go` | Custom observation store, not ADK `memory.Service`                            |
| `internal/observability/` | (does not exist)                                                                | n/a — would be new                                                            |
| `internal/otel/`          | `otel.go:1-253`                                                                 | Raw OTel SDK setup, no LLM-payload enrichment                                 |
| `internal/provider/`      | `anthropic.go`, `model_catalog.go`, `provider.go`                               | Has catalog, lacks prompt caching + dual thinking + `ModelRegistry` interface |
| `internal/session/`       | `store.go:1-1033+`                                                              | File-based `session.Service` impl, manual `Compact()` never auto-triggered    |
| `internal/tools/`         | `compactor.go`, `mem_search.go:1-227`                                           | Tool-output compactor; read-only memory tools                                 |

---

## Appendix B: Feature Score Matrix (All Features, Sorted by Score)

|  # | Feature                                          | Value | Effort | Risk | Total |     Phase     |
|---:|--------------------------------------------------|:-----:|:------:|:----:|:-----:|:-------------:|
|  1 | `runner.PluginConfig` plumbing                   |   5   |   1    |  1   |  +23  |       0       |
|  2 | Anthropic prompt caching                         |   5   |   1    |  1   |  +23  |       1       |
|  3 | ADK `memory.Service` interface                   |   5   |   1    |  1   |  +23  |       1       |
|  4 | Filesystem artifact service                      |   5   |   2    |  2   |  +21  |       1       |
|  5 | Context-window auto-compaction                   |   5   |   4    |  3   |  +18  |       2       |
|  6 | Anthropic dual thinking API                      |   4   |   2    |  2   |  +16  |       2       |
|  7 | Memory toolset (read + write)                    |   4   |   2    |  2   |  +16  |       2       |
|  8 | 3-tier search fallback                           |   4   |   3    |  2   |  +15  |       3       |
|  9 | Langfuse plugin (architecture)                   |   4   |   4    |  3   |  +13  |       3       |
| 10 | ModelRegistry interface                          |   3   |   1    |  1   |  +13  |       1       |
| 11 | OpenAI-compatible embedding adapter              |   3   |   1    |  1   |  +13  |       2       |
| 12 | `StreamOptions.IncludeUsage`                     |   3   |   1    |  1   |  +13  |       1       |
| 13 | Redis session backend                            |   3   |   3    |  3   |  +9   |       3       |
| 14 | `MarshalToolPayload`                             |   2   |   1    |  1   |  +8   |       2       |
| 15 | Tool `tool_call_id` 40-char                      |   2   |   1    |  1   |  +8   |       2       |
| 16 | `reasoning_content` extraction                   |   2   |   2    |  2   |  +6   |       3       |
| 17 | Filesystem `user:` namespace                     |   4   |   1    |  1   |  +4   |  1 (bundled)  |
| 18 | OpenAI schema lowercase + ensure properties      |   2   |   1    |  1   |  +8   | 2 (if needed) |
| 19 | `function/tool_choice` ModeAuto/Any/None mapping |   2   |   1    |  1   |  +8   | 2 (if needed) |
| 20 | `MaxOutputTokens` public Config field            |   1   |   1    |  1   |  +3   | 2 (cosmetic)  |

---

## Appendix C: Concrete File-by-File Port Plan

For the top-3 highest-priority features, here are the exact files to create/modify:

### Feature 1: PluginConfig Plumbing (Phase 0)

**Modify:** `internal/agent/agent.go`

```go
// Line 241-276: Add to Config struct
PluginConfig *runner.PluginConfig

// Line 361-365: Add to runner.Config
r, err := runner.New(runner.Config{
    AppName:        AppName,
    Agent:          llmAgent,
    SessionService: sessionSvc,
    PluginConfig:   cfg.PluginConfig,  // NEW
})
```

### Feature 2: Anthropic Prompt Caching (Phase 1)

**Create:** `internal/provider/anthropic/caching.go` (port 75 LOC)
**Create:** `internal/provider/anthropic/caching_test.go` (port 196 LOC)
**Modify:** `internal/provider/anthropic.go`

```go
// End of buildMessageParams:
if !m.disableCaching {
    applyCacheControl(&params)
}
```

### Feature 3: ADK `memory.Service` Interface (Phase 1)

**Create:** `internal/memory/adk/service.go` (port 36 LOC from `memorytypes/types.go`)
**Create:** `internal/memory/adk/service_test.go` (basic interface compliance test)

### Feature 4: Filesystem Artifact Service (Phase 1)

**Create:** `internal/artifact/filesystem/artifact.go` (port 341 LOC)
**Create:** `internal/artifact/filesystem/artifact_test.go` (port tests)
**Modify:** `internal/agent/agent.go` (add `ArtifactService artifact.Service` field)
**Modify:** `internal/tui/tui.go:1736-1746` (replace stub with real list call)

### Feature 5: Context-Window Auto-Compaction (Phase 2)

**Create:** `internal/contextguard/contextguard.go` (port 253 LOC)
**Create:** `internal/contextguard/compaction_strategy_threshold.go` (port 141 LOC)
**Create:** `internal/contextguard/compaction_strategy_sliding_window.go` (port 145 LOC)
**Create:** `internal/contextguard/compaction_utils.go` (port 795 LOC)
**Create:** `internal/contextguard/model_registry.go` (port 20 LOC)
**Create:** `internal/contextguard/model_registry_default.go` (50 LOC wrapper around pi-go's catalog)
**Create:** `internal/contextguard/*_test.go` (port 3 test files)
**Modify:** `internal/agent/agent.go` (add `PluginConfig` construction helper)
**Modify:** `internal/session/store.go:865` (replace `SimpleSummarizer` with real LLM-backed impl)

---

## Appendix D: Risks & Caveats

- **Anthropic thinking API churn** (`genai/anthropic/anthropic.go:46-103`): the dual API (`enabled` budget vs `adaptive`
  effort) reflects the live state of Anthropic as of late 2025/early 2026. Opus 4.5 rejects `enabled`; older models
  reject `adaptive`. Pin the SDK version and watch for breakage.
- **OpenAI non-standard fields** (`genai/openai/openai.go:166-186`): reading `reasoning_content` from raw JSON works but
  couples the adapter to whatever shape the provider emits. Different providers format differently.
- **Compression via LLM-call during compaction** is a per-turn latency hit (one extra LLM round-trip on every compaction
  pass). For tight interactive loops this matters.
- **`CrushRegistry` pulls `charm.land/catwalk` as a hard dep**: it's the same data source Crush CLI uses, so it's likely
  already fine for pi-go's ecosystem, but a different model DB could be plugged via the `ModelRegistry` interface.
- **Memory service swallows per-event errors** silently during `AddSessionToMemory` (
  `memory/postgres/memory.go:242-244`). A safer port would log them and expose metrics.
- **`Trim-Final-Assistant-Whitespace`** is Anthropic-specific.
- **Per-registry defaults** (`model_registry_crush.go:51, 61`): 128k/4k fallbacks may not suit all model families —
  interface lets you override.
- **Langfuse plugin dependencies:** the source uses `otlptracehttp` and `auth` for Basic Auth (Langfuse public:secret).
  pi-go's `internal/otel/otel.go` already uses `otlptracehttp`, so the dep is already in `go.mod`. No new deps needed
  for the Langfuse port.
- **License:** adk-utils-go is at `github.com/achetronic/adk-utils-go` — license file at `tmp/adk-utils-go/LICENSE`
  needs to be confirmed before any code is ported (the user should verify this is compatible with pi-go's MIT license
  before any port work begins).
