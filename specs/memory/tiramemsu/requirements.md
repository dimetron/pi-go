# Requirements

## Constraints

- C1. Pure Go. No cgo, no new native dependency, no Rust binary. The engine runs
  on the palace database opened by `palace.OpenDB` (`modernc.org/sqlite`).
- C2. One database file. The new tables live in the existing palace DB, added by
  an ordinary palace migration.
- C3. Nothing the agent wrote is lost by the upgrade: every row of the legacy
  `triples` table is imported, with its valid time, its source file and its
  extraction time. The legacy table itself is left in place, unread and unwritten.
- C4. Existing callers keep working with their current signatures:
  `Palace.KGAdd / KGQuery / KGInvalidate / KGTimeline / KGStats`, the tools
  `palace-kg-add / -query / -invalidate / -timeline`, `pi memory kg
  query|add|timeline`, and `palace-status`.
- C5. No stdout/stderr writes from palace code (TUI rule in `AGENTS.md`).
- C6. Tests close every DB; no test depends on the host search tool name.

## Functional requirements

Identity and layers
- R1. Every statement has an integer eid, never reused. A statement's subject or
  object may be another statement's eid (a layer).
- R2. Values: entity (normalized name, as `entityID` does today), statement,
  string, number (float64), integer, bool, time. Predicates are entities.

Time
- R3. Every committed write call is one transaction with a gap-free `t` starting
  at 1 and a strictly increasing `instant` (`max(now_ms, previous + 1)`), plus
  `author`, `source`, `reason`.
- R4. A statement is believed during `[t_add, t_ret)` and valid during
  `[v_from, v_to)` (epoch ms, NULL = unbounded).
- R5. Views: `Now`, `AsOf(t)`, `AsOfInstant(time)` (largest `t` with
  `instant <= time`; before the first tx it is empty), `History`; each can be
  combined with `ValidAt(time)`.

Never forget
- R6. SQLite triggers reject: DELETE on statements, terms and transactions; any
  update of a statement except setting `t_ret`/`ret_kind` once from NULL; any
  update of a term or transaction; re-insert of an existing key.

Verbs (all inside `KnowledgeGraph.Transact`)
- R7. `Assert(s, p, o, valid)` is idempotent on a live statement with the same
  `(s, p, o)` and an overlapping valid interval; with `OnExisting: Confirm` the
  existing statement is also confirmed.
- R8. `Create` always inserts.
- R9. `Retract(eid)` sets `t_ret` and cascades, breadth-first with a visited set,
  to live statements whose subject or object is a reached eid and to live
  statements derived from a reached eid. Retracting a retracted eid returns false.
  The cascade aborts the transaction past `MaxCascade` (default 10 000).
- R10. `Supersede(eid, patch)` retracts the cascade set with kind `supersede`,
  replays the retained set under fresh eids with subject/object rewritten through
  σ, applies the patch (object, valid from, valid to) to the root, and asserts
  `(new sys:supersedes old)`. Derived statements reached through a support link
  are dropped, not replayed, with their structural dependents. The subject and
  predicate cannot be patched.
- R11. `Confirm(eid)` asserts `(eid sys:confirmed_by <tx>)`.
- R12. `Derive(s, p, o, valid, support)` writes a statement and one
  `(D sys:derived_from x)` link per support. It is idempotent only on a live
  statement with the same content, an overlapping interval and exactly the same
  live support set. Supports must be live.
- R13. Predicate schema: `cardinality one` retracts live statements
  `(s, p, o' != o)` with overlapping valid time (kind `cardinality`, with
  cascade, layers not replayed); `unique` rejects a second live subject for the
  same `(p, o)`. A schema change that live data already violates is rejected.
- R14. `sys:` predicates are engine-owned; user writes with them are rejected.
- R15. Statement-valued endpoints of ordinary writes must exist and be live, and
  a statement cannot reference itself.
- R16. `DryRun` returns the report and commits nothing.

Reads
- R17. `About(entity, direction)`, `Match(pattern)`, `Layers(eid)`,
  `Dependents(eid)` (the cascade walk as a read), `Explain(eid)` (statement,
  layers, supports, lineage both ways, confirmations with tx metadata,
  dependents count), `Conflicts(filter)`, `TextSearch(query)`, `Events(since)`.
  All honour the view.
- R18. Conflicts: subject/predicate pairs with two or more distinct live objects
  whose valid times overlap, each value with its eids and evidence (confidence,
  sources, confirmation count, authors). No score is invented.
- R19. Text search: FTS5 over entity names and string literal values; a hit
  returns the visible statements whose subject or object is the matching term.

Agent surface
- R20. `palace-kg-add` gains `valid_to`, `confidence`, `source`, `about` (eid to
  annotate), `support` (eids → derive), `literal` (object is a string value),
  `confirm_existing`. Its output names the eid.
- R21. New tools: `palace-kg-revise` (`action`: supersede | confirm | retract),
  `palace-kg-explain`, `palace-kg-conflicts`. `palace-kg-query` gains `tx_as_of`
  and `text`. Output shows eids, confidence and sources.
- R22. Tool transactions record `author` = agent name and `source` =
  `session:<id>` when the ADK context provides them.
- R23. `pi memory kg` gains `explain <eid>`, `conflicts`, `log`, and
  `query --tx-as-of`.
- R24. `KGStats` reports facts (non-layer, non-system live statements), layers,
  retracted statements, transactions, entities.

## Acceptance

- A1. Every requirement above has a test in `internal/palace`.
- A2. A database created by the current release, with triples in it, opens,
  migrates, and answers `KGQuery`/`KGTimeline` with the same facts.
- A3. Raw SQL `DELETE FROM kg_stmt`, `UPDATE kg_stmt SET o = ...`, and a second
  retraction fail on the opened file.
- A4. `make build test lint vet` pass.
