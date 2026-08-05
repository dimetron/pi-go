# Outline — Temporal Knowledge Memory

## Slice order (vertical; each slice compiles + passes tests on its own)

| #  | Slice                                                                                                                                                                                                    | Files touched                                                                                                                           | Verifies via                              |
|----|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------|
| 1  | Schema migration v3 + types in `internal/memory/knowledge.go` (types only, no methods).                                                                                                                  | `db.go` (append migration), `knowledge.go` (types + enums + sentinel errors), `db_test.go` (new tables present)                         | `go test ./internal/memory/...`           |
| 2  | `KnowledgeStore` interface + `SQLiteKnowledgeStore` CRUD: `UpsertNode`, `GetNode`, `GetNodeByContentHash`, `AddSources`, `SourcesFor`, `Events`.                                                         | `knowledge_store.go`, `knowledge_store_test.go`                                                                                         | `go test ./internal/memory/...`           |
| 3  | Validation FSM + edges: `TransitionStatus` (with FSM guard), `RecordConfidence`, `AddEdge`, `Edges`.                                                                                                     | `knowledge_store.go` (extend), `knowledge_store_test.go` (extend)                                                                       | `go test ./internal/memory/...`           |
| 4  | Graph queries: `Why` (recursive CTE), `Supersedes`, `Conflicts`, `ByType`, `Events`. Recursive CTE works in `modernc.org/sqlite` 1.56.                                                                   | `knowledge_store.go` (extend), `knowledge_store_test.go` (seeded mini-graph)                                                            | `go test ./internal/memory/...`           |
| 5  | FTS5: `Search`, `RecentDecisions`, sync triggers.                                                                                                                                                        | `db.go` (extend migration to add virtual table — or roll into v3 in slice 1), `knowledge_store.go` (queries), `knowledge_store_test.go` | `go test ./internal/memory/...`           |
| 6  | `Extractor` interface + `RuleExtractor` (deterministic, table-driven weight map, returns `Candidate`s from `Observation`s).                                                                              | `extractor.go`, `extractor_test.go`                                                                                                     | `go test ./internal/memory/...`           |
| 7  | `Consolidator` (real one, not interface): reads observations for a session, runs extractor, dedup by hash, upsert by `node_id`, link provenance, emit events. Returns `ConsolidationReport`.             | `consolidator.go`, `consolidator_test.go`                                                                                               | `go test ./internal/memory/...`           |
| 8  | `SQLiteStore.Knowledge()` accessor exposing `*SQLiteKnowledgeStore`. Wire-up helper `DefaultConsolidator(obsStore)` that returns a consolidator wired with `RuleExtractor` + `SQLiteKnowledgeStore(db)`. | `store.go` (one-line method), `consolidator.go` (helper), `store_test.go` (sanity)                                                      | `go test ./internal/memory/...`           |
| 9  | End-to-end test (`//go:build e2e`): write observations → consolidate → query graph.                                                                                                                      | `e2e_test.go` (extend)                                                                                                                  | `go test -tags e2e ./internal/memory/...` |
| 10 | Cleanup: `golangci-lint run ./internal/memory/...`, `go vet`, ensure `go test ./...` still passes repo-wide (no caller was touched).                                                                     | —                                                                                                                                       | `make test && make vet && make lint`      |

## Key types / interfaces (header view)

```go
// types
type KnowledgeKind, SourceType, KnowledgeStatus, EdgeKind   // string enums
type KnowledgeNode, KnowledgeEdge, KnowledgeSource, KnowledgeEvent, Candidate
var ErrInvalidTransition, ErrNodeNotFound, ErrDuplicateNode

// store
type KnowledgeStore interface { ... }       // see design.md for full surface
type SQLiteKnowledgeStore struct { db *sql.DB }
func NewSQLiteKnowledgeStore(db *sql.DB) *SQLiteKnowledgeStore
func (s *SQLiteStore) Knowledge() *SQLiteKnowledgeStore

// extraction
type Extractor interface { Extract(ctx, []*Observation) ([]Candidate, error) }
type RuleExtractor struct { MinDecisionConfirmations int }
func NewRuleExtractor() *RuleExtractor

// consolidation
type Consolidator struct { ... }
func NewConsolidator(Store, KnowledgeStore, Extractor) *Consolidator
func (c *Consolidator) Consolidate(ctx, sessionID string) (*ConsolidationReport, error)
func DefaultConsolidator(obsStore Store) (*Consolidator, error)   // wires rule + sqlite-ks

type ConsolidationReport struct {
    SessionID string
    CandidatesAdded, CandidatesMerged, EdgesAdded, EventsEmitted int
    StartedAt, CompletedAt time.Time
    Errors []error
}
```

## DB surface

- `knowledge_nodes (node_id PK)`, `knowledge_edges (from_id, to_id, kind)`,
  `knowledge_sources (node_id, observation_id)`, `knowledge_events (node_id, kind)`, `knowledge_fts`.
- Indexes: `(from_id, kind)`, `(to_id, kind)`, `(status, kind)`, `(project, kind)`, `(kind, content_hash)`,
  `(kind, created_at_epoch DESC)`.

## Patterns carried forward from existing code

- Error wrap: `fmt.Errorf("memory: %s: %w", verb, err)`.
- Time columns: RFC3339 + Unix-epoch.
- Migrations: append a SQL string to `migrations[2]` (version 3). All FTS triggers + virtual table go in the same v3
  chunk (single schema bump).
- Tests: standard library, `OpenDB(":memory:")`, table-driven.
- No new external deps.

## Verification gate (per slice and final)

- Per slice: `go test ./internal/memory/...`
- Final: `make test && make vet && make lint`
- E2E (slice 9 only): `go test -tags e2e ./internal/memory/...`
