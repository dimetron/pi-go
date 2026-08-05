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
