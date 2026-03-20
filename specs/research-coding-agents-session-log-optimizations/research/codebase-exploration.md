# Codebase Exploration: Key Files and Patterns

This document analyzes five core files in the pi-go codebase to understand how the coding agent operates, handles errors, and manages tools.

---

## 1. `internal/agent/agent.go` — SystemInstruction and Agent Configuration

### SystemInstruction Structure (lines 36–112)

The `SystemInstruction` constant (lines 36–112) is a multi-section system prompt with the following structure:

| Section | Lines | Purpose |
|---|---|---|
| **Role definition** | 36–39 | Identifies agent as "pi-go, a coding agent" |
| **Codebase exploration** | 41–56 | Top-down exploration strategy (Orient → Narrow → Read → Trace) |
| **Coding tasks** | 57–76 | Workflow: Understand → Plan → Implement → Verify → Report; coding principles |
| **Context management** | 77–84 | Rules for handling context window pressure |
| **Multi-step tasks** | 85–91 | Vertical slicing strategy |
| **Parallel execution** | 92–99 | When to parallelize independent operations |
| **Internal tools** | 100–103 | Restart tool documentation |
| **Subagents** | 104–112 | Spawning rules (max 5 concurrent), focused task requirements |

### Key Function Signatures

```go
// Config holds agent creation parameters
type Config struct {
    Model               model.LLM
    Tools               []tool.Tool
    Toolsets            []tool.Toolset
    Instruction         string           // overrides SystemInstruction
    SessionService      session.Service  // defaults to in-memory
    BeforeToolCallbacks []BeforeToolCallback
    AfterToolCallbacks  []AfterToolCallback
}

// New creates a new Agent with the given configuration.
func New(cfg Config) (*Agent, error)

// RebuildWithInstruction recreates the agent's runner with a new instruction.
func (a *Agent) RebuildWithInstruction(instruction string) error

// CreateSession creates a new session and returns its ID.
func (a *Agent) CreateSession(ctx context.Context) (string, error)

// Run sends a user message and returns an iterator over agent events.
func (a *Agent) Run(ctx context.Context, sessionID string, userMessage string) iter.Seq2[*session.Event, error]

// RunStreaming sends a user message with SSE streaming enabled.
func (a *Agent) RunStreaming(ctx context.Context, sessionID string, userMessage string) iter.Seq2[*session.Event, error]

// LoadInstruction loads .pi-go/AGENTS.md and appends it to the base instruction.
func LoadInstruction(baseInstruction string) string
```

### Error Handling

- **`New()`**: Wraps errors from `llmagent.New()` and `runner.New()` with context (`fmt.Errorf("creating LLM agent: %w", err)`)
- **`RebuildWithInstruction()`**: Returns error if instruction is empty; wraps creation errors
- **`CreateSession()`**: Wraps session creation errors

### Patterns for Retries/Caching/Recovery

- **No built-in retries**: The agent relies on the ADK runner for retry logic
- **Session reuse**: `RebuildWithInstruction()` reuses the same `sessionService` — existing sessions remain accessible
- **Instruction hot-reload**: `RebuildWithInstruction()` allows changing system prompt without restarting

### Dependencies

- Depends on `google.golang.org/adk/agent`, `adk/agent/llmagent`, `adk/runner`, `adk/session`, `adk/tool`
- Composes with tools via `cfg.Tools` (e.g., `tools.CoreTools(sandbox)`)

---

## 2. `internal/tools/edit.go` — editHandler Function

### Function Signature

```go
type EditInput struct {
    FilePath   string `json:"file_path"`
    OldString  string `json:"old_string"`
    NewString  string `json:"new_string"`
    ReplaceAll bool   `json:"replace_all,omitempty"`
}

type EditOutput struct {
    Path         string `json:"path"`
    Replacements int    `json:"replacements"`
}

func editHandler(sb *Sandbox, input EditInput) (EditOutput, error)
```

### How old_string Matching Works (lines 39–81)

1. **Validation** (lines 40–48):
   - `file_path` must be non-empty
   - `old_string` must be non-empty
   - `old_string` must differ from `new_string`

2. **Read and Count** (lines 50–63):
   - Reads entire file via `sb.ReadFile(input.FilePath)`
   - Uses `strings.Count(content, input.OldString)` to count occurrences
   - If count == 0 → returns error: `"old_string not found in file"`
   - If count > 1 && !ReplaceAll → returns error with count

3. **Replace** (lines 65–71):
   - `ReplaceAll=true` → `strings.ReplaceAll(content, oldString, newString)`
   - Default (first occurrence only) → `strings.Replace(content, oldString, newString, 1)`

4. **Write** (lines 73–80):
   - Writes result with `0o644` permissions
   - Returns `Replacements` count

### Error Handling

| Scenario | Error Message |
|---|---|
| Missing `file_path` | `"file_path is required"` |
| Missing `old_string` | `"old_string is required"` |
| No change | `"old_string and new_string must be different"` |
| `old_string` not found | `"old_string not found in file"` |
| Multiple matches without `ReplaceAll` | `"old_string found N times in file..."` |
| Read/write failure | `"reading file: %w"` or `"writing file: %w"` |

