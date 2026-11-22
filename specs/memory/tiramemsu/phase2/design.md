# Design — phase 2: the deferred capabilities

Phase 1 (`../design.md`) ported the statement graph, the verbs, the views,
explain/conflicts and text search. This phase ports every row of
`../research/feature-map.md` that phase 1 deferred. Each one is a Go
implementation in `internal/palace` on the same `modernc.org/sqlite` database:
no cgo, no new module dependency, no second binary.

"Subset" below means a deliberately smaller surface than upstream, stated
exactly, so a caller gets a clear error for anything outside it instead of a
wrong answer. Upstream references are under
`tmp/tiramemsu/openspec/specs/<capability>/spec.md`.

## Storage: migration 7

One migration adds only side tables and engine predicates. No phase 1 table
changes shape.

```sql
-- Volatile state (#21): high-churn values outside history.
CREATE TABLE kg_volatile (
	s          INTEGER NOT NULL,   -- objectID of an entity or statement
	key        INTEGER NOT NULL,   -- objectID of an entity (the key)
	o          INTEGER NOT NULL,   -- objectID of the value
	updated_at INTEGER NOT NULL,   -- instant of the writing transaction
	PRIMARY KEY (s, key)
);

-- Crypto-shredding (#28): one key per data subject; the only erasable bytes.
CREATE TABLE kg_seal_key (
	subject     INTEGER PRIMARY KEY, -- objectID of the data-subject node
	key         BLOB NOT NULL,       -- 64 bytes; zeroed on erasure
	created_t   INTEGER NOT NULL,
	destroyed_t INTEGER
);
-- triggers: no DELETE; UPDATE only sets destroyed_t (once, to the open tx)
-- and replaces key with zeroblob(64) in the same statement.

-- Statement vectors (#26): derived records, not statements.
CREATE TABLE kg_stmt_vec (
	eid   INTEGER NOT NULL,
	model TEXT NOT NULL,
	dim   INTEGER NOT NULL,
	vec   BLOB NOT NULL,           -- little-endian float32
	t     INTEGER NOT NULL,        -- transaction it became visible in
	PRIMARY KEY (eid, model)
);

-- Saved answers (#25): derived records, never consume a transaction.
CREATE TABLE kg_saved_answer (
	name        TEXT PRIMARY KEY,
	lang        TEXT NOT NULL,     -- sparql | cypher | datalog
	query       TEXT NOT NULL,
	view        TEXT NOT NULL,     -- JSON of the view (mode, t, valid_at)
	result      TEXT NOT NULL,     -- JSON rows
	deps        TEXT NOT NULL,     -- JSON eids the result rests on
	preds       TEXT NOT NULL,     -- JSON predicate objectIDs it reads; null = any
	negative    INTEGER NOT NULL,  -- query has NOT/OPTIONAL/aggregate
	checkpoint  INTEGER NOT NULL,  -- last transaction checked
	stale       TEXT               -- NULL = fresh, else the reason
);
```

`PRAGMA secure_delete = ON` joins the DSN pragmas so a zeroed key leaves no
copy in freed pages. Value tag 9 is `SEALED` (a `kg_term` row whose `lex` is
base64 ciphertext; never FTS-indexed, never inlined).

New engine predicates, upper-case so no user predicate can collide (phase 1
resolution 17): `sys:InGraph`, `sys:ValueType`, `sys:SubjectType`,
`sys:IsEdge`, `sys:Sensitive`, `sys:RuleText`, `sys:EmbeddingModel`.

## A. Named graphs (#19)

A graph is an entity. Membership is a layer statement
`(eid sys:InGraph g)`, so it is bitemporal and versioned like any fact.

- `KGTx.AddToGraph(eid, g)` asserts membership (idempotent);
  `KGTx.RemoveFromGraph(eid, g)` retracts the live membership statement.
  `KGTx.DropGraph(g)` retracts every live membership of `g` (facts stay).
