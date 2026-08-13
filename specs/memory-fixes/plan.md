# Implementation Plan — Memory subsystem repair

> Vertical slices. Each ends in a passing build and a green package test.
> No slice depends on a later one being merged.
>
> Repo gate per slice: `make test`, `make vet`, `make lint`.
> `internal/cli` tests bind local listeners — run them **outside the sandbox**
> (`CLAUDE.md` § "Two environment traps").
>
> Slices 1–2 are the outage. Land and verify them before starting 3.

---

## Slice 1 — Callback chaining (R1)

The whole outage. Ship it alone.

- [ ] **Create `internal/extension/chain.go`** with `ChainAfterTool` and
  `ChainBeforeTool` per `design.md` § 1. Document ADK's differing contract in
  the doc comment, with the `base_flow.go` reference — the next person needs to
  know why a bare slice is wrong.
- [ ] **`internal/cli/cli.go`** — replace `AfterToolCallbacks: afterCBs` with a
  single `extension.ChainAfterTool(afterCBs...)`. Same for `BeforeToolCallbacks`.
- [ ] **`internal/cli/interactive.go`** — identical change.
- [ ] **Reorder the chain** so observers precede transformers:
  `hooks → tracing → memory recorder → LSP → compactor → dedup`.
  The memory recorder moves ahead of the compactor so observations record the
  untruncated output.
- [ ] **`internal/extension/hooks.go`** — the OTel after-tool callback returns
  `nil, nil` on both paths. It observes; it must not claim to transform.
- [ ] **Tests** (`internal/extension/chain_test.go`):
  - `TestChainAfterTool_AllRun` — three callbacks, all observed.
  - `TestChainAfterTool_NilIsNoChange` — nil return preserves the prior result.
  - `TestChainAfterTool_ResultThreads` — cb1's map is what cb2 receives.
  - `TestChainAfterTool_ErrorAborts` — cb2 errors, cb3 never runs, error propagates.
  - `TestChainAfterTool_Empty` — returns the original result and error.
  - `TestChainBeforeTool_ShortCircuits` — first non-nil skips the tool (ADK
    semantics preserved for the before chain).
- [ ] **Regression test** (`internal/cli/callback_chain_test.go`): build the real
  chain, fire one tool result through it, assert the memory recorder enqueued
  **and** the compactor truncated. This is the test that pins the bug shut.
- [ ] **Verify manually.** Build, run
  `pi --mode print "read go.mod"`, then
  `sqlite3 ~/.pi-go/memory/claude-mem.db "select count(*) from observations"`.
  Expect ≥ 1. Today it is 0.

---

## Slice 2 — Worker lifecycle (R2, R3)

- [ ] **`internal/memory/worker.go`** — add `WorkerConfig{BufSize, Concurrency,
  DrainTimeout, ItemTimeout}` with the defaults from `design.md` § 2. Keep
  `NewWorker(store, compressor, bufSize)` as a thin wrapper so existing callers
  and tests compile.
- [ ] `Start` launches `Concurrency` goroutines over `obsChan`; `w.done` closes
  once all have exited (`sync.WaitGroup`, not the current single `close`).
- [ ] `processOne` wraps compression in `context.WithTimeout(ctx, ItemTimeout)`.
- [ ] `Enqueue` increments an atomic `dropped` counter on the `default:` branch.
- [ ] `Shutdown` logs `dropped` and the undrained count when the deadline expires.
- [ ] **Fix shutdown ordering** in `cli.go` `setupMemory` and `interactive.go`
  `initResources.close`: drain to completion (or `DrainTimeout`) **before**
  `store.Close()`. Raise the budget from 5 s to `DrainTimeout`.
- [ ] **`internal/subagent/bundled/memory-compressor.md`** — `timeout: 600000` →
  `45000`.
- [ ] **Tests** (`internal/memory/worker_test.go`):
  - `TestWorker_ConcurrentDrain` — N slow mock compressions finish in roughly
    `N/Concurrency × d`, not `N × d`.
  - `TestWorker_ItemTimeout` — a compressor that never returns yields a fallback
    observation and frees its slot.
  - `TestWorker_DroppedCounter` — buffer 1, blocked worker, second enqueue
    increments `dropped`.
  - `TestWorker_ShutdownDrainsBeforeClose` — no insert lands on a closed store.
- [ ] **Verify:** `go test ./internal/memory/... ./internal/cli/...` (outside sandbox).

---

## Slice 3 — In-process compression (R4)

- [ ] **`internal/memory/compress_llm.go`** — `LLMCompressor` implementing
  `Compressor` against the provider layer, reusing `buildCompressionPrompt`,
  `parseCompressedResponse` and `SummarizeSession`'s prompt.
- [ ] **`internal/config`** — add `Memory.Compressor` (`"llm"` default,
  `"subagent"`, `"none"`) and `Memory.Concurrency`, `Memory.DrainTimeout`,
  `Memory.ItemTimeout`.
- [ ] **Wiring** picks the compressor from config. `NoopCompressor` for `"none"`
  returns the fallback observation directly.
- [ ] **Log once per session** when `smol` is unresolved and `ResolveRole` fell
  back to `default`, naming the model that will actually be billed.
