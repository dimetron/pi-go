# Summary — phase 2

Every capability phase 1 deferred is now in `internal/palace`, in pure Go on
the same `modernc.org/sqlite` database: no cgo, no new module dependency.
Plan: `design.md`, `plan.md`. Codex reviewed the plan (`codex-plan-review.md`,
25 findings, all accepted; resolutions at the end of `design.md`).

| Capability | Files | Surface |
|---|---|---|
| Migration 7: volatile, seal keys, vectors, saved answers (with never-forget guards on keys and vectors) | `kg_schema2.go`, `db.go` | — |
| Predicate schema: value type, subject type, is-edge, sensitive; unique upsert | `kg_predschema.go` | `pi memory kg schema` |
| Derived upsert (D51), widening (D52) | `kg_predschema.go`, `kg_verbs.go` | — |
| Named graphs | `kg_graph.go`, `kg_view.go` | `palace-kg-graph`, `graph` on add/query/select/path/recall/rules, `pi memory kg graphs` |
| Volatile state | `kg_volatile.go` | `pi memory kg volatile` |
| Crypto-shredding | `kg_seal.go` | `pi memory kg erase --yes` (CLI only, on purpose) |
| Query IR, SPARQL subset, Cypher read subset, budgets | `kg_query.go`, `kg_sparql.go`, `kg_cypher.go`, `kg_budget.go` | `palace-kg-select`, `pi memory kg select` |
| Paths, time-respecting paths | `kg_path.go` | `palace-kg-path`, `pi memory kg path` |
| Datalog (stratified negation, constraints, materialize), N3 Horn subset | `kg_rules.go`, `kg_rules_store.go`, `kg_n3.go` | `palace-kg-rules`, `pi memory kg rules` |
| ELPI Horn subset (SLD with fuel, `tm.triple`) | `kg_elpi.go` | `palace-kg-rules syntax=elpi` |
| Saved answers with conservative invalidation | `kg_answers.go` | `palace-kg-select save_as/check`, `pi memory kg answers` |
| Statement vectors, hybrid recall (RRF) | `kg_vector.go` | `palace-kg-recall` |
| Fact bundles | `kg_bundle.go` | `palace-kg-bundle`, `pi memory kg bundle` |

Fixes to phase 1 found on the way: `AsOf` reads showed retractions made after
their bound (Codex #21); `annotateEvidence` replaced a predicate's whole schema
when it set confidence to cardinality one.

## Subsets, stated

- SPARQL: no CONSTRUCT/ASK/DESCRIBE, property paths, BIND, VALUES, subqueries,
  aggregates other than COUNT, dataset clauses other than `GRAPH <g>`.
- Cypher: read only; no variable-length relationships, WITH, UNWIND, CALL.
- N3 and ELPI: Horn clauses only; ELPI has no λ-terms, higher-order
  unification, constraints or modules. ELPI programs are not stored or
  materialized (Datalog and N3 are).
- Rules: `Materialize` rejects programs with negation (upstream
  `N3NotMaterializable`) and reads only the now view.
- Bundles: JSON only (no N-Triples).

## Verification

- Tests per capability (`kg_*_test.go`, `tool_kg_*_test.go`,
  `internal/cli/memory_kg2_test.go`); every new tool runs through `tool.Run` in
  `tool_invoke_test.go`. `make build test lint vet` clean.
- Guards seen failing once: sealing off → plaintext found in `kg_term`/FTS;
  negative-query invalidation off → `TestAnswers_NegativeQueryStaleOnRetraction`;
  MinHops dropped from the path visited key → five path tests; own-conclusions
  filter off in Materialize → `TestRules_OwnConclusionsAreNotInput`;
  sealed filter off in embedding → `TestVector_NeverEmbedsSealedValues`;
  `IncludeSensitive` check off → `TestBundle_Sensitive`; plus the query
  agent's dedup mutation.

## Not verified

- Performance at scale: rules load stored predicates fully into memory; path
  search issues one lookup per node and predicate; vector search is exact.
  Nothing was measured.
- Agent behaviour with the six new tools (`make eval-tools` not run).
- WAL checkpoint after erase is tested only for success, not with a busy reader.
