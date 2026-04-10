# Summary: /plan and /run Commands with PDD SOP Workflow

## Artifacts

| File | Description |
|------|-------------|
| `specs/plan-command-sop/rough-idea.md` | Original rough idea and reference to ralph-orchestrator |
| `specs/plan-command-sop/requirements.md` | 15 Q&A decisions defining command behavior |
| `specs/plan-command-sop/research/tui-slash-commands.md` | TUI slash command architecture analysis |
| `specs/plan-command-sop/research/subagent-system.md` | Subagent/orchestrator system analysis |
| `specs/plan-command-sop/research/ralph-orchestrator.md` | Ralph-orchestrator pipeline analysis |
| `specs/plan-command-sop/research/prompt-md-template.md` | PROMPT.md template pattern extraction |
| `specs/plan-command-sop/design.md` | Detailed design with architecture, components, acceptance criteria |
| `specs/plan-command-sop/plan.md` | 9-step incremental implementation plan |
| `specs/plan-command-sop/summary.md` | This file |
| `specs/plan-command-sop/PROMPT.md` | Autonomous execution prompt for /run |

## Overview

Two new slash commands forming pi-go's native plan→run pipeline:

1. **`/plan <idea>`** (Steps 1-3) — Injects PDD SOP as system instruction, LLM drives multi-turn conversation through requirements → research → design → plan → PROMPT.md. Creates `specs/{task_name}/` skeleton upfront, updates incrementally. Discovers project build/test commands and embeds them in PROMPT.md's `## Gates` section. ~300 LOC across 3 new files.

2. **`/run <spec-name>`** (Steps 4-7) — Reads `specs/<spec-name>/PROMPT.md`, spawns task subagent with worktree isolation, streams full output to TUI. Updates plan.md checklist as steps complete. Runs gate commands before merge. Auto-retries (max 3) on gate failure. Auto-merges on success. ~400 LOC across 2 new files.

3. **Integration** (Steps 8-9) — TUI registration, autocomplete, help text, E2E tests. ~100 LOC.

**Total:** ~800 new LOC across 6 new files, 1 new package (`internal/sop/`).

## Key Decisions

- **LLM-driven /plan** — PDD SOP as system instruction, no custom TUI state machine
- **Embedded SOP with override** — ships built-in, customizable via `.pi-go/sops/pdd.md`
- **Subagent execution** — /run uses existing orchestrator with "task" agent type + worktree
- **Auto-discovered gates** — build/test commands found during /plan, embedded in PROMPT.md
- **Auto-merge with gates** — validates before merge, retries on failure
