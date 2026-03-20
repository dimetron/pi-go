# Implementation Plan: Native claude-mem in pi-go

## Checklist

- [ ] Step 1: SQLite foundation (`internal/memory/db.go`, `types.go`)
- [ ] Step 2: Observation & summary store (`internal/memory/store.go`)
- [ ] Step 3: FTS5 search (`internal/memory/search.go`)
- [ ] Step 4: Privacy filtering (`internal/memory/privacy.go`)
- [ ] Step 5: Memory worker (`internal/memory/worker.go`)
- [ ] Step 6: AI compression subagent (`internal/memory/compress.go`, bundled agent)
- [ ] Step 7: Context injection (`internal/memory/context.go`)
- [ ] Step 8: Search tools (`internal/tools/mem_search.go`)
- [ ] Step 9: CLI wiring & config (`internal/cli/cli.go`, `internal/config/config.go`)
- [ ] Step 10: Integration testing & polish

---

## Step 1: SQLite Foundation

**Objective**: Set up SQLite database with migrations, WAL mode, and core schema.

**Implementation guidance**:
- Add `modernc.org/sqlite` to `go.mod`
- Create `internal/memory/types.go` with all Go types (`Session`, `Observation`, `ObservationType`, `SessionSummary`, `RawObservation`, `SearchQuery`, `SearchResult`, `SearchResultRow`)
- Create `internal/memory/db.go`:
  - `OpenDB(dbPath string) (*sql.DB, error)` — opens SQLite with WAL, foreign keys, mmap
  - `Migrate(db *sql.DB) error` — creates `schema_versions` table, runs pending migrations
  - Migration 1: `sessions`, `observations`, `session_summaries` tables with indexes
  - Migration 2: FTS5 virtual tables + sync triggers
  - `EnsureDir()` for `~/.pi-go/memory/`
- Register the `modernc.org/sqlite` driver via blank import

**Test requirements**:
- `db_test.go`: Open `:memory:` DB, verify migrations create all tables, verify WAL mode, verify idempotent migration runs
- Test FTS5 availability detection (graceful fallback if unavailable)

**Integration notes**: No integration with other packages yet. Self-contained foundation.

**Demo**: `go test ./internal/memory/ -run TestOpenDB` passes, creates schema in memory.

---

## Step 2: Observation & Summary Store

**Objective**: CRUD operations for sessions, observations, and summaries.

**Implementation guidance**:
- Create `internal/memory/store.go`:
  - `SQLiteStore` struct wrapping `*sql.DB`
  - `NewSQLiteStore(db *sql.DB) *SQLiteStore`
  - `CreateSession(ctx, *Session) error`
  - `CompleteSession(ctx, sessionID string) error`
  - `InsertObservation(ctx, *Observation) error` — marshal `SourceFiles` as JSON array
  - `GetObservations(ctx, ids []int64) ([]*Observation, error)` — batch fetch
  - `UpsertSummary(ctx, *SessionSummary) error` — INSERT OR REPLACE
  - `RecentObservations(ctx, project string, limit int) ([]*Observation, error)` — ordered by created_at DESC
  - `RecentSummaries(ctx, project string, limit int) ([]*SessionSummary, error)`
  - `Close() error`
- Use prepared statements for hot-path queries
- JSON marshal/unmarshal for `source_files` column

**Test requirements**:
- `store_test.go`: Insert session → insert observations → fetch by ID → verify fields
- Test batch get with mixed valid/invalid IDs
- Test recent observations ordering and limit
- Test upsert summary (insert then update)

**Integration notes**: Depends on Step 1 (db.go).

**Demo**: `go test ./internal/memory/ -run TestStore` — full CRUD cycle passes.

---

## Step 3: FTS5 Search

**Objective**: Full-text search over observations and summaries with progressive disclosure.

**Implementation guidance**:
- Create `internal/memory/search.go`:
  - `Search(ctx, SearchQuery) (*SearchResult, error)` — FTS5 MATCH query against `observations_fts`, returns compact rows (id, title, type, created_at, token estimates)
  - `Timeline(ctx, anchorID int64, before, after int) ([]*Observation, error)` — fetch N observations before/after anchor by created_at_epoch, same project
  - `SearchSummaries(ctx, query string, project string) ([]*SessionSummary, error)` — FTS5 on summaries
  - Fallback: if FTS5 is unavailable, use `LIKE '%query%'` on title+text columns
- Token cost estimation: `len(text) / 4` as rough heuristic

**Test requirements**:
- `search_test.go`: Insert 10 observations → search by keyword → verify matches ranked
- Test timeline: insert 10 sequential observations → timeline around #5 → verify window
- Test empty results
- Test project scoping (observations from other projects excluded)

**Integration notes**: Depends on Step 2 (store.go).

**Demo**: `go test ./internal/memory/ -run TestSearch` — search returns expected results.

---

## Step 4: Privacy Filtering

**Objective**: Strip `<private>...</private>` content before storage.

