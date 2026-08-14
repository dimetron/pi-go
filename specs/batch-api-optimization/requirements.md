# Requirements

## Scope

Reduce the number of model round trips per unit of work, and make that number
measurable. No Batch API client — `design.md` § D1 rejects it with numbers. No
new tools. No provider or ADK changes.

## Acceptance criteria

### R1 — Round trips are a reported metric

**Given** `pi session-stats --json` over a directory of sessions
**When** it reports
**Then** the output includes `requests`, `calls_per_response` and
`prompt_tokens_per_request`.

- `requests` counts events carrying `UsageMetadata` — one per model request.
- `calls_per_response` is total `functionCall` parts ÷ `requests`.
- `prompt_tokens_per_request` is Σ `promptTokenCount` ÷ `requests`.
- All three appear in the human-readable summary as well as `--json`.
- A histogram of calls-per-response (0, 1, 2, 3, 4+) is reported, because the
  mean alone hides whether the model batches rarely-and-widely or often-and-narrowly.

**Rationale:** `sweepTotals` (`internal/tools/session_sweep.go:41`) today tracks
`PromptTokens`, `OutputTokens`, `ToolBytes`, `DupBytes`, `Tools`, `Aborts` and
`Models` — no request count. The accumulation loop already branches on
`ev.UsageMetadata != nil` (line 125) and `p.FunctionCall != nil` (line 134), so
both counters land in code that already runs. Without R1, R2 is unfalsifiable.

### R2 — The system prompt directs batching, and it is safe

**Given** a session doing read-only exploration
**When** the model has several independent lookups to perform
**Then** it issues them in one response.

- `internal/agent/agent.go` § "Parallel execution" is rewritten from permission
  ("You **can** call multiple tools") to instruction, matching the emphasis the
  subagent section already uses at line 235.
- It names the batchable tools explicitly: `read`, `grep`/`ripgrep`, `find`,
  `ls`, `tree`, and read-only `bash`. Together these are 75.7% of measured calls.
- It states the reason: each response is a round trip that re-sends the entire
  conversation.
- **The independence caveat survives verbatim.** "Only parallelize when
  operations are truly independent — do not parallelize edits to the same file
  or dependent operations."
- It states explicitly that `edit`, `write` and mutating `bash` are **never**
  batched. ADK executes calls concurrently
  (`internal/llminternal/base_flow.go:1063`), so concurrent writes are a real
  race, not a hypothetical one.
- The existing "Verify after every edit — run the build or relevant test
  immediately. Do not batch multiple edits before checking."
  (`internal/agent/agent.go:94`) must not be contradicted.

### R3 — The change is verified against measurement, not opinion

**Given** the metric from R1
**When** sessions are compared before and after R2
**Then** the comparison is recorded with n, and the change is kept or reverted
on that evidence.

- Baseline is stated in `research/measurements.md`: **1.307** calls per
  response, 71.3% of responses carrying exactly one, n = 4,016.
- Target: **≥ 1.6**. Measured ceiling is 1.73 and assumes perfect independence,
  so 1.6 is deliberately short of it.
- Comparison uses at least 500 model requests on each side.
- **A rise in calls-per-response with no fall in total requests per completed
  task is a failure, not a success** — it means the model is issuing extra calls
  rather than merging existing ones.
- If the target is not met, the prompt change is reverted rather than iterated
  on indefinitely. Cost of failure is one revert.

### R4 — The poll storm cannot return

**Given** a backgrounded command producing no output
**When** `bash_wait` is called on its handle
**Then** the call blocks until output appears, the command exits, or `wait_ms`
elapses — it does not return empty immediately.

- A test pins this. Commit `5362ffd` pins that a wait ends on child *exit*; the
  untested half is that an *idle* command makes the call block.
- The tool description must keep telling the model not to loop
  (`internal/tools/bash.go:187`).

**Rationale:** 799 of 4,016 responses in the measured window were poll-only,
costing 19.7% of all prompt tokens (`research/measurements.md` § M5). The fix
landed in `ffe337a`; nothing pins the behaviour that delivers it.

### R5 — The Batch API rejection is recorded, not just decided

**Given** a future proposal to add a Batch API client
**When** someone looks for prior art
**Then** `design.md` § D1 gives the payoff ($0.63–$2.96/day), the cost (durable
job store, three provider paths, 24-hour latency), and the three specific
conditions that would reverse the decision.

## Constraints

- **`gpt-5.6-luna` is the configured default role.** At $0.20/MTok input, R2's
  target payoff is ~$3.76/day. The same traffic on Opus 5 is ~$93/day. Do not
  justify this work on cost alone at current settings — the latency argument
  (ADK runs batched calls concurrently) is the stronger one today.
- No new dependencies. The Anthropic, OpenAI and Gemini Go SDKs already vendor
  batch clients; this spec deliberately does not use them.
- Gate for every slice: `make test`, `make vet`, `make lint`.
- `internal/cli` tests bind local listeners and must run outside the sandbox
  (`CLAUDE.md` § "Two environment traps").
- Prompt changes touch `internal/agent/agent.go`, which is shared by every
  session and every subagent. A regression here is expensive and invisible;
  R3's measurement gate is not optional.

## Out of scope

- Provider Batch API integration (`design.md` § D1).
- A `bash_batch` tool (`design.md` § D3).
- Prompt caching for Gemini and Ollama — real gap, different mechanism, own spec.
- Auto-compaction's prompt-cache invalidation
  (`internal/session/compaction.go:26`) — noted, unquantified, own spec.
- Tool-output compaction. Already handled by `internal/tools/compactor*.go`, and
  tool results are a small share of prompt spend.
- Anything in `specs/memory-fixes`. R4 of that spec removes the compressor's
  per-call child process, which is worth more than any batching change here.
