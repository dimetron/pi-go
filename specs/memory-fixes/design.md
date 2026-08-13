# Design

## 1. Callback composition (R1)

### Problem

ADK's contract is "first non-nil result wins and ends the chain". pi-go treats
the slice as a broadcast list. Both readings are defensible; only one is the
library's. See `research/findings.md` § F1.

### Decision

Keep pi-go's semantics, and stop handing ADK a slice. Compose pi-go's callbacks
into **one** ADK callback that owns the chaining rules.

New file `internal/extension/chain.go`:

```go
// ChainAfterTool folds callbacks into a single ADK AfterToolCallback with
// pi-go semantics: every callback runs, in order; a nil return means "no
// change"; a non-nil return replaces the result passed to the next callback;
// an error aborts the chain.
//
// ADK's own contract is different — it stops at the first non-nil result
// (adk/v2 internal/llminternal/base_flow.go, invokeAfterToolCallbacks). Handing
// ADK a slice therefore silently disables every callback but the first. Always
// register exactly one composed callback.
func ChainAfterTool(cbs ...llmagent.AfterToolCallback) llmagent.AfterToolCallback

// ChainBeforeTool is the same fold for before-tool callbacks, where ADK's
// short-circuit ("a non-nil result skips the tool") is the intended behaviour
// and is preserved.
func ChainBeforeTool(cbs ...llmagent.BeforeToolCallback) llmagent.BeforeToolCallback
```

`ChainAfterTool` semantics, precisely:

| Callback returns | Chain does |
|---|---|
| `nil, nil` | keep current result, continue |
| `m, nil` | current result becomes `m`, continue |
| `_, err` | abort, return `nil, err` |

Final return is the current result and the original tool error — matching ADK's
`return fResult, fErr` tail.

Both call sites become:

```go
AfterToolCallbacks: []llmagent.AfterToolCallback{
    extension.ChainAfterTool(afterCBs...),
},
```

Ordering matters and is now explicit: observers (tracing, memory) before
transformers (compactor, dedup), so observers see the untransformed result.

### Why not "make observers return nil"

It would work today and break the first time someone appends a transformer
after an observer. The bug is that a slice does not mean what the code assumes;
fix that, not the symptom. Making tracing return `nil, nil` is still correct and
lands as part of the ordering pass — it is just not the fix.

## 2. Worker lifecycle and compression cost (R2, R3, R4)

### Concurrency

`internal/memory/worker.go` grows a configurable worker count.

```go
type WorkerConfig struct {
    BufSize      int           // default 100
    Concurrency  int           // default 4
    DrainTimeout time.Duration // default 60s
    ItemTimeout  time.Duration // default 45s
}
```

`Start` launches `Concurrency` goroutines over the same channel. `processOne`
wraps its compression in `context.WithTimeout(ctx, ItemTimeout)` so one stuck
item cannot hold a slot for ten minutes.

Ordering across observations is not guaranteed today either (single queue, async
callback) and nothing depends on it — `created_at_epoch` orders reads.

### Drop accounting

`Enqueue`'s `default:` branch increments an atomic `dropped` counter alongside
the existing warning. `Shutdown` logs the total. Silent loss becomes visible
loss.

### Shutdown ordering

The current closer drains for 5 s then closes the store, so a compression in
flight writes into a closed handle. New order:

1. `close(obsChan)`
2. wait for drain up to `DrainTimeout` (default 60 s > one compression)
3. log `dropped` and any undrained count
4. `store.Close()`

Same change in both closers (`cli.go` `setupMemory`, `interactive.go`
`initResources.close`).

### In-process compression

`SubagentCompressor` spawns a `pi --mode json` child per observation: ~5.6 s and
one process. The `Compressor` interface already isolates this.

Add `internal/memory/compress_llm.go` — `LLMCompressor`, which calls the
resolved `smol` model directly through the provider layer, no subprocess, no
worktree, no pool slot. Same prompt, same JSON contract, same
`parseCompressedResponse`.

Selection: `cfg.Memory.Compressor` = `"llm"` (default) | `"subagent"` | `"none"`.
`"none"` stores the fallback observation without any model call — cheap, useful
for CI and for users who want capture without inference.

