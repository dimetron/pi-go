# Research: Prompt/Instruction Sources for Stage Events

## 1. coordinatorContract — internal/tui/run.go:229-296

A `const` string injected into every `/run` prompt. It defines the Coordinator → Worker →
Verifier execution contract: delegate slices to workers, verify each, tick plan.md checkboxes,
spawn the Verifier, and end with a `VERDICT: PASS/FAIL` line.

**Injection points** (all use `fmt.Fprintf(&b, coordinatorContract, specName)` — `%[1]s` is the spec name):
- `buildRunPrompt` — run.go:301-325, injection at 304. Used by single-agent `/run` (startRunAgent, run.go:514).
- `buildParallelPrompt` — run.go:678-697, injection at 681. Used by parallel `/run` (handleRunParallel, run.go:575-576).
- `buildResumePrompt` — run.go:1089-1108, injection at 1099. Used by retry cycles (retryRun, run.go:1046).

**Machine-readable signals today:** only the `VERDICT:` line, parsed by `sop.ParseVerdict`
(run.go:959). There is **no structured stage/phase event** emitted from the coordinator prompt.
Progress is tracked by the TUI re-reading plan.md checkboxes (`refreshRunChecklist`,
run.go:1692-1703) and by the `runState.phase` field (run.go:54), set by the TUI event loop.

**Implication:** to emit stage events from the run coordinator, the `coordinatorContract`
must be extended with an instruction telling the coordinator to emit a machine-readable
stage marker (e.g. a line like `STAGE: <id>` or a `{"type":"stage_start",...}` JSON event)
as it enters/leaves each stage. The TUI's `handleRunAgentEvent` must parse it.

## 2. DefaultPDDSOP — internal/sop/pdd_default.go:6-190

A `const` string, the embedded default PDD SOP instruction. Phases (each produces an artifact):
- Phase 1: Skeleton Creation (Already Done) — 20-22
- Phase 2: Requirements Clarification — 24-29 (Q&A appended to requirements.md)
- Phase 3: Objective Research — 31-44 (delegate to parallel explore subagents; write research/*.md)
- Phase 4: Design Discussion — 46-58 (write design.md)
- Phase 5: Structure Outline — 60-66 (write outline.md)
- Phase 6: Implementation Plan — 68-94 (write plan.md with `- [ ] Slice N:` checkboxes)
- Phase 7: PROMPT.md Generation — 96-104 (write PROMPT.md)

The SOP instructs the LLM to "Announce the phase transition clearly (e.g., 'Moving to Phase 3:
Research')" (pdd_default.go:18) — a text-only instruction, not a machine event.

**Loading:** `sop.LoadPDD(workDir)` (sop.go:11-32): project `.pi-go/sops/pdd.md` → global
`~/.pi-go/sops/pdd.md` → embedded `DefaultPDDSOP`.

**Injection:** `startPlanSession` (plan.go:392-484): `sop.LoadPDD` (398), assembles instruction
(408-417) = `sopText + "\n\n" + validate.PlanContract().Describe() + "\n## Current Task\n" + ...`,
injects via `m.cfg.Agent.RebuildWithInstruction(instruction)` (428), creates fresh session (438),
persists `PlanContext` with `Phase: "plan"` (457-462), sends rough idea as first user message,
starts `m.startAgentLoop(roughIdea)` (483).

## 3. Plan agent's multi-turn flow — artifact writing and phase signaling

`/plan` runs through the **standard agent loop** (`startAgentLoop`, agent_loop.go:719-726 →
`runAgentLoop` at 867 → `streamTurn` at 969). It is a normal interactive multi-turn conversation:
the LLM writes artifacts with `write`/`edit` tools; the user reviews/approves between phases.

**Does the LLM write artifact files?** Yes. The SOP instructs it to write to `specDir/` using
write/edit tools (plan.go:416-417). `finishPlanWorktree` (plan.go:229-291) commits the worktree,
validates artifacts, and merges on turn completion. It is invoked from `handleAgentDone`
(agent_loop.go:1729-1733) when `m.mode == "plan"`.

**How would it signal phase transitions?** There is **no existing event emission** from the plan
agent for phase transitions. The only phase-related signals:
- The SOP's prose instruction to "Announce the phase transition clearly" (pdd_default.go:18).
- **Sidebar inference from artifact file existence:** `detectPlanPhases` (sidebar.go:98-105)
  stats each phase artifact and marks a phase `Done` when the file exists. Driven from
  tui.go:1734 (`in.PlanPhases = detectPlanPhases(specDir)`). The TUI polls disk, not the LLM.
- `PlanContext.Phase` (store.go:33) is set once to `"plan"` at session creation (plan.go:461),
  never updated per-phase.
- Lifecycle events `turn_complete` and `user_input_required` via `runLifecycleHooks`
  (agent_loop.go:1754-1758) fire on turn completion, not on phase transitions.

**Implication:** to emit stage events from the plan agent, the `DefaultPDDSOP` instruction must
be extended to tell the LLM to emit a machine-readable stage marker as it moves between PDD
phases (e.g. a line like `STAGE: research` when it starts Phase 3). The TUI's plan agent-loop
handler must parse it. Because `/plan` uses the `agentMsg` channel (not `subagent.Event`), the
stage marker must be parsed from the plan agent's streamed text/tool events.

## 4. session.AgentContext / run-tree recording (commit be8daa7)

`internal/session/store.go:79-91`:
```go
type AgentContext struct {
    AgentID   string `json:"agentID,omitempty"`
    AgentType string `json:"agentType,omitempty"` // task | worker | quick-task | code-reviewer | explore
    ParentID  string `json:"parentSessionID,omitempty"`
    RunID     string `json:"runID,omitempty"` // groups every session of one /run
    SpecName  string `json:"specName,omitempty"`
    Slice     int    `json:"slice,omitempty"`
    Cycle     int    `json:"cycle,omitempty"` // /run retry index
    Worktree  string `json:"worktree,omitempty"`
    Branch    string `json:"branch,omitempty"`
    Status    string `json:"status,omitempty"` // terminal status, written when the run ends
}
```

Nested optional block on `Meta` (store.go:73-75, `Agent *AgentContext json:"agentContext,omitempty"`).

**Populated via env vars** (agentenv.go:14-24): `PI_AGENT_ID`, `PI_AGENT_TYPE`, `PI_RUN_ID`,
`PI_SPEC_NAME`, `PI_RUN_SLICE`, `PI_RUN_CYCLE`, `PI_PARENT_SESSION`, `PI_AGENT_BRANCH`,
`PI_WORKTREE_ROOT`. `AgentContextFromEnv()` (31-48) builds the struct; `isEmpty()` (53-59)
returns nil when nothing identifying is set.

The child writes it onto its own session at creation (`Agent.CreateSession`, agent.go:530-539).
The orchestrator fills in agent id/type/worktree; the caller supplies run-level fields. `/run`
mints one run id per invocation via `newRunID` (run.go:2086-2088) and passes it to every spawned
agent through `runAttribution` (run.go:2096-2105), including retries and both parallel halves.

**Relevance to stage tracking:** `AgentContext` records **where** an agent sits in a run tree
(static attribution metadata) — it does **not** record stage/phase transitions. It is relevant
only in that it provides the identity/attribution context (run id, spec, slice, cycle) a stage
event would need to be attributed to, and it is the existing mechanism (env vars + session
metadata) through which such context already flows to spawned agents.