### Patterns for Retries/Caching/Recovery

- **No retries**: Errors are returned immediately; the LLM must retry with a corrected `old_string`
- **Exact matching only**: No fuzzy matching, whitespace normalization, or Levenshtein distance
- **Atomicity**: Write is not atomic (read-modify-write is two steps)

---

## 3. `internal/tools/sandbox.go` — Sandbox Struct and Resolve Method

### Struct Definition (lines 11–14)

```go
type Sandbox struct {
    root *os.Root  // Go 1.24+ secure root handle
    dir  string    // absolute path of the root directory
}
```

### Key Function Signatures

```go
// Constructor
func NewSandbox(dir string) (*Sandbox, error)

// Lifecycle
func (s *Sandbox) Close() error

// Accessors
func (s *Sandbox) FS() fs.FS
func (s *Sandbox) Dir() string

// Path security
func (s *Sandbox) Resolve(name string) (string, error)

// File operations
func (s *Sandbox) ReadFile(name string) ([]byte, error)
func (s *Sandbox) WriteFile(name string, data []byte, perm os.FileMode) error
func (s *Sandbox) Open(name string) (*os.File, error)
func (s *Sandbox) Stat(name string) (os.FileInfo, error)
func (s *Sandbox) ReadDir(name string) ([]os.DirEntry, error)
func (s *Sandbox) MkdirAll(name string, perm os.FileMode) error
```

### Resolve Method — Path Security (lines 44–56)

```go
func (s *Sandbox) Resolve(name string) (string, error) {
    if filepath.IsAbs(name) {
        rel, err := filepath.Rel(s.dir, name)
        if err != nil {
            return "", fmt.Errorf("path %s is outside sandbox %s", name, s.dir)
        }
        return rel, nil
    }
    return name, nil
}
```

**Security model:**
- Uses `os.Root` (Go 1.24+) for enforcement — prevents escape via `..` or symlinks
- `Resolve()` converts absolute paths to relative; relative paths are passed through
- All file operations call `Resolve()` first before using `root.*` methods
- Parent directory creation in `WriteFile()` (lines 74–78) is sandboxed via `s.root.MkdirAll()`

### Error Handling

- `NewSandbox()`: Wraps `filepath.Abs()` and `os.OpenRoot()` errors
- `Resolve()`: Returns error if absolute path is outside sandbox
- File operations: Propagate `os.Root` errors (e.g., file not found, permission denied)
- `WriteFile()`: Handles write errors and close errors separately (lines 84–89)

### Patterns for Retries/Caching/Recovery

- **No retries**: File operations fail immediately on error
- **No caching**: Each `ReadFile()`/`WriteFile()` hits the filesystem
- **WriteFile atomicity**: Writes to temp file then rename would be safer (currently overwrites in place)

---

## 4. `internal/tools/read.go` — readHandler Function

### Function Signature

```go
const defaultReadLimit = 2000 // max lines returned when no limit specified

type ReadInput struct {
    FilePath string `json:"file_path"`
    Offset   int    `json:"offset,omitempty"`  // 1-based, defaults to 1
    Limit    int    `json:"limit,omitempty"`    // 0 means up to 2000 lines
}

type ReadOutput struct {
    Content    string `json:"content"`
    TotalLines int    `json:"total_lines"`
    Truncated  bool   `json:"truncated,omitempty"`
}

func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error)
```

### Behavior (lines 41–96)

1. **Validation** (lines 42–44): Requires `file_path`

2. **Read** (lines 46–52): Reads entire file via `sb.ReadFile()`

3. **Offset handling** (lines 54–61):
   - 1-based indexing: `offset = 1` if not specified or < 1
   - If `offset > totalLines` → returns empty content with `totalLines`

4. **Limit handling** (lines 66–69): `limit <= 0` defaults to 2000

5. **Truncation** (lines 71–86):
   - `truncated` is true if: `endIdx < totalLines` AND `input.Limit <= 0` (i.e., implicit truncation)
   - If explicit `Limit` was given and endIdx < totalLines, `truncated` is false
   - Appends `"... (truncated: showing X of Y lines, use offset/limit to read more)"`

6. **Safety net** (line 89): `truncateOutput(content)` applies 256KB byte-level limit

### Output Format

Each line is prefixed with 6-digit line number and tab:
```
     1	const foo = "bar"
     2	// comment
```

### Error Handling

| Scenario | Error |
|---|---|
| Missing `file_path` | `"file_path is required"` |
| Read failure | `"reading file: %w"` |

### Patterns for Retries/Caching/Recovery

- **No retries**: Single read attempt
- **No caching**: File is read fresh on each call
- **Implicit pagination**: `Truncated` flag and suggestion to use `offset/limit` enables cursor-based iteration

---

## 5. `internal/tools/registry.go` — Tool Coercion and Parameter Handling

### Core Function (lines 14–40)

```go
// CoreTools returns the core coding agent tools as ADK FunctionTools.
func CoreTools(sandbox *Sandbox) ([]tool.Tool, error)
```

