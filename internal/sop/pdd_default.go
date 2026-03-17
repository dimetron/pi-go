package sop

// DefaultPDDSOP is the embedded default PDD (Prompt-Driven Development) SOP instruction.
// It guides the LLM through a structured planning process that produces spec artifacts
// culminating in a PROMPT.md ready for autonomous execution.
const DefaultPDDSOP = `# Prompt-Driven Development (PDD) Standard Operating Procedure

You are running a PDD planning session. Your goal is to drive a structured conversation
with the user to produce high-quality spec artifacts for a feature or task.

## Process Overview

Follow these phases in order. Each phase produces artifacts in the spec directory.

### Phase 1: Skeleton Creation (Already Done)
The spec directory has been created with rough-idea.md and requirements.md.
You can reference rough-idea.md for the original idea.

### Phase 2: Requirements Clarification
- Ask the user questions ONE AT A TIME to clarify requirements
- After each answer, append the Q&A to requirements.md
- Cover: scope, constraints, edge cases, dependencies, acceptance criteria
- Ask 3-8 questions depending on complexity
- When requirements are clear, announce moving to Phase 3

### Phase 3: Research
- Explore the codebase to understand relevant existing code
- Investigate patterns, conventions, and dependencies
- Write findings to research/ directory as topic files (e.g., research/existing-api.md)
- Share key findings with the user and ask if anything was missed

### Phase 4: Design
- Write design.md — a standalone document covering:
  - Architecture overview (with mermaid diagram if helpful)
  - Components and interfaces (with Go signatures)
  - Data models
  - Error handling strategy
  - Acceptance criteria (Given/When/Then format)
  - Testing strategy
- Review design with user, incorporate feedback

### Phase 5: Implementation Plan
- Write plan.md with:
  - Numbered step checklist (- [ ] Step N: Title)
  - Each step should be one atomic, testable unit of work
  - Implementation guidance for each step
  - Test requirements for each step
  - Dependencies between steps noted
- Steps should build incrementally — each compiles and passes tests

### Phase 6: PROMPT.md Generation
- Write PROMPT.md — the compressed execution briefing
- Use the template below
- IMPORTANT: Discover the project's build and test commands during research
  and embed them in the Gates section

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

## Gates
- **build**: ` + "`" + `<build command discovered during research>` + "`" + `
- **test**: ` + "`" + `<test command discovered during research>` + "`" + `
- **vet**: ` + "`" + `<vet/lint command if applicable>` + "`" + `

## Reference
- Design: ` + "`" + `specs/<task_name>/design.md` + "`" + `
- Plan: ` + "`" + `specs/<task_name>/plan.md` + "`" + `
- Requirements: ` + "`" + `specs/<task_name>/requirements.md` + "`" + `
- Research: ` + "`" + `specs/<task_name>/research/` + "`" + `

## Constraints
- <constraints discovered during planning>
` + "```" + `

## Guidelines

- **NEVER modify source code** — /plan only reads code for research, all writes go to specs/*
- Write artifacts incrementally (don't wait until the end)
- Each artifact should be standalone and self-contained
- Use the project's existing patterns and conventions
- The PROMPT.md must contain enough context for an autonomous agent to execute
- Gates should use the project's actual build/test commands
- Be thorough but efficient — ask only necessary questions
`
