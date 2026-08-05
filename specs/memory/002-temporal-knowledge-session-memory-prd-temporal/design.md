# Design — Temporal Knowledge Memory

## Current state (where we are today)

`internal/memory/` in pi-go already provides a working **observation/summary**
memory subsystem backed by pure-Go SQLite (`modernc.org/sqlite`).
[`research/existing-memory-package.md`](research/existing-memory-package.md)
has the full fact dump. The four facts that shape this design:

1. The existing `Observation` table already functions as the PRD's
   *Observation* construct (typed: decision/bugfix/feature/refactor/discovery/change).
2. `SessionSummary` already matches the PRD's *Summary*.
3. Schema migration is ordered and append-only; the next slot is `v3`.
4. Callers wire up via `memory.OpenDB(path) → memory.NewSQLiteStore(*sql.DB)`.
   The same `*sql.DB` is shared between calls; new code can attach to it
   without breaking callers.

What is missing — and what this design adds — is the **knowledge layer**:
`temporal supersession`, `provenance`, `confidence`, `validation state`,
`graph relationships` between facts/decisions/lessons/questions, and
full-text search over that knowledge.

## Desired end state

A package (`internal/memory/`) that exposes:

- A `KnowledgeStore` type with an interface (`Store.Knowledge()` accessor or
  split interface — see Components) covering CRUD on knowledge nodes, edges,
  events, sources, plus the MVP graph queries and FTS search.
- An `Extractor` interface and a deterministic `RuleExtractor` implementation
  that turns a slice of `Observation` rows into `Candidate` knowledge nodes.
- A `Consolidator` that takes a session ID, reads its observations, runs
  extraction, merges by content hash, upserts by `node_id`, links
  provenance, and returns a `ConsolidationReport`.
- A `Validation` workflow with explicit state transitions
  (`Candidate → PendingValidation → Confirmed → Deprecated → Rejected`),
  exposed as `KnowledgeStore.Confirm(nodeID, evidence)` etc.
- FTS5 virtual table over knowledge nodes' titles + text, plus sync
  triggers.
- Existing test patterns preserved: standard library `testing`, in-memory DB,
  `mockCompressor`-style helpers for new interfaces.

Knowledge is **never deleted**. Rejection only flips status.

## Architecture overview

```text
                       existing pipeline
   +------------+   Enqueue    +----------+   Insert   +-----------------+
   | ADK Agent  |  ----------> | Worker   |  --------> |  observations   |
   +------------+              +----------+            +-----------------+
                                                              |
                                                  .----------' (new link)
                                                  v
                              +------------------------------+
                              |  Consolidator                |
                              |  - Read observations         |
                              |  - Extract (Extractor)       |
                              |  - Dedup by content_hash     |
                              |  - Upsert by node_id         |
                              |  - Emit events               |
                              |  - Link provenance           |
                              +------------------------------+
                                                  |
                                                  v
                              +------------------------------+
                              | SQLite knowledge tables      |
                              |  knowledge_nodes             |
                              |  knowledge_edges             |
                              |  knowledge_events            |
                              |  knowledge_sources           |
                              |  knowledge_fts (v3)          |
                              +------------------------------+
                                         ^
                                         |  queries
                                     (caller)
                                         v
                              Why / Supersedes / Conflicts /
                              ByType / Search / RecentDecisions
```

## Components and interfaces (Go signatures)

