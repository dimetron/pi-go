# Design

## Overview

A small bitemporal statement engine, `kg_*`, inside `internal/palace`, on the
palace database. `KnowledgeGraph` keeps its role as the KG facade and gains
`Transact` (writes) and views (reads). The old `triples` table is imported once
and then left alone.

```
tools (palace-kg-*) ─┐
pi memory kg ────────┼─> Palace.KG* ─> KnowledgeGraph ─┬─ Transact ─> kgTx (assert, create, retract,
palace-status ───────┘                                 │                supersede, confirm, derive, schema)
                                                       └─ View ─────> About, Match, Layers, Dependents,
                                                                      Explain, Conflicts, TextSearch, Events
                                                                         │
                                               kg_tx · kg_term · kg_stmt · kg_term_fts · kg_event (view)
```

## Values: tagged ObjectID

A value is one `int64`: `payload << 4 | tag`, as in Tiramemsu
(`data-model.md#ObjectId`). Every column `s`, `p`, `o` is one integer, so the
indexes stay small and a statement reference is just another value.

| Tag | Kind | Payload |
|---|---|---|
| 1 | Entity | `kg_term.id` (lex = normalized entity id) |
| 2 | Statement | eid |
| 3 | String | `kg_term.id` (lex = text) |
| 4 | Integer | the value itself (inline, signed, ±2⁵⁹) |
| 5 | Number | `kg_term.id` (lex = shortest float text, `num` = value) |
| 6 | Bool | 0 / 1 |
| 7 | Time | epoch ms (inline) |
| 8 | Transaction | `t` |

`Value` is the Go type callers use (`Ent`, `StmtRef`, `Str`, `Int`, `Num`,
`Bool`, `TimeVal`); the engine interns terms on write and decodes on read.
Entities keep using `entityID()` for normalization, and the `entities` table
keeps display names.

## Schema (palace migration 6)

```sql
CREATE TABLE kg_tx (
  t       INTEGER PRIMARY KEY,
  instant INTEGER NOT NULL UNIQUE,
  author  TEXT NOT NULL DEFAULT '',
  source  TEXT NOT NULL DEFAULT '',
  reason  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE kg_term (
  id  INTEGER PRIMARY KEY AUTOINCREMENT,
  tag INTEGER NOT NULL,
  lex TEXT NOT NULL,
  num REAL,
  UNIQUE (tag, lex)
);
CREATE TABLE kg_stmt (
  eid      INTEGER PRIMARY KEY AUTOINCREMENT,  -- never reused
  s        INTEGER NOT NULL,
  p        INTEGER NOT NULL,
  o        INTEGER NOT NULL,
  t_add    INTEGER NOT NULL,
  t_ret    INTEGER,           -- NULL = live
  v_from   INTEGER,           -- epoch ms, NULL = unbounded
  v_to     INTEGER,
  ret_kind INTEGER            -- 0 explicit, 1 cascade, 2 supersede, 3 cardinality
);
-- live (partial) and history indexes on (s,p,o), (p,o,s), (o,s,p); log indexes
CREATE VIEW kg_event(t, eid, op, kind) AS
  SELECT t_add, eid, 'assert', NULL FROM kg_stmt
  UNION ALL SELECT t_ret, eid, 'retract', ret_kind FROM kg_stmt WHERE t_ret IS NOT NULL;
CREATE VIRTUAL TABLE kg_term_fts USING fts5(lex, content='kg_term', content_rowid='id');
-- AFTER INSERT ON kg_term WHEN tag IN (1, 3): index it
```

Triggers (Tiramemsu `storage.md#Invariant Triggers`, renamed): `kg_stmt` no
delete, retract-once (only `t_ret`/`ret_kind` from NULL, `t_ret >= t_add`),
no replace; `kg_term` and `kg_tx` no delete, no update, no replace.
`AUTOINCREMENT` keeps eids and term ids from being reused after a committed
row; rolled-back ids may be reissued (see "Deviations").

`sys:` predicates (engine-owned entity terms): `sys:supersedes`,
`sys:confirmed_by`, `sys:derived_from`, `sys:cardinality`, `sys:unique`.
User-facing layer predicates are ordinary: `confidence` (Number), `source`
(String), `source_file` (String, from legacy rows).

## Transactions

