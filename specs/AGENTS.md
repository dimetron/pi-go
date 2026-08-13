# Specs — Feature Design Documents

This directory contains Plan-Driven Design (PDD) specs for pi-go features.
Each spec follows a phased workflow from rough idea through implementation.

## Organization

```
specs/
├── evaluations/          # Benchmark/evaluation specs
├── features/             # Feature implementations by component
│   ├── COD/             # Coding, testing & audit
│   ├── LLM/             # Language model research
│   ├── MEM/             # Memory system
│   ├── SOP/             # Standard Operating Procedures
│   ├── SUB/             # Subagents
│   ├── TST/             # Testing & audit
│   ├── TUI/             # Terminal UI
│   └── WEB/             # Web server
├── issues/               # Issue tracking/fixes
├── memory-fixes/         # Memory subsystem repair
├── research/             # Architecture improvements
├── sessio-errors/        # Session error analysis
└── tools/                # Tool specifications
```

## Phases

| Phase           | File                         | Description                                   |
|-----------------|------------------------------|-----------------------------------------------|
| 1. Idea         | `rough-idea.md`              | Initial concept, motivation, and source       |
| 2. Requirements | `requirements.md`            | Acceptance criteria and constraints           |
| 3. Research     | `research/`                  | Codebase exploration, gap analysis, prior art |
| 4. Design       | `design.md`                  | Architecture, data flow, interfaces           |
| 5. Plan         | `plan.md`                    | Step-by-step implementation tasks             |
| 6. Prompt       | `PROMPT.md`                  | Agent prompt to execute the plan              |
| 7. Summary      | `summary.md` or `SUMMARY.md` | Artifacts created, outcome, lessons learned   |

## Spec Index

### features/COD (Coding & Improvements)

| Spec                                                  | Phases        | Description                                  |
|-------------------------------------------------------|---------------|----------------------------------------------|
| 000-improve-test-coverage/                            | idea..summary | Test coverage improvements                   |
| 001-code-review-codex/                                | prompt        | Code review using OpenAI Codex CLI           |
| 002-research-coding-agents-session-log-optimizations/ | idea..summary | Session log error patterns and optimizations |

### features/LLM (Language Model Research)

| Spec            | Phases   | Description                                      |
|-----------------|----------|--------------------------------------------------|
| 000-openai-sdk/ | research | OpenAI SDK vs responses/completions API research |

### features/MEM (Memory)

| Spec        | Phases        | Description                               |
|-------------|---------------|-------------------------------------------|
| claude-mem/ | idea..summary | Native claude-mem implementation in pi-go |

### features/SOP (Standard Operating Procedures)

| Spec                   | Phases        | Description                                       |
|------------------------|---------------|---------------------------------------------------|
| enhance-from-oh-my-pi/ | idea..summary | Enhance pi-go with oh-my-pi features              |
| plan-command-sop/      | idea..summary | `/plan` and `/run` commands with PDD SOP workflow |
| plan-resume/           | plan          | Resume interrupted plan execution                 |

### features/SUB (Subagents)

| Spec                      | Phases        | Description                  |
|---------------------------|---------------|------------------------------|
| skills-subagents/         | idea..summary | Skills-based subagent system |
| subagent-execution-modes/ | prompt        | Subagent execution modes     |

### features/TOO (Tool Specifications)

| Spec                            | Phases        | Description                                                                                 |
|---------------------------------|---------------|---------------------------------------------------------------------------------------------|
| 000-mcp-support/                | prompt        | MCP (Model Context Protocol) support                                                        |
| 001-a2a-client/                 | idea..summary | A2A client tool specification                                                               |
| 002-context-references/         | idea..summary | Context reference resolution for tools                                                      |
| 003-large-files/                | design        | Large file handling strategy                                                                |
| 004-acp-subagent/               | idea..summary | ACP-based subagent tool specification                                                       |
| 005-otel-fixes/                 | idea..summary | OpenTelemetry instrumentation fixes for tools                                               |
| 017-canopy-embedded-code-index/ | idea..prompt  | Canopy as an embedded structural code index; replaces LSP navigation, keeps LSP diagnostics |

