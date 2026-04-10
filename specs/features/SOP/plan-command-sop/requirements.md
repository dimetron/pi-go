# Requirements: /plan Command SOP Workflow

## Questions & Answers

### Q1: Should `/plan` be a multi-turn interactive conversation (like the PDD SOP — asking questions one at a time, doing research, iterating) or a single-shot command where the LLM generates the full spec in one pass?

The interactive approach matches ralph's Inquisitor→Architect flow and produces better specs, but takes longer. The single-shot approach is faster but may miss edge cases.

**Answer:** Yes, `/plan` should be the full PDD SOP — multi-turn interactive conversation following the Inquisitor→Architect flow. Requirements clarification one question at a time, research phase, design, implementation plan, and PROMPT.md generation.

### Q2: How should `/plan` take input? Options:

- **a)** `/plan <inline text>` — rough idea typed directly after the command
- **b)** `/plan <file path>` — read idea from a file
- **c)** `/plan` with no args — prompt the user to type/paste the idea
- **d)** All of the above (detect automatically)

**Answer:** Option (a) — `/plan <inline text>`. The rough idea is typed directly after the command.

### Q3: For the `/run` command, should it:

- **a)** `/run` with no args — auto-detect PROMPT.md in the most recent specs/ directory
- **b)** `/run <spec-name>` — run from `specs/<spec-name>/PROMPT.md`
- **c)** `/run <path>` — run any PROMPT.md file by path
- **d)** Combination (b + c, with b as shorthand)

**Answer:** Option (b) — `/run <spec-name>` looks up `specs/<spec-name>/PROMPT.md`.

### Q4: Should `/run` execute the PROMPT.md using the existing subagent system (spawn a "task" type agent with worktree isolation), or should it run in the current agent session (like sending the PROMPT.md content as a regular prompt)?

- **a)** Subagent with worktree — isolated execution, streamed results, non-blocking
- **b)** Current session — simpler, user can interact mid-execution, but blocks the conversation
- **c)** Let user choose with a flag (e.g., `/run --bg <spec-name>` for subagent)

**Answer:** Option (a) — Subagent with worktree isolation. Spawn a "task" type agent, stream results back to TUI.

### Q5: During `/plan`'s interactive PDD flow, who drives the conversation — the LLM agent (asking questions, proposing research, writing artifacts) or does the TUI need custom multi-step state management?

Two approaches:
- **a)** Send the PDD SOP as a system instruction to the agent, then let the normal agent loop drive the conversation. The LLM asks questions, user responds via normal prompt, artifacts get written via tool calls (write tool). Minimal TUI changes — just the `/plan` command kickoff.
- **b)** Build dedicated TUI state machine for PDD phases (requirements, research, design, plan). Custom UI for each phase, progress tracking, phase transitions.

**Answer:** Option (a) — LLM-driven via system instruction. Send the PDD SOP as system instruction, let the agent loop handle the multi-turn flow. Minimal TUI changes — just the `/plan` command kickoff and the SOP prompt injection.

### Q6: Which model role should `/plan` use? The existing "plan" role seems like a natural fit, but the PDD SOP involves both research (reads codebase, fetches URLs) and design (writes artifacts). Should it:

- **a)** Use the "plan" role (read-only tools, no write access — would need to add write tool)
- **b)** Use the "default" role (full tool access)
- **c)** Use the "slow" role (thorough thinking, full tools — like the designer agent type)

**Answer:** Option (b) — Use the "default" role with full tool access. The PDD flow needs to read code, write spec artifacts, and potentially fetch URLs.

### Q7: Should `/plan` produce artifacts incrementally (write each file as the SOP progresses — rough-idea.md first, then requirements.md updated after each Q&A, etc.) or batch-write everything at the end?

The SOP says "record as you go" — but confirming since this affects how the system instruction is written.

**Answer:** First create the full skeleton directory from the rough idea (rough-idea.md, empty requirements.md, research/, etc.), then update files incrementally as the SOP progresses — append Q&A to requirements.md after each answer, write research notes as discovered, etc.

### Q8: For the PDD SOP system instruction — should it be embedded as a Go string constant in the codebase, or loaded from an external file (e.g., `.pi-go/sops/pdd.md` or a config-referenced path)?

- **a)** Embedded Go constant — simpler, no file dependency, versioned with the binary
- **b)** External file — user-customizable, can be updated without rebuilding
- **c)** Embedded default with external override — best of both

**Answer:** Option (c) — Embedded default with external override. Ship a built-in PDD SOP, but allow users to override with a custom file.

### Q9: When `/run` spawns the task agent, should the user see streaming output in the TUI (like watching the agent work in real-time), or just a progress indicator with the final result shown at the end?

- **a)** Full streaming — see every text delta, tool call, and tool result as it happens
- **b)** Summary only — progress spinner, then final result
- **c)** Condensed streaming — show tool calls and key milestones, skip verbose text deltas

**Answer:** Option (a) — Full streaming. Show every text delta, tool call, and tool result as the task agent works.

### Q10: After `/run` completes, should it automatically merge the worktree changes back, or ask the user to review first?

- **a)** Auto-merge — merge worktree branch back to current branch on success
- **b)** Ask first — show summary of changes, user confirms merge
- **c)** Leave worktree — user manually reviews and merges (e.g., via `/branch merge`)

**Answer:** Option (a) — Auto-merge worktree branch back to current branch on successful completion.

### Q11: Should `/run` run any validation before merging (e.g., `go build ./...`, `go test ./...`) or trust the agent's own work?

- **a)** No validation — trust the agent (it already runs tests as part of its instruction)
- **b)** Run build + test gate before merge — like ralph's backpressure gates
- **c)** Configurable — default to build+test, but allow skipping

**Answer:** Option (b) — Run build + test gate before merge. Like ralph's backpressure gates — `go build ./...` and `go test ./...` must pass before merging back.

### Q12: Should the build/test gate commands be hardcoded (`go build/test`) or configurable in pi-go config (to support non-Go projects)?

- **a)** Hardcoded for Go — pi-go is a Go tool, keep it simple
- **b)** Configurable — `gates: [{name: "build", cmd: "go build ./..."}, {name: "test", cmd: "go test ./..."}]` in config

**Answer:** Neither — discover the build/test commands during the `/plan` phase (detect project type, build system, test runner) and embed them in the PROMPT.md or a companion artifact. `/run` then uses those discovered commands for the gate. This makes it project-agnostic without manual config.

### Q13: Where should the discovered gate commands be stored? Options:

- **a)** In PROMPT.md itself under a `## Gates` section
- **b)** In a separate `specs/<task_name>/gates.yml` file
- **c)** In the `## Constraints` section of PROMPT.md (natural fit — already lists build/test constraints)

**Answer:** Option (a) — Dedicated `## Gates` section in PROMPT.md. `/run` parses this section to extract the build/test commands for the pre-merge gate.

### Q14: Should `/run` update the plan.md checklist as it progresses?

**Answer:** Yes — `/run` should update `specs/<task_name>/plan.md` checklist items from `[ ]` to `[x]` as each step is completed during execution.

### Q15: When the gate fails after `/run` completes, what should happen?

- **a)** Abort and leave worktree intact — user can inspect and fix manually
- **b)** Retry — re-spawn the agent to fix the failures
- **c)** Ask the user — show the failure and let them choose (retry/abort/merge anyway)

**Answer:** Option (b) — Retry. Re-spawn the agent with the gate failure output so it can fix the issues. (Needs a max retry limit to avoid infinite loops.)
