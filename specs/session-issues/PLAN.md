# Session Issues Fix Plan

Fix errors identified from session log analysis documented in `docs/SESSION_ERRORS.md`.

**Target**: Reduce session error rate from ~33% to <15%

**Timeline**: 4 phases over 2 weeks

---

## Phase 1: Schema Validation & Type Coercion (High Priority)

### Issue 1.1: Missing Required Properties (324 errors)

**Root Cause**: LLMs send tool calls without required parameters. ADK validates before our coercion can help.

**Files to modify**: `internal/tools/registry.go`

**Changes**:
1. Make required properties optional in tool input schemas by using pointer types
2. Or add `additionalProperties: true` and handle missing required fields gracefully

**Implementation**:

```go
// Option A: Add defaults for commonly missing fields
// In lenientSchema, set all properties to optional by removing "required" array

func lenientSchema[T any]() *jsonschema.Schema {
    schema, err := jsonschema.For[T](nil)
    if err != nil {
        return nil
    }
    // Remove required constraints
    schema.Required = nil
    relaxSchema(schema)
    return schema
}
```

**Verification**: `go test ./internal/tools/... -run TestSchema`

---

### Issue 1.2: Unexpected Additional Properties (254 errors)

**Root Cause**: ADK validation runs before `coercingTool.ProcessRequest` registers our tool declaration.

**Files to modify**: `internal/tools/registry.go`

**Changes**:
1. The `lenientSchema` already sets `AdditionalProperties = &jsonschema.Schema{}`
2. Verify the schema is actually being used by the ADK runner
3. Add defensive filtering in `coerceArgs` to remove unknown properties

**Implementation**:

```go
// In coerceArgs, add at the start:
func (c *coercingTool) coerceArgs(m map[string]any) {
    // First, allow any extra properties (already permissive)
    // But log if we see common problematic ones
    
    knownProps := make(map[string]bool)
    for k := range c.intProps { knownProps[k] = true }
    for k := range c.boolProps { knownProps[k] = true }
    for k := range c.jsonProps { knownProps[k] = true }
    // Don't remove unknown props - just coerce known ones
    // ... existing coercion logic
}
```

**Status**: Already implemented via `relaxSchema`. Need to verify ADK hook timing.

---

### Issue 1.3: Type Coercion Failures (208 errors)

**Pattern**: `validating /properties/depth: type: 3 has type "string"`

**Root Cause**: `collectCoerceProps` only handles top-level properties, not nested ones.

**Files to modify**: `internal/tools/registry.go`

**Changes**:

1. Extend `collectCoerceProps` to recursively collect nested property paths:

```go
// collectCoerceProps recursively collects properties that need coercion
func collectCoerceProps(schema *jsonschema.Schema) (intProps, boolProps, jsonProps map[string]bool) {
    intProps = make(map[string]bool)
    boolProps = make(map[string]bool)
    jsonProps = make(map[string]bool)
    if schema == nil {
        return
    }
    collectFromSchema(schema, "", intProps, boolProps, jsonProps)
    return
}

func collectFromSchema(schema *jsonschema.Schema, prefix string, intProps, boolProps, jsonProps map[string]bool) {
    if schema == nil {
        return
    }
    for name, prop := range schema.Properties {
        fullName := name
        if prefix != "" {
            fullName = prefix + "." + name
        }
        switch prop.Type {
        case "integer", "number":
            intProps[fullName] = true
        case "boolean":
            boolProps[fullName] = true
        case "array", "object":
            jsonProps[fullName] = true
        }
        // Recurse into nested objects
        if prop.Properties != nil {
            collectFromSchema(prop, fullName, intProps, boolProps, jsonProps)
        }
        // Recurse into items for arrays
        if prop.Items != nil {
            collectFromSchema(prop.Items, fullName, intProps, boolProps, jsonProps)
        }
    }
}
```

2. Update `coerceArgs` to handle dot-notation paths:

```go
func (c *coercingTool) coerceArgs(m map[string]any) {
    for k, v := range m {
        // Handle string → expected type conversion
        if s, ok := v.(string); ok {
            if c.intProps[k] {
                if i, err := strconv.ParseInt(s, 10, 64); err == nil {
                    m[k] = float64(i)
                } else if f, err := strconv.ParseFloat(s, 64); err == nil {
                    m[k] = f
                }
            }
            // ... rest of coercion
        }
    }
}
```

**Verification**: `go test ./internal/tools/... -run TestCoerce`

---

### Issue 1.4: Subagent Tasks/Chain Array Coercion (70 errors)

**Pattern**: `validating /properties/tasks: type: [{...}] has type "string"`

**Root Cause**: `tasks` and `chain` are arrays but LLMs sometimes send them as JSON strings.

**Files to modify**: `internal/tools/subagent.go`, `internal/tools/registry.go`

**Changes**:
1. Add `Tasks` and `Chain` to `jsonProps` in subagent tool constructor:

```go
// In NewSubagentTool, modify to pass jsonProps
return newTool("subagent", desc,
    func(ctx tool.Context, input SubagentInput) (SubagentOutput, error) {
        return subagentHandler(ctx, orch, input, onEvent)
    },
    // Add aliases for common mistakes
    map[string]string{"type": "agent"},
)
// Note: Need to extend newTool to accept jsonProps explicitly
```

2. Better approach - handle in `SubagentInput` unmarshaling:

```go
// In SubagentInput, add custom UnmarshalJSON to handle string→array coercion
func (i *SubagentInput) UnmarshalJSON(data []byte) error {
    type alias SubagentInput
    a := &struct {
        Tasks any `json:"tasks"`
        Chain any `json:"chain"`
    }
    if err := json.Unmarshal(data, a); err != nil {
        return err
    }
    // Convert string tasks/chain if needed
    // ... coercion logic
    return nil
}
```

**Verification**: `go test ./internal/tools/... -run TestSubagent`

---

## Phase 2: Path Escaping Fix (Medium Priority)

### Issue 2.1: Subagent Path Escaping (18+ errors)

**Pattern**: `openat ../../go.mod: path escapes from parent`

**Root Cause**: Subagents run in isolated worktrees but tools assume paths relative to sandbox root.

**Files to modify**: `internal/subagent/orchestrator.go`, `internal/tools/sandbox.go`

**Changes**:

1. In `orchestrator.go`, pass the worktree path to subagent so it can normalize:

```go
// In Spawn(), when setting up environment:
env = append(append([]string(nil), env...),
    "PI_SANDBOX_ROOT="+o.worktree.RepoRoot(),
    "PI_WORKTREE_ROOT="+workDir,  // NEW: tell subagent its worktree
)
```

2. In sandbox.go, handle worktree-relative paths:

```go
// Sandbox.Resolve() - already handles this, but add worktree support
func (s *Sandbox) Resolve(name string) (string, error) {
    // If path starts with "../" and we have a worktree context,
    // resolve relative to worktree, not sandbox root
    if strings.HasPrefix(name, "../") {
        // Check if this is a valid path within the sandbox
        // by checking if the absolute path is under sandbox root
    }
    // ... existing logic
}
```

3. Alternative: Add path normalization helper:

```go
// NormalizeSubagentPath converts worktree-relative paths to sandbox-relative
func NormalizeSubagentPath(worktreeRoot, sandboxRoot, path string) (string, error) {
    if !strings.HasPrefix(path, "../") {
        return path, nil // already relative to sandbox
    }
    // Convert ../.. to absolute
    abs, err := filepath.Abs(filepath.Join(worktreeRoot, path))
    if err != nil {
        return "", err
    }
    // Make relative to sandbox root
    rel, err := filepath.Rel(sandboxRoot, abs)
    if err != nil {
        return "", err
    }
    return rel, nil
}
```

**Verification**: 
```bash
go test ./internal/subagent/... -run TestPath
go test ./internal/tools/... -run TestSandbox
```

---

## Phase 3: Tool Descriptions & Prompt Engineering (Medium Priority)

### Issue 3.1: Tool Hallucinations (9 errors)