```go
// In internal/memory/knowledge.go (new file)

// Ontology enum (subset of the PRD ontology; Observation and Summary already
// exist in types.go and are not redeclared here).
type KnowledgeKind string

const (
    KindFact       KnowledgeKind = "fact"
    KindDecision   KnowledgeKind = "decision"
    KindConstraint KnowledgeKind = "constraint"
    KindProcedure  KnowledgeKind = "procedure"
    KindLesson     KnowledgeKind = "lesson"
    KindQuestion   KnowledgeKind = "question"
)

// Source enum from PRD.
type SourceType string

const (
    SourceUser    SourceType = "user"
    SourceCode    SourceType = "code"
    SourceTest    SourceType = "test"
    SourceDocument SourceType = "document"
    SourceTool    SourceType = "tool"
    SourceAgent   SourceType = "agent"
    SourceSystem  SourceType = "system"
)

// Status from PRD validation state machine.
type KnowledgeStatus string

const (
    StatusCandidate         KnowledgeStatus = "candidate"
    StatusPendingValidation KnowledgeStatus = "pending_validation"
    StatusConfirmed         KnowledgeStatus = "confirmed"
    StatusDeprecated        KnowledgeStatus = "deprecated"
    StatusRejected          KnowledgeStatus = "rejected"
)

// Edge kinds from the PRD relationships section.
type EdgeKind string

const (
    EdgeBasedOn    EdgeKind = "based_on"
    EdgeSupersedes EdgeKind = "supersedes"
    EdgeIntroduces EdgeKind = "introduces"
    EdgeLearnedFrom EdgeKind = "learned_from"
    EdgePrevents   EdgeKind = "prevents"
    EdgeDerivedFrom EdgeKind = "derived_from"
    EdgeUses       EdgeKind = "uses"
    EdgeResolvedBy EdgeKind = "resolved_by"
    // Graph-internal edges emitted by the system, not authored by humans:
    EdgeMergedInto EdgeKind = "merged_into"  // dedup signal
)

// KnowledgeNode is one fact/decision/constraint/procedure/lesson/question.
type KnowledgeNode struct {
    ID            int64
    NodeID        string          // caller-supplied stable string
    Project       string
    Kind          KnowledgeKind
    Title         string
    Text          string
    Status        KnowledgeStatus
    Confidence    float64         // [0, 1]
    ContentHash   string          // sha256(canonical(title+text))
    SourceType    SourceType
    SourceWeight  int             // PRD's 0..100 priority at the time of write
    CreatedAt     time.Time
    ValidFrom     time.Time       // when this knowledge became true (defaults to CreatedAt)
    LastConfirmed time.Time
    SupersededAt  *time.Time
    ExpiresAt     *time.Time
    EventCount    int
}

type KnowledgeEdge struct {
    FromID   string
    ToID     string
    Kind     EdgeKind
    CreatedAt time.Time
}

type KnowledgeSource struct {
    NodeID         string  // -> knowledge_nodes.node_id
    ObservationID  int64   // -> observations.id
    Contribution   string  // short note: "supports", "contradicts"
    CreatedAt      time.Time
}

type KnowledgeEvent struct {
    ID         int64
    NodeID     string
    Kind       string  // "created", "confirmed", "superseded", "rejected", "expired", "merged_into", "confidence_updated"
    OldStatus  KnowledgeStatus
    NewStatus  KnowledgeStatus
    Payload    string  // free-form JSON for event-specific data
    CreatedAt  time.Time
}

// Candidate is the output of an Extractor (pre-persistence).
type Candidate struct {
    NodeID      string
    Kind        KnowledgeKind
    Title       string
    Text        string
    SourceType  SourceType
    SourceWeight int
    SourceObservationIDs []int64
}

// KnowledgeStore is the interface for the knowledge layer.
// Implemented by SQLiteKnowledgeStore (constructed via NewSQLiteKnowledgeStore(*sql.DB)).
type KnowledgeStore interface {
    // Node CRUD
    UpsertNode(ctx context.Context, n *KnowledgeNode, sources []KnowledgeSource) error
    GetNode(ctx context.Context, nodeID string) (*KnowledgeNode, error)
    GetNodeByContentHash(ctx context.Context, kind KnowledgeKind, hash string) (*KnowledgeNode, error)

    // Edges
    AddEdge(ctx context.Context, fromID, toID string, kind EdgeKind) error
    Edges(ctx context.Context, nodeID string, kind EdgeKind) ([]KnowledgeEdge, error)

    // Provenance
    AddSources(ctx context.Context, sources []KnowledgeSource) error
    SourcesFor(ctx context.Context, nodeID string) ([]KnowledgeSource, error)

    // Validation
    TransitionStatus(ctx context.Context, nodeID string, to KnowledgeStatus, payload string) error
    RecordConfidence(ctx context.Context, nodeID string, newConf float64, reason string) error

    // Graph queries (MVP)
    Why(ctx context.Context, nodeID string, maxDepth int) ([]KnowledgeEdge, error)            // ancestor trail
    Supersedes(ctx context.Context, nodeID string) ([]KnowledgeEdge, error)                  // supersession chain
    Conflicts(ctx context.Context, nodeID string) ([]*KnowledgeNode, error)                  // overlapping content
    ByType(ctx context.Context, kind KnowledgeKind, project string, limit int) ([]*KnowledgeNode, error)

    // FTS search (MVP)
    Search(ctx context.Context, query string, project string, limit int) ([]*KnowledgeNode, error)
    RecentDecisions(ctx context.Context, project string, since time.Time, limit int) ([]*KnowledgeNode, error)

    // Events (audit / training data)
    Events(ctx context.Context, nodeID string, limit int) ([]KnowledgeEvent, error)
}

// In internal/memory/extractor.go (new file)
type Extractor interface {
    Extract(ctx context.Context, obs []*Observation) ([]Candidate, error)
}

// RuleExtractor is the first implementation. Pure functions over Observations,
// no LLM. Deterministic. Fully covered by table-driven tests.
type RuleExtractor struct {
    // Future hooks live here, e.g. min support count, allow/deny filters.
    MinDecisionConfirmations int  // promoted Decision needs >=N confirmations of TypeDecision
}

func NewRuleExtractor() *RuleExtractor { ... }
func (r *RuleExtractor) Extract(ctx context.Context, obs []*Observation) ([]Candidate, error) { ... }

// In internal/memory/consolidator.go (new file)
type Consolidator struct {
    store        KnowledgeStore
    observations Store           // existing Store; reads Observations
    extractor    Extractor
}

func NewConsolidator(obs Store, ks KnowledgeStore, e Extractor) *Consolidator { ... }

type ConsolidationReport struct {
    SessionID        string
    CandidatesAdded  int
    CandidatesMerged int
    EdgesAdded       int
    EventsEmitted    int
    StartedAt        time.Time
    CompletedAt      time.Time
    Errors           []error
}

func (c *Consolidator) Consolidate(ctx context.Context, sessionID string) (*ConsolidationReport, error)
```

