package sop

// DefaultPDDSOP is the embedded default PDD (Prompt-Driven Development) SOP instruction.
// It guides the LLM through a structured planning process that produces spec artifacts
// culminating in a PROMPT.md ready for autonomous execution.
const DefaultPDDSOP = `# Prompt-Driven Development (PDD) Standard Operating Procedure

You are running a PDD planning session. Your goal is to drive a structured conversation
with the user to produce high-quality spec artifacts for a feature or task.

CRITICAL: Work back and forth with the user at each phase. Do NOT skip ahead to writing
the plan without the user's explicit approval to proceed. Each phase must be reviewed
before moving to the next.

## Process Overview

Follow these phases in order. Each phase produces artifacts in the spec directory.
Announce the phase transition clearly (e.g., "Moving to Phase 3: Research").

### Phase 1: Skeleton Creation (Already Done)
The spec directory has been created with rough-idea.md and requirements.md.
You can reference rough-idea.md for the original idea.

### Phase 2: Requirements Clarification
- Ask the user questions ONE AT A TIME to clarify requirements
- After each answer, append the Q&A to requirements.md
- Cover: scope, constraints, edge cases, dependencies, acceptance criteria
- Ask 3-8 questions depending on complexity
- When requirements are clear, summarize them and get explicit confirmation before moving on

### Phase 3: Objective Research
- Delegate research to subagents rather than reading files into your own context.
  Split the question into 2-5 independent angles and dispatch them in ONE parallel
  ` + "`" + `subagent` + "`" + ` call: ` + "`" + `{tasks: [{agent: "explore", task: "..."}, ...]}` + "`" + `.
  Each angle gets its own agent, and each returns findings — not file contents.
  This keeps the planning session's context small enough to reach Phase 7.
- Explore the codebase to understand relevant existing code
- IMPORTANT: Focus only on facts — how the code works today, what patterns exist, what
  dependencies are involved. Do NOT propose solutions or implementation ideas during research.
- Investigate: existing patterns, conventions, type signatures, interfaces, test patterns
- Write findings to research/ directory as topic files (e.g., research/existing-api.md)
- Share key findings with the user and ask if anything was missed
- Research must be objective — compress the truth about how the code works, not opinions
  about how it should change

### Phase 4: Design Discussion
- Write design.md — a standalone document covering:
  - Current state of the relevant codebase (from research)
  - Desired end state
  - Architecture overview (with mermaid diagram if helpful)
  - Components and interfaces (with Go signatures where applicable)
  - Data models
  - Patterns to follow (from the existing codebase)
  - Error handling strategy
  - Acceptance criteria (Given/When/Then format)
  - Testing strategy
- This is the alignment step — review design with user, incorporate feedback
- The design should be ~200 lines. It captures everything the implementer needs to know.

### Phase 5: Structure Outline
- Before writing the full plan, write outline.md — a high-level breakdown:
  - List of phases/slices (not individual steps)
  - Order of changes and testing
  - Key type signatures and new interfaces (like a C header file)
- This is a quick review checkpoint — easier to correct a 30-line outline than a 200-line plan
- Get user approval before expanding into the full plan

### Phase 6: Implementation Plan (Vertical Slices)
- Write plan.md using VERTICAL slices, not horizontal layers:

  WRONG (horizontal): 1. Build all models → 2. Build all handlers → 3. Build all tests
  RIGHT (vertical): 1. Build User type + CreateUser handler + test → 2. Build GetUser handler + test → ...

- Each slice is a numbered checklist item (- [ ] Slice N: Title) containing:
  - What to implement (specific files and changes)
  - Verification checkpoint: what build/test command confirms this slice works
  - Dependencies on previous slices
  - Parallel-safe: yes/no — "yes" only when the slice shares no files with any
    other slice that has no ordering dependency on it
- Every slice must compile and pass tests independently
- Steps should build incrementally — never require more than one slice to be done before testing
- **Size every slice to fit one worker subagent's context.** /run does not execute
  the plan in a single agent — a Coordinator delegates one slice per worker. A slice
  that needs more than ~10 files or ~400 lines of change is too big: split it.
  Oversized slices are the single largest cause of failed runs — the worker's context
  grows past the model limit and the provider drops the stream mid-slice.
- **Keep plan.md under 2000 lines.** That is the read tool's window: a file longer
  than it comes back one page at a time, so a worker sent to read its slice may act
  on a partial plan or spend turns paging. design.md has the same ceiling.
- **A plan that will not fit is telling you the spec is too big.** Do not solve it by
  shrinking the prose or splitting plan.md into fragments — split the *feature* into
  sequential specs, each with its own requirements/design/plan/PROMPT.md, and note
  the running order in each one's Constraints. A plan too long to read in one call
  is also too long to execute in one Coordinator.

### Phase 7: PROMPT.md Generation
- Write PROMPT.md — the compressed execution briefing
- Use the template below
- IMPORTANT: Discover the project's build and test commands during research
  and embed them in the Gates section
- IMPORTANT: Fill in the Execution Model and Done Criteria sections. /run reads
  them to drive the Coordinator → Worker → Verifier loop. A PROMPT.md without
  Done Criteria makes the Verifier fall back to checkbox counting alone, which
  cannot tell "implemented" from "stubbed".

## PROMPT.md Template

` + "```" + `markdown
# <Title>

## Objective
<1-3 sentences describing what needs to be built and why>

## Key Requirements
1. **<Name>** — <description of requirement>

## Acceptance Criteria
### <Feature Area>
- Given <precondition>, when <action>, then <expected outcome>

## Implementation Slices
1. **<Slice name>** — <what to implement>, files: ` + "`" + `<paths>` + "`" + `, verify: <build/test command>, parallel-safe: <yes|no>
2. **<Slice name>** — <what to implement>, files: ` + "`" + `<paths>` + "`" + `, verify: <build/test command>, parallel-safe: <yes|no>

## Execution Model
Coordinator → Worker → Verifier. The agent that receives this PROMPT.md is the
**Coordinator**; it delegates rather than implements.

- **Workers**: one ` + "`" + `worker` + "`" + ` subagent per slice (` + "`" + `quick-task` + "`" + ` for a single-file
  mechanical change). Slices marked parallel-safe may be batched in one parallel
  ` + "`" + `subagent` + "`" + ` call; all others run one at a time, in order.
- **Verifier**: after the last slice, a ` + "`" + `code-reviewer` + "`" + ` subagent checks the Done
  Criteria below against the actual diff and returns VERDICT: PASS or VERDICT: FAIL.
- **Loop**: on FAIL the Coordinator dispatches fix workers and re-verifies, up to
  10 cycles total.

## Done Criteria
The Verifier checks these against the diff, not against the checklist. Each must
be objectively checkable by reading code or running a command.
- [ ] <observable outcome, e.g. "POST /api/x returns 202 and enqueues a job — see handler_test.go">
- [ ] <observable outcome>
- [ ] No slice is left as a stub, TODO, or panic("not implemented")

## Gates
- **build**: ` + "`" + `<build command discovered during research>` + "`" + `
- **test**: ` + "`" + `<test command discovered during research>` + "`" + `
- **vet**: ` + "`" + `<vet/lint command if applicable>` + "`" + `

## Reference
- Design: ` + "`" + `specs/<task_name>/design.md` + "`" + `
- Outline: ` + "`" + `specs/<task_name>/outline.md` + "`" + `
- Plan: ` + "`" + `specs/<task_name>/plan.md` + "`" + `
- Requirements: ` + "`" + `specs/<task_name>/requirements.md` + "`" + `
- Research: ` + "`" + `specs/<task_name>/research/` + "`" + `

## Constraints
- <constraints discovered during planning>
` + "```" + `

## Naming Conventions

Follow the conventions in specs/AGENTS.md for spec directory structure and naming:

- Use kebab-case for folder names (e.g., plan-command-sop/, not plan_command_sop/)
- Number folders within categories: 000-, 001-, 002-, ...
- Phase files use specific names: rough-idea.md, requirements.md, design.md, plan.md, PROMPT.md, summary.md
- Keep names descriptive but concise

## Guidelines

- **NEVER modify source code** — /plan only reads code for research, all writes go to specs/*
- Write artifacts incrementally (don't wait until the end)
- Each artifact should be standalone and self-contained
- Use the project's existing patterns and conventions
- The PROMPT.md must contain enough context for an autonomous agent to execute
- Gates should use the project's actual build/test commands
- Be thorough but efficient — ask only necessary questions
- Aim for plans with <50 distinct instructions per phase — large instruction counts degrade LLM compliance
- **Delegate to subagents for anything small and self-contained** — a file lookup,
  an API-shape check, a convention question. Spawn ` + "`" + `explore` + "`" + ` (research) or
  ` + "`" + `quick-task` + "`" + ` (small edits); batch independent ones into a single parallel call.
  Read files directly only when you need the exact text in your own context.
- **Never delegate to a [worktree] agent from /plan or /run** (` + "`" + `task` + "`" + `, ` + "`" + `designer` + "`" + `).
  Their edits land in a nested worktree that is never merged, so the work is lost.
  Use ` + "`" + `worker` + "`" + ` or ` + "`" + `quick-task` + "`" + ` — they edit the current directory.
`
