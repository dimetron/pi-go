# Skills Subagents

Implement a skills-based subagent system in pi-go, inspired by [pi-superpowers-plus](https://github.com/coctostan/pi-superpowers-plus/tree/main).

## Core Idea

Replace the current hardcoded agent types (explore, plan, designer, reviewer, task, quick_task) with a discoverable, markdown-defined agent system. Agent profiles are markdown files with YAML frontmatter that specify name, description, tools, model, and a system prompt body.

## Key Features

1. **Markdown Agent Definitions** — Agent profiles as `.md` files with YAML frontmatter (`name`, `description`, `tools`, `model`) and system prompt body
2. **Agent Discovery** — Load agents from multiple locations with priority override:
   - Bundled agents (built-in defaults, lowest priority)
   - User agents (`~/.pi-go/agents/`, medium priority)
   - Project agents (`.pi-go/agents/`, highest priority)
3. **Three Execution Modes:**
   - **Single:** `{agent: "name", task: "..."}` — one agent, one task
   - **Parallel:** `{tasks: [{agent, task}, ...]}` — concurrent execution with concurrency control
   - **Chain:** `{chain: [{agent, task}, ...]}` — sequential with `{previous}` placeholder for piping output
4. **Enhanced Process Management:**
   - Inactivity timeout (120s default)
   - Absolute timeout (10min default, configurable)
   - Environment filtering for subprocess isolation
   - Process lifecycle tracking with cleanup on exit
5. **Backward Compatibility** — Existing hardcoded types become bundled agent definitions
6. **TUI Integration** — Live streaming of subagent events for all three modes

## Source Reference

- Repository: https://github.com/coctostan/pi-superpowers-plus
- Key files:
  - `extensions/subagent/index.ts` — Main subagent tool (1124 lines)
  - `extensions/subagent/agents.ts` — Agent discovery and configuration
  - `extensions/subagent/concurrency.ts` — Semaphore-based concurrency
  - `extensions/subagent/lifecycle.ts` — Process tracking
  - `extensions/subagent/timeout.ts` — Timeout configuration
  - `extensions/subagent/env.ts` — Environment filtering
  - `agents/` — 4 bundled agent definitions (implementer, worker, code-reviewer, spec-reviewer)
  - `skills/subagent-driven-development/SKILL.md` — Workflow skill
  - `skills/dispatching-parallel-agents/SKILL.md` — Parallel dispatch skill

## Current pi-go State

- Subagent system exists at `internal/subagent/` with orchestrator, pool, worktree manager, spawner
- 6 hardcoded agent types in Go map
- Single execution mode only
- Agent tool at `internal/tools/agent.go`
- TUI already streams subagent events
- Skill system with YAML frontmatter markdown files already in place
