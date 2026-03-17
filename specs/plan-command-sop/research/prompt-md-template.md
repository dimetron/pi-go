# Research: PROMPT.md Template Pattern

## Analysis of Existing Specs

Two existing PROMPT.md files analyzed:
1. `specs/enhance-from-oh-my-pi/PROMPT.md` — large multi-feature spec (59 lines)
2. `specs/simple-ollama-test/PROMPT.md` — focused single-task spec

## Common Structure

Both share these sections in order:

```
# <Descriptive Title>

## Objective
1-3 sentences, references specs/ folder

## Key Requirements
Numbered list with **bold names** and descriptions

## Acceptance Criteria
Given/When/Then format (grouped by feature if multi-feature)

## Reference
Paths to design.md, plan.md, research/, requirements.md

## Constraints
Technical constraints (build system, libraries, compat)
```

## Variable Sections

- `## Known Issues to Investigate` — only in simple-ollama-test (risks/unknowns)
- `## Constraints` — only in enhance-from-oh-my-pi (technical constraints)

## Template

```markdown
# <Title>

## Objective
<1-3 sentences. What to build and why. Reference specs/{task_name}/ for full context.>

## Key Requirements
1. **<Name>** — <Dense description of scope, components, key details>
2. ...

## Acceptance Criteria
### <Feature Area>  <!-- if multiple features -->
- Given <precondition>, when <action>, then <expected outcome>
- ...

## Reference
- Design: `specs/<task_name>/design.md`
- Plan: `specs/<task_name>/plan.md` (<N steps>)
- Requirements: `specs/<task_name>/requirements.md`
- Research: `specs/<task_name>/research/`

## Constraints
- <Language, build, tooling constraints>
- <Library/dependency constraints>
```

## Key Insight

PROMPT.md is a **compressed briefing** for an implementing agent:
- Objective from design.md Overview
- Requirements summarized from design.md components
- Acceptance criteria from design.md (simplified)
- References point back to full artifacts for deep context
- Under 100 lines — enough to understand scope without overwhelming

This is exactly what `/plan` should produce at the end, and what `/run` should consume as input.
