# Plan: Fix Remaining ISSUES.md Items

## Summary

Fix 3 categories of errors from 30-day log analysis (total ~665 occurrences):
- **P1**: Schema validation errors (454) — additional properties + missing properties rejected before tool runs
- **P2**: Subagent path escaping (11) — worktree sandbox too narrow
- **P3**: Missing required properties (201) — LLMs omit fields that have sensible defaults

## Phase 0: Documentation & Research (COMPLETE)

### Key Findings

**Validation flow** (ADK v0.6.0):
```
coercingTool.Run(ctx, args)
  ├── aliasArgs(m)       — rename aliased keys
  ├── coerceArgs(m)      — string→type coercion
  └── functionTool.Run() — ADK internal
       └── ConvertToWithJSONSchema(m, inputSchema)
            └── resolvedSchema.Validate(m)   ← VALIDATION HAPPENS HERE
```

Coercion runs BEFORE validation. The `lenientSchema()` sets `additionalProperties: {}` (open) on the **top-level** schema only. Nested object schemas (e.g. `TaskItem`, `ChainItem` in subagent) still have `additionalProperties: false` from `jsonschema-go`'s default `falseSchema()`.

**Sandbox flow**: Subagent processes start with `cmd.Dir = worktreePath`. The child `pi` process calls `os.Getwd()` → builds sandbox rooted at worktree. Paths like `../../go.mod` (reaching parent repo) are rejected by `os.Root`'s `openat` syscall.

**Required fields**: `jsonschema-go` marks every struct field without `omitempty` as required. Several fields have runtime defaults but are schema-required (e.g. `LSPPositionInput.Line`, `LSPPositionInput.Column`).

### Allowed APIs / Patterns

| API | Location | Use |
|-----|----------|-----|
| `lenientSchema[T]()` | `registry.go:46-54` | Generate permissive schema from struct |
| `coercingTool` | `registry.go:113-248` | Wraps tool with alias + type coercion |
| `Sandbox.Resolve()` | `sandbox.go:95` | Path validation against sandbox root |
| `spawner.Spawn()` | `subagent/spawner.go:71` | Launches child `pi` process |
| `WorktreeManager.Create()` | `subagent/worktree.go:46` | Creates git worktree |
| `jsonschema.For[T]()` | `jsonschema-go` library | Infers schema from Go struct |

### Anti-patterns to avoid

- Do NOT bypass `os.Root` security — it's the kernel-level sandbox
- Do NOT make ALL fields optional — `file_path`, `command`, `pattern` must stay required
- Do NOT patch ADK source — we only control pi-go code
- Do NOT use `additionalProperties: true` (wrong type) — use `&jsonschema.Schema{}` (empty schema = accept anything)

---

## Phase 1: Recursive Lenient Schema (P1 — 253 "additional properties" errors)

**Goal**: Make `lenientSchema()` recursively set `additionalProperties: {}` on all nested object schemas, not just the top level.

### Files to modify

- `internal/tools/registry.go` — modify `lenientSchema()` function

### Implementation

1. Add a recursive helper `relaxSchema(s *jsonschema.Schema)` that walks the schema tree:
   - Sets `s.AdditionalProperties = &jsonschema.Schema{}` on any object schema
   - Recurses into `s.Properties` values
   - Recurses into `s.Items` (for array items)
   - Handles `s.Definitions` / `s.Defs` if present

2. Call `relaxSchema(schema)` in `lenientSchema()` after `jsonschema.For[T]()`.

### Verification

```bash
go test ./internal/tools/... -run TestLenientSchema -v
# Verify nested schemas (SubagentInput → TaskItem) allow additional properties
```

### Anti-pattern guard

- Do NOT remove `required` arrays here — that's Phase 3
- Do NOT modify `type` constraints — only `additionalProperties`

---

## Phase 2: Subagent Sandbox Root (P2 — 11 "path escapes" errors)

**Goal**: Give subagent processes access to the full repo root, not just their worktree directory.

### Files to modify

- `internal/subagent/spawner.go` — pass repo root as env var
- `internal/cli/cli.go` — read env var to override sandbox root

### Implementation

1. In `spawner.go:Spawn()`, add `PI_SANDBOX_ROOT=<repoRoot>` to the child process environment (line ~108, alongside `cmd.Dir`):
   ```go
   cmd.Env = append(os.Environ(), "PI_SANDBOX_ROOT="+s.RepoRoot)
   ```