**Implementation guidance**:
- Create `internal/memory/privacy.go`:
  - `StripPrivate(s string) string` — regex replace `<private>[\s\S]*?</private>` with `[PRIVATE]`
  - `StripPrivateFromMap(m map[string]any) map[string]any` — deep-walk string values in map, apply StripPrivate
- Apply in worker before compression and before storage

**Test requirements**:
- `privacy_test.go`: Single tag, multiple tags, nested content, multiline, no tags (passthrough), empty string
- Test map stripping with nested structures

**Integration notes**: Used by Worker (Step 5).

**Demo**: `go test ./internal/memory/ -run TestPrivacy` passes.

---

## Step 5: Memory Worker

**Objective**: Background goroutine that drains observation channel and stores to SQLite.

**Implementation guidance**:
- Create `internal/memory/worker.go`:
  - `Worker` struct: `store Store`, `compressor Compressor` (interface), `obsChan chan RawObservation`, `done chan struct{}`
  - `NewWorker(store Store, compressor Compressor, bufSize int) *Worker`
  - `Enqueue(obs RawObservation)` — non-blocking send, drop + log if full
  - `Start(ctx context.Context)` — goroutine: range over obsChan, apply privacy filter, call compressor, store result
  - `Shutdown(ctx context.Context) error` — close channel, drain with timeout (5s), signal done
- `Compressor` interface: `CompressObservation(ctx, RawObservation) (*Observation, error)`
- Fallback on compression failure: create observation with `title=toolName`, `type=change`, `text=truncated raw output`

**Test requirements**:
- `worker_test.go`: Mock compressor, enqueue 5 observations, verify all stored
- Test channel-full drop behavior (buffer=1, enqueue 3 rapidly)
- Test shutdown drain (enqueue, then shutdown, verify processed)
- Test compression failure fallback

**Integration notes**: Depends on Steps 2, 4. Compressor interface allows mocking (Step 6 provides real impl).

**Demo**: `go test ./internal/memory/ -run TestWorker` — enqueue/process/store cycle works with mock compressor.

---

## Step 6: AI Compression Subagent

**Objective**: Subagent that compresses raw tool observations into structured observations.

**Implementation guidance**:
- Create `internal/subagent/bundled/memory-compressor.md`:
  ```yaml
  ---
  name: memory-compressor
  description: Compress tool observations into structured memory entries
  role: smol
  worktree: false
  tools: []
  timeout: 30
  ---
  ```
  System prompt instructs the agent to:
  - Accept JSON with tool_name, tool_input, tool_output
  - Return JSON: `{"title": "...", "type": "bugfix|feature|...", "text": "...", "source_files": [...]}`
  - Be concise (1-line title, 2-3 sentence text)
  - Extract file paths from tool input/output

- Create `internal/memory/compress.go`:
  - `SubagentCompressor` struct with `orchestrator *subagent.Orchestrator`
  - `NewSubagentCompressor(orch *subagent.Orchestrator) *SubagentCompressor`
  - `CompressObservation(ctx, RawObservation) (*Observation, error)`:
    - Build prompt from raw observation (truncate large outputs to 4KB)
    - Spawn `memory-compressor` agent via orchestrator
    - Collect text output, parse JSON response
    - Map to `*Observation` struct
  - `SummarizeSession(ctx, []*Observation) (*SessionSummary, error)`:
    - Build prompt listing all observations
    - Return structured summary

**Test requirements**:
- `compress_test.go`: Test prompt building (verify truncation of large outputs)
- Test JSON response parsing (valid, malformed, empty)
- Test timeout handling
- Integration test with real subagent (build tag `e2e`)

**Integration notes**: Depends on subagent orchestrator. Implements `Compressor` interface from Step 5.

**Demo**: `go test ./internal/memory/ -run TestCompress` — prompt building and response parsing work with mock.

---

## Step 7: Context Injection

**Objective**: Generate markdown context from recent observations for system instruction.

**Implementation guidance**:
- Create `internal/memory/context.go`:
  - `ContextGenerator` struct with `store Store`, `tokenBudget int`
  - `NewContextGenerator(store Store, tokenBudget int) *ContextGenerator`
  - `Generate(ctx context.Context, project string) (string, error)`:
    - Fetch recent summaries (last 3 sessions)
    - Fetch recent observations (last 72 hours, current project)
    - Build markdown table grouped by session
    - Track token count, stop when budget reached
    - Include footer: "Access past observations with mem-search, mem-timeline, mem-get tools."
  - `estimateTokens(s string) int` — `len(s) / 4`

**Test requirements**:
- `context_test.go`: Insert observations → generate context → verify markdown format
- Test token budget enforcement (large observation set truncated)
- Test empty project (no observations → empty string)
- Test cross-session grouping

**Integration notes**: Depends on Step 2 (store). Called from cli.go at startup (Step 9).

**Demo**: `go test ./internal/memory/ -run TestContext` — generates expected markdown.

---

## Step 8: Search Tools