- `View.InGraph(g ...string) View` restricts `Match`, `About`, `TextSearch`,
  query-language patterns, paths and rule input to statements with a
  membership selected **by the same view** in any of the graphs. No graph =
  the default graph = the union of everything (upstream "default graph is the
  union").
- `View.Graphs(ctx)` lists graphs with live member counts.
- Retracting a fact cascades to its membership (it is a layer); superseding
  it replays membership onto the new version.
- `sys:InGraph` is subject to predicate schema like any predicate; it is
  hidden from `About` like other engine predicates.

Not ported: SPARQL dataset clauses (`FROM`, `FROM NAMED`, `GRAPH ?g`) beyond
`GRAPH <g> { … }` in the SPARQL subset (section D).

## B. Fact bundles (#20)

A bundle is a fact with everything stated about it, as a JSON tree:

```go
type Bundle struct {
	Version int          `json:"version"` // 1
	Root    BundleStmt   `json:"root"`
}
type BundleStmt struct {
	ID      string       `json:"id"`        // bundle-local "b1", "b2"…
	S, P, O BundleValue  `json:"s"…`        // statement subjects/objects nest
	Valid   *BundleValid `json:"valid,omitempty"`
	Layers  []BundleStmt `json:"layers,omitempty"`
}
type BundleValue struct {
	Value              // entity / literal
	Stmt *BundleStmt `json:"stmt,omitempty"` // a statement object, inlined
	Ref  string      `json:"ref,omitempty"`  // a statement already in the bundle
}
```

- Members (`View.Bundle(ctx, eid)`): the root, its layers to any depth, and
  any statement a member references as an object (inlined once, later
  occurrences use `ref`). Selected by the view, so a past bundle comes from
  `AsOf(t)`.
- Excluded: `sys:Supersedes`, `sys:ConfirmedBy`, `sys:DerivedFrom` (they
  name local eids/transactions and mean nothing in another graph);
  memberships are included.
- `KGTx.ImportBundle(b) (map[string]int64, error)` imports depth-first through
  idempotent `Assert`, so importing twice changes nothing the second time.
  A `ref` to an id not yet imported in the walk is a reference cycle and is
  rejected with `ErrBundleCycle` before anything is written.

## C. Volatile state (#21)

Spec: `volatile-state`. `KGTx.SetVolatile(s, key, o)` upserts with the
transaction's instant; `KGTx.ClearVolatile(s, key)` deletes (succeeds when
absent). Writes are inside the transaction (rolled back on error, discarded by
dry run). No eid, no event, no `About`/`Match` result.
`View.Resolve(ctx, s, key) ([]Value, error)`: objects of statements `(s key ?)`
selected by the view; if none and the view is `Now` (no `ValidAt`), the
volatile value; otherwise nothing.

## D. Query languages (#22): one IR, SPARQL and Cypher subsets

`kg_query.go` defines a small logical IR evaluated in Go over a `View`:

```
Query   = Select{Vars, Distinct, Where Group, OrderBy, Limit, Offset, Count}
Group   = []Elem           // conjunction, left to right
Elem    = Triple{S,P,O Term} | Filter{Expr} | Optional{Group} | Union{[]Group}
          | NotExists{Group}
Term    = Var | Const(Value)
Expr    = = != < <= > >= && || ! , bound(?x), regex(?x,"…"), contains, strstarts
```

Evaluation is index nested-loop join: each triple pattern becomes one
`Match` call with the current binding's constants (so the existing indexes
are used); `Optional` is a left join; `Union` concatenates; `NotExists` is
anti-join; filters run when their variables are bound (three-valued: an
unbound/ill-typed comparison is false). Each solution row also carries the eids
of the statements it matched (used by saved answers and `explain`). Results
are rows of `map[string]Value`, deterministic order by eid unless `OrderBy`.

**SPARQL subset** (`ParseSPARQL`): `PREFIX`, `SELECT [DISTINCT] ?v… | *`,
`SELECT (COUNT(*) AS ?n)`, `WHERE { … }` with triple patterns (`.`, `;`
and `,` abbreviations), `FILTER`, `OPTIONAL`, `UNION`, `FILTER NOT EXISTS`,
`GRAPH <g> { … }`, `ORDER BY [DESC] ?v`, `LIMIT`, `OFFSET`. Terms: `<iri>` and
`prefix:name` → entity (local name lower-cased like `entityID`), `"lit"`,
integers, decimals, `true/false`. `<< s p o >>` (RDF-star) as a subject binds
the statement. Anything else (CONSTRUCT, property paths, BIND, VALUES,
aggregates other than COUNT, subqueries) fails with
`ErrUnsupportedQuery{Pos, Construct}` before execution.

