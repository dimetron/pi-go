# Summary: Enhance pi-go with oh-my-pi Features

## Artifacts

| File | Description |
|------|-------------|
| `specs/enhance-from-oh-my-pi/rough-idea.md` | Full oh-my-pi feature inventory (23 features) and project goals |
| `specs/enhance-from-oh-my-pi/requirements.md` | 15 Q&A decisions narrowing scope and architecture |
| `specs/enhance-from-oh-my-pi/research/gap-analysis.md` | Feature-by-feature gap mapping, priority tiers, architecture compatibility |
| `specs/enhance-from-oh-my-pi/design.md` | Detailed design: architecture, components, interfaces, data models, error handling, acceptance criteria, testing strategy |
| `specs/enhance-from-oh-my-pi/plan.md` | 14-step incremental implementation plan with test requirements and demos |
| `specs/enhance-from-oh-my-pi/summary.md` | This file |

## Overview

Enhance pi-go with 4 high-value feature areas from oh-my-pi:

1. **Model Roles** (Steps 1-2) — Config-based role→model routing with 5 roles (default, smol, slow, plan, commit). CLI flags `--smol`/`--slow`/`--plan`. ~250 LOC.

2. **AI Git Commits** (Steps 3-5) — Three ADK tools (git-overview, git-file-diff, git-hunk) for LLM git awareness + `/commit` slash command with conventional commit generation. ~400 LOC.

3. **Subagent System** (Steps 6-9) — Process-based multi-agent with 6 types (explore, plan, designer, reviewer, task, quick_task). Parallel execution (max 5), git worktree isolation by type, role-based model assignment, streaming RPC results. ~850 LOC.

4. **LSP Integration** (Steps 10-13) — JSON-RPC LSP client supporting 4 languages (Go, TS/JS, Python, Rust). Auto format-on-write + diagnostics-on-edit via hooks. 5 explicit tools (diagnostics, definition, references, hover, symbols). ~1,250 LOC.

**Total:** ~2,750 new LOC across 18 files, 2 new packages. ~27% codebase growth.

## Key Decisions

- **Clean slate** — config restructured (roles replace defaultModel), no backward compat constraints
- **Process-based subagents** — separate `pi` processes via `--mode json`, not ADK-native multi-agent
- **Worktree by type** — task/reviewer/designer isolated; explore/plan/quick_task share cwd
- **LSP dual mode** — automatic hooks for write/edit + explicit tools for LLM code intelligence
- **Git via exec** — shell `git` commands, not go-git library (simpler, smaller binary)

## Suggested Next Steps

1. Review and approve the plan
2. Implement via Ralph or manual development following the 14-step plan
3. Each step is independently testable and demoable
