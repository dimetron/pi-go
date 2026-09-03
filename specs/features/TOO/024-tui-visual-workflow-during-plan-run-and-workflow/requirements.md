# Requirements

## Questions & Answers

### Q1. What should the visual workflow be based on — the compiled SOP graph or the imperative state machine?
**A.** Both. Use sidebar space to show the workflow during `/plan` and `/run`.

### Q2. How should the workflow be represented in the 23-column sidebar?
**A.** Keep the existing linear stage list as it is, and show a compact graph underneath it to show branching/loops.

### Q3. Should the sidebar graph be derived from the compiled SOP graph, or hand-authored?
**A.** Derived from the compiled SOP graph (`internal/sop` compiles `plan.sop.yaml` / `run.sop.yaml` into `*sop.Compiled` with Nodes/Edges/Order). A custom SOP override changes the graph.

### Q4. How should the TUI determine which graph stage is active?
**A.** Instrument actual execution — emit stage events from the executing workflow, not map the imperative `phase`/artifact state onto stage IDs.

### Q5. Does this feature include migrating /run and /plan to execute through the compiled ADK workflow?
**A.** Assumption to verify in research: the compiled workflow execution is already wired in; only the TUI visualization part is missing. (To be confirmed during Phase 3.)

### Q6. How should the TUI receive stage-transition events?
**A.** Reuse the existing subagent event channel — surface stage events as new event types on the same channel the /run and /plan subagents already stream into the TUI.

### Q7. What style for the compact graph?
**A.** Vertical tree with connector glyphs (`│ ├ └ →`) showing the forward path and loop-back arrows.

### Q8. How should /plan produce stage transitions (it is a multi-turn LLM flow, not a single execution)?
**A.** Emit from the plan agent — the plan agent is instructed to emit stage events as it moves between PDD phases.

### Q9. Should /run use the same stage-event mechanism, or map the existing phase field?
**A.** Both /run and /plan use the same mechanism — stage events on the existing channel. The run coordinator contract and the plan agent's PDD SOP instruction both emit stage events.

### Q10. What is the full scope?
**A.** Full scope in-scope: (a) event emission plumbing (run coordinator contract + plan agent PDD SOP instruction emit stage events), and (b) TUI rendering (compile SOP graph, render linear stage list + vertical-tree compact graph, highlight active stage from emitted events). Include tests (sidebar render, event parsing, graph layout) following existing patterns. Degrade gracefully when the terminal is too narrow or the SOP cannot be compiled.

## Scope

- **In scope:**
  - Stage-event emission from the run coordinator contract (`coordinatorContract` in `internal/tui/run.go`) and the plan agent's PDD SOP instruction (`internal/sop/pdd_default.go`).
  - New subagent event types for stage transitions (`stage_start` / `stage_end`), surfaced on the existing event channel.
  - TUI sidebar rendering: compile the SOP graph, render the existing linear stage list, add a vertical-tree compact graph showing branching/loops, highlight the active stage from emitted events.
  - Graceful degradation: hide the compact graph when the terminal is too narrow or the SOP cannot be compiled.
  - Tests: sidebar render, event parsing, graph layout, following existing patterns.

- **Out of scope (unless research shows otherwise):**
  - Migrating /run and /plan to execute through the compiled ADK workflow (assumed already wired; verify in research).

## Constraints

- Sidebar width is fixed at 23 columns (`SidebarWidth` in `internal/tui/sidebar.go`).
- The compact graph must fit within the sidebar's inner width.
- Stage events must ride the existing subagent event channel — no new side-channels.
- The graph is derived from the compiled SOP graph, so a custom SOP override changes the visualization.
- Must degrade gracefully (narrow terminal, un-compilable SOP).

## Acceptance Criteria (draft — refined in design)

- Given a `/run` in progress, when the coordinator emits a stage event, then the sidebar highlights the corresponding stage in the run graph.
- Given a `/plan` in progress, when the plan agent emits a stage event, then the sidebar highlights the corresponding stage in the plan graph.
- Given a SOP with a loop-back (e.g. run `repair → verify`), when rendered, then the compact graph shows the loop with connector glyphs.
- Given a terminal too narrow for the graph, when rendering, then the compact graph is hidden without breaking the layout.
- Given a SOP that cannot be compiled, when rendering, then the sidebar degrades to the linear stage list without crashing.

---

# Post-Research Revisions

Research (Phase 3) disproved the assumption behind Q5 and changed Q3, Q4 and Q6.
The answers above are the original record; these supersede them.

### Q5 (revised). Does this include migrating /run and /plan to the compiled workflow?
**Yes — that is now the feature.** The compiled graph is never executed: the
only `NodeFactory` is the non-executing `DescribeFactory` and
`Compiled.Workflow()` has no production caller. `/plan` and `/run` are to run as
ADK 2.x workflow agents with the missing executable factory supplied.

### Q4 (revised). How does the TUI determine the active stage?
**From the run itself, via the engine's event stream** — `NodeInfo.Path`,
`Output`/`OutputFor`, `RequestedInput`. Not an LLM-emitted marker, and not the
imperative `phase` field. Note that live `RunState` is *not* readable and node
start is *not* an event; see `research/adk-workflow-agent.md`.

### Q6 (revised). How does the TUI receive stage transitions?
**On the workflow agent's own event stream.** `/run` and `/plan` converge on one
`iter.Seq2[*session.Event, error]`. The earlier plan — new `stage_start` /
`stage_end` types on the subagent channel — is dropped: it was unimplementable,
since the child `pi --mode json` emitter writes a closed set of event types and
`/plan` has no subagent channel at all.

### Q3 (revised). Derived from the compiled graph, or hand-authored?
**Derived, from the EMBEDDED SOP only.** Overrides stay disabled until the engine
is the only executor. Added `sop.LoadEmbeddedDefinition` for this.

### Q2 / Q7 (confirmed)
Vertical layout in the 23-column sidebar: the stage list on top, a text workflow
diagram underneath, for both commands. For `/plan` the list becomes the real
stage ids from `Compiled.Order`, replacing the seven hardcoded PDD artifact
phases in `phaseArtifacts`.

## Scope (revised)

- **In scope:** an executable `NodeFactory`; hosting the compiled graph as a
  workflow agent behind `PI_SOP_ENGINE=1`; closing the declared-but-inert gaps
  (fan-out width/grouping/isolation, `max_cycles` enforcement, artifact
  validators, typed outputs, workspace ownership); human-in-the-loop review
  checkpoints; the sidebar list + diagram; tests including a both-paths parity
  test covering all six terminal outcomes.
- **Out of scope:** SOP overrides; per-node token accounting.

## Constraints (revised)

- Sidebar fixed at 23 columns; the diagram is dropped whole when rows are short,
  never clipped — `sidebarFrame` clips from the bottom.
- Stage status comes from the engine, never from parsed model output.
- A stage reports failure as a **routed value**, never as a returned error: any
  node error fails the entire workflow.
- `max_cycles` must be enforced by the factory in invocation-scoped state.
  `repair` is set to 10 cycles, matching today's `maxRetries`, so migrating the
  loop bound is a port rather than a behaviour change.
- One worktree per run, owned by the workspace rather than one per agent — see
  the decision in `design.md`.
- The imperative path stays until parity is demonstrated.
