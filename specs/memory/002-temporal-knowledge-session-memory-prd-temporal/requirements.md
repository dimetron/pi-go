# Requirements

## Questions & Answers

### Q1 — Target scope

**Q:** What is the target scope for this implementation?
*Options: (a) Go library within pi-go, (b) Full runtime integration, (c) Standalone service, (d) Library + minimal reference agent.*

**A:** (a) Go library within pi-go.

**Implication:** The deliverable is a package (likely in `internal/memory/` or a new sibling package) that any pi-go agent or CLI can import. No HTTP server, no external service. Storage local. Extraction/embedding are pluggable interfaces that the host wires up.

**Critical context discovered:** `internal/memory/` already exists in pi-go with a working subsystem covering `Session`, `Observation` (typed: decision/bugfix/feature/refactor/discovery/change), and `SessionSummary`. It already does compression, search, context loading, privacy filtering, and worker-based extraction. The new PRD's `Observation` and `Summary` types overlap. The new work must **extend** the existing package, not replace it — adding `Fact/Decision/Constraint/Procedure/Lesson/Question` knowledge nodes with temporal supersession, provenance, confidence, and a validation state machine on top of the existing observation/summary machinery.

### Q2 — Relationship to existing `internal/memory`

**Q:** How should the new system relate to the existing `internal/memory` package?
*Options: (a) Extend in place, (b) New sibling package, (c) Both with knowledge as a thin layer.*

**A:** (a) Extend in place — new knowledge tables live alongside `Observation`/`Summary` in the same package, sharing the existing SQLite DB and worker pool.

### Q3 — Storage backend

**Q:** What is the storage backend for the knowledge graph?
*Options: (a) Same SQLite, simple tables, (b) SQLite + dedicated graph tables with proper indexes and adjacency representation, (c) SQLite + in-memory graph layer.*

**A:** (b) SQLite + dedicated graph tables with proper indexes. Edges in an `edge(from_id, to_id, kind)` table with indexes so the PRD's `supersedes / based_on / learned_from / prevents / derived_from / uses / resolved_by` traversals are fast via recursive CTE.

### Q4 — Node identity

**Q:** How should knowledge nodes be addressed and merged?
*Options: (a) Content-derived ID, (b) Caller-supplied ID, (c) Hybrid caller-supplied ID + content hash for dedup.*

**A:** (c) Hybrid. Every node carries (1) a caller-supplied `node_id` (stable string used as primary key and referenced by edges), and (2) a `content_hash = sha256(canonical_text)` used only for de-dup detection. Identical hash + identical type produces a `MergedInto` event against the existing node (history preserved per PRD principle 3 — "nothing is deleted"). Edges always reference `node_id`, never `content_hash`.

### Q5 — Candidate extraction mechanism

**Q:** How are candidates extracted from raw observations?
*Options: (a) Rule-based only, (b) LLM-driven only, (c) Both behind an `Extractor` interface, (d) Other.*

**A:** (c) Both behind a `type Extractor interface { Extract(ctx, []Observation) ([]Candidate, error) }`. **First
implemented slice is a `RuleExtractor`** — deterministic, heuristic-based, fully covered by table-driven tests. The LLM
extractor lands in a later slice (out of MVP scope). The pipeline contract is identical for both.

### Q6 — MVP query surface

**Q:** What query surface should the MVP expose?
*Options: (a) Core graph queries only (4), (b) Core graph + FTS5 full-text search (6), (c) Core + FTS + LLM-friendly
session-start retrieval (all 10).*

**A:** (b) Core graph queries plus full-text search via SQLite FTS5.

**Core graph queries (MVP):**

1. `Why(nodeID)` → ancestor trail via `based_on / supersedes / resolved_by`.
2. `Supersedes(nodeID)` → just the supersession chain.
3. `Conflicts(nodeID)` → nodes sharing `content_hash` prefix *or* overlapping `valid_from` window with divergent claim.
4. `ByType(kind, project, limit)` → current active nodes of a given kind for a project.

**Full-text queries (MVP, via FTS5):**

5. `RecentDecisions(project, since, limit)` → FTS-ranked decision nodes.
6. `Search(query, project, limit)` → generic FTS5 search over title + text.

**Out of MVP (later slices):** `UnresolvedQuestions`, `Deprecated`, `UserConfirmed` filter, session-start summary
retrieval, multi-hop ancestors, graph algorithms (PageRank for memory quality).

## Requirements Summary (proposed)

Before I move to research, here's the consolidated requirements picture:

**MVP deliverable:** a Go library, exposed as additions to `internal/memory/` in pi-go, that adds a persistent *
*temporal knowledge graph** alongside the existing `Observation`/`Summary` subsystems — sharing the SQLite DB and worker
pool, never deleting knowledge, always recording provenance and temporal transitions.

**Knowledge ontology (subset implemented in MVP):** `Fact`, `Decision`, `Constraint`, `Procedure`, `Lesson`, `Question`.
`Observation` and `Summary` from the existing package remain as the *raw material* the consolidation pipeline reads.

**Identity:** hybrid — caller-supplied `node_id` (stable, used by edges) + `content_hash` (for dedup events).

**Temporal model:** every node carries `created_at`, `valid_from`, `last_confirmed`, `superseded_at`, `expires_at`.
Supersession creates a new node and an edge; old node retains a `superseded_by` pointer and a `"deprecated"` status — it
is not deleted.

**Provenance:** every node carries `source_type` enum (`USER | CODE | TEST | DOCUMENT | TOOL | AGENT | SYSTEM`) plus
source-priority weight and a list of supporting evidence IDs (observation IDs that contributed).

**Confidence:** float in `[0,1]`. Updated by events (`UserConfirmed`, `TestPassed`, `ConflictDetected`,
`UserCorrected`).

**Validation state machine:** `Candidate → PendingValidation → Confirmed → Deprecated → Rejected`. Rejected nodes are
kept (training/eval data per PRD).

**Storage:** SQLite (existing DB) with new tables `knowledge_nodes`, `knowledge_edges`, `knowledge_events`,
`knowledge_sources`. Indexes on `(from_id, kind)`, `(to_id, kind)`, `(status, kind)`, `(project, created_at)`. FTS5
virtual table over title + text.

**Extraction:** `Extractor` interface; first implementation is deterministic `RuleExtractor`. LLM extractor deferred.

**Consolidation triggers (MVP):** explicit `Consolidate(ctx, sessionID)` method that callers wire up at end-of-session.
Periodic and ask-user-boundary triggers are out of scope for MVP (callers invoke manually).

**Conflict resolution:** priority = source_weight × confidence. Higher wins; loser becomes `superseded` (never deleted).
Tie-breaker: later `valid_from` wins *only* when source_weight is equal.

**Out of MVP (explicit non-goals for this slice set):**

- LLM extractor
- Multi-agent shared memory
- Automatic PR summaries / ADR generation
- Knowledge aging/decay
- Reinforcement learning from corrected memories
- Session-start summary retrieval helper
- Graph algorithms

---

**Please confirm or push back on this summary before I move to Phase 3 (Objective Research).** Anything ambiguous,
missing, or that you want to scope in/out? If you're happy, just say "proceed to research" and I'll explore the existing
codebase to ground the design in fact.
