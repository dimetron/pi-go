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
