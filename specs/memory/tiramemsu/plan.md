# Plan

Branch `feat/palace-tiramemsu`, worktree `.worktrees/feat-palace-tiramemsu`.
Each step ends with `go test ./internal/palace/...` green.

1. **Values** — `kg_value.go`: `ObjectID` encode/decode, `Value` constructors,
   term interning (`internTerm(tx, tag, lex, num)`) and batch decoding.
   Tests: round-trip every tag, negative ints, interning idempotent.
2. **Schema + triggers** — migration 6 SQL (`kg_tx`, `kg_term`, `kg_stmt`,
   indexes, `kg_event`, `kg_term_fts` + trigger, invariant triggers). Migration
   list gains Go steps. `OpenDB(":memory:")` → `SetMaxOpenConns(1)`.
   `SQLitePalaceStore.DB()`. Tests: raw DELETE / content UPDATE / second
   retraction / re-insert all fail; first retraction succeeds.
3. **Transactions** — `kg_tx.go`: `Transact`, `TxOptions{Author, Source,
   Reason, DryRun, MaxCascade}`, `TxReport`, tx allocation, in-process mutex.
   Tests: gap-free `t`, strictly increasing instant under a frozen clock,
   dry run commits nothing, error rolls back.
4. **Assert / create / reserved / liveness** — tests: idempotency with overlapping
   vs disjoint valid time (episodes), reserved predicate rejected, dead and
   self statement endpoints rejected, `OnExisting: Confirm`.
5. **Retract + cascade + dependents** — tests: layer chain cascades, nodes do
   not cascade, retract twice returns false, cascade limit aborts and rolls
   back, `Dependents` equals the dry-run retraction set, works on `AsOf` after
   retraction.
6. **Supersede** — tests: object patch, valid-time patch, layers replayed on new
   eids with σ (layer-on-layer), lineage `sys:supersedes`, old structure visible
   in `AsOf(t-1)`, derived dependents dropped, patch into cascade rejected,
   s/p unchanged, superseding a derived statement keeps its supports.
7. **Confirm + derive** — tests: confirmation count, derive idempotent per
   exact support set, a different support set makes a second statement,
   retracting any support retracts the conclusion, independent identical fact
   survives, supports must be live.
8. **Predicate schema** — tests: `one` replaces overlapping values (kind
   cardinality, layers not carried), disjoint episodes coexist, `unique` rejects,
   schema change violating live data rejected, schema is versioned.
9. **Views and reads** — `kg_view.go`, `kg_explain.go`: `Now`, `AsOf`,
   `AsOfInstant`, `History`, `ValidAt`, `About`, `Match`, `Layers`, `Explain`,
   `Conflicts`, `TextSearch`, `Events`, stats. Tests: each view predicate,
   empty view before first tx, half-open valid time, conflicts with evidence,
   text search respects views, events replay equals `AsOf` state.
10. **Legacy facade + migration** — rewrite `kg.go` on the engine; drop the four
    triple methods from `PalaceStore` and `SQLitePalaceStore`; move `KGStats`.
    Legacy import step. Update existing KG tests to the new behaviour (half-open
    valid time, `e<eid>` ids). Tests: a DB built with migrations 1–5 plus legacy
    rows migrates to equal facts, source_file layer, closed valid time kept,
    tx instants ordered by extraction time.
11. **Tools** — extend `palace-kg-add`, `-query`, `-invalidate`, `-timeline`;
    add `palace-kg-revise`, `-explain`, `-conflicts`; register in `tools.go`;
    tx author/source from ADK context. Tests through the handlers and through
    `tool_invoke_test.go`'s invocation path.
12. **CLI** — `pi memory kg explain|conflicts|log`, `query --tx-as-of`. Tests in
    `internal/cli`.
13. **Docs + verification** — package doc in `kg.go`, update the spec index in
    `specs/AGENTS.md`, `summary.md`. Run `make build test lint vet`; check no
    `fmt.Print`/`os.Stdout` in palace; commit signed + signed-off.
