# Feature map: Tiramemsu → palace knowledge graph

Each row is a Tiramemsu capability, where it is specified upstream, what the
palace has today, and the decision. "Port" means a Go implementation in
`internal/palace`; "Adapt" means a reduced or reshaped version; "Defer" means
not in this change.

Upstream references are under `tmp/tiramemsu/lat.md/` and
`tmp/tiramemsu/openspec/specs/`.

| # | Capability | Upstream | Palace today | Decision |
|---|---|---|---|---|
| 1 | Statement identity: every fact has an integer eid; an eid can be subject or object | `data-model.md#Statements`, `statement-lifecycle` | Text id `t_<s>_<p>_<o>_<md5>`; cannot be referenced by other triples | **Port.** `kg_stmt.eid`, values encoded as tagged `ObjectID` (`id<<4 \| tag`) |
| 2 | Layers: statements about statements (confidence, source, beliefs), any depth | `data-model.md#Layers`, `layer-hops` | None | **Port.** `Annotate(eid, p, o)` = assert with a statement subject |
| 3 | Transaction time: gap-free `t`, strictly increasing `instant`, tx metadata | `time-model.md#Transaction Time`, `transactions` | Only `extracted_at` text | **Port**, tx metadata as columns (`author`, `source`, `reason`) instead of triples on the tx node |
| 4 | Valid time, half-open `[v_from, v_to)`, epoch ms, immutable | `time-model.md#Valid Time` | Text columns, `valid_to` rewritten by `Invalidate`, inclusive `<=` / `>=` | **Port.** Closing an interval becomes a supersede |
| 5 | Views: now, as_of(t / instant), history × valid_at | `temporal-views`, `view-scoped-scans` | `as_of` filters valid time only | **Port.** `View{Now, AsOf, History}` + `ValidAt` |
| 6 | Never forget: triggers reject DELETE, a second retraction, content updates, id reuse | `storage.md#Invariant Triggers`, `never-forget` | Rows updated in place | **Port** the triggers onto `kg_stmt`, `kg_term`, `kg_tx` |
| 7 | Idempotent assert (same s,p,o, overlapping valid time → existing eid) | `time-model.md#Assert` | Idempotent on `valid_to IS NULL` only | **Port**, with `on_existing = confirm` |
| 8 | `create` for parallel edges | `time-model.md#Create` | None | **Port** (small; used by migration and replay) |
| 9 | Retract with cascade (statement subjects/objects, visited set, `max_cascade`) | `time-model.md#Cascade`, `retraction-cascade` | None | **Port** |
| 10 | Supersede: retract cascade set, replay layers on new eids with σ, patch root, `sys:supersedes` lineage | `time-model.md#Supersede`, `supersede` | `Invalidate` overwrites `valid_to` | **Port.** `Invalidate` becomes supersede with `v_to = now` |
| 11 | Confirm: `(eid sys:confirmedBy tx)` | `time-model.md#Confirm` | None | **Port** |
| 12 | Derived beliefs: `derive(s,p,o, support)`, conjunctive support, falls with any support, supersede drops derived dependents | `derived-beliefs.md`, `derived-beliefs` | None | **Port** (D45–D48); derived upsert (D51) and widening (D52) in phase 2 |
| 13 | Predicate schema: `sys:cardinality one`, `sys:unique` | `data-model.md#Predicate Schema`, `predicate-schema` | None | **Port** (phase 2 adds `valueType`, `subjectType`, `isEdge`, `sensitive`, unique upsert) |
| 14 | Dependents: what a retraction would take with it, read-only, any view | `time-model.md#Dependents`, `statement-dependents` | None | **Port** |
| 15 | Event log view `event(t, eid, op, kind)` | `storage.md#Event View` | None | **Port** as a SQL view + `Events(since)` |
| 16 | Conflict review: same s,p, distinct o, overlapping valid time, with evidence | `memory-conflict-review` | None | **Port** (evidence = confidence, sources, confirmations, tx author) |
| 17 | Text recall over string values, view-aware | `text-retrieval`, `storage.md#Text Index` | Drawers have FTS5; KG has none | **Adapt:** FTS5 over entity names and string literals in `kg_term`; hits filtered by view |
| 18 | Dry run (`TxOptions.dry_run`) | `speculative-transactions` | None | **Port** (rollback; burned ids not guaranteed, see design) |
| 19 | Named graphs as `sys:inGraph` layers | `named-graphs` | Wings/rooms exist for drawers only | **Port** (phase 2): `sys:InGraph` membership layers, `View.InGraph`, `palace-kg-graph` |
| 20 | Fact bundles (export a fact with its layers, idempotent import) | `fact-bundles` | None | **Port** (phase 2): JSON bundles; N-Triples form not ported |
| 21 | Volatile table | `storage.md#Volatile Table` | None | **Port** (phase 2) |
| 22 | SPARQL, openCypher, logical IR, planner | `sparql-*`, `cypher-*`, `query-ir` | None | **Adapt** (phase 2): one Go IR with SPARQL and read-only Cypher subsets; out-of-subset constructs are rejected before execution |
| 23 | Paths / time-respecting paths | `path-*`, `temporal-language-paths` | Room BFS in `graph.go` | **Port** (phase 2): reach, shortest, all shortest, trails, time-respecting |
| 24 | Datalog, N3, ELPI reasoning and constraints | `datalog-*`, `n3-*`, `elpi-*` | None | **Port** (phase 2): stratified Datalog with constraints and materialization, N3 Horn subset, ELPI Horn subset with fuel |
| 25 | Saved answers with invalidation | `saved-answer-invalidation` | None | **Port** (phase 2) |
| 26 | Statement vectors / hybrid recall | `vector-retrieval`, `hybrid-recall` | Drawers have embeddings | **Port** (phase 2): exact cosine, RRF hybrid recall |
| 27 | redb engine, WASM host, bindings, MCP server, CLI `tm` | `redb-storage`, `wasm-sqlite-host`, ... | n/a | **Not applicable** |
| 28 | Crypto-shredding erasure | `time-model.md#Erasure` | None | **Port** (phase 2): per-subject keys, deterministic AES-GCM SIV, `pi memory kg erase` |
| 29 | Query budgets | `query-budgets` | None | **Port** (phase 2): operation-wide row/byte budgets, context cancellation |

## What the palace gains, in agent terms

- "How sure am I and where did I read it?" — confidence and source layers on
  every fact, shown by `palace-kg-query` and `palace-kg-explain`.
- "I was wrong." — `palace-kg-revise action=supersede` corrects a fact and carries
  its layers over; the old version stays visible in `tx_as_of` and timeline.
- "What did I believe before?" — `tx_as_of` on query.
- "What breaks if this is wrong?" — derived facts fall with their support;
  `palace-kg-explain` lists dependents before a retraction.
- "Where does memory disagree with itself?" — `palace-kg-conflicts`.
- "A second source agrees." — confirm, counted in the evidence.
