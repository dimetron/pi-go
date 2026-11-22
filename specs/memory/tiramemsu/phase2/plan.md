# Plan — phase 2

Same branch (`feat/palace-tiramemsu`, PR #483). Each step ends with
`go test ./internal/palace/...` green and adds its tests in a file of its own.

1. **Migration 7** — `kg_schema2.go`: side tables, seal-key triggers,
   `secure_delete` pragma, tag 9. Tests: tables exist; seal key cannot be
   deleted, re-keyed, or destroyed twice; migration from a schema-6 file.
2. **Schema completion + upsert (K)** — valueType, subjectType, isEdge,
   sensitive; check order; `Upsert`. Tests per flag, violated-by-live-data,
   order of checks.
3. **Derived upsert + widening (J)** — tests: reuse across transactions, falls
   with support then a new node, asserted key not reused; widening keeps support.
4. **Named graphs (A)** — tests: membership bitemporal (AsOf before add),
   cascade on retract, replay on supersede, union default, `InGraph` on
   Match/About/TextSearch, DropGraph keeps facts.
5. **Volatile (C)** — the five upstream scenarios.
6. **Bundles (B)** — round trip into a fresh palace equals the source,
   idempotent re-import, past bundle via AsOf, statement-object inlining, cycle
   rejected with nothing written, excluded engine predicates.
7. **Query IR + SPARQL + Cypher (D)** — tests: joins, OPTIONAL, UNION,
   NOT EXISTS, FILTER three-valued, ORDER/LIMIT/OFFSET, COUNT, GRAPH,
   RDF-star subject, Cypher relationship var + layer property, as-of view
   changes answers, unsupported constructs rejected with position, SPARQL and
   Cypher agree on a shared query set.
8. **Budgets (L)** — row budget error (not truncation), ctx cancel stops a
   join and a path search, empty budget = same result.
9. **Paths (E)** — reach, shortest, all shortest, trails, cycles terminate,
   hop limits, frontier guard, time-respecting earliest arrival, graph scope.
10. **Rules (F)** — transitive closure, stratified negation, unsafe and
    unstratified rejected, constraint violations with witnesses, pointwise valid
    time, materialize idempotent, conclusions fall with a premise and with the
    program, reconcile retracts no-longer-derivable, widening on re-run, N3
    agrees with Datalog.
11. **Saved answers (G)** — all six upstream scenarios plus "no tx/event/row
    written".
12. **Vectors + recall (H)** — fake embedder; backfill after supersede, view
    awareness (AsOf before vector stored), exact cosine order, RRF ranks
    reported per channel, missing channel fails.
13. **Crypto-shredding (I)** — sealed at rest (raw `kg_term.lex` has no
    plaintext), idempotent assert of sealed value, annotations of a sensitive
    fact share the subject key, erase → `sys:Erased`, history intact, FTS
    never sees plaintext, sensitive-on-existing-plaintext rejected.
14. **Tools + CLI** — six tools, two extended; handler tests and `tool.Run`
    tests; CLI tests.
15. **Docs + verification** — feature map rows → Port/Adapt, summary,
    `make build test lint vet`, signed commits, push, Codex PR review,
    `make vulncheck`.
