# Temporal Knowledge Memory for Coding Agents (Go library)

## Objective

Add a **temporal knowledge graph** layer to pi-go's existing `internal/memory`
package, sharing the SQLite DB and worker pool. The new layer stores
Facts/Decisions/Constraints/Procedures/Lessons/Questions with hybrid
identity, validation state machine, provenance, confidence, and temporal
supersession. It consumes the existing `Observation` rows (produced by
the ADK pipeline) as raw material and exposes graph + FTS5 queries.
Deliverable: 10 vertical slices, each compiling and passing
`go test ./internal/memory/...` on its own.

## Key Requirements

1. **Extend in place.** Migration `v3` adds `knowledge_nodes`,
   `knowledge_edges`, `knowledge_sources`, `knowledge_events`,
   `knowledge_fts` + sync triggers to the existing SQLite DB. No schema
   changes to existing tables.
2. **Hybrid identity.** Each node has a caller-supplied `node_id` (used as
   the primary key and by edges) and a `content_hash` used only for dedup
   detection. Identical `(kind, content_hash)` produces a `merged_into`
   event on the existing node; no second row.
3. **Temporal supersession, never delete.** New knowledge supersedes old
   via an edge; the old node gets `status='deprecated'` and
   `superseded_at` set. No `DeleteNode` method exists.
4. **Validation FSM.**
   `Candidate → PendingValidation → Confirmed → Deprecated | Rejected`.
   Transitions go through `TransitionStatus` which validates and emits a
   `KnowledgeEvent`. Rejected nodes stay in the table (training data).
5. **`Extractor` interface, deterministic first cut.**
   `RuleExtractor` maps `ObservationType` → knowledge kinds via an
   explicit table; LLM extractor is deferred.
6. **Consolidator** reads observations for a session, runs the extractor,
   dedups by `(kind, content_hash)`, upserts by `node_id`, links
   observation provenance, and returns a `ConsolidationReport`.
7. **MVP queries.** `Why`, `Supersedes`, `Conflicts`, `ByType`,
   `Search` (FTS5), `RecentDecisions`. Graph queries use recursive CTEs
   over `knowledge_edges`.
8. **No deletion.** A canonical characteristic of the design —
   `ON DELETE RESTRICT` on every knowledge FK.

## Acceptance Criteria

### Extend the schema

- Given an empty DB, when `memory.OpenDB(":memory:")` is called, then
  `knowledge_nodes`, `knowledge_edges`, `knowledge_sources`,
  `knowledge_events`, `knowledge_fts` exist and
  `schema_versions.version = 3`.
- Given an existing v2 DB, when `OpenDB` is called, then migrations apply
  idempotently and `version` becomes 3 without altering existing tables.

### Identity and dedup

- Given a candidate with `(kind=K, content_hash=H)` where (K, H) already
  exists, no second node is created; a `KnowledgeEvent{kind:"merged_into"}`
  is recorded on the existing node carrying the new `node_id` in payload.
- Different `node_id`, same `(kind, content_hash)` → same outcome as above.

### Temporal supersession

- Given `Decision A`, when a new candidate supersedes A and is upserted,
  then A's status is `deprecated`, `superseded_at` is set, and an edge
  `(new, old, supersedes)` exists.
- Direct row deletion is impossible — no `DeleteNode` method exists.

### Validation FSM

- `TransitionStatus(id, pending_validation)` on a `Candidate` succeeds
  and emits a `KnowledgeEvent{kind:"transitioned"}`.
- `TransitionStatus(id, archived)` returns `ErrInvalidTransition` and
  writes no event.

### Graph queries

- `Why(root, 3)` returns ancestors via `based_on`, `supersedes`,
  `resolved_by` only, up to depth 3.
- `Supersedes(id)` returns the chain via recursive CTE.
- `Conflicts(id)` returns active nodes sharing a `content_hash` *or*
  with overlapping `valid_from`.
- `ByType(kind, project, 10)` returns at most 10 active nodes ordered by
  `last_confirmed DESC` (NULLs last).
- `RecentDecisions(project, since, 10)` returns confirmed decisions
  created at or after `since`.

### FTS