`KnowledgeGraph.Transact(ctx, TxOptions, func(*KGTx) error) (*TxReport, error)`

1. `BeginTx`. The first statement allocates the transaction in one write:
   `INSERT INTO kg_tx SELECT COALESCE(MAX(t),0)+1, MAX(?, COALESCE(MAX(instant),0)+1), … FROM kg_tx`.
   Being a write, it takes SQLite's write lock before anything is read, so two
   writers (two pi processes on one palace) serialize on `busy_timeout`
   instead of racing for `t`.
2. Run the callback with a `KGTx` bound to the `*sql.Tx`. Every read inside uses
   the tx, never the pool.
3. Commit, or roll back on error or `DryRun`.

`TxReport{T, Instant, Asserted, Existing, Retracted[(eid, kind)], Superseded[(old,new)], DryRun}`.

A process-wide `sync.Mutex` per `KnowledgeGraph` serializes writers inside one
process so they do not spin on `SQLITE_BUSY`.

`:memory:` databases get `SetMaxOpenConns(1)` in `OpenDB`; otherwise every pool
connection would open its own empty database.

## Verbs

All follow Tiramemsu's `time-model.md#Operations`; the differences are listed
under "Deviations".

- **Assert:** reject reserved predicates (R14) and dead or self statement
  endpoints (R15); look up a live `(s,p,o)` with overlapping valid time
  (`live_spo`), return it; else check `unique`, apply `cardinality one`
  replacement, insert. Overlap:
  `(a.from IS NULL OR b.to IS NULL OR a.from < b.to) AND (b.from IS NULL OR a.to IS NULL OR b.from < a.to)`.
- **Create:** the same checks, always inserts.
- **Retract / cascade:** BFS from the root over live rows with `s = x` or
  `o = x` (x as Statement value) and rows `D` with a live `(D sys:derived_from x)`;
  visited set; frontier expanded in eid order in chunks of 500; set
  `t_ret = t`, `ret_kind`; abort with `ErrCascadeLimit` past `MaxCascade`.
- **Supersede:** cascade set C (root first). Drop set: derived statements
  reached through a support link other than the root, plus everything whose
  subject or object is in the drop set (closure inside C). Retained R = C − drop.
  Retract all of C with kind supersede. Insert R in topological order of its
  references inside R (a member is inserted after every retained statement its
  s or o names), rewriting s/o through σ as it goes; the root gets the patch;
  then `(σ(root) sys:supersedes root)`. Content is immutable and endpoints must
  exist when written, so ordinary writes cannot create a reference cycle and a
  topological order always exists. A patch object that names a member of C
  (which would make the replay cyclic) is rejected with `ErrPatchIntoCascade`.
- **Confirm:** live check, then `Create(StmtRef(eid), sys:confirmed_by, Tx(t))`.
- **Derive:** supports must be live statements, deduplicated; find a live
  statement with equal content and overlap whose live `derived_from` set equals
  the support set exactly; else insert it plus one link per support.
- **Schema:** `SetPredicate(p, PredicateSchema{One, Unique})` writes
  `(p sys:cardinality "one")` / `(p sys:unique true)` as ordinary statements
  (versioned), after checking live data does not already violate them. Read per
  transaction and cached for its lifetime.

## Views

`View{mode: now|asOf|history, t, validAt *int64}` builds one SQL predicate,
applied to every scan (Tiramemsu `view.rs#scan_predicates`):

| Mode | Predicate |
|---|---|
| now | `t_ret IS NULL` |
| asOf(t) | `t_add <= t AND (t_ret IS NULL OR t_ret > t)` |
| history | `1` |
| + validAt(ms) | `(v_from IS NULL OR v_from <= ms) AND (v_to IS NULL OR v_to > ms)` |

Reads: `About(entity, direction)` (statements with the entity as s or o,
excluding `sys:` predicates), `Match`, `Layers(eid)`, `Dependents(eid)`,
`Explain(eid)`, `Conflicts`, `TextSearch`, `Events(since)`. Decoding a page of
statements loads all terms in one `IN (…)` query.

## Legacy compatibility