**Cypher subset** (`ParseCypher`): `MATCH` one or more comma-separated
patterns `(a)-[:p]->(b)`, `(a)<-[:p]-(b)`, `(a)-[r:p]->(b)` (r binds the
statement eid), `(a {name: 'x'})`, chains `(a)-[:p]->(b)-[:q]->(c)`;
`OPTIONAL MATCH`; `WHERE` with comparisons, `AND/OR/NOT`, `r.confidence`
(a layer value of a relationship); `RETURN a, b.name AS n` / `count(*)`;
`ORDER BY`, `SKIP`, `LIMIT`. Read only: CREATE/MERGE/SET/DELETE, variable
length relationships, `CALL`, `UNWIND`, `WITH` fail with
`ErrUnsupportedQuery`. Nodes are entities; a relationship is a statement whose
object is an entity, a property is a statement whose object is a literal
(upstream "statement classification").

Both compile to the same IR, so they agree on any query expressible in both
(tested).

## E. Paths (#23)

`View.Paths(ctx, PathQuery) ([]Path, error)`:

```go
type PathQuery struct {
	From, To   string     // entity names; at least one required
	Predicates []string   // empty = any non-engine entity-object predicate
	Direction  string     // out | in | both
	MinHops, MaxHops int  // default 1..6; MaxHops capped at 16
	Mode       PathMode   // Reach | Shortest | AllShortest | Trails
	TimeRespecting bool   // each step's valid interval must start at or after
	                      // the previous step's start, and overlap it
	Limit      int        // results, default 50
	MaxFrontier int       // memory guard, default 100k states
}
```

BFS over `Match` with the view (and its graph restriction). Cycles terminate:
reach/shortest keep a visited set per node (per (node, arrival time) when time
respecting); trails forbid repeating an edge eid. Exceeding `MaxFrontier` fails
with `ErrBudget`; `ctx` is checked at every frontier expansion. A `Path` is the
list of statement eids and nodes; results ordered by length, then eids.
Time-respecting search returns the earliest arrival per target.

## F. Rules (#24): Datalog, N3 syntax, constraints

`kg_rules.go`. A program is a list of rules over atoms:

```
reports(X, Y) :- manager(X, Y).
reports(X, Z) :- manager(X, Y), reports(Y, Z).
peer(X, Y)    :- reports(X, M), reports(Y, M), X != Y, not reports(X, Y).
:- age(X, A), A < 0.                       % constraint (denial)
```

- Stored atoms: `p(S, O)` reads statements with predicate `p` in the view
  (and graph); `triple(S, P, O)` reads any predicate. Rule-defined
  (intensional) predicates must not also be stored predicates in the same
  program's input unless listed; head predicates are entity names.
- Builtins: `= != < <= > >=`, `contains(X,"s")`.
- Safety: every head and builtin/negated variable occurs in a positive body
  atom (`ErrUnsafeRule`). Negation must be stratified (`ErrUnstratified`).
  No function symbols, so evaluation terminates.
- Evaluation: stratified semi-naive bottom-up in Go; `MaxFacts` budget
  (default 100k) → `ErrBudget`. Every derived tuple keeps one support set: the
  eids of stored statements in its first derivation (union through
  intensional premises), and its valid time is the intersection of those
  statements' valid intervals (pointwise valid time; empty intersection →
  not derived).
- `View.Solve(ctx, prog, goal)` answers a goal `reports(alice, Y)` → rows.
- `View.Check(ctx, prog)` returns constraint violations, each with the eids
  that witness it.
- `KGTx.Materialize(prog, view)` writes every derived tuple whose head is not
  already stored as a live derived statement with the same support through
  `Derive` (so conclusions fall with any premise); a stored conclusion whose
  valid interval is narrower than the new one is widened (D52, below);
  derived conclusions written by an earlier run of the same program that are
  no longer derivable are retracted (reconcile). Negated or constraint-only
  programs materialize only their positive strata. Its own previous
  conclusions are not input (filtered by `sys:DerivedFrom` provenance on the
  program statement).
- Programs are stored as statements: `(program:<name> sys:RuleText "…")`,
  so they have transaction time and a `Materialize` run can be traced to a
  program version. The program statement joins every conclusion's support
  set, so retracting or superseding the program withdraws its conclusions
  (upstream "conclusions fall with premises and programs"), and "own
  conclusions are not input" is a filter on that program eid in the support
  links. `Materialize` of an unstored program text is rejected.
- **N3** (`ParseN3`): `@prefix`, rules `{ ?a :p ?b . ?b :p ?c } => { ?a :q ?c } .`
  and facts are compiled to the same rule IR (Horn subset; no `log:` builtins
  except `math:lessThan`/`greaterThan`/`notEqualTo`). Tested to agree with the
  Datalog form.
