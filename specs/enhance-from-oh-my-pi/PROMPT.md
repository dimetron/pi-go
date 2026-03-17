# Enhance pi-go with Model Roles, Git Tools, Subagents, and LSP Integration

## Objective

Implement 4 feature areas in the pi-go coding agent, following the design and plan in `specs/enhance-from-oh-my-pi/`.

## Key Requirements

1. **Model Roles** — Replace `DefaultModel` with `Roles map[string]RoleConfig` in config. 5 roles: default, smol, slow, plan, commit. Config-based mapping. CLI flags `--smol`, `--slow`, `--plan`. Fallback chain: requested role → default → error.

2. **AI Git Commits** — 3 ADK tools: `git-overview` (branch, commits, file status), `git-file-diff` (unified diff per file), `git-hunk` (parsed hunks). Plus `/commit` slash command using commit role model to generate conventional commit messages.

3. **Subagent System** — New `internal/subagent/` package. Process-based: spawn `pi --mode json` subprocesses. 6 types: explore, plan, designer, reviewer, task, quick_task. Concurrency pool (default 5). Git worktree isolation for task/reviewer/designer. Role-based model per type. Streaming events via JSON. `agent` ADK tool for LLM to spawn subagents.

4. **LSP Integration** — New `internal/lsp/` package. JSON-RPC 2.0 client with Content-Length framing over stdio. Manager with on-demand+persistent server lifecycle. 4 languages: Go (gopls), TypeScript (typescript-language-server), Python (pyright), Rust (rust-analyzer). Auto hooks: format-on-write + diagnostics-on-edit after write/edit tools. 5 explicit ADK tools: lsp-diagnostics, lsp-definition, lsp-references, lsp-hover, lsp-symbols.

## Acceptance Criteria

### Model Roles
- Given config with roles, when agent starts, then resolved role model is used
- Given `--smol` flag, then smol role model is used
- Given unknown role, then default role is fallback
- Given no roles configured, then error returned

### Git Tools
- Given repo with changes, when LLM calls git-overview, then receives branch/commits/files
- Given modified file, when LLM calls git-file-diff, then receives unified diff
- Given file with hunks, when LLM calls git-hunk, then receives parsed hunk array
- Given staged changes, when user runs /commit, then conventional commit message generated and committed on approval

### Subagents
- Given agent tool call with type=explore, then pi process spawns with smol model, no worktree
- Given agent tool call with type=task, then pi process spawns with worktree isolation
- Given max concurrent reached, then new spawn blocks until slot available
- Given cancel request, then subagent process killed and resources cleaned

### LSP
- Given .go file edited with error, then diagnostics appended to tool result
- Given .go file written, then auto-formatted by gopls
- Given lsp-definition call on function, then returns file and line of definition
- Given no language server installed, then graceful skip with warning

## Reference

- Design: `specs/enhance-from-oh-my-pi/design.md`
- Plan: `specs/enhance-from-oh-my-pi/plan.md` (14 steps, follow in order)
- Requirements: `specs/enhance-from-oh-my-pi/requirements.md`
- Research: `specs/enhance-from-oh-my-pi/research/gap-analysis.md`

## Constraints

- Go language, builds with `go build ./...`
- Tests pass with `go test ./...`
- Each step must compile and pass tests before proceeding to next
- Git tools use `exec.Command("git", ...)`, not go-git library
- LSP client is custom (no external LSP client library)
- Subagents are separate processes, not in-process ADK multi-agent
- Clean slate: config restructuring OK, no backward compat required
