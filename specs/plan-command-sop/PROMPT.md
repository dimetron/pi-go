# Add /plan and /run Slash Commands with PDD SOP Workflow

## Objective

Implement two new slash commands in pi-go that form a native plan→run pipeline: `/plan <idea>` drives an interactive PDD session producing structured spec artifacts and a PROMPT.md, while `/run <spec-name>` executes that PROMPT.md via an isolated task subagent with gate validation and auto-merge. Full design and plan in `specs/plan-command-sop/`.

## Key Requirements

1. **PDD SOP Loader** — New `internal/sop/` package. Embedded default PDD SOP constant with file override support (project `.pi-go/sops/pdd.md` → global `~/.pi-go/sops/pdd.md` → embedded). Uses `go:embed` or string constant for default.

2. **`/plan <idea>` Command** — New `internal/tui/plan.go`. Derives kebab-case task name from idea. Creates `specs/{task_name}/` skeleton (rough-idea.md, requirements.md, research/). Loads PDD SOP, injects as system instruction with task context. Clears conversation and sends rough idea as first message. LLM drives the multi-turn PDD flow (requirements → research → design → plan → PROMPT.md). Uses "default" role with full tool access. Artifacts written incrementally via agent's write/edit tools.

3. **`/run <spec-name>` Command** — New `internal/tui/run.go`. Reads `specs/<spec-name>/PROMPT.md`. Parses `## Gates` section for validation commands. Spawns "task" type subagent via Orchestrator with worktree isolation and PROMPT.md content as prompt. Streams all events (text_delta, tool_call, tool_result) to TUI in real-time. Agent updates plan.md checklist (`[ ]` → `[x]`) as steps complete.

4. **Gate Validation & Auto-merge** — On agent completion, runs each gate command sequentially in the worktree via `exec.Command`. If all pass, auto-merges worktree branch to current branch. If gates fail, re-spawns agent with failure context (max 3 retries). If retries exhausted, leaves worktree intact for manual inspection. No gates section = merge directly.

5. **TUI Integration** — Register `/plan` and `/run` in slash command dispatch and autocomplete array. Update `/help` text. Handle new message types for /run streaming and gate results in `Update()`.

## Acceptance Criteria

### /plan Command
- Given user types `/plan add rate limiting to API`, when command executes, then `specs/add-rate-limiting-to-api/` is created with rough-idea.md and requirements.md
- Given `/plan` starts, when LLM drives PDD conversation, then requirements.md is updated after each Q&A exchange
- Given PDD SOP completes all phases, when done, then PROMPT.md exists with Objective, Key Requirements, Acceptance Criteria, Gates, Reference, and Constraints sections
- Given `.pi-go/sops/pdd.md` exists in project, when `/plan` runs, then custom SOP is used instead of embedded default
- Given `specs/{task_name}/` already exists, when `/plan` invoked, then error shown without clearing conversation

### /run Command
- Given valid spec-name with PROMPT.md, when `/run spec-name` executes, then task subagent spawns in isolated worktree
- Given running subagent, when events stream, then TUI shows text deltas, tool calls, and tool results in real-time
- Given agent completes a plan step, when step finished, then plan.md checklist item is marked `[x]`
- Given agent completes and gates pass, then worktree branch is auto-merged to current branch
- Given gate fails with retries remaining, then agent re-spawns with failure context in same worktree
- Given gate fails 3 times, then worktree is left intact and failure is reported with path
- Given no `## Gates` section in PROMPT.md, then merge proceeds without validation
- Given `specs/<spec-name>/PROMPT.md` does not exist, then error shown with list of available specs

### Integration
- Given `/plan` produces PROMPT.md, when user runs `/run` with same spec-name, then full pipeline works end-to-end

## Gates

- **build**: `go build ./...`
- **test**: `go test ./...`
- **vet**: `go vet ./...`

## Reference

- Design: `specs/plan-command-sop/design.md` (architecture, components, interfaces, error handling)
- Plan: `specs/plan-command-sop/plan.md` (9 steps, follow in order)
- Requirements: `specs/plan-command-sop/requirements.md` (15 Q&A decisions)
- Research: `specs/plan-command-sop/research/` (TUI architecture, subagent system, ralph-orchestrator, PROMPT.md template)

## Constraints

- Go language, builds with `go build ./...`
- Tests pass with `go test ./...`
- Each step must compile and pass tests before proceeding to next
- No new external dependencies — uses existing subagent/orchestrator system
- PDD SOP embedded as Go constant, overridable via file
- Gate commands executed via `exec.Command` in worktree directory
- Subagent spawned as "task" type via existing Orchestrator