- `Search("sqlite")` ranks by FTS5 `rank` and joins back to base.
- Updates to a node's `title`/`text` reflect in the FTS index.

### Consolidator

- A session with 3 `TypeDecision` observations, no prior knowledge →
  `Consolidate` produces 1 candidate, 3 `knowledge_sources`, and a
  `Report{CandidatesAdded:1}`.
- An extractor error on one observation does not abort; the report
  continues and `Report.Errors` contains the error.

## Implementation Slices

1. **Schema v3 + types** — append migration to `internal/memory/db.go`;
   create `knowledge.go` with all enums, structs, sentinel errors,
   `CanonicalText`, `ContentHash`. Verify:
   `go test ./internal/memory/...`
2. **`KnowledgeStore` interface + CRUD** — `knowledge_store.go` with
   `UpsertNode`, `GetNode`, `GetNodeByContentHash`, `AddSources`,
   `SourcesFor`, `Events`. Verify: same command.
3. **Validation FSM + edges** — `TransitionStatus`,
   `RecordConfidence`, `AddEdge`, `Edges`. Verify: same command.
4. **Graph queries** — `Why` (recursive CTE), `Supersedes`, `Conflicts`,
   `ByType`. Seeded-graph tests. Verify: same command.
5. **FTS5 search** — `Search`, `RecentDecisions`. Verify: same command.
6. **`Extractor` interface + `RuleExtractor`** — type→kind table,
   inference-key merge, deterministic. Table-driven tests only. Verify:
   same command.
7. **`Consolidator`** — reads observations for a session,
   dedup-by-hash, upsert-by-node_id, link provenance, emit events.
   Adds `ObservationsForSession` to the `Store` interface and minimal
   `mockStore` stub. Verify: same command.
8. **`SQLiteStore.Knowledge()` accessor + `DefaultConsolidator`** —
   wires rule-based extractor + sqlite knowledge store.
   Verify: same command.
9. **E2E** — extend `e2e_test.go` (`//go:build e2e`). Verify:
   `go test -tags e2e ./internal/memory/...`
10. **Cleanup & repo-wide gate** — `make vet`, `make lint`, `make test`.
    No new dependencies; no edits outside `internal/memory/` (except the
    one-method `mockStore` widening in slice 7).

## Gates

- **build**: `go build ./...`
- **test**: `go test ./internal/memory/...` (per slice) and `make test`
  (= `go test ./...`) for the repo at slice 10.
- **vet**: `make vet` (= `go vet ./...`)
- **lint**: `make lint` (= `golangci-lint run ./...`)
- **e2e** (slice 9 only): `go test -tags e2e ./internal/memory/...`

## Reference

- Design: `specs/memory/002-temporal-knowledge-session-memory-prd-temporal/design.md`
- Outline: `specs/memory/002-temporal-knowledge-session-memory-prd-temporal/outline.md`
- Plan: `specs/memory/002-temporal-knowledge-session-memory-prd-temporal/plan.md`
- Requirements: `specs/memory/002-temporal-knowledge-session-memory-prd-temporal/requirements.md`
- Research: `specs/memory/002-temporal-knowledge-session-memory-prd-temporal/research/existing-memory-package.md`

## Constraints

- Pure-Go SQLite only — `modernc.org/sqlite` 1.56, already in `go.mod`.
  No cgo, no new deps.
- Migration `v3` is **append-only**; no edits or deletions of `v1`/`v2`.
- All knowledge tables use `ON DELETE RESTRICT` — knowledge is
  undeletable at the SQL level too.
- Errors wrap with `fmt.Errorf("memory: %s: %w", verb, err)`.
- Time columns: RFC3339 + `*_epoch` (Unix seconds) — matches existing
  pattern.
- Tests use standard library `testing`, `OpenDB(":memory:")`,
  `t.TempDir()`, table-driven where applicable.
- Do **not** extend the `Store` interface in slices 2–6; only slice 7
  adds the single `ObservationsForSession` method (and the `mockStore`
  one-liner), and slice 8 adds the accessor method on `*SQLiteStore`.
  This keeps existing callers untouched.
- `e2e_test.go` already uses `//go:build e2e` — preserve the tag when
  extending it.