### features/TST (Testing & Audit)

| Spec                                  | Phases              | Description                                       |
|---------------------------------------|---------------------|---------------------------------------------------|
| atif-support/                         | idea..summary       | ATIF (Agent Trajectory Interchange Format) export |
| simple-ollama-test/                   | idea..summary       | E2E test with actual Ollama provider              |
| skill-commands-list-create-load-pull/ | idea, req, research | Skill CRUD commands                               |
| skills-audit/                         | idea..summary       | Security audit for SKILL.md files                 |

### features/TUI (Terminal UI)

| Spec                        | Phases              | Description                               |
|-----------------------------|---------------------|-------------------------------------------|
| better-completion-commands/ | idea..prompt        | Improved completion/autocomplete commands |
| login-with-openai-codex/    | idea, req, research | OAuth login for OpenAI Codex provider     |
| nanocoder-tui/              | idea..summary       | TUI design patterns from nanocoder        |
| 001-make-main-chat-always-open-and-for-each-task/ | idea..prompt | Main chat always open; per-task queue drains as follow-up turns |

### features/WEB (Web Server)

| Spec       | Phases        | Description                 |
|------------|---------------|-----------------------------|
| web-serve/ | idea..summary | Web-based serving interface |

### evaluations/

| Spec                           | Phases        | Description                       |
|--------------------------------|---------------|-----------------------------------|
| 000-evaluation-terminal-bench/ | idea..summary | Terminal-Bench evaluation harness |

### issues/

| Spec                      | Phases | Description                                                                |
|---------------------------|--------|----------------------------------------------------------------------------|
| 000-issues-fix/           | plan   | General issue fixes                                                        |
| 001-session-errors/       | prompt | Session error analysis                                                     |
| 002-code-review-codex/    | prompt | Code review with Codex                                                     |
| 003-code-review-pi/       | prompt | Code review for pi-go                                                      |
| 004-memory-dimentions/    | readme | Memory dimensions research                                                 |
| 007-write-tool-data-loss/ | readme | `write`/`edit` truncate before writing and never fsync — data loss on quit |
| 008-adk-utils-go-review/  | research | adk-utils-go feature comparison & scored recommendations                  |
| 009-tui-review-followups/ | readme | Deferred follow-ups from the TUI review and the PR #134 review           |

### memory-fixes/

| Spec           | Phases       | Description                                                                     |
|----------------|--------------|---------------------------------------------------------------------------------|
| memory-fixes/  | idea..prompt | Repair the memory subsystem: dead after-tool callback chain, worker lifecycle, palace paths, TUI wiring |

### research/

| Spec                     | Phases        | Description                        |
|--------------------------|---------------|------------------------------------|
| 000-rtk-hooks-optimizer/ | idea..summary | RTK output compactor hooks         |
| 003-improvements/        | research      | Architecture and gap analysis docs |
| 007-codex-context-compaction/ | research | Expose upstream Codex app-server context compaction through `internal/codex/` |

### tools/

| Spec            | Phases        | Description                   |
|-----------------|---------------|-------------------------------|
| 001-a2a-client/ | idea..summary | A2A client tool specification |

### sessio-errors/

| Spec      | Phases | Description                   |
|-----------|--------|-------------------------------|
| PROMPT.md | prompt | Session error analysis prompt |

## Conventions

### Directory structure

```
specs/
├── features/
│   └── SOP/
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
- Number folders within categories: `000-`, `001-`, `002-`, ...
- Keep names descriptive but concise
- Phase files use specific names: `rough-idea.md`, `requirements.md`, `design.md`, `plan.md`, `PROMPT.md`, `summary.md`
  or `SUMMARY.md`
