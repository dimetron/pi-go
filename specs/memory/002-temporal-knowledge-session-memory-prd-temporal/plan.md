# Implementation Plan — Temporal Knowledge Memory

> Vertical slices. Each slice ends in a passing build + `go test ./internal/memory/...`
> unless noted. No slice requires any later slice to be mergeable.
>
> Repo gate: `make test` (= `go test ./...`), `make vet`, `make lint`.

---

## Slice 1 — Schema migration v3 + types (no behavior yet)

- [ ] **Add migration `v3`** to `internal/memory/db.go`'s `migrations` slice:
  `knowledge_nodes`, `knowledge_edges`, `knowledge_sources`,
  `knowledge_events` + `knowledge_fts` (virtual table) + three sync
  triggers (`_ai`, `_au`, `_ad`). All indexes from `design.md` data-model.
  All FKs `ON DELETE RESTRICT` for `knowledge_*` tables so knowledge
  is undeletable even if a caller tries to cascade through.
- [ ] **Create `internal/memory/knowledge.go`** with type definitions only:
  - Constants: `KnowledgeKind` (`fact|decision|constraint|procedure|lesson|question`),
  `SourceType` (`user|code|test|document|tool|agent|system`),
  `KnowledgeStatus` (`candidate|pending_validation|confirmed|deprecated|rejected`),
  `EdgeKind` (8 PRD kinds + `merged_into`).
  - Struct types: `KnowledgeNode`, `KnowledgeEdge`, `KnowledgeSource`,
  `KnowledgeEvent`, `Candidate`. Field-for-field as in `design.md`.
  - Sentinel errors: `ErrInvalidTransition`, `ErrNodeNotFound`,
  `ErrDuplicateNode`.
  - `func CanonicalText(title, text string) string` — whitespace-normalized
  concat used as the input to content hashing.
  - `func ContentHash(title, text string) string` — `sha256([]byte(CanonicalText(...)))`
  encoded as hex. (Hash column lives in the DB; this is the single source
  of truth for what the hash represents.)
  - No methods on any of these yet.
- [ ] **Tests** (`internal/memory/knowledge_test.go`):
  - `TestCanonicalText_NormalizesWhitespace` — multiple spaces, leading/trailing
  whitespace, case-fold.
  - `TestContentHash_Stable` — same input → same hash; differing inputs → different.
  - `TestContentHash_IgnoresWhitespace` — `"a  b"` and `"a b"` hash equal.
- [ ] Extend `db_test.go`:
  - `TestMigrations_CreateAllTables` (existing) — add the four new tables
  to the `tables` slice; bump version check to `>=3`.
  - `TestMigrations_V3FTS5Triggers` — `INSERT` then `UPDATE` then `DELETE` a
  stub row in `knowledge_nodes` and verify `knowledge_fts` reflects.
- [ ] **Verify:**
  `go test ./internal/memory/...` — slice 1 complete when migration is
  applied, types compile, sentinel errors are declared.

---

## Slice 2 — `KnowledgeStore` interface + basic CRUD

- [ ] **Define `KnowledgeStore` interface** in `knowledge.go` with the full
  surface from `design.md`. Concrete `SQLiteKnowledgeStore` in a new file
  `internal/memory/knowledge_store.go`. Constructor
  `NewSQLiteKnowledgeStore(db *sql.DB) *SQLiteKnowledgeStore`.
- [ ] **Implement CRUD methods** (just these in this slice):
  `UpsertNode`, `GetNode`, `GetNodeByContentHash`, `AddSources`,
  `SourcesFor`, `Events` (write-event-and-read).
- [ ] `UpsertNode` is idempotent on `node_id`. If a row with that `node_id`
  exists, update the mutable fields (`title`, `text`, `status`, `confidence`,
  `valid_from`, `last_confirmed`, `superseded_at`, `expires_at`); do *not*
  touch `created_at_epoch` or `content_hash` (immutable).
- [ ] `GetNodeByContentHash` is the dedup probe used by the consolidator;
  include `kind` in the lookup. `(kind, content_hash)` index already
  covers it.
