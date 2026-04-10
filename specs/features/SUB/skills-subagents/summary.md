# Summary: Skills Subagents

## Artifacts

| File | Description |
|------|-------------|
| `rough-idea.md` | Initial concept with source references and gap analysis |
| `requirements.md` | 15 Q&A decisions defining scope and constraints |
| `research/01-pi-superpowers-plus-architecture.md` | Source repo analysis (agent format, subagent tool, 3 modes) |
| `research/02-pi-go-subagent-internals.md` | Existing system deep dive (orchestrator, spawner, pool, TUI) |
| `research/03-parallel-sequential-orchestration.md` | Execution model analysis (ADK sequential, internal parallelism) |
| `research/04-tui-pipeline-visualization.md` | TUI rendering capabilities and pipeline design proposals |
| `design.md` | Full design: architecture, interfaces, data models, 25 acceptance criteria |
| `plan.md` | 10-step incremental implementation plan with TDD |
| `summary.md` | This file |

## Overview

Transform pi-go's hardcoded 6-agent-type system into a discoverable, markdown-defined agent architecture with three execution modes and visual pipeline TUI.

### Key Changes

- **8 bundled agents** as embedded markdown files (explore, plan, designer, task, quick-task, worker, code-reviewer, spec-reviewer)
- **3-tier discovery**: project > user > bundled, with `agentScope` per call
- **`subagent` tool** replacing `agent` tool with single/parallel/chain modes
- **Pipeline visualization** using box-drawing for parallel (side-by-side) and chain (arrows) modes
- **Process isolation**: env filtering, inactivity timeout (120s), absolute timeout (10min)
- **Concurrency**: pool of 6 (configurable), max 8 parallel tasks per call

### Files Affected

| Action | Count | Key Files |
|--------|-------|-----------|
| New | 13 | agents.go, bundled/*.md, embed.go, env.go, timeout.go, subagent.go, pipeline.go |
| Modified | 5 | orchestrator.go, spawner.go, pool.go, tui.go, cli.go |
| Deleted | 1 | agent.go |

## Suggested Next Steps

1. **Review artifacts** — ensure design and plan align with expectations
2. **Implementation** — follow the 10-step plan in `plan.md`
3. **Ralph integration** — optionally create PROMPT.md for autonomous execution