| Old | New |
|---|---|
| `Add(TripleInput)` | `Transact{Assert(Ent(s), Ent(p), Ent(o), [ValidFrom, ValidTo))}` plus optional `confidence` / `source` layers; `TripleInput` gains `ValidTo`, `Confidence`, `Source`, `Literal` |
| `Query(entity, asOf, dir)` | `Now().ValidAt(asOf).About(...)`; **half-open** valid time instead of the old inclusive `valid_to >= asOf` |
| `Invalidate(s,p,o)` | supersede every live match with `v_to = now` (layers carried over) |
| `Timeline(entity)` | `History().About(...)` ordered by `t_add`, with status (live, ended, superseded → eid, retracted kind) |
| `Triple.ID` | `e<eid>`; `Triple` gains `EID`, `Confidence`, `Sources`, `Status`, `TAdd` |

`PalaceStore` loses `InsertTriple`, `QueryTriples`, `InvalidateTriple` and
`TimelineTriples`; nothing outside `kg.go` called them. `KGStats` moves to the
engine. `NewKnowledgeGraph(store)` reaches the `*sql.DB` through an optional
`interface{ DB() *sql.DB }` that `SQLitePalaceStore` implements; without it
every KG call returns `ErrKGUnavailable`.

## Migration of legacy triples

Migration 6 becomes a Go step (the migration list grows a `func(*sql.Tx) error`
form). In `extracted_at` order, one transaction per legacy row,
`author = "migration"`, `reason = "import legacy triple <id>"`,
`instant = max(extracted_at_ms, previous + 1)`. Each row becomes
`create(Ent(s), Ent(p), Ent(o), [valid_from, valid_to))`, plus a `source_file`
layer when set. Rows whose `valid_to` was set by `Invalidate` stay live with a
closed valid interval — which is what they meant. Unparseable times are stored
as unbounded and counted in the migration log entry (session logger is not
available in `OpenDB`, so the count goes into the tx `reason`).

## Agent tools

| Tool | Change |
|---|---|
| `palace-kg-add` | + `valid_to`, `confidence` (0–1), `source`, `literal`, `about` (eid: annotate a statement, `subject` ignored), `support` (eids: derive), `confirm_existing` |
| `palace-kg-query` | + `tx_as_of` (tx number or time), `text` (FTS search instead of entity); rows show eid, confidence, sources |
| `palace-kg-revise` | new: `action` supersede (`eid`, `object`, `valid_from`, `valid_to`), confirm (`eid`), retract (`eid`); `reason`, `source`; reports cascaded/replayed eids |
| `palace-kg-explain` | new: `eid`, `tx_as_of`; statement, layers, supports, lineage, confirmations, dependents |
| `palace-kg-conflicts` | new: `subject`, `predicate`, `limit` |
| `palace-kg-invalidate`, `-timeline` | same inputs, richer output |

Outputs stay `{content: string}` markdown like the other palace tools, so the
compactor and TUI are unaffected. Tool handlers take `TxMeta{Author, Source}`
from the ADK context (`AgentName()`, `SessionID()`).

## CLI

`pi memory kg explain <eid>`, `pi memory kg conflicts [--subject] [--predicate]`,
`pi memory kg log [--since t]`, `pi memory kg query --tx-as-of`.

## Files

`kg_value.go` (ObjectID, Value, terms), `kg_schema.go` (migration SQL + legacy
import), `kg_tx.go` (Transact, report), `kg_verbs.go` (assert, create, retract,
cascade, supersede, confirm, derive), `kg_schema_flags.go` (predicate schema),
`kg_view.go` (views and reads), `kg_explain.go` (explain, conflicts, text),
`kg.go` (legacy facade on top), `tool_kg.go` + `tool_kg_revise.go`,
`internal/cli/memory_kg.go`. Tests beside each.

## Deviations from Tiramemsu, on purpose

- Tx metadata is three columns, not triples on the tx node. Enough for
  author/source/reason; arbitrary tx metadata is deferred.
- Rolled-back ids (dry run, failed tx) may be reissued: `AUTOINCREMENT` only
  protects committed ids. Dry-run reports therefore say their eids are
  provisional.
- No term dictionary inlining beyond Int/Bool/Time; no language tags or typed
  literals.
- Format/version handled by the palace migration table, not a `meta` table.
- Schema: only `one` and `unique`.
- A supersede patch whose object is inside the root's cascade set is rejected
  (Tiramemsu substitutes it through σ); without it no reference cycle can exist.

## Review resolution