2. Add `RepoRoot string` field to `Spawner` struct, populated from `Orchestrator`.

3. In `cli.go`, when building the sandbox (~line 170-178), check for `PI_SANDBOX_ROOT` env var:
   ```go
   sandboxRoot := os.Getenv("PI_SANDBOX_ROOT")
   if sandboxRoot == "" {
       sandboxRoot = cwd
   }
   sandbox, err := tools.NewSandbox(sandboxRoot)
   ```

### Verification

```bash
go test ./internal/subagent/... -v
# Manual: run pi with subagent, verify worktree agent can read ../../go.mod
```

### Anti-pattern guard

- Do NOT change `cmd.Dir` — the working directory should remain the worktree (for git operations)
- Do NOT allow arbitrary paths — only widen sandbox to repo root
- Ensure `PI_SANDBOX_ROOT` is only set for child `pi` processes, not leaked to user bash commands

---

## Phase 3: Reduce Required Fields (P3 — 201 "missing properties" errors)

**Goal**: Add `omitempty` to fields that have runtime defaults, making them optional in the JSON schema.

### Files to modify

- `internal/tools/lsp.go` — LSPPositionInput fields
- `internal/tools/edit.go` — EditInput.NewString
- Improve tool descriptions for remaining required fields

### Implementation

#### 3a. Make LSP position fields optional

In `lsp.go`, change `LSPPositionInput`:
```go
type LSPPositionInput struct {
    File   string `json:"file"`                // stays required
    Line   int    `json:"line,omitempty"`       // was required, default 0 (first line)
    Column int    `json:"column,omitempty"`     // was required, default 0 (first col)
}
```

This is safe because `line=0` and `column=0` are valid positions (first line, first column) and Go's zero value for `int` is `0`.

#### 3b. Make edit new_string optional

In `edit.go`, change `EditInput`:
```go
type EditInput struct {
    FilePath   string `json:"file_path"`
    OldString  string `json:"old_string"`
    NewString  string `json:"new_string,omitempty"`  // optional — empty = delete old_string
    ReplaceAll bool   `json:"replace_all,omitempty"`
}
```

This is safe because the handler already works with empty `new_string` (it replaces `old_string` with nothing = deletion).

#### 3c. Improve tool descriptions

Add explicit notes about required fields in tool descriptions for `edit`, `write`, and LSP tools. Use format like:
```
Required: file_path, old_string
Optional: new_string (default: "" = delete), replace_all (default: false)
```

### Verification

```bash
go test ./internal/tools/... -v
# Verify schema generation no longer marks line/column/new_string as required
# Verify tools still work correctly when these fields are omitted
```

### Anti-pattern guard

- Do NOT make `file_path`, `old_string`, `command`, `pattern`, `query` optional
- Do NOT change `TaskItem.agent`/`TaskItem.task` — those are genuinely required
- Do NOT add `omitempty` to string fields that distinguish "" from missing (like `content` in write)

---

## Phase 4: Validation & Monitoring

### Tests to run

```bash
# Unit tests
go test ./internal/tools/... -v
go test ./internal/subagent/... -v
go test ./internal/cli/... -v

# Full suite
go test ./... -count=1

# E2E (if available)
go test ./... -tags e2e
```

### Post-deployment monitoring

```bash
# Compare error rates after 7 days
find ~/.pi-go/sessions -name "events.jsonl" -mtime -7 | \
  xargs grep -h '"error":"[^"]*"' 2>/dev/null | \
  sed 's/.*"error":"//;s/".*//' | \
  sort | uniq -c | sort -rn
```

### Expected impact

| Error | Before | Expected After | Reduction |
|-------|--------|----------------|-----------|
| unexpected additional properties | 253 | ~20 | ~92% |
| missing properties | 201 | ~80 | ~60% |
| path escapes from parent | 11 | 0 | 100% |
| **Total** | **465** | **~100** | **~78%** |

---

## Execution Order

1. **Phase 1** first — largest impact (253 errors), self-contained change
2. **Phase 3** second — next largest (201 errors), low risk
3. **Phase 2** third — smallest count (11 errors), touches more files
4. **Phase 4** last — verify everything together