- **ELPI**: not ported as a language. Upstream ships it as an off-by-default
  cargo feature wrapping a third-party λProlog engine; a pure-Go λProlog is a
  project of its own. What agents use it for — goal queries with recursion and
  a fuel limit, constraint checks, conclusions with stated support — is
  covered by `Solve`, `Check` and `Materialize` above. Feature map row 24
  records this as **Adapt**.

## G. Saved answers (#25)

`kg_answers.go`, table `kg_saved_answer`, spec `saved-answer-invalidation`.

- `kg.SaveAnswer(ctx, name, lang, query, view)` runs the query and stores the
  result, the eids it matched, the predicates its patterns read (null when a
  pattern has a variable predicate), whether it has negation/optional, and
  `checkpoint = last_t`.
- `kg.CheckAnswer(ctx, name)` reads events after the checkpoint:
  - views fixed in transaction time (`AsOf(t)` with no clock dependence) stay
    fresh forever;
  - a retraction of a dep eid → stale with that event;
  - an assert whose predicate is in `preds` (or `preds` null, or `negative`)
    → stale ("may have new rows");
  - otherwise fresh, and the checkpoint advances. Idempotent: checking twice
    gives the same verdict.
- `kg.RefreshAnswer` re-runs; only success clears `stale` and moves the
  checkpoint. `kg.DeleteAnswer`, `kg.ListAnswers`.
- None of these write `kg_tx`, `kg_stmt` or `kg_term`.

## H. Statement vectors and hybrid recall (#26)

`kg_vector.go`. The embedder is the palace's existing `Embedder`.

- Statement text = `"<s> <p> <o>"` rendered with `Value.String`.
- `kg.EmbedStatements(ctx, emb, model, batch)` backfills vectors for live
  statements without one for that model (and therefore for the new eid after a
  supersede), `t` = current `last_t`. Caller-supplied vectors:
  `kg.PutVector(ctx, eid, model, vec)`.
- `View.VectorSearch(ctx, emb, model, query, k)`: exact cosine over
  vectors whose statement is selected by the view and whose `t <=` the view's
  transaction time. Returns `[]Scored{Statement, Score, Rank}`.
- `View.Recall(ctx, emb, model, query, k)`: reciprocal-rank fusion (k=60) of
  `TextSearch` and `VectorSearch`. Each hit reports its rank in each channel
  and the fused score is labelled as RRF — no invented similarity. If a
  channel is unavailable (no embedder, no vectors for the model) the call
  fails (`ErrChannelUnavailable`) rather than silently degrading; the tool
  falls back explicitly to text-only and says so.
- The embedding model is recorded as `(model:<name> sys:EmbeddingModel dim)`
  on first use, so a vector set can be traced.

## I. Crypto-shredding (#28)

`kg_seal.go`. Spec: `time-model.md#Erasure`.

- `SetPredicate(p, {Sensitive: true})` marks `p`. A literal object of a
  sensitive predicate is sealed: data subject = follow statement subjects down
  to a node; key = that subject's `kg_seal_key` row (created on first use, 32
  random bytes AES-256 + 32 bytes HMAC). Encryption is deterministic SIV-style
  with the standard library: `iv = HMAC-SHA256(macKey, tag||plaintext)[:12]`,
  `ct = AES-GCM(encKey, iv, plaintext, aad = subject)`, stored `iv||ct`. One
  value under one key always gives the same term, so assert stays idempotent.
- Decoding a sealed term decrypts with the live key; a destroyed key decodes
  as `Str("sys:Erased")`. Sealed values are never FTS-indexed or embedded
  (vector text uses the decoded value, so erased values embed as
  `sys:Erased`; `Erase` also drops that subject's statement vectors —
  they are derived records).
- `KGTx.Erase(subject)` zeroes the key and sets `destroyed_t`; recorded in
  the transaction's author/reason. Statements, eids, times, history stay.
- Entity names are never sealed (upstream limit).
- Turning `Sensitive` on for a predicate that already has plaintext literal
  objects is rejected (`ErrSchemaViolated`): they cannot be sealed after the
  fact without rewriting history.

## J. Derived upsert and widening (D51, D52)

- `KGTx.UpsertDerived(p, o, support) (node Value, eid int64, err)`: returns
  the subject of a live `(n p o)` derived from exactly `support`; otherwise a
  fresh node `n = Ent("skolem:<uuid>")` and `Derive(n, p, o, support)`.
  Asserted keys and keys with other supports are not reused.
