# Configuring Skills and Agent Instructions for pi-go

This guide explains how to configure pi-go's extension system — skills, agent instructions, hooks, and subagents — with direct comparisons to the Claude Code `.claude/` setup for readers already familiar with that workflow.

---

## Conceptual Mapping: `.claude/` → `.pi-go/`

| Claude Code concept | File/location | pi-go equivalent | File/location |
|---|---|---|---|
| Global AI instructions | `~/.claude/CLAUDE.md` | Global config + skills | `~/.pi-go/config.json` + `~/.pi-go/skills/` |
| Project AI instructions | `CLAUDE.md` (repo root) or `.claude/CLAUDE.md` | Project agent instructions | `.pi-go/AGENTS.md` |
| Global skills | `~/.claude/skills/<name>/SKILL.md` | Global skills | `~/.pi-go/skills/<name>/SKILL.md` |
| Project skills | `.claude/skills/<name>/SKILL.md` | Project skills | `.pi-go/skills/<name>/SKILL.md` |
| Skill selection index | `~/.claude/INDEX.md` | (bundled `agents-md` skill handles this) | n/a |
| Hooks | `~/.claude/settings.json` → `hooks` | Hooks | `~/.pi-go/config.json` → `hooks` |
| MCP servers | `~/.claude/settings.json` → `mcpServers` | MCP servers | `~/.pi-go/config.json` → `mcp.servers` |
| Subagent types | Agent tool with `subagent_type` | Named agent definitions | `~/.pi-go/agents/<name>.md` or `.pi-go/agents/<name>.md` |

The key structural difference is priority: pi-go loads **bundled → user (`~/.pi-go/`) → project (`.pi-go/`)**, with later layers overriding earlier ones. Claude Code does the same for skills (global → project).

---

## Directory Layout

### Global (`~/.pi-go/`)

```
~/.pi-go/
├── config.json          # model roles, hooks, MCP, guardrails, memory toggles
├── .env                 # API keys (written by `pi login`, never commit this)
├── skills/              # global skills
│   └── <name>/
│       └── SKILL.md
├── agents/              # custom global agent definitions
│   └── <name>.md
├── sessions/            # conversation JSONL logs
├── memory/              # palace.db (Memory Palace), claude-mem.db (observations)
├── sops/
│   └── pdd.md           # custom PDD template (overrides the bundled default)
├── models/              # local embedding models (downloaded via `pi memory model download`)
└── log/
    └── YYYY-MM-DD/      # session logs
```

### Project-local (`.pi-go/` at repo root)

```
.pi-go/
├── AGENTS.md            # project rules injected into agent context at startup
├── config.json          # project-specific overrides (model roles, hooks, etc.)
├── mcp.json             # project MCP server definitions
├── skills/              # project skills (override global skills of the same name)
│   └── <name>/
│       └── SKILL.md
├── agents/              # project agent definitions (override global agents)
│   └── <name>.md
└── sops/                # PDD artifacts (requirements, design, plan, PROMPT.md)
```

Project-local config overrides global config for the same keys. Both files are merged — keys present only in one are preserved.

---

## 1. Project Instructions: `.pi-go/AGENTS.md`

**Claude Code analogy:** `CLAUDE.md` at the repo root.

This file is injected into the agent's context at startup. Put everything here that a developer or AI needs to know about the project before touching code.

The file already exists at `.pi-go/AGENTS.md` in this repo. A well-structured version covers:

```markdown
# Project Rules

## Architecture
One-paragraph summary of what this repo is.

## Package Layout
| Package | Purpose |
|---------|---------|
| `cmd/pi` | Entry point |
| `internal/agent` | ADK agent setup |
...

## Conventions
- ADK interfaces: use native model.LLM, tool.Tool — never custom wrappers
- Error wrapping: fmt.Errorf("context: %w", err) always
- No init() functions

## Do NOT
- Add multi-module structure
- Import internal/ from outside the module
- Add CGO dependencies
```

**Tip:** Keep AGENTS.md factual and brief. It is loaded on every session. Link to longer docs (`ARCHITECTURE.md`, `docs/`) rather than inlining them.

---

## 2. Skills

Skills are Markdown instruction files that are injected into the agent's system prompt when invoked. They teach the agent how to perform specific tasks.

### File Format

```
.pi-go/skills/
└── <skill-name>/
    └── SKILL.md
```

A skill file has YAML frontmatter followed by a Markdown instruction body:

```markdown
---
name: pi-rebuild
description: Rebuild and reinstall the pi binary after code changes.
tools: bash
---

# Pi Rebuild

Rebuild and reinstall the pi binary from source, then restart.

## Steps

1. Run linters: `golangci-lint run ./...`
2. If linters pass: `go build ./cmd/pi && go install ./cmd/pi/`
3. If build succeeds, call the `restart` tool.
4. On any failure, show full error output — do not restart.
```

