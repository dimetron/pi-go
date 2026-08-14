# Research — Evidence for each defect

Everything below was measured on 2026-08-13 against the live databases and by
running the binary. Line references are against `69b4d03`.

---

## F1 — After-tool callback chain stops at the first callback

### Upstream semantics

`$(go env GOMODCACHE)/google.golang.org/adk/v2@v2.0.0/internal/llminternal/base_flow.go:1296-1309`

The full signature matters here, so it is not elided: `fResult` is a **parameter**
of this function, which is what makes defect 2 below visible rather than
something to take on trust.

```go
func (f *Flow) invokeAfterToolCallbacks(toolCtx agent.Context, tool toolinternal.FunctionTool,
	fArgs, fResult map[string]any, fErr error) (map[string]any, error) {
	for _, callback := range f.AfterToolCallbacks {
		result, err := callback(toolCtx, tool, fArgs, fResult, fErr)
		//                                            ^^^^^^^ defect 2: the parameter,
		//                                            never reassigned by the loop
		if err != nil {
			return nil, err
		}
		// When a list of callbacks is provided, the callbacks will be called in the
		// order they are listed while a callback returns nil.
		if result != nil {
			return result, nil // defect 1: the chain ends here
		}
	}
	// If no callback returned a result/error, return the original result/error.
	return fResult, fErr
}
```

There are **two** defects in this one function, and the second is the worse one.

**Defect 1 — the chain stops.** A non-nil return means "override the tool result
and stop". It does not mean "here is the result, carry on".

**Defect 2 — results are never threaded.** `fResult` is the loop's parameter and
is never reassigned, so every callback is handed the *original* tool result
rather than the previous callback's output. Even with defect 1 fixed, a chain
still would not compose.

Defect 2 matters because pi-go's ordering already assumes composition: dedup is
registered *after* the compactor precisely so it sees post-compaction results.
It never has, and fixing only the short-circuit would not give it them. Any fix
must thread the result forward itself; ADK will not do it.

### What pi-go registers

`internal/cli/cli.go:602-624` (headless) and `internal/cli/interactive.go:338-358`
(TUI) build the same chain in the same order:

| # | Callback | Returns | Source |
|---|----------|---------|--------|
| 1 | `extension.BuildAfterToolCallbacks(hooks)` | `result` | `internal/extension/hooks.go:256` |
| 2 | `extension.BuildTracingCallbacks` (OTel) | `result` | `internal/extension/hooks.go:266` |
| 3 | `lsp.BuildLSPAfterToolCallback` | `result` | `internal/lsp/hooks.go:36,81` |
| 4 | `tools.BuildCompactorCallback` | `result` | `internal/tools/compactor.go:82,91` |
| 5 | `tools.BuildDedupCallback` | `result` | `internal/tools/dedup.go:110,113` |
| 6 | `memoryObservationCallback` / `memRecorder.afterTool` | `result` | `internal/cli/cli.go:1091` |

No hooks are configured by default, so **#2 is the first to return non-nil and
#3, #4, #5 and #6 never execute**. The OTel callback returns `result` on both
its paths, including the `span == nil` early return, so the short-circuit is
unconditional.

Consequences, all four silent:

- LSP diagnostics/format-on-write after-tool hooks: dead.
- Tool-output compaction: dead — full untruncated outputs reach the model.
- Result dedup: dead.
- Memory observation recording: dead.

And independently of the short-circuit, any two callbacks that both transform a
result cannot chain, because the second never sees the first's output.

### Proof by experiment

Built two binaries from `aae880b` in a throwaway worktree.

**A — tracing patched to return `nil, nil`.** Still zero observations: #3 (LSP)
becomes the new short-circuit. Reordering one callback does not fix it.

**B — memory callback prepended to the slice.**

```
before:  t=40s obs=0
after:   t=5s  obs=1   →  1|discovery|Inspected memory package files|bash
```

The pipeline itself is sound. Only the position in the chain was wrong.

---

## F2 — Compression cannot finish inside the shutdown budget

### Cost per observation

Measured with a direct `orchestrator.Spawn` of the `memory-compressor` agent
against the configured model:

```
spawn -> err=<nil> (2.2ms)
[  4.64s] message_start
[  6.05s] stream closed, 54 events
worker drain (enqueue → stored): 5.6s
```

**5.6 s per observation**, because each one spawns a full `pi --mode json` child
process.

### The pipeline cannot absorb that

- `internal/memory/worker.go:67-76` — one goroutine, strictly serial.
- `internal/subagent/bundled/memory-compressor.md` — `timeout: 600000`, so a
  single stuck compression blocks the queue for ten minutes.
- `internal/memory/worker.go:55-64` — `Enqueue` drops silently when the 100-slot
  buffer fills.
