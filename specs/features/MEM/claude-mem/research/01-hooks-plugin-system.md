# Research: pi-go Hook/Plugin System

## Hook System

**No Claude-style named lifecycle events** (SessionStart, PostToolUse, etc.) exist. The hook system uses two generic events mapped to ADK callbacks:

### Hook Definition (`internal/extension/hooks.go`)
```go
type HookConfig struct {
    Event   string   `json:"event"`    // "before_tool" or "after_tool"
    Command string   `json:"command"`  // shell command via "sh -c"
    Tools   []string `json:"tools,omitempty"` // optional tool name filter
    Timeout int      `json:"timeout,omitempty"` // seconds, default 10
}
```

### Data Passed to Hooks
- `runHookCommand` executes shell command with JSON on stdin: `{"tool": toolName, "data": data}`
- `before_tool` receives `args map[string]any` (tool call arguments)
- `after_tool` receives `result map[string]any` (tool output)
- **NOT passed**: session ID, cwd, or tool input to after_tool

### ADK Callback Signatures
```go
BeforeToolCallback: func(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error)
AfterToolCallback:  func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error)
```

### Registration Chain
```
config.Load() -> cfg.Hooks -> convertHooks() -> extension.BuildBeforeToolCallbacks()
                                              -> extension.BuildAfterToolCallbacks()
                                              -> agent.New(Config{BeforeToolCallbacks, AfterToolCallbacks})
```

### Current AfterToolCallbacks (cli.go:264-271)
1. Shell hooks from `extension.BuildAfterToolCallbacks(hooks)`
2. LSP callback from `lsp.BuildLSPAfterToolCallback(lspMgr)`
3. Compactor callback from `tools.BuildCompactorCallback(...)`

## Plugin-like Extension Points (internal/extension/)

1. **Shell hooks** (`hooks.go`) — before_tool / after_tool shell scripts
2. **MCP toolsets** (`mcp.go`) — External MCP servers as subprocesses
3. **Skills** (`skills.go`) — SKILL.md files with YAML frontmatter

No formal plugin registry or dynamic loading exists.

## Observation Capture Integration Point

A new PostToolUse observation callback would slot in as an additional `AfterToolCallback`. The LSP hook at `internal/lsp/hooks.go` is the best pattern to follow.

## Context Injection

SessionStart does NOT inject context into the agent. Context injection happens through:
1. **AGENTS.md** — `agent.LoadInstruction()` reads `.pi-go/AGENTS.md` → system prompt
2. **Skills** — listed in system instruction as `/name: description`
3. **CWD** — appended to instruction
4. **`RebuildWithInstruction()`** — can change system instruction mid-session
