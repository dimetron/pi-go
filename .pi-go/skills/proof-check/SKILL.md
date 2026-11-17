---
name: proof-check
description: Falsify a large improvement claim before reporting it. Bound inputs by what the producer can emit, build fixtures from real structs, prove the saving end to end, and confirm the guard can fail. Use when reporting a big saving, speedup, or fix.
---

# Never trust a big win without a proof check

A checklist for turning a large number into a verified one.

**A large improvement number is a claim about the code, and claims need evidence
of the same size as the number.** A 90% saving, a 10× speedup, "this fixes the
slow path" — each is exactly as likely to mean *the thing never ran* as it is to
mean the work succeeded. Treat a big win as a hypothesis to falsify, not a result
to report.

This is not hypothetical. The compaction work above reported savings per tool,
and three separate defects hid behind those numbers:

- **The shape was impossible.** A pipeline was credited with a 49–76% saving on
  payloads of 200 commits. The tool runs `git log --oneline -10`
  (`git_overview.go:72`), so that input can never occur and the pipeline never
  fires. The number was real and meaningless.
- **The input was synthetic.** Pipelines looked healthy against hand-written maps
  carrying an `output` key no tool emits. The tests passed; production did
  nothing.
- **The saving was measured per-tool, not end-to-end.** Nothing checked that the
  bytes actually left the prompt.

The rules that follow from that:

- **Bound the input by what the producer can emit.** Before measuring, find the
  cap the tool itself enforces (a constant like `maxGrepMatches`, a `-10` in the
  command, `truncateOutput`'s byte ceiling) and measure at or below it. A
  payload larger than the tool can produce measures a program that does not
  exist. State the bound next to the number.
- **Build fixtures from the real types.** Marshal the actual output struct and
  unmarshal into `map[string]any`, the way ADK does. Never a hand-written map: a
  fixture you invented encodes the contract you assumed, which is the thing under
  test.
- **Prove the saving end-to-end, not at the seam.** A per-function number shows a
  function works; it does not show the result reached the model. `AvgResultBytes`
  per tool in the eval harness (`internal/eval/metrics.go`) is the end-to-end
  version of this — it is currently *reported* in every eval but asserted only in
  one unit test, and never gated against a baseline. That gap is why seven dead
  pipelines looked healthy.
- **Check information, not just size.** Ask what the consumer needs from the
  output — a file name, a count, a total, whether the list is complete — and
  assert each survives. A cap that drops the one field a decision depends on is a
  regression that a byte count calls a win. `compactor_preservation_test.go`
  exists for this.
- **Verify the guard can fail.** Reintroduce the bug and confirm the test goes
  red, then restore. A test that has never failed proves nothing; several of the
  guards here were only trusted after being seen to break.
- **Prefer the cheap falsification first.** Before a full eval run, ask what
  result would show the win is fake, and run the smallest thing that would show
  it. Measuring at the producer's real bound is usually a few seconds of work and
  kills most false wins outright.
- **Say which numbers you did not verify.** If a figure came from an earlier
  session, a different shape, or reasoning rather than a run, label it. An
  honest gap is cheap; a confident wrong number is expensive.

When a win is large enough to be worth reporting, it is large enough to be worth
an eval. `make eval-tools` runs one headless scenario per tool family and rolls
the results into a coverage matrix (`internal/eval/scenarios/README.md`);
`make eval-run` and `make eval-judge` drive `/run` end-to-end against the pinned
`eval/base` baseline (`internal/eval/eval.md`). Report the before/after the
harness produced, not the before/after you expected.