- [ ] `AddSources` accepts a slice; ignore duplicate `(node_id, observation_id)`
  inserts (use `INSERT OR IGNORE`).
- [ ] `Events` filters by `node_id` and is ordered by `created_at_epoch ASC`.
- [ ] **Tests** (`knowledge_store_test.go`):
  - `TestUpsertNode_Idempotent` — insert twice, observe `LastInsertId` set
  after first insert, `RowsAffected` reflects update on second.
  - `TestGetNodeByContentHash_FindsExisting` / `_ReturnsNilIfAbsent`.
  - `TestAddSources_DedupesOnObservationID`.
  - `TestSourcesFor_OrderedReturns`.
  - `TestEvents_OrderedAppend`.
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 3 — Validation FSM + edges

- [ ] **Implement** on `SQLiteKnowledgeStore`:
  - `TransitionStatus(ctx, nodeID, to, payload)` — validates the FSM
  (`Candidate ↔ PendingValidation → Confirmed → Deprecated | Rejected`),
  returns `ErrInvalidTransition` for illegal moves. Inside a single tx:
  read current status, validate, write new status, append
  `KnowledgeEvent{kind: "transitioned", old_status, new_status, payload}`.
  - `RecordConfidence(ctx, nodeID, newConf, reason)` — same shape: write
  new confidence, append `KnowledgeEvent{kind: "confidence_updated"}`.
  - `AddEdge(ctx, fromID, toID, kind)` — `INSERT OR IGNORE` into
  `knowledge_edges`; idempotent on `(from_id, to_id, kind)` UNIQUE constraint.
  - `Edges(ctx, nodeID, kind)` — outgoing edges of a given kind ordered by
  `created_at_epoch ASC`.
- [ ] **Tests:**
  - `TestTransitionStatus_HappyPath` — `Candidate → PendingValidation → Confirmed`
  succeeds, events recorded with correct `old`/`new`.
  - `TestTransitionStatus_RejectsIllegal` — e.g.
  `Candidate → Rejected` (skipping `Confirmed`) returns
  `ErrInvalidTransition` and writes no event.
  - `TestTransitionStatus_RecordsEvent`.
  - `TestRecordConfidence_UpdatesValueAndAppendsEvent`.
  - `TestAddEdge_Idempotent` — second call no-ops.
  - `TestEdges_FiltersByKind`.
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 4 — Graph queries

- [ ] **Implement** on `SQLiteKnowledgeStore`:
  - `Why(ctx, nodeID, maxDepth int)` — recursive CTE over `based_on`,
  `supersedes`, `resolved_by` *incoming* edges (predecessors). Limit
  depth to the parameter. Return ordered by `created_at_epoch ASC`.
  - `Supersedes(ctx, nodeID)` — recursive CTE over the single-kind
  `supersedes` edge from this node. Returns the chain.
  - `Conflicts(ctx, nodeID)` — two arms joined via `UNION`:
  (a) nodes sharing `content_hash` (excluding self),
  (b) nodes of the same kind with overlapping `valid_from` windows
  *and* different `node_id`. Filter by `status NOT IN ('rejected', 'deprecated')`.
  - `ByType(ctx, kind, project, limit)` — `status IN ('confirmed',
        'pending_validation', 'candidate')` only (active), ordered by
  `last_confirmed DESC` (NULLs last).
- [ ] **Tests** (seeded mini-graphs):
  - `TestWhy_FollowsAncestorChain` — seed A `supersedes` B and `based_on`
  C; `Why(A, 3)` returns both.
  - `TestWhy_StopsAtMaxDepth`.
  - `TestSupersedes_Chain` — A→B→C; returns B and C.
  - `TestConflicts_SameHash`.
  - `TestByType_FiltersByProjectAndExcludesDeprecated`.
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 5 — FTS5 search

- [ ] **Confirm v3 already created** `knowledge_fts` and triggers (slice 1
  should have). If not, fold those statements into slice 1's migration;
  do not create a v4 migration just for FTS.
