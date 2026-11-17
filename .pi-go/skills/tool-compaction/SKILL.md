---
name: tool-compaction
description: Invariants for pi-go tool-output compaction, the after-tool callback chain, and the deduper. Use before changing internal/tools/compactor*.go, dedup.go, an after-tool callback, or a tool's output struct, or when a compaction saving looks wrong.
---

# Tool-output compaction, the after-tool chain, and dedup

Every rule here exists because its violation was silent: nothing crashed, the pipeline simply did nothing.

## Tool-output compaction: one route per tool, and write what you read

`internal/tools/compactor*.go` shrinks tool results before they reach the model.
Seven of its nine pipelines were no-ops in production for a long time, and every
cause was a silent mismatch rather than a crash. The invariants below exist so
that cannot recur.

- **Route on the registered name.** `compactorPipelines` is keyed by the name a
  tool actually registers — hyphens for the git tools (`git-file-diff`), and
  `ripgrep` for the search tool, which self-names via `grepToolName` and is
  built once by `CoreTools`. The tool registers `grep` only on a host without
  `rg`. A `switch` on underscore spellings is what made three git pipelines
  unreachable.
- **A pipeline may only write the field it read.** `CompactResult` carries
  `CompactWrite{Key, Value}` pairs. Do not add a probe order that guesses a
  target field: the previous `stdout → content → output → diff → result → data`
  order wrote to the wrong key, and no tool emits `output` at all.
- **Keep the value's type.** ADK round-trips every result through
  `json.Marshal`/`json.Unmarshal` (`internal/typeutil/convert.go`), so a
  `[]GrepMatch` arrives as `[]any` of `map[string]any` — never a typed slice.
  Cap the array; do not re-render it to a string. The TUI result summaries read
  the same keys (`internal/tui/tool_display.go`) as `[]any`.
- **Preserve the true total.** A cap sets `truncated` and leaves
  `total_matches`/`total_files`/`total_entries`/`total_hunks` as the pre-cap
  count, so a partial list is never read as the whole answer. `truncated`
  carries `omitempty`, so it is absent from a complete result — `applyCompaction`
  writes named keys unconditionally for exactly this reason.
- **Decline when there is nothing to gain.** Return `nil` when the result would
  not shrink (the rtk `never_worse` guard). Do not cap lists that mislead when
  truncated: `git-overview` caps `recent_commits` but leaves the
  staged/unstaged/untracked lists whole, because hiding a dirty file is a
  correctness problem, not a saving.
- **Test against real structs.** Build test payloads by marshalling the tool's
  actual output struct (`buildProdResult`), never a hand-written map. Synthetic
  maps carrying an `output` key are what kept the dead pipelines green.
  `TestCompactor_EveryToolCompacts` and `TestCompactorRouting_EveryRegisteredTool`
  are the guards; both fail if a route or field drifts.

### Compose the after-tool chain — a slice runs only its first callback

**ADK invokes only the first after-tool callback that returns a non-nil result.**
`Flow.invokeAfterToolCallbacks` (`adk/v2 internal/llminternal/base_flow.go`) is:

```go
for _, callback := range f.AfterToolCallbacks {
    result, err := callback(...)
    if result != nil { return result, nil }   // stops here
}
```

Every pi-go after-tool callback returns the result map, and the OTEL tracing
callback is registered first and always returns it — so handing ADK a slice ran
*tracing only*. Dedup, the compactor, the LSP after-hook and memory recording
were all silently dead for months, and it is why the compactor's revival changed
nothing at runtime until the chain was composed.

Rules:

- **Pass the chain through `extension.ComposeAfterToolChain`.** It folds the
  callbacks into one, so each stage's effect survives. Do not hand ADK a slice of
  mutating callbacks, and do not "reorder" the slice expecting a later stage to
  run.
- **A composed stage returns `(nil, nil)` to continue**, `(m, nil)` to replace
  the result for later stages, or `(_, err)` to abort. Returning the result map
  from a non-final stage is what short-circuits the chain.
- **Test with the composed callback and a real `agent.Context`.** The tracing
  stage reads trace state off the context and panics on nil, so a test that
  passes nil cannot observe the real chain. Better still, assert end to end
  through a real agent — `internal/agent/after_chain_compose_test.go` shows the
  `toolCallingLLM` pattern.
- **A guard that walks the slice itself proves nothing.** An earlier test in this
  repo looped over the callbacks and fed each output into the next, which is the
  opposite of ADK's semantics; it passed while production stopped at the first
  entry. Model the real invocation or the test cannot fail.

### The deduper reaches fewer tools than it lists

`dedupTools` (`internal/tools/dedup.go`) names the tools whose repeats may be
elided, but the deduper hashes exactly one string field per result —
`primaryOutputField` picks from `stdout|content|output|diff|result|data`. Measured
against each tool's real output struct, only `read`, `read_image` and
`git-file-diff` can actually be elided. `ripgrep`/`grep` (`matches`), `find`
(`files`) and `ls` (`entries`) carry arrays, and `tree` puts its listing under
`tree`, which the probe does not look at. `git-hunk` and `git-overview` were
removed from the list for the same reason.

`TestDedup_ReachableFieldsAreHonest` pins which tools are reachable. Extending
`primaryOutputField` to serialize array payloads would make the remaining five
reachable, but that is a feature change: do it deliberately, and move each tool
into the reachable table as it starts working rather than listing it in advance.

### rtk is a CLI proxy, not a library — do not delegate to it

`rtk` (third-party, `rtk-ai/rtk`, installed at `/opt/homebrew/bin/rtk`) is an
independent Rust CLI. It is a **reference for design**, not a dependency: pi-go's
compactor is native Go. Measured against pi-go's real shapes, delegating to it
does not work:

- `rtk pipe -f grep` was **byte-identical** on a 400-match list — the same class
  of no-op bug described above. `rtk grep` (command mode) does compact (93.8% on
  the same payload), but that is a different interface: it *runs* the search
  rather than filtering a result pi-go already has.
- `rtk pipe -f go-test` on real `go test` failure output emitted
  `Go test: No tests found` and **exit code 0**, losing both the failure and its
  signal.
- Shelling out per tool result adds a process spawn on the hot path, and rtk's
  filter set is not pi-go's tool set.

Use rtk's *decisions* (caps that preserve totals, the never-worse guard,
head-capping logs while keeping status lists whole); do not wire it in.