**Registered tools:**
1. `newReadTool` — file reading
2. `newWriteTool` — file writing
3. `newEditTool` — string-based editing
4. `newBashTool` — shell commands
5. `newGrepTool` — regex search
6. `newFindTool` — glob-based file finding
7. `newLsTool` — directory listing
8. `newTreeTool` — directory tree
9. `newGitOverviewTool` — git status
10. `newGitFileDiffTool` — file diff
11. `newGitHunkTool` — hunk diff

### Tool Builder Pattern (lines 73–92)

```go
func newTool[TArgs, TResults any](name, description string, handler functiontool.Func[TArgs, TResults]) (tool.Tool, error)
```

**Key behaviors:**
- Generates lenient JSON schema (allows extra properties)
- Wraps with `coercingTool` if schema has int/bool fields

### CoercingTool — String Coercion (lines 94–185)

```go
type coercingTool struct {
    tool.Tool
    intProps  map[string]bool  // fields that should be coerced from string to int
    boolProps map[string]bool  // fields that should be coerced from string to bool
}
```

**Problem solved:** LLMs sometimes send `{"depth": "3"}` instead of `{"depth": 3}`

**Coercion logic** (`coerceArgs`, lines 167–185):
- **Integer fields**: Parse via `strconv.ParseInt()` → convert to `float64` (JSON convention)
- **Boolean fields**: Parse via `strconv.ParseBool()`
- Falls through silently if parsing fails

**Request registration** (`ProcessRequest`, lines 117–150):
- Registers the `coercingTool` (not the inner tool) in `req.Tools`
- Adds function declaration to `req.Config.Tools`

### Lenient Schema (lines 42–52)

```go
func lenientSchema[T any]() *jsonschema.Schema {
    schema, err := jsonschema.For[T](nil)
    if err != nil {
        return nil // fall back to auto-inference
    }
    // Replace strict "additionalProperties: false" with open schema
    schema.AdditionalProperties = &jsonschema.Schema{}
    return schema
}
```

### Error Handling

- `CoreTools()`: Returns first error from any tool builder
- `newTool()`: Wraps functiontool creation errors
- `ProcessRequest()`: Error on duplicate tool registration

### Patterns for Retries/Caching/Recovery

- **No retries**: Tool creation is one-time at startup
- **No caching**: Tools are created fresh on each `CoreTools()` call
- **Recovery**: `ProcessRequest()` checks for duplicate tool names

---

## 6. Supporting: `internal/tools/truncate.go` — Output Truncation

```go
const (
    maxOutputBytes = 256 * 1024  // 256KB
    maxLineLength  = 500         // chars per line
)

func truncateOutput(s string) string
func truncateLine(s string) string
```

Used by:
- `internal/tools/read.go:89`
- `internal/tools/bash.go:75,85,86`
- `internal/tools/git_diff.go:87`
- `internal/tools/tree.go:84`
- `internal/tools/agent.go:101`

---

## Dependency Graph

```
cli.go (line 170-177)
  └── tools.NewSandbox(cwd) → Sandbox
  └── tools.CoreTools(sandbox) → []tool.Tool
        ├── newReadTool → readHandler(sb, input)
        ├── newWriteTool → Sandbox.WriteFile
        ├── newEditTool → editHandler(sb, input)
        ├── newBashTool → ...
        ├── newGrepTool → ...
        ├── newFindTool → ...
        ├── newLsTool → ...
        ├── newTreeTool → ...
        ├── newGitOverviewTool → ...
        ├── newGitFileDiffTool → ...
        └── newGitHunkTool → ...

Sandbox
  ├── Resolve(name) → path security
  ├── ReadFile(name)
  ├── WriteFile(name, data, perm)
  ├── Open(name)
  ├── Stat(name)
  ├── ReadDir(name)
  └── MkdirAll(name, perm)
  All use os.Root for security enforcement

agent.go (line 161-170)
  └── llmagent.New(cfg) ← tools in cfg.Tools
  └── runner.New(cfg) ← session service + llm agent
```

---

## Summary: Error Handling, Retries, and Caching

| Component | Error Handling | Retries | Caching |
|---|---|---|---|
| **agent.go** | Wrapped errors with context | No built-in | Session reuse via `RebuildWithInstruction()` |
| **edit.go** | Immediate errors for all failure modes | None | None |
| **sandbox.go** | Propagates `os.Root` errors | None | None |
| **read.go** | Simple validation + read errors | None | None |
| **registry.go** | Tool creation errors wrapped | None | None |
| **truncate.go** | Not applicable (stateless) | N/A | N/A |

**Key observation:** There is no retry logic anywhere in the tool layer. Tools fail immediately on error, relying on the LLM to retry with corrected parameters. This makes sense for file operations where retrying with the same input will produce the same result.

**Potential optimization opportunities:**
1. `editHandler` could attempt to find "near-miss" `old_string` matches and suggest corrections
2. `readHandler` could cache file contents within a session to avoid re-reading
3. `sandbox.WriteFile` could use atomic rename instead of in-place overwrite