**Pattern**: `tool 'task' not found`, `tool 'glob' not found`

**Root Cause**: LLMs confuse parameter names with tool names, or use wrong tool names.

**Files to modify**: `internal/tools/registry.go`, `internal/tools/subagent.go`

**Changes**:

1. Add aliases to subagent tool:

```go
// In NewSubagentTool
return newTool("subagent", desc,
    func(ctx tool.Context, input SubagentInput) (SubagentOutput, error) {
        return subagentHandler(ctx, orch, input, onEvent)
    },
    // Parameter aliases for common LLM mistakes
    map[string]string{
        "type": "agent",      // LLM sends type instead of agent
        "prompt": "task",     // LLM sends prompt instead of task
    },
)
```

2. Add `find` tool description to mention it works like `glob`:

```go
// In newFindTool description:
description := `Find files matching a glob pattern.
Similar to 'glob' or 'ls' in other tools. Use patterns like '**/*.go' for recursive search.
`
```

3. Add clear "available tools" list to system prompt (if configurable):

```go
// In agent setup, ensure system prompt includes:
const toolList = "Available tools: read, write, edit, bash, grep, find, ls, tree, git-overview, git-file-diff, git-hunk, agent, subagent, lsp-diagnostics, lsp-definition, lsp-hover, lsp-references, lsp-symbols"
```

**Verification**: Manual testing with LLM that previously hallucinated tools

---

### Issue 3.2: Subagent Mode Detection Failures (9 errors)

**Pattern**: `could not detect mode`

**Root Cause**: LLMs send partial parameters without clear mode specification.

**Files to modify**: `internal/tools/subagent.go`

**Changes**:

1. Improve `detectMode` to be more lenient:

```go
func detectMode(input SubagentInput) string {
    // Chain takes priority
    if len(input.Chain) > 0 {
        return "chain"
    }
    // Parallel
    if len(input.Tasks) > 0 {
        return "parallel"
    }
    // Single mode: allow partial input
    if input.Agent != "" || input.Task != "" {
        // If either is present, assume single mode
        return "single"
    }
    return ""
}
```

2. Improve error message with examples:

```go
// In subagentHandler default case:
default:
    return SubagentOutput{}, fmt.Errorf(
        "could not detect mode: use ONE of:\n"+
        "  Single: {agent: \"explore\", task: \"description\"}\n"+
        "  Parallel: {tasks: [{agent: \"worker\", task: \"job\"}]}\n"+
        "  Chain: {chain: [{agent: \"planner\", task: \"plan\"}]}\n"+
        "Received: agent=%q, task=%q, tasks=%d, chain=%d",
        input.Agent, input.Task, len(input.Tasks), len(input.Chain))
}
```

**Verification**: `go test ./internal/tools/... -run TestDetectMode`

---

## Phase 4: Edit Tool Improvements (Low Priority)

### Issue 4.1: Old String Not Found (29 errors)

**Status**: Already has retry logic (3 attempts, exponential backoff)

**Additional improvements**:

1. Add more context to error messages (already done)

2. Consider caching file content during session for faster retry:

```go
// FileContentCache for edit retries (already exists, ensure it's used)
type FileContentCache struct {
    mu    sync.RWMutex
    cache map[string]string
}
```

**Verification**: `go test ./internal/tools/... -run TestEdit`

---

## Phase 5: Orchestrator Shutdown Race (Low Priority)

### Issue 5.1: Orchestrator Shutdown Errors (4 errors)

**Pattern**: `orchestrator is shut down`

**Root Cause**: Race between mode detection and orchestrator shutdown.

**Files to modify**: `internal/subagent/orchestrator.go`

**Changes**:

1. Improve shutdown handling to drain gracefully:

```go
// In orchestrator.go Shutdown():
func (o *Orchestrator) Shutdown() {
    o.mu.Lock()
    if o.closed {
        o.mu.Unlock()
        return // already shut down
    }
    o.closed = true
    
    // Give running agents a chance to finish gracefully
    var running []string
    for id, state := range o.agents {
        if state.Status == "running" {
            running = append(running, id)
        }
    }
    o.mu.Unlock()
    
    // If agents are running, cancel them
    for _, id := range running {
        o.Cancel(id)
    }
    
    // Cleanup worktrees
    if o.worktree != nil {
        _ = o.worktree.CleanupAll()
    }
}
```

