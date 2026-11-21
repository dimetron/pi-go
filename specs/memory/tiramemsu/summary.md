# Summary

The palace knowledge graph is now a bitemporal statement graph, a pure-Go port
of Tiramemsu's core model on the existing `modernc.org/sqlite` palace
database. No cgo, no new dependency, one file.

## What changed

| Area | Files |
|---|---|
| Values, term dictionary | `internal/palace/kg_value.go` |
| Schema 6, never-forget and date triggers, legacy import + freeze | `internal/palace/kg_schema.go`, `db.go` (Go migration steps, DSN pragmas on every connection, newer-schema refusal, race-safe version claim) |
| Transactions, report, dry run | `internal/palace/kg_tx.go` |
| Verbs: assert, create, annotate, retract + cascade, supersede + replay, confirm, derive, predicate schema | `internal/palace/kg_verbs.go` |
| Views and reads: now / as-of / as-of-instant / history × valid-at, about, match, layers, dependents, events | `internal/palace/kg_view.go` |
| Explain, evidence, conflicts, text search, stats | `internal/palace/kg_explain.go` |
| Legacy facade (Add, Query, Invalidate, Timeline) on the engine | `internal/palace/kg.go`, `types.go` |
| Tools: extended `palace-kg-add/-query/-invalidate/-timeline`; new `palace-kg-revise`, `-explain`, `-conflicts` | `internal/palace/tool_kg.go`, `tool_kg_revise.go`, `tools.go` |
| CLI: `pi memory kg explain|conflicts|log`, `query --tx-as-of` | `internal/cli/memory_kg.go` |

`PalaceStore` lost `InsertTriple`, `QueryTriples`, `InvalidateTriple`,
`TimelineTriples` and `KGStats` and gained `DB()`.

## Behaviour changes callers can see

- Point-in-time (`as_of`) queries use half-open valid time `[from, to)`; the
  old query treated `valid_to` as inclusive.
- Triple ids are `e<eid>` instead of `t_<s>_<p>_<o>_<md5>`.
- `Invalidate` supersedes the open fact, so the timeline shows the open
  version (status `superseded`) and its closed replacement (`ended`).
- Old pi binaries can still read a migrated palace but writing a triple fails
  with "upgrade pi" instead of silently forking the graph.

## Verification

- 16 engine tests (`kg_engine_test.go`), 3 tool tests (`tool_kg_revise_test.go`),
  a CLI test, and the existing KG, store, tool and status tests (updated where
  the behaviour changed on purpose). The end-to-end tool test now also fails on
  `Error:` content and runs the alternative inputs (`about` without `subject`,
  `text` without `entity`) through `tool.Run`.
- Migration test builds a schema-5 file with legacy rows (closed interval,
  source file, tied extraction times, a `sys:` predicate) and checks the facts,
  times, transaction order, the frozen legacy table and newer-schema refusal.
- Concurrency test: two handles on one file, 40 transactions, no errors, gap-free.
- Guards seen failing: removing the post-replacement liveness re-check fails
  `TestKGSchema`; removing the committed-watermark condition from the date
  triggers fails `TestKGTriggers_NeverForget` (after that test was strengthened
  — its first version did not catch it).

## Not verified

- Performance at scale. Nothing was measured; reads decode terms per page and
  `evidenceFor` loads one transaction row per distinct `t_add`.
- Agent behaviour: whether models use confidence, revise and explain well was
  not evaluated (`make eval-tools` not run).

## Phase 2

Everything phase 1 deferred is now ported; see `phase2/summary.md`. Only the
redb engine, WASM host, language bindings, MCP server and the `tm` CLI are not
applicable to pi-go (feature map row 27).
