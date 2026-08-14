# Plan

Four slices, in dependency order. Slice 1 must land before slice 2, because
slice 2 is unverifiable without it. Total scope is small — this spec's main
output is the decision in `design.md` § D1 not to build the thing it was asked
to evaluate.

Gate for every slice: `make test`, `make vet`, `make lint`.

## Slice 1 — Measure round trips (R1)

**Why first:** slice 2 changes model behaviour. Without a metric, "did it work?"
is a matter of opinion.

`internal/tools/session_sweep.go`:

1. Add to `sweepTotals` (line 41):
   ```go
   Requests   int
   CallsPerResponse map[int]int   // calls in a response → count of responses
   ```
   Initialise the map in `newSweepTotals` (line 51).
2. In the accumulation loop, at the existing `if ev.UsageMetadata != nil` branch
   (line 125), increment `Requests`.
3. In the same iteration, count `functionCall` parts for that event and record
   `CallsPerResponse[n]++` — only for events carrying `UsageMetadata`, so
   replayed or synthetic events do not skew the denominator.
4. Report in the human summary next to the existing prompt/output token lines
   (around line 232):
   - `requests`
   - `calls_per_response` (mean, to 3 dp)
   - `prompt_tokens_per_request`
   - the 0/1/2/3/4+ histogram
5. Add the same fields to the `--json` output.

**Test:** a table-driven test over a fixture `events.jsonl` containing responses
with 0, 1, 2 and 5 function calls, asserting the mean, the histogram and
`prompt_tokens_per_request`. Include one event *without* `UsageMetadata` that
carries function calls, and assert it does not move the denominator.

**Verification:** run against `$HOME/.pi-go/sessions` and confirm it reproduces
`research/measurements.md`: 4,016 requests and 1.307 calls/response over the
same 400-directory sample. **If it does not reproduce, the measurement
methodology is wrong and the rest of the plan is suspect** — stop and reconcile
before continuing.

## Slice 2 — Capture the baseline

No code. Run the slice-1 tool over the current session corpus and commit the
output to `research/baseline.md` with the date, the sample definition and the
model in use.

This exists as its own slice so the baseline is taken with the *shipped* counter
rather than the ad-hoc Python in `research/measurements.md`, and so slice 3's
before/after cannot be accused of comparing two different measurements.

## Slice 3 — Rewrite the parallel-execution guidance (R2)

`internal/agent/agent.go`, the `# Parallel execution` section at line 185.

Requirements the new text must satisfy — the wording is the implementer's, the
content is not:

- Directive, not permissive. Match the force of line 235's
  "**Prefer parallel over sequential**".
- Names the batchable tools: `read`, `ripgrep`, `find`, `ls`, `tree`, read-only
  `bash`.
- Gives the reason: each response is a round trip that re-sends the whole
  conversation, so two independent lookups in one response cost roughly half
  what they cost in two.
- Keeps the independence caveat verbatim.
- Adds an explicit prohibition on batching `edit`, `write` and mutating `bash`,
  noting that calls execute concurrently.
- Does not contradict line 94 ("Verify after every edit … Do not batch multiple
  edits before checking").

**Test:** `internal/agent/agent_test.go` — assert the section contains the
prohibition on batching mutations and retains the independence caveat. A test
that pins exact prose is brittle; pin the load-bearing clauses only.

**Do not** grow the section significantly. It sits in the fixed preamble that
every request pays for. If the rewrite adds more than ~80 tokens, cut elsewhere
in the same section.

## Slice 4 — Pin `bash_wait` blocking (R4)

`internal/tools/bash_control_test.go` (or a new `bash_wait_block_test.go`).

Add a test: start a command that produces no output for longer than the test's
`wait_ms`, call `bash_wait`, and assert the call **did not return before**
`wait_ms` elapsed. Use a synthetic clock if the supervisor supports one;
otherwise a short real `wait_ms` (200 ms) with a generous tolerance.

This complements `5362ffd`, which pins that a wait ends on child exit rather
than on its budget. Together they pin both ends: a wait returns *early* on exit
and does *not* return early on idleness.

## Evaluation gate (R3)

After slice 3 has been in use for a working week:

1. Re-run the slice-1 counter over sessions created since the change.
2. Require ≥ 500 model requests in the after-sample.
3. Compare against `research/baseline.md`.

| Outcome | Action |
|---------|--------|
| calls/response ≥ 1.6 **and** requests per completed task down | Keep; record the result in `summary.md` |
| calls/response up, requests per task flat or up | **Revert slice 3.** The model is adding calls, not merging them |
| calls/response < 1.4 | **Revert slice 3.** The prompt change did not take |
| Between 1.4 and 1.6 | Judgement call; record the number either way |

The revert costs one commit. Do not iterate on prompt wording more than once
before reverting — the preamble is shared by every session and every subagent,
and unbounded fiddling there is how a shared prompt becomes unmaintainable.

## Not planned

- Batch API client. `design.md` § D1. Revisit only if one of its three named
  conditions becomes true.
- `bash_batch` tool. `design.md` § D3.
- Gemini/Ollama prompt caching. Separate spec.