`ResolveRole` falling back to `default` stays, but the wiring logs once per
session when `smol` is unconfigured, so a frontier-model fallback is visible
rather than a surprise on the bill.

This also removes the `memory → subagent` layering violation recorded as
`TODO.md` item 36.

## 3. Path resolution (R5)

New `internal/palace/paths.go`, the single source of truth:

```go
// ResolveDBPath returns the palace database path for workDir.
// Precedence: explicit → config → project → home.
func ResolveDBPath(explicit string, cfg *config.PalaceConfig, workDir string) string

// ResolveModelPath returns the embedding-model directory.
// Precedence: config → $HOME/.pi-go/models/<DefaultModelDir>.
func ResolveModelPath(cfg *config.PalaceConfig) string

// DefaultModelDir is the on-disk name written by `pi memory model download`.
const DefaultModelDir = "sentence-transformers_all-MiniLM-L6-v2"
```

"Project" means `<git root of workDir>/.pi-go/palace.db`, falling back to
`workDir` when not in a repo — so `pi memory search` run from a subdirectory
finds the palace `pi memory mine .` created at the root. That is a behaviour
change for the `pi memory *` commands and is intentional; today they are
cwd-relative and miss.

Every current computation of these paths is replaced by a call:
`memory.go:13`, `memory_init.go:48`, `memory_mine.go:283`, `memory_status.go:32`,
`memory_search.go:43`, `memory_wakeup.go:38`, `memory_model.go:59,105,109`,
`tui/memory.go:27`, `cli.go` `palaceConfigFromCLI`.

`KnightsAnalytics_all-MiniLM-L6-v2` disappears — it names a directory that has
never existed on disk.

### Migration

Users with an existing `$HOME/.pi-go/palace.db` and no project palace keep
working: home is the last precedence step. `pi memory status` gains a line
naming the resolved path and which precedence rule chose it, so "where is my
palace" is answerable without reading code.

## 4. Palace in the interactive path (R6, R7)

`setupPalace` moves from `cli.go` to a shared helper both entry points call.
Because the TUI builds resources after the UI starts, palace setup joins
`initMemoryAfterUI`: open palace, register tools, wire the bridge, push the
wake-up context.

Wake-up context arrives after the instruction is built, so it goes in as an
`InstructionParts` field rather than string concatenation — that keeps the
context-gauge breakdown honest, which is the stated reason `instructionParts`
exists.

### Wing scoping

`palace.WingForProject(project string) string` becomes the one derivation,
extracted from `bridge.go:deriveWing`. Both the bridge and wake-up call it:

```go
wakeUp, err := p.WakeUp(ctx, palace.WingForProject(cwd))
```

Empty wing keeps meaning "all wings" at the store layer — that is right for
`pi memory wake-up --wing ""`. Only the session call site becomes specific.

## 5. Retrieval safety and measurement (R8, R9)

Smallest useful versions; ANN indexing is deliberately deferred.

- `palace.SanitizeQuery` — strips FTS5 operators and control characters beyond
  today's quote-stripping.
- Retrieved drawer bodies are fenced when injected, so drawer content cannot
  read as instructions.
- `internal/palace/testdata/bench/` — a small committed corpus with labelled
  query → expected-drawer pairs. `TestRetrievalRecall` computes recall@5 and
  fails below a floor. Runs with the deterministic hash embedder, no network.
- `TestObservationReachesPalace` — the end-to-end assertion: fake tool call →
  chained callbacks → worker → observation row → bridge → drawer → found by
  search. This is the test whose absence let F1 hide for four months.

Brute-force cosine stays. `GetAllEmbeddings` + `RankBySimilarity` is fine at
current corpus sizes; the benchmark from R9 is what will tell us when it is not.

## Risks

| Risk | Mitigation |
|---|---|
| Enabling the compactor and LSP hooks for the first time changes model-visible output | Land Slice 1 alone and observe; compactor config already exists and is testable in isolation |
| In-process compression adds latency to the agent's own provider path | Runs on worker goroutines, never the turn path; concurrency capped |
| Compression cost per tool call is now real, where before it was zero | `Compressor: "none"` opt-out, plus the once-per-session log when `smol` is unconfigured |
| Project-relative palace path changes where `pi memory *` looks | `pi memory status` prints the resolved path and rule; home remains the final fallback |
