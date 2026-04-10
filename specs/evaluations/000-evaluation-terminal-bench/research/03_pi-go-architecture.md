# Research: pi-go Architecture for Terminal Execution

## Core Components

### Agent (internal/agent/agent.go)

- Uses Google ADK (Agent Development Kit) for agent loop
- Wraps llmagent.New() with model, tools, instruction
- Session management via session.Service
- Run() returns iterator over session.Event

### Tools

Key tools in `internal/tools/`:
- **bash.go** - Execute shell commands with timeout
- **read.go** - Read file contents
- **write.go** / **edit.go** - File modification
- **grep.go**, **find.go**, **ls.go** - File exploration
- **sandbox.go** - Container/sandbox management

### Bash Tool (internal/tools/bash.go)

```go
type BashInput struct {
    Command string  // shell command
    Timeout int     // ms, default 120000, max 600000
}

type BashOutput struct {
    Stdout   string
    Stderr   string
    ExitCode int
}
```

- Executes via `exec.CommandContext` with bash -c
- Respects timeout limits (max 10 minutes)
- Returns exit code, truncated output

### Subagents (internal/subagent/)

- AgentConfig with name, description, role, tools, timeout
- Markdown-based agent definitions (frontmatter + body)
- DiscoverAgents() loads from bundled/user/project directories
- Spawner creates subprocess with own context

## Integration Points for Harbor

To integrate pi-go with Harbor:

1. **Python wrapper** - Create a Python package that wraps pi-go CLI
2. **BaseAgent implementation** - Implement run(), setup(), create_run_agent_commands()
3. **Command translation** - Convert ExecInput commands to pi-go tool calls
4. **Result parsing** - Convert pi-go output to AgentContext/ATIF format

## Alternative: Standalone Runner

Instead of Harbor integration, could build:
- Custom task loader (reads instruction.md, Dockerfile)
- Docker container manager (builds task containers)
- pi-go process spawner (runs pi with instruction)
- Result verifier (runs test.sh, parses exit code)

## Files to Reference

- `internal/agent/agent.go` - Main agent logic
- `internal/tools/bash.go` - Command execution
- `internal/subagent/agents.go` - Subagent system
- `internal/tools/sandbox.go` - Sandboxing