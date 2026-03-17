# Research: Ralph Orchestrator Plan/Run Pipeline

Source: https://github.com/dimetron/ralph-orchestrator

## Core Architecture

Ralph is an **event-driven multi-agent orchestrator** using specialized roles ("hats"). Each hat is triggered by events and publishes new events. The system runs a loop until `LOOP_COMPLETE`.

## Pipeline: code-assist (default preset)

```
Planner → Builder → Critic → Finalizer → (loop or LOOP_COMPLETE)
```

1. **Planner**: Resolves input into scratchpad, creates `context.md`, `plan.md`, `progress.md`. Decomposes current step into atomic tasks.
2. **Builder**: TDD implementation (RED → GREEN → REFACTOR). One task per iteration.
3. **Critic**: Adversarial review. Re-runs verification. Default is "rejected."
4. **Finalizer**: Checks whole-prompt completion. Decides: advance, fail, or LOOP_COMPLETE.

## Pipeline: pdd-to-code-assist (full idea→code)

Adds pre-planning hats: Inquisitor → Architect → Design Critic → Explorer → Planner → Task Writer → then standard Builder/Critic/Finalizer.

## Presets

| Preset | Hats | Purpose |
|--------|------|---------|
| code-assist.yml | 4 | Default implementation |
| pdd-to-code-assist.yml | 11 | Full idea-to-code |
| research.yml | 2 | Read-only exploration |
| debug.yml | - | Investigation/fix |
| review.yml | - | Code review |

## Key Design Principles

- **One hat per iteration** — deterministic event routing
- **Step-wave discipline** — only current step's tasks exist
- **Adversarial by default** — Critic defaults to rejection
- **Backpressure gates** — fmt/test must pass before progress
- **Confidence protocol** — score 0-100; >80 autonomous

## Relevance to pi-go /plan and /run

**What to adopt:**
- The PDD flow (requirements → research → design → plan → PROMPT.md) is already the SOP we follow
- The code-assist pipeline (plan → build → review → finalize) maps to spawning a "task" subagent
- PROMPT.md as the handoff artifact between /plan and /run

**What to simplify:**
- Ralph has 11 hats for pdd-to-code-assist; pi-go /plan is a single LLM conversation (the PDD SOP)
- Ralph has event-driven hat dispatch; pi-go /run can use the simpler subagent system
- No need for backpressure gates in v1 — the subagent's instruction handles quality
