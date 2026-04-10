---
name: discovery
description: Subagent discovery — find and load bundled subagent definitions
role: system
worktree: false
tools: read, ls, find
---

# Subagent Discovery

Bundled subagent definitions live in `internal/subagent/bundled/`. Each `.md` file contains a YAML frontmatter block followed by agent instructions.

## Discovery

The discovery system scans `internal/subagent/bundled/` for `*.md` files, parses the frontmatter, and registers subagents by `name`. Each file's content becomes the agent's system prompt.

## Bundled Subagents

| Name | Role | Description |
|------|------|-------------|
| `explore` | smol | Fast codebase research — grep, tree, find, read |
| `task` | default | Complete coding tasks end-to-end in isolated worktree |
| `worker` | default | General purpose worker for various tasks |
| `quick-task` | default | Small focused tasks with minimal overhead |
| `code-reviewer` | default | Review code for bugs, correctness, and style |
| `spec-reviewer` | default | Review specifications and design documents |
| `plan` | default | Analyze codebase and create detailed implementation plans |
| `designer` | default | Design and modify code in isolated worktree |
| `memory-compressor` | default | Compress tool observations into structured memory entries |

## Frontmatter Schema

```yaml
---
name: <string>        # Unique identifier, used for lookup
description: <string>  # One-line purpose
role: <string>       # "default" | "smol" | "system"
worktree: <bool>     # Whether agent runs in isolated git worktree
tools: <string>      # Comma-separated list of available tools
---
```

## Bundled Directory Structure

```
internal/subagent/
├── bundled/           # Bundled subagent .md definitions
│   ├── code-reviewer.md
│   ├── designer.md
│   ├── explore.md
│   ├── memory-compressor.md
│   ├── plan.md
│   ├── quick-task.md
│   ├── spec-reviewer.md
│   ├── task.md
│   └── worker.md
├── agents.go          # Agent registry and lookup
├── embed.go           # Embeds bundled/ for compile-time inclusion
├── pool.go            # Subagent pool management
├── spawner.go         # Spawns subagent processes
├── orchestrator.go    # Orchestrates multi-agent workflows
├── types.go           # Type definitions
├── environ.go         # Environment setup
└── worktree.go        # Worktree management
```

## Tool Mapping

| Tool | Purpose |
|------|---------|
| `read` | Read file contents with offset/limit |
| `write` | Create or overwrite files |
| `edit` | Replace exact string match in files |
| `bash` | Execute shell commands |
| `grep` | Search file contents with regex |
| `find` | Find files matching glob patterns |
| `tree` | Directory tree with depth control |
| `ls` | List directory contents |
| `git-overview` | Git status, branch, recent commits |

## Loading a Subagent

1. Read the `.md` file from `bundled/`
2. Parse frontmatter for `name`, `role`, `worktree`, `tools`
3. Use the remainder as system prompt content
4. Register in the agent pool with parsed metadata

## Adding a New Bundled Subagent

1. Create `internal/subagent/bundled/<name>.md`
2. Add required frontmatter (`name`, `description`, `role`, `worktree`, `tools`)
3. Write agent instructions below the `---`
4. The discovery system will pick it up on next load

---

## Go

- Module: `github.com/dimetron/pi-go`
- Build: `go build ./...`
- Test: `go test ./...`
- Lint: `golangci-lint run`
- Format: `gofmt -s -w . && goimports -w .`