**Objective**: Register `mem-search`, `mem-timeline`, `mem-get` as native tools.

**Implementation guidance**:
- Create `internal/tools/mem_search.go`:
  - Define input/output structs with JSON tags
  - `newMemSearchTool(store memory.Store) (tool.Tool, error)` — FTS5 search, return compact table
  - `newMemTimelineTool(store memory.Store) (tool.Tool, error)` — chronological context
  - `newMemGetTool(store memory.Store) (tool.Tool, error)` — batch fetch by IDs
  - Format output as markdown tables for readability
- Modify `internal/tools/registry.go`:
  - Add `MemoryStore` field to tool builder context (or pass via `Sandbox`)
  - Register 3 new tools in `CoreTools()` builder list
  - Skip registration if memory store is nil (memory disabled)

**Test requirements**:
- `mem_search_test.go`: Test tool schema generation (valid JSON Schema)
- Test search tool with mock store
- Test timeline tool edge cases (anchor not found)
- Test get tool with empty IDs list

**Integration notes**: Depends on Steps 2, 3 (store, search). Tools registered conditionally.

**Demo**: `go test ./internal/tools/ -run TestMemSearch` — tools produce expected output.

---

## Step 9: CLI Wiring & Config

**Objective**: Wire everything together in cli.go and add configuration.

**Implementation guidance**:
- Modify `internal/config/config.go`:
  - Add `MemoryConfig` struct:
    ```go
    type MemoryConfig struct {
        Enabled           bool     `json:"enabled"`
        DBPath            string   `json:"db_path,omitempty"`
        TokenBudget       int      `json:"token_budget,omitempty"`
        CompressionRole   string   `json:"compression_model_role,omitempty"`
        MaxPending        int      `json:"max_pending_observations,omitempty"`
        LookbackHours     int      `json:"context_lookback_hours,omitempty"`
        ExcludedTools     []string `json:"excluded_tools,omitempty"`
        ExcludedProjects  []string `json:"excluded_projects,omitempty"`
    }
    ```
  - Add `Memory *MemoryConfig` to `Config` struct
  - Defaults: enabled=true, db_path=`~/.pi-go/memory/claude-mem.db`, token_budget=8000, compression_role=smol, max_pending=100, lookback=72

- Modify `internal/cli/cli.go` in `runRoot()`:
  1. After config load, initialize memory system:
     ```go
     var memStore memory.Store
     var memWorker *memory.Worker
     if cfg.Memory == nil || cfg.Memory.Enabled {
         db, err := memory.OpenDB(memCfg.DBPath)
         memStore = memory.NewSQLiteStore(db)
         compressor := memory.NewSubagentCompressor(orchestrator)
         memWorker = memory.NewWorker(memStore, compressor, memCfg.MaxPending)
         memWorker.Start(ctx)
         defer memWorker.Shutdown(ctx)
     }
     ```
  2. Add memory callback to afterCBs:
     ```go
     if memWorker != nil {
         memoryCB := memory.BuildMemoryCallback(memWorker, sessionID, cwd)
         afterCBs = append(afterCBs, memoryCB)
     }
     ```
  3. Generate and inject context:
     ```go
     if memStore != nil {
         ctxGen := memory.NewContextGenerator(memStore, memCfg.TokenBudget)
         memContext, _ := ctxGen.Generate(ctx, cwd)
         if memContext != "" {
             instruction = instruction + "\n\n" + memContext
         }
     }
     ```
  4. Pass memStore to tool registry for search tools
  5. Create memory session record
  6. On shutdown, generate session summary

**Test requirements**:
- Verify memory system initializes with default config
- Verify memory system disabled when `enabled: false`
- Verify graceful behavior when DB path is invalid

**Integration notes**: This is the final wiring step. Touches cli.go, config.go, registry.go.

**Demo**: Run `pi` with memory enabled → use tools → `/exit` → restart → verify context appears in system instruction.

---

## Step 10: Integration Testing & Polish

**Objective**: End-to-end validation and quality polish.

**Implementation guidance**:
- Create `internal/memory/e2e_test.go` (build tag `e2e`):
  - Full cycle: open DB → create session → enqueue observations → compress → store → search → generate context
  - Verify context markdown format matches expected output
  - Verify search → timeline → get progressive disclosure workflow
- Performance test: insert 1000 observations, verify search < 100ms
- Add `--memory-off` CLI flag for quick disable
- Add `/memory` TUI slash command to show memory stats (observation count, DB size)
- Verify `go test ./...` passes (no regressions)
- Verify `go build ./cmd/pi` succeeds with new dependency

**Test requirements**:
- E2E test with real SQLite (file-based, t.TempDir)
- E2E test with mock compressor (avoid real LLM calls in CI)
- Regression: all existing tests still pass

**Integration notes**: Final validation step. May require minor fixes discovered during integration.

**Demo**: Full demo — start pi, write some code, exit, restart, see context injected, use `mem-search` to find past observations.