### Frontmatter Fields

| Field | Required | Description |
|---|---|---|
| `name` | No (defaults to directory name) | Skill identifier, used for `/name` invocation |
| `description` | Yes | One-line description shown in `/skills` list |
| `tools` | No | Comma-separated tool allowlist for this skill |

### Discovery Order (lowest → highest priority)

1. **Bundled** — compiled into the `pi` binary (`internal/extension/bundled_skills/`)
2. **User** — `~/.pi-go/skills/<name>/SKILL.md`
3. **Project** — `.pi-go/skills/<name>/SKILL.md`

Project skills override user skills of the same name. User skills override bundled skills.

**Pi-go also reads `.claude/skills/` and `.cursor/skills/`** when walking up from the working directory — existing Claude Code skills work in pi-go without copying.

### Claude Code Skill Format Comparison

Claude Code skills use the same subdirectory + `SKILL.md` pattern. The frontmatter fields differ slightly:

| Field | Claude Code | pi-go |
|---|---|---|
| `name` | Yes | Yes |
| `description` | Yes | Yes |
| `tools` | No (not applicable) | Yes — restricts which tools the skill may call |
| `metadata.type` | Yes (`user`, `feedback`, etc.) | No |

### Invoking Skills

```
/pi-rebuild        # invoke by name in TUI
/skills            # list all available skills
pi audit           # scan all skill files for hidden-character threats
```

### Security: Audit System

Pi-go scans every skill file for hidden Unicode characters (BiDi attacks, zero-width characters) before loading. Skills with critical findings are **blocked** and logged. Run `pi audit` to inspect findings or `pi audit --strip` to auto-remove dangerous characters.

There is no equivalent audit in Claude Code — pi-go's audit system is unique to it.

---

## 3. Project Skills for pi-go

Suggested project-specific skills to create under `.pi-go/skills/`:

### `code-review-go`

```markdown
---
name: code-review-go
description: Review Go code changes for correctness, style, and ADK interface compliance.
tools: read, grep, git-file-diff, git-hunk
---

# Go Code Review for pi-go

## Checklist

- ADK interfaces used directly (model.LLM, tool.Tool) — no custom wrappers
- Errors wrapped with fmt.Errorf("context: %w", err)
- No init() functions
- No CGO imports or build tags
- Single go.mod module — no sub-modules
- Tests in *_test.go; E2E tests behind `//go:build e2e`
- Tool registration via tool.NewFunctionTool in tools.CoreTools()
```

### `design-review`

```markdown
---
name: design-review
description: Review a proposed feature design against ADK patterns and pi-go architecture.
tools: read, grep, find
---

# Design Review

Check the proposed design against:
- Does it fit the existing Init→Update→View (TUI) or agent→tool→callback (core) patterns?
- Does it introduce new external dependencies? (Must be pure Go, no CGO)
- Does it add a new tool? Register in tools.CoreTools(), add to DESCRIPTION.md tool table.
- Does it add a new subagent type? Define in internal/subagent/bundled/ or .pi-go/agents/.
```

---

## 4. Hooks

Hooks run shell commands before or after tool calls. They are configured in `config.json`, not in skill files.

**Claude Code analogy:** `~/.claude/settings.json` → `hooks` array.

### Global hooks: `~/.pi-go/config.json`

```json
{
  "hooks": [
    {
      "event": "after_tool",
      "tool": "write",
      "command": "gofmt -w {{.Path}}"
    },
    {
      "event": "after_tool",
      "tool": "edit",
      "command": "goimports -w {{.Path}}"
    },
    {
      "event": "before_tool",
      "tool": "bash",
      "command": "echo '[pi-go] running: {{.Command}}' >> ~/.pi-go/log/bash-audit.log"
    }
  ]
}
```

### Hook Fields

| Field | Description |
|---|---|
| `event` | `before_tool` or `after_tool` |
| `tool` | Tool name to match (e.g., `write`, `edit`, `bash`) |
| `command` | Shell command; tool args passed as JSON on stdin |

The LSP integration uses `after_tool` hooks internally: after `write` or `edit`, gopls formats the file and collects diagnostics automatically.

---

## 5. Subagent Definitions

This is the feature with no direct Claude Code equivalent. Pi-go can spawn child `pi` processes as specialized agents, each with a defined role, model, and tool set.

### Bundled Agent Types

These ship with the binary and are always available:

| Name | Role | Worktree | Purpose |
|---|---|---|---|
| `explore` | smol | No | Fast read-only codebase research |
| `plan` | plan | No | Architecture and design analysis |
| `designer` | slow | Yes | Code creation in isolated worktree |
| `task` | default | Yes | End-to-end coding tasks |
| `quick-task` | smol | No | Small focused tasks |
| `worker` | default | No | Background processing |
| `code-reviewer` | slow | No | Code review |
| `spec-reviewer` | slow | No | Design document review |
| `memory-compressor` | smol | No | Observation compression (internal) |
| `discovery` | smol | No | Agent discovery / capability enumeration |
| `claude` | — | No | ACP bridge to Claude Code CLI |
| `cursor` | — | No | ACP bridge to Cursor CLI |
| `gemini` | — | No | ACP bridge to Gemini CLI |

### Custom Agent Definitions

Create custom agents at:
- `~/.pi-go/agents/<name>.md` (user-level)
- `.pi-go/agents/<name>.md` (project-level, overrides user)

Format matches the bundled agents:

```markdown
---
name: security-auditor
description: Audit Go code for OWASP vulnerabilities and pi-go-specific security patterns.
role: slow
worktree: false
tools: read, grep, find, git-file-diff
---