- Widening: `Supersede(eid, Patch{ValidFrom/ValidTo only})` on a derived
  statement replays its support links (phase 1 already keeps them; D52 adds
  the test and `Materialize` uses it).

## K. Predicate schema completion (#13)

`PredicateSchema` gains `ValueType` (`entity|string|int|number|bool|time|statement`),
`SubjectType` (`entity|statement`), `IsEdge` (stored, reported, no check), and
`Sensitive` (section I). Checks run in the upstream order: valueType,
subjectType, unique, cardinality. Setting a flag violated by live data is
rejected, as in phase 1. `KGTx.Upsert(p, o)` on a `unique` predicate returns
the subject of the live `(n p o)` or creates `n`; on a non-unique predicate it
fails with `ErrNotUnique`.

## L. Query budgets (#29)

`Budget{MaxRows int, MaxBytes int}` on `QueryOptions` for query languages,
paths, rules and recall; deadlines and cancellation come from `ctx` (checked
in every join loop, frontier expansion and fixpoint round). Exceeding a budget
returns `ErrBudget` (typed, with which limit); a truncated result is never
returned as complete. No budget = unchanged behaviour.

## Tools and CLI

Agents get capability, not knobs. New tools (all in `tools.go`):

| Tool | Does |
|---|---|
| `palace-kg-select` | `lang` = sparql \| cypher \| datalog, `query`, `goal`, view args (`tx_as_of`, `as_of`), `graph`; rows as a table |
| `palace-kg-path` | paths between entities (`from`, `to`, `predicates`, `mode`, `time_respecting`, `max_hops`) |
| `palace-kg-rules` | `action` = check \| materialize \| store, `name`, `program`, `syntax` = datalog \| n3, `dry_run` |
| `palace-kg-recall` | hybrid text+vector recall over facts, falls back to text when no embedder and says so |
| `palace-kg-graph` | `action` = add \| remove \| list, `eid`, `graph` |
| `palace-kg-bundle` | `action` = export \| import, `eid` / `bundle` |

Existing tools: `palace-kg-add` gains `graph`; `palace-kg-query` gains `graph`.
Volatile state, saved answers and erase are Go API plus CLI only — erase is
destructive and should be a human decision:
`pi memory kg select|path|rules|bundle export|import|erase|answers`.

## Errors

`ErrUnsupportedQuery` (with position), `ErrQueryParse`, `ErrBudget`,
`ErrUnsafeRule`, `ErrUnstratified`, `ErrBundleCycle`, `ErrNotUnique`,
`ErrChannelUnavailable`, `ErrErased` (writing to an erased subject).

## Review resolution

Codex reviewed this plan (`codex-plan-review.md`). Each finding was checked
against the code; #3 (`encode3` interns before any schema check, Supersede
encodes its patch directly) and #21 (`queryStatements` returns `TRet`,
`RetKind`, `RetractedAt` from after an `AsOf` bound) are confirmed in the
current files. All 25 are accepted. Where this section and the text above
disagree, this section wins.

