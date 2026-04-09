# Specs — Feature Design Documents

This directory contains Plan-Driven Design (PDD) specs for pi-go features.
Each spec follows a phased workflow from rough idea through implementation.

## Phases

| Phase | File | Description |
|-------|------|-------------|
| 1. Idea | `rough-idea.md` | Initial concept, motivation, and source |
| 2. Requirements | `requirements.md` | Acceptance criteria and constraints |
| 3. Research | `research/` | Codebase exploration, gap analysis, prior art |
| 4. Design | `design.md` | Architecture, data flow, interfaces |
| 5. Plan | `plan.md` | Step-by-step implementation tasks |
| 6. Prompt | `PROMPT.md` | Agent prompt to execute the plan |
| 7. Summary | `summary.md` | Artifacts created, outcome, lessons learned |

## Spec Index

### Complete (all phases)

| Spec | Focus | Description |
|------|-------|-------------|
| [claude-mem](claude-mem/) | memory | Native claude-mem implementation in pi-go |
| [enhance-from-oh-my-pi](enhance-from-oh-my-pi/) | tools | Enhance pi-go with oh-my-pi features |
| [nanocoder-tui](nanocoder-tui/) | tools | TUI design patterns from nanocoder |
| [plan-command-sop](plan-command-sop/) | tools | `/plan` and `/run` commands with PDD SOP workflow |
| [research-coding-agents-session-log-optimizations](research-coding-agents-session-log-optimizations/) | sessions | Session log error patterns and optimizations |
| [simple-ollama-test](simple-ollama-test/) | tools | E2E test with actual Ollama provider |
| [skills-audit](skills-audit/) | skills | Security audit for SKILL.md files |
| [web-serve](web-serve/) | tools | Web-based serving interface |
| [improve-test-coverage](improve-test-coverage/) | tools | Test coverage improvements |

### In Progress (some phases done)

| Spec | Phases | Description |
|------|--------|-------------|
| [atif-support](atif-support/) | idea, research, summary | ATIF (Agent Trajectory Interchange Format) export |
| [better-completion-commands](better-completion-commands/) | idea..prompt | Improved completion/autocomplete commands |
| [evaluation-terminal-bench](evaluation-terminal-bench/) | idea..plan, summary | Terminal-Bench evaluation harness |
| [rtk-hooks-optimizer](rtk-hooks-optimizer/) | idea..plan, summary | RTK output compactor hooks |
| [skills-subagents](skills-subagents/) | idea..plan, summary | Skills-based subagent system |

### Early Stage (idea/requirements only)

| Spec | Phases | Description |
|------|--------|-------------|
| [login-with-openai-codex](login-with-openai-codex/) | idea, req, research | OAuth login for OpenAI Codex provider |
| [skill-commands-list-create-load-pull](skill-commands-list-create-load-pull/) | idea, req, research | Skill CRUD commands |
| [terminal-bench-evaluations](terminal-bench-evaluations/) | idea, req, research | Terminal bench evaluation (earlier iteration) |

### Other

| Spec | Notes |
|------|-------|
| [improvements](improvements/) | Architecture and gap analysis docs |
| [issues-fix](issues-fix/) | Bug fix plan |
| [subagent-execution-modes](subagent-execution-modes/) | Prompt only |

## Conventions

### Directory structure

```
specs/
  <feature-name>/
    rough-idea.md
    requirements.md
    research/
      01-topic.md
      02-topic.md
    design.md
    plan.md
    PROMPT.md
    summary.md
```

### Naming

- Use kebab-case for folder names
- Number research files to indicate reading order (`001-`, `002-`, ...)
- Keep names descriptive but concise

### Future improvements

- **Focus area grouping**: Organize specs into subdirectories by focus area (`sessions/`, `skills/`, `memory/`, `tools/`)
- **Versioning**: Number features in implementation order within each focus area (`001-feature/`, `002-feature/`)