You are a security auditor specializing in Go. Your job is to find security issues, not fix them.

## Focus Areas

- Input validation at system boundaries (user input, external APIs)
- Path traversal vulnerabilities in file operations
- Command injection in bash tool usage
- SQL injection in SQLite queries (modernc.org/sqlite)
- Credential handling — no secrets in logs or tool args
- Audit log completeness for all tool calls

## Output

Return findings as a structured list:
- Severity: Critical / High / Medium / Low
- Location: file:line
- Issue: one sentence
- Evidence: the relevant code snippet
```

### Invoking Agents

```
# From TUI slash command
/agents            # list running agents

# From agent tool call (the agent calls this automatically when needed)
agent(type="explore", prompt="find all places that call os.Root")
agent(type="task", prompt="fix the nil pointer in internal/session/store.go:142")
```

---

## 6. MCP Servers

**Claude Code analogy:** `~/.claude/settings.json` → `mcpServers`.

Configure in `~/.pi-go/config.json` (global) or `.pi-go/mcp.json` (project).

```json
{
  "mcp": {
    "servers": [
      {
        "name": "filesystem",
        "transport": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
      },
      {
        "name": "github",
        "transport": "http",
        "url": "https://api.githubcopilot.com/mcp/"
      }
    ]
  }
}
```

An example is at `.pi-go/mcp-example.json`.

---

## 7. Complete Config Schema

`~/.pi-go/config.json` (global) or `.pi-go/config.json` (project):

```json
{
  "roles": {
    "default": { "model": "claude-sonnet-4-20250514" },
    "smol":    { "model": "claude-haiku-3-20240307" },
    "plan":    { "model": "claude-sonnet-4-20250514" },
    "slow":    { "model": "claude-opus-4-20250514" }
  },
  "hooks": [],
  "mcp": { "servers": [] },
  "guardrail": { "max_daily_tokens": 0 },
  "memory": { "enabled": true },
  "palace": { "enabled": true },
  "compactor": { "enabled": true }
}
```

---

## 8. Recommended Setup for pi-go Development

If you are working on pi-go itself with pi-go as your coding agent, this is the minimal recommended configuration:

### `.pi-go/skills/pi-rebuild/SKILL.md`
Already exists — rebuild and reinstall after code changes.

### `.pi-go/skills/check-linters/SKILL.md`
Already exists as `check-linters-before-commit`.

### `.pi-go/skills/code-guidelines-go/SKILL.md`
Already exists — Go conventions specific to this codebase.

### `.pi-go/agents/security-auditor.md`
Create per the example above for security-focused review passes.

### `~/.pi-go/config.json` — format on write
```json
{
  "hooks": [
    {
      "event": "after_tool",
      "tool": "write",
      "command": "gofmt -w {{.Path}} 2>/dev/null; goimports -w {{.Path}} 2>/dev/null; true"
    }
  ]
}
```

---

## Summary

| Task | What to create | Where |
|---|---|---|
| Project rules / architecture | `AGENTS.md` | `.pi-go/AGENTS.md` |
| Teach agent a workflow | Skill `<name>/SKILL.md` | `.pi-go/skills/<name>/SKILL.md` |
| Reuse Claude Code skills | Nothing — auto-discovered | `.claude/skills/` (already read) |
| Run shell commands on tool events | `hooks` in config | `.pi-go/config.json` |
| Define a new subagent type | `<name>.md` | `.pi-go/agents/<name>.md` |
| Add an external tool server | `mcp.servers` in config | `.pi-go/mcp.json` |
| Global defaults (model, limits) | `config.json` | `~/.pi-go/config.json` |
