# Topic 7 — Measurement: `pi tokens`, OTel, compactor metrics, regression fixtures

## Research

The substrate for measurement already exists; almost nothing needs new plumbing.

### Per-turn usage is already recorded

Every session records `UsageMetadata` per turn. Nothing new needs
instrumenting; the numbers in TOKENS.md come from replaying on-disk history.

```bash
jq -c 'select(.UsageMetadata != null) | .UsageMetadata' \
   ~/.pi-go/sessions/<id>/events.jsonl
# → {"candidatesTokenCount":79,"promptTokenCount":10886}
```

Session dirs also hold `meta.json` (model, provider, workDir) and
`trajectory.atif.json`. Corpus: 1,404 sessions, 36,822 LLM calls, 48,782 tool
calls.

### The OTel span already emits usage

`internal/extension/hooks.go:355-370` already emits `gen_ai.usage.input_tokens`,
`gen_ai.usage.cached_input_tokens`, `gen_ai.usage.reasoning_tokens`,
`gen_ai.usage.total_tokens`. What is missing is the *shape* of the session —
tools-per-turn and turn index — which is what makes the quadratic cost visible.

### Compactor metrics are computed but never persisted

`compactor_metrics.go:112` `Save()` has **no caller** (confirmed by grep), and
`cli.go:613` builds the metrics object inline and discards it. The real
per-tool compression ratio is currently unknown to everyone.

### No regression guard

There is no `pi tokens` command (grep for `"tokens"` in `internal/cli` finds
nothing), and no fixture-based assertion that a fixed transcript's total prompt
tokens stay bounded.

## Recommendations

1. **Ship the replay as `pi tokens`.** Per session: prompt total, turns,
   tools/turn, cache hit rate, and `prompt_tokens / output_tokens` as the
   headline. That ratio — **currently 201:1** — is the single number to drive
   down. This is the primary measurement surface for every other topic in this
   document.
2. **Extend the OTel span with `tools_per_turn` and `turn_index`.** These two
   attributes make the quadratic cost directly visible in traces, complementing
   the aggregate `pi tokens` view.
3. **Persist compactor metrics.** Call `CompactMetrics.Save(sessionDir)` at
   session end (or on a cadence), so the real per-tool compression ratio is
   known. Currently the object is built at `cli.go:613` and discarded.
4. **Guard against regressions.** Pin a handful of representative sessions as
   fixtures and assert total prompt tokens for a fixed transcript. Context
   changes then show up as a number in CI rather than as a feeling.

## Expected impact

Turns the 201:1 ratio into a continuously measured, regression-guarded number.
Every other topic in this document becomes verifiable against a concrete metric
rather than a one-off replay.

## Risk

Low. All four changes are additive measurement surfaces; none alter runtime
behaviour. The `pi tokens` command is the largest piece of new code.
