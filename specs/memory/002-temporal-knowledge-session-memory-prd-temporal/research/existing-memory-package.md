# Research: Existing `internal/memory` package

> Objective facts about the existing pi-go memory subsystem that the new
> temporal knowledge layer will live alongside. No design proposals here.

## Location and surface area

- Package path: `internal/memory/`
- Files: `db.go`, `store.go`, `search.go`, `context.go`, `compress.go`,
  `privacy.go`, `worker.go`, `types.go` + matching `_test.go` files plus
  `e2e_test.go`.
- Consumers (callers): `internal/cli/cli.go:718`, `internal/cli/interactive.go:651`,
  `internal/cli/memory_recent.go:70` — all use the same pattern
  `memory.OpenDB(path) → memory.NewSQLiteStore(*sql.DB)`.

## Types (`types.go`)

- `ObservationType` enum: `decision | bugfix | feature | refactor | discovery | change`.
- `Session { ID, SessionID, Project, UserPrompt, StartedAt, CompletedAt, Status }`
  — `Status` is `"active" | "completed" | "failed"`.
- `Observation { ID, SessionID, Project, Title, Type, Text, SourceFiles ([]string),
  ToolName, PromptNumber, DiscoveryTokens, CreatedAt }`.
- `SessionSummary { ID, SessionID, Project, Request, Investigated, Learned,
  Completed, NextSteps, DiscoveryTokens, CreatedAt }`.
- `RawObservation { SessionID, Project, ToolName, ToolInput, ToolOutput, Timestamp }`.
- `SearchQuery { Query, Project, Type, Limit, Offset }`.
- `SearchResultRow { ID, Title, Type, CreatedAt, ReadCost, WorkCost }`,
  `SearchResult { Rows, Total }`.

## Storage (`db.go`, `store.go`)

- SQLite via **pure-Go** driver `modernc.org/sqlite` (no cgo).
- Database opened with `OpenDB(dbPath string) (*sql.DB, error)`. Supports
  `":memory:"`. Auto-creates parent directory for non-memory paths.
- Pragmas on open: `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`,
  `mmap_size=268435456`.
- Migration framework: ordered slice `migrations []string`. Applied in a
  transaction; each version recorded in `schema_versions(version, applied_at)`.
- **Existing migrations:**
    - `v1`: `sessions`, `observations`, `session_summaries` + indexes.
    - `v2`: FTS5 virtual tables `observations_fts`, `session_summaries_fts` with
      sync triggers (`_ai`, `_au`, `_ad`).
- `HasFTS5(db)` helper checks `SELECT 1 FROM observations_fts LIMIT 0`.
- `Store` interface + `SQLiteStore` implementation. Methods:
  `CreateSession`, `CompleteSession`, `InsertObservation`, `GetObservations`,
  `RecentObservations`, `UpsertSummary`, `RecentSummaries`, `Search`,
  `Timeline`, `Close`.
- Naming convention in errors: `memory: <verb noun>: %w`.
- Time columns are stored as **both** RFC3339 strings *and* `*_epoch` Unix
  integers for indexable ordering.

## Search (`search.go`)

- `Search(ctx, SearchQuery) (*SearchResult, error)` with FTS5 path and LIKE
  fallback.
- `sanitizeFTS5Query` exists; FTS5 query syntax is escaped.
- Results ranked by FTS5 `rank`, joined back to base table for metadata.

## Worker pipeline (`worker.go`)

- `Worker` is a goroutine draining `chan RawObservation`.
- `Enqueue` is non-blocking; drops + logs when full.
- `processOne`: privacy-filter input/output (`StripPrivateFromMap`) →
  `Compressor.CompressObservation` → on failure produces a `fallbackObservation`
  (type=`TypeChange`, title="<tool> (uncompressed)") → `store.InsertObservation`
  → fires `AfterStoreHook`s (e.g. palace bridge).
- `Compressor` interface: `CompressObservation(ctx, RawObservation) (*Observation, error)`.
- `NewWorker(store, compressor, bufSize int)` (default bufSize=100).
- `OnAfterStore(hook AfterStoreHook)` registers hooks **before** `Start`.
- `BuildMemoryCallback` and `BuildAfterToolCallback` construct ADK
  `AfterToolCallback`s that enqueue raw tool events.

## Existing ontology overlap with the new PRD

| PRD ontology kind                                              | Existing construct    | Notes                                                                                                                           |
|----------------------------------------------------------------|-----------------------|---------------------------------------------------------------------------------------------------------------------------------|
| `Observation`                                                  | `Observation` (typed) | Same word but PRD's observation is "temporary, becomes Fact/Lesson/Decision later". Our `Observation` already serves this role. |
| `Summary`                                                      | `SessionSummary`      | Direct match — covers Request/Investigated/Learned/Completed/NextSteps.                                                         |
| `Fact / Decision / Constraint / Procedure / Lesson / Question` | *none*                | New types only — these are what we are adding.                                                                                  |

The existing package does **not** have: temporal supersession, provenance
records, confidence, knowledge edges, validation state machine, semantic
dedup. These are all genuinely new.

## Build & test conventions

- Module: `github.com/dimetron/pi-go`, Go `1.26.5`.
- `make test` → `go test ./...` (alias `make test-unit`).
- `make vet` → `go vet ./...`.
- `make lint` → `golangci-lint run ./...`.
- Tests use standard `testing` package; in-memory DB via `OpenDB(":memory:")`;
  `t.TempDir()` for file paths; `mockCompressor`/`mockStore` defined in
  `worker_test.go` for interface-driven tests; `e2e_test.go` exists with
  `//go:build e2e` style gating (need to verify the exact build tag if we
  need e2e coverage — file is present, almost certainly tagged).
- Error wrapping style: `fmt.Errorf("memory: %s: %w", ...)`.

## Constraints / dependencies discovered

- Pure-Go SQLite only (modernc.org/sqlite). Supports FTS5 and recursive CTEs.
- No external services in package.
- No reflection-heavy dependencies (would matter for LLM extractors later).
- Schema migrations are append-only by convention; new tables = new version
  entry in the `migrations` slice.