- [ ] **Move `SubagentCompressor` out of `internal/memory`** into the wiring
  layer so `memory` no longer imports `subagent`. Closes `TODO.md` item 36.
  `memory` keeps only the `Compressor` interface.
- [ ] **Tests:**
  - `TestLLMCompressor_ParsesResponse` — fake provider, fenced JSON, fields map.
  - `TestLLMCompressor_MalformedFallsBack` — bad JSON surfaces an error so the
    worker's fallback path runs.
  - `TestNoopCompressor` — produces the fallback observation, calls no model.
  - `TestCompressorSelection` — each config value builds the right type.
- [ ] **Verify:** memory package no longer imports `internal/subagent`
  (`go list -deps` assertion in the test).

---

## Slice 4 — Path unification (R5)

- [ ] **Create `internal/palace/paths.go`** — `ResolveDBPath`, `ResolveModelPath`,
  `DefaultModelDir` per `design.md` § 3.
- [ ] **Replace every ad-hoc computation** with a call: `memory.go:13`,
  `memory_init.go:48`, `memory_mine.go:283`, `memory_status.go:32`,
  `memory_search.go:43`, `memory_wakeup.go:38`, `memory_model.go:59,105,109`,
  `tui/memory.go:27`, `cli.go` `palaceConfigFromCLI`.
- [ ] **Delete the `KnightsAnalytics_all-MiniLM-L6-v2` literal.** No such
  directory has ever existed.
- [ ] **`pi memory status`** prints the resolved DB path, the resolved model
  path, and which precedence rule selected each.
- [ ] **Tests** (`internal/palace/paths_test.go`): table-driven over the
  precedence ladder — explicit beats config beats project beats home; project
  resolution walks up to the git root; no-repo falls back to `workDir`.
- [ ] **Verify:** `pi memory init . && pi memory status` and a session in the
  same directory report the same file.

---

## Slice 5 — Palace in the TUI, scoped wake-up (R6, R7)

- [ ] **Extract `setupPalace`** from `cli.go` into a helper both entry points use.
- [ ] **Call it from `initMemoryAfterUI`** (`interactive.go`): open the palace,
  append `palace.PalaceTools`, wire `OnAfterStore(bridge.ConvertAndStore)`,
  deliver the wake-up context.
- [ ] **Wake-up context as an `InstructionParts` field**, not string
  concatenation, so the context gauge attributes it correctly.
- [ ] **Extract `palace.WingForProject`** from `bridge.go:deriveWing`; the bridge
  and both wake-up call sites use it. Session wake-up passes the project wing;
  `pi memory wake-up` keeps its `--wing` flag with `""` meaning all wings.
- [ ] **Close the palace** in `initResources.close`, after the memory worker
  drains — the bridge writes drawers from worker goroutines.
- [ ] **Tests:**
  - `TestWingForProject` — path → wing, including `/`, `.`, and empty.
  - `TestWakeUpScopedToWing` — two wings present, only the requested one renders.
  - `TestInteractivePalaceWiring` — TUI setup registers palace tools when a
    palace exists and stays silent when it does not.
- [ ] **Verify:** start the TUI in a project with a mined palace; palace tools
  are listed and the sidebar drawer count matches `pi memory status`.

---

## Slice 6 — End-to-end regression test (R9, part 1)

The test that should have existed from the start. Written after 1–5 so it
asserts the repaired behaviour.

- [ ] **`internal/cli/memory_e2e_test.go`** — `TestObservationReachesPalace`:
  temp home, temp palace, fake tool result through the real composed chain →
  worker (noop compressor, deterministic) → `observations` row → bridge →
  `drawers` row → retrievable via `mem-search`. No network, no API key.
- [ ] **`TestShortSessionRecordsObservation`** — enqueue then immediately run the
  real shutdown closer; assert the row exists and no closed-DB error is logged.
- [ ] **Verify:** both fail against `69b4d03` and pass on the branch.

---

## Slice 7 — Query sanitisation and fencing (R8)

- [ ] **`palace.SanitizeQuery`** — strip FTS5 operators, control characters and
  instruction-shaped prefixes; replaces the bare quote-stripping in
  `sanitizeFTS5Query`.
- [ ] **Fence retrieved drawer bodies** in wake-up, recall and `mem-search`
  output so drawer content cannot read as instructions.
- [ ] **Tests:** operator injection (`"a" OR "b"`, `NEAR/3`, `*`) is neutralised;
  a drawer containing `## System:` renders fenced.

---

## Slice 8 — Retrieval benchmark (R9, part 2)

- [ ] **`internal/palace/testdata/bench/`** — small committed corpus plus
  labelled query → expected-drawer pairs.
- [ ] **`TestRetrievalRecall`** — recall@5 against the fixtures with the
  deterministic hash embedder; assert a floor and print the number.
- [ ] **`make test-memory-bench`** target.
- [ ] **Record the baseline** in `summary.md` so the next change has something to
  regress against.

---

## Post-merge

- [ ] Rebuild and install the local `pi` binary.
- [ ] Run a real session; confirm observations accumulate and
  `pi memory recent` shows them.
- [ ] Note in `summary.md` whether re-enabling the compactor and LSP hooks
  changed model-visible behaviour — they have been dead the whole time, so this
  is the first honest measurement of them.
