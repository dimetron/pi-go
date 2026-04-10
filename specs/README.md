# Specs — Feature Design Documents

This directory contains Plan-Driven Design (PDD) specs for pi-go features.
Each spec follows a phased workflow from rough idea through implementation.

## Organization

```
specs/
├── 000-evaluations/       # Benchmark/evaluation specs
├── 001-features/          # Feature implementations by component
│   ├── 000-MEM/          # Memory system
│   ├── 001-SOP/          # Standard Operating Procedures
│   ├── 002-SUB/          # Subagents
│   ├── 003-TST/          # Testing & audit
│   ├── 004-TUI/           # Terminal UI
│   └── 005-WEB/          # Web server
├── 002-improvements/       # Architecture improvements
├── 003-issues/            # Issue tracking/fixes
├── 004-research/          # Research documents
└── 005-tools/             # Tool specifications
```

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

### 001-Features / 000-MEM (Memory)

| Spec | Phases | Description |
|------|--------|-------------|
| claude-mem/ | idea..summary | Native claude-mem implementation in pi-go |

### 001-Features / 001-SOP (Standard Operating Procedures)

| Spec | Phases | Description |
|------|--------|-------------|
| plan-command-sop/ | idea..summary | `/plan` and `/run` commands with PDD SOP workflow |
| plan-resume/ | idea..plan | Resume interrupted plan execution |
| enhance-from-oh-my-pi/ | idea..summary | Enhance pi-go with oh-my-pi features |

### 001-Features / 002-SUB (Subagents)

| Spec | Phases | Description |
|------|--------|-------------|
| skills-subagents/ | idea..summary | Skills-based subagent system |
| subagent-execution-modes/ | prompt | Subagent execution modes |

### 001-Features / 003-TST (Testing & Audit)

| Spec | Phases | Description |
|------|--------|-------------|
| atif-support/ | idea, research, summary | ATIF (Agent Trajectory Interchange Format) export |
| simple-ollama-test/ | idea..summary | E2E test with actual Ollama provider |
| skills-audit/ | idea..summary | Security audit for SKILL.md files |
| skill-commands-list-create-load-pull/ | idea, req, research | Skill CRUD commands |

### 001-Features / 004-TUI (Terminal UI)

| Spec | Phases | Description |
|------|--------|-------------|
| nanocoder-tui/ | idea..summary | TUI design patterns from nanocoder |
| better-completion-commands/ | idea..prompt | Improved completion/autocomplete commands |
| login-with-openai-codex/ | idea, req, research | OAuth login for OpenAI Codex provider |

### 001-Features / 005-WEB (Web Server)

| Spec | Phases | Description |
|------|--------|-------------|
| web-serve/ | idea..summary | Web-based serving interface |

### 000-Evaluations

| Spec | Phases | Description |
|------|--------|-------------|
| evaluation-terminal-bench/ | idea..plan, summary | Terminal-Bench evaluation harness |
| terminal-bench-evaluations/ | idea, req, research | Terminal bench evaluation (earlier iteration) |

### 004-Research

| Spec | Phases | Description |
|------|--------|-------------|
| code-review-codex/ | idea, req, research | Code review using OpenAI Codex CLI |
| improve-test-coverage/ | idea..summary | Test coverage improvements |
| research-coding-agents-session-log-optimizations/ | idea..summary | Session log error patterns and optimizations |
| rtk-hooks-optimizer/ | idea..plan, summary | RTK output compactor hooks |

### Other Categories

| Category | Contents |
|----------|----------|
| 002-improvements/ | Architecture and gap analysis docs |
| 003-issues/ | Bug fix plans (issues-fix, session-errors) |
| 005-tools/ | Tool specifications (001-a2a-client) |

## Conventions

### Directory structure

```
specs/
├── 001-features/
│   └── 001-SOP/
│       └── plan-command-sop/
│           rough-idea.md
│           requirements.md
│           research/
│           design.md
│           plan.md
│           PROMPT.md
│           summary.md
```

### Naming

- Use kebab-case for folder names
- Number categories: `000-`, `001-`, `002-`, ...
- Number components within features: `000-`, `001-`, ...
- Number research files: `001-`, `002-`, ...
- Keep names descriptive but concise