- `internal/cli/interactive.go:57-61` and `internal/cli/cli.go:959-967` — a
  **5 s** shutdown budget, less than one compression.

### Observed failure at exit

From experiment B's stderr, after the run finished:

```
WARN  memory: compression failed  error="pi process failed: signal: terminated"
ERROR memory: failed to store observation  error="sql: database is closed"
WARN  palace bridge: failed to store       error="sql: database is closed"
```

Two distinct problems in three lines: the compressor child is killed when the
parent exits, and the store is closed before the worker has drained, so even a
successful compression writes into a closed handle.

### Role resolution

`memory-compressor` declares `role: smol`. `~/.pi-go/config.json` defines only
`default`, and `Config.ResolveRole` (`internal/config/config.go:191-196`) falls
back to `default` — a frontier model. Every tool call in every session would
bill a frontier-model subprocess call if the pipeline were running.

---

## F3 — Palace database and model paths disagree

| Consumer | Path | Source |
|---|---|---|
| `pi memory init` | `<dir>/.pi-go/palace.db` | `memory_init.go:48` |
| `pi memory mine` | `<dir>/.pi-go/palace.db` | `memory_mine.go:283` |
| `pi memory status/search/wake-up` | `.pi-go/palace.db` (cwd-relative) | `memory_status.go:32`, `memory_search.go:43`, `memory_wakeup.go:38` |
| TUI sidebar | `<workDir>/.pi-go/palace.db` | `tui/memory.go:27` |
| **Agent session** | `$HOME/.pi-go/palace.db` | `cli.go:1719` |

Anything mined into a project is invisible to the agent running in that project.

`setupPalace` (`cli.go:991-995`) returns early when the file does not exist, so
the mismatch degrades to "palace silently absent" rather than an error.

Same class of bug for the embedding model:

| Consumer | Directory |
|---|---|
| `pi memory model download` writes | `~/.pi-go/models/sentence-transformers_all-MiniLM-L6-v2` |
| `pi memory model status` checks | `sentence-transformers_all-MiniLM-L6-v2` (`memory_model.go:109`) |
| `defaultPalaceModelPath()` | `sentence-transformers_all-MiniLM-L6-v2` (`memory.go:13`) |
| **`palaceConfigFromCLI`** | `KnightsAnalytics_all-MiniLM-L6-v2` (`cli.go:1724`) |

On disk: `~/.pi-go/models/sentence-transformers_all-MiniLM-L6-v2` exists,
`KnightsAnalytics_*` does not. The in-process embedder fallback can never load
at session time. It is masked today because `DefaultConfig().UseOllama` is true
(`palace/config.go:38`) and Ollama has `all-minilm` pulled — it surfaces the
moment Ollama is not running.

---

## F4 — Palace is absent from the interactive path, and wake-up is unscoped

`setupPalace` is called from exactly one place — `cli.go:588`, the headless
`--mode print` path. `internal/cli/interactive.go` never calls it. In the TUI:
no palace tools, no wake-up context, no observation bridge. The TUI's only
palace contact is the sidebar status widget, which reads a different file (F3).

Where the palace *is* wired, the wake-up call is unscoped:

- `cli.go:1010` — `p.WakeUp(context.Background(), "")`.
- `palace/layers.go:47-49` → `ListDrawers(DrawerFilter{Wing: ""})`.
- `sqlite_store.go:199` — an empty wing adds no `WHERE` clause, so all wings match.

Meanwhile the bridge files every drawer under `wing = strings.ToLower(filepath.Base(project))`
(`palace/bridge.go:65-74`). Wake-up therefore mixes every project the user has
ever worked on into one L1 essential story, ranked by importance alone.

---

## F5 — Retrieval does not scale and is not measured

- `SQLitePalaceStore.GetAllEmbeddings` (`sqlite_store.go:253`) selects **every**
  embedding matching the wing/room filter; `RankBySimilarity` scores them all in
  process. No ANN index anywhere in `internal/palace`. Fine at 3 drawers, a full
  scan per query at 100k. Upstream gets HNSW from ChromaDB.
- No equivalent of upstream's `query_sanitizer.py`. Search input reaches FTS5
  through `sanitizeFTS5Query` (quote-stripping only) and the retrieved drawer
  text is injected into the prompt verbatim.
- No retrieval benchmark. Upstream publishes reproducible LongMemEval / LoCoMo /
  ConvoMem / MemBench numbers; pi-go has no measurement at all, which is why a
  four-month outage went unnoticed.

---

## Environment note

`go test ./internal/palace/...` panics under the sandbox in
`TestOllamaEmbedSerializesAcrossGoroutines` — `httptest.NewServer` cannot bind.
Outside the sandbox both packages pass (2.8 s). See `CLAUDE.md` § "Two
environment traps"; do not read that panic as a real failure.