- [ ] **Implement** on `SQLiteKnowledgeStore`:
  - `Search(ctx, query, project, limit)` — FTS5 MATCH with the same
  `sanitizeFTS5Query` pattern from the existing `search.go`. Join
  back to `knowledge_nodes`. Order by FTS rank.
  - `RecentDecisions(ctx, project, since, limit)` — SQL query joining FTS
  not strictly required; plain `SELECT ... WHERE kind='decision' AND
        project=? AND created_at_epoch >= ? AND status='confirmed' ORDER BY
        created_at_epoch DESC LIMIT ?`. (Named "Recent" not "FTS" because
  temporal filtering beats relevance here.)
- [ ] **Tests:**
  - `TestSearch_RankedResults` — insert three nodes whose text differs
  only by query match count; assert ranked order.
  - `TestSearch_FiltersByProject`.
  - `TestSearch_FTSSanitization` — query with FTS5 operators must not
  blow up.
  - `TestRecentDecisions_SinceFilter` — boundary cases (equal to `since`,
  newer, older).
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 6 — `Extractor` interface + `RuleExtractor`

- [ ] **Create `internal/memory/extractor.go`:**
  - `type Extractor interface { Extract(ctx, []*Observation) ([]Candidate, error) }`.
  - `type RuleExtractor struct` with a `KindWeights map[ObservationType]KnowledgeKind`,
  a `SourceWeights map[ObservationType]int`, and a `MinDecisionConfirmations int`
  (default 1 in `NewRuleExtractor`).
  - `NewRuleExtractor()` returns a config with the explicit table:
  ```
  TypeDecision → KindDecision,    SourceAgent (50)
  TypeBugfix   → KindLesson,      SourceTool  (70)
  TypeFeature  → KindLesson,      SourceTool  (70)
  TypeRefactor → KindLesson,      SourceTool  (70)
  TypeDiscovery→ KindFact,        SourceAgent (50)
  TypeChange   → (skip — too noisy), not mapped
  ```
  Plus an optional `LessonPatternRe *regexp.Regexp` for titles starting
  with `"Lesson:"`/`"Learning:"` to override into `KindLesson`.
  - `Extract` groups observations by an inference key (see below) and
  emits one `Candidate` per group; one observation can appear in at
  most one candidate per session.
  - **Inference key** for the rule extractor: SHA-256 over
  `(kind + canonical(title prefix up to ':' or first 64 chars))`.
  Observations with the same key merge into one candidate. The
  candidate's `Title` is the most recent observation's title,
  `Text` is the latest non-empty text, `SourceObservationIDs` is the
  union of contributing IDs.
- [ ] **Tests** (`extractor_test.go`) — entirely table-driven, no real DB:
  - `TestRuleExtractor_MapsObservationTypes` — assert each
  `ObservationType → KnowledgeKind` mapping.
  - `TestRuleExtractor_MergesByKey` — three observations that should
  collapse into one candidate.
  - `TestRuleExtractor_SkipsTypeChange`.
  - `TestRuleExtractor_LessonPrefixOverride`.
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 7 — `Consolidator`

- [ ] **Create `internal/memory/consolidator.go`:**
  -
  `type Consolidator struct { observations Store; knowledge KnowledgeStore; extractor Extractor; now func() time.Time }`.
  - `func NewConsolidator(o Store, k KnowledgeStore, e Extractor) *Consolidator` —
  `now` defaults to `time.Now`.
  - `func (c *Consolidator) Consolidate(ctx, sessionID) (*ConsolidationReport, error)`:
  1. Read all observations for the session via
  `observations.GetObservations` by ID lookup (caller passes IDs)
  or a new helper `ObservationsForSession(ctx, sessionID)`. — *add this
  minimal helper to `Store` interface and `SQLiteStore` as part of this slice.*
  2. Run extractor.
  3. For each candidate:
  - Compute `ContentHash` from `title + text`.
  - `GetNodeByContentHash(kind, hash)` → if hit, add an edge
  `(newNodeID, existingNodeID, merged_into)` from a *temporary*
  placeholder… actually simpler: do not introduce a brand-new
  `node_id` on dedup; instead, let the caller's `node_id`
  collide-detection happen naturally, and emit the
  `merged_into` edge only if the consolidator decides the new
  content *is the same knowledge* under a different `node_id`.
  v1 rule: if same `(kind, content_hash)`, emit a `KnowledgeEvent{kind:"merged_into"}` on the
  existing node carrying the new candidate's `node_id` as the
  payload. Do *not* create a second row.
  - Else, `UpsertNode` with `status=candidate`, default `confidence=0.5`.
  - `AddSources(node_id, observation_ids)`.
  4. Aggregate counts. Per-candidate errors go into `Report.Errors`,
  pipeline continues.