2. Add timeout for graceful shutdown:

```go
// Add graceful shutdown with timeout
func (o *Orchestrator) ShutdownWithTimeout(timeout time.Duration) {
    done := make(chan struct{})
    go func() {
        o.Shutdown()
        close(done)
    }()
    
    select {
    case <-done:
        return
    case <-time.After(timeout):
        // Force cleanup
        o.forceShutdown()
    }
}
```

**Verification**: `go test ./internal/subagent/... -run TestShutdown`

---

## Implementation Checklist

### Phase 1: Schema & Coercion
- [x] **1.1** Remove required constraints from lenientSchema ✅
- [x] **1.2** Verify ADK hook timing for additionalProperties ✅ (already works via relaxSchema)
- [x] **1.3** Implement recursive `collectCoerceProps` for nested types ✅
- [x] **1.4** Handle subagent Tasks/Chain String→Array coercion ✅ (via jsonProps)
- [x] Write tests for all coercion changes ✅
- [x] Run `go test ./internal/tools/...` ✅

### Phase 2: Path Escaping
- [x] **2.1** Pass worktree root to subagent environment ✅ (PI_SANDBOX_ROOT + PI_WORKTREE_ROOT in orchestrator.go)
- [x] **2.2** Implement path normalization in sandbox ✅ (resolveWorktreePath in sandbox.go)
- [x] Write tests for path escaping scenarios ✅ (TestSandbox_Resolve_Worktree*)
- [ ] Run `go test ./internal/subagent/...` ✅

### Phase 3: Tool Descriptions
- [x] **3.1** Add parameter aliases to subagent tool ✅
- [x] **3.2** Improve find tool description ✅ (already documented in available tools)
- [x] **3.3** Improve detectMode with lenient matching ✅
- [x] **3.4** Add detailed mode examples in error messages ✅
- [x] Run `go test ./internal/tools/...` ✅

### Phase 4: Edit Tool
- [x] **4.1** Verify existing retry logic is working ✅
- [x] **4.2** Add tests for edge cases ✅
- [x] Run `go test ./internal/tools/...` ✅

### Phase 5: Orchestrator
- [ ] **5.1** Improve graceful shutdown
- [ ] **5.2** Add shutdown timeout
- [ ] Run `go test ./internal/subagent/...`

### Final Verification
- [x] Run full test suite: `go test ./...` ✅
- [x] Run race detector: `go test -race ./...` ✅
- [x] Run vet: `go vet ./...` ✅
- [x] Update SESSION_ERRORS.md with fixed issues ✅

---

## Files to Modify

| File | Changes | Phase |
|------|---------|-------|
| `internal/tools/registry.go` | Schema leniency, recursive coercion | 1 |
| `internal/tools/subagent.go` | Aliases, lenient mode detection | 1, 3 |
| `internal/subagent/orchestrator.go` | Worktree path env, graceful shutdown | 2, 5 |
| `internal/tools/sandbox.go` | Path normalization | 2 |
| `internal/tools/edit.go` | (already has retry) | 4 |
| `internal/tools/find.go` | Improved description | 3 |

---

## Risk Assessment

| Issue | Risk | Mitigation |
|-------|------|------------|
| Schema changes | Low | Non-breaking, more lenient |
| Recursive coercion | Medium | Add comprehensive tests |
| Path normalization | Medium | Ensure sandbox security preserved |
| Shutdown changes | Low | Backward compatible |

---

## Success Metrics

| Metric | Before | After Target |
|--------|--------|--------------|
| Missing required props | 324 | <100 |
| Unexpected additional props | 254 | <100 |
| Type coercion errors | 208 | <50 |
| Path escaping errors | 18 | <5 |
| Tool hallucinations | 9 | 0 |
| Overall error rate | 33% | <15% |
