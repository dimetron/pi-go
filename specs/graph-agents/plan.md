# Implementation Plan: Graph-Based Agents for pi-go

## Checklist

- [ ] Step 1: ADK Upgrade to v2.2.0+
- [ ] Step 2: SpawnNode — wrap the subprocess spawner as a workflow node
- [ ] Step 3: /run Graph — single-agent mode (task → gate → retry/merge)
- [ ] Step 4: /run Graph — parallel mode (split → JoinNode → gate → merge)
- [ ] Step 5: /run Graph — cross-turn resume via RunState persistence
- [ ] Step 6: Graph templates on the subagent tool (research, review, sequential)
- [ ] Step 7: HITL via RequestInput/Resume (merge approval)
- [ ] Step 8: Integration & E2E testing

---

## Step 1: ADK Upgrade to v2.2.0+

**Objective:** Bump `google.golang.org/adk/v2` from v2.1.0 to v2.2.0+ so pi-go gets the
workflow fixes (external context cancellation #1258, nested HITL resume #1129) and the
full graph API surface.

**Implementation guidance:**
- `go get google.golang.org/adk/v2@v2.2.0` (or the latest available; the tmp checkout is
  v2.2.0-4-g817fdc0).
- `go mod tidy`, then `make build` and `make test-unit`.
- Watch for API changes between v2.1.0 and v2.2.0 in the packages pi-go imports
  (`agent`, `llmagent`, `runner`, `session`, `tool`). The `workflow` package is new to
  pi-go, so no migration there.

**Test requirements:**
- `make build` succeeds.
- `make test-unit` passes (existing tests unaffected by the version bump).

**Demo:** `make build` produces a binary; `make test-unit` is green.

---

## Step 2: SpawnNode — wrap the subprocess spawner as a workflow node

**Objective:** Create `internal/graph/spawn_node.go` with a custom `workflow.Node` that
calls `Orchestrator.SpawnWithInput`, streams events via `emit`, and returns the result
as `Event.Output`.

**Implementation guidance:**
- Create `internal/graph/` package.
- `SpawnNode` embeds `workflow.BaseNode` (via `workflow.NewBaseNode`), holds the
  `*subagent.Orchestrator` and the `subagent.AgentConfig`.
- `Run(ctx agent.Context, input any) iter.Seq2[*session.Event, error]`:
  - Input is the task prompt (string) or a structured `{prompt, worktree_name, env}`.
  - Call `orch.SpawnWithInput(ctx, subagent.AgentInput{...})`.
  - Forward each event through `emit` (so the TUI keeps its live pipeline view).
  - Aggregate the final result text into `Event.Output`.
- Do **not** use `SpawnWithRetry` — retries come from `NodeConfig.RetryConfig`.
- Do **not** set the absolute timeout in the node — use `NodeConfig.Timeout`; the
  orchestrator's absolute timeout stays as a backstop.
- Add `SpawnNodeOptions` for worktree name, env, skip-cleanup.

**Test requirements:**
- `TestSpawnNode_Run` — with a mock orchestrator (or a stub), the node spawns, streams
  events, and returns the result as output.
- `TestSpawnNode_RetryConfig` — a failing spawn retries per the policy.
- `TestSpawnNode_Timeout` — exceeding `NodeConfig.Timeout` fails the node.
- `TestSpawnNode_InputShapes` — string and structured inputs both work.

**Integration notes:** Depends on Step 1 (ADK v2.2.0+). No other new code depends on
this yet.

**Demo:** Unit tests pass. A `SpawnNode` in a trivial `Chain(Start, node)` workflow
spawns a subagent and returns its result.

---

## Step 3: /run Graph — single-agent mode

**Objective:** Rebuild the single-agent `/run` path in `internal/tui/run.go` as a
declared graph: `START → taskAgent(SpawnNode) → gateCheck(FunctionNode) → merge`.

**Implementation guidance:**
- Create `internal/graph/run_spec.go` with `BuildRunSpecGraph(specName, prompt, gates,
  checklist) ([]workflow.Edge, []workflow.Node, error)`.
- Nodes:
  - `taskAgent` — `SpawnNode` with the "task" agent config, worktree isolation,
    `NodeConfig.Timeout` = 60 min, `RetryConfig` with `MaxAttempts` = 10 (matching
    today's `maxRetries`).
  - `gateCheck` — `FunctionNode` that runs each gate command in the worktree via
    `exec.Command`; returns pass/fail. On fail, the graph retries via `RetryConfig`
    (the retry loop re-enters `taskAgent` in the same worktree — mirror today's
    `retryRun` which re-spawns with `WorkDir: wtPath`).
  - `merge` — `FunctionNode` that merges the worktree branch back to the current branch.
- Wire the TUI: `handleRun` builds the graph, runs it, and streams events to the chat
  view. Keep gate parsing and merge bookkeeping in the TUI.
- Remove `retryRun` and the `maxRetries`/`retries` fields from `runState`.

**Test requirements:**
- `TestRunGraph_SingleAgent` — task agent runs, gates pass, merge happens.
- `TestRunGraph_GateFailRetry` — gate fails, graph retries in the same worktree.
- `TestRunGraph_GateFailExhausted` — retry budget exhausted, worktree left intact,
  failure reported with path.
- `TestRunGraph_NoGates` — no `## Gates` section → merge without validation.
- Existing `internal/tui/run_*_test.go` tests pass (adapted).

**Integration notes:** Depends on Step 2. This is the first vertical slice that proves
the pattern end-to-end.

**Demo:** `/run spec-name` executes a spec with gates, retries on failure, and merges
on success — behaviorally equivalent to today, but graph-driven.

---

## Step 4: /run Graph — parallel mode

**Objective:** Rebuild the parallel `/run` path as a graph:
`START → split → taskAgentPart1 → JoinNode → gateCheck → merge` (2 agents).

**Implementation guidance:**
- `split` — `FunctionNode` that splits the checklist into first-half/second-half
  prompts (mirroring `handleRunParallel`).
- `taskAgentPart1` / `taskAgentPart2` — `SpawnNode`s with distinct worktree names
  (`part-1`, `part-2`).
- `JoinNode` — gathers both agents' outputs into `map[string]any` keyed by node name.
- `gateCheck` — runs gates in the merged view (today gates run per-worktree; decide:
  run gates on each worktree, or merge-then-gate — match today's behavior).
- Remove `handleRunParallel`'s manual fan-in and `collapseParallel`.

**Test requirements:**
- `TestRunGraph_Parallel` — 2 agents run concurrently, JoinNode gathers both, gates
  pass, merge happens.
- `TestRunGraph_ParallelOneFails` — one agent fails; behavior matches today's
  collapse-to-single-coordinator retry.
- `TestRunGraph_ParallelJoinOrder` — JoinNode output is keyed by node name, order
  independent.

**Integration notes:** Depends on Step 3.

**Demo:** `/run spec-name --parallel` splits across 2 agents, joins, gates, merges.

---

## Step 5: /run Graph — cross-turn resume via RunState persistence

**Objective:** Make an interrupted `/run` resumable on a follow-up turn using the
workflow's `RunState` persisted in `session.State`.

**Implementation guidance:**
- The workflow must have a non-empty name (e.g. `run:{specName}`) so `RunState`
  persists. pi-go's `internal/session/store.go:780-830` already implements
  `session.State`, so no new storage work.
- The TUI's resume path (today: `retryRun` re-spawns with `buildResumePrompt`) becomes:
  detect the persisted `RunState` for the spec, call `Workflow.Resume` with the
  user's response, and continue from the paused node.
- This subsumes the `/plan resume` mechanism for the run half
  (`specs/features/SOP/plan-resume/`).

**Test requirements:**
- `TestRunGraph_Resume` — interrupt mid-run, resume on a follow-up turn, graph continues
  from the paused node.
- `TestRunGraph_ResumeIdempotent` — a completed node is not re-triggered on resume.

**Integration notes:** Depends on Step 3/4. This is the headline benefit of the graph
engine — verify it early.

**Demo:** Ctrl+C during `/run`, restart the TUI with the same session, `/run spec-name`
resumes from the paused node instead of starting over.

---

## Step 6: Graph templates on the subagent tool

**Objective:** Add `template` mode to the `subagent` tool: `research`, `review`,
`sequential`. The LLM picks a template by name; the engine handles concurrency, joins,
and routing.

**Implementation guidance:**
- Add `Template string` to `SubagentInput`; `detectMode` returns `"template"` when set.
- `internal/graph/templates.go`:
  - `research` — fan-out N explore `SpawnNode`s → `JoinNode` → synthesize `SpawnNode`.
  - `review` — task `SpawnNode` → code-reviewer `SpawnNode` → fix loop (`loopagent` or
    a graph cycle with a route on "needs_fix"/"approved").
  - `sequential` — `Chain` of `SpawnNode`s (the existing chain mode, graph-backed).
- `templateModeHandler` builds the graph, runs it through the engine, and streams
  events via the existing `SubagentEventCallback`.
- Concurrency: use `WithMaxConcurrency` on the workflow; keep the orchestrator `Pool`
  as the process-level budget. Remove the per-call `maxParallelTasks`/`maxChainSteps`
  caps (`internal/tools/subagent.go:31-33`).

**Test requirements:**
- `TestTemplate_Research` — N explores fan out, JoinNode gathers, synthesize merges.
- `TestTemplate_Review` — task → reviewer → fix loop until approved or budget exhausted.
- `TestTemplate_Sequential` — chain of spawns, `{previous}` placeholder works.
- `TestTemplate_ConcurrencyCap` — more agents than `WithMaxConcurrency` → capped by
  the engine.

**Integration notes:** Depends on Step 2. Parallel/chain modes may be re-expressed as
templates later.

**Demo:** `{template: "research", topic: "auth", agents: ["explore","explore"]}` runs a
fan-out research pipeline and returns a synthesized report.

---

## Step 7: HITL via RequestInput/Resume

**Objective:** Add orchestration-level pause/resume for human approval — e.g. the `/run`
merge step asks "merge this worktree?" before merging.

**Implementation guidance:**
- `gateCheck` (or a new `approvalCheck` node) emits a `workflow.NewRequestInputEvent`
  with a `session.RequestInput` and returns `ErrNodeInterrupted` to park the graph.
- The TUI renders the prompt; the user's reply is delivered as a `FunctionResponse`
  targeting the `InterruptID`.
- The workflow's `Resume` path (already wired in `workflowagent.run`) continues from
  the paused node.

**Test requirements:**
- `TestRunGraph_HITLApproval` — graph parks at the approval node, resumes on answer.
- `TestRunGraph_HITLReject` — user rejects → graph takes the reject route (no merge).

**Integration notes:** Depends on Step 3. kagent's `docs/architecture/a2a-subagents.md`
is a reference for HITL propagation.

**Demo:** `/run` pauses before merge with "Approve merge? (y/n)"; answering resumes or
aborts the merge.

---

## Step 8: Integration & E2E testing

**Objective:** Full pipeline verification: `/plan` → `/run` (graph) → gates → merge,
plus the subagent templates, plus resume.

**Implementation guidance:**
- E2E: `/plan` produces a PROMPT.md; `/run` executes it via the graph; gates pass;
  merge happens.
- E2E: interrupted `/run` resumes across a TUI restart.
- E2E: `{template: "research"}` produces a synthesized report.
- Update `ARCHITECTURE.md` (the subagent section, `ARCHITECTURE.md:236-290`) to show
  the graph layer.
- Update `specs/graph-agents/summary.md` with results.

**Test requirements:**
- All unit tests from Steps 1-7 pass.
- E2E tests in `internal/tui/run_eval_e2e_test.go` pass.
- `make test-all` green.

**Demo:** The full plan→run→review pipeline runs end-to-end on a sample spec.

---

## Notes

- **Vertical slicing:** Steps 3-5 are one vertical slice (the `/run` graph). Do them in
  order; each is verifiable on its own.
- **Don't delete code you didn't create:** `internal/tui/run.go` orchestration is
  replaced incrementally — keep the gate/merge helpers until the graph path is proven,
  then remove the dead state machine.
- **Worktree discipline:** per CLAUDE.md, do the work in a git worktree
  (`.worktrees/graph-agents/`), commit with `-s -S`, and never `--no-verify`.