- [ ] **ObservationsForSession helper:**
  - Add `ObservationsForSession(ctx, sessionID string) ([]*Observation, error)`
  to `Store` interface and `SQLiteStore`.
  - `mockStore` in `worker_test.go` already exists; update it to return
  `nil, nil` for this new method (one-line change). Keep that file's
  diff minimal.
- [ ] **Tests** (`consolidator_test.go`):
  - `TestConsolidate_CreatesCandidate` — session with 2
  `TypeDecision` obs → consolidator creates 1 candidate, links 2
  sources.
  - `TestConsolidate_MergesByHash` — second consolidation with rephrased
  text but identical hash → no second node, `merged_into` event
  appended.
  - `TestConsolidate_PerCandidateError` — extractor returns an error
  on one observation; report continues with the rest, error captured
  in `Report.Errors`.
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 8 — `SQLiteStore.Knowledge()` accessor + wiring helper

- [ ] Add `func (s *SQLiteStore) Knowledge() *SQLiteKnowledgeStore` to
  `store.go`. Returns `&SQLiteKnowledgeStore{db: s.db}`. No new `mockStore`
  work needed because the interface is *not* extended.
- [ ] Add `func DefaultConsolidator(obsStore Store) (*Consolidator, error)` to
  `consolidator.go`. Builds `SQLiteKnowledgeStore` from a `*SQLiteStore`,
  returns `NewConsolidator(obsStore, ks, NewRuleExtractor())`. Errors
  only from a future typed accessor check; for now returns
  `nil, nil` for non-`*SQLiteStore` callers and a real consolidator
  when the type asserts.
- [ ] **Tests** (`store_test.go`):
  - `TestSQLiteStore_Knowledge_ReturnsSameDB` — open memory DB, call
  `Knowledge()`, write a node via the returned store, read it back
  via a fresh `Knowledge()` call to confirm sharing.
- [ ] **Verify:** `go test ./internal/memory/...`

---

## Slice 9 — E2E

- [ ] Extend `internal/memory/e2e_test.go` (already `//go:build e2e`):
  - `TestEndToEnd_ConsolidateThenQuery` — insert a session, three
  observations of `TypeDecision` with overlapping topic, run
  `DefaultConsolidator`, assert one candidate, then call
  `ByType(decision, project, 10)` and confirm it appears.
- [ ] **Verify:** `go test -tags e2e ./internal/memory/...`

---

## Slice 10 — Cleanup & repo-wide gate

- [ ] `make vet` — no findings.
- [ ] `make lint` — no findings; if `golangci-lint` reports anything style-only,
  fix in the new files only (do not touch unrelated code).
- [ ] `make test` — full repo green; nothing outside `internal/memory/` was
  modified outside the optional `ObservationsForSession` interface
  widening in slice 7.
- [ ] Confirm `go.mod` is unchanged (no new deps).
- [ ] Commit message: clearly identifies the slice boundary (e.g.
  `feat(memory): temporal knowledge graph — slices 1..N`).

---

## Risk register

- **`//go:build e2e` widening** in `mockStore` (slice 7) — minimal change,
  one method returning `nil, nil`. Existing worker tests are unaffected.
- **Recursive CTE correctness** in `Why` / `Supersedes` — covered by
  seeded-graph tests with non-trivial depth (≥3).
- **FTS5 trigger insertion order** — slice 1 must create the virtual table
  *before* the triggers; SQLite has historically allowed either, but we
  follow the existing v2 ordering (table then triggers).
- **Migration v3 idempotency** — `CREATE TABLE IF NOT EXISTS`,
  `CREATE INDEX IF NOT EXISTS`, etc., mirroring v1/v2. Tested in `db_test.go`.