### How callers wire it up

```go
db, err := memory.OpenDB(dbPath)            // existing
obsStore := memory.NewSQLiteStore(db)       // existing
ks       := memory.NewSQLiteKnowledgeStore(db) // NEW
ext      := memory.NewRuleExtractor()      // NEW (deterministic first cut)
cons     := memory.NewConsolidator(obsStore, ks, ext)

report, err := cons.Consolidate(ctx, sessionID)
// Caller wires end-of-session trigger; periodic/ask-user triggers stay out of MVP.
```

## Data model (SQLite schema, migration `v3`)

Tables created in `internal/memory/db.go`'s `migrations[2]` (index 2 = version
3). All `*_epoch` columns are Unix seconds; `*_at` is RFC3339 — matches the
existing convention.

```sql
CREATE TABLE knowledge_nodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id         TEXT UNIQUE NOT NULL,
    project         TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK(kind IN ('fact','decision','constraint','procedure','lesson','question')),
    title           TEXT NOT NULL,
    text            TEXT NOT NULL,
    status          TEXT NOT NULL CHECK(status IN ('candidate','pending_validation','confirmed','deprecated','rejected')),
    confidence      REAL NOT NULL DEFAULT 0.5,
    content_hash    TEXT NOT NULL,
    source_type     TEXT NOT NULL,
    source_weight   INTEGER NOT NULL,
    created_at      TEXT NOT NULL,
    created_at_epoch INTEGER NOT NULL,
    valid_from      TEXT NOT NULL,
    valid_from_epoch INTEGER NOT NULL,
    last_confirmed  TEXT,
    last_confirmed_epoch INTEGER,
    superseded_at   TEXT,
    superseded_at_epoch INTEGER,
    expires_at      TEXT,
    expires_at_epoch INTEGER
);
CREATE INDEX idx_kn_project_kind  ON knowledge_nodes(project, kind);
CREATE INDEX idx_kn_project_status ON knowledge_nodes(project, status);
CREATE INDEX idx_kn_created       ON knowledge_nodes(created_at_epoch DESC);
CREATE INDEX idx_kn_hash          ON knowledge_nodes(kind, content_hash);
CREATE INDEX idx_kn_status_kind   ON knowledge_nodes(status, kind);

CREATE TABLE knowledge_edges (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id         TEXT NOT NULL,
    to_id           TEXT NOT NULL,
    kind            TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    created_at_epoch INTEGER NOT NULL,
    UNIQUE(from_id, to_id, kind)
);
CREATE INDEX idx_ke_from_kind ON knowledge_edges(from_id, kind);
CREATE INDEX idx_ke_to_kind   ON knowledge_edges(to_id, kind);

CREATE TABLE knowledge_sources (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id         TEXT NOT NULL,
    observation_id  INTEGER NOT NULL,
    contribution    TEXT NOT NULL DEFAULT 'supports',
    created_at      TEXT NOT NULL,
    FOREIGN KEY(node_id) REFERENCES knowledge_nodes(node_id) ON DELETE RESTRICT,
    FOREIGN KEY(observation_id) REFERENCES observations(id) ON DELETE CASCADE
);
CREATE INDEX idx_ks_node        ON knowledge_sources(node_id);
CREATE INDEX idx_ks_observation ON knowledge_sources(observation_id);

CREATE TABLE knowledge_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id         TEXT NOT NULL,
    kind            TEXT NOT NULL,   -- 'created','confirmed','rejected','superseded','merged_into','confidence_updated','transitioned'
    old_status      TEXT,
    new_status      TEXT,
    payload         TEXT,           -- JSON
    created_at      TEXT NOT NULL,
    created_at_epoch INTEGER NOT NULL
);
CREATE INDEX idx_kev_node    ON knowledge_events(node_id);
CREATE INDEX idx_kev_kind_at ON knowledge_events(kind, created_at_epoch DESC);

CREATE VIRTUAL TABLE knowledge_fts USING fts5(
    title, text,
    content='knowledge_nodes', content_rowid='id'
);
-- _ai / _au / _ad triggers mirror those in v2.
```