| # | Resolution |
|---|---|
| 1 | `kg_seal_key` gets the phase-1 guard set: BEFORE INSERT rejects an existing `subject`; `created_t` and `destroyed_t` must be the open transaction (`max(kg_tx.t) > last_t`); no DELETE; UPDATE only sets `destroyed_t` once with `key = zeroblob(64)`. Tests: raw `INSERT OR REPLACE`, backdated create/destroy, second destroy |
| 2 | After an `Erase` commit the engine runs `PRAGMA wal_checkpoint(TRUNCATE)` and reports `EraseReport.Checkpointed`; a busy reader makes it `false` and the CLI says the key bytes may still be in the WAL until the next checkpoint. Test: raw WAL file has no key bytes after a successful checkpoint |
| 3 | One choke point: every object reaches the database through `w.encodeObject(s, p, o)`, which seals when `p` is sensitive. Assert, Create, Annotate, Derive, Supersede patches, UpsertDerived, Upsert and ImportBundle all use it. Test inspects `kg_term` and `kg_term_fts` after each path |
| 4 | `SetVolatile` on a sensitive key is rejected (`ErrSensitiveVolatile`) |
| 5 | `Erase` marks every saved answer stale and replaces its stored result with `null` (re-evaluation re-derives it, now decoding `sys:Erased`). Saving an answer whose rows contain a sealed value stores those cells as `sys:Sealed` placeholders, never plaintext |
| 6 | `Bundle` refuses a bundle containing sealed values unless `IncludeSensitive`; such a bundle marks those values `"sensitive": true`, and import requires the target predicate to be sensitive (`ErrSchemaViolated` otherwise), so they are resealed before interning |
| 7 | `match` with a literal constant on a sensitive predicate: with a bound subject, seal the constant under that subject's key and look up by id; with an unbound subject, scan `(p)` and compare decoded values |
| 8 | `Sensitive` together with `Unique` is rejected in either order (`ErrSchemaViolated`) |
| 9 | Vectors are written inside a KG transaction with its open `t` (`kg.EmbedStatements` opens one, author `palace-embed`); `kg_stmt_vec` gets immutability triggers (no delete, no update, no replace, `t` = open transaction). `PutVector` of the same vector is a no-op, a different one is `ErrVectorConflict` |
| 10 | Every composite operation (query evaluation, paths, rules, saved-answer save/check/refresh, bundle export, recall) runs on one `querier` from one `kg.read`; `Materialize` uses `w.q`. Test: single-connection `:memory:` palace and two-handle writer |
| 11 | Retractions also stale an answer that is `negative` and reads the retracted statement's predicate (or any predicate) |
| 12 | The saved view records its graph scope; with a graph scope, any `sys:InGraph` event stales the answer |
| 13 | Derivations are kept per (tuple, support set) as fragments, each with its own interval, up to `MaxFragments` (default 8) per tuple; `Materialize` writes each fragment through `Derive`. Tests: two disjoint proofs, reordered input gives the same result |
| 14 | Reconcile (retracting no-longer-derivable conclusions) runs only for a `Now` view with no graph scope; a restricted view only adds |
| 15 | The IR evaluator takes a dialect: SPARQL deduplicates solutions from statements with equal `(s,p,o)` (set of triples); Cypher keeps one row per eid (bag) and forbids binding the same eid twice within one `MATCH` (relationship isomorphism). Tests show the expected differences |
| 16 | Filters use Kleene three-valued logic (`true/false/unknown`); only the filter boundary turns `unknown` into rejection. Truth-table test |
| 17 | Time-respecting paths carry the arrival instant τ: an edge is usable when `v_to > τ` (open end = ∞), and τ becomes `max(τ, v_from)`. Tests `[1,5)→[0,2)` (usable) and `[1,2)→[5,6)` (waits) |
| 18 | Visited state is `(node, min(hops, MinHops))` (plus τ when time respecting). Test A→B direct does not suppress A→C→B with `MinHops = 2` |
| 19 | Bundle members: the root, its view-visible dependents (`Dependents`), and closure over subject and object statement references of every member, without expanding dependents of reference-only members |
| 20 | Membership statements in a bundle import through `AddToGraph`; any other engine predicate in a bundle is rejected before writing. Round-trip test with memberships |
| 21 | `queryStatements` under `AsOf(t)` masks `TRet`, `RetKind` and `RetractedAt` when `t_ret > t`. Test: the same `AsOf` row before and after a later retraction is equal |
| 22 | A saved `AsOf(t)` with `t > last_t` is stored with its bound frozen to `last_t` at save time (it then is fixed). Test `AsOf(last_t + 1)` |
| 23 | Vector text is built from the stored tags: a statement whose object is sealed is not embedded at all. Spy-embedder test covers live sealed values and erased values |
| 24 | `Budget` is one operation-wide counter (`budget.take(rows, bytes)`) passed to every lookup of the operation, `MaxBytes` counts decoded literal bytes. Tests: oversized literal; siblings each under the limit, together over |
| 25 | `PutVector` requires the model's recorded dimension, finite components and a non-zero norm (`ErrBadVector`). Tests for each, identical-write no-op, conflicting-write rejection |

Upstream-scope adjustments that follow: `Materialize` rejects programs with
negation (`ErrNotMaterializable`, like upstream `N3NotMaterializable`); `Solve`
and `Check` accept them, with negation evaluated over the whole view
(atemporal), which is documented on `Solve`.

## Implementation notes

Where the code differs from the text above: `palace-kg-select` takes `sparql`
and `cypher`; Datalog, N3 and ELPI goals go through `palace-kg-rules` (one tool
per kind of program). ELPI is implemented as a Horn-clause subset
(`kg_elpi.go`) rather than recorded as Adapt. Volatile state, saved-answer
management and erasure are CLI commands; agents reach saved answers through
`palace-kg-select save_as` / `check`. `Materialize` reads only the now view.