Codex reviewed this plan (`research/codex-plan-review.md`). Every finding was
checked against the code and the Tiramemsu source and accepted. Where this
section and the text above disagree, this section wins.

| # | Finding | Resolution |
|---|---|---|
| 1 | Supersede skips predicate constraints | The patched root runs the same `unique` check and `cardinality one` replacement as assert, ignoring members of its own cascade set. Tests: unique collision, widening over another `one` value |
| 2 | Endpoint liveness checked before replacement | Endpoints (and derive supports) are re-checked after cardinality replacement, immediately before insert |
| 3 | Replace guards miss unique keys | `kg_term` guard checks `id` **or** `(tag, lex)`; `kg_tx` guard checks `t` **or** `instant`; `kg_stmt` guard checks `eid`. Tests replace with omitted and different keys |
| 4 | Backdating through raw SQL | Port Tiramemsu's date guards: `kg_meta(key, value)` holds `last_t`, the committed watermark. An insert into `kg_stmt` must use `t_add = max(kg_tx.t) > last_t`; a retraction must use `t_ret = max(kg_tx.t) > last_t`; a new `kg_tx` row must be `last_t + 1`; `last_t` only advances to `max(t)` (done just before commit). Migration follows the same protocol |
| 5 | PRAGMAs only on one pool connection | `OpenDB` passes `busy_timeout`, `foreign_keys` and `journal_mode` as `_pragma=` DSN parameters, so every connection gets them. The in-process mutex is dropped: the first statement of every write transaction is the `kg_tx` insert, so the write lock is taken first and waits on `busy_timeout`. Test: two handles on one file writing concurrently |
| 6 | Old binaries write the legacy table | After import, triggers on `triples` reject INSERT/UPDATE/DELETE with an "upgrade pi" message. `migrate` refuses a database whose version is newer than it knows. Test: old-style insert fails after upgrade |
| 7 | FTS external content mismatch | Standalone FTS5 table (`kg_term_fts(lex)`, rowid = term id) filled by the insert trigger for entity and string terms only. Test: `integrity-check` passes; numbers are not searchable |
| 8 | No snapshot for composite reads | Every read operation (`About`, `Explain`, `Conflicts`, …) runs inside one read-only `sql.Tx`; all its scans use it |
| 9 | Events filtered by statement visibility | `Events(since, until)` reads the whole log regardless of view, ordered `(t, op: assert before retract, eid)`. Test: assert and retract in one tx |
| 10 | Conflicts on History | Rejected with `ErrConflictsOnHistory` |
| 11 | Schema cache goes stale | No cache; schema is read per write |
| 12 | Interval validation | `v_from < v_to` required whenever both are set, for assert/create/derive/supersede patches; `ErrEmptyInterval` |
| 13 | Empty support set | `Derive` requires at least one support: `ErrNoSupport` |
| 14 | Confirm inflates counts | Confirm uses the engine's idempotent assert: one link per (statement, tx) |
| 15 | Invalidate extends ended episodes | `Invalidate` supersedes only live matches whose `v_to` is NULL, as today |
| 16 | Legacy extraction time lost | `kg_legacy_triple(legacy_id PK, eid, extracted_at)` keeps the original id and timestamp; `Triple.ExtractedAt` reads it when present, otherwise the tx instant |
| 17 | Legacy `sys:` predicates | Engine predicates contain upper-case letters (`sys:Supersedes`, `sys:ConfirmedBy`, `sys:DerivedFrom`, `sys:Cardinality`, `sys:Unique`). `entityID` lower-cases every user name, so a user or legacy predicate can never equal one; nothing is rejected or hidden by prefix. `About` hides engine predicates by term id |
| 18 | Removed store methods used by tests | Those tests move to the engine API and keep their coverage |
| 19 | Stats compatibility | `TripleCount` = live fact statements (no layers, no engine predicates); `ActiveTriples` = those with `v_to IS NULL`; new fields `Statements`, `Layers`, `Retracted`, `Transactions`. Test status after invalidate and supersede |
| 20 | Required tool fields | Alternative inputs (`subject` vs `about`, `entity` vs `text`) are `omitempty`, validated in the handler, and tested through `tool.Run` |
| 21 | Runtime `DB()` probing | `DB() *sql.DB` joins the `PalaceStore` interface (compile-time); `ErrKGUnavailable` goes away |