## Patterns to follow (from existing code)

- **Constructor pairing**: `OpenDB` returns `*sql.DB`; types like
  `NewSQLiteStore(*sql.DB)` wrap it. We add `NewSQLiteKnowledgeStore(*sql.DB)`.
- **Error wrapping**: `fmt.Errorf("memory: %s: %w", verb, err)` everywhere.
- **Time columns**: dual RFC3339 + Unix-epoch for indexable ordering.
- **Migration versioning**: append a single SQL string to the `migrations`
  slice; no deletions, no edits of older entries.
- **FTS5 sync triggers**: copy the `_ai/_au/_ad` pattern from v2.
- **In-memory tests**: `OpenDB(":memory:")`, `t.TempDir()` for file paths,
  `mockCompressor`-style helpers for new interfaces.
- **Pure Go only**: `modernc.org/sqlite` already in tree; no cgo.

## Design decisions worth calling out

1. **Why extend in place rather than sibling package.** Existing
   `Observation` and `SessionSummary` already serve the PRD's lower-ontology
   role; the consolidation pipeline *reads observations* and produces
   knowledge nodes. Splitting packages adds a foreign-key hop and a redundant
   migration story for the same DB.
2. **Hybrid identity.** Caller-supplied `node_id` as primary key (stable
   across edits, used by edges); `content_hash` only as a dedup signal that
   emits a `merged_into` event so history is preserved.
3. **Single edges table, not adjacency-per-kind.** All 8 PRD edge kinds go
   into `knowledge_edges(kind)`; indexes on `(from_id, kind)` and
   `(to_id, kind)` keep recursive CTE queries fast. Avoids DDL churn.
4. **Reject ≠ delete.** Rejected nodes stay in the table with status
   `rejected`. They become evaluation/training data later (PRD principle 3).
5. **Status transitions are explicit.** `TransitionStatus` validates the FSM
   (`Candidate → PendingValidation → Confirmed → Deprecated → Rejected`) and
   emits a `KnowledgeEvent`. Callers cannot bypass it.
6. **No LLM in MVP.** `RuleExtractor` first; `LLMExtractor` deferred.

## Error handling strategy

- All public methods return `error`.
- Errors wrap with `fmt.Errorf("memory: <context>: %w", err)`.
- Sentinel errors for caller branching: `ErrInvalidTransition`,
  `ErrNodeNotFound`, `ErrDuplicateNode`. Defined in `knowledge.go`.
- Consolidator collects per-candidate errors into `Report.Errors`; it does
  not abort the whole batch (per PRD: knowledge is best-effort, never
  blocking the host).

## Acceptance criteria

### Extend the schema

- Given an empty DB, when `OpenDB(":memory:")` is called, then tables
  `knowledge_nodes`, `knowledge_edges`, `knowledge_sources`,
  `knowledge_events`, `knowledge_fts` exist and `schema_versions.version = 3`.
- Given an existing DB at v2, when `OpenDB` is called, then migrations apply
  idempotently and `version` becomes 3 without altering existing tables.

### Identity and dedup

- Given a candidate with `content_hash = H` and `kind = K`, when the same
  hash+kind already exists, then no second node is created and a
  `merged_into` edge is recorded against the existing node, the existing
  node receives a `KnowledgeEvent{kind: "merged_into"}`, and the report
  reports `CandidatesMerged++`.
- Given two candidates with different `node_id` but same hash+kind, behavior
  is the same as above.

### Temporal supersession

