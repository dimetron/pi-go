# ADK Callback Wiring in pi-go

## Exact Chain: config.json → ADK Registration

```
config.json "hooks" array
    → config.Config.Hooks []HookConfig
    → convertHooks() in cli.go:217
    → extension.BuildBeforeToolCallbacks() / BuildAfterToolCallbacks()
    → []llmagent.BeforeToolCallback / []llmagent.AfterToolCallback
    → agent.New(Config{BeforeToolCallbacks, AfterToolCallbacks})
    → llmagent.New(Config{BeforeToolCallbacks, AfterToolCallbacks})
```

## Callback Signatures

```go
// BeforeToolCallback — can modify args, return modified map
type BeforeToolCallback func(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error)

// AfterToolCallback — can modify result, return modified map
type AfterToolCallback func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error)
```

## How Modification Works

- **BeforeToolCallback**: Return modified `args` map → becomes args for tool execution
- **AfterToolCallback**: Return modified `result` map → becomes result sent to LLM
- **Short-circuit**: Return non-nil result from BeforeToolCallback to skip tool entirely
- **Error**: Return error to abort

## Current Limitation

Shell-based hooks **cannot modify** args or results — they receive JSON on stdin but output is ignored. They always `return args, nil` / `return result, nil`.

## How Multiple Callbacks Compose

Sequential chaining, slice order matters:

```go
// cli.go:217-224
hooks := convertHooks(cfg.Hooks)
beforeCBs := extension.BuildBeforeToolCallbacks(hooks)  // shell hooks first
afterCBs := extension.BuildAfterToolCallbacks(hooks)     // shell hooks first
afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))  // LSP after shell hooks
```

Flow: `Shell hook #1 → Shell hook #2 → ... → LSP hook → Final result`

## LSP Callback: Working Example of Result Modification

```go
// lsp/hooks.go — modifies result map directly
func BuildLSPAfterToolCallback(mgr *Manager) llmagent.AfterToolCallback {
    return func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
        if name == "write" {
            result = formatFile(ctx, mgr, srv, filePath, result)  // adds lsp_formatted
        }
        result = collectDiagnostics(mgr, srv, filePath, result)   // adds lsp_diagnostics
        return result, nil
    }
}
```

## Adding Go-Native Callbacks

**No structural changes needed.** Just append to the callback slices:

```go
// cli.go — after building shell hooks
beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
afterCBs := extension.BuildAfterToolCallbacks(hooks)

// Add Go-native RTK optimizer callbacks
beforeCBs = append(beforeCBs, rtk.BuildCommandRewriteCallback(rtkConfig))
afterCBs = append(afterCBs, rtk.BuildOutputCompactionCallback(rtkConfig))

// Then LSP (runs last)
afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))
```

## Key Files

| File | Lines | What |
|------|-------|------|
| `cli.go` | 217-224 | Hook building and composition |
| `cli.go` | 282-290 | Agent creation with callbacks |
| `cli.go` | 562-574 | `convertHooks()` mapper |
| `agent.go` | 98-122 | Config struct with callback fields |
| `agent.go` | 145-154 | Passed to `llmagent.New()` |
| `extension/hooks.go` | 49-90 | BuildBefore/AfterToolCallbacks |
| `lsp/hooks.go` | 31-82 | LSP callback (modification example) |