- Given a `Decision` node A that exists, when a new candidate supersedes A
  and is upserted, then A's status becomes `superseded`, A's
  `superseded_at` is set, and an edge `(new, old, supersedes)` is added;
  querying A returns the superseded node with a populated `SupersededAt`.
- A direct delete of a node is impossible: store has no `DeleteNode`.

### Validation FSM

- Given a `Candidate` node, calling `TransitionStatus(id, pending_validation)`
  emits `KnowledgeEvent{kind: "transitioned", old: candidate, new: pending_validation}`.
- Calling `TransitionStatus(id, archived)` (invalid) returns
  `ErrInvalidTransition` and emits no event.

### Graph queries

- `Why(root, 3)` returns ancestors up to depth 3 via `based_on`,
  `supersedes`, `resolved_by` only.
- `Supersedes(id)` returns a linear chain via recursive CTE on
  `knowledge_edges`.
- `Conflicts(id)` returns active nodes that share a `content_hash` prefix
  *or* have overlapping `valid_from` windows with the opposite claim.
- `ByType(decision, project, 10)` returns at most 10 active decision nodes
  ordered by `last_confirmed DESC`.
- `RecentDecisions(project, since, 10)` returns confirmed decision nodes
  created after `since`.

### FTS

- `Search("sqlite")` ranks knowledge nodes by FTS5 `rank` and joins back to
  `knowledge_nodes` for metadata.
- When a node is updated, the FTS index reflects the change (trigger
  verification).

### Consolidator

- Given a session with 3 `TypeDecision` observations and no prior knowledge,
  when `Consolidate(ctx, sessionID)` is called, then a new `Decision` node
  is created with `status="candidate"`, three `knowledge_sources` rows link
  the observation IDs, and the report's `CandidatesAdded = 1`.
- Given an extractor that errors on one observation, the report continues
  with the others and `Report.Errors` contains one entry.

## Testing strategy

- Unit tests use `OpenDB(":memory:")` per the existing pattern.
- `RuleExtractor` tests are 100% table-driven — no clock, no randomness.
- `SQLiteKnowledgeStore` tests cover: upsert idempotency, dedup-on-hash,
  supersession transitions, FSM rejection, FTS ranking, `Why`/`Supersedes`/
  `Conflicts` accuracy on small seeded graphs.
- `Consolidator` tests use an in-memory mock `Extractor` returning
  fixed candidates to assert pipeline wiring (no observation rows required
  for many tests).
- E2E (`//go:build e2e`): a single `TestConsolidate_RealObservations` writes
  three observations, runs the consolidator, verifies a decision node
  appeared.
- All test names follow existing convention: `TestSubject_Behavior[_Condition]`.
- Coverage target: `>=80%` on the new files (matches the repo's existing
  posture).

## Migration & rollout

- Migration `v3` is additive only; no backfill required. Existing DBs
  upgrade on next `OpenDB` call.
- The new store is *opt-in* for existing callers — adding a method to the
  `Store` interface would break the existing mockStore in tests; instead
  expose `func (s *SQLiteStore) Knowledge() *SQLiteKnowledgeStore` returning
  the knowledge accessor. See "Open questions for the plan" below.

## Out of scope for this slice set (explicit non-goals)

- LLM extractor (planned, deferred)
- Multi-agent shared memory
- Automatic PR summaries / ADR generation
- Knowledge aging/decay scoring
- Reinforcement learning from corrected memories
- Session-start summary retrieval helper
- Graph algorithms (PageRank, community detection)
- Multi-hop ancestor queries beyond depth limit
- Knowledge node *editing* (history is preserved via supersession, not edit)
- CLI surface (a future `cmd/pi/memory_knowledge_*.go` can land later)

## Open questions for the plan (not blockers, just choices to confirm)

1. Should `SQLiteStore` gain a `Knowledge() *SQLiteKnowledgeStore` accessor
   (preserves existing `Store` interface and `mockStore`), or should we
   extend the interface? **Recommendation: accessor.** Less churn, mockStore
   tests don't need updating.
2. Should `Observation` rows from the *same session* automatically
   contribute `source_observation_ids` to derived knowledge? **Recommendation:
   yes, via `Consolidator` reading observations and forwarding IDs to
   `AddSources`.** This is the natural way to express "this knowledge came
   from these tool calls."
3. Default `source_weight` mapping for rule extractor — likely
   `TypeDecision → 50` (Agent), `TypeBugfix/Feature/Refactor → 70` (Tool +
   context), `TypeDiscovery → 60`. **Recommendation: explicit table in
   test, not magic numbers.**